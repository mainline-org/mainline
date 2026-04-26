package engine

import (
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

// scanMainNotes used to walk LogOneline(mainBranch, Check.Lookback)
// — any note attached to a commit older than Lookback was silently
// invisible. The post-fix walks the notes ref directly, so depth into
// main history no longer matters.
//
// This test sets Check.Lookback to a deliberately tiny number, pins
// an intent to a commit that the lookback window would not cover, and
// asserts the view still reports it as merged.
func TestScanMainNotes_HonoursNotesPastLookback(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Seed: one merged intent (the one we want sync to find via its
	// note), then push a stack of unrelated commits onto main so that
	// the seed's merge commit falls outside Check.Lookback.
	intentID, mergeCommit := seedMergedIntent(t, dir, svc, "lookback-test", "lb.go")
	gitCmd(t, dir, "checkout", "main")
	for i := 0; i < 5; i++ {
		writeFile(t, dir, "filler_"+randomTestString(4)+".txt", "filler\n")
		gitCmd(t, dir, "add", ".")
		gitCmd(t, dir, "commit", "-m", "filler")
	}

	// Tighten Lookback to 2: not enough to reach mergeCommit, which
	// now sits 5+ commits back on main.
	cfg, _ := svc.Store.ReadTeamConfig()
	cfg.Check.Lookback = 2
	if err := svc.Store.WriteTeamConfig(cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	view, _ := svc.Store.ReadMainlineView()
	var found *domain.IntentView
	for i := range view.Intents {
		if view.Intents[i].IntentID == intentID {
			found = &view.Intents[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("intent %s missing from view", intentID)
	}
	if found.Status != domain.StatusMerged {
		t.Errorf("intent should be merged despite note past lookback, got %s",
			found.Status)
	}
	if found.StatusEvidence.MergedMainCommit != mergeCommit {
		t.Errorf("merged commit: got %s want %s",
			found.StatusEvidence.MergedMainCommit, mergeCommit)
	}
}

// Reachability filter: a note attached to a commit that is NOT in
// main's ancestry (for example a feature-branch tip that never
// merged, or someone manually pinning a dangling commit) must NOT
// flip the intent to merged. Otherwise unrelated branches could
// pollute the view of mainline state.
func TestScanMainNotes_IgnoresNotesOnNonMainCommits(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Seal an intent on a feature branch but never merge it.
	intentID, _ := seedSealedIntent(t, dir, svc, "off-main", "om.go")

	// Commit on the feature branch — this will be the target of the
	// stray note. main does not contain this commit.
	tip, err := gitRunIn(t, dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	tip = strings.TrimSpace(tip)

	// Hand-write a mainline commit_note onto that feature-branch tip
	// (simulates either a buggy `mainline pin <intent> <commit>` call
	// or a manual `git notes add` to the wrong commit).
	noteJSON := `{"schema_version":1,"kind":"mainline.commit_note","intents":[{"intent_id":"` +
		intentID + `","seal_result_hash":"sha256:fake"}],"added_at":"2026-04-26T00:00:00Z","added_by":"actor_x","via":"manual"}`
	if err := svc.Git.NotesAdd(tip, noteJSON); err != nil {
		t.Fatalf("notes add: %v", err)
	}

	gitCmd(t, dir, "checkout", "main")
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	view, _ := svc.Store.ReadMainlineView()
	for _, iv := range view.Intents {
		if iv.IntentID != intentID {
			continue
		}
		if iv.Status == domain.StatusMerged {
			t.Errorf("intent should NOT be merged — note is on a non-main commit")
		}
		return
	}
	t.Fatalf("intent %s missing from view", intentID)
}
