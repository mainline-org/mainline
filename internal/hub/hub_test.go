package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/storage"
)

// Tests focus on the model layer (model.go + buildHubModel) because
// that is the part the planned Hub v2 hosted service would inherit
// as its API DTO. The render layer is throwaway; we cover it with a
// single smoke test that confirms every page type ends up on disk.

func makeView(intents ...domain.IntentView) *domain.MainlineView {
	return &domain.MainlineView{
		SchemaVersion: 1,
		MainBranch:    "main",
		MainHead:      "deadbeef",
		Intents:       intents,
	}
}

func intent(id, actor, sealed string, status domain.IntentStatus, files ...string) domain.IntentView {
	return domain.IntentView{
		IntentID: id,
		Status:   status,
		ActorID:  actor,
		Thread:   "thread-" + id,
		SealedAt: sealed,
		Summary: &domain.IntentSummary{
			Title: "Title for " + id,
			What:  "What for " + id,
		},
		Fingerprint: &domain.SemanticFingerprint{FilesTouched: files},
	}
}

func TestBuildHubModel_FlattensView(t *testing.T) {
	v := makeView(intent("int_a", "actor_1", "2026-04-28T01:00:00Z", domain.StatusMerged, "a.go", "b.go"))
	m := buildHubModel(v)

	if got := len(m.Intents); got != 1 {
		t.Fatalf("expected 1 intent, got %d", got)
	}
	in := m.Intents[0]
	if in.ID != "int_a" || in.Status != "merged" || in.Title != "Title for int_a" {
		t.Errorf("unexpected flattened intent: %+v", in)
	}
	if len(in.FilesTouched) != 2 {
		t.Errorf("files_touched not copied through: %v", in.FilesTouched)
	}
	if m.MainBranch != "main" || m.MainHead != "deadbeef" {
		t.Errorf("view metadata not propagated: %+v", m)
	}
}

func TestBuildHubModel_TitleFallsBackToGoal(t *testing.T) {
	v := makeView(domain.IntentView{
		IntentID: "int_no_summary",
		Status:   domain.StatusProposed,
		Goal:     "the raw goal text",
	})
	m := buildHubModel(v)
	if m.Intents[0].Title != "the raw goal text" {
		t.Errorf("title should fall back to goal, got %q", m.Intents[0].Title)
	}
}

func TestBuildHubModel_SortsNewestFirst(t *testing.T) {
	v := makeView(
		intent("int_old", "a", "2026-04-25T00:00:00Z", domain.StatusMerged),
		intent("int_new", "a", "2026-04-28T00:00:00Z", domain.StatusMerged),
		intent("int_mid", "a", "2026-04-26T00:00:00Z", domain.StatusMerged),
	)
	m := buildHubModel(v)
	got := []string{m.Intents[0].ID, m.Intents[1].ID, m.Intents[2].ID}
	want := []string{"int_new", "int_mid", "int_old"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sort order wrong at %d: got %v want %v", i, got, want)
			break
		}
	}
}

func TestBuildFileIndex_AggregatesAcrossIntents(t *testing.T) {
	v := makeView(
		intent("int_a", "act", "2026-04-28T01:00:00Z", domain.StatusMerged, "src/auth.go", "src/db.go"),
		intent("int_b", "act", "2026-04-28T02:00:00Z", domain.StatusMerged, "src/auth.go"),
		intent("int_c", "act", "2026-04-28T03:00:00Z", domain.StatusMerged, "src/web.go"),
	)
	m := buildHubModel(v)

	authIDs := lookupFile(m.FileIndex, "src/auth.go")
	if len(authIDs) != 2 {
		t.Errorf("auth.go should appear in 2 intents, got %d (%v)", len(authIDs), authIDs)
	}
	if lookupFile(m.FileIndex, "src/db.go") == nil {
		t.Errorf("db.go should appear in the index")
	}
	if lookupFile(m.FileIndex, "src/missing.go") != nil {
		t.Errorf("missing.go should not appear")
	}
}

func TestBuildActorIndex_GroupsByActor(t *testing.T) {
	v := makeView(
		intent("int_a", "actor_alice", "2026-04-28T01:00:00Z", domain.StatusMerged),
		intent("int_b", "actor_alice", "2026-04-28T02:00:00Z", domain.StatusMerged),
		intent("int_c", "actor_bob", "2026-04-28T03:00:00Z", domain.StatusMerged),
	)
	m := buildHubModel(v)
	if len(m.ActorIndex) != 2 {
		t.Fatalf("expected 2 actor entries, got %d", len(m.ActorIndex))
	}
	for _, a := range m.ActorIndex {
		switch a.ActorID {
		case "actor_alice":
			if len(a.IntentIDs) != 2 {
				t.Errorf("alice should own 2 intents, got %d", len(a.IntentIDs))
			}
		case "actor_bob":
			if len(a.IntentIDs) != 1 {
				t.Errorf("bob should own 1 intent")
			}
		default:
			t.Errorf("unexpected actor %s", a.ActorID)
		}
	}
}

