package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

// rc7+ status-as-daily-entry-point tests. Each block (UnsealedDrafts,
// RecentSealed, Suggestions) has at least one focused case here so a
// regression is caught at the engine layer regardless of CLI rendering.

func TestStatus_UnsealedDraftsSurfacesOtherBranches(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Drafting intent on feature/orphan, then return to main.
	gitCmd(t, dir, "checkout", "-b", "feature/orphan")
	orphan, err := svc.Start("orphaned in another branch", "")
	if err != nil {
		t.Fatalf("start orphan: %v", err)
	}
	if _, err := svc.Append("did some work"); err != nil {
		t.Fatalf("append: %v", err)
	}
	gitCmd(t, dir, "checkout", "main")

	res, err := svc.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(res.UnsealedDrafts) == 0 {
		t.Fatalf("expected the orphan draft to surface in UnsealedDrafts")
	}
	found := false
	for _, d := range res.UnsealedDrafts {
		if d.IntentID == orphan.IntentID {
			found = true
			if d.GitBranch != "feature/orphan" {
				t.Errorf("expected GitBranch=feature/orphan, got %s", d.GitBranch)
			}
			if d.Status != string(domain.StatusDrafting) {
				t.Errorf("expected drafting status, got %s", d.Status)
			}
			if d.TurnCount != 1 {
				t.Errorf("expected 1 turn, got %d", d.TurnCount)
			}
		}
	}
	if !found {
		t.Fatalf("orphan intent %s missing from UnsealedDrafts", orphan.IntentID)
	}

	// Confirm the active draft on the CURRENT branch is NOT
	// double-counted in UnsealedDrafts (it's already shown via
	// ActiveIntent on the main rollup).
	gitCmd(t, dir, "checkout", "-b", "feature/active")
	active, _ := svc.Start("active on current branch", "")

	res, err = svc.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if res.ActiveIntent == nil || res.ActiveIntent.IntentID != active.IntentID {
		t.Fatalf("expected ActiveIntent=%s, got %+v", active.IntentID, res.ActiveIntent)
	}
	for _, d := range res.UnsealedDrafts {
		if d.IntentID == active.IntentID {
			t.Errorf("active draft on current branch should NOT appear in UnsealedDrafts (already in ActiveIntent)")
		}
	}
	if len(res.SiblingWorktreeDrafts) != 0 {
		t.Fatalf("current worktree draft should not appear as sibling draft: %+v", res.SiblingWorktreeDrafts)
	}
}

func TestStatus_SurfacesSiblingWorktreeDrafts(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	linked := filepath.Join(t.TempDir(), "linked-status-draft")
	gitCmd(t, dir, "worktree", "add", "-b", "feature/status-sibling", linked)
	resolvedLinked, err := filepath.EvalSymlinks(linked)
	if err != nil {
		t.Fatalf("resolve linked path: %v", err)
	}
	linkedSvc := NewServiceFromRoot(linked)
	start, err := linkedSvc.Start("sibling status draft", "")
	if err != nil {
		t.Fatalf("start linked draft: %v", err)
	}
	if err := linkedSvc.Store.AppendTurn(&domain.Turn{
		IntentID:    start.IntentID,
		Index:       0,
		CreatedAt:   "2026-05-07T00:00:00Z",
		Description: "progress",
	}); err != nil {
		t.Fatalf("append linked draft turn: %v", err)
	}

	res, err := svc.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(res.SiblingWorktreeDrafts) != 1 {
		t.Fatalf("expected one sibling draft, got %+v", res.SiblingWorktreeDrafts)
	}
	d := res.SiblingWorktreeDrafts[0]
	if d.IntentID != start.IntentID || d.WorktreePath != resolvedLinked || d.TurnCount != 1 {
		t.Fatalf("bad sibling draft summary: %+v", d)
	}
	if !strings.Contains(strings.Join(res.Suggestions, "\n"), resolvedLinked) {
		t.Fatalf("suggestions should point at sibling worktree: %v", res.Suggestions)
	}
}

