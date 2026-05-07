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
	if got, want := BranchBackedDefaultActorLogRef("actor_test"), "refs/heads/refs/mainline/actors/actor_test"; got != want {
		t.Fatalf("BranchBackedDefaultActorLogRef: got %q want %q", got, want)
	}
	if got, want := BranchBackedDefaultActorLogFetchRefspec("origin"), "+refs/heads/refs/mainline/actors/*:refs/remotes/origin/refs/mainline/actors/*"; got != want {
		t.Fatalf("BranchBackedDefaultActorLogFetchRefspec: got %q want %q", got, want)
	}
	if got, want := BranchBackedActorLogRef("actor_test", "refs/custom/actors"), "refs/heads/refs/custom/actors/actor_test"; got != want {
		t.Fatalf("BranchBackedActorLogRef custom prefix: got %q want %q", got, want)
	}
	if got, want := BranchBackedActorLogFetchRefspec("refs/custom/actors", "upstream"), "+refs/heads/refs/custom/actors/*:refs/remotes/upstream/refs/custom/actors/*"; got != want {
		t.Fatalf("BranchBackedActorLogFetchRefspec custom prefix: got %q want %q", got, want)
	}
	if got := ActorLogPushRefspec(DefaultActorLogPrefix); got != "refs/mainline/actors/*/log:refs/mainline/actors/*/log" {
		t.Fatalf("ActorLogPushRefspec: %q", got)
	}
}
