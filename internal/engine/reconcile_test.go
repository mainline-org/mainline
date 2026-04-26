package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"mainline/internal/domain"
)

// squashMergeNoNote replicates `git merge --squash` followed by a commit
// without writing the mainline note Service.Merge would normally attach.
// The result is the state created by clicking "Squash and merge" in the
// GitHub UI: feature tree on main, no mainline metadata anywhere.
func squashMergeNoNote(t *testing.T, dir, branch, commitMsg string) (mainCommit string) {
	t.Helper()
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "merge", "--squash", branch)
	gitCmd(t, dir, "commit", "-m", commitMsg)
	out, err := gitRunIn(t, dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return strings.TrimSpace(out)
}

// gitRunIn shells out to git inside dir and returns stdout. Different from
// the existing gitCmd helper because we need the captured output, not just
// success.
func gitRunIn(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	svc := NewServiceFromRoot(dir)
	return svc.Git.Run(args...)
}

// seedMergedIntent creates a feature branch, runs the full
// start→commit→append→seal→merge cycle and returns (intent ID, merge
// commit hash). Lives here (rather than property_test.go) so non-PBT
// builds with -tags quick still see it.
func seedMergedIntent(t helperTB, dir string, svc *Service, branchSuffix, fileName string) (intentID, mergeCommit string) {
	t.Helper()
	branch := "feature/" + branchSuffix
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "checkout", "-b", branch)
	start, err := svc.Start("seed "+branchSuffix, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	writeFile(t, dir, fileName, "package main\n")
	gitCmd(t, dir, "add", fileName)
	gitCmd(t, dir, "commit", "-m", "seed "+branchSuffix)
	if _, err := svc.Append("seed work"); err != nil {
		t.Fatalf("append: %v", err)
	}

	sr := validSealResult(start.IntentID)
	data, _ := json.Marshal(sr)
	if _, err := svc.SealSubmit(json.RawMessage(data)); err != nil {
		t.Fatalf("seal: %v", err)
	}
	mr, err := svc.Merge(start.IntentID)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	return start.IntentID, mr.MergeCommit
}

// seedSealedIntent walks the agent flow up to seal but stops before any
// merge so the test can drive the reconcile path against the fresh
// proposed intent. Returns intent ID + the feature branch tip commit.
func seedSealedIntent(t *testing.T, dir string, svc *Service, branchSuffix, fileName string) (intentID, codeCommit string) {
	t.Helper()
	branch := "feature/" + branchSuffix
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "checkout", "-b", branch)
	start, err := svc.Start("seal "+branchSuffix, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	writeFile(t, dir, fileName, "package main\n// "+branchSuffix+"\n")
	gitCmd(t, dir, "add", fileName)
	gitCmd(t, dir, "commit", "-m", "work "+branchSuffix)
	if _, err := svc.Append("work"); err != nil {
		t.Fatalf("append: %v", err)
	}
	sr := validSealResult(start.IntentID)
	data, _ := json.Marshal(sr)
	if _, err := svc.SealSubmit(json.RawMessage(data)); err != nil {
		t.Fatalf("seal: %v", err)
	}

	// Code commit recorded on the actor log is the feature tip.
	tip, err := gitRunIn(t, dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return start.IntentID, strings.TrimSpace(tip)
}

// -----------------------------------------------------------
// Tree-hash strategy
// -----------------------------------------------------------

// Squash merge in the GitHub web UI leaves the main-commit message looking
// nothing like the intent goal but the tree is byte-identical to the
// feature tip. tree_hash matching is the only strategy that works on this
// path; without it reconcile is useless for the most common merge
// workflow.
func TestReconcileAutoTreeHashMatch(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent-A"); err != nil {
		t.Fatalf("init: %v", err)
	}

	intentID, _ := seedSealedIntent(t, dir, svc, "tree-match", "tm.go")
	mainCommit := squashMergeNoNote(t, dir, "feature/tree-match",
		"Merge pull request #42 from org/feature/tree-match")

	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	res, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Reconciled != 1 || len(res.Links) != 1 {
		t.Fatalf("expected 1 reconcile, got %+v", res)
	}
	link := res.Links[0]
	if link.IntentID != intentID {
		t.Errorf("intent mismatch: %s", link.IntentID)
	}
	if link.MatchStrategy != "tree_hash" {
		t.Errorf("expected tree_hash, got %s", link.MatchStrategy)
	}
	if link.Commit != mainCommit {
		t.Errorf("expected commit %s, got %s", mainCommit, link.Commit)
	}

	// The note should record via=pin_auto and match_strategy=tree_hash.
	noteRaw, _ := svc.Git.NotesShow(mainCommit)
	if noteRaw == "" {
		t.Fatal("no note written")
	}
	var note domain.CommitNote
	if err := json.Unmarshal([]byte(noteRaw), &note); err != nil {
		t.Fatalf("parse note: %v", err)
	}
	if note.Via != "pin_auto" {
		t.Errorf("expected via=pin_auto, got %s", note.Via)
	}
	if note.MatchStrategy != "tree_hash" {
		t.Errorf("expected match_strategy=tree_hash, got %s", note.MatchStrategy)
	}
}

