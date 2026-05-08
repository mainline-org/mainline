package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/gitops"
)

// ── matchesMergeHeader unit tests ──

func TestMatchesMergeHeader(t *testing.T) {
	tests := []struct {
		name    string
		message string
		branch  string
		want    bool
	}{
		{
			name:    "standard GitHub merge header",
			message: "Merge pull request #107 from mainline-org/feature/pr-intent-comment\n\nfeat: add PR intent comment workflow",
			branch:  "feature/pr-intent-comment",
			want:    true,
		},
		{
			name:    "first line only, no body",
			message: "Merge pull request #99 from owner/fix-bug",
			branch:  "fix-bug",
			want:    true,
		},
		{
			name:    "wrong branch",
			message: "Merge pull request #99 from owner/feature-a",
			branch:  "feature-b",
			want:    false,
		},
		{
			name:    "squash commit — not a merge header",
			message: "docs: add spec drafts v0.1 (#106)",
			branch:  "spec-drafts-v0",
			want:    false,
		},
		{
			name:    "branch name substring — must be exact",
			message: "Merge pull request #1 from org/feature/auth-v2",
			branch:  "feature/auth",
			want:    false,
		},
		{
			name:    "empty message",
			message: "",
			branch:  "main",
			want:    false,
		},
		{
			name:    "empty branch",
			message: "Merge pull request #1 from org/fix",
			branch:  "",
			want:    false,
		},
		{
			name:    "nested org path",
			message: "Merge pull request #42 from some-org/fix/context-files-varargs",
			branch:  "fix/context-files-varargs",
			want:    true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchesMergeHeader(tc.message, tc.branch)
			if got != tc.want {
				t.Errorf("matchesMergeHeader(%q, %q) = %v, want %v", tc.message, tc.branch, got, tc.want)
			}
		})
	}
}

// ── findPinMatchBatched strategy tests ──

func TestFindPinMatchBatched_MergeParent(t *testing.T) {
	branchTip := "aaaa"
	mergeCommit := "bbbb"
	mainParent := "cccc"

	iv := domain.IntentView{
		IntentID:   "int_test",
		Status:     domain.StatusProposed,
		CodeCommit: branchTip,
		GitBranch:  "feature/test",
		Goal:       "unrelated goal text",
	}
	entries := []gitops.LogEntry{
		{Hash: mergeCommit, Subject: "Merge pull request #1 from org/feature/test"},
	}
	ctx := &pinContext{
		treeOf:         map[string]string{mergeCommit: "tree1"},
		intentTreeOf:   map[string]string{branchTip: "tree_different"},
		intentSubjects: map[string]string{branchTip: "feature: original subject"},
		entryMessages:  map[string]string{mergeCommit: "Merge pull request #1 from org/feature/test"},
		noteCache:      map[string]string{},
		commitParents:  map[string][]string{mergeCommit: {mainParent, branchTip}},
	}

	commit, strategy := findPinMatchBatched(iv, entries, ctx)
	if commit != mergeCommit {
		t.Errorf("expected commit %s, got %s", mergeCommit, commit)
	}
	if strategy != "merge_parent" {
		t.Errorf("expected strategy merge_parent, got %s", strategy)
	}
}

func TestFindPinMatchBatched_MergeParent_NotMergeCommit(t *testing.T) {
	iv := domain.IntentView{
		IntentID:   "int_test",
		Status:     domain.StatusProposed,
		CodeCommit: "aaaa",
		GitBranch:  "feature/test",
	}
	entries := []gitops.LogEntry{
		{Hash: "bbbb", Subject: "regular commit"},
	}
	ctx := &pinContext{
		treeOf:         map[string]string{"bbbb": "tree1"},
		intentTreeOf:   map[string]string{"aaaa": "tree_diff"},
		intentSubjects: map[string]string{"aaaa": "different"},
		entryMessages:  map[string]string{"bbbb": "regular commit"},
		noteCache:      map[string]string{},
		commitParents:  map[string][]string{"bbbb": {"cccc"}}, // single parent
	}

	commit, _ := findPinMatchBatched(iv, entries, ctx)
	if commit != "" {
		t.Errorf("expected no match for non-merge commit, got %s", commit)
	}
}

func TestFindPinMatchBatched_BranchInMessage(t *testing.T) {
	mergeCommit := "bbbb"

	iv := domain.IntentView{
		IntentID:   "int_test",
		Status:     domain.StatusProposed,
		CodeCommit: "aaaa",
		GitBranch:  "fix/remove-inherited-pr-noise",
		Goal:       "unrelated chinese goal",
	}
	entries := []gitops.LogEntry{
		{Hash: mergeCommit, Subject: "Merge pull request #108 from mainline-org/fix/remove-inherited-pr-noise"},
	}
	ctx := &pinContext{
		treeOf:         map[string]string{mergeCommit: "tree1"},
		intentTreeOf:   map[string]string{"aaaa": "tree_diff"},
		intentSubjects: map[string]string{"aaaa": "different subject"},
		entryMessages:  map[string]string{mergeCommit: "Merge pull request #108 from mainline-org/fix/remove-inherited-pr-noise\n\nfix: remove inherited constraints from PR surfaces"},
		noteCache:      map[string]string{},
		commitParents:  map[string][]string{mergeCommit: {"cccc"}}, // single parent (won't match merge_parent)
	}

	commit, strategy := findPinMatchBatched(iv, entries, ctx)
	if commit != mergeCommit {
		t.Errorf("expected commit %s, got %s", mergeCommit, commit)
	}
	if strategy != "branch_in_message" {
		t.Errorf("expected strategy branch_in_message, got %s", strategy)
	}
}

