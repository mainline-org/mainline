package eval

import (
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

// fakeRetriever is a hand-controlled retrieval result so the
// substrate-layer tests don't pull in engine. Tests in
// internal/cli/eval_test.go (or the integration layer) exercise the
// engine wire-up.
type fakeRetriever struct{ out []Retrieved }

func (f fakeRetriever) RetrieveByQuery(_ string, _ int) ([]Retrieved, error) {
	return f.out, nil
}

// BuildView is the seam between fixture data and the materialised
// view RetrieveContext reads. Spot-check the non-trivial fields:
// supersedes evidence, AntiPatterns flow through, AgeDays drives
// SealedAt direction (older = earlier timestamp).
func TestBuildView_PopulatesIntentSummaryAndStatusEvidence(t *testing.T) {
	f := Fixture{
		Name: "test",
		Intents: []SeedIntent{
			{
				ID:           "int_a",
				Title:        "Title",
				What:         "what",
				Why:          "why",
				AntiPatterns: []domain.AntiPattern{{What: "no", Why: "load-bearing"}},
				Status:       domain.StatusSuperseded,
				SupersededBy: "int_b",
				AgeDays:      30,
			},
			{ID: "int_b", What: "later", Status: domain.StatusMerged, AgeDays: 0},
		},
	}
	v := BuildView(f)
	if len(v.Intents) != 2 {
		t.Fatalf("expected 2 intents, got %d", len(v.Intents))
	}
	a := v.Intents[0]
	if a.IntentID != "int_a" || a.Summary == nil || a.Summary.What != "what" {
		t.Errorf("int_a summary lost: %+v", a)
	}
	if a.StatusEvidence.SupersededByIntent != "int_b" {
		t.Errorf("supersedes evidence not propagated: %+v", a.StatusEvidence)
	}
	if len(a.Summary.AntiPatterns) != 1 || a.Summary.AntiPatterns[0].Why != "load-bearing" {
		t.Errorf("anti-patterns not flowing through: %+v", a.Summary.AntiPatterns)
	}
	// Older-aged intent must have a strictly-earlier SealedAt than
	// the newer one. AgeDays=30 vs AgeDays=0.
	if a.SealedAt >= v.Intents[1].SealedAt {
		t.Errorf("AgeDays not driving SealedAt order: a=%s b=%s",
			a.SealedAt, v.Intents[1].SealedAt)
	}
}

// ScoreFixture: every Expected.IntentID present + all anti-patterns
// matched + statuses match → Pass=true, every item.Pass=true.
func TestScoreFixture_AllExpectedMet(t *testing.T) {
	f := Fixture{
		Name: "happy",
		Expected: []ExpectedItem{
			{IntentID: "int_a", AntiPatternMatch: "oauth"},
			{IntentID: "int_b", MinStatus: "superseded"},
		},
	}
	got := []Retrieved{
		{IntentID: "int_a", Status: "current", AntiPatterns: []domain.AntiPattern{
			{What: "Removing the /oauth path", Why: "callback needs it"},
		}},
		{IntentID: "int_b", Status: "superseded"},
	}
	res := ScoreFixture(f, got)
	if !res.Pass {
		t.Fatalf("expected pass, got %+v", res)
	}
	if len(res.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(res.Items))
	}
}

// Missing IntentID → that item fails AND the fixture fails.
func TestScoreFixture_MissingIntentFailsFixture(t *testing.T) {
	f := Fixture{
		Name: "missing",
		Expected: []ExpectedItem{
			{IntentID: "int_present"},
			{IntentID: "int_missing"},
		},
	}
	got := []Retrieved{{IntentID: "int_present", Status: "current"}}
	res := ScoreFixture(f, got)
	if res.Pass {
		t.Errorf("missing intent should fail fixture: %+v", res)
	}
	var missingItem *ScoreItem
	for i := range res.Items {
		if res.Items[i].IntentID == "int_missing" {
			missingItem = &res.Items[i]
		}
	}
	if missingItem == nil {
		t.Fatal("expected a missing-item entry in score items")
	}
	if missingItem.Pass {
		t.Errorf("missing item should be Pass=false")
	}
}

