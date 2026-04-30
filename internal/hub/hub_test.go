package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestBuildDashboard_PrioritizesHumanReviewQueues(t *testing.T) {
	proposed := intent("int_proposed", "a", "2026-04-28T03:00:00Z", domain.StatusProposed, "src/hub.go")
	risky := intent("int_risky", "a", "2026-04-28T02:00:00Z", domain.StatusMerged, "src/hub.go", "src/model.go")
	risky.Summary.Risks = []string{"needs careful rollout"}
	merged := intent("int_merged", "a", "2026-04-28T01:00:00Z", domain.StatusMerged, "src/model.go")
	v := makeView(proposed, risky, merged)

	m := buildHubModel(v)
	if m.Dashboard.TotalIntents != 3 || m.Dashboard.ProposedIntents != 1 || m.Dashboard.MergedIntents != 2 || m.Dashboard.RiskIntents != 1 {
		t.Fatalf("dashboard counts wrong: %+v", m.Dashboard)
	}
	if len(m.Dashboard.Focus) < 3 {
		t.Fatalf("expected proposed, risky, and recent merged focus rows, got %+v", m.Dashboard.Focus)
	}
	if m.Dashboard.Focus[0].ID != "int_proposed" || m.Dashboard.Focus[0].Reason != "waiting for review" {
		t.Errorf("proposed intent should lead focus queue, got %+v", m.Dashboard.Focus[0])
	}
	if len(m.Dashboard.HotFiles) == 0 || m.Dashboard.HotFiles[0].Path != "src/hub.go" || m.Dashboard.HotFiles[0].IntentCount != 2 {
		t.Errorf("hot files should sort by intent count, got %+v", m.Dashboard.HotFiles)
	}
}