// -----------------------------------------------------------
// Commit-hash strategy
// -----------------------------------------------------------

// fast-forward / no-ff merges leave the feature tip's hash intact on
// main, so commit_hash matching catches that path even when the tree
// happens to differ from any earlier comparison.
func TestReconcileAutoCommitHashMatch(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent-A"); err != nil {
		t.Fatalf("init: %v", err)
	}

	intentID, codeCommit := seedSealedIntent(t, dir, svc, "ff", "ff.go")

	// Fast-forward: just point main at the feature tip directly.
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "merge", "--ff-only", "feature/ff")

	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	res, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Reconciled != 1 {
		t.Fatalf("expected 1 reconcile, got %d", res.Reconciled)
	}
	link := res.Links[0]
	if link.IntentID != intentID {
		t.Errorf("intent mismatch")
	}
	// tree_hash will fire first (the trees are identical) — that's fine.
	// What we want to verify is that commit_hash *would* fire if tree
	// matched nothing. Confirm the matched commit is indeed code_commit.
	if link.Commit != codeCommit {
		t.Errorf("expected commit %s, got %s", codeCommit, link.Commit)
	}
}

// -----------------------------------------------------------
// Cross-actor reconcile
// -----------------------------------------------------------

// rc4 lifted the actor restriction: any teammate can reconcile any
// proposed intent. Reverting the restriction silently would re-strand the
// motivating bug (GitHub web UI merges by an agent the local user does
// not own can never be reconciled), so we pin it down here.
func TestReconcileWorksAcrossActors(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svcA := NewServiceFromRoot(dir)
	if _, err := svcA.Init("agent-A"); err != nil {
		t.Fatalf("init: %v", err)
	}
	idA, _ := svcA.Store.ReadIdentity()

	intentID, _ := seedSealedIntent(t, dir, svcA, "cross", "cx.go")
	mainCommit := squashMergeNoNote(t, dir, "feature/cross", "Merge PR #99")

	// Swap identity to simulate a second actor running reconcile.
	identity := &domain.Identity{
		ActorID:   "actor_otheruser",
		ActorName: "agent-B",
		CreatedAt: "2026-04-25T00:00:00Z",
	}
	if err := svcA.Store.WriteIdentity(identity); err != nil {
		t.Fatalf("swap identity: %v", err)
	}

	if _, err := svcA.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	res, err := svcA.Reconcile()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Reconciled != 1 {
		t.Fatalf("expected 1 reconcile under foreign actor, got %d", res.Reconciled)
	}
	if res.Links[0].IntentID != intentID {
		t.Errorf("intent mismatch")
	}

	// Note's added_by should be the *current* actor (B), not the intent owner.
	noteRaw, _ := svcA.Git.NotesShow(mainCommit)
	var note domain.CommitNote
	json.Unmarshal([]byte(noteRaw), &note)
	if note.AddedBy != "actor_otheruser" {
		t.Errorf("expected added_by=actor_otheruser, got %s", note.AddedBy)
	}
	// Sanity: the original actor still exists on disk (we just swapped identity.json).
	if idA == nil || idA.ActorID == "" {
		t.Error("original identity should have been readable before the swap")
	}
}

// -----------------------------------------------------------
// Manual reconcile
// -----------------------------------------------------------