func TestStatus_RecentSealedListsLatestMerged(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	id1, _ := seedMergedIntent(t, dir, svc, "rec-1", "r1.go")
	id2, _ := seedMergedIntent(t, dir, svc, "rec-2", "r2.go")
	id3, _ := seedMergedIntent(t, dir, svc, "rec-3", "r3.go")
	gitCmd(t, dir, "checkout", "main")
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	res, err := svc.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(res.RecentSealed) == 0 {
		t.Fatalf("expected RecentSealed populated, got empty")
	}
	if len(res.RecentSealed) > statusRecentSealedLimit {
		t.Fatalf("RecentSealed exceeded limit %d, got %d",
			statusRecentSealedLimit, len(res.RecentSealed))
	}
	seen := make(map[string]bool)
	for _, r := range res.RecentSealed {
		seen[r.IntentID] = true
		if r.Status != string(domain.StatusMerged) {
			t.Errorf("RecentSealed entry must have status=merged, got %s for %s",
				r.Status, r.IntentID)
		}
	}
	// At least one of the three seeded ids should be in there.
	if !seen[id1] && !seen[id2] && !seen[id3] {
		t.Fatalf("none of the seeded merged intents (%s, %s, %s) appear in RecentSealed",
			id1, id2, id3)
	}
}

func TestStatus_SuggestionsActiveDraftingIntent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	gitCmd(t, dir, "checkout", "-b", "feature/suggestions")
	svc.Start("test suggestions", "")

	res, err := svc.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	joined := strings.Join(res.Suggestions, "\n")
	if !strings.Contains(joined, "mainline append") {
		t.Errorf("suggestions for drafting intent should mention `mainline append`, got: %v", res.Suggestions)
	}
	if !strings.Contains(joined, "mainline seal --prepare") {
		t.Errorf("suggestions for drafting intent should mention seal --prepare, got: %v", res.Suggestions)
	}
}

func TestStatus_SuggestionsCleanRepoSuggestsStart(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	// On main, no active intent.

	res, err := svc.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	joined := strings.Join(res.Suggestions, " ")
	if !strings.Contains(joined, "mainline start") {
		t.Errorf("clean-repo suggestions should prompt `mainline start`, got: %v", res.Suggestions)
	}
}

// Alpha-walkthrough regression: a draft file may say sealed_local
// while the view (rebuilt from sync's auto-pin) already shows the
// intent as merged. Pre-fix, status surfaced the stale draft as
// "Unsealed intents" and the Suggestions block proposed
// `git checkout … && mainline status # resume <id>` for an intent
// the team had already landed. Status should trust the view.
func TestStatus_UnsealedDraftsIgnoresIntentsAlreadyMergedInView(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	id, _ := seedMergedIntent(t, dir, svc, "view-overrides", "vo.go")
	gitCmd(t, dir, "checkout", "main")
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	res, err := svc.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, d := range res.UnsealedDrafts {
		if d.IntentID == id {
			t.Fatalf("merged-in-view intent %s should not surface in UnsealedDrafts; got %+v", id, d)
		}
	}
	for _, sug := range res.Suggestions {
		if strings.Contains(sug, id) && strings.Contains(sug, "resume") {
			t.Errorf("suggestions should not propose resuming a merged intent; got %q", sug)
		}
	}
}

func TestStatus_SuggestionsResumeOrphanBranchWhenIdle(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	gitCmd(t, dir, "checkout", "-b", "feature/forgotten")
	orphan, _ := svc.Start("orphan needs resuming", "")
	gitCmd(t, dir, "checkout", "main")

	res, err := svc.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	joined := strings.Join(res.Suggestions, "\n")
	if !strings.Contains(joined, "git checkout feature/forgotten") {
		t.Errorf("idle-but-orphan suggestions should propose checking out the orphan branch, got: %v", res.Suggestions)
	}
	if !strings.Contains(joined, orphan.IntentID) {
		t.Errorf("suggestion should reference the orphan intent id %s, got: %v", orphan.IntentID, res.Suggestions)
	}
}

