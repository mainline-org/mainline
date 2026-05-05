package engine

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestMigrateNotesInferMovesUnreachableNoteByTreeHash(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	initial := svc.Git.ReadRef("refs/heads/main")

	writeFile(t, dir, "feature.txt", "same tree\n")
	gitCmd(t, dir, "add", "feature.txt")
	gitCmd(t, dir, "commit", "-m", "old feature")
	oldCommit := svc.Git.ReadRef("refs/heads/main")
	gitCmd(t, dir, "branch", "old-main", oldCommit)

	note := domain.CommitNote{
		SchemaVersion: 1,
		Kind:          "mainline.commit_note",
		Intents: []domain.IntentReference{
			{IntentID: "int_rewrite", SealResultHash: "sha256:test"},
		},
		AddedAt:       "2026-05-05T00:00:00Z",
		AddedBy:       "actor_test",
		Via:           "pin_auto",
		MatchStrategy: "tree_hash",
	}
	noteJSON, _ := json.Marshal(note)
	if err := svc.Git.NotesAdd(oldCommit, string(noteJSON)); err != nil {
		t.Fatalf("add old note: %v", err)
	}

	gitCmd(t, dir, "reset", "--hard", initial)
	writeFile(t, dir, "feature.txt", "same tree\n")
	gitCmd(t, dir, "add", "feature.txt")
	gitCmd(t, dir, "commit", "-m", "new feature")
	newCommit := svc.Git.ReadRef("refs/heads/main")
	if newCommit == oldCommit {
		t.Fatal("test setup should create a rewritten commit")
	}

	report, err := svc.Doctor(DoctorOptions{Notes: true})
	if err != nil {
		t.Fatalf("doctor notes: %v", err)
	}
	if report.Notes == nil || report.Notes.UnreachableMainlineNotes != 1 {
		t.Fatalf("expected one unreachable mainline note, got %#v", report.Notes)
	}

	dryRun, err := svc.MigrateNotes(NotesMigrationOptions{Infer: true})
	if err != nil {
		t.Fatalf("dry-run migrate: %v", err)
	}
	if dryRun.Wrote {
		t.Fatal("dry-run should not write")
	}
	if len(dryRun.Plan.SafeMigrations) != 1 {
		t.Fatalf("expected one safe migration, got %#v", dryRun.Plan)
	}
	migration := dryRun.Plan.SafeMigrations[0]
	if migration.OldCommit != oldCommit || migration.NewCommit != newCommit ||
		migration.Strategy != "tree_hash_unique" {
		t.Fatalf("unexpected migration: %#v", migration)
	}

	applied, err := svc.MigrateNotes(NotesMigrationOptions{Infer: true, Write: true})
	if err != nil {
		t.Fatalf("write migrate: %v", err)
	}
	if !applied.Wrote {
		t.Fatal("expected write migration")
	}
	if raw, _ := svc.Git.NotesShow(oldCommit); raw != "" {
		t.Fatalf("old note should be removed, got %q", raw)
	}
	raw, _ := svc.Git.NotesShow(newCommit)
	if raw == "" {
		t.Fatal("expected migrated note on new commit")
	}
	var migrated domain.CommitNote
	if err := json.Unmarshal([]byte(raw), &migrated); err != nil {
		t.Fatalf("parse migrated note: %v", err)
	}
	if len(migrated.Intents) != 1 || migrated.Intents[0].IntentID != "int_rewrite" {
		t.Fatalf("unexpected migrated note: %#v", migrated)
	}
}

func TestStatusSurfacesLikelyNotesRewriteDrift(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	initial := svc.Git.ReadRef("refs/heads/main")

	note := domain.CommitNote{
		SchemaVersion: 1,
		Kind:          "mainline.commit_note",
		Intents: []domain.IntentReference{
			{IntentID: "int_rewrite", SealResultHash: "sha256:test"},
		},
		AddedAt:       "2026-05-05T00:00:00Z",
		AddedBy:       "actor_test",
		Via:           "pin_auto",
		MatchStrategy: "tree_hash",
	}
	noteJSON, _ := json.Marshal(note)

	var oldTip string
	for i := 0; i < 10; i++ {
		writeFile(t, dir, "feature.txt", fmt.Sprintf("rewrite %d\n", i))
		gitCmd(t, dir, "add", "feature.txt")
		gitCmd(t, dir, "commit", "-m", fmt.Sprintf("old feature %d", i))
		oldCommit := svc.Git.ReadRef("refs/heads/main")
		oldTip = oldCommit
		if err := svc.Git.NotesAdd(oldCommit, string(noteJSON)); err != nil {
			t.Fatalf("add old note %d: %v", i, err)
		}
	}
	gitCmd(t, dir, "branch", "old-main", oldTip)
	gitCmd(t, dir, "reset", "--hard", initial)

	res, err := svc.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if res.NotesHealth == nil || !res.NotesHealth.LikelyHistoryRewrite {
		t.Fatalf("expected notes rewrite health, got %#v", res.NotesHealth)
	}
	if res.NotesHealth.RecommendedCommand != "mainline doctor --notes --json" {
		t.Fatalf("status should recommend read-only doctor, got %#v", res.NotesHealth)
	}
	if len(res.ActionableItems) == 0 || res.ActionableItems[0].Kind != "notes_rewrite" {
		t.Fatalf("status should make notes rewrite an actionable item, got %#v", res.ActionableItems)
	}
}
