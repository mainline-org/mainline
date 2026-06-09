package engine

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestImportActorLogAcceptsForkContributorIntentAlongsideMaintainerBackfill(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	initRes, err := svc.Init("maintainer")
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	baseMain := svc.Git.ReadRef("refs/heads/main")
	contributorActor := "actor_jiangge"
	contributorIntent := "int_jiangge_pi"
	contributorBranch := "feature/pi-agent"

	gitCmd(t, dir, "checkout", "-b", contributorBranch, "main")
	writeFile(t, dir, "sources/pi.go", "package sources\n\nfunc PiAgentSession() string { return \"pi\" }\n")
	gitCmd(t, dir, "add", "sources/pi.go")
	gitCmd(t, dir, "commit", "-m", "feat(sources): add Pi agent session support")
	codeCommit := strings.TrimSpace(mustGitRun(t, dir, "rev-parse", "HEAD"))
	codeTree := strings.TrimSpace(mustGitRun(t, dir, "rev-parse", "HEAD^{tree}"))

	importRef := "refs/mainline/imports/" + contributorActor + "/log"
	contributorEvent := domain.IntentSealedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       "evt_jiangge_pi_sealed",
			SchemaVersion: 1,
			EventType:     domain.EventIntentSealed,
			ActorID:       contributorActor,
			ActorName:     "jiangge",
			Timestamp:     "2026-06-01T10:00:00Z",
		},
		IntentID:   contributorIntent,
		Thread:     contributorBranch,
		Goal:       "feat(sources): add Pi agent session support",
		GitBranch:  contributorBranch,
		BaseCommit: baseMain,
		CodeCommit: codeCommit,
		CodeTree:   codeTree,
		Summary: domain.IntentSummary{
			Title:    "Add Pi agent session support",
			What:     "Added Pi agent session source support.",
			Why:      "Sherlog should ingest Pi agent sessions.",
			UserGoal: "feat(sources): add Pi agent session support",
		},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:   []string{"sources"},
			FilesTouched: []string{"sources/pi.go"},
			Tags:         []string{"fork-pr"},
		},
		TurnCount: 1,
		SealedAt:  "2026-06-01T10:05:00Z",
	}
	importHead := writeActorEventCommit(t, svc, contributorEvent)
	if err := svc.Git.UpdateRef(importRef, importHead); err != nil {
		t.Fatalf("write import ref: %v", err)
	}

	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "merge", "--no-ff", contributorBranch, "-m",
		"Merge pull request #56 from jiangge/feature/pi-agent\n\nfeat(sources): add Pi agent session support")
	mergeCommit := strings.TrimSpace(mustGitRun(t, dir, "rev-parse", "HEAD"))

	maintainerIntent := seedMaintainerIntentPinnedToCommit(t, dir, svc, "review-pi", mergeCommit)
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync before import: %v", err)
	}
	before, _ := svc.Store.ReadMainlineView()
	if findIntent(before, contributorIntent) != nil {
		t.Fatalf("contributor intent should not be visible before actor-log import")
	}

	result, err := svc.ImportActorLog(ActorLogImportOptions{
		ActorID:   contributorActor,
		SourceRef: importRef,
	})
	if err != nil {
		t.Fatalf("import actor log: %v", err)
	}
	if !result.Accepted || result.EventCount != 1 || result.SealedIntentCount != 1 {
		t.Fatalf("unexpected import result: %+v", result)
	}
	if result.TargetRef != "refs/mainline/actors/"+contributorActor+"/log" {
		t.Fatalf("wrong target ref: %s", result.TargetRef)
	}
	if svc.Git.ReadRef(result.TargetRef) != importHead {
		t.Fatalf("accepted actor ref head mismatch: got %s want %s", svc.Git.ReadRef(result.TargetRef), importHead)
	}
	if !hasPinnedCommit(result.AutoPinned, contributorIntent, mergeCommit) {
		t.Fatalf("contributor intent should be auto-pinned after accept, got %+v", result.AutoPinned)
	}

	view, _ := svc.Store.ReadMainlineView()
	contrib := findIntent(view, contributorIntent)
	if contrib == nil {
		t.Fatalf("contributor intent missing from view after import")
	}
	if contrib.Status != domain.StatusMerged || contrib.StatusEvidence.MergedMainCommit != mergeCommit {
		t.Fatalf("contributor intent should be merged on PR merge commit, got %+v", contrib.StatusEvidence)
	}
	if contrib.ActorID != contributorActor || contrib.ActorName != "jiangge" {
		t.Fatalf("contributor actor identity lost: %+v", contrib)
	}
	if contrib.Provenance == nil ||
		contrib.Provenance.Kind != "accepted_actor_log" ||
		contrib.Provenance.AuthorSealed != true ||
		contrib.Provenance.Verified != true ||
		contrib.Provenance.AcceptedByActor != initRes.ActorID {
		t.Fatalf("accepted actor-log provenance missing or wrong: %+v", contrib.Provenance)
	}

	noteRaw, _ := svc.Git.NotesShow(mergeCommit)
	var note domain.CommitNote
	if err := json.Unmarshal([]byte(noteRaw), &note); err != nil {
		t.Fatalf("parse merge note: %v\n%s", err, noteRaw)
	}
	if !noteHasIntent(note, maintainerIntent) || !noteHasIntent(note, contributorIntent) {
		t.Fatalf("merge commit note should retain maintainer backfill and contributor intent, got %+v", note.Intents)
	}

	idx, _ := svc.Store.ReadProposedIndex()
	for _, iv := range idx.Proposed {
		if iv.IntentID == contributorIntent {
			t.Fatalf("pinned contributor intent must not pollute review queue: %+v", idx.Proposed)
		}
	}
}

