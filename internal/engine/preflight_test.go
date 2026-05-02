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