func TestBuildDashboard_IncludesOpenIntentCount(t *testing.T) {
	m := buildHubModel(makeView(intent("int_a", "a", "2026-04-28T01:00:00Z", domain.StatusMerged)))
	m.OpenIntents = []HubOpenIntent{{ID: "int_open"}, {ID: "int_other"}}

	d := buildDashboard(m)
	if d.OpenIntents != 2 {
		t.Fatalf("expected 2 open intents, got %+v", d)
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

// Phase-2 check judgments are persisted in IntentView.LastCheck.
// Hub must surface them as conflicts_with edges (bidirectional) and
// must NOT emit edges when the latest check came back clean.
func TestBuildRelations_EmitsConflictsFromLastCheck(t *testing.T) {
	a := intent("int_a", "act", "2026-04-28T01:00:00Z", domain.StatusProposed, "src/auth.go")
	a.LastCheck = &domain.CheckSummary{
		HasConflict:     true,
		HighestSeverity: "high",
		AgainstIntents:  []string{"int_b"},
	}
	b := intent("int_b", "act", "2026-04-28T02:00:00Z", domain.StatusMerged, "src/auth.go")
	clean := intent("int_clean", "act", "2026-04-28T03:00:00Z", domain.StatusMerged)
	clean.LastCheck = &domain.CheckSummary{HasConflict: false, AgainstIntents: []string{"int_b"}}
	v := makeView(a, b, clean)

	m := buildHubModel(v)

	var fwd, rev bool
	for _, r := range m.Relations {
		if r.Kind != "conflicts_with" {
			continue
		}
		if r.From == "int_clean" || r.To == "int_clean" {
			t.Errorf("clean check should not produce conflict edges: %+v", r)
		}
		if r.From == "int_a" && r.To == "int_b" {
			fwd = true
			if r.Note != "high" {
				t.Errorf("expected severity in note, got %q", r.Note)
			}
		}
		if r.From == "int_b" && r.To == "int_a" {
			rev = true
		}
	}
	if !fwd || !rev {
		t.Errorf("expected bidirectional conflicts_with rows, got %+v", m.Relations)
	}
}

// Pairs sharing 2+ files emit shares_file edges (bidirectional) with
// the count in Note. Pairs sharing exactly 1 file are dropped — that
// signal is too weak to surface on most repos (every PR eventually
// touches root.go).
func TestBuildRelations_EmitsSharedFileEdges(t *testing.T) {
	a := intent("int_a", "act", "2026-04-28T01:00:00Z", domain.StatusMerged, "src/auth.go", "src/db.go")
	b := intent("int_b", "act", "2026-04-28T02:00:00Z", domain.StatusMerged, "src/auth.go", "src/db.go", "src/web.go")
	c := intent("int_c", "act", "2026-04-28T03:00:00Z", domain.StatusMerged, "src/web.go")
	v := makeView(a, b, c)

	m := buildHubModel(v)
	pairs := map[string]string{}
	for _, r := range m.Relations {
		if r.Kind == "shares_file" {
			pairs[r.From+"|"+r.To] = r.Note
		}
	}
	if pairs["int_a|int_b"] == "" || pairs["int_b|int_a"] == "" {
		t.Errorf("expected shares_file edges between int_a and int_b (2 shared files), got %+v", pairs)
	}
	if !strings.Contains(pairs["int_a|int_b"], "2 shared files") {
		t.Errorf("expected 2 shared files in note, got %q", pairs["int_a|int_b"])
	}
	if pairs["int_b|int_c"] != "" {
		t.Errorf("expected NO shares_file edge for single-file overlap (b/c only share src/web.go), got %+v", pairs)
	}
}

func TestBuildRelations_OrdersKindsByLoadBearing(t *testing.T) {
	a := intent("int_a", "act", "2026-04-28T01:00:00Z", domain.StatusSuperseded, "x.go")
	a.StatusEvidence.SupersededByIntent = "int_b"
	a.LastCheck = &domain.CheckSummary{HasConflict: true, AgainstIntents: []string{"int_b"}}
	b := intent("int_b", "act", "2026-04-28T02:00:00Z", domain.StatusMerged, "x.go")
	v := makeView(a, b)

	m := buildHubModel(v)
	if len(m.Relations) == 0 {
		t.Fatal("expected at least one relation")
	}
	// Supersession should rank before conflict, conflict before shares_file.
	first := m.Relations[0].Kind
	if first != "supersedes" && first != "superseded_by" {
		t.Errorf("expected supersession first, got %q", first)
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
	if err := store.WriteDraft(&domain.DraftIntent{
		IntentID:       "int_draft",
		Status:         domain.StatusDrafting,
		Goal:           "draft the next hub fix",
		Thread:         "fix/hub-draft",
		GitBranch:      "fix/hub-draft",
		CreatedAt:      "2026-04-28T03:00:00Z",
		LastModifiedAt: "2026-04-28T03:10:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurn(&domain.Turn{IntentID: "int_draft", Index: 0, CreatedAt: "2026-04-28T03:05:00Z"}); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "site")
	res, err := Export(store, ExportOptions{OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if res.IntentCount != 2 || res.OpenCount != 1 || res.FileCount != 1 || res.ActorCount != 2 || res.RiskCount != 1 {
		t.Errorf("counts off: %+v", res)
	}

	for _, want := range []string{
		"index.html",
		"open.html",
		"review.html",
		"files.html",
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
	if len(roundTrip.OpenIntents) != 1 || roundTrip.OpenIntents[0].ID != "int_draft" || roundTrip.OpenIntents[0].TurnCount != 1 {
		t.Errorf("round-trip lost open intents: %+v", roundTrip.OpenIntents)
	}

	// Spot-check that the index page mentions both intents — links
	// across pages must resolve, and this confirms the main table is
	// populated rather than just the chrome.
	idx, _ := os.ReadFile(filepath.Join(out, "index.html"))
	for _, fragment := range []string{"int_a", "int_b", "Alice", "View in-flight work"} {
		if !strings.Contains(string(idx), fragment) {
			t.Errorf("index.html missing %q", fragment)
		}
	}
	reviewPage, _ := os.ReadFile(filepath.Join(out, "review.html"))
	if !strings.Contains(string(reviewPage), "No proposed intents are waiting for review.") {
		t.Errorf("review.html should render the empty queue state")
	}
	filesPage, _ := os.ReadFile(filepath.Join(out, "files.html"))
	for _, fragment := range []string{"src/auth.go", "files/src__auth.go.html"} {
		if !strings.Contains(string(filesPage), fragment) {
			t.Errorf("files.html missing %q", fragment)
		}
	}
	openPage, _ := os.ReadFile(filepath.Join(out, "open.html"))
	for _, fragment := range []string{"int_draft", "draft the next hub fix", "1"} {
		if !strings.Contains(string(openPage), fragment) {
			t.Errorf("open.html missing %q", fragment)
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
	for _, want := range []string{"index.html", "open.html", "review.html", "files.html", "risks.html", "graph.html"} {
		if _, err := os.Stat(filepath.Join(out, want)); err != nil {
			t.Errorf("expected %s even with empty view: %v", want, err)
		}
	}
}

func TestBuildOpenIntents_SkipsStaleTerminalDrafts(t *testing.T) {
	dir := t.TempDir()
	repoRoot := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".ml-cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := storage.New(repoRoot, nil)
	view := makeView(intent("int_landed", "actor", "2026-04-28T01:00:00Z", domain.StatusMerged))
	if err := store.WriteMainlineView(view); err != nil {
		t.Fatal(err)
	}
	for _, d := range []*domain.DraftIntent{
		{
			IntentID:       "int_open",
			Status:         domain.StatusDrafting,
			Goal:           "still open",
			Thread:         "fix/open",
			GitBranch:      "fix/open",
			CreatedAt:      "2026-04-28T02:00:00Z",
			LastModifiedAt: "2026-04-28T02:10:00Z",
		},
		{
			IntentID:       "int_landed",
			Status:         domain.StatusSealedLocal,
			Goal:           "stale local copy",
			Thread:         "fix/landed",
			GitBranch:      "fix/landed",
			CreatedAt:      "2026-04-28T01:00:00Z",
			LastModifiedAt: "2026-04-28T01:10:00Z",
		},
	} {
		if err := store.WriteDraft(d); err != nil {
			t.Fatal(err)
		}
	}

	open := buildOpenIntents(store, view)
	if len(open) != 1 || open[0].ID != "int_open" {
		t.Fatalf("expected only non-terminal open draft, got %+v", open)
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

// -----------------------------------------------------------
// Team-health / founder-dashboard tests (spec §14)
// -----------------------------------------------------------

func intentSealed(id, status string, daysAgo int, files []string, risks []string) domain.IntentView {
	now := time.Now()
	sealedAt := now.Add(-time.Duration(daysAgo) * 24 * time.Hour).UTC().Format(time.RFC3339)
	iv := domain.IntentView{
		IntentID: id,
		Status:   domain.IntentStatus(status),
		ActorID:  "actor_x",
		Thread:   "t",
		SealedAt: sealedAt,
		Summary:  &domain.IntentSummary{Title: id, What: "did " + id, Risks: risks},
		Fingerprint: &domain.SemanticFingerprint{
			Subsystems:   []string{"sub"},
			FilesTouched: files,
		},
	}
	return iv
}

func TestHubTeamHealth_GeneratesSummary(t *testing.T) {
	v := makeView(
		intentSealed("int_p1", "proposed", 1, []string{"a.go"}, nil),
		intentSealed("int_m1", "merged", 0, []string{"a.go"}, []string{"watch the rollout"}),
		intentSealed("int_m2", "merged", 0, []string{"b.go"}, nil),
	)
	m := buildHubModel(v)
	if m.TeamHealth.HealthSummary == "" {
		t.Fatal("HealthSummary must not be empty")
	}
	if m.TeamHealth.HealthLevel == "" {
		t.Fatal("HealthLevel must be populated; got empty")
	}
	// Coverage is unavailable in this layer — the partial-data
	// banner is the spec-required honesty signal.
	if !strings.Contains(m.TeamHealth.HealthSummary, "Coverage data unavailable") {
		t.Errorf("partial-data summary should mention coverage unavailability: %q", m.TeamHealth.HealthSummary)
	}
}

func TestHubReviewAging_SortsOldHighRiskFirst(t *testing.T) {
	v := makeView(
		intentSealed("int_old_low", "proposed", 2, []string{"x.go"}, nil),
		intentSealed("int_new_high_risk", "proposed", 0, []string{"x.go"}, []string{"breaking change"}),
		intentSealed("int_old_high_risk", "proposed", 3, []string{"x.go"}, []string{"big risk"}),
	)
	m := buildHubModel(v)
	// Focus list should put high-risk proposed first; within
	// high-risk, oldest first.
	if len(m.Dashboard.Focus) < 3 {
		t.Fatalf("expected 3 focus items, got %d", len(m.Dashboard.Focus))
	}
	if m.Dashboard.Focus[0].ID != "int_old_high_risk" {
		t.Errorf("oldest high-risk should be first; got order=%v",
			focusOrder(m.Dashboard.Focus))
	}
	if m.Dashboard.Focus[1].ID != "int_new_high_risk" {
		t.Errorf("high-risk should come before low-risk; got order=%v",
			focusOrder(m.Dashboard.Focus))
	}
}

func focusOrder(items []HubFocusIntent) []string {
	out := make([]string, 0, len(items))
	for _, f := range items {
		out = append(out, f.ID)
	}
	return out
}

func TestHubRiskRadar_GroupsRiskBearingProposed(t *testing.T) {
	v := makeView(
		intentSealed("int_proposed_risky", "proposed", 1, []string{"a.go"}, []string{"r"}),
		intentSealed("int_merged_risky", "merged", 1, []string{"a.go"}, []string{"r"}),
		intentSealed("int_proposed_clean", "proposed", 0, []string{"b.go"}, nil),
	)
	m := buildHubModel(v)
	if m.TeamHealth.Risk.RiskBearingProposed != 1 {
		t.Errorf("expected 1 risk-bearing proposed, got %d", m.TeamHealth.Risk.RiskBearingProposed)
	}
	if len(m.TeamHealth.Risk.RiskBearingProposedRows) != 1 ||
		m.TeamHealth.Risk.RiskBearingProposedRows[0].ID != "int_proposed_risky" {
		t.Errorf("expected the risky-proposed row to surface; got %+v",
			m.TeamHealth.Risk.RiskBearingProposedRows)
	}
}

func TestHubDecisionHotspots_UsesIntentHistoryCounts(t *testing.T) {
	v := makeView(
		intentSealed("int_a", "merged", 1, []string{"hot.go", "cold.go"}, []string{"r"}),
		intentSealed("int_b", "merged", 2, []string{"hot.go"}, []string{"r"}),
		intentSealed("int_c", "merged", 3, []string{"hot.go"}, nil),
		intentSealed("int_d", "merged", 4, []string{"cold.go"}, nil),
	)
	m := buildHubModel(v)
	if len(m.Dashboard.HotFiles) == 0 {
		t.Fatal("hot files should be populated")
	}
	// "hot.go" should be first (3 intents) and carry risk + recent
	// counts for the dashboard's Decision-hotspots metadata.
	if m.Dashboard.HotFiles[0].Path != "hot.go" {
		t.Errorf("hot.go should rank first, got %q", m.Dashboard.HotFiles[0].Path)
	}
	if m.Dashboard.HotFiles[0].IntentCount != 3 {
		t.Errorf("hot.go should report 3 intents, got %d", m.Dashboard.HotFiles[0].IntentCount)
	}
	if m.Dashboard.HotFiles[0].RiskIntentCount != 2 {
		t.Errorf("hot.go should report 2 risk-bearing intents, got %d",
			m.Dashboard.HotFiles[0].RiskIntentCount)
	}
}

func TestHubDigest_GeneratesSevenDaySummary(t *testing.T) {
	v := makeView(
		intentSealed("int_recent_merged", "merged", 1, []string{"a.go"},
			[]string{"a real risk here that should appear"}),
		intentSealed("int_recent_proposed", "proposed", 2, []string{"a.go"}, nil),
		intentSealed("int_recent_abandoned", "abandoned", 3, []string{"a.go"}, nil),
		intentSealed("int_old_merged", "merged", 60, []string{"a.go"}, nil), // outside 7-day window
	)
	m := buildHubModel(v)
	d := m.TeamHealth.Digest
	if d.WindowDays != 7 {
		t.Errorf("expected 7-day window, got %d", d.WindowDays)
	}
	// SealedThisWindow counts merged intents in window only (1 here).
	// ProposedThisWindow counts proposed (1). AbandonedThisWindow
	// counts abandoned (1). int_old_merged is outside the window.
	if d.SealedThisWindow != 1 || d.ProposedThisWindow != 1 || d.AbandonedThisWindow != 1 {
		t.Errorf("digest counts wrong: sealed=%d proposed=%d abandoned=%d",
			d.SealedThisWindow, d.ProposedThisWindow, d.AbandonedThisWindow)
	}
	// Risk-bearing this window should pick up the merged one with risks.
	if d.RiskBearingThisWindow != 1 {
		t.Errorf("expected 1 risk-bearing in window, got %d", d.RiskBearingThisWindow)
	}
	if len(d.RisksToWatch) == 0 || d.RisksToWatch[0].ID != "int_recent_merged" {
		t.Errorf("RisksToWatch should surface int_recent_merged: %+v", d.RisksToWatch)
	}
}

// Spec §14 mandatory copy test: dashboard / digest / team-health
// output must NOT use productivity / leaderboard / performance
// language. This test renders the template against a real fixture
// and asserts none of those words appear — guards against future
// edits silently turning Hub into a manager-monitoring panel.
func TestHubDashboard_DoesNotUseLeaderboardLanguage(t *testing.T) {
	dir := t.TempDir()
	repoRoot := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".ml-cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := storage.New(repoRoot, nil)
	v := makeView(
		intentSealed("int_a", "merged", 1, []string{"a.go"}, []string{"r"}),
		intentSealed("int_b", "proposed", 2, []string{"a.go"}, nil),
	)
	if err := store.WriteMainlineView(v); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "site")
	if _, err := Export(store, ExportOptions{OutputDir: out}); err != nil {
		t.Fatal(err)
	}
	indexBytes, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	indexLower := strings.ToLower(string(indexBytes))
	for _, forbidden := range []string{
		"productivity",
		"performance",
		"leaderboard",
		"velocity",
	} {
		if strings.Contains(indexLower, forbidden) {
			t.Errorf("Hub index must not contain %q — that turns it into a manager-monitoring panel",
				forbidden)
		}
	}
}
