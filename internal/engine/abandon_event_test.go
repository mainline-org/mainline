package engine

import (
	"encoding/json"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

// Pre-this-fix Abandon only updated the local draft file; it never
// wrote an IntentAbandonedEvent to the actor log. That meant a
// teammate's Sync would rebuild the view from the actor log and keep
// showing the intent as proposed — silent divergence.
//
// The fix writes the event so view-rebuild on any clone (including
// the abandoning actor's own next sync) classifies it as abandoned.
func TestAbandonProposedWritesActorLogEvent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	gitCmd(t, dir, "checkout", "-b", "feature/to-abandon")
	start, _ := svc.Start("about to be abandoned", "")
	writeFile(t, dir, "abandon_me.go", "package main\n")
	gitCmd(t, dir, "add", "abandon_me.go")
	gitCmd(t, dir, "commit", "-m", "abandon-target")
	if _, err := svc.Append("the work that won't ship"); err != nil {
		t.Fatalf("append: %v", err)
	}

	sr := validSealResult(start.IntentID)
	data, _ := json.Marshal(sr)
	if _, err := svc.SealSubmit(json.RawMessage(data)); err != nil {
		t.Fatalf("seal: %v", err)
	}

	// Abandon and verify the result block reports event id + drafting
	// is NOT the prior status (this is the sealed-local/proposed path).
	res, err := svc.Abandon(start.IntentID, "duplicated by teammate's PR")
	if err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if res.EventID == "" {
		t.Fatalf("expected EventID populated for non-drafting abandon, got empty")
	}
	if res.PriorStatus == string(domain.StatusDrafting) {
		t.Fatalf("expected PriorStatus != drafting, got %s", res.PriorStatus)
	}

	proposals, err := svc.ListProposals()
	if err != nil {
		t.Fatalf("list proposals after abandon: %v", err)
	}
	for _, proposal := range proposals.Proposals {
		if proposal.IntentID == start.IntentID {
			t.Fatalf("abandoned intent still present in proposed index before sync")
		}
	}
	status, err := svc.Status()
	if err != nil {
		t.Fatalf("status after abandon: %v", err)
	}
	if status.ProposedCount != 0 {
		t.Fatalf("expected proposed count to refresh before sync, got %d", status.ProposedCount)
	}

	// Sync rebuilds the view from the actor log — the abandon event
	// must land the intent in StatusAbandoned. If the event was not
	// written, view would still show proposed.
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	view, _ := svc.Store.ReadMainlineView()
	var found *domain.IntentView
	for i, iv := range view.Intents {
		if iv.IntentID == start.IntentID {
			found = &view.Intents[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("intent %s missing from view after abandon+sync", start.IntentID)
	}
	if found.Status != domain.StatusAbandoned {
		t.Fatalf("expected view status abandoned, got %s — likely the actor-log event was not written", found.Status)
	}
}

// Drafting-state abandon is still local-only; the draft files are
// the entire footprint, so we delete them rather than write a
// public event for a never-published intent.
func TestAbandonDraftingDeletesDraft(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	gitCmd(t, dir, "checkout", "-b", "feature/draft-only")
	start, _ := svc.Start("never sealed", "")
	if _, err := svc.Append("some thinking"); err != nil {
		t.Fatalf("append: %v", err)
	}

	res, err := svc.Abandon(start.IntentID, "")
	if err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if res.EventID != "" {
		t.Fatalf("drafting abandon should not write an event, got EventID=%s", res.EventID)
	}
	if res.PriorStatus != string(domain.StatusDrafting) {
		t.Fatalf("expected PriorStatus=drafting, got %s", res.PriorStatus)
	}

	// Draft is gone — Show must NotFound, and starting a new intent
	// on the same branch must succeed (no orphan blocking the slot).
	if _, err := svc.Show(start.IntentID); err == nil {
		t.Fatalf("expected drafting-abandon to delete the draft entirely")
	}
	if _, err := svc.Start("a fresh attempt", ""); err != nil {
		t.Fatalf("should be able to Start after drafting-abandon: %v", err)
	}
}
