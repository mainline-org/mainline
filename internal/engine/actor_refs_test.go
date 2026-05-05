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
	legacyCommit := writeActorEventCommit(t, svc, "", legacyEvent)
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

func writeActorEventCommit(t *testing.T, svc *Service, parent string, event any) string {
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
	commitHash, err := svc.Git.CommitTree(treeHash, parent, "actor-log-event")
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
