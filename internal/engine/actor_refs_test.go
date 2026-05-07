package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestActorLogDefaultRefIsHiddenAndMigratesLegacyParent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	initRes, err := svc.Init("agent")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := svc.getTeamConfig()
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	legacyEvent := actorRefTestEvent("evt_legacy", initRes.ActorID, "int_legacy")
	legacyCommit := writeActorEventCommit(t, svc, legacyEvent)
	legacyRef := domain.LegacyActorLogRef(initRes.ActorID)
	if err := svc.Git.UpdateRef(legacyRef, legacyCommit); err != nil {
		t.Fatalf("write legacy ref: %v", err)
	}

	if err := svc.Store.AppendActorLogEvent(
		initRes.ActorID,
		cfg.Mainline.ActorLogPrefix,
		actorRefTestEvent("evt_hidden", initRes.ActorID, "int_hidden"),
	); err != nil {
		t.Fatalf("append hidden event: %v", err)
	}

	hiddenRef := svc.Store.ActorLogRef(initRes.ActorID, cfg.Mainline.ActorLogPrefix)
	if strings.HasPrefix(hiddenRef, "refs/heads/") {
		t.Fatalf("actor log should not be a branch ref: %s", hiddenRef)
	}
	if got, wantPrefix := hiddenRef, "refs/mainline/actors/"+initRes.ActorID+"/log"; got != wantPrefix {
		t.Fatalf("hidden actor ref: got %q want %q", got, wantPrefix)
	}
	if head := svc.Git.ReadRef(hiddenRef); head == "" {
		t.Fatalf("hidden ref was not written: %s", hiddenRef)
	}
	if parent := firstParent(t, svc, hiddenRef); parent != legacyCommit {
		t.Fatalf("hidden actor log should continue from legacy parent: got %s want %s", parent, legacyCommit)
	}

	rawEvents, err := svc.Store.ReadActorLogEvents(initRes.ActorID, cfg.Mainline.ActorLogPrefix)
	if err != nil {
		t.Fatalf("read hidden events: %v", err)
	}
	if len(rawEvents) != 2 {
		t.Fatalf("expected migrated legacy event plus hidden event, got %d", len(rawEvents))
	}
	for i, want := range []string{"evt_legacy", "evt_hidden"} {
		var evt domain.BaseEvent
		if err := json.Unmarshal(rawEvents[i], &evt); err != nil {
			t.Fatalf("unmarshal event %d: %v", i, err)
		}
		if evt.EventID != want {
			t.Fatalf("event %d: got %q want %q", i, evt.EventID, want)
		}
	}
}

func TestActorLogDefaultRefMigratesBranchBackedDefaultParent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	initRes, err := svc.Init("agent")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := svc.getTeamConfig()
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	branchBackedEvent := actorRefTestEvent("evt_branch_backed", initRes.ActorID, "int_branch_backed")
	branchBackedCommit := writeActorEventCommit(t, svc, branchBackedEvent)
	branchBackedRef := domain.BranchBackedDefaultActorLogRef(initRes.ActorID)
	if err := svc.Git.UpdateRef(branchBackedRef, branchBackedCommit); err != nil {
		t.Fatalf("write branch-backed default ref: %v", err)
	}

	if err := svc.Store.AppendActorLogEvent(
		initRes.ActorID,
		cfg.Mainline.ActorLogPrefix,
		actorRefTestEvent("evt_hidden", initRes.ActorID, "int_hidden"),
	); err != nil {
		t.Fatalf("append hidden event: %v", err)
	}

	hiddenRef := svc.Store.ActorLogRef(initRes.ActorID, cfg.Mainline.ActorLogPrefix)
	if parent := firstParent(t, svc, hiddenRef); parent != branchBackedCommit {
		t.Fatalf("hidden actor log should continue from branch-backed default parent: got %s want %s", parent, branchBackedCommit)
	}
}

func TestCollectAllEventsIncludesBranchBackedDefaultRefs(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	initRes, err := svc.Init("agent")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := svc.getTeamConfig()
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	branchBackedEvent := actorRefTestEvent("evt_branch_backed", initRes.ActorID, "int_branch_backed")
	branchBackedCommit := writeActorEventCommit(t, svc, branchBackedEvent)
	branchBackedRef := domain.BranchBackedDefaultActorLogRef(initRes.ActorID)
	if err := svc.Git.UpdateRef(branchBackedRef, branchBackedCommit); err != nil {
		t.Fatalf("write branch-backed default ref: %v", err)
	}

	rawEvents, err := svc.collectAllEvents(cfg.Mainline.ActorLogPrefix)
	if err != nil {
		t.Fatalf("collect events: %v", err)
	}
	if len(rawEvents) != 1 {
		t.Fatalf("expected one branch-backed event, got %d", len(rawEvents))
	}
	var evt domain.BaseEvent
	if err := json.Unmarshal(rawEvents[0], &evt); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if evt.EventID != "evt_branch_backed" {
		t.Fatalf("event id: got %q want evt_branch_backed", evt.EventID)
	}
}

