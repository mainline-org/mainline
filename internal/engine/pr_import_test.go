package engine

import (
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestImportPullRequestIntentDiscoversForkActorAndImportsUniqueMatch(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("maintainer"); err != nil {
		t.Fatalf("init: %v", err)
	}

	forkDir := cloneForkForPRImport(t, dir)
	branch := "feature/pr-import-unique"
	codeCommit, codeTree := seedForkPRBranch(t, forkDir, branch, "sources/pr_import_unique.go")
	actorID := "actor_pr_import_unique"
	intentID := "int_pr_import_unique"
	writeForkActorLog(t, svc, forkDir, actorID, intentID, branch, codeCommit, codeTree)

	result, err := svc.ImportPullRequestIntent(PullRequestImportOptions{
		PRNumber: 56,
		ForkURL:  forkDir,
		HeadRef:  branch,
		HeadSHA:  codeCommit,
	})
	if err != nil {
		t.Fatalf("pr import: %v", err)
	}
	if result.Status != "imported" || result.Selected == nil || result.Import == nil || !result.Import.Accepted {
		t.Fatalf("expected imported unique match, got %+v", result)
	}
	if result.Selected.ActorID != actorID || result.Selected.IntentID != intentID {
		t.Fatalf("selected wrong candidate: %+v", result.Selected)
	}
	if result.Selected.Score != 180 {
		t.Fatalf("expected commit+tree+branch match score 180, got %+v", result.Selected)
	}
	if !containsString(result.Selected.MatchReasons, "code_commit") ||
		!containsString(result.Selected.MatchReasons, "code_tree") ||
		!containsString(result.Selected.MatchReasons, "git_branch") {
		t.Fatalf("missing match reasons: %+v", result.Selected.MatchReasons)
	}
	if got := svc.Git.ReadRef(domain.ActorLogRef(actorID, domain.DefaultActorLogPrefix)); got == "" {
		t.Fatalf("accepted contributor actor ref should exist")
	}
	view, _ := svc.Store.ReadMainlineView()
	if findIntent(view, intentID) == nil {
		t.Fatalf("imported contributor intent missing from view")
	}
}

func TestImportPullRequestIntentNoMatchDoesNotAcceptActorLog(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("maintainer"); err != nil {
		t.Fatalf("init: %v", err)
	}

	forkDir := cloneForkForPRImport(t, dir)
	branch := "feature/pr-import-no-match"
	codeCommit, _ := seedForkPRBranch(t, forkDir, branch, "sources/pr_import_no_match.go")
	actorID := "actor_pr_import_no_match"
	writeForkActorLog(t, svc, forkDir, actorID, "int_pr_import_no_match", "feature/unrelated", strings.Repeat("a", 40), "")

	result, err := svc.ImportPullRequestIntent(PullRequestImportOptions{
		PRNumber: 57,
		ForkURL:  forkDir,
		HeadRef:  branch,
		HeadSHA:  codeCommit,
	})
	if err != nil {
		t.Fatalf("pr import: %v", err)
	}
	if result.Status != "no_match" || result.CandidateNum != 0 || result.Import != nil {
		t.Fatalf("expected no_match without import, got %+v", result)
	}
	if got := svc.Git.ReadRef(domain.ActorLogRef(actorID, domain.DefaultActorLogPrefix)); got != "" {
		t.Fatalf("no_match must not accept actor log, got target ref %s", got)
	}
}

func TestImportPullRequestIntentRejectsBranchOnlyMatchWhenHeadSHAIsKnown(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("maintainer"); err != nil {
		t.Fatalf("init: %v", err)
	}

	forkDir := cloneForkForPRImport(t, dir)
	branch := "feature/pr-import-branch-only"
	codeCommit, _ := seedForkPRBranch(t, forkDir, branch, "sources/pr_import_branch_only.go")
	actorID := "actor_pr_import_branch_only"
	writeForkActorLog(t, svc, forkDir, actorID, "int_pr_import_branch_only", branch, strings.Repeat("b", 40), "")

	result, err := svc.ImportPullRequestIntent(PullRequestImportOptions{
		PRNumber: 60,
		ForkURL:  forkDir,
		HeadRef:  branch,
		HeadSHA:  codeCommit,
	})
	if err != nil {
		t.Fatalf("pr import: %v", err)
	}
	if result.Status != "no_match" || result.Import != nil {
		t.Fatalf("branch-only match with known head sha should not import, got %+v", result)
	}
	if got := svc.Git.ReadRef(domain.ActorLogRef(actorID, domain.DefaultActorLogPrefix)); got != "" {
		t.Fatalf("branch-only match must not accept actor log, got target ref %s", got)
	}
}

func TestImportPullRequestIntentAmbiguousMatchDoesNotAcceptActorLog(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("maintainer"); err != nil {
		t.Fatalf("init: %v", err)
	}

	forkDir := cloneForkForPRImport(t, dir)
	branch := "feature/pr-import-ambiguous"
	codeCommit, codeTree := seedForkPRBranch(t, forkDir, branch, "sources/pr_import_ambiguous.go")
	actorA := "actor_pr_import_ambiguous_a"
	actorB := "actor_pr_import_ambiguous_b"
	writeForkActorLog(t, svc, forkDir, actorA, "int_pr_import_ambiguous_a", branch, codeCommit, codeTree)
	writeForkActorLog(t, svc, forkDir, actorB, "int_pr_import_ambiguous_b", branch, codeCommit, codeTree)

	result, err := svc.ImportPullRequestIntent(PullRequestImportOptions{
		PRNumber: 58,
		ForkURL:  forkDir,
		HeadRef:  branch,
		HeadSHA:  codeCommit,
	})
	if err != nil {
		t.Fatalf("pr import: %v", err)
	}
	if result.Status != "ambiguous" || result.CandidateNum != 2 || result.Import != nil {
		t.Fatalf("expected ambiguous without import, got %+v", result)
	}
	for _, actorID := range []string{actorA, actorB} {
		if got := svc.Git.ReadRef(domain.ActorLogRef(actorID, domain.DefaultActorLogPrefix)); got != "" {
			t.Fatalf("ambiguous match must not accept actor log for %s, got %s", actorID, got)
		}
	}
}

func TestImportPullRequestIntentIsIdempotentAfterManualActorImport(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("maintainer"); err != nil {
		t.Fatalf("init: %v", err)
	}

	forkDir := cloneForkForPRImport(t, dir)
	branch := "feature/pr-import-idempotent"
	codeCommit, codeTree := seedForkPRBranch(t, forkDir, branch, "sources/pr_import_idempotent.go")
	actorID := "actor_pr_import_idempotent"
	intentID := "int_pr_import_idempotent"
	writeForkActorLog(t, svc, forkDir, actorID, intentID, branch, codeCommit, codeTree)

	manual, err := svc.ImportActorLog(ActorLogImportOptions{
		ActorID: actorID,
		Remote:  forkDir,
	})
	if err != nil {
		t.Fatalf("manual actor import: %v", err)
	}
	if !manual.Accepted {
		t.Fatalf("manual import should accept actor log: %+v", manual)
	}

	result, err := svc.ImportPullRequestIntent(PullRequestImportOptions{
		PRNumber: 59,
		ForkURL:  forkDir,
		HeadRef:  branch,
		HeadSHA:  codeCommit,
	})
	if err != nil {
		t.Fatalf("pr import: %v", err)
	}
	if result.Status != "already_imported" || result.Selected == nil || result.Import == nil || result.Import.Accepted {
		t.Fatalf("expected already_imported after manual import, got %+v", result)
	}
	if result.Selected.IntentID != intentID {
		t.Fatalf("selected wrong intent: %+v", result.Selected)
	}
}

func TestImportActorLogRejectsUnexpectedSourceHead(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("maintainer"); err != nil {
		t.Fatalf("init: %v", err)
	}

	forkDir := cloneForkForPRImport(t, dir)
	branch := "feature/pr-import-race"
	codeCommit, codeTree := seedForkPRBranch(t, forkDir, branch, "sources/pr_import_race.go")
	actorID := "actor_pr_import_race"
	sourceRef := domain.ActorLogRef(actorID, domain.DefaultActorLogPrefix)
	writeForkActorLog(t, svc, forkDir, actorID, "int_pr_import_race", branch, codeCommit, codeTree)
	expectedHead := strings.TrimSpace(mustGitRun(t, forkDir, "rev-parse", sourceRef))

	writeForkActorLog(t, svc, forkDir, actorID, "int_pr_import_race_changed", branch, codeCommit, codeTree)

	_, err := svc.ImportActorLog(ActorLogImportOptions{
		ActorID:            actorID,
		Remote:             forkDir,
		ExpectedSourceHead: expectedHead,
	})
	if err == nil || !strings.Contains(err.Error(), "changed while importing") {
		t.Fatalf("expected changed source head rejection, got %v", err)
	}
	if got := svc.Git.ReadRef(sourceRef); got != "" {
		t.Fatalf("mismatched source head must not accept actor log, got target ref %s", got)
	}
}

func cloneForkForPRImport(t *testing.T, upstreamDir string) string {
	t.Helper()
	forkDir := t.TempDir()
	gitCmd(t, upstreamDir, "clone", upstreamDir, forkDir)
	gitCmd(t, forkDir, "config", "user.email", "fork@test.com")
	gitCmd(t, forkDir, "config", "user.name", "Fork")
	return forkDir
}

func seedForkPRBranch(t *testing.T, forkDir, branch, file string) (string, string) {
	t.Helper()
	gitCmd(t, forkDir, "checkout", "-b", branch, "main")
	writeFile(t, forkDir, file, "package sources\n\nfunc PRImportFixture() string { return \""+branch+"\" }\n")
	gitCmd(t, forkDir, "add", file)
	gitCmd(t, forkDir, "commit", "-m", "feat: add PR import fixture")
	codeCommit := strings.TrimSpace(mustGitRun(t, forkDir, "rev-parse", "HEAD"))
	codeTree := strings.TrimSpace(mustGitRun(t, forkDir, "rev-parse", "HEAD^{tree}"))
	return codeCommit, codeTree
}

func writeForkActorLog(t *testing.T, upstream *Service, forkDir, actorID, intentID, branch, codeCommit, codeTree string) {
	t.Helper()
	forkSvc := NewServiceFromRoot(forkDir)
	event := actorRefTestSealedEvent("evt_"+intentID+"_sealed", actorID, intentID, "2026-06-01T00:00:00Z")
	event.Thread = branch
	event.GitBranch = branch
	event.BaseCommit = upstream.Git.ReadRef("refs/heads/main")
	event.CodeCommit = codeCommit
	event.CodeTree = codeTree
	event.Goal = "exercise fork PR import automation"
	event.Summary = domain.IntentSummary{
		Title:    "Fork PR import fixture",
		What:     "Recorded a fork contributor intent for PR import automation.",
		Why:      "Upstream automation should import author-sealed intent metadata after merge.",
		UserGoal: "exercise fork PR import automation",
	}
	event.Fingerprint = domain.SemanticFingerprint{
		Subsystems:   []string{"fork-pr-import"},
		FilesTouched: []string{"sources"},
		Tags:         []string{"fork-pr", "action"},
	}
	sourceRef := domain.ActorLogRef(actorID, domain.DefaultActorLogPrefix)
	if err := forkSvc.Git.UpdateRef(sourceRef, writeActorEventCommit(t, forkSvc, event)); err != nil {
		t.Fatalf("write fork actor ref: %v", err)
	}
}
