package engine

import (
	"testing"
	"time"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestDoctorDeletesOnlyMissingBranchDrafts(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("test-agent"); err != nil {
		t.Fatal(err)
	}

	gitCmd(t, dir, "checkout", "-b", "feature/gone")
	start, err := svc.Start("orphaned work", "")
	if err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "branch", "-D", "feature/gone")

	result, err := svc.Doctor(DoctorOptions{Fix: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.OrphanDrafts) != 1 {
		t.Fatalf("expected one orphan draft, got %d", len(result.OrphanDrafts))
	}
	if result.OrphanDrafts[0].IntentID != start.IntentID {
		t.Fatalf("expected orphan %s, got %s", start.IntentID, result.OrphanDrafts[0].IntentID)
	}
	if len(result.DeletedDrafts) != 1 || result.DeletedDrafts[0] != start.IntentID {
		t.Fatalf("expected deleted draft %s, got %#v", start.IntentID, result.DeletedDrafts)
	}
	if _, err := svc.Store.ReadDraft(start.IntentID); err == nil {
		t.Fatalf("expected draft %s to be deleted", start.IntentID)
	}
}

func TestDoctorReportsStaleCurrentBranchDraftWithoutDeleting(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("test-agent"); err != nil {
		t.Fatal(err)
	}
	start, err := svc.Start("current branch work", "")
	if err != nil {
		t.Fatal(err)
	}

	draft, err := svc.Store.ReadDraft(start.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	draft.LastModifiedAt = old
	if err := svc.Store.WriteDraft(draft); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Doctor(DoctorOptions{Fix: true, StaleAfter: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.OrphanDrafts) != 0 {
		t.Fatalf("expected no orphan drafts, got %#v", result.OrphanDrafts)
	}
	if len(result.StaleDrafts) != 1 {
		t.Fatalf("expected one stale draft, got %d", len(result.StaleDrafts))
	}
	if result.StaleDrafts[0].IntentID != start.IntentID {
		t.Fatalf("expected stale %s, got %s", start.IntentID, result.StaleDrafts[0].IntentID)
	}
	if len(result.DeletedDrafts) != 0 {
		t.Fatalf("expected no deleted drafts, got %#v", result.DeletedDrafts)
	}
	if got, err := svc.Store.ReadDraft(start.IntentID); err != nil || got.Status != domain.StatusDrafting {
		t.Fatalf("expected current branch draft to remain drafting, got %#v err=%v", got, err)
	}
}
