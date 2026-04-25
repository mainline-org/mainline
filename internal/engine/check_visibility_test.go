package engine

import (
	"encoding/json"
	"testing"

	"mainline/internal/domain"
)

// -----------------------------------------------------------
// Pure helpers
// -----------------------------------------------------------

func TestCheckMarkerStates(t *testing.T) {
	cases := []struct {
		name string
		in   *domain.CheckSummary
		want string
	}{
		{"nil", nil, ""},
		{"clean", &domain.CheckSummary{}, "ok"},
		{"conflict", &domain.CheckSummary{HasConflict: true}, "!"},
		{"human", &domain.CheckSummary{NeedsHumanReview: true}, "?"},
		{"both", &domain.CheckSummary{HasConflict: true, NeedsHumanReview: true}, "?"},
	}
	for _, tc := range cases {
		if got := checkMarker(tc.in); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestExtractAgainstIntentsPrefersEvidence(t *testing.T) {
	candidate := "int_aaaaaaaa"
	judgments := []domain.ConflictJudgment{
		{
			TaskID: "task_int_aaaaaaaa_int_bbbbbbbb",
			Evidence: []domain.ConflictEvidence{
				{MainlineIntent: "int_bbbbbbbb"},
			},
		},
		{
			TaskID: "task_int_aaaaaaaa_int_cccccccc",
			Evidence: []domain.ConflictEvidence{
				{MainlineIntent: "int_cccccccc"},
				{MainlineIntent: "int_dddddddd"}, // multi-evidence judgment
			},
		},
	}
	got := extractAgainstIntents(judgments, candidate)
	want := []string{"int_bbbbbbbb", "int_cccccccc", "int_dddddddd"}
	if !sameSlice(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestExtractAgainstIntentsFallsBackToTaskID(t *testing.T) {
	got := extractAgainstIntents([]domain.ConflictJudgment{
		{TaskID: "task_int_aaaaaaaa_int_eeeeeeee"},
	}, "int_aaaaaaaa")
	if !sameSlice(got, []string{"int_eeeeeeee"}) {
		t.Errorf("got %v", got)
	}
}

func TestExtractAgainstIntentsDeduplicatesAndExcludesCandidate(t *testing.T) {
	got := extractAgainstIntents([]domain.ConflictJudgment{
		{Evidence: []domain.ConflictEvidence{{MainlineIntent: "int_b"}}},
		{Evidence: []domain.ConflictEvidence{{MainlineIntent: "int_b"}}},
		{Evidence: []domain.ConflictEvidence{{MainlineIntent: "int_a"}}}, // candidate, must drop
		{Evidence: []domain.ConflictEvidence{{MainlineIntent: "int_c"}}},
	}, "int_a")
	if !sameSlice(got, []string{"int_b", "int_c"}) {
		t.Errorf("got %v", got)
	}
}

func sameSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// -----------------------------------------------------------
// End-to-end: CheckSubmit → Sync → IntentView.LastCheck
// -----------------------------------------------------------

// Repros the dogfood gap: pre-rc4 the engine wrote CheckJudgmentEvent
// to the actor log but no command read it back. After this fix the
// view's IntentView.LastCheck must be populated from the latest
// candidate-matched judgment event.
func TestCheckSubmitSurfacesInLastCheck(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Two sealed intents — candidate + the one the judgment will
	// reference. Both go through the standard seal pipeline so the
	// actor log holds real IntentSealedEvents alongside the judgment.
	candidateID, _ := seedSealedIntent(t, dir, svc, "lc-cand", "lc_cand.go")
	mainlineID, _ := seedSealedIntent(t, dir, svc, "lc-main", "lc_main.go")

	gitCmd(t, dir, "checkout", "main")

	// Synthesise a judgment without going through the agent prompt.
	cr := domain.CheckJudgmentResult{
		CandidateIntent: candidateID,
		Judgments: []domain.ConflictJudgment{
			{
				TaskID:     "task_" + candidateID + "_" + mainlineID,
				Severity:   "low",
				Confidence: 0.9,
				Evidence: []domain.ConflictEvidence{
					{MainlineIntent: mainlineID, CandidateIntent: candidateID},
				},
			},
		},
		Overall: domain.CheckOverall{
			HasConflict:      false,
			HighestSeverity:  "low",
			NeedsHumanReview: false,
		},
	}
	data, _ := json.Marshal(cr)
	res, err := svc.CheckSubmit(json.RawMessage(data))
	if err != nil {
		t.Fatalf("CheckSubmit: %v", err)
	}

	if _, err := svc.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	view, _ := svc.Store.ReadMainlineView()
	var iv *domain.IntentView
	for i := range view.Intents {
		if view.Intents[i].IntentID == candidateID {
			iv = &view.Intents[i]
			break
		}
	}
	if iv == nil {
		t.Fatalf("candidate intent %s missing from view", candidateID)
	}
	if iv.LastCheck == nil {
		t.Fatalf("IntentView.LastCheck nil — sync did not consume CheckJudgmentEvent")
	}
	lc := iv.LastCheck
	if lc.EventID != res.EventID {
		t.Errorf("EventID: got %s want %s", lc.EventID, res.EventID)
	}
	if lc.JudgmentCount != 1 {
		t.Errorf("JudgmentCount: got %d want 1", lc.JudgmentCount)
	}
	if lc.HasConflict {
		t.Error("HasConflict should be false")
	}
	if lc.HighestSeverity != "low" {
		t.Errorf("HighestSeverity: got %s want low", lc.HighestSeverity)
	}
	if !sameSlice(lc.AgainstIntents, []string{mainlineID}) {
		t.Errorf("AgainstIntents: got %v want [%s]", lc.AgainstIntents, mainlineID)
	}

	// And the LogIntentEntry should carry the marker too.
	logRes, _ := svc.Log(20)
	for _, e := range logRes.Intents {
		if e.IntentID == candidateID {
			if e.Check != "ok" {
				t.Errorf("log entry Check: got %q want ok", e.Check)
			}
			return
		}
	}
	t.Error("candidate not found in log")
}

// Last-write-wins: if the same candidate is judged twice, the second
// (later) judgment overwrites the first in LastCheck. Otherwise old
// "no_conflict" results would mask a freshly discovered conflict.
func TestCheckSubmitLastWriteWins(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	candidateID, _ := seedSealedIntent(t, dir, svc, "lww-cand", "lww_cand.go")
	mainlineID, _ := seedSealedIntent(t, dir, svc, "lww-main", "lww_main.go")
	gitCmd(t, dir, "checkout", "main")

	first := domain.CheckJudgmentResult{
		CandidateIntent: candidateID,
		Judgments: []domain.ConflictJudgment{{
			TaskID:     "task_" + candidateID + "_" + mainlineID,
			Severity:   "low",
			Confidence: 0.9,
			Evidence: []domain.ConflictEvidence{
				{MainlineIntent: mainlineID},
			},
		}},
		Overall: domain.CheckOverall{HasConflict: false, HighestSeverity: "low"},
	}
	d1, _ := json.Marshal(first)
	svc.CheckSubmit(json.RawMessage(d1))

	second := first
	second.Overall = domain.CheckOverall{
		HasConflict:      true,
		HighestSeverity:  "high",
		NeedsHumanReview: true,
	}
	d2, _ := json.Marshal(second)
	res2, _ := svc.CheckSubmit(json.RawMessage(d2))

	svc.Sync()
	view, _ := svc.Store.ReadMainlineView()
	for _, iv := range view.Intents {
		if iv.IntentID != candidateID {
			continue
		}
		if iv.LastCheck == nil {
			t.Fatal("LastCheck nil")
		}
		if iv.LastCheck.EventID != res2.EventID {
			t.Errorf("LastCheck.EventID: got %s want %s", iv.LastCheck.EventID, res2.EventID)
		}
		if !iv.LastCheck.HasConflict {
			t.Error("expected HasConflict=true (second judgment)")
		}
		if iv.LastCheck.HighestSeverity != "high" {
			t.Errorf("HighestSeverity: got %s", iv.LastCheck.HighestSeverity)
		}
		return
	}
	t.Error("candidate missing from view")
}
