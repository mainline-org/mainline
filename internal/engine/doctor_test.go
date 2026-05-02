package engine

import (
	"strings"
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

func TestDoctorProposalsFlagsOnlyAfter72Hours(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("test-agent"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	view := &domain.MainlineView{SchemaVersion: 1, MainBranch: "main", Intents: []domain.IntentView{
		proposedView("int_recent", "recent", now.Add(-71*time.Hour), nil),
		proposedView("int_old", "old", now.Add(-73*time.Hour), nil),
	}}
	if err := svc.Store.WriteMainlineView(view); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Doctor(DoctorOptions{Proposals: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Proposals == nil {
		t.Fatal("expected proposal report")
	}
	if result.Proposals.CheckedProposals != 2 {
		t.Fatalf("expected 2 checked proposals, got %d", result.Proposals.CheckedProposals)
	}
	if len(result.Proposals.Findings) != 1 {
		t.Fatalf("expected one suspicious proposal, got %#v", result.Proposals.Findings)
	}
	f := result.Proposals.Findings[0]
	if f.IntentID != "int_old" {
		t.Fatalf("expected old proposal finding, got %s", f.IntentID)
	}
	if !hasFindingCode(f, "stale_proposed") {
		t.Fatalf("expected stale_proposed finding, got %#v", f.FindingCodes)
	}
	if !strings.Contains(f.RecommendedCommand, "mainline abandon int_old") {
		t.Fatalf("expected abandon recommendation, got %q", f.RecommendedCommand)
	}
}

func TestDoctorProposalsFlagsOrphanCodeCommit(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("test-agent"); err != nil {
		t.Fatal(err)
	}
	view := &domain.MainlineView{SchemaVersion: 1, MainBranch: "main", Intents: []domain.IntentView{
		proposedView("int_orphan", "orphan", time.Now().UTC().Add(-80*time.Hour), []string{"a.go"}),
	}}
	view.Intents[0].CodeCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := svc.Store.WriteMainlineView(view); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Doctor(DoctorOptions{Proposals: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Proposals.Findings) != 1 {
		t.Fatalf("expected one orphan finding, got %#v", result.Proposals.Findings)
	}
	if !hasFindingCode(result.Proposals.Findings[0], "orphan_code_commit") {
		t.Fatalf("expected orphan_code_commit, got %#v", result.Proposals.Findings[0])
	}
}

func TestDoctorProposalsSuggestsLaterMergedOverlap(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("test-agent"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	view := &domain.MainlineView{SchemaVersion: 1, MainBranch: "main", Intents: []domain.IntentView{
		proposedView("int_old", "old proposal", now.Add(-80*time.Hour), []string{"internal/pr.go", "internal/pr_test.go"}),
		mergedView("int_new", "new merged replacement", now.Add(-2*time.Hour), []string{"internal/pr.go", "internal/pr_test.go"}),
	}}
	if err := svc.Store.WriteMainlineView(view); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Doctor(DoctorOptions{Proposals: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Proposals.Findings) != 1 {
		t.Fatalf("expected one finding, got %#v", result.Proposals.Findings)
	}
	f := result.Proposals.Findings[0]
	if !hasFindingCode(f, "later_merged_overlap") {
		t.Fatalf("expected later_merged_overlap, got %#v", f.FindingCodes)
	}
	if len(f.ReplacementHints) != 1 || !strings.Contains(f.ReplacementHints[0], "int_new") {
		t.Fatalf("expected replacement hint for int_new, got %#v", f.ReplacementHints)
	}
}

func TestStatusSuggestsProposalDoctorForOldProposals(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("test-agent"); err != nil {
		t.Fatal(err)
	}
	view := &domain.MainlineView{SchemaVersion: 1, MainBranch: "main", Intents: []domain.IntentView{
		proposedView("int_old", "old proposal", time.Now().UTC().Add(-80*time.Hour), nil),
	}}
	if err := svc.Store.WriteMainlineView(view); err != nil {
		t.Fatal(err)
	}

	status, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.ProposalHealth == nil || status.ProposalHealth.SuspiciousCount != 1 {
		t.Fatalf("expected proposal health warning, got %#v", status.ProposalHealth)
	}
	found := false
	for _, s := range status.Suggestions {
		if strings.Contains(s, "mainline doctor --proposals") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected proposal doctor suggestion, got %#v", status.Suggestions)
	}
}

func proposedView(id, title string, sealedAt time.Time, files []string) domain.IntentView {
	return domain.IntentView{
		IntentID: id,
		Status:   domain.StatusProposed,
		Thread:   "feature/test",
		Goal:     title,
		SealedAt: sealedAt.Format(time.RFC3339),
		Summary:  &domain.IntentSummary{Title: title},
		Fingerprint: &domain.SemanticFingerprint{
			FilesTouched: files,
		},
	}
}

func mergedView(id, title string, sealedAt time.Time, files []string) domain.IntentView {
	iv := proposedView(id, title, sealedAt, files)
	iv.Status = domain.StatusMerged
	return iv
}

func hasFindingCode(f DoctorProposalFinding, code string) bool {
	for _, got := range f.FindingCodes {
		if got == code {
			return true
		}
	}
	return false
}
