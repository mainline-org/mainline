//go:build !quick

package engine

import (
	"fmt"
	"math"
	"sort"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/mainline-org/mainline/internal/domain"
)

// -----------------------------------------------------------
// Property-based tests for the load-bearing context-retrieval
// invariants declared in docs_for_ai/mainline-spec-v0.2.md §9
// Step 2. Each test exercises one of the named Properties.
//
// These supplement (do not replace) the example-based tests in
// context_retrieval_test.go: examples pin the obvious cases for
// fast feedback; the PBTs explore the input space rapid considers
// most adversarial.
// -----------------------------------------------------------

// Property 2: classifyRetrievalStatus is a pure deterministic
// function of (IntentView, churn map, now). Calling it twice with
// the same arguments must return the same status — anything else
// would mean retrieval results would flicker between sync calls,
// which is the failure mode the determinism property exists to
// rule out.
func TestPropertyClassifyRetrievalStatusDeterministic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		iv := drawIntentView(rt, "iv")
		churn := drawChurnMap(rt, "churn", iv.IntentID)
		now := drawNow(rt, "now")

		first := classifyRetrievalStatus(iv, churn, now)
		// A handful of repeated calls — if the function ever depends
		// on hidden state (a global RNG, a clock read), repetition
		// surfaces it cheaply.
		for i := 0; i < 5; i++ {
			got := classifyRetrievalStatus(iv, churn, now)
			if got != first {
				rt.Fatalf("classifyRetrievalStatus not deterministic: first=%q now=%q (iter %d) iv=%+v churn=%+v",
					first, got, i, iv, churn)
			}
		}

		// And the result must be one of the four canonical retrieval
		// statuses, never something else: an unrecognised value here
		// would land in the JSON contract and confuse agents.
		switch first {
		case RetrievalStatusCurrent, RetrievalStatusSuperseded,
			RetrievalStatusAbandoned, RetrievalStatusStale:
		default:
			rt.Fatalf("classifier returned non-canonical status %q", first)
		}
	})
}

// Property 5: AntiPatterns conservation. Across any synthetic
// MainlineView, every intent that appears in the retrieval result
// carries exactly the same number of anti-patterns it had in the
// view. Truncation breaks the load-bearing safety property; this
// property is what enforces that no top-N path drops them.
func TestPropertyAntiPatternsConservation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}

		nIntents := rapid.IntRange(1, 6).Draw(rt, "n")
		view := &domain.MainlineView{SchemaVersion: 1, MainBranch: "main"}
		expectedAPs := map[string]int{}
		for i := 0; i < nIntents; i++ {
			iv := drawSealedIntent(rt, fmt.Sprintf("iv-%d", i))
			view.Intents = append(view.Intents, iv)
			if iv.Summary != nil {
				expectedAPs[iv.IntentID] = len(iv.Summary.AntiPatterns)
			}
		}
		if err := svc.Store.WriteMainlineView(view); err != nil {
			rt.Fatalf("write view: %v", err)
		}

		// Use a query that matches *something* in roughly half the
		// runs but not all — exercises both the high-overlap and
		// low-overlap paths through the ranker.
		query := rapid.SampledFrom([]string{"foo bar", "auth jwt", "router db", "test", ""}).Draw(rt, "query")
		req := ContextRetrievalRequest{Mode: "query", Query: query, Limit: 100}
		if query == "" {
			req = ContextRetrievalRequest{Mode: "files", Files: []string{"src/touched.go"}, Limit: 100}
		}
		res, err := svc.RetrieveContext(req)
		if err != nil {
			rt.Fatalf("retrieve: %v", err)
		}

		// For every intent that appears in the result, anti-pattern
		// count must equal the seal-time count. Truncation here
		// would break Property 5.
		for _, ri := range res.RelevantIntents {
			want := expectedAPs[ri.IntentID]
			got := len(ri.AntiPatterns)
			if got != want {
				rt.Fatalf("intent %s: anti-patterns truncated: want %d, got %d (status=%s)",
					ri.IntentID, want, got, ri.Status)
			}
		}
	})
}

