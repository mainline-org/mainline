package engine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mainline-org/mainline/internal/core"
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
// Step 2: anti_patterns + retrieval status + guidance + ranking
// -----------------------------------------------------------

// AntiPatterns must reach the agent verbatim — they're the load-
// bearing safety surface and must never be truncated by the top-N
// caps that apply to decisions/risks. Property 5.
func TestContextRetrieval_AntiPatternsPropagateAndAreNeverTruncated(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	id := sealedIntentTouchingWithAntiPatterns(t, dir, svc, "ap-test",
		[]string{"src/auth/middleware.go"},
		"Auth migration with constraints",
		[]domain.AntiPattern{
			{What: "Removing legacy session middleware on /oauth path", Why: "OAuth callback handler still requires session state", Severity: "high"},
			{What: "Bypassing JWT validation in dev mode", Why: "Same code path runs in CI; bypass leaks", Severity: "medium"},
			{What: "Calling internal token issuer from request handlers", Why: "Couples HTTP layer to crypto subsystem", Severity: "low"},
			{What: "Storing session tokens in localStorage", Why: "XSS risk; we standardised on httpOnly cookies", Severity: "high"},
			{What: "Using sync ops in the auth callback", Why: "Blocks event loop; we already had one outage", Severity: "medium"},
		})

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "files",
		Files: []string{"src/auth/middleware.go"},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	var found *ContextRelevant
	for i := range res.RelevantIntents {
		if res.RelevantIntents[i].IntentID == id {
			found = &res.RelevantIntents[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected intent %s in retrieval", id)
	}
	if len(found.AntiPatterns) != 5 {
		t.Fatalf("anti_patterns must NOT be truncated; want 5, got %d", len(found.AntiPatterns))
	}
	for i, ap := range found.AntiPatterns {
		if ap.What == "" || ap.Why == "" {
			t.Errorf("anti_pattern[%d] missing what/why: %+v", i, ap)
		}
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
		status        string
		supersededBy  string
		mustContain   string
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

func TestEnforceSupersessionRankingHandlesChains(t *testing.T) {
	scored := []ContextRelevant{
		{
			IntentID:     "int_old",
			SupersededBy: "int_mid",
			Relevance:   ContextRelevance{Score: 0.33},
		},
		{
			IntentID:     "int_mid",
			SupersededBy: "int_new",
			Relevance:   ContextRelevance{Score: 0.33},
		},
		{
			IntentID:   "int_new",
			Relevance: ContextRelevance{Score: 0.32},
		},
	}

	enforceSupersessionRanking(scored)

	got := []string{scored[0].IntentID, scored[1].IntentID, scored[2].IntentID}
	want := []string{"int_new", "int_mid", "int_old"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("supersession chain order mismatch: got %v, want %v", got, want)
		}
	}
}

// Property 1: an anti_pattern with empty why is rejected at seal
// time. This is what keeps the load-bearing safety property
// honest — agents that paste anti_patterns without reasons get a
// clear failure rather than silent acceptance.
func TestValidateSealResult_RejectsAntiPatternWithEmptyWhy(t *testing.T) {
	sr := &domain.SealResult{
		IntentID: "int_x12345678",
		Summary: domain.IntentSummary{
			Title: "t", What: "w", Why: "y",
			AntiPatterns: []domain.AntiPattern{
				{What: "do X", Why: "", Severity: "high"},
			},
		},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:   []string{"s"},
			FilesTouched: []string{"f.go"},
		},
		Confidence: domain.SealConfidence{Summary: 0.9, Fingerprint: 0.9},
	}
	if err := core.ValidateSealResult(sr); err == nil {
		t.Fatal("expected rejection of anti_pattern with empty why")
	} else if !strings.Contains(err.Error(), "anti_patterns") || !strings.Contains(err.Error(), "why") {
		t.Errorf("error should mention anti_patterns + why, got: %v", err)
	}

	// Empty severity is fine; non-canonical severity is rejected.
	sr.Summary.AntiPatterns[0].Why = "real reason"
	sr.Summary.AntiPatterns[0].Severity = "catastrophic"
	if err := core.ValidateSealResult(sr); err == nil {
		t.Fatal("expected rejection of non-canonical severity")
	}

	sr.Summary.AntiPatterns[0].Severity = "high"
	if err := core.ValidateSealResult(sr); err != nil {
		t.Errorf("valid anti_pattern should pass: %v", err)
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

// Helper: seal an intent with a populated AntiPatterns slice.
func sealedIntentTouchingWithAntiPatterns(t *testing.T, dir string, svc *Service, branchSuffix string,
	files []string, summaryTitle string, antiPatterns []domain.AntiPattern) string {
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
	sr.Summary.AntiPatterns = antiPatterns
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
