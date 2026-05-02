package engine

import (
	"encoding/json"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

// TestBackfill_StartCommitsCoversManualCommit covers the v0.3 backfill
// path end-to-end: a commit lands on main without an intent, then
// `mainline start --commits <sha>` + seal + sync's auto-pin must mark
// it covered.
func TestBackfill_StartCommitsCoversManualCommit(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	gitCmd(t, dir, "checkout", "main")

	// Manual commit on main with no intent.
	writeFile(t, dir, "manual.go", "package main\n")
	gitCmd(t, dir, "add", "manual.go")
	gitCmd(t, dir, "commit", "-m", "manual edit, no intent")
	manualCommit, _ := svc.Git.HeadCommit()

	// Coverage should report 1 uncovered.
	view, _ := svc.Store.ReadMainlineView()
	cfg, _ := svc.Store.ReadTeamConfig()
	cfg.Mainline.Skip.Patterns = nil // strip defaults to isolate
	svc.Store.WriteTeamConfig(cfg)
	cfg, _ = svc.Store.ReadTeamConfig()
	if view == nil {
		view = &domain.MainlineView{}
	}
	cov, _ := svc.CoverageWindow(10, view, cfg)
	uncovered := 0
	for _, c := range cov {
		if c.State == CoverageUncovered {
			uncovered++
		}
	}
	if uncovered == 0 {
		t.Fatalf("expected at least 1 uncovered, got coverage=%+v", cov)
	}

	// Now start --commits + seal.
	startResult, err := svc.StartWithOptions("retroactively explain manual edit", "", &StartOptions{
		BackfillCommits: []string{manualCommit},
	})
	if err != nil {
		t.Fatalf("start --commits: %v", err)
	}
	if len(startResult.BackfillCommits) != 1 {
		t.Fatalf("expected 1 backfill commit, got %v", startResult.BackfillCommits)
	}
	if _, err := svc.Append("turn-by-turn after-the-fact description"); err != nil {
		t.Fatalf("append: %v", err)
	}
	sr := validSealResult(startResult.IntentID)
	data, _ := json.Marshal(sr)
	// Skip the snapshot contract here — the test does not run --prepare,
	// it constructs SealResult directly. Legacy permissive path applies.
	if _, err := svc.SealSubmit(json.RawMessage(data)); err != nil {
		t.Fatalf("seal submit: %v", err)
	}

	// Sync auto-pin should attach the new intent's note to manualCommit.
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	view, _ = svc.Store.ReadMainlineView()
	cov, _ = svc.CoverageWindow(10, view, cfg)
	for _, c := range cov {
		if c.Commit == manualCommit {
			if c.State != CoverageCovered {
				t.Fatalf("manual commit %s should be covered after backfill, got %s", manualCommit, c.State)
			}
			found := false
			for _, id := range c.IntentIDs {
				if id == startResult.IntentID {
					found = true
				}
			}
			if !found {
				t.Fatalf("manual commit covered but not by the backfill intent %s; ids=%v",
					startResult.IntentID, c.IntentIDs)
			}
			return
		}
	}
	t.Fatalf("manual commit %s not in coverage window", manualCommit)
}

// TestBackfill_PinIdempotent verifies that backfill pin is idempotent
// across multiple Sync() calls. This regressed when BackfillCommits were
// stored as short hashes but noteCache was keyed by full hashes, causing
// alreadyHasIntentCached to miss every time.
func TestBackfill_PinIdempotent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	gitCmd(t, dir, "checkout", "main")

	// Create a manual commit on main.
	writeFile(t, dir, "idempotent.go", "package main\n")
	gitCmd(t, dir, "add", "idempotent.go")
	gitCmd(t, dir, "commit", "-m", "manual edit for idempotent test")
	manualCommit, _ := svc.Git.HeadCommit()

	// Use a SHORT hash to reproduce the bug scenario.
	shortCommit := manualCommit[:7]

	startResult, err := svc.StartWithOptions("idempotent backfill test", "", &StartOptions{
		BackfillCommits: []string{shortCommit},
	})
	if err != nil {
		t.Fatalf("start --commits: %v", err)
	}

	// BackfillCommits should be normalized to full hash at start time.
	if len(startResult.BackfillCommits) != 1 {
		t.Fatalf("expected 1 backfill commit, got %v", startResult.BackfillCommits)
	}
	if len(startResult.BackfillCommits[0]) < 40 {
		t.Fatalf("backfill commit should be normalized to full hash, got %q", startResult.BackfillCommits[0])
	}

	if _, err := svc.Append("turn description"); err != nil {
		t.Fatalf("append: %v", err)
	}
	sr := validSealResult(startResult.IntentID)
	data, _ := json.Marshal(sr)
	// SealSubmit auto-syncs, which triggers the first Pin().
	if _, err := svc.SealSubmit(json.RawMessage(data)); err != nil {
		t.Fatalf("seal submit: %v", err)
	}

	// The backfill note should exist after SealSubmit's auto-sync.
	if raw, _ := svc.Git.NotesShow(startResult.BackfillCommits[0]); raw == "" {
		t.Fatalf("backfill note should exist after seal submit's auto-sync")
	}

	// Subsequent Sync calls should NOT re-pin the same backfill commit.
	for i := 0; i < 3; i++ {
		syncResult, err := svc.Sync()
		if err != nil {
			t.Fatalf("sync #%d: %v", i+1, err)
		}
		for _, link := range syncResult.AutoPinned {
			if link.IntentID == startResult.IntentID {
				t.Fatalf("sync #%d re-pinned intent %s — backfill is not idempotent", i+1, startResult.IntentID)
			}
		}
	}
}