func TestRebuildViewAppliesCrossRefEventsChronologically(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	initRes, err := svc.Init("agent")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := svc.getTeamConfig()
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	sealedEvent := actorRefTestSealedEvent(
		"evt_sealed",
		initRes.ActorID,
		"int_cross_ref",
		"2026-05-05T00:00:00Z",
	)
	sealedCommit := writeActorEventCommit(t, svc, sealedEvent)
	if err := svc.Git.UpdateRef(domain.LegacyActorLogRef(initRes.ActorID), sealedCommit); err != nil {
		t.Fatalf("write legacy sealed ref: %v", err)
	}

	abandonedEvent := actorRefTestEvent("evt_abandoned", initRes.ActorID, "int_cross_ref")
	abandonedEvent.Timestamp = "2026-05-05T00:01:00Z"
	abandonedCommit := writeActorEventCommit(t, svc, abandonedEvent)
	if err := svc.Git.UpdateRef(domain.BranchBackedDefaultActorLogRef(initRes.ActorID), abandonedCommit); err != nil {
		t.Fatalf("write branch-backed abandoned ref: %v", err)
	}

	view, err := svc.rebuildView(cfg)
	if err != nil {
		t.Fatalf("rebuild view: %v", err)
	}
	var found *domain.IntentView
	for i := range view.Intents {
		if view.Intents[i].IntentID == "int_cross_ref" {
			found = &view.Intents[i]
			break
		}
	}
	if found == nil {
		t.Fatal("expected cross-ref intent in rebuilt view")
	}
	if found.Status != domain.StatusAbandoned {
		t.Fatalf("cross-ref events should apply chronologically; got status %s", found.Status)
	}
	if found.StatusEvidence.AbandonedEventID != "evt_abandoned" {
		t.Fatalf("abandoned event id: got %q want evt_abandoned", found.StatusEvidence.AbandonedEventID)
	}
}

func TestConfiguredPushRefspecPublishesHiddenActorRefsOnly(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	remoteDir := t.TempDir()
	gitCmd(t, remoteDir, "init", "--bare")
	gitCmd(t, dir, "remote", "add", "origin", remoteDir)

	svc := NewServiceFromRoot(dir)
	initRes, err := svc.Init("agent")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := svc.getTeamConfig()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if err := svc.Store.AppendActorLogEvent(
		initRes.ActorID,
		cfg.Mainline.ActorLogPrefix,
		actorRefTestEvent("evt_hidden_push", initRes.ActorID, "int_hidden_push"),
	); err != nil {
		t.Fatalf("append hidden event: %v", err)
	}

	gitCmd(t, dir, "push", "origin")

	hiddenRefs, err := gitRunIn(t, remoteDir, "for-each-ref", "--format=%(refname)", "refs/mainline/actors")
	if err != nil {
		t.Fatalf("list hidden remote refs: %v", err)
	}
	if want := "refs/mainline/actors/" + initRes.ActorID + "/log"; !strings.Contains(hiddenRefs, want) {
		t.Fatalf("remote missing hidden actor ref %q; refs:\n%s", want, hiddenRefs)
	}
	legacyRefs, err := gitRunIn(t, remoteDir, "for-each-ref", "--format=%(refname)", "refs/heads/_mainline/actor")
	if err != nil {
		t.Fatalf("list legacy remote refs: %v", err)
	}
	if strings.TrimSpace(legacyRefs) != "" {
		t.Fatalf("remote should not receive branch-backed actor refs:\n%s", legacyRefs)
	}
}

func actorRefTestEvent(eventID, actorID, intentID string) domain.IntentAbandonedEvent {
	return domain.IntentAbandonedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       eventID,
			SchemaVersion: 1,
			EventType:     domain.EventIntentAbandoned,
			ActorID:       actorID,
			ActorName:     "agent",
			Timestamp:     "2026-05-05T00:00:00Z",
		},
		IntentID: intentID,
		Reason:   "actor ref migration regression",
	}
}

func actorRefTestSealedEvent(eventID, actorID, intentID, timestamp string) domain.IntentSealedEvent {
	return domain.IntentSealedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       eventID,
			SchemaVersion: 1,
			EventType:     domain.EventIntentSealed,
			ActorID:       actorID,
			ActorName:     "agent",
			Timestamp:     timestamp,
		},
		IntentID:   intentID,
		Thread:     "fix/actor-ref-test",
		Goal:       "exercise actor-log ref ordering",
		GitBranch:  "fix/actor-ref-test",
		BaseCommit: "base",
		CodeCommit: "code",
		Summary: domain.IntentSummary{
			Title:    "actor ref ordering",
			What:     "test fixture",
			Why:      "test fixture",
			UserGoal: "exercise actor-log ref ordering",
		},
		Fingerprint: domain.SemanticFingerprint{
			FilesTouched: []string{"internal/engine/actor_refs_test.go"},
		},
		TurnCount: 1,
		SealedAt:  timestamp,
	}
}

func writeActorEventCommit(t *testing.T, svc *Service, event any) string {
	t.Helper()
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
	commitHash, err := svc.Git.CommitTree(treeHash, "", "actor-log-event")
	if err != nil {
		t.Fatalf("commit tree: %v", err)
	}
	return commitHash
}

func firstParent(t *testing.T, svc *Service, ref string) string {
	t.Helper()
	out, err := svc.Git.Run("log", "-n", "1", "--format=%P", ref)
	if err != nil {
		t.Fatalf("read first parent: %v", err)
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