// Property 3: when intent A is superseded by intent B, and both
// appear in the retrieval result, B's index must be strictly less
// than A's index — i.e. the superseder always ranks above the
// superseded in the ordered result. Score-pinning makes this hold
// regardless of A's raw signal weight; the property is what would
// catch a regression that flipped the rule.
func TestPropertySupersessionRanking(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}

		nIntents := rapid.IntRange(2, 6).Draw(rt, "n")
		view := &domain.MainlineView{SchemaVersion: 1, MainBranch: "main"}

		ids := make([]string, nIntents)
		for i := range ids {
			ids[i] = fmt.Sprintf("int_p%d_%s", i, randomTestString(4))
			iv := drawSealedIntent(rt, ids[i])
			iv.IntentID = ids[i]
			// Force every intent onto a shared file so retrieval
			// returns at least the candidates we want to compare.
			iv.Fingerprint.FilesTouched = append(iv.Fingerprint.FilesTouched, "src/shared.go")
			view.Intents = append(view.Intents, iv)
		}

		// Wire up zero or more supersedes links. Each link points
		// from an earlier-listed intent (older, more raw signal in
		// the score budget) to a later one (the replacement).
		nLinks := rapid.IntRange(0, nIntents/2).Draw(rt, "nLinks")
		links := map[string]string{} // superseded → superseder
		for i := 0; i < nLinks; i++ {
			a := rapid.IntRange(0, nIntents-2).Draw(rt, fmt.Sprintf("from-%d", i))
			b := rapid.IntRange(a+1, nIntents-1).Draw(rt, fmt.Sprintf("to-%d", i))
			view.Intents[a].Status = domain.StatusSuperseded
			view.Intents[a].StatusEvidence.SupersededByIntent = ids[b]
			links[ids[a]] = ids[b]
		}

		if err := svc.Store.WriteMainlineView(view); err != nil {
			rt.Fatalf("write view: %v", err)
		}

		res, err := svc.RetrieveContext(ContextRetrievalRequest{
			Mode:  "files",
			Files: []string{"src/shared.go"},
			Limit: 100,
		})
		if err != nil {
			rt.Fatalf("retrieve: %v", err)
		}

		idx := map[string]int{}
		for i, ri := range res.RelevantIntents {
			idx[ri.IntentID] = i
		}
		// Property 3 check: every superseded ↔ superseder pair
		// present in the result has the right ordering.
		for superseded, superseder := range links {
			si, sok := idx[superseded]
			ri, rok := idx[superseder]
			if !sok || !rok {
				continue // one or both didn't clear the relevance threshold
			}
			if ri >= si {
				rt.Fatalf("Property 3 violated: superseder %s at idx %d, superseded %s at idx %d (superseded must be lower-ranked)",
					superseder, ri, superseded, si)
			}
		}
	})
}

// Relevance breakdown observability: every raw score mutation in
// scoreIntentRelevance must be mirrored in ContextRelevanceBreakdown.
// Example tests pin visible JSON shapes; this property catches future
// scorer edits that add a signal but forget the debug surface.
func TestPropertyScoreBreakdownTracksRawScore(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		iv := drawScoredIntentView(rt, "score")
		files := rapid.SampledFrom([][]string{
			nil,
			{"src/auth/jwt.go"},
			{"src/billing/charges.go"},
			{"internal/engine/context_retrieval.go"},
			{"src/auth/jwt.go", "internal/engine/context_retrieval.go"},
		}).Draw(rt, "files")
		query := rapid.SampledFrom([]string{
			"",
			"auth jwt",
			"context retrieval",
			"risk followup",
			"anti_pattern constraint",
			"billing charges",
		}).Draw(rt, "query")

		score, _, breakdown := scoreIntentRelevance(iv, files, query, "feature/test")
		sum := breakdown.File + breakdown.Subsystem + breakdown.Title + breakdown.Summary +
			breakdown.Decision + breakdown.Risk + breakdown.Followup + breakdown.AntiPattern +
			breakdown.Recency + breakdown.SameThread + breakdown.Lineage - breakdown.StatusPenalty
		if math.Abs(sum-score) > 0.000001 {
			rt.Fatalf("score breakdown drift: sum=%.6f score=%.6f breakdown=%+v iv=%+v files=%+v query=%q",
				sum, score, breakdown, iv, files, query)
		}
	})
}

