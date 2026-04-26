package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPin_SameTreeNeighborsCoveredOnPropose verifies the v0.3 fix for
// the GitHub merge-commit + squash-content-commit pair: when a feature
// branch is merged with `git merge --no-ff`, the resulting merge commit
// and the squash content commit share the same tree (the merge added
// no new content). Pin must cover BOTH so coverage shows neither as
// uncovered.
func TestPin_SameTreeNeighborsCoveredOnPropose(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Build a feature branch + content commit, then merge into main
	// with --no-ff so we get a merge commit + content commit pair.
	gitCmd(t, dir, "checkout", "-b", "feature/pair-test")
	start, _ := svc.Start("ship the pair", "")
	writeFile(t, dir, "feature.go", "package main\n")
	gitCmd(t, dir, "add", "feature.go")
	gitCmd(t, dir, "commit", "-m", "feature: ship the pair")
	contentCommit, _ := svc.Git.HeadCommit()

	if _, err := svc.Append("did the work"); err != nil {
		t.Fatalf("append: %v", err)
	}
	sr := validSealResult(start.IntentID)
	data, _ := json.Marshal(sr)
	if _, err := svc.SealSubmit(json.RawMessage(data)); err != nil {
		t.Fatalf("seal: %v", err)
	}

	// Now merge into main with --no-ff. The merge commit's tree will
	// equal contentCommit's tree because no new content is added.
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "merge", "--no-ff", "feature/pair-test", "-m", "Merge pull request #99 from feature/pair-test")
	mergeCommit, _ := svc.Git.HeadCommit()

	if mergeCommit == contentCommit {
		t.Fatalf("test setup wrong: --no-ff should produce a distinct merge commit")
	}

	// Sync runs auto-pin. After the fix, BOTH commits should carry the note.
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	mergeNote, _ := svc.Git.NotesShow(mergeCommit)
	contentNote, _ := svc.Git.NotesShow(contentCommit)

	if !strings.Contains(mergeNote, start.IntentID) {
		t.Errorf("merge commit missing intent note for %s — was: %q", start.IntentID, mergeNote)
	}
	if !strings.Contains(contentNote, start.IntentID) {
		t.Errorf("content commit missing intent note for %s — same-tree expansion failed: %q",
			start.IntentID, contentNote)
	}
}

// TestPin_RetroactiveBackfillForMergedIntent verifies that an intent
// already in `merged` status (note on merge commit only) gets the
// note expanded to its same-tree neighbors on the next Pin run. This
// is the path that backfills coverage for PRs merged before the fix
// landed.
func TestPin_RetroactiveBackfillForMergedIntent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Same setup: feature branch + --no-ff merge.
	gitCmd(t, dir, "checkout", "-b", "feature/retro-test")
	start, _ := svc.Start("retro coverage", "")
	writeFile(t, dir, "retro.go", "package main\n")
	gitCmd(t, dir, "add", "retro.go")
	gitCmd(t, dir, "commit", "-m", "feature: retro work")
	contentCommit, _ := svc.Git.HeadCommit()
	svc.Append("work")
	sr := validSealResult(start.IntentID)
	data, _ := json.Marshal(sr)
	svc.SealSubmit(json.RawMessage(data))

	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "merge", "--no-ff", "feature/retro-test", "-m", "Merge pull request #100")

	// Simulate the pre-fix state: only the merge commit carries the
	// note. Manually wipe the content commit's note (in case auto-pin
	// already wrote it in the v0.3-fix-shipped world this test runs in).
	svc.Sync()
	svc.Git.Run("notes", "--ref=mainline/intents", "remove", contentCommit)

	// Sanity: content commit currently uncovered.
	if note, _ := svc.Git.NotesShow(contentCommit); strings.Contains(note, start.IntentID) {
		t.Fatalf("setup wrong: content commit should have no intent note now, got %q", note)
	}

	// Run sync again. Pin's retroactive arm sees an already-merged
	// intent (merged_main_commit = mergeCommit) and expands the note
	// to the content commit (same tree).
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("retro sync: %v", err)
	}

	contentNote, _ := svc.Git.NotesShow(contentCommit)
	if !strings.Contains(contentNote, start.IntentID) {
		t.Errorf("retroactive backfill failed: content commit still has no intent note — got %q", contentNote)
	}

	// Verify idempotency: a second sync must not duplicate notes.
	beforeRaw := contentNote
	svc.Sync()
	afterRaw, _ := svc.Git.NotesShow(contentCommit)
	if afterRaw != beforeRaw {
		t.Errorf("retroactive expansion not idempotent: note changed across syncs\n before: %s\n after:  %s", beforeRaw, afterRaw)
	}
}
