package domain

import "testing"

func TestActorLogDefaultRefsStayOutOfHeads(t *testing.T) {
	if got := NormalizeActorLogPrefix("_mainline/actor"); got != DefaultActorLogPrefix {
		t.Fatalf("legacy prefix should normalize to default hidden refs: got %q want %q", got, DefaultActorLogPrefix)
	}
	if got := NormalizeActorLogPrefix("refs/heads/_mainline/actor/"); got != DefaultActorLogPrefix {
		t.Fatalf("legacy full ref prefix should normalize to default hidden refs: got %q want %q", got, DefaultActorLogPrefix)
	}

	if got, want := ActorLogRef("actor_test", DefaultActorLogPrefix), "refs/mainline/actors/actor_test/log"; got != want {
		t.Fatalf("ActorLogRef: got %q want %q", got, want)
	}
	if got := ActorLogFetchRefspec(DefaultActorLogPrefix, "origin"); got != "+refs/mainline/actors/*/log:refs/remotes/origin/mainline/actors/*/log" {
		t.Fatalf("ActorLogFetchRefspec: %q", got)
	}
	if got := ActorLogPushRefspec(DefaultActorLogPrefix); got != "refs/mainline/actors/*/log:refs/mainline/actors/*/log" {
		t.Fatalf("ActorLogPushRefspec: %q", got)
	}
}