// -----------------------------------------------------------
// Generators
// -----------------------------------------------------------

// drawIntentView produces a synthetic IntentView with the fields
// classifyRetrievalStatus reads. We don't draw Summary/Fingerprint
// here because the classifier doesn't read them — only Status,
// StatusEvidence, SealedAt, and Fingerprint.FilesTouched.
func drawIntentView(rt *rapid.T, label string) domain.IntentView {
	statuses := []domain.IntentStatus{
		domain.StatusMerged,
		domain.StatusProposed,
		domain.StatusSealedLocal,
		domain.StatusAbandoned,
		domain.StatusReverted,
		domain.StatusSuperseded,
	}
	st := rapid.SampledFrom(statuses).Draw(rt, label+".status")

	// Sealed-at: random offset from the reference now, but bounded
	// so the test runs in a deterministic time space.
	ageDays := rapid.IntRange(0, 200).Draw(rt, label+".ageDays")
	sealedAt := time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC).
		Add(-time.Duration(ageDays) * 24 * time.Hour).
		Format(time.RFC3339)

	// Optional supersede link.
	var ev domain.StatusEvidence
	if rapid.Bool().Draw(rt, label+".hasSupersedeLink") {
		ev.SupersededByIntent = "int_other_" + randomTestString(4)
	}

	// Optional files (used by file-churn stale signal).
	var fp *domain.SemanticFingerprint
	if rapid.Bool().Draw(rt, label+".hasFiles") {
		nFiles := rapid.IntRange(1, 4).Draw(rt, label+".nFiles")
		files := make([]string, nFiles)
		for i := range files {
			files[i] = fmt.Sprintf("src/f%d.go", i)
		}
		fp = &domain.SemanticFingerprint{FilesTouched: files}
	}

	return domain.IntentView{
		IntentID:       "int_" + label + "_" + randomTestString(4),
		Status:         st,
		StatusEvidence: ev,
		SealedAt:       sealedAt,
		Fingerprint:    fp,
	}
}

// drawChurnMap produces a churn lookup that *might* tip the file-
// churn stale signal. Half the runs return an empty map (no
// churn); the other half populate it for some files of the input
// intent so the threshold path is exercised.
func drawChurnMap(rt *rapid.T, label, intentID string) map[string]int {
	if !rapid.Bool().Draw(rt, label+".populated") {
		return map[string]int{}
	}
	out := map[string]int{}
	nKeys := rapid.IntRange(0, 5).Draw(rt, label+".nKeys")
	for i := 0; i < nKeys; i++ {
		// Hit and miss values around the threshold so we exercise
		// both sides of the comparison.
		count := rapid.IntRange(0, staleFileChurnThreshold+2).Draw(rt, fmt.Sprintf("%s.count-%d", label, i))
		f := fmt.Sprintf("src/f%d.go", i)
		out[idForFile(intentID, f)] = count
	}
	return out
}

func drawNow(rt *rapid.T, label string) time.Time {
	// Fixed reference + small jitter so test output is reproducible
	// under a given seed.
	jitterHours := rapid.IntRange(-24, 24).Draw(rt, label)
	return time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC).
		Add(time.Duration(jitterHours) * time.Hour)
}

