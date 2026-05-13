package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mainline-org/mainline/internal/domain"
)

// sealedIntentTouching is a focused setup helper: produces a sealed
// intent whose fingerprint records the given files. Returns the
// sealed intent id; the test caller then queries trace / context
// against it.
func sealedIntentTouching(t *testing.T, dir string, svc *Service, branchSuffix string, files []string, summaryTitle string) string {
	t.Helper()
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "checkout", "-b", "feature/"+branchSuffix)
	start, err := svc.Start("ctx test "+branchSuffix, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, f := range files {
		writeFile(t, dir, f, "// "+f+"\n")
	}
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "ctx test "+branchSuffix)
	if _, err := svc.Append("did the work"); err != nil {
		t.Fatalf("append: %v", err)
	}
	sr := validSealResult(start.IntentID)
	sr.Summary.Title = summaryTitle
	sr.Fingerprint.FilesTouched = files
	sr.Fingerprint.Subsystems = subsystemsFromFiles(files)
	data, _ := json.Marshal(sr)
	if _, err := svc.SealSubmit(json.RawMessage(data)); err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	return start.IntentID
}

func TestContextRetrieval_FilesModeMatchesByPath(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	want := sealedIntentTouching(t, dir, svc, "auth-jwt",
		[]string{"src/auth/middleware.go", "src/auth/jwt.go"},
		"Move auth from session to JWT")
	_ = sealedIntentTouching(t, dir, svc, "billing",
		[]string{"src/billing/charges.go"},
		"Refactor billing fees")

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "files",
		Files: []string{"src/auth/middleware.go"},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.RelevantIntents) == 0 {
		t.Fatalf("expected at least one relevant intent for src/auth/middleware.go")
	}
	if res.RelevantIntents[0].IntentID != want {
		t.Fatalf("expected file-match intent %s ranked first, got %s",
			want, res.RelevantIntents[0].IntentID)
	}
	// Look for "touched" reason — file overlap is the strongest
	// signal and should always show up in reasons when it fires.
	found := false
	for _, r := range res.RelevantIntents[0].Relevance.Reasons {
		if strings.Contains(r, "touched") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'touched' reason in: %v", res.RelevantIntents[0].Relevance.Reasons)
	}
}

func TestContextRetrieval_QueryModeMatchesKeywords(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	want := sealedIntentTouching(t, dir, svc, "kw-test",
		[]string{"src/auth/middleware.go"},
		"Refactor authentication to JWT")
	_ = sealedIntentTouching(t, dir, svc, "kw-other",
		[]string{"src/billing/charges.go"},
		"Update billing")

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "query",
		Query: "JWT authentication refresh tokens",
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.RelevantIntents) == 0 {
		t.Fatalf("expected query match for 'JWT authentication'")
	}
	if res.RelevantIntents[0].IntentID != want {
		t.Errorf("expected keyword-match intent %s ranked first, got %s",
			want, res.RelevantIntents[0].IntentID)
	}
}

