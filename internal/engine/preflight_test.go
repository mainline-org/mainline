package engine

import (
	"testing"

	"github.com/mainline-org/mainline/internal/core"
	"github.com/mainline-org/mainline/internal/domain"
)

func TestPreflightCleanRepoNoActiveIntentIsOK(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	markSyncedToHead(t, svc)

	res, err := svc.Preflight()
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if res.Level != PreflightLevelOK {
		t.Fatalf("expected ok level, got %+v", res)
	}
	if !res.OKToContinue {
		t.Fatalf("clean repo should be ok to continue: %+v", res)
	}
	if len(res.Findings) != 0 || len(res.Overlaps) != 0 {
		t.Fatalf("clean repo should be quiet: findings=%+v overlaps=%+v", res.Findings, res.Overlaps)
	}
}

func TestPreflightWarnsOnNotesRewriteDrift(t *testing.T) {
	res := buildPreflightResult(preflightInput{
		status: &StatusResult{
			Initialized:        true,
			IdentityConfigured: true,
			NotesHealth: &StatusNotesHealth{
				LikelyHistoryRewrite:     true,
				UnreachableMainlineNotes: 42,
			},
		},
	})

	if res.Level != PreflightLevelWarn || !res.OKToContinue {
		t.Fatalf("notes drift should be a warning, got %+v", res)
	}
	if !hasPreflightFinding(res, PreflightFindingNotesRewriteDrift) {
		t.Fatalf("expected notes rewrite finding, got %+v", res.Findings)
	}
	found := false
	for _, next := range res.RecommendedNext {
		if next == "mainline doctor --notes --json" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected notes doctor recommendation, got %+v", res.RecommendedNext)
	}
}

func TestPreflightBlocksDirtyFileOverlapWithProposedIntent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	markSyncedToHead(t, svc)
	gitCmd(t, dir, "checkout", "-b", "feature/work")
	if _, err := svc.Start("touch shared file", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	writeFile(t, dir, "shared.go", "package shared\n")
	if err := svc.Store.WriteProposedIndex(&domain.ProposedIndex{
		SchemaVersion: 1,
		Proposed: []domain.IntentView{
			preflightIntent("int_prop", domain.StatusProposed, []string{"shared.go"}, ""),
		},
	}); err != nil {
		t.Fatalf("write proposed index: %v", err)
	}

	res, err := svc.Preflight()
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if res.Level != PreflightLevelBlock || res.OKToContinue {
		t.Fatalf("expected block from proposed overlap, got %+v", res)
	}
	if !hasPreflightOverlap(res, PreflightOverlapProposed, "int_prop") {
		t.Fatalf("expected proposed overlap with int_prop, got %+v", res.Overlaps)
	}
}

