package engine

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

// -----------------------------------------------------------
// Pure-function tests for the merge primitives.
// -----------------------------------------------------------

func TestMergeIntentRefsKeepsExistingFirstAndDedupes(t *testing.T) {
	existing := []domain.IntentReference{
		{IntentID: "int_a", SealResultHash: "sha256:aa"},
		{IntentID: "int_b", SealResultHash: "sha256:bb"},
	}
	additions := []domain.IntentReference{
		{IntentID: "int_b", SealResultHash: "sha256:newer"}, // dup — must be ignored
		{IntentID: "int_c", SealResultHash: "sha256:cc"},
	}
	got := mergeIntentRefs(existing, additions)
	want := []domain.IntentReference{
		{IntentID: "int_a", SealResultHash: "sha256:aa"},
		{IntentID: "int_b", SealResultHash: "sha256:bb"}, // existing wins
		{IntentID: "int_c", SealResultHash: "sha256:cc"},
	}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestMergeStringSetsDedupesPreservingOrder(t *testing.T) {
	got := mergeStringSets([]string{"x", "y"}, []string{"y", "z", "x", "w"})
	want := []string{"x", "y", "z", "w"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v want %v", got, want)
	}
	if mergeStringSets(nil, nil) != nil {
		t.Error("two nils should return nil")
	}
}

// -----------------------------------------------------------
// Integration test for upsertCommitNote (read-modify-write).
// -----------------------------------------------------------

// The motivating case: two intents land on the same main commit (e.g. a
// later intent's seal-time HEAD coincides with the merge commit of an
// earlier one). Without upsert, the second NotesAdd -f wipes the first
// reference. With upsert, both intents persist on the commit.
func TestUpsertCommitNoteAppendsSecondIntent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	commit, _ := svc.Git.HeadCommit()

	first := domain.CommitNote{
		Intents: []domain.IntentReference{{IntentID: "int_first", SealResultHash: "sha256:1"}},
		AddedAt: "t1", AddedBy: "actor_a", Via: "merge",
	}
	if err := upsertCommitNote(svc.Git, commit, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	second := domain.CommitNote{
		Intents:       []domain.IntentReference{{IntentID: "int_second", SealResultHash: "sha256:2"}},
		AddedAt:       "t2",
		AddedBy:       "actor_b",
		Via:           "reconcile_auto",
		MatchStrategy: "tree_hash",
	}
	if err := upsertCommitNote(svc.Git, commit, second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	raw, _ := svc.Git.NotesShow(commit)
	var note domain.CommitNote
	if err := json.Unmarshal([]byte(raw), &note); err != nil {
		t.Fatalf("parse note: %v", err)
	}
	if note.Kind != "mainline.commit_note" {
		t.Errorf("kind: %q", note.Kind)
	}
	if len(note.Intents) != 2 {
		t.Fatalf("intents: got %d want 2 (%+v)", len(note.Intents), note.Intents)
	}
	if note.Intents[0].IntentID != "int_first" || note.Intents[1].IntentID != "int_second" {
		t.Errorf("intent order/contents: %+v", note.Intents)
	}
	// Latest writer's metadata wins on note-level fields.
	if note.AddedBy != "actor_b" || note.Via != "reconcile_auto" || note.MatchStrategy != "tree_hash" {
		t.Errorf("metadata not from latest writer: %+v", note)
	}
}

func TestUpsertCommitNoteDedupesSameIntent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	commit, _ := svc.Git.HeadCommit()

	note := domain.CommitNote{
		Intents: []domain.IntentReference{{IntentID: "int_x", SealResultHash: "sha256:original"}},
		AddedAt: "t1", AddedBy: "actor_a", Via: "merge",
	}
	upsertCommitNote(svc.Git, commit, note)
	upsertCommitNote(svc.Git, commit, domain.CommitNote{
		Intents: []domain.IntentReference{{IntentID: "int_x", SealResultHash: "sha256:NEWER"}},
		AddedAt: "t2", AddedBy: "actor_b",
	})

	raw, _ := svc.Git.NotesShow(commit)
	var got domain.CommitNote
	json.Unmarshal([]byte(raw), &got)
	if len(got.Intents) != 1 {
		t.Fatalf("dup not collapsed: %+v", got.Intents)
	}
	// First-write wins for per-intent fields (the existing seal_result_hash).
	if got.Intents[0].SealResultHash != "sha256:original" {
		t.Errorf("seal_result_hash should be the original, got %s", got.Intents[0].SealResultHash)
	}
}

func TestUpsertCommitNoteReplacesNonMainlineKind(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	commit, _ := svc.Git.HeadCommit()

	// Pre-existing foreign content on the mainline notes ref.
	svc.Git.NotesAdd(commit, `{"kind":"someone-elses-tool","payload":42}`)

	addition := domain.CommitNote{
		Intents: []domain.IntentReference{{IntentID: "int_a", SealResultHash: "sha256:1"}},
		AddedBy: "actor_a", Via: "merge",
	}
	if err := upsertCommitNote(svc.Git, commit, addition); err != nil {
		t.Fatal(err)
	}

	raw, _ := svc.Git.NotesShow(commit)
	var got domain.CommitNote
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("foreign payload should have been replaced: %v", err)
	}
	if got.Kind != "mainline.commit_note" || len(got.Intents) != 1 {
		t.Errorf("foreign payload not cleanly replaced: %+v", got)
	}
}