func TestBuildRiskList_SelectsIntentsWithRisks(t *testing.T) {
	withRisk := intent("int_risky", "a", "2026-04-28T01:00:00Z", domain.StatusMerged)
	withRisk.Summary.Risks = []string{"the deploy might break old clients"}
	plain := intent("int_plain", "a", "2026-04-28T02:00:00Z", domain.StatusMerged)
	v := makeView(withRisk, plain)

	m := buildHubModel(v)
	if len(m.RiskIntents) != 1 || m.RiskIntents[0] != "int_risky" {
		t.Errorf("risk list wrong: %v", m.RiskIntents)
	}
}

func TestBuildRelations_EmitsBothDirections(t *testing.T) {
	a := intent("int_a", "act", "2026-04-28T01:00:00Z", domain.StatusSuperseded)
	a.StatusEvidence.SupersededByIntent = "int_b"
	b := intent("int_b", "act", "2026-04-28T02:00:00Z", domain.StatusMerged)
	v := makeView(a, b)

	m := buildHubModel(v)
	var sawForward, sawReverse bool
	for _, r := range m.Relations {
		if r.From == "int_a" && r.Kind == "superseded_by" && r.To == "int_b" {
			sawForward = true
		}
		if r.From == "int_b" && r.Kind == "supersedes" && r.To == "int_a" {
			sawReverse = true
		}
	}
	if !sawForward || !sawReverse {
		t.Errorf("expected both supersedes and superseded_by rows, got %+v", m.Relations)
	}
}

func TestExport_ProducesAllPageTypes(t *testing.T) {
	dir := t.TempDir()
	repoRoot := filepath.Join(dir, "repo")
	mlDir := filepath.Join(repoRoot, ".ml-cache")
	if err := os.MkdirAll(mlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := storage.New(repoRoot, nil)

	a := intent("int_a", "actor_alice", "2026-04-28T01:00:00Z", domain.StatusMerged, "src/auth.go")
	a.ActorName = "Alice"
	a.Summary.Risks = []string{"breaks old clients"}
	b := intent("int_b", "actor_bob", "2026-04-28T02:00:00Z", domain.StatusSuperseded, "src/auth.go")
	b.StatusEvidence.SupersededByIntent = "int_a"
	if err := store.WriteMainlineView(makeView(a, b)); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "site")
	res, err := Export(store, ExportOptions{OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if res.IntentCount != 2 || res.FileCount != 1 || res.ActorCount != 2 || res.RiskCount != 1 {
		t.Errorf("counts off: %+v", res)
	}

	for _, want := range []string{
		"index.html",
		"risks.html",
		"graph.html",
		"intents/int_a.html",
		"intents/int_b.html",
		"files/src__auth.go.html",
		"actors/actor_alice.html",
		"actors/actor_bob.html",
		"assets/style.css",
		"data/intents.json",
	} {
		if _, err := os.Stat(filepath.Join(out, want)); err != nil {
			t.Errorf("expected %s on disk: %v", want, err)
		}
	}

	// JSON dump must round-trip into HubModel cleanly — that is the
	// shape Hub v2 will inherit, so it has to be valid right now.
	jb, err := os.ReadFile(filepath.Join(out, "data", "intents.json"))
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip HubModel
	if err := json.Unmarshal(jb, &roundTrip); err != nil {
		t.Fatalf("data/intents.json should be valid HubModel: %v", err)
	}
	if len(roundTrip.Intents) != 2 {
		t.Errorf("round-trip lost intents: got %d", len(roundTrip.Intents))
	}

	// Spot-check that the index page mentions both intents — links
	// across pages must resolve, and this confirms the main table is
	// populated rather than just the chrome.
	idx, _ := os.ReadFile(filepath.Join(out, "index.html"))
	for _, fragment := range []string{"int_a", "int_b", "Alice"} {
		if !strings.Contains(string(idx), fragment) {
			t.Errorf("index.html missing %q", fragment)
		}
	}
}

func TestExport_NoIntentsStillWritesIndex(t *testing.T) {
	dir := t.TempDir()
	repoRoot := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".ml-cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := storage.New(repoRoot, nil)
	if err := store.WriteMainlineView(makeView()); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "empty-site")
	res, err := Export(store, ExportOptions{OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if res.IntentCount != 0 {
		t.Errorf("expected 0 intents, got %d", res.IntentCount)
	}
	for _, want := range []string{"index.html", "risks.html", "graph.html"} {
		if _, err := os.Stat(filepath.Join(out, want)); err != nil {
			t.Errorf("expected %s even with empty view: %v", want, err)
		}
	}
}

func lookupFile(idx []HubFileEntry, path string) []string {
	for _, e := range idx {
		if e.Path == path {
			return e.IntentIDs
		}
	}
	return nil
}
