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
					What:  "Old storage summary",
					Why:   "Existing storage path",
					Decisions: []domain.Decision{{
						Point:     "Storage",
						Chose:     "JSON",
						Rationale: "Existing path",
					}},
					Risks:     domain.LegacyRiskStatements("old risk"),
					Followups: domain.LegacyFollowupStatements("old follow-up"),
					AntiPatterns: []domain.AntiPattern{{
						What:     "Bypassing the JSON fallback",
						Why:      "SQLite is only a derived cache",
						Severity: "high",
					}},
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
	assertCount(t, db, "intent_followups", 1)
	assertCount(t, db, "intent_anti_patterns", 1)
}

// PK lookup: ReadIntentViewByID returns the same IntentView shape
// the JSON view path returns, so callers can swap with no semantics
// change. Missing-id returns (nil, nil) so callers can fall through
// to JSON without distinguishing "absent" from "errored".
func TestReadIntentViewByID_RoundTripsAndHandlesMissing(t *testing.T) {
	store := New(t.TempDir(), nil)
	view := &domain.MainlineView{
		SchemaVersion: 1, RebuiltAt: "2026-04-29T00:00:00Z", MainBranch: "main",
		Intents: []domain.IntentView{
			{
				IntentID: "int_target", Status: domain.StatusMerged,
				ActorID: "actor_a", Thread: "t",
				Summary: &domain.IntentSummary{
					Title: "Target",
					AntiPatterns: []domain.AntiPattern{
						{What: "x", Why: "y", Severity: "high"},
					},
				},
				Fingerprint: &domain.SemanticFingerprint{FilesTouched: []string{"a.go"}, Subsystems: []string{"a"}},
			},
		},
	}
	if err := store.RebuildMainlineIndex(view); err != nil {
		t.Fatal(err)
	}

	got, err := store.ReadIntentViewByID("int_target")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got == nil || got.IntentID != "int_target" {
		t.Fatalf("expected IntentView for int_target, got %+v", got)
	}
	// raw_json round-trip must carry AntiPatterns through verbatim —
	// the load-bearing safety surface.
	if got.Summary == nil || len(got.Summary.AntiPatterns) != 1 || got.Summary.AntiPatterns[0].What != "x" {
		t.Errorf("AntiPatterns lost in round-trip: %+v", got.Summary)
	}

	missing, err := store.ReadIntentViewByID("int_does_not_exist")
	if err != nil {
		t.Errorf("missing id should be (nil, nil), got err=%v", err)
	}
	if missing != nil {
		t.Errorf("missing id should be nil, got %+v", missing)
	}
}

// File reverse-index: ReadIntentViewsByFiles returns every intent
// whose fingerprint touches at least one of the queried paths.
// Empty paths returns (nil, nil); a path with no matches returns
// an empty slice without error.
func TestReadIntentViewsByFiles_ReverseIndexHits(t *testing.T) {
	store := New(t.TempDir(), nil)
	view := &domain.MainlineView{
		SchemaVersion: 1, RebuiltAt: "2026-04-29T00:00:00Z", MainBranch: "main",
		Intents: []domain.IntentView{
			{IntentID: "int_a", Status: domain.StatusMerged, ActorID: "x",
				Summary: &domain.IntentSummary{Title: "a"},
				Fingerprint: &domain.SemanticFingerprint{
					FilesTouched: []string{"src/auth.go", "src/db.go"},
					Subsystems:   []string{"auth"},
				}},
			{IntentID: "int_b", Status: domain.StatusMerged, ActorID: "x",
				Summary: &domain.IntentSummary{Title: "b"},
				Fingerprint: &domain.SemanticFingerprint{
					FilesTouched: []string{"src/auth.go"},
					Subsystems:   []string{"auth"},
				}},
			{IntentID: "int_c", Status: domain.StatusMerged, ActorID: "x",
				Summary: &domain.IntentSummary{Title: "c"},
				Fingerprint: &domain.SemanticFingerprint{
					FilesTouched: []string{"src/web.go"},
					Subsystems:   []string{"web"},
				}},
		},
	}
	if err := store.RebuildMainlineIndex(view); err != nil {
		t.Fatal(err)
	}

	got, err := store.ReadIntentViewsByFiles([]string{"src/auth.go"})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, iv := range got {
		ids[iv.IntentID] = true
	}
	if !ids["int_a"] || !ids["int_b"] {
		t.Errorf("expected int_a + int_b, got %+v", ids)
	}
	if ids["int_c"] {
		t.Errorf("int_c should not match — it doesn't touch src/auth.go")
	}

	// Empty paths returns (nil, nil) — same shape as no-cache fall-through.
	if rs, err := store.ReadIntentViewsByFiles(nil); err != nil || rs != nil {
		t.Errorf("empty paths should be (nil, nil); got rs=%v err=%v", rs, err)
	}

	// No match returns empty slice without error.
	none, err := store.ReadIntentViewsByFiles([]string{"src/nope.go"})
	if err != nil || len(none) != 0 {
		t.Errorf("no-match should be ([], nil); got rs=%v err=%v", none, err)
	}
}