// -----------------------------------------------------------
// End-to-end: Reconcile no longer kicks an existing intent off a commit.
// -----------------------------------------------------------

// Replicates the dogfood failure: intent A already has a note on main
// commit C. A second intent B happens to be sealed against a feature
// whose tree, after squash, equals C. Reconcile hits tree_hash on B →
// upsert merges B into C's note instead of replacing A. After sync both
// intents read as merged.
func TestReconcileDoesNotEvictExistingIntent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Intent A: full modern pipeline through ordinary squash merge + sync auto-pin.
	idA, mergeA := seedMergedIntent(t, dir, svc, "evict-A", "evict_a.go")
	_ = mergeA

	// Intent B: seal against current main HEAD (== mergeA), no further commits.
	// Squash of an empty branch keeps tree identical → tree_hash will hit mergeA.
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "checkout", "-b", "feature/evict-B")
	startB, err := svc.Start("evict B", "")
	if err != nil {
		t.Fatalf("start B: %v", err)
	}
	// Touch a file then revert so we have a turn but tree returns to mergeA's state.
	writeFile(t, dir, "evict_a.go", "package main\n")
	gitCmd(t, dir, "add", "evict_a.go")
	gitCmd(t, dir, "commit", "--allow-empty", "-m", "marker")
	svc.Append("marker")

	srB := validSealResult(startB.IntentID)
	dataB, _ := json.Marshal(srB)
	// Use --offline so SealSubmit's internal sync does NOT auto-pin
	// — we want the explicit Sync below to be the one that triggers
	// the upsert path we are testing.
	if _, err := svc.SealSubmitWithOptions(json.RawMessage(dataB),
		&SealSubmitOptions{Offline: true}); err != nil {
		t.Fatalf("seal B: %v", err)
	}

	gitCmd(t, dir, "checkout", "main")
	// v0.2: Sync auto-pins via the strategy cascade and uses
	// upsertCommitNote so the existing intent on the matched commit
	// is preserved instead of overwritten.
	syncRes, err := svc.Sync()
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(syncRes.AutoPinned) != 1 {
		t.Fatalf("expected sync to auto-pin 1 link, got %d (%+v)",
			len(syncRes.AutoPinned), syncRes.AutoPinned)
	}
	target := syncRes.AutoPinned[0].Commit

	// The commit's note must now carry BOTH intents.
	raw, _ := svc.Git.NotesShow(target)
	var note domain.CommitNote
	json.Unmarshal([]byte(raw), &note)
	if len(note.Intents) != 2 {
		t.Fatalf("note should contain both intents after reconcile, got %d (%+v)",
			len(note.Intents), note.Intents)
	}

	// And sync should now show A and B both merged.
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("post-reconcile sync: %v", err)
	}
	view, _ := svc.Store.ReadMainlineView()
	statuses := make(map[string]domain.IntentStatus, len(view.Intents))
	for _, iv := range view.Intents {
		statuses[iv.IntentID] = iv.Status
	}
	if statuses[idA] != domain.StatusMerged {
		t.Errorf("intent A expected merged, got %s", statuses[idA])
	}
	if statuses[startB.IntentID] != domain.StatusMerged {
		t.Errorf("intent B expected merged, got %s", statuses[startB.IntentID])
	}
}