func TestContextRetrieval_QueryModeScoresOnlyOpenRisks(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	sealedAt := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	view := &domain.MainlineView{
		SchemaVersion: 1,
		MainBranch:    "main",
		RebuiltAt:     time.Now().UTC().Format(time.RFC3339),
		Intents: []domain.IntentView{
			{
				IntentID:      "int_resolved_risk",
				Status:        domain.StatusMerged,
				ActorID:       "agent",
				SealedAt:      sealedAt,
				ViewRebuiltAt: time.Now().UTC().Format(time.RFC3339),
				Summary: &domain.IntentSummary{
					Title: "Unrelated resolved work",
					What:  "No matching content here.",
				},
				Fingerprint: &domain.SemanticFingerprint{FilesTouched: []string{"src/old.go"}},
			},
			{
				IntentID:      "int_open_risk",
				Status:        domain.StatusMerged,
				ActorID:       "agent",
				SealedAt:      sealedAt,
				ViewRebuiltAt: time.Now().UTC().Format(time.RFC3339),
				Summary: &domain.IntentSummary{
					Title: "Unrelated open work",
					What:  "No matching content here either.",
				},
				Fingerprint: &domain.SemanticFingerprint{FilesTouched: []string{"src/new.go"}},
			},
		},
		Risks: []domain.Risk{
			{
				ID:           "risk_aaa",
				Text:         "rollback corruption on old clients",
				SourceIntent: "int_resolved_risk",
				OpenedAt:     sealedAt,
			},
			{
				ID:           "risk_bbb",
				Text:         "rollback corruption on new clients",
				SourceIntent: "int_open_risk",
				OpenedAt:     sealedAt,
			},
		},
		RiskResolutions: map[string][]domain.RiskResolution{
			"risk_aaa": {{IntentID: "int_fix", Rationale: "fixed"}},
		},
	}
	if err := svc.Store.WriteMainlineView(view); err != nil {
		t.Fatalf("write view: %v", err)
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "query",
		Query: "rollback corruption",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	seen := map[string]ContextRelevant{}
	for _, ri := range res.RelevantIntents {
		seen[ri.IntentID] = ri
	}
	if _, ok := seen["int_resolved_risk"]; ok {
		t.Fatalf("resolved risk text should not make an intent relevant: %+v", res.RelevantIntents)
	}
	open, ok := seen["int_open_risk"]
	if !ok {
		t.Fatalf("open risk text should still make the intent relevant: %+v", res.RelevantIntents)
	}
	if open.Relevance.Breakdown == nil || open.Relevance.Breakdown.Risk == 0 {
		t.Fatalf("open risk should carry risk relevance breakdown, got %+v", open.Relevance)
	}
}

func TestContextRetrieval_OutputCompactWithLimits(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	for i := 0; i < 8; i++ {
		sealedIntentTouching(t, dir, svc,
			"fan-"+randomTestString(3),
			[]string{"src/shared/util.go"},
			"shared util change")
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "files",
		Files: []string{"src/shared/util.go"},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	// Default limit caps at ContextRetrievalDefaultLimit.
	if len(res.RelevantIntents) > ContextRetrievalDefaultLimit {
		t.Fatalf("default limit %d exceeded: got %d intents",
			ContextRetrievalDefaultLimit, len(res.RelevantIntents))
	}
	// Each entry exposes follow-up commands so the agent can drill
	// into the full record without re-deriving the command shape.
	for _, ri := range res.RelevantIntents {
		if ri.Followups["show"] == "" || ri.Followups["trace"] == "" {
			t.Errorf("intent %s missing show/trace followups", ri.IntentID)
		}
	}
}

func TestContextRetrieval_GuidanceAlwaysPresent(t *testing.T) {
	// Guidance is the rc7 honest-signal: "use these as historical
	// context, not as a replacement for reading current code".
	// Must always appear — even on empty results — so the agent
	// internalises the contract.
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "query",
		Query: "this string matches absolutely nothing useful zzzqqq",
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.Notes) < 2 {
		t.Fatalf("expected >=2 retrieval notes (use-and-verify), got %v", res.Notes)
	}
	gtext := strings.Join(res.Notes, " ")
	if !strings.Contains(gtext, "Verify") && !strings.Contains(gtext, "verify") {
		t.Errorf("retrieval notes must remind the agent to verify against current code: %v", res.Notes)
	}
}

func TestContextRetrieval_AbandonedIntentVisibleButPenalised(t *testing.T) {
	// Abandoned intents are valuable signal: "this approach was
	// tried; don't repeat it". They should appear in retrieval when
	// the file/keyword overlap is strong enough — but rank below
	// merged intents of comparable score.
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	mergedID := sealedIntentTouching(t, dir, svc, "merged-path",
		[]string{"src/shared/x.go"}, "merged approach")
	abandonedID := sealedIntentTouching(t, dir, svc, "abandoned-path",
		[]string{"src/shared/x.go"}, "abandoned approach")
	if _, err := svc.Abandon(abandonedID, "approach didn't work"); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "files",
		Files: []string{"src/shared/x.go"},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	var mergedScore, abandonedScore float64
	abandonedSeen := false
	for _, ri := range res.RelevantIntents {
		switch ri.IntentID {
		case mergedID:
			mergedScore = ri.Relevance.Score
		case abandonedID:
			abandonedScore = ri.Relevance.Score
			abandonedSeen = true
			if ri.Status != string(domain.StatusAbandoned) {
				t.Errorf("expected abandoned status in retrieval output, got %s", ri.Status)
			}
		}
	}
	if !abandonedSeen {
		t.Fatalf("abandoned intent should still appear in retrieval (signal: 'this was tried')")
	}
	if abandonedScore >= mergedScore {
		t.Errorf("abandoned intent should rank below same-relevance merged: merged=%.2f abandoned=%.2f",
			mergedScore, abandonedScore)
	}
}

func TestContextRetrieval_DraftingIntentsExcluded(t *testing.T) {
	// Drafting intents have no sealed fingerprint — they are
	// in-progress and shouldn't be surfaced as historical context.
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	gitCmd(t, dir, "checkout", "-b", "feature/in-progress")
	draftID := mustStart(t, svc, "still drafting")

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "query",
		Query: "still drafting",
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	for _, ri := range res.RelevantIntents {
		if ri.IntentID == draftID {
			t.Fatalf("drafting intent %s should not appear in retrieval", draftID)
		}
	}
}

func TestContextRetrieval_BadModeRejected(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	_, err := svc.RetrieveContext(ContextRetrievalRequest{Mode: "nonsense"})
	if err == nil {
		t.Fatalf("expected mode-validation error, got nil")
	}
	if !strings.Contains(err.Error(), "current") || !strings.Contains(err.Error(), "files") || !strings.Contains(err.Error(), "query") {
		t.Errorf("error should suggest the three supported modes, got: %v", err)
	}
}

func mustStart(t *testing.T, svc *Service, goal string) string {
	t.Helper()
	r, err := svc.Start(goal, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return r.IntentID
}

// -----------------------------------------------------------
// Step 2: legacy signals + retrieval status + guidance + ranking
// -----------------------------------------------------------

func TestContextRetrieval_LegacyAntiPatternsDoNotPropagateInDefaultContext(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	view := &domain.MainlineView{
		SchemaVersion: 1,
		MainBranch:    "main",
		RebuiltAt:     "2026-04-29T00:00:00Z",
		Intents: []domain.IntentView{{
			IntentID:      "int_legacy_ap",
			Status:        domain.StatusMerged,
			ActorID:       "agent",
			Thread:        "feature/auth",
			GitBranch:     "feature/auth",
			Goal:          "legacy anti-pattern context test",
			SealedAt:      "2026-04-28T00:00:00Z",
			ViewRebuiltAt: "2026-04-29T00:00:00Z",
			Summary: &domain.IntentSummary{
				Title: "Auth migration with legacy anti-patterns",
				What:  "Changed auth middleware.",
				Why:   "OAuth callback needed compatibility.",
				AntiPatterns: []domain.AntiPattern{{
					What:     "Removing legacy session middleware on /oauth path",
					Why:      "OAuth callback handler still requires session state",
					Severity: "high",
				}},
			},
			Fingerprint: &domain.SemanticFingerprint{
				FilesTouched: []string{"src/auth/middleware.go"},
				Subsystems:   []string{"auth"},
			},
		}},
	}
	if err := svc.Store.WriteMainlineView(view); err != nil {
		t.Fatalf("write view: %v", err)
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "files",
		Files: []string{"src/auth/middleware.go"},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	var found *ContextRelevant
	for i := range res.RelevantIntents {
		if res.RelevantIntents[i].IntentID == "int_legacy_ap" {
			found = &res.RelevantIntents[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected legacy intent in retrieval")
	}
	if len(found.AntiPatterns) != 0 {
		t.Fatalf("legacy anti_patterns must not be promoted into default context, got %+v", found.AntiPatterns)
	}
	if len(res.InheritedConstraints) != 0 {
		t.Fatalf("legacy anti_patterns must not become inherited constraints, got %+v", res.InheritedConstraints)
	}
}

func TestContextRetrieval_QueryModeDoesNotScoreLegacyAntiPatterns(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	view := &domain.MainlineView{
		SchemaVersion: 1,
		MainBranch:    "main",
		RebuiltAt:     "2026-04-29T00:00:00Z",
		Intents: []domain.IntentView{
			{
				IntentID:      "int_terminology",
				Status:        domain.StatusMerged,
				ActorID:       "agent",
				Thread:        "docs/terminology",
				GitBranch:     "docs/terminology",
				Goal:          "standardise user-facing terminology",
				SealedAt:      "2026-04-28T00:00:00Z",
				ViewRebuiltAt: "2026-04-29T00:00:00Z",
				Summary: &domain.IntentSummary{
					Title: "Terminology cleanup",
					What:  "Keep user-facing copy consistent.",
					Why:   "Reader feedback showed internal vocabulary leaking into docs.",
					AntiPatterns: []domain.AntiPattern{
						{
							What:     "Reintroducing 'managed block' or 'Mainline template' in CLI output, help text, README, or AGENTS.md",
							Why:      "Distinct vocabularies for distinct audiences was the whole point of the rewrite.",
							Severity: "medium",
						},
					},
				},
				Fingerprint: &domain.SemanticFingerprint{
					FilesTouched: []string{"AGENTS.md"},
					Subsystems:   []string{"docs"},
				},
			},
		},
	}
	if err := svc.Store.WriteMainlineView(view); err != nil {
		t.Fatalf("write view: %v", err)
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "query",
		Query: "managed block Mainline template",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.RelevantIntents) != 0 {
		t.Fatalf("legacy anti_pattern-only matches should not drive default context, got %+v", res.RelevantIntents)
	}
}

func TestContextRetrieval_QueryModeDropsRecentWeakSignalsFromResults(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	recent := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	old := time.Now().Add(-60 * 24 * time.Hour).UTC().Format(time.RFC3339)
	view := &domain.MainlineView{
		SchemaVersion: 1,
		MainBranch:    "main",
		RebuiltAt:     time.Now().UTC().Format(time.RFC3339),
		Intents: []domain.IntentView{
			{
				IntentID:      "int_keyword",
				Status:        domain.StatusMerged,
				ActorID:       "agent",
				Goal:          "column export",
				SealedAt:      old,
				ViewRebuiltAt: time.Now().UTC().Format(time.RFC3339),
				Summary:       &domain.IntentSummary{Title: "Column export"},
				Fingerprint:   &domain.SemanticFingerprint{FilesTouched: []string{"src/export.go"}},
			},
			{
				IntentID:      "int_recent_context",
				Status:        domain.StatusMerged,
				ActorID:       "agent",
				Goal:          "billing boundary",
				SealedAt:      recent,
				ViewRebuiltAt: time.Now().UTC().Format(time.RFC3339),
				Summary:       &domain.IntentSummary{Title: "Billing boundary"},
				Fingerprint:   &domain.SemanticFingerprint{FilesTouched: []string{"src/billing.go"}},
			},
		},
	}
	if err := svc.Store.WriteMainlineView(view); err != nil {
		t.Fatalf("write view: %v", err)
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "query",
		Query: "column",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	ids := map[string]bool{}
	for _, ri := range res.RelevantIntents {
		ids[ri.IntentID] = true
	}
	if !ids["int_keyword"] {
		t.Fatalf("query mode should keep content matches, got %+v", res.RelevantIntents)
	}
	if ids["int_recent_context"] {
		t.Fatalf("query mode should not return recency-only weak signals, got %+v", res.RelevantIntents)
	}
}

// Status classification: a sealed intent older than staleAge with no
// supersede / abandon mark should classify as "stale" and carry the
// "verify decisions" guidance. Property 2 + Property 6.
func TestContextRetrieval_OldIntentClassifiedStale(t *testing.T) {
	now := time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)
	old := domain.IntentView{
		IntentID: "int_old",
		Status:   domain.StatusMerged,
		SealedAt: now.Add(-100 * 24 * time.Hour).Format(time.RFC3339),
		Fingerprint: &domain.SemanticFingerprint{
			FilesTouched: []string{"src/x.go"},
		},
	}
	churn := map[string]int{}

	got := classifyRetrievalStatus(old, churn, now)
	if got != RetrievalStatusStale {
		t.Errorf("expected stale for 100-day-old intent, got %q", got)
	}
}

// Status classification: a recent intent whose files have been
// re-touched by 3+ later intents is stale (file-churn signal).
func TestContextRetrieval_FileChurnTriggersStale(t *testing.T) {
	now := time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)
	iv := domain.IntentView{
		IntentID: "int_churned",
		Status:   domain.StatusMerged,
		SealedAt: now.Add(-7 * 24 * time.Hour).Format(time.RFC3339),
		Fingerprint: &domain.SemanticFingerprint{
			FilesTouched: []string{"src/hot.go"},
		},
	}
	churn := map[string]int{
		idForFile("int_churned", "src/hot.go"): staleFileChurnThreshold,
	}
	if got := classifyRetrievalStatus(iv, churn, now); got != RetrievalStatusStale {
		t.Errorf("file churn at threshold should mark stale, got %q", got)
	}

	// One below the threshold: still current.
	churnLight := map[string]int{
		idForFile("int_churned", "src/hot.go"): staleFileChurnThreshold - 1,
	}
	if got := classifyRetrievalStatus(iv, churnLight, now); got != RetrievalStatusCurrent {
		t.Errorf("churn below threshold should stay current, got %q", got)
	}
}

// Property 6: guidance is a deterministic function of status. A
// table-driven test pins the mapping so a future change to the
// guidance text fails the test loudly rather than silently.
func TestContextRetrieval_GuidanceIsDeterministicPerStatus(t *testing.T) {
	cases := []struct {
		status       string
		supersededBy string
		mustContain  string
	}{
		{RetrievalStatusCurrent, "", "verify against current code"},
		{RetrievalStatusSuperseded, "int_newer", "int_newer"},
		{RetrievalStatusAbandoned, "", "abandoned"},
		{RetrievalStatusStale, "", "verify decisions"},
	}
	for _, c := range cases {
		got := guidanceFor(c.status, c.supersededBy)
		if !strings.Contains(strings.ToLower(got), strings.ToLower(c.mustContain)) {
			t.Errorf("status %q expected guidance containing %q, got %q",
				c.status, c.mustContain, got)
		}
	}
}

// Property 3: when an intent A is superseded by B, B must rank
// strictly above A in retrieval — even when A's raw signal is
// stronger (older intent that touches more of the queried files).
func TestContextRetrieval_SupersederAlwaysRanksAboveSuperseded(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	// Original sealed intent touches the file heavily.
	oldID := sealedIntentTouching(t, dir, svc, "old-decision",
		[]string{"src/auth/middleware.go", "src/auth/cookies.go", "src/auth/csrf.go"},
		"Original auth approach")
	// New sealed intent touches the same file once + supersedes the old.
	newID := sealedIntentSupersedingWith(t, dir, svc, "new-decision",
		[]string{"src/auth/middleware.go"},
		"Replacement auth approach", oldID)

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "files",
		Files: []string{"src/auth/middleware.go"},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	var oldIdx, newIdx = -1, -1
	for i, ri := range res.RelevantIntents {
		switch ri.IntentID {
		case oldID:
			oldIdx = i
		case newID:
			newIdx = i
		}
	}
	if newIdx < 0 || oldIdx < 0 {
		t.Fatalf("both intents must appear; oldIdx=%d newIdx=%d", oldIdx, newIdx)
	}
	if newIdx >= oldIdx {
		t.Errorf("superseder %s must rank above superseded %s; got newIdx=%d oldIdx=%d",
			newID, oldID, newIdx, oldIdx)
	}
	if res.RelevantIntents[oldIdx].Status != RetrievalStatusSuperseded {
		t.Errorf("superseded intent should carry superseded status, got %q",
			res.RelevantIntents[oldIdx].Status)
	}
	if res.RelevantIntents[oldIdx].Guidance == "" ||
		!strings.Contains(res.RelevantIntents[oldIdx].Guidance, newID) {
		t.Errorf("superseded guidance must point at the replacement (%s), got %q",
			newID, res.RelevantIntents[oldIdx].Guidance)
	}
}

func TestContextRetrieval_IncludesSupersededLineageWhenSupersederMatches(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	view := &domain.MainlineView{
		SchemaVersion: 1,
		MainBranch:    "main",
		RebuiltAt:     "2026-04-29T00:00:00Z",
		Intents: []domain.IntentView{
			{
				IntentID:      "int_old_csv",
				Status:        domain.StatusSuperseded,
				ActorID:       "agent",
				Thread:        "feature/export-csv",
				GitBranch:     "feature/export-csv",
				Goal:          "build CSV export endpoint",
				SealedAt:      "2026-03-01T00:00:00Z",
				ViewRebuiltAt: "2026-04-29T00:00:00Z",
				StatusEvidence: domain.StatusEvidence{
					SupersededByIntent: "int_new_parquet",
				},
				Summary: &domain.IntentSummary{
					Title: "Build CSV export endpoint",
					What:  "Server-rendered CSV from /export.csv.",
				},
				Fingerprint: &domain.SemanticFingerprint{
					FilesTouched: []string{"src/export/csv.go"},
					Subsystems:   []string{"export"},
				},
			},
			{
				IntentID:      "int_new_parquet",
				Status:        domain.StatusMerged,
				ActorID:       "agent",
				Thread:        "feature/export-parquet",
				GitBranch:     "feature/export-parquet",
				Goal:          "move analyst export to columnar parquet",
				SealedAt:      "2026-04-28T00:00:00Z",
				ViewRebuiltAt: "2026-04-29T00:00:00Z",
				Summary: &domain.IntentSummary{
					Title: "Replace CSV export with columnar Parquet",
					What:  "Parquet export under /export.parquet; deprecate /export.csv.",
				},
				Fingerprint: &domain.SemanticFingerprint{
					FilesTouched: []string{"src/export/parquet.go", "src/export/csv.go"},
					Subsystems:   []string{"export"},
				},
			},
		},
	}
	if err := svc.Store.WriteMainlineView(view); err != nil {
		t.Fatalf("write view: %v", err)
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "query",
		Query: "column",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	idx := map[string]int{}
	for i, ri := range res.RelevantIntents {
		idx[ri.IntentID] = i
	}
	oldIdx, oldSeen := idx["int_old_csv"]
	newIdx, newSeen := idx["int_new_parquet"]
	if !oldSeen || !newSeen {
		t.Fatalf("expected both supersession endpoints, got %+v", res.RelevantIntents)
	}
	if newIdx >= oldIdx {
		t.Fatalf("superseder must rank above superseded; newIdx=%d oldIdx=%d", newIdx, oldIdx)
	}
	old := res.RelevantIntents[oldIdx]
	if old.Status != RetrievalStatusSuperseded {
		t.Fatalf("expected superseded retrieval status, got %q", old.Status)
	}
	if !strings.Contains(strings.Join(old.Relevance.Reasons, " "), "superseded by returned intent int_new_parquet") {
		t.Fatalf("expected lineage inclusion reason, got %v", old.Relevance.Reasons)
	}
}

// Legacy anti_patterns remain readable in sealed views, and lint still keeps
// their historical shape coherent. New seal submissions use SealSummaryInput
// and cannot create these fields.
func TestLintIntent_FlagsLegacyAntiPatternWithEmptyWhy(t *testing.T) {
	summary := &domain.IntentSummary{
		Title: "t", What: "w", Why: "y",
		Decisions: []domain.Decision{{Point: "p", Chose: "c"}},
		AntiPatterns: []domain.AntiPattern{
			{What: "do X", Why: "", Severity: "high"},
		},
	}
	fp := &domain.SemanticFingerprint{
		Subsystems:   []string{"s"},
		FilesTouched: []string{"f.go"},
	}
	res := LintIntent("int_x12345678", summary, fp, "", nil)
	if res.Pass {
		t.Fatalf("expected legacy anti_pattern with empty why to fail lint: %+v", res.Issues)
	}
	if !strings.Contains(fmt.Sprint(res.Issues), "anti_pattern_no_why") {
		t.Fatalf("expected anti_pattern_no_why lint issue, got %+v", res.Issues)
	}
}

// Helper: like sealedIntentTouching but threads a SupersededBy
// link from the new intent to a prior one. The prior intent's
// status_evidence is updated post-hoc via the supersede event so
// the view rebuild reflects the link.
func sealedIntentSupersedingWith(t *testing.T, dir string, svc *Service, branchSuffix string,
	files []string, summaryTitle string, supersedes string) string {
	t.Helper()
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "checkout", "-b", "feature/"+branchSuffix)
	start, err := svc.Start("ctx test "+branchSuffix, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, f := range files {
		writeFile(t, dir, f, "// "+f+"\n")
	}
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "ctx test "+branchSuffix)
	if _, err := svc.Append("did the work"); err != nil {
		t.Fatalf("append: %v", err)
	}
	sr := validSealResult(start.IntentID)
	sr.Summary.Title = summaryTitle
	sr.Fingerprint.FilesTouched = files
	sr.Fingerprint.Subsystems = subsystemsFromFiles(files)
	data, _ := json.Marshal(sr)
	if _, err := svc.SealSubmit(json.RawMessage(data)); err != nil {
		t.Fatalf("seal: %v", err)
	}
	// Mark the prior intent as superseded by this one. We patch the
	// view directly so the test does not depend on a Supersede CLI
	// command shape that may evolve; the rebuild after Sync would
	// otherwise re-derive from events.
	v, _ := svc.Store.ReadMainlineView()
	for i := range v.Intents {
		if v.Intents[i].IntentID == supersedes {
			v.Intents[i].Status = domain.StatusSuperseded
			v.Intents[i].StatusEvidence.SupersededByIntent = start.IntentID
		}
	}
	if err := svc.Store.WriteMainlineView(v); err != nil {
		t.Fatalf("write view: %v", err)
	}
	return start.IntentID
}

// TestContextRetrieval_SurfacesInheritedConstraints verifies that
// when --files names a file with an explicit human-promoted
// constraint, it appears in the top-level inherited_constraints
// field even if no intent is otherwise relevant.
func TestContextRetrieval_SurfacesInheritedConstraints(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	view := &domain.MainlineView{
		SchemaVersion: 1,
		MainBranch:    "main",
		RebuiltAt:     "2026-04-29T00:00:00Z",
		Constraints: []domain.Constraint{{
			ID:           "guard_auth",
			SourceIntent: "int_auth_old",
			Files:        []string{"src/auth/middleware.go"},
			What:         "Do not remove legacy session middleware on /oauth path",
			Why:          "OAuth callback needs session",
			Severity:     "high",
			OpenedAt:     "2026-04-28T00:00:00Z",
		}},
	}
	if err := svc.Store.WriteMainlineView(view); err != nil {
		t.Fatalf("write view: %v", err)
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "files",
		Files: []string{"src/auth/middleware.go"},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.InheritedConstraints) != 1 {
		t.Fatalf("expected 1 inherited constraint (only high severity), got %d (%+v)", len(res.InheritedConstraints), res.InheritedConstraints)
	}
	if res.InheritedConstraints[0].Severity != "high" {
		t.Errorf("expected high-severity constraint, got %q",
			res.InheritedConstraints[0].Severity)
	}
	if res.InheritedConstraints[0].ConstraintID == "" {
		t.Errorf("expected non-empty constraint_id")
	}
	// Top-level note must alert the agent about acknowledged_constraints format.
	hasAckNote := false
	for _, n := range res.Notes {
		if strings.Contains(n, "Inherited high-severity") || strings.Contains(n, "acknowledged_constraints") {
			hasAckNote = true
			break
		}
	}
	if !hasAckNote {
		t.Errorf("expected acknowledgement note in Notes; got %v", res.Notes)
	}
}

// TestContextRetrieval_NoInheritedWhenNoOverlap is the negative case
// — no constraints surfaced when the queried files don't overlap any
// explicit constraint scope.
func TestContextRetrieval_NoInheritedWhenNoOverlap(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	view := &domain.MainlineView{
		SchemaVersion: 1,
		MainBranch:    "main",
		RebuiltAt:     "2026-04-29T00:00:00Z",
		Constraints: []domain.Constraint{{
			ID:           "guard_auth",
			SourceIntent: "int_auth_old",
			Files:        []string{"internal/auth/middleware.go"},
			What:         "Do not remove legacy session middleware",
			Why:          "breaks sso",
			Severity:     "high",
			OpenedAt:     "2026-04-28T00:00:00Z",
		}},
	}
	if err := svc.Store.WriteMainlineView(view); err != nil {
		t.Fatalf("write view: %v", err)
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "files",
		Files: []string{"internal/billing/charges.go"},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.InheritedConstraints) != 0 {
		t.Errorf("expected zero inherited constraints for non-overlapping path, got %v", res.InheritedConstraints)
	}
}