func TestImportActorLogRejectsMismatchedActorID(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("maintainer"); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, _ := svc.getTeamConfig()

	importRef := "refs/mainline/imports/actor_expected/log"
	event := actorRefTestSealedEvent("evt_wrong_actor", "actor_other", "int_wrong", "2026-06-01T00:00:00Z")
	if err := svc.Git.UpdateRef(importRef, writeActorEventCommit(t, svc, event)); err != nil {
		t.Fatalf("write import ref: %v", err)
	}

	if _, err := svc.ImportActorLog(ActorLogImportOptions{
		ActorID:   "actor_expected",
		SourceRef: importRef,
	}); err == nil {
		t.Fatalf("expected actor-id mismatch to fail")
	}
	if got := svc.Git.ReadRef(domain.ActorLogRef("actor_expected", cfg.Mainline.ActorLogPrefix)); got != "" {
		t.Fatalf("mismatched actor log must not be accepted, target ref=%s", got)
	}
}

func TestImportActorLogRejectsForeignAcceptanceEvents(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("maintainer"); err != nil {
		t.Fatalf("init: %v", err)
	}

	actorID := "actor_expected"
	importRef := "refs/mainline/imports/" + actorID + "/log"
	event := domain.ActorLogAcceptedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       "evt_foreign_accept",
			SchemaVersion: 1,
			EventType:     domain.EventActorLogAccepted,
			ActorID:       actorID,
			ActorName:     "fork actor",
			Timestamp:     "2026-06-01T00:00:00Z",
		},
		AcceptedActorID: actorID,
		SourceRef:       "refs/mainline/actors/" + actorID + "/log",
		SourceHead:      "deadbeef",
		TargetRef:       "refs/mainline/actors/" + actorID + "/log",
		Verified:        true,
		AuthorSealed:    true,
	}
	if err := svc.Git.UpdateRef(importRef, writeActorEventCommit(t, svc, event)); err != nil {
		t.Fatalf("write import ref: %v", err)
	}

	if _, err := svc.ImportActorLog(ActorLogImportOptions{
		ActorID:   actorID,
		SourceRef: importRef,
	}); err == nil {
		t.Fatalf("expected foreign accept event to be rejected")
	}
}

func TestImportActorLogRequiresAuthorSealedIntent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("maintainer"); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, _ := svc.getTeamConfig()

	actorID := "actor_no_seal"
	importRef := "refs/mainline/imports/" + actorID + "/log"
	event := domain.IntentAbandonedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       "evt_abandoned_only",
			SchemaVersion: 1,
			EventType:     domain.EventIntentAbandoned,
			ActorID:       actorID,
			Timestamp:     "2026-06-01T00:00:00Z",
		},
		IntentID: "int_missing_seal",
		Reason:   "fixture has no author-sealed intent",
	}
	if err := svc.Git.UpdateRef(importRef, writeActorEventCommit(t, svc, event)); err != nil {
		t.Fatalf("write import ref: %v", err)
	}

	if _, err := svc.ImportActorLog(ActorLogImportOptions{
		ActorID:   actorID,
		SourceRef: importRef,
	}); err == nil {
		t.Fatalf("expected actor log without sealed intent to be rejected")
	}
	if got := svc.Git.ReadRef(domain.ActorLogRef(actorID, cfg.Mainline.ActorLogPrefix)); got != "" {
		t.Fatalf("actor log without sealed intent must not be accepted, target ref=%s", got)
	}
}