// AntiPattern substring match is case-insensitive — fixtures should
// not need to know whether agents recorded AntiPatterns in lower
// case or title case.
func TestScoreFixture_AntiPatternMatchIsCaseInsensitive(t *testing.T) {
	f := Fixture{
		Name: "case",
		Expected: []ExpectedItem{
			{IntentID: "int_a", AntiPatternMatch: "OAUTH PATH"},
		},
	}
	got := []Retrieved{
		{IntentID: "int_a", AntiPatterns: []domain.AntiPattern{
			{What: "removing the /oauth path", Why: "x"},
		}},
	}
	res := ScoreFixture(f, got)
	if !res.Pass {
		t.Errorf("case-insensitive match should pass: %+v", res)
	}
}

// MinStatus mismatch fails the item with a clear reason — agents
// should be able to read the score and know what to fix.
func TestScoreFixture_StatusMismatchExplains(t *testing.T) {
	f := Fixture{
		Name: "status",
		Expected: []ExpectedItem{
			{IntentID: "int_a", MinStatus: "stale"},
		},
	}
	got := []Retrieved{{IntentID: "int_a", Status: "current"}}
	res := ScoreFixture(f, got)
	if res.Pass {
		t.Errorf("status mismatch should fail: %+v", res)
	}
	if !strings.Contains(res.Items[0].Reason, "stale") || !strings.Contains(res.Items[0].Reason, "current") {
		t.Errorf("reason should mention both statuses, got %q", res.Items[0].Reason)
	}
}

// RunFixture wires the retriever and emits the same ScoreFixture
// shape; smoke-test the round trip.
func TestRunFixture_RoundTripsThroughRetriever(t *testing.T) {
	f := Fixture{
		Name: "round-trip",
		Expected: []ExpectedItem{
			{IntentID: "int_a"},
		},
	}
	r := fakeRetriever{out: []Retrieved{{IntentID: "int_a", Status: "current"}}}
	res, err := RunFixture(f, r, 5)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.Pass {
		t.Errorf("expected pass: %+v", res)
	}
}

// Stubs (no Intents) appear in RunAll's summary as Skipped, never
// counted as a pass or fail.
func TestRunAll_SkipsStubsCleanly(t *testing.T) {
	stub := Fixture{Name: "stub", Description: "[stub]"}
	live := Fixture{
		Name:     "live",
		Intents:  []SeedIntent{{ID: "int_a", What: "did work", Status: domain.StatusMerged}},
		Task:     "anything",
		Expected: []ExpectedItem{{IntentID: "int_a"}},
	}
	r := fakeRetriever{out: []Retrieved{{IntentID: "int_a"}}}
	out, err := RunAll([]Fixture{stub, live}, r, 5)
	if err != nil {
		t.Fatal(err)
	}
	if out.Skipped != 1 || out.Passed != 1 || out.Failed != 0 {
		t.Errorf("unexpected counts: %+v", out)
	}
	if out.AllPassed {
		t.Errorf("AllPassed should be false when Skipped > 0; harness must finish stubs before claiming all-clear")
	}
}

// Embedded catalog must be non-empty and every populated fixture
// must declare at least one Expected — otherwise the scorer would
// trivially pass (nothing to check).
func TestFixtures_CatalogContractHolds(t *testing.T) {
	fs := Fixtures()
	if len(fs) == 0 {
		t.Fatal("Fixtures() must not be empty")
	}
	for _, f := range fs {
		if f.Name == "" {
			t.Errorf("fixture without a name: %+v", f)
		}
		if len(f.Intents) == 0 {
			// stub: must still have a description signalling it
			if !strings.Contains(f.Description, "[stub]") {
				t.Errorf("fixture %s has no Intents and no [stub] marker — describe its status", f.Name)
			}
			continue
		}
		if len(f.Expected) == 0 {
			t.Errorf("fixture %s has Intents but no Expected; scorer would trivially pass", f.Name)
		}
		if f.Task == "" {
			t.Errorf("fixture %s has Intents but empty Task; the retriever has no query", f.Name)
		}
	}
}
