package engine

import (
	"encoding/json"
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