func TestImportActorLogRejectsImportedSignalEventsBeforeContextPollution(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("maintainer"); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, _ := svc.getTeamConfig()

	actorID := "actor_signal_smuggle"
	intentID := "int_signal_smuggle"
	importRef := "refs/mainline/imports/" + actorID + "/log"
	sealed := actorRefTestSealedEvent("evt_signal_smuggle_sealed", actorID, intentID, "2026-06-01T00:00:00Z")
	constraint := domain.ConstraintAddedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       "evt_smuggled_constraint",
			SchemaVersion: 1,
			EventType:     domain.EventConstraintAdded,
			ActorID:       actorID,
			Timestamp:     "2026-06-01T00:01:00Z",
		},
		ConstraintID: "guard_smuggled",
		IntentID:     intentID,
		Files:        []string{"internal/engine/actor_import.go"},
		What:         "Do not let fork actor logs create upstream constraints",
		Why:          "Contributor actor logs cross an explicit maintainer trust boundary",
		Severity:     "high",
		Source:       "fork_actor_log",
	}
	head := writeActorEventChain(t, svc, sealed, constraint)
	if err := svc.Git.UpdateRef(importRef, head); err != nil {
		t.Fatalf("write import ref: %v", err)
	}

	if _, err := svc.ImportActorLog(ActorLogImportOptions{
		ActorID:   actorID,
		SourceRef: importRef,
	}); err == nil {
		t.Fatalf("expected imported constraint signal to be rejected")
	}
	if got := svc.Git.ReadRef(domain.ActorLogRef(actorID, cfg.Mainline.ActorLogPrefix)); got != "" {
		t.Fatalf("actor log with imported signal must not be accepted, target ref=%s", got)
	}
	view, _ := svc.Store.ReadMainlineView()
	if view != nil && len(view.Constraints) > 0 {
		t.Fatalf("imported fork signal must not pollute upstream constraints: %+v", view.Constraints)
	}
}

func TestImportActorLogFetchesFromForkRemote(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("maintainer"); err != nil {
		t.Fatalf("init: %v", err)
	}

	forkDir, forkCleanup := testRepo(t)
	defer forkCleanup()
	forkSvc := NewServiceFromRoot(forkDir)
	if _, err := forkSvc.Init("jiangge"); err != nil {
		t.Fatalf("fork init: %v", err)
	}

	actorID := "actor_fork_fetch"
	sourceRef := "refs/mainline/actors/" + actorID + "/log"
	event := actorRefTestSealedEvent("evt_fetch_actor", actorID, "int_fetch_actor", "2026-06-01T00:00:00Z")
	if err := forkSvc.Git.UpdateRef(sourceRef, writeActorEventCommit(t, forkSvc, event)); err != nil {
		t.Fatalf("write fork actor ref: %v", err)
	}

	res, err := svc.ImportActorLog(ActorLogImportOptions{
		ActorID: actorID,
		Remote:  forkDir,
	})
	if err != nil {
		t.Fatalf("import from fork remote: %v", err)
	}
	if res.SourceRef != sourceRef || res.ImportRef != "refs/mainline/imports/"+actorID+"/log" {
		t.Fatalf("unexpected refs: %+v", res)
	}
	if svc.Git.ReadRef(res.ImportRef) == "" || svc.Git.ReadRef(res.TargetRef) == "" {
		t.Fatalf("fetch+accept should populate import and target refs: %+v", res)
	}
}