func TestFindPinMatchBatched_BranchInMessage_UsesGitBranchNotThread(t *testing.T) {
	mergeCommit := "bbbb"

	iv := domain.IntentView{
		IntentID:   "int_test",
		Status:     domain.StatusProposed,
		CodeCommit: "aaaa",
		Thread:     "different-thread-name",
		GitBranch:  "fix/real-branch",
	}
	entries := []gitops.LogEntry{
		{Hash: mergeCommit, Subject: "Merge pull request #1 from org/fix/real-branch"},
	}
	ctx := &pinContext{
		treeOf:         map[string]string{mergeCommit: "tree1"},
		intentTreeOf:   map[string]string{"aaaa": "tree_diff"},
		intentSubjects: map[string]string{"aaaa": "different"},
		entryMessages:  map[string]string{mergeCommit: "Merge pull request #1 from org/fix/real-branch"},
		noteCache:      map[string]string{},
		commitParents:  map[string][]string{mergeCommit: {"cccc"}},
	}

	commit, strategy := findPinMatchBatched(iv, entries, ctx)
	if commit != mergeCommit {
		t.Errorf("expected match on GitBranch, got commit=%s", commit)
	}
	if strategy != "branch_in_message" {
		t.Errorf("expected branch_in_message, got %s", strategy)
	}
}

func TestFindPinMatchBatched_StrategyPriority(t *testing.T) {
	// When multiple strategies could match, tree_hash wins.
	iv := domain.IntentView{
		IntentID:   "int_test",
		Status:     domain.StatusProposed,
		CodeCommit: "aaaa",
		GitBranch:  "feature/test",
		Goal:       "test goal",
	}
	treeHash := "shared_tree"
	mergeCommit := "bbbb"
	entries := []gitops.LogEntry{
		{Hash: mergeCommit, Subject: "Merge pull request #1 from org/feature/test"},
	}
	ctx := &pinContext{
		treeOf:         map[string]string{mergeCommit: treeHash},
		intentTreeOf:   map[string]string{"aaaa": treeHash}, // tree matches!
		intentSubjects: map[string]string{"aaaa": "Merge pull request #1 from org/feature/test"},
		entryMessages:  map[string]string{mergeCommit: "Merge pull request #1 from org/feature/test\n\ntest goal"},
		noteCache:      map[string]string{},
		commitParents:  map[string][]string{mergeCommit: {"cccc", "aaaa"}},
	}

	_, strategy := findPinMatchBatched(iv, entries, ctx)
	if strategy != "tree_hash" {
		t.Errorf("expected tree_hash to win priority, got %s", strategy)
	}
}

// ── Integration tests: merge_parent and branch_in_message via real git ──

func TestPin_MergeParent_NoFFMerge(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Create feature branch with a commit whose tree differs from main's
	// eventual state (we'll add extra content on main after branching).
	gitCmd(t, dir, "checkout", "-b", "feature/merge-parent-test")
	start, _ := svc.Start("test merge parent", "")
	writeFile(t, dir, "feature.go", "package main\n// feature content\n")
	gitCmd(t, dir, "add", "feature.go")
	gitCmd(t, dir, "commit", "-m", "feat: add feature")

	if _, err := svc.Append("implemented feature"); err != nil {
		t.Fatalf("append: %v", err)
	}
	sr := validSealResult(start.IntentID)
	data, _ := json.Marshal(sr)
	if _, err := svc.SealSubmit(json.RawMessage(data)); err != nil {
		t.Fatalf("seal: %v", err)
	}

	// Merge with --no-ff. The merge commit's second parent is the branch tip.
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "merge", "--no-ff", "feature/merge-parent-test",
		"-m", "Merge pull request #42 from org/feature/merge-parent-test")
	mergeCommit, _ := svc.Git.HeadCommit()

	// Sync triggers auto-pin — merge_parent or branch_in_message should match.
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	note, _ := svc.Git.NotesShow(mergeCommit)
	if !strings.Contains(note, start.IntentID) {
		t.Errorf("merge commit missing intent note for %s — note: %q", start.IntentID, note)
	}
}

