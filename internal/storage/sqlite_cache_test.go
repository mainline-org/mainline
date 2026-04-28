package storage

import (
	"database/sql"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestRebuildMainlineIndexLogRowsAndLookupTables(t *testing.T) {
	store := New(t.TempDir(), nil)
	view := &domain.MainlineView{
		SchemaVersion: 1,
		RebuiltAt:     "2026-04-29T00:00:00Z",
		MainBranch:    "main",
		MainHead:      "abc123",
		Intents: []domain.IntentView{
			{
				IntentID:      "int_old",
				SchemaVersion: 1,
				Status:        domain.StatusMerged,
				ActorID:       "actor_a",
				Thread:        "feature/old",
				GitBranch:     "feature/old",
				Goal:          "old goal",
				SealedAt:      "2026-04-28T00:00:00Z",
				Summary: &domain.IntentSummary{
					Title: "Old title",
					Decisions: []domain.Decision{{
						Point:     "Storage",
						Chose:     "JSON",
						Rationale: "Existing path",
					}},
					Risks: []string{"old risk"},
				},
				Fingerprint: &domain.SemanticFingerprint{
					FilesTouched: []string{"internal/storage/storage.go"},
					Subsystems:   []string{"storage"},
					Tags:         []string{"json"},
				},
				ViewRebuiltAt: "2026-04-29T00:00:00Z",
			},
			{
				IntentID:    "int_new",
				Status:      domain.StatusProposed,
				ActorID:     "actor_b",
				ActorName:   "bee",
				Thread:      "feature/new",
				GitBranch:   "feature/new",
				Goal:        "new goal",
				SealedAt:    "2026-04-29T00:00:00Z",
				LastCheck:   &domain.CheckSummary{HasConflict: true, HighestSeverity: "high"},
				Summary:     &domain.IntentSummary{Title: "New title"},
				Fingerprint: &domain.SemanticFingerprint{Tags: []string{"sqlite"}},
			},
		},
	}

	if err := store.RebuildMainlineIndex(view); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}

	rows, err := store.ReadIndexedLogIntents("")
	if err != nil {
		t.Fatalf("read indexed log intents: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].IntentID != "int_new" || rows[0].Author != "bee" {
		t.Fatalf("unexpected first row: %+v", rows[0])
	}
	if rows[0].LastCheck == nil || !rows[0].LastCheck.HasConflict {
		t.Fatalf("last_check did not round-trip: %+v", rows[0].LastCheck)
	}

	proposed, err := store.ReadIndexedLogIntents(domain.StatusProposed)
	if err != nil {
		t.Fatalf("read proposed rows: %v", err)
	}
	if len(proposed) != 1 || proposed[0].IntentID != "int_new" {
		t.Fatalf("unexpected proposed rows: %+v", proposed)
	}

	db, err := sql.Open("sqlite", store.mainlineIndexPath())
	if err != nil {
		t.Fatalf("open sqlite index: %v", err)
	}
	defer db.Close()
	assertCount(t, db, "intent_files", 1)
	assertCount(t, db, "intent_tags", 2)
	assertCount(t, db, "intent_subsystems", 1)
	assertCount(t, db, "intent_decisions", 1)
	assertCount(t, db, "intent_risks", 1)
}

func assertCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}