func TestStatus_ActionableItemsBuildsTopInboxWithoutSignalNoise(t *testing.T) {
	status := &StatusResult{
		Initialized:        true,
		IdentityConfigured: true,
		Coverage: &StatusCoverageSummary{
			UncoveredCount: 3,
		},
		ProposalHealth: &StatusProposalHealth{
			StaleAfterHours: 72,
			SuspiciousCount: 1,
		},
		UnsealedDrafts: []StatusUnsealedDraft{
			{
				IntentID:   "int_old",
				GitBranch:  "feature/old",
				Status:     string(domain.StatusDrafting),
				AgeSeconds: 6 * 24 * 3600,
			},
		},
	}

	items := buildStatusActionItems(status)
	if len(items) != 3 {
		t.Fatalf("expected 3 actionable items, got %d: %#v", len(items), items)
	}
	wantKinds := []string{"coverage", "proposal", "draft"}
	for i, want := range wantKinds {
		if items[i].Kind != want {
			t.Fatalf("item %d kind = %q, want %q; items=%#v", i, items[i].Kind, want, items)
		}
		if items[i].Why == "" || items[i].Risk == "" || items[i].RecommendedCommand == "" {
			t.Fatalf("item %d should carry why/risk/command, got %#v", i, items[i])
		}
	}
}

func TestStatus_ActionableItemsPrioritizeNotesRewriteDrift(t *testing.T) {
	status := &StatusResult{
		Initialized:        true,
		IdentityConfigured: true,
		NotesHealth: &domain.NotesHealth{
			LikelyHistoryRewrite:     true,
			UnreachableMainlineNotes: 238,
			RecommendedCommand:       "mainline doctor --notes --json",
		},
		Coverage: &StatusCoverageSummary{
			UncoveredCount: 3,
		},
		ProposalHealth: &StatusProposalHealth{
			StaleAfterHours: 72,
			SuspiciousCount: 1,
		},
	}

	items := buildStatusActionItems(status)
	if len(items) < 3 {
		t.Fatalf("expected notes, coverage, and proposal actions, got %#v", items)
	}
	if items[0].Kind != "notes_rewrite" {
		t.Fatalf("notes rewrite drift should be first because it can invalidate derived queues, got %#v", items)
	}
	if items[0].RecommendedCommand != "mainline doctor --notes --json" {
		t.Fatalf("notes action should point at read-only doctor, got %#v", items[0])
	}
}

func TestStatus_SuggestionsIncludeNotesDoctor(t *testing.T) {
	status := &StatusResult{
		Initialized:        true,
		IdentityConfigured: true,
		NotesHealth: &domain.NotesHealth{
			LikelyHistoryRewrite:     true,
			UnreachableMainlineNotes: 10,
		},
	}

	suggestions := buildStatusSuggestions(status)
	joined := strings.Join(suggestions, "\n")
	if !strings.Contains(joined, "mainline doctor --notes --json") {
		t.Fatalf("expected notes doctor suggestion, got %v", suggestions)
	}
}

func TestStatus_ActionableItemsKeepSetupExclusive(t *testing.T) {
	items := buildStatusActionItems(&StatusResult{Initialized: false})
	if len(items) != 1 {
		t.Fatalf("expected one setup item, got %#v", items)
	}
	if items[0].Kind != "setup" || !strings.Contains(items[0].RecommendedCommand, "mainline init") {
		t.Fatalf("expected setup init action, got %#v", items[0])
	}
}

func TestStatus_ActionableItemsPopulateBeforeInit(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)

	res, err := svc.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(res.ActionableItems) != 1 {
		t.Fatalf("expected one actionable setup item before init, got %#v", res.ActionableItems)
	}
	if res.ActionableItems[0].Kind != "setup" {
		t.Fatalf("expected setup item before init, got %#v", res.ActionableItems[0])
	}
}