func TestPreflightDoesNotWarnBranchDriftForNormalFeatureAhead(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	markSyncedToHead(t, svc)
	gitCmd(t, dir, "checkout", "-b", "feature/ahead")
	if _, err := svc.Start("normal feature work", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	writeFile(t, dir, "feature.go", "package feature\n")
	gitCmd(t, dir, "add", "feature.go")
	gitCmd(t, dir, "commit", "-m", "feature: normal branch work")

	res, err := svc.Preflight()
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if res.Level != PreflightLevelOK || !res.OKToContinue {
		t.Fatalf("normal ahead-only feature branch should be quiet, got %+v", res)
	}
	if hasPreflightFinding(res, PreflightFindingBranchDrift) {
		t.Fatalf("ahead-only feature branch must not report branch drift: %+v", res.Findings)
	}
	if !containsString(res.Facts.CommitDiffFiles, "feature.go") {
		t.Fatalf("feature diff should still be tracked for overlap checks, got %+v", res.Facts.CommitDiffFiles)
	}
	if !containsString(res.Facts.CurrentFiles, "feature.go") {
		t.Fatalf("feature file should still be current work, got %+v", res.Facts.CurrentFiles)
	}
}

func TestPreflightBlocksOnlyNewMergedIntentOverlapWhenLocalBehind(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	base, _ := svc.Git.HeadCommit()
	gitCmd(t, dir, "checkout", "-b", "feature/behind")
	if _, err := svc.Start("edit file touched upstream", ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	gitCmd(t, dir, "checkout", "main")
	writeFile(t, dir, "upstream.go", "package upstream\n")
	gitCmd(t, dir, "add", "upstream.go")
	gitCmd(t, dir, "commit", "-m", "upstream change")
	newMain, _ := svc.Git.HeadCommit()

	// This historical merged intent touches the same file, but its
	// merged commit is not in local..synced-main and must not be
	// reported as an upstream drift overlap.
	old := preflightIntent("int_old_merged", domain.StatusMerged, []string{"upstream.go"}, base)
	fresh := preflightIntent("int_new_merged", domain.StatusMerged, []string{"upstream.go"}, newMain)
	if err := svc.Store.WriteMainlineView(&domain.MainlineView{
		SchemaVersion: 1,
		MainBranch:    "main",
		MainHead:      newMain,
		Intents:       []domain.IntentView{old, fresh},
	}); err != nil {
		t.Fatalf("write view: %v", err)
	}
	if err := svc.Store.WriteLastSync(&domain.LastSync{At: core.Now(), MainHead: newMain}); err != nil {
		t.Fatalf("write last sync: %v", err)
	}

	gitCmd(t, dir, "checkout", "feature/behind")
	writeFile(t, dir, "upstream.go", "package local\n")

	res, err := svc.Preflight()
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if res.Level != PreflightLevelBlock || res.OKToContinue {
		t.Fatalf("expected block from new merged overlap, got %+v", res)
	}
	if !hasPreflightFinding(res, PreflightFindingActiveBaseBehind) {
		t.Fatalf("expected active-base-behind finding, got %+v", res.Findings)
	}
	if !hasPreflightFinding(res, PreflightFindingBranchDrift) {
		t.Fatalf("expected branch drift when synced main has commits missing from local HEAD, got %+v", res.Findings)
	}
	if !hasPreflightOverlap(res, PreflightOverlapUpstreamMerged, "int_new_merged") {
		t.Fatalf("expected upstream merged overlap with int_new_merged, got %+v", res.Overlaps)
	}
	if hasPreflightOverlap(res, PreflightOverlapUpstreamMerged, "int_old_merged") {
		t.Fatalf("old merged history must not be treated as drift overlap: %+v", res.Overlaps)
	}
}

func TestPreflightDoesNotTreatUpstreamOnlyFilesAsCurrentWork(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	base, _ := svc.Git.HeadCommit()
	gitCmd(t, dir, "checkout", "-b", "feature/behind-only")
	if _, err := svc.Start("behind only", ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	gitCmd(t, dir, "checkout", "main")
	writeFile(t, dir, "upstream_only.go", "package upstream\n")
	gitCmd(t, dir, "add", "upstream_only.go")
	gitCmd(t, dir, "commit", "-m", "upstream only")
	newMain, _ := svc.Git.HeadCommit()
	if err := svc.Store.WriteMainlineView(&domain.MainlineView{
		SchemaVersion: 1,
		MainBranch:    "main",
		MainHead:      newMain,
		Intents: []domain.IntentView{
			preflightIntent("int_upstream_only", domain.StatusMerged, []string{"upstream_only.go"}, newMain),
		},
	}); err != nil {
		t.Fatalf("write view: %v", err)
	}
	if err := svc.Store.WriteLastSync(&domain.LastSync{At: core.Now(), MainHead: newMain}); err != nil {
		t.Fatalf("write last sync: %v", err)
	}

	gitCmd(t, dir, "checkout", "feature/behind-only")
	res, err := svc.Preflight()
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if len(res.Facts.CurrentFiles) != 0 {
		t.Fatalf("upstream-only files must not be current work; got %v (base %s)", res.Facts.CurrentFiles, base)
	}
	if hasPreflightOverlap(res, PreflightOverlapUpstreamMerged, "int_upstream_only") {
		t.Fatalf("upstream-only changes must not create overlap: %+v", res.Overlaps)
	}
	if !hasPreflightFinding(res, PreflightFindingActiveBaseBehind) {
		t.Fatalf("branch/base drift should still be reported, got %+v", res.Findings)
	}
}

func TestPreflightWarnsWhenDirtyOnlyWouldProduceWeakSealEvidence(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	markSyncedToHead(t, svc)
	gitCmd(t, dir, "checkout", "-b", "feature/dirty-only")
	if _, err := svc.Start("dirty only", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	writeFile(t, dir, "scratch.go", "package scratch\n")

	res, err := svc.Preflight()
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if res.Level != PreflightLevelWarn || !res.OKToContinue {
		t.Fatalf("expected advisory warn, got %+v", res)
	}
	if !hasPreflightFinding(res, PreflightFindingDirtyWithoutCommitDiff) {
		t.Fatalf("expected dirty-only finding, got %+v", res.Findings)
	}
}

func TestPreflightIgnoresNonProposedAndNonNewMergedStatusesForOverlap(t *testing.T) {
	current := []string{"shared.go"}
	upstreamCommits := map[string]bool{
		"new-main": true,
	}
	in := preflightInput{
		status:       &StatusResult{Initialized: true, IdentityConfigured: true, LocalHead: "local", MainHead: "new-main"},
		currentFiles: current,
		proposed: []domain.IntentView{
			preflightIntent("int_abandoned", domain.StatusAbandoned, current, ""),
			preflightIntent("int_superseded", domain.StatusSuperseded, current, ""),
			preflightIntent("int_reverted", domain.StatusReverted, current, ""),
		},
		view: &domain.MainlineView{Intents: []domain.IntentView{
			preflightIntent("int_merged_old", domain.StatusMerged, current, "old-main"),
			preflightIntent("int_abandoned_view", domain.StatusAbandoned, current, "new-main"),
			preflightIntent("int_superseded_view", domain.StatusSuperseded, current, "new-main"),
			preflightIntent("int_reverted_view", domain.StatusReverted, current, "new-main"),
		}},
		upstreamCommits: upstreamCommits,
	}

	res := buildPreflightResult(in)
	if len(res.Overlaps) != 0 {
		t.Fatalf("non-proposed/non-new-merged statuses must not overlap, got %+v", res.Overlaps)
	}
}

func markSyncedToHead(t *testing.T, svc *Service) {
	t.Helper()
	head, _ := svc.Git.HeadCommit()
	if err := svc.Store.WriteMainlineView(&domain.MainlineView{
		SchemaVersion: 1,
		RebuiltAt:     core.Now(),
		MainBranch:    "main",
		MainHead:      head,
	}); err != nil {
		t.Fatalf("write view: %v", err)
	}
	if err := svc.Store.WriteLastSync(&domain.LastSync{
		At:       core.Now(),
		ByActor:  "agent",
		MainHead: head,
	}); err != nil {
		t.Fatalf("write last sync: %v", err)
	}
}

func preflightIntent(id string, status domain.IntentStatus, files []string, mergedCommit string) domain.IntentView {
	return domain.IntentView{
		IntentID: id,
		Status:   status,
		Goal:     id,
		Summary: &domain.IntentSummary{
			Title: id,
			What:  "what",
			Why:   "why",
		},
		Fingerprint: &domain.SemanticFingerprint{FilesTouched: append([]string{}, files...)},
		StatusEvidence: domain.StatusEvidence{
			MergedMainCommit: mergedCommit,
		},
	}
}

// Goal-text overlap is the duplicate-work-in-flight signal that
// fires *before* any code is written, when the worktree is clean and
// file-overlap detection has nothing to bite on. The motivating
// scenario: agent runs `mainline start "make user_goal authoritative"`
// on a fresh main, unaware that another teammate already proposed
// a fix with the same goal text. Without this signal the agent burns
// 30+ minutes building a duplicate.
func TestPreflightWarnsGoalTextOverlapEvenWithCleanWorktree(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	markSyncedToHead(t, svc)
	gitCmd(t, dir, "checkout", "-b", "feature/local")

	// Active draft on the local branch — same words an existing
	// proposed intent already used.
	if _, err := svc.Start("Make user_goal authoritative across seal and Hub", ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	other := preflightIntent("int_other", domain.StatusProposed, []string{"some/file.go"}, "")
	other.ActorID = "actor_someone_else"
	other.ActorName = "z2z23n0"
	other.Goal = "let user_goal flow only from mainline start; seal and Hub mirror it"
	other.Summary.Title = "Make user_goal authoritative"
	if err := svc.Store.WriteProposedIndex(&domain.ProposedIndex{
		SchemaVersion: 1,
		Proposed:      []domain.IntentView{other},
	}); err != nil {
		t.Fatalf("write proposed index: %v", err)
	}

	res, err := svc.Preflight()
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	// Worktree is clean (no edits yet) — the only overlap we expect
	// is goal-text. Block-level overlaps would mask a duplicate-of-
	// my-own-work bug.
	var goalOverlap *PreflightOverlap
	for i := range res.Overlaps {
		if res.Overlaps[i].Kind == PreflightOverlapGoalText {
			goalOverlap = &res.Overlaps[i]
			break
		}
	}
	if goalOverlap == nil {
		t.Fatalf("expected goal_text_overlap, got: %+v", res.Overlaps)
	}
	if goalOverlap.IntentID != "int_other" {
		t.Errorf("matched wrong intent: %+v", goalOverlap)
	}
	if goalOverlap.AuthorName != "z2z23n0" {
		t.Errorf("author should be surfaced for goal_text_overlap, got %q", goalOverlap.AuthorName)
	}
	if len(goalOverlap.MatchedKeywords) == 0 {
		t.Errorf("matched_keywords should be populated, got %+v", goalOverlap)
	}
	// Warn level — same goal words can mean a related-but-distinct
	// intent. We surface, but never block.
	if goalOverlap.Level != PreflightLevelWarn {
		t.Errorf("expected warn level, got %q", goalOverlap.Level)
	}
	if !res.OKToContinue {
		t.Errorf("warn-level overlap must not block ok_to_continue")
	}
}

// Self-exclusion: the active draft must never match itself, even when
// it appears in the proposed list (e.g. a re-sync after a publish).
// Without this guard the agent gets warned about its own work, which
// trains them to ignore the warning.
func TestPreflightGoalTextOverlapExcludesSelf(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	markSyncedToHead(t, svc)
	gitCmd(t, dir, "checkout", "-b", "feature/local")

	startRes, err := svc.Start("rewrite the cache layer with proper LRU semantics", "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	self := preflightIntent(startRes.IntentID, domain.StatusProposed, nil, "")
	self.Goal = "rewrite the cache layer with proper LRU semantics"
	self.Summary.Title = "rewrite cache layer LRU"
	if err := svc.Store.WriteProposedIndex(&domain.ProposedIndex{
		SchemaVersion: 1,
		Proposed:      []domain.IntentView{self},
	}); err != nil {
		t.Fatalf("write proposed index: %v", err)
	}

	res, err := svc.Preflight()
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	for _, o := range res.Overlaps {
		if o.IntentID == startRes.IntentID {
			t.Errorf("active draft matched itself in goal-text overlap: %+v", o)
		}
	}
}

// One- or two-word goals would match too many candidates ("fix" or
// "improve" alone) — preflightGoalOverlapMinKeywords keeps the noise
// floor down. This pins that behavior so a future tweak that lowers
// the threshold has to consciously update both code and test.
func TestPreflightGoalTextOverlapSkipsTooShortGoals(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	markSyncedToHead(t, svc)
	gitCmd(t, dir, "checkout", "-b", "feature/local")
	if _, err := svc.Start("fix bug", ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	other := preflightIntent("int_other", domain.StatusProposed, nil, "")
	other.Goal = "fix bug in the auth handler please"
	other.Summary.Title = "fix bug"
	if err := svc.Store.WriteProposedIndex(&domain.ProposedIndex{
		SchemaVersion: 1,
		Proposed:      []domain.IntentView{other},
	}); err != nil {
		t.Fatalf("write proposed index: %v", err)
	}

	res, err := svc.Preflight()
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	for _, o := range res.Overlaps {
		if o.Kind == PreflightOverlapGoalText {
			t.Errorf("short goal should not produce goal_text_overlap, got %+v", o)
		}
	}
}

func hasPreflightFinding(res *PreflightResult, code string) bool {
	for _, f := range res.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}

func hasPreflightOverlap(res *PreflightResult, kind, id string) bool {
	for _, o := range res.Overlaps {
		if o.Kind == kind && o.IntentID == id {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