func TestPin_BranchInMessage_TreeMismatch(t *testing.T) {
	// Unit-level: verify branch_in_message matches when other strategies
	// would not. In a real --no-ff merge, commit_hash catches the branch
	// tip before branch_in_message runs. But when the branch tip is
	// unavailable (garbage collected, squash, etc.), branch_in_message
	// provides a fallback via the merge header.
	//
	// This test uses the batched function directly to isolate the strategy.
	mergeCommit := "mmmm"
	iv := domain.IntentView{
		IntentID:   "int_test",
		Status:     domain.StatusProposed,
		CodeCommit: "dead_commit", // not in any map
		GitBranch:  "feature/tree-mismatch",
		Goal:       "unrelated",
	}
	entries := []gitops.LogEntry{
		{Hash: mergeCommit, Subject: "Merge pull request #50 from org/feature/tree-mismatch"},
	}
	ctx := &pinContext{
		treeOf:         map[string]string{mergeCommit: "tree_merge"},
		intentTreeOf:   map[string]string{}, // code_commit not found
		intentSubjects: map[string]string{}, // code_commit not found
		entryMessages:  map[string]string{mergeCommit: "Merge pull request #50 from org/feature/tree-mismatch"},
		noteCache:      map[string]string{},
		commitParents:  map[string][]string{mergeCommit: {"parent1", "parent2"}},
	}

	commit, strategy := findPinMatchBatched(iv, entries, ctx)
	if commit != mergeCommit {
		t.Errorf("expected commit %s, got %s", mergeCommit, commit)
	}
	if strategy != "branch_in_message" {
		t.Errorf("expected branch_in_message, got %s", strategy)
	}
}

func TestPin_BranchInMessageSkipsMergeBeforeSeal(t *testing.T) {
	mergeCommit := "mmmm"
	entries := []gitops.LogEntry{
		{
			Hash:    mergeCommit,
			Subject: "Merge pull request #50 from org/feature/reused",
			Date:    "2026-05-08T17:24:12+08:00",
		},
	}
	ctx := &pinContext{
		treeOf:         map[string]string{mergeCommit: "tree_merge"},
		intentTreeOf:   map[string]string{},
		intentSubjects: map[string]string{},
		entryMessages:  map[string]string{mergeCommit: "Merge pull request #50 from org/feature/reused"},
		noteCache:      map[string]string{},
		commitParents:  map[string][]string{mergeCommit: {"parent1", "parent2"}},
	}

	lateIntent := domain.IntentView{
		IntentID:   "int_late",
		Status:     domain.StatusProposed,
		GitBranch:  "feature/reused",
		CodeCommit: "not_on_main",
		SealedAt:   "2026-05-08T17:28:48+08:00",
	}
	commit, strategy := findPinMatchBatched(lateIntent, entries, ctx)
	if commit != "" || strategy != "" {
		t.Fatalf("intent sealed after PR merge should not pin to old merge commit, got %s via %s",
			commit, strategy)
	}

	earlyIntent := lateIntent
	earlyIntent.IntentID = "int_early"
	earlyIntent.SealedAt = "2026-05-08T17:20:00+08:00"
	commit, strategy = findPinMatchBatched(earlyIntent, entries, ctx)
	if commit != mergeCommit || strategy != "branch_in_message" {
		t.Fatalf("intent sealed before PR merge should pin by branch message, got %s via %s",
			commit, strategy)
	}
}

func TestSameTreePinTargetsOnlyDirectNeighbors(t *testing.T) {
	entries := []gitops.LogEntry{
		{Hash: "merge"},
		{Hash: "content"},
		{Hash: "unrelated-same-tree"},
	}
	ctx := &pinContext{
		treeOf: map[string]string{
			"merge":               "tree_final",
			"content":             "tree_final",
			"unrelated-same-tree": "tree_final",
		},
		commitParents: map[string][]string{
			"merge":               {"base", "content"},
			"unrelated-same-tree": {"other-parent"},
		},
	}

	targets := sameTreePinTargets("merge", entries, ctx)
	if len(targets) != 2 || targets[0] != "merge" || targets[1] != "content" {
		t.Fatalf("expected only primary + direct same-tree parent, got %#v", targets)
	}
}

func TestPin_SquashMerge_TreeHashStillWorks(t *testing.T) {
	// Verify that simple squash merges (no divergent main) still get
	// pinned via tree_hash — the existing strategy handles this case.
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	gitCmd(t, dir, "checkout", "-b", "feature/squash-test")
	start, _ := svc.Start("test squash merge", "")
	writeFile(t, dir, "feature.go", "package main\n// squash content\n")
	gitCmd(t, dir, "add", "feature.go")
	gitCmd(t, dir, "commit", "-m", "feat: squash content")

	svc.Append("done")
	sr := validSealResult(start.IntentID)
	data, _ := json.Marshal(sr)
	svc.SealSubmit(json.RawMessage(data))

	// Squash merge — creates a single commit, branch tip orphaned.
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "merge", "--squash", "feature/squash-test")
	gitCmd(t, dir, "commit", "-m", "feat: squash merged (#42)")
	squashCommit, _ := svc.Git.HeadCommit()

	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// tree_hash should match because no divergent commits on main.
	note, _ := svc.Git.NotesShow(squashCommit)
	if !strings.Contains(note, start.IntentID) {
		t.Errorf("squash commit missing intent note for %s — note: %q", start.IntentID, note)
	}
}
