package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

// helper: build a sealed intent with N append turns the test can
// then trace.
func sealedIntentWithTurns(t *testing.T, dir string, svc *Service, branchSuffix string, turnDescs []string) string {
	t.Helper()
	branch := "feature/" + branchSuffix
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "checkout", "-b", branch)
	start, err := svc.Start("trace test "+branchSuffix, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	writeFile(t, dir, branchSuffix+".go", "package main\n")
	gitCmd(t, dir, "add", branchSuffix+".go")
	gitCmd(t, dir, "commit", "-m", "trace seed "+branchSuffix)
	for _, d := range turnDescs {
		if _, err := svc.Append(d); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	sr := validSealResult(start.IntentID)
	data, _ := json.Marshal(sr)
	if _, err := svc.SealSubmit(json.RawMessage(data)); err != nil {
		t.Fatalf("seal: %v", err)
	}
	return start.IntentID
}

// 1. Basic: 5-turn intent (start + 3 append + seal) traces correctly.
func TestTrace_BasicTimelineHasStartAppendsSeal(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	id := sealedIntentWithTurns(t, dir, svc, "basic", []string{
		"first turn", "second turn", "third turn",
	})

	res, err := svc.Trace(id, nil)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if res.Status != string(domain.StatusProposed) && res.Status != string(domain.StatusSealedLocal) {
		t.Fatalf("expected proposed or sealed_local, got %s", res.Status)
	}
	// 1 start + 3 appends + 1 seal = 5 turns.
	if len(res.Turns) != 5 {
		t.Fatalf("expected 5 turns, got %d (%+v)", len(res.Turns), res.Turns)
	}
	if res.Turns[0].Type != TraceTurnStart {
		t.Fatalf("first turn must be start, got %s", res.Turns[0].Type)
	}
	if res.Turns[len(res.Turns)-1].Type != TraceTurnSeal {
		t.Fatalf("last turn must be seal, got %s", res.Turns[len(res.Turns)-1].Type)
	}
	for i := 1; i <= 3; i++ {
		if res.Turns[i].Type != TraceTurnAppend {
			t.Fatalf("turn %d must be append, got %s", i, res.Turns[i].Type)
		}
	}
}

// 2. JSON-shape sanity: the schema documented in the spec is what
// callers receive. Spot-check the load-bearing fields.
func TestTrace_JSONShape(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	id := sealedIntentWithTurns(t, dir, svc, "json", []string{"a"})

	res, err := svc.Trace(id, nil)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	buf, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)
	for _, must := range []string{
		`"intent_id"`, `"status"`, `"turns"`, `"summary"`,
		`"index"`, `"type"`, `"timestamp"`,
		`"elapsed_from_start_seconds"`, `"elapsed_from_previous_seconds"`,
		`"append_turns_recorded_together"`, `"total_turns"`,
	} {
		if !strings.Contains(got, must) {
			t.Errorf("JSON missing required field %s", must)
		}
	}
}

// 3. Drafting: not yet sealed, trace still works and shows turns
// without the seal event.
func TestTrace_DraftingIntentBeforeSeal(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	gitCmd(t, dir, "checkout", "-b", "feature/still-drafting")
	start, _ := svc.Start("not yet sealed", "")
	svc.Append("first")
	svc.Append("second")

	res, err := svc.Trace(start.IntentID, nil)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if res.Status != string(domain.StatusDrafting) {
		t.Fatalf("expected drafting, got %s", res.Status)
	}
	// Start + 2 appends, no seal yet.
	if len(res.Turns) != 3 {
		t.Fatalf("expected 3 turns, got %d", len(res.Turns))
	}
	for _, turn := range res.Turns {
		if turn.Type == TraceTurnSeal {
			t.Fatalf("drafting trace must not contain a seal turn")
		}
	}
	if res.SealedAt != "" {
		t.Fatalf("drafting trace must have empty SealedAt, got %q", res.SealedAt)
	}
}

// 4. Abandon: terminal event lands on the actor log when abandoning a
// SEALED intent (drafting-state abandons delete the draft entirely
// per #36 — they have no audit trail and trace correctly reports
// them as not found).
func TestTrace_AbandonedIntentSurfacesReason(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	id := sealedIntentWithTurns(t, dir, svc, "abandon", []string{"trying things"})
	if _, err := svc.Abandon(id, "approach didn't pan out"); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	res, err := svc.Trace(id, nil)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if res.Status != string(domain.StatusAbandoned) {
		t.Fatalf("expected abandoned, got %s", res.Status)
	}
	if res.StatusReason != "approach didn't pan out" {
		t.Fatalf("expected reason in trace, got %q", res.StatusReason)
	}
	last := res.Turns[len(res.Turns)-1]
	if last.Type != TraceTurnAbandon {
		t.Fatalf("last turn should be abandon, got %s", last.Type)
	}
}