// drawSealedIntent produces an intent suitable for retrieval-level
// PBTs (Properties 3 and 5). Summary + Fingerprint are populated;
// AntiPatterns count is randomised so the conservation property
// has signal across runs.
func drawSealedIntent(rt *rapid.T, label string) domain.IntentView {
	id := "int_" + label + "_" + randomTestString(4)
	nAPs := rapid.IntRange(0, 4).Draw(rt, label+".nAPs")
	aps := make([]domain.AntiPattern, nAPs)
	for i := range aps {
		aps[i] = domain.AntiPattern{
			What:     fmt.Sprintf("avoid pattern %d in %s", i, id),
			Why:      "load-bearing reason " + randomTestString(3),
			Severity: rapid.SampledFrom([]string{"low", "medium", "high"}).Draw(rt, fmt.Sprintf("%s.ap-%d.sev", label, i)),
		}
	}
	nFiles := rapid.IntRange(1, 4).Draw(rt, label+".nFiles")
	files := make([]string, nFiles)
	for i := range files {
		files[i] = fmt.Sprintf("src/touched.go")
	}
	// dedupe
	sort.Strings(files)
	dedup := files[:1]
	for _, f := range files[1:] {
		if dedup[len(dedup)-1] != f {
			dedup = append(dedup, f)
		}
	}
	return domain.IntentView{
		IntentID:      id,
		Status:        domain.StatusMerged,
		ActorID:       "actor_test",
		Thread:        "feature/" + label,
		SealedAt:      time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		ViewRebuiltAt: time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		Summary: &domain.IntentSummary{
			Title:        "title for " + id,
			What:         "did real work touching " + dedup[0],
			Why:          "real reason",
			Decisions:    []domain.Decision{{Point: "p", Chose: "c"}},
			AntiPatterns: aps,
		},
		Fingerprint: &domain.SemanticFingerprint{
			Subsystems:   []string{"sub"},
			FilesTouched: dedup,
		},
	}
}

func drawScoredIntentView(rt *rapid.T, label string) domain.IntentView {
	id := "int_" + label + "_" + randomTestString(4)
	status := rapid.SampledFrom([]domain.IntentStatus{
		domain.StatusMerged,
		domain.StatusProposed,
		domain.StatusSealedLocal,
		domain.StatusAbandoned,
		domain.StatusReverted,
		domain.StatusSuperseded,
	}).Draw(rt, label+".status")
	ageDays := rapid.SampledFrom([]int{1, 5, 20, 45}).Draw(rt, label+".ageDays")
	files := rapid.SampledFrom([][]string{
		{"src/auth/jwt.go"},
		{"src/billing/charges.go"},
		{"internal/engine/context_retrieval.go"},
		{"src/auth/jwt.go", "internal/engine/context_retrieval.go", "src/billing/charges.go"},
	}).Draw(rt, label+".files")
	subsystems := subsystemsFromFiles(files)
	if rapid.Bool().Draw(rt, label+".extraSubsystem") {
		subsystems = append(subsystems, "extra")
	}
	return domain.IntentView{
		IntentID:      id,
		Status:        status,
		ActorID:       "actor_test",
		Thread:        "feature/" + label,
		SealedAt:      time.Now().Add(-time.Duration(ageDays) * 24 * time.Hour).UTC().Format(time.RFC3339),
		ViewRebuiltAt: time.Now().UTC().Format(time.RFC3339),
		Summary: &domain.IntentSummary{
			Title: "auth jwt context retrieval " + id,
			What:  "implemented context retrieval for billing charges",
			Why:   "because auth risk and followup visibility matter",
			Decisions: []domain.Decision{{
				Point:     "context retrieval decision",
				Chose:     "score auth and billing signals",
				Rationale: "decision rationale mentions jwt",
			}},
			Risks:     []string{"risk followup can drift"},
			Followups: []string{"followup context query audit"},
			AntiPatterns: []domain.AntiPattern{{
				What:     "anti_pattern constraint",
				Why:      "avoid hidden context retrieval drift",
				Severity: "high",
			}},
		},
		Fingerprint: &domain.SemanticFingerprint{
			Subsystems:   subsystems,
			FilesTouched: files,
		},
	}
}