func TestImportActorLogFetchesForkBranchObjectsForSquashMergePin(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("maintainer"); err != nil {
		t.Fatalf("init: %v", err)
	}
	baseMain := svc.Git.ReadRef("refs/heads/main")

	forkDir := t.TempDir()
	gitCmd(t, dir, "clone", dir, forkDir)
	gitCmd(t, forkDir, "config", "user.email", "fork@test.com")
	gitCmd(t, forkDir, "config", "user.name", "Fork")
	forkSvc := NewServiceFromRoot(forkDir)

	actorID := "actor_squash"
	intentID := "int_squash"
	branch := "feature/squash-pi"
	gitCmd(t, forkDir, "checkout", "-b", branch, "main")
	writeFile(t, forkDir, "sources/pi.go", "package sources\n\nfunc PiAgentSession() string { return \"pi\" }\n")
	gitCmd(t, forkDir, "add", "sources/pi.go")
	gitCmd(t, forkDir, "commit", "-m", "feat(sources): add Pi agent session support")
	codeCommit := strings.TrimSpace(mustGitRun(t, forkDir, "rev-parse", "HEAD"))
	codeTree := strings.TrimSpace(mustGitRun(t, forkDir, "rev-parse", "HEAD^{tree}"))
	sourceRef := "refs/mainline/actors/" + actorID + "/log"
	event := domain.IntentSealedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       "evt_squash_sealed",
			SchemaVersion: 1,
			EventType:     domain.EventIntentSealed,
			ActorID:       actorID,
			ActorName:     "jiangge",
			Timestamp:     "2026-06-01T10:00:00Z",
		},
		IntentID:   intentID,
		Thread:     branch,
		Goal:       "feat(sources): add Pi agent session support",
		GitBranch:  branch,
		BaseCommit: baseMain,
		CodeCommit: codeCommit,
		CodeTree:   codeTree,
		Summary: domain.IntentSummary{
			Title:    "Add Pi agent session support",
			What:     "Added Pi agent session source support.",
			Why:      "Sherlog should ingest Pi agent sessions.",
			UserGoal: "feat(sources): add Pi agent session support",
		},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:   []string{"sources"},
			FilesTouched: []string{"sources/pi.go"},
		},
		TurnCount: 1,
		SealedAt:  "2026-06-01T10:05:00Z",
	}
	if err := forkSvc.Git.UpdateRef(sourceRef, writeActorEventCommit(t, forkSvc, event)); err != nil {
		t.Fatalf("write fork actor ref: %v", err)
	}

	if _, err := svc.Git.Run("cat-file", "-e", codeCommit+"^{commit}"); err == nil {
		t.Fatalf("test setup wrong: upstream should not already have fork code commit %s", codeCommit)
	}

	gitCmd(t, dir, "checkout", "main")
	writeFile(t, dir, "sources/pi.go", "package sources\n\nfunc PiAgentSession() string { return \"pi\" }\n")
	gitCmd(t, dir, "add", "sources/pi.go")
	gitCmd(t, dir, "commit", "-m", "feat(sources): add Pi agent session support (#56)")
	squashCommit := strings.TrimSpace(mustGitRun(t, dir, "rev-parse", "HEAD"))

	res, err := svc.ImportActorLog(ActorLogImportOptions{
		ActorID: actorID,
		Remote:  forkDir,
	})
	if err != nil {
		t.Fatalf("import actor log: %v", err)
	}
	wantImportBranchRef := "refs/mainline/imports/" + actorID + "/branches/" + branch
	if !containsString(res.ImportedBranchRefs, wantImportBranchRef) {
		t.Fatalf("import should retain fork branch ref %s, got %+v", wantImportBranchRef, res.ImportedBranchRefs)
	}
	if len(res.ObjectFetchWarnings) > 0 {
		t.Fatalf("existing fork branch should not produce object fetch warnings: %+v", res.ObjectFetchWarnings)
	}
	if _, err := svc.Git.Run("cat-file", "-e", codeCommit+"^{commit}"); err != nil {
		t.Fatalf("import should fetch fork code commit object for pinning: %v", err)
	}
	if !hasPinnedCommit(res.AutoPinned, intentID, squashCommit) {
		t.Fatalf("squash merge should auto-pin after branch object import, got %+v", res.AutoPinned)
	}
	view, _ := svc.Store.ReadMainlineView()
	contrib := findIntent(view, intentID)
	if contrib == nil || contrib.Status != domain.StatusMerged || contrib.StatusEvidence.MergedMainCommit != squashCommit {
		t.Fatalf("contributor squash intent should be merged on squash commit, got %+v", contrib)
	}
	if contrib.Provenance == nil || !containsString(contrib.Provenance.ImportedBranchRefs, wantImportBranchRef) {
		t.Fatalf("contributor provenance should include imported branch ref, got %+v", contrib.Provenance)
	}
}