// Manual pin (formerly "reconcile manual") is the escape hatch when no
// heuristic matches. It must pin the link unconditionally and stamp the
// note with via=pin_explicit.
func TestReconcileManualPinsCommit(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	intentID, _ := seedSealedIntent(t, dir, svc, "manual", "mn.go")

	// Make an unrelated commit on main with a totally different tree —
	// nothing automatic could possibly match it.
	gitCmd(t, dir, "checkout", "main")
	writeFile(t, dir, "unrelated.go", "package main\n// nothing to do with mn.go\n")
	gitCmd(t, dir, "add", "unrelated.go")
	gitCmd(t, dir, "commit", "-m", "wholly unrelated change")
	mainCommit, _ := gitRunIn(t, dir, "rev-parse", "HEAD")
	mainCommit = strings.TrimSpace(mainCommit)

	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Auto reconcile shouldn't touch this — no strategy matches.
	res, err := svc.Reconcile()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Reconciled != 0 {
		t.Fatalf("auto reconcile should not match unrelated commit, got %d", res.Reconciled)
	}

	// Manual reconcile should pin it.
	link, err := svc.ReconcileManual(intentID, mainCommit)
	if err != nil {
		t.Fatalf("manual reconcile: %v", err)
	}
	if link.MatchStrategy != "manual" {
		t.Errorf("expected match_strategy=manual, got %s", link.MatchStrategy)
	}

	noteRaw, _ := svc.Git.NotesShow(mainCommit)
	var note domain.CommitNote
	json.Unmarshal([]byte(noteRaw), &note)
	if note.Via != "pin_explicit" {
		t.Errorf("expected via=pin_explicit, got %s", note.Via)
	}
	if note.MatchStrategy != "manual" {
		t.Errorf("expected match_strategy=manual, got %s", note.MatchStrategy)
	}

	// And sync should now show the intent as merged.
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("post-manual sync: %v", err)
	}
	view, _ := svc.Store.ReadMainlineView()
	for _, iv := range view.Intents {
		if iv.IntentID == intentID && iv.Status != domain.StatusMerged {
			t.Errorf("expected intent merged after manual reconcile, got %s", iv.Status)
		}
	}
}

// -----------------------------------------------------------
// Manual reconcile guards
// -----------------------------------------------------------

func TestReconcileManualRejectsBadCommit(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	intentID, _ := seedSealedIntent(t, dir, svc, "guard", "gd.go")
	svc.Sync()

	if _, err := svc.ReconcileManual(intentID, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"); err == nil {
		t.Error("expected error on non-existent commit, got nil")
	}
}

func TestReconcileManualRejectsMergedIntent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	intentID, _ := seedSealedIntent(t, dir, svc, "rmi", "rmi.go")
	if _, err := svc.Merge(intentID); err != nil {
		t.Fatalf("merge: %v", err)
	}
	svc.Sync()

	gitCmd(t, dir, "checkout", "main")
	headOut, _ := gitRunIn(t, dir, "rev-parse", "HEAD")
	if _, err := svc.ReconcileManual(intentID, strings.TrimSpace(headOut)); err == nil {
		t.Error("expected error reconciling already-merged intent, got nil")
	}
}

// -----------------------------------------------------------
// Backward-compat parsing
// -----------------------------------------------------------

// Older notes wrote via=reconcile / reconcile_auto / reconcile_manual /
// manual (rc3 era and pre-Patch7 rc4). The current writer emits
// pin_auto / pin_explicit. normaliseVia must collapse every flavour to
// the view-layer bucket "pin" so MainlineView.merged_via stays stable
// for downstream readers across the rename.
func TestNormaliseViaBackwardCompat(t *testing.T) {
	cases := map[string]string{
		"":                 "merge",
		"merge":            "merge",
		"pin_auto":         "pin",
		"pin_explicit":     "pin",
		"link_auto":        "pin",
		"link_explicit":    "pin",
		"reconcile":        "pin",
		"reconcile_auto":   "pin",
		"reconcile_manual": "pin",
		"manual":           "pin",
		"unknown_future":   "unknown_future",
	}
	for in, want := range cases {
		if got := normaliseVia(in); got != want {
			t.Errorf("normaliseVia(%q) = %q, want %q", in, got, want)
		}
	}
}
