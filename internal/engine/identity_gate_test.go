package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

// freshCloneRepo simulates a `git clone` of a mainline-enabled repo:
// the team config is committed and present, but the per-actor
// identity (.ml-cache/identity.json, gitignored) is absent. This is
// the exact precondition behind the bug report — Init created
// identity locally for the cloning actor, but a SEPARATE clone never
// ran Init and therefore has no local identity.
func freshCloneRepo(t *testing.T) (*Service, func()) {
	t.Helper()
	dir, cleanup := testRepo(t)
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		cleanup()
		t.Fatalf("init: %v", err)
	}
	// Wipe the local identity to simulate a peer clone that hasn't
	// run `mainline init` yet. The team config + .gitignore + AGENTS.md
	// remain — they are committed.
	identityPath := filepath.Join(dir, ".ml-cache", "identity.json")
	if err := os.Remove(identityPath); err != nil && !os.IsNotExist(err) {
		cleanup()
		t.Fatalf("simulate fresh clone (remove identity): %v", err)
	}
	return svc, cleanup
}

func TestIdentityGate_StartRejectsBeforeCreatingDraft(t *testing.T) {
	svc, cleanup := freshCloneRepo(t)
	defer cleanup()

	gitCmd(t, svc.Git.RepoRoot, "checkout", "-b", "feature/no-identity-test")

	_, err := svc.Start("probe missing identity", "")
	if err == nil {
		t.Fatalf("Start must reject when identity is missing; succeeded")
	}
	if !strings.Contains(err.Error(), "identity") {
		t.Fatalf("error should mention identity, got: %v", err)
	}

	// CRITICAL: no draft file should have been created.
	drafts, _ := svc.Store.ListDrafts()
	for _, id := range drafts {
		d, _ := svc.Store.ReadDraft(id)
		if d != nil && d.GitBranch == "feature/no-identity-test" {
			t.Fatalf("Start must not create a draft when identity gate fails; found draft %s", id)
		}
	}
}

func TestIdentityGate_StatusReportsMissingIdentity(t *testing.T) {
	svc, cleanup := freshCloneRepo(t)
	defer cleanup()

	res, err := svc.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !res.Initialized {
		t.Fatalf("expected Initialized=true (team config exists), got false")
	}
	if res.IdentityConfigured {
		t.Fatalf("expected IdentityConfigured=false on fresh clone, got true")
	}
	if res.ActorID != "" {
		t.Fatalf("expected ActorID='' on fresh clone, got %q", res.ActorID)
	}
}

func TestIdentityGate_ContextReportsMissingIdentity(t *testing.T) {
	svc, cleanup := freshCloneRepo(t)
	defer cleanup()

	res, err := svc.Context()
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if res.IdentityConfigured {
		t.Fatalf("expected IdentityConfigured=false, got true")
	}
	if res.ActorID != "" {
		t.Fatalf("expected ActorID='' on fresh clone, got %q", res.ActorID)
	}
}

func TestIdentityGate_SealSubmitDoesNotCorruptDraftWhenIdentityMissing(t *testing.T) {
	// Critical regression: pre-fix, SealSubmit mutated draft to
	// sealed_local BEFORE checking identity. If identity was missing,
	// the draft was left in sealed_local with no actor-log event —
	// unrecoverable via the normal flow. This test asserts the draft
	// stays in `drafting` after a failed SealSubmit due to missing
	// identity, so the user can `mainline init --actor-name <name>`
	// and retry submit cleanly.
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	gitCmd(t, dir, "checkout", "-b", "feature/seal-no-identity")
	start, err := svc.Start("probe identity gate in seal submit", "")
	if err != nil {
		t.Fatalf("start (with identity): %v", err)
	}
	if _, err := svc.Append("did some work"); err != nil {
		t.Fatalf("append: %v", err)
	}
	writeFile(t, dir, "x.go", "package main\n")
	gitCmd(t, dir, "add", "x.go")
	gitCmd(t, dir, "commit", "-m", "x")

	// Now wipe identity to simulate the user's repro: identity.json
	// gone but draft already exists.
	identityPath := filepath.Join(dir, ".ml-cache", "identity.json")
	if err := os.Remove(identityPath); err != nil {
		t.Fatalf("remove identity: %v", err)
	}

	sr := validSealResult(start.IntentID)
	data, _ := json.Marshal(sr)
	_, err = svc.SealSubmit(json.RawMessage(data))
	if err == nil {
		t.Fatalf("SealSubmit must reject when identity is missing; succeeded")
	}
	if !strings.Contains(err.Error(), "identity") {
		t.Fatalf("error should mention identity, got: %v", err)
	}

	// Draft must still be in drafting state — the gate runs BEFORE
	// any mutation, so a retry after `mainline init --actor-name X`
	// would work cleanly.
	draft, err := svc.Store.ReadDraft(start.IntentID)
	if err != nil {
		t.Fatalf("ReadDraft after failed seal: %v", err)
	}
	if draft.Status != domain.StatusDrafting {
		t.Fatalf("draft must remain drafting after failed identity gate; got %s", draft.Status)
	}
}

func TestIdentityGate_EmptyActorIDInIdentityFileRejected(t *testing.T) {
	// A corrupt or partially-written identity file with empty ActorID
	// is the same failure mode as a missing file — it must be rejected
	// the same way, not silently treated as "valid identity".
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	corrupt := &domain.Identity{ActorID: "", ActorName: "no id", CreatedAt: "2026-04-26T00:00:00Z"}
	if err := svc.Store.WriteIdentity(corrupt); err != nil {
		t.Fatalf("write corrupt identity: %v", err)
	}

	gitCmd(t, dir, "checkout", "-b", "feature/empty-actor-id")
	if _, err := svc.Start("should be rejected", ""); err == nil {
		t.Fatalf("Start must reject identity with empty ActorID; succeeded")
	}
}