func TestImportActorLogWarnsWhenForkBranchCannotBeFetched(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("maintainer"); err != nil {
		t.Fatalf("init: %v", err)
	}

	forkDir, forkCleanup := testRepo(t)
	defer forkCleanup()
	forkSvc := NewServiceFromRoot(forkDir)
	if _, err := forkSvc.Init("jiangge"); err != nil {
		t.Fatalf("fork init: %v", err)
	}

	actorID := "actor_deleted_branch"
	sourceRef := "refs/mainline/actors/" + actorID + "/log"
	event := actorRefTestSealedEvent("evt_deleted_branch", actorID, "int_deleted_branch", "2026-06-01T00:00:00Z")
	event.GitBranch = "feature/deleted-before-import"
	if err := forkSvc.Git.UpdateRef(sourceRef, writeActorEventCommit(t, forkSvc, event)); err != nil {
		t.Fatalf("write fork actor ref: %v", err)
	}

	res, err := svc.ImportActorLog(ActorLogImportOptions{
		ActorID: actorID,
		Remote:  forkDir,
	})
	if err != nil {
		t.Fatalf("missing fork branch should warn, not reject actor log: %v", err)
	}
	if !res.Accepted || len(res.ImportedBranchRefs) != 0 || len(res.ObjectFetchWarnings) != 1 {
		t.Fatalf("expected accepted actor log with one object-fetch warning, got %+v", res)
	}
	view, _ := svc.Store.ReadMainlineView()
	contrib := findIntent(view, "int_deleted_branch")
	if contrib == nil || contrib.Provenance == nil || len(contrib.Provenance.ObjectFetchWarnings) != 1 {
		t.Fatalf("object-fetch warning should survive provenance, got %+v", contrib)
	}
}

func TestImportActorLogPushesImportedForkBranchRefs(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	remoteDir, err := os.MkdirTemp("", "mainline-import-remote-*")
	if err != nil {
		t.Fatalf("remote temp: %v", err)
	}
	defer os.RemoveAll(remoteDir)
	gitCmd(t, remoteDir, "init", "--bare")
	gitCmd(t, dir, "remote", "add", "origin", remoteDir)
	gitCmd(t, dir, "push", "origin", "main")

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("maintainer"); err != nil {
		t.Fatalf("init: %v", err)
	}
	baseMain := svc.Git.ReadRef("refs/heads/main")

	forkDir := t.TempDir()
	gitCmd(t, dir, "clone", dir, forkDir)
	gitCmd(t, forkDir, "config", "user.email", "fork@test.com")
	gitCmd(t, forkDir, "config", "user.name", "Fork")
	forkSvc := NewServiceFromRoot(forkDir)

	actorID := "actor_push_branch"
	branch := "feature/pushed-import"
	gitCmd(t, forkDir, "checkout", "-b", branch, "main")
	writeFile(t, forkDir, "sources/pushed.go", "package sources\n\nfunc PushedImport() {}\n")
	gitCmd(t, forkDir, "add", "sources/pushed.go")
	gitCmd(t, forkDir, "commit", "-m", "feat: pushed import branch")
	codeCommit := strings.TrimSpace(mustGitRun(t, forkDir, "rev-parse", "HEAD"))
	codeTree := strings.TrimSpace(mustGitRun(t, forkDir, "rev-parse", "HEAD^{tree}"))
	sourceRef := "refs/mainline/actors/" + actorID + "/log"
	event := domain.IntentSealedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       "evt_push_branch_sealed",
			SchemaVersion: 1,
			EventType:     domain.EventIntentSealed,
			ActorID:       actorID,
			ActorName:     "jiangge",
			Timestamp:     "2026-06-01T10:00:00Z",
		},
		IntentID:   "int_push_branch",
		Thread:     branch,
		Goal:       "feat: pushed import branch",
		GitBranch:  branch,
		BaseCommit: baseMain,
		CodeCommit: codeCommit,
		CodeTree:   codeTree,
		Summary: domain.IntentSummary{
			Title:    "Pushed import branch",
			What:     "Added a fork branch object transport fixture.",
			Why:      "Upstream import refs must keep contributor code objects reachable.",
			UserGoal: "feat: pushed import branch",
		},
		TurnCount: 1,
		SealedAt:  "2026-06-01T10:05:00Z",
	}
	if err := forkSvc.Git.UpdateRef(sourceRef, writeActorEventCommit(t, forkSvc, event)); err != nil {
		t.Fatalf("write fork actor ref: %v", err)
	}

	res, err := svc.ImportActorLog(ActorLogImportOptions{
		ActorID: actorID,
		Remote:  forkDir,
	})
	if err != nil {
		t.Fatalf("import actor log: %v", err)
	}
	if !res.Pushed {
		t.Fatalf("import should push accepted metadata when origin exists")
	}
	wantRef := "refs/mainline/imports/" + actorID + "/branches/" + branch
	if !containsString(res.ImportedBranchRefs, wantRef) {
		t.Fatalf("expected imported branch ref %s, got %+v", wantRef, res.ImportedBranchRefs)
	}
	if got := strings.TrimSpace(mustGitRun(t, remoteDir, "rev-parse", wantRef)); got != codeCommit {
		t.Fatalf("remote import branch ref mismatch: got %s want %s", got, codeCommit)
	}
}