// Query reverse-index: ReadIntentViewsByQuery hits title / goal /
// summary / decision / risk / follow-up / anti_pattern text via case-insensitive
// LIKE. Empty keyword returns (nil, nil).
func TestReadIntentViewsByQuery_TextSearchHits(t *testing.T) {
	store := New(t.TempDir(), nil)
	view := &domain.MainlineView{
		SchemaVersion: 1, RebuiltAt: "2026-04-29T00:00:00Z", MainBranch: "main",
		Intents: []domain.IntentView{
			{IntentID: "int_jwt", Status: domain.StatusMerged, ActorID: "x", Goal: "Add JWT auth",
				Summary:     &domain.IntentSummary{Title: "JWT migration", Decisions: []domain.Decision{{Chose: "use JWT"}}},
				Fingerprint: &domain.SemanticFingerprint{Subsystems: []string{"auth"}, FilesTouched: []string{"a.go"}}},
			{IntentID: "int_billing", Status: domain.StatusMerged, ActorID: "x", Goal: "Add billing",
				Summary: &domain.IntentSummary{
					Title:     "Billing rewrite",
					Risks:     domain.LegacyRiskStatements("may break old jwt sessions"),
					Followups: domain.LegacyFollowupStatements("add homebrew install path"),
				},
				Fingerprint: &domain.SemanticFingerprint{Subsystems: []string{"billing"}, FilesTouched: []string{"b.go"}}},
			{IntentID: "int_docs", Status: domain.StatusMerged, ActorID: "x", Goal: "Clean up docs copy",
				Summary: &domain.IntentSummary{
					Title: "Terminology cleanup",
					What:  "User-facing copy covers AGENTS.md conventions",
					Why:   "Reader feedback called out confusing copy",
					AntiPatterns: []domain.AntiPattern{{
						What:     "Reintroducing managed block in AGENTS.md",
						Why:      "Agent guidance is the user-facing term",
						Severity: "medium",
					}},
				},
				Fingerprint: &domain.SemanticFingerprint{Subsystems: []string{"docs"}, FilesTouched: []string{"AGENTS.md"}}},
			{IntentID: "int_other", Status: domain.StatusMerged, ActorID: "x", Goal: "Refactor logging",
				Summary:     &domain.IntentSummary{Title: "Logging cleanup"},
				Fingerprint: &domain.SemanticFingerprint{Subsystems: []string{"logs"}, FilesTouched: []string{"c.go"}}},
		},
	}
	if err := store.RebuildMainlineIndex(view); err != nil {
		t.Fatal(err)
	}

	// "jwt" should hit int_jwt (title + decision text) AND int_billing
	// (risk text mentions "old jwt sessions") — case-insensitive.
	got, err := store.ReadIntentViewsByQuery("JWT")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, iv := range got {
		ids[iv.IntentID] = true
	}
	if !ids["int_jwt"] || !ids["int_billing"] {
		t.Errorf("expected int_jwt + int_billing, got %+v", ids)
	}
	if ids["int_other"] {
		t.Errorf("int_other should not match jwt query")
	}

	docs, err := store.ReadIntentViewsByQuery("managed")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].IntentID != "int_docs" {
		t.Fatalf("expected anti_pattern text to hit int_docs, got %+v", docs)
	}

	summary, err := store.ReadIntentViewsByQuery("agents")
	if err != nil {
		t.Fatal(err)
	}
	if len(summary) != 1 || summary[0].IntentID != "int_docs" {
		t.Fatalf("expected summary text to hit int_docs, got %+v", summary)
	}

	followups, err := store.ReadIntentViewsByQuery("homebrew")
	if err != nil {
		t.Fatal(err)
	}
	if len(followups) != 1 || followups[0].IntentID != "int_billing" {
		t.Fatalf("expected follow-up text to hit int_billing, got %+v", followups)
	}

	// Empty keyword returns nil without hitting the DB.
	if rs, err := store.ReadIntentViewsByQuery(""); err != nil || rs != nil {
		t.Errorf("empty keyword should be (nil, nil); got %v / %v", rs, err)
	}
}

// Cache-missing paths return ErrMainlineIndexUnavailable so callers
// can fall through to the JSON view scan without distinguishing
// "broken cache" from "no rows".
func TestReadIntentViewsByFiles_MissingCacheIsErrMainlineIndexUnavailable(t *testing.T) {
	store := New(t.TempDir(), nil)
	_, err := store.ReadIntentViewsByFiles([]string{"a.go"})
	if err == nil {
		t.Fatal("expected ErrMainlineIndexUnavailable when cache missing")
	}
	if err != ErrMainlineIndexUnavailable {
		t.Errorf("expected ErrMainlineIndexUnavailable, got %v", err)
	}
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