// 5. Same-second append turns: detected and reported in the summary.
// Mirrors the rc7 honest-signal flag — turns batched right before
// seal share timestamps; tests that we surface that fact.
//
// Bypasses svc.Append (which timestamps via core.Now()) and writes
// the turn entries directly via Store.AppendTurn with an explicit
// shared CreatedAt. The original wall-clock-driven version was
// flaky under `-race -short`: three consecutive svc.Append calls
// are not guaranteed to land in the same second when the race
// detector is on. The actual claim under test ("given turns
// sharing a timestamp, the flag fires") doesn't need a real clock
// to verify.
func TestTrace_AppendTurnsRecordedTogetherDetected(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "checkout", "-b", "feature/samesec")
	start, err := svc.Start("trace test samesec", "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	const sameTS = "2026-04-29T12:00:00Z"
	for i, desc := range []string{"a", "b", "c"} {
		if err := svc.Store.AppendTurn(&domain.Turn{
			ID:          fmt.Sprintf("turn_samesec_%d", i),
			IntentID:    start.IntentID,
			Index:       i,
			CreatedAt:   sameTS,
			Description: desc,
		}); err != nil {
			t.Fatalf("append turn[%d]: %v", i, err)
		}
	}

	res, err := svc.Trace(start.IntentID, nil)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if !res.Summary.AppendTurnsRecordedTogether {
		t.Fatalf("expected AppendTurnsRecordedTogether=true for batched appends; got summary=%+v", res.Summary)
	}
}

// 6. Intent not found: friendly error mentions the bad ref and
// suggests recovery actions.
func TestTrace_IntentNotFound(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	_, err := svc.Trace("int_no_such_id", nil)
	if err == nil {
		t.Fatalf("expected NotFound error, got nil")
	}
	if !strings.Contains(err.Error(), "int_no_such_id") {
		t.Fatalf("error should mention the missing id, got: %v", err)
	}
}

// 7. Prefix match: shorter form resolves to the full id.
func TestTrace_PrefixMatchResolves(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	id := sealedIntentWithTurns(t, dir, svc, "prefix", []string{"x"})

	// Use first 8 chars after `int_` as the prefix probe.
	if len(id) < 8 {
		t.Fatalf("intent id too short to test prefix match: %s", id)
	}
	short := id[:8]
	res, err := svc.Trace(short, nil)
	if err != nil {
		t.Fatalf("prefix trace failed: %v", err)
	}
	if res.IntentID != id {
		t.Fatalf("prefix %q should have resolved to %s, got %s", short, id, res.IntentID)
	}
}

// 8. Ambiguous prefix: error lists candidate ids.
func TestTrace_AmbiguousPrefixListsCandidates(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	id1 := sealedIntentWithTurns(t, dir, svc, "amb1", []string{"x"})
	id2 := sealedIntentWithTurns(t, dir, svc, "amb2", []string{"y"})

	// Use the literal common prefix `int_` which matches both — the
	// canonical "ambiguous" probe in this corpus.
	_, err := svc.Trace("int_", nil)
	if err == nil {
		t.Fatalf("expected ambiguous-prefix error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ambiguous") {
		t.Fatalf("error should mention 'ambiguous', got: %v", err)
	}
	if !strings.Contains(msg, id1) || !strings.Contains(msg, id2) {
		t.Fatalf("error should list both candidates, got: %v", err)
	}
}

// 9. Limit: --limit caps the timeline; total_turns reports the un-
// truncated count + summary.limit_applied flips.
func TestTrace_LimitTruncatesAndFlagSet(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	id := sealedIntentWithTurns(t, dir, svc, "limit", []string{"a", "b", "c", "d", "e"})

	full, _ := svc.Trace(id, nil)
	if len(full.Turns) < 4 {
		t.Fatalf("test setup expects >=4 turns, got %d", len(full.Turns))
	}

	res, err := svc.Trace(id, &TraceOptions{Limit: 3})
	if err != nil {
		t.Fatalf("trace --limit 3: %v", err)
	}
	if len(res.Turns) != 3 {
		t.Fatalf("limit=3 should yield 3 turns, got %d", len(res.Turns))
	}
	if !res.Summary.LimitApplied {
		t.Fatalf("LimitApplied must be true when --limit truncates")
	}
	if res.Summary.TotalTurns != full.Summary.TotalTurns {
		t.Fatalf("TotalTurns should reflect un-truncated count: got %d, want %d",
			res.Summary.TotalTurns, full.Summary.TotalTurns)
	}
}

// 10. Files-touched paths only (rc7 principle: no diff stats).
func TestTrace_FilesTouchedReturnsPathsOnly(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	id := sealedIntentWithTurns(t, dir, svc, "files", []string{"work"})

	res, err := svc.Trace(id, nil)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	for _, f := range res.Summary.FilesTouched {
		if strings.ContainsAny(f, "+-()") {
			t.Errorf("files_touched entry must be a plain path (no diff stats), got %q", f)
		}
	}
}