func TestImportedBranchRefSanitizesUnsafeGitRefNames(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	got := svc.importedBranchRef("actor_bad_ref", "topic/../bad.lock")
	if !strings.HasPrefix(got, "refs/mainline/imports/actor_bad_ref/branches/") {
		t.Fatalf("imported branch ref should stay in import namespace, got %s", got)
	}
	if _, err := svc.Git.Run("check-ref-format", got); err != nil {
		t.Fatalf("sanitized import ref should be a valid git ref: %v", err)
	}
}

func seedMaintainerIntentPinnedToCommit(t *testing.T, dir string, svc *Service, suffix, commit string) string {
	t.Helper()
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "checkout", "-b", "maint/"+suffix)
	start, err := svc.Start("maintainer backfill for "+suffix, "")
	if err != nil {
		t.Fatalf("start maintainer intent: %v", err)
	}
	writeFile(t, dir, "maint-"+suffix+".txt", "maintainer review\n")
	gitCmd(t, dir, "add", "maint-"+suffix+".txt")
	gitCmd(t, dir, "commit", "-m", "chore: maintainer review "+suffix)
	if _, err := svc.Append("reviewed and pinned fork PR contribution"); err != nil {
		t.Fatalf("append maintainer intent: %v", err)
	}
	sr := validSealResult(start.IntentID)
	data, _ := json.Marshal(sr)
	if _, err := svc.SealSubmit(json.RawMessage(data)); err != nil {
		t.Fatalf("seal maintainer intent: %v", err)
	}
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync maintainer intent: %v", err)
	}
	if _, err := svc.PinExplicit(start.IntentID, commit); err != nil {
		t.Fatalf("pin maintainer intent: %v", err)
	}
	gitCmd(t, dir, "checkout", "main")
	return start.IntentID
}

func mustGitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitRunIn(t, dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func findIntent(view *domain.MainlineView, intentID string) *domain.IntentView {
	if view == nil {
		return nil
	}
	for i := range view.Intents {
		if view.Intents[i].IntentID == intentID {
			return &view.Intents[i]
		}
	}
	return nil
}

func noteHasIntent(note domain.CommitNote, intentID string) bool {
	for _, ref := range note.Intents {
		if ref.IntentID == intentID {
			return true
		}
	}
	return false
}

func hasPinnedCommit(links []PinnedCommit, intentID, commit string) bool {
	for _, link := range links {
		if link.IntentID == intentID && link.Commit == commit {
			return true
		}
	}
	return false
}

func writeActorEventChain(t *testing.T, svc *Service, events ...any) string {
	t.Helper()
	parent := ""
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		blobHash, err := svc.Git.HashObject(data)
		if err != nil {
			t.Fatalf("hash event blob: %v", err)
		}
		treeHash, err := svc.Git.MakeTree("event.json", blobHash)
		if err != nil {
			t.Fatalf("make tree: %v", err)
		}
		parent, err = svc.Git.CommitTree(treeHash, parent, "actor-log-event")
		if err != nil {
			t.Fatalf("commit tree: %v", err)
		}
	}
	return parent
}
