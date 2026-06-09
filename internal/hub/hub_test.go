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

func addExplicitRisk(v *domain.MainlineView, intentID, text string, files ...string) {
	openedAt := ""
	for _, iv := range v.Intents {
		if iv.IntentID == intentID {
			openedAt = iv.SealedAt
			break
		}
	}
	v.Risks = append(v.Risks, domain.Risk{
		ID:           "risk_" + strings.TrimPrefix(intentID, "int_"),
		Text:         text,
		Status:       "open",
		SourceIntent: intentID,
		Files:        append([]string(nil), files...),
		OpenedAt:     openedAt,
	})
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

func TestBuildHubModel_UserGoalUsesIntentGoal(t *testing.T) {
	v := makeView(domain.IntentView{
		IntentID: "int_goal",
		Status:   domain.StatusProposed,
		Goal:     "canonical start goal",
		Summary: &domain.IntentSummary{
			Title:    "Title",
			What:     "What",
			UserGoal: "bad seal-time mirror",
		},
	})
	m := buildHubModel(v)
	if got := m.Intents[0].UserGoal; got != "canonical start goal" {
		t.Errorf("hub user goal should use IntentView.Goal, got %q", got)
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

func TestBuildHubModel_AttachesExternalPRContributionWithoutPollutingActorOrReviewState(t *testing.T) {
	maintainer := intent("int_maintainer_fix", "actor_maintainer", "2026-06-09T01:00:00Z", domain.StatusMerged, "src/pi.go")
	maintainer.ActorName = "catoncat"
	maintainer.StatusEvidence.MergedMainCommit = "8006baae417d3ac3c8fe646ad77f67527480a17f"
	v := makeView(maintainer)

	m := buildHubModel(v)
	attachExternalContributions(m, []HubExternalContribution{
		{
			ID:             "github-pr-catoncat-cxs-56",
			Title:          "feat(sources): add Pi agent session support",
			AuthorLogin:    "jiangge",
			Source:         "github",
			Repository:     "catoncat/cxs",
			PRNumber:       56,
			PRURL:          "https://github.com/catoncat/cxs/pull/56",
			MergedCommit:   "8006baae417d3ac3c8fe646ad77f67527480a17f",
			Provenance:     "github_pr_imported",
			ImportedBy:     "actor_maintainer",
			ImportedAt:     "2026-06-09T02:00:00Z",
			BodyIntentNote: "empty_template",
		},
	})

	if len(m.ExternalContributions) != 1 {
		t.Fatalf("expected one external contribution, got %+v", m.ExternalContributions)
	}
	contrib := m.ExternalContributions[0]
	if contrib.AuthorLogin != "jiangge" || contrib.Provenance != "github_pr_imported" {
		t.Fatalf("external contribution should preserve GitHub author + provenance, got %+v", contrib)
	}
	if contrib.AuthorSealed || !contrib.NotAuthorSealed || contrib.Verified {
		t.Fatalf("github-pr import must not masquerade as an author-sealed verified intent, got %+v", contrib)
	}
	if len(contrib.AssociatedIntentIDs) != 1 || contrib.AssociatedIntentIDs[0] != "int_maintainer_fix" {
		t.Fatalf("external contribution should link to maintainer pin on same merge commit, got %+v", contrib.AssociatedIntentIDs)
	}
	if m.Dashboard.ActorCount != 1 || len(m.ActorIndex) != 1 || m.ActorIndex[0].ActorID != "actor_maintainer" {
		t.Fatalf("external contributor must not pollute actor index/count, dashboard=%+v actors=%+v", m.Dashboard, m.ActorIndex)
	}
	if len(m.Dashboard.Focus) != 0 {
		t.Fatalf("external contribution must not enter review queue/focus, got %+v", m.Dashboard.Focus)
	}
}

func TestBuildHubModel_PreservesAcceptedActorLogProvenance(t *testing.T) {
	contributor := intent("int_contributor", "actor_jiangge", "2026-06-09T01:00:00Z", domain.StatusMerged, "src/pi.go")
	contributor.ActorName = "jiangge"
	contributor.StatusEvidence.MergedMainCommit = "8006baae417d3ac3c8fe646ad77f67527480a17f"
	contributor.Provenance = &domain.IntentProvenance{
		Kind:            "accepted_actor_log",
		SourceRemote:    "jiangge",
		SourceRef:       "refs/mainline/actors/actor_jiangge/log",
		TargetRef:       "refs/mainline/actors/actor_jiangge/log",
		AcceptedByActor: "actor_maintainer",
		AcceptedByName:  "catoncat",
		AuthorSealed:    true,
		Verified:        true,
	}

	m := buildHubModel(makeView(contributor))
	if len(m.Intents) != 1 || m.Intents[0].Provenance == nil {
		t.Fatalf("accepted actor-log provenance should survive hub flattening: %+v", m.Intents)
	}
	prov := m.Intents[0].Provenance
	if prov.Kind != "accepted_actor_log" || !prov.AuthorSealed || !prov.Verified || prov.AcceptedByActor != "actor_maintainer" {
		t.Fatalf("wrong accepted provenance: %+v", prov)
	}
	if m.Dashboard.ActorCount != 1 || len(m.ActorIndex) != 1 || m.ActorIndex[0].ActorID != "actor_jiangge" {
		t.Fatalf("accepted author-sealed intent should count as contributor actor intent, dashboard=%+v actors=%+v", m.Dashboard, m.ActorIndex)
	}
}

func TestBuildRiskList_SelectsIntentsWithRisks(t *testing.T) {
	withRisk := intent("int_risky", "a", "2026-04-28T01:00:00Z", domain.StatusMerged)
	plain := intent("int_plain", "a", "2026-04-28T02:00:00Z", domain.StatusMerged)
	v := makeView(withRisk, plain)
	addExplicitRisk(v, "int_risky", "the deploy might break old clients")

	m := buildHubModel(v)
	if len(m.RiskIntents) != 1 || m.RiskIntents[0] != "int_risky" {
		t.Errorf("risk list wrong: %v", m.RiskIntents)
	}
}

func TestBuildRiskList_UsesOpenRiskLifecycle(t *testing.T) {
	resolved := intent("int_resolved", "a", "2026-04-28T01:00:00Z", domain.StatusMerged)
	resolved.Summary.Risks = []string{"resolved risk should stay historical"}
	expired := intent("int_expired", "a", "2026-04-28T02:00:00Z", domain.StatusSuperseded)
	expired.Summary.Risks = []string{"expired risk should stay historical"}
	open := intent("int_open", "a", "2026-04-28T03:00:00Z", domain.StatusMerged)
	open.Summary.Risks = []string{"open risk should appear"}

	v := makeView(resolved, expired, open)
	addExplicitRisk(v, "int_resolved", "resolved explicit risk")
	addExplicitRisk(v, "int_expired", "expired explicit risk")
	addExplicitRisk(v, "int_open", "open explicit risk")
	v.RiskResolutions = map[string][]domain.RiskResolution{
		"risk_resolved": {{IntentID: "int_fix", Rationale: "fixed"}},
	}

	m := buildHubModel(v)
	if len(m.RiskIntents) != 1 || m.RiskIntents[0] != "int_open" {
		t.Fatalf("risk list should include only open risks, got %v", m.RiskIntents)
	}
	byID := indexByID(m.Intents)
	if got := len(byID["int_open"].OpenRisks); got != 1 {
		t.Fatalf("open intent should expose 1 open risk, got %d", got)
	}
	if got := len(byID["int_resolved"].OpenRisks); got != 0 {
		t.Fatalf("resolved intent should expose no open risks, got %d", got)
	}
	if got := len(byID["int_expired"].OpenRisks); got != 0 {
		t.Fatalf("expired intent should expose no open risks, got %d", got)
	}
	if len(byID["int_resolved"].Risks) != 1 {
		t.Fatalf("raw resolved risk should remain on intent detail/history surfaces")
	}

	ctx := risksCtx(m, byID)
	if len(ctx.RiskRows) != 1 || ctx.RiskRows[0].Intent.ID != "int_open" {
		t.Fatalf("risks page should default to open-risk rows only, got %+v", ctx.RiskRows)
	}
	if len(ctx.RiskRows[0].Risks) != 1 || ctx.RiskRows[0].Risks[0].Status != "open" {
		t.Fatalf("risk row should carry materialized open risk, got %+v", ctx.RiskRows[0].Risks)
	}
}

// Focus list under the post-signal-reduction taxonomy is no longer
// "proposed > risky > merged"; it surfaces only items that have a
// concrete actionable reason (unack inherited high-severity, stale
// proposed, stale open, or hot-file abandoned/superseded). Plain
// "merged" intents are gone from the focus list — they belonged to
// the vanity-grid era.
func TestBuildDashboard_FocusListSurfacesOnlyActionableItems(t *testing.T) {
	proposed := intent("int_proposed", "a", "2026-04-28T03:00:00Z", domain.StatusProposed, "src/hub.go")
	risky := intent("int_risky", "a", "2026-04-28T02:00:00Z", domain.StatusMerged, "src/hub.go", "src/model.go")
	merged := intent("int_merged", "a", "2026-04-28T01:00:00Z", domain.StatusMerged, "src/model.go")
	v := makeView(proposed, risky, merged)
	addExplicitRisk(v, "int_risky", "needs careful rollout", "src/hub.go")

	m := buildHubModel(v)
	if m.Dashboard.TotalIntents != 3 || m.Dashboard.ProposedIntents != 1 || m.Dashboard.MergedIntents != 2 || m.Dashboard.RiskIntents != 1 {
		t.Fatalf("dashboard counts wrong: %+v", m.Dashboard)
	}
	// The proposed intent must lead the queue.
	if len(m.Dashboard.Focus) == 0 || m.Dashboard.Focus[0].ID != "int_proposed" {
		t.Fatalf("proposed must lead focus list, got %+v", m.Dashboard.Focus)
	}
	// Plain merged intents must NOT appear in focus.
	for _, f := range m.Dashboard.Focus {
		if f.ID == "int_merged" {
			t.Errorf("plain merged intent must not appear in focus list: %+v", f)
		}
	}
	// Risky-merged: only allowed if it's recent + in a hot-file as
	// the abandoned/superseded bucket. Merged status with risks
	// alone is no longer a focus reason.
	for _, f := range m.Dashboard.Focus {
		if f.ID == "int_risky" {
			t.Errorf("risky merged intent must not appear in focus under new taxonomy: %+v", f)
		}
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
	a.StatusEvidence.MergedMainCommit = "8006baae417d3ac3c8fe646ad77f67527480a17f"
	b := intent("int_b", "actor_bob", "2026-04-28T02:00:00Z", domain.StatusSuperseded, "src/auth.go")
	b.StatusEvidence.SupersededByIntent = "int_a"
	v := makeView(a, b)
	addExplicitRisk(v, "int_a", "breaks old clients", "src/auth.go")
	if err := store.WriteMainlineView(v); err != nil {
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
	res, err := Export(store, ExportOptions{
		OutputDir: out,
		ExternalContributions: []HubExternalContribution{{
			Title:          "feat(sources): add Pi agent session support",
			AuthorLogin:    "jiangge",
			Repository:     "catoncat/cxs",
			PRNumber:       56,
			PRURL:          "https://github.com/catoncat/cxs/pull/56",
			MergedCommit:   "8006baae417d3ac3c8fe646ad77f67527480a17f",
			Provenance:     "github_pr_imported",
			ImportedBy:     "actor_alice",
			ImportedAt:     "2026-06-09T02:00:00Z",
			BodyIntentNote: "empty_template",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IntentCount != 2 || res.OpenCount != 1 || res.FileCount != 1 || res.ActorCount != 2 || res.RiskCount != 1 {
		t.Errorf("counts off: %+v", res)
	}

	for _, want := range []string{
		"index.html",
		"open.html",
		"intents.html",
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

	// Author should surface on every intent-listing page, not just the
	// per-intent detail. risks.html is the regression that motivated
	// this — verify the actor link renders there. intents.html is the
	// new "browse all" canonical surface.
	risksPage, _ := os.ReadFile(filepath.Join(out, "risks.html"))
	for _, fragment := range []string{"int_a", "actor_alice", "actors/actor_alice.html"} {
		if !strings.Contains(string(risksPage), fragment) {
			t.Errorf("risks.html missing %q (author should be linked from risk-bearing intent)", fragment)
		}
	}
	intentsPage, _ := os.ReadFile(filepath.Join(out, "intents.html"))
	for _, fragment := range []string{"int_a", "int_b", "actor_alice", "actors/actor_alice.html"} {
		if !strings.Contains(string(intentsPage), fragment) {
			t.Errorf("intents.html missing %q (browse-all surface should list every intent with author)", fragment)
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
	if len(roundTrip.ExternalContributions) != 1 {
		t.Fatalf("round-trip lost external contribution: %+v", roundTrip.ExternalContributions)
	}
	contrib := roundTrip.ExternalContributions[0]
	if contrib.AuthorLogin != "jiangge" || contrib.Provenance != "github_pr_imported" ||
		!contrib.NotAuthorSealed || contrib.AuthorSealed || contrib.Verified {
		t.Fatalf("external contribution trust/provenance fields wrong: %+v", contrib)
	}
	if len(contrib.AssociatedIntentIDs) != 1 || contrib.AssociatedIntentIDs[0] != "int_a" {
		t.Fatalf("external contribution should be associated with maintainer intent int_a, got %+v", contrib.AssociatedIntentIDs)
	}
	if roundTrip.Dashboard.ActorCount != 2 || len(roundTrip.ActorIndex) != 2 {
		t.Fatalf("external contribution must not affect actor count/index: dashboard=%+v actors=%+v", roundTrip.Dashboard, roundTrip.ActorIndex)
	}
	searchData, err := os.ReadFile(filepath.Join(out, "data", "search_index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(searchData), `"type":"external"`) || !strings.Contains(string(searchData), "jiangge") {
		t.Fatalf("search index should include external contribution, got %s", string(searchData))
	}

	// Spot-check that the index page mentions both intents — links
	// across pages must resolve, and this confirms the main table is
	// populated rather than just the chrome.
	idx, _ := os.ReadFile(filepath.Join(out, "index.html"))
	for _, fragment := range []string{"int_a", "int_b", "View in-flight work", "jiangge", "github_pr_imported", "not author-sealed"} {
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

func TestExport_BoundsLongFilePageSlug(t *testing.T) {
	dir := t.TempDir()
	repoRoot := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".ml-cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := storage.New(repoRoot, nil)

	longPath := `domain_docs/mes/spec/process/compair/smt_forms/` +
		strings.Repeat(`\345\215\260\345\210\267\346\234\272`, 18) +
		` (QR-Mac-144).md`
	longSlug := fileSlug(longPath)
	if got := len([]byte(longSlug + ".html")); got > 255 {
		t.Fatalf("file page slug must fit a filesystem path component: got %d bytes (%q)", got, longSlug)
	}
	if !strings.Contains(longSlug, "--") {
		t.Fatalf("long slug should keep a hash suffix to avoid collisions, got %q", longSlug)
	}

	v := makeView(intent("int_long_file", "actor", "2026-04-28T01:00:00Z", domain.StatusMerged, longPath))
	if err := store.WriteMainlineView(v); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "site")
	if _, err := Export(store, ExportOptions{OutputDir: out}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "files", longSlug+".html")); err != nil {
		t.Fatalf("expected bounded file page on disk: %v", err)
	}
	filesPage, _ := os.ReadFile(filepath.Join(out, "files.html"))
	if !strings.Contains(string(filesPage), "files/"+longSlug+".html") {
		t.Fatalf("files index should link to bounded slug %q", longSlug)
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

func TestExport_SurfaceSourceAndSiblingDraftVisibility(t *testing.T) {
	dir := t.TempDir()
	repoRoot := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".ml-cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := storage.New(repoRoot, nil)
	if err := store.WriteMainlineView(makeView()); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "site")
	res, err := Export(store, ExportOptions{
		OutputDir: out,
		Source: HubSource{
			RepoPath:                 repoRoot,
			Branch:                   "main",
			LastSyncAt:               "2026-05-07T03:00:00Z",
			CurrentWorktreeDraftsDir: filepath.Join(repoRoot, ".ml-cache", "drafts"),
		},
		SiblingDrafts: []HubWorktreeDraft{{
			ID:             "int_sibling",
			Goal:           "sibling draft",
			Status:         "drafting",
			GitBranch:      "feature/sibling",
			WorktreePath:   filepath.Join(dir, "repo-sibling"),
			DraftPath:      filepath.Join(dir, "repo-sibling", ".ml-cache", "drafts", "int_sibling.json"),
			TurnCount:      2,
			LastModifiedAt: "2026-05-07T03:10:00Z",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.SiblingDraftCount != 1 {
		t.Fatalf("expected sibling draft count, got %+v", res)
	}
	for page, wants := range map[string][]string{
		"index.html": {"Viewing synced Mainline state", "sibling drafts", "current worktree"},
		"open.html":  {"Visibility", "sibling draft", "current worktree"},
	} {
		body, err := os.ReadFile(filepath.Join(out, page))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(string(body), want) {
				t.Fatalf("%s missing %q:\n%s", page, want, string(body))
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(out, "data", "intents.json"))
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip HubModel
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.Source.RepoPath != repoRoot || !roundTrip.Source.IncludesCurrentWorktreeDrafts ||
		!roundTrip.Source.IncludesSiblingWorktreeDraftList || len(roundTrip.SiblingDrafts) != 1 {
		t.Fatalf("source/sibling metadata missing: %+v", roundTrip.Source)
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
	addExplicitRisk(v, "int_m1", "watch the rollout", "a.go")
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

// Under the post-signal-reduction taxonomy, focus ranks proposed
// intents by (1) unack high-severity inherited constraints, then
// (2) age past the stale-review threshold (24h). Plain "has Risks"
// is no longer a focus-promotion signal — risks live on the intent
// page, not the review queue.
func TestHubReviewAging_OldestStaleProposedFirst(t *testing.T) {
	v := makeView(
		intentSealed("int_old_low", "proposed", 2, []string{"x.go"}, nil),
		intentSealed("int_new_with_risk", "proposed", 0, []string{"x.go"}, []string{"breaking change"}),
		intentSealed("int_old_with_risk", "proposed", 3, []string{"x.go"}, []string{"big risk"}),
	)
	addExplicitRisk(v, "int_new_with_risk", "breaking change", "x.go")
	addExplicitRisk(v, "int_old_with_risk", "big risk", "x.go")
	m := buildHubModel(v)
	if len(m.Dashboard.Focus) == 0 {
		t.Fatalf("expected at least one focus item, got %d", len(m.Dashboard.Focus))
	}
	// Oldest stale proposed wins (3 days > 2 days); the new<24h
	// proposed shouldn't appear ahead of stale ones, and risks
	// don't promote.
	if m.Dashboard.Focus[0].ID != "int_old_with_risk" {
		t.Errorf("oldest stale proposed should be first; got order=%v", focusOrder(m.Dashboard.Focus))
	}
	// The 0-day proposed is not stale yet but lands in the fallback
	// bucket; it should still be ranked LAST among proposed.
	idx := map[string]int{}
	for i, f := range m.Dashboard.Focus {
		idx[f.ID] = i
	}
	if idx["int_new_with_risk"] < idx["int_old_low"] {
		t.Errorf("non-stale proposed should rank below stale, got order=%v", focusOrder(m.Dashboard.Focus))
	}
}

func focusOrder(items []HubFocusIntent) []string {
	out := make([]string, 0, len(items))
	for _, f := range items {
		out = append(out, f.ID)
	}
	return out
}

func TestHubRiskRadar_HidesSealSummaryAntiPatterns(t *testing.T) {
	ap := intent("int_ap_only", "act", time.Now().UTC().Format(time.RFC3339), domain.StatusProposed)
	ap.Summary.AntiPatterns = []domain.AntiPattern{
		{What: "Do not delete /oauth", Why: "callback needs it", Severity: "high"},
	}
	v := makeView(ap)
	m := buildHubModel(v)

	if m.TeamHealth.Risk.RiskBearingProposed != 0 {
		t.Errorf("seal-summary anti-patterns should not register as active risk-bearing proposed; got count %d",
			m.TeamHealth.Risk.RiskBearingProposed)
	}
	if len(m.Intents) != 1 || len(m.Intents[0].AntiPatterns) != 1 {
		t.Errorf("HubIntent must carry AntiPatterns through; got %+v", m.Intents)
	}
}

func TestHubRiskRadar_GroupsRiskBearingProposed(t *testing.T) {
	v := makeView(
		intentSealed("int_proposed_risky", "proposed", 1, []string{"a.go"}, []string{"r"}),
		intentSealed("int_merged_risky", "merged", 1, []string{"a.go"}, []string{"r"}),
		intentSealed("int_proposed_clean", "proposed", 0, []string{"b.go"}, nil),
	)
	addExplicitRisk(v, "int_proposed_risky", "r", "a.go")
	addExplicitRisk(v, "int_merged_risky", "r", "a.go")
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
	addExplicitRisk(v, "int_a", "r", "hot.go")
	addExplicitRisk(v, "int_b", "r", "hot.go")
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
	addExplicitRisk(v, "int_recent_merged", "a real risk here that should appear", "a.go")
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

// Hub renders both EN and ZH copies of every page; the ZH version
// lives under /zh/, intent CONTENT (titles, what/why) stays as
// the user wrote it, only the chrome (nav / section headers /
// labels) gets translated. Assets/data stay at root.
func TestHubExport_RendersBothLanguagesWithToggle(t *testing.T) {
	dir := t.TempDir()
	repoRoot := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".ml-cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := storage.New(repoRoot, nil)
	v := makeView(intent("int_a", "actor_x", time.Now().UTC().Format(time.RFC3339), domain.StatusMerged, "src/x.go"))
	if err := store.WriteMainlineView(v); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "site")
	if _, err := Export(store, ExportOptions{OutputDir: out}); err != nil {
		t.Fatal(err)
	}

	// Both top-level index pages exist, each tagged with its lang.
	enIndex, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatalf("EN index missing: %v", err)
	}
	zhIndex, err := os.ReadFile(filepath.Join(out, "zh", "index.html"))
	if err != nil {
		t.Fatalf("ZH index missing: %v", err)
	}
	if !strings.Contains(string(enIndex), `<html lang="en">`) {
		t.Errorf("EN index should declare lang=en")
	}
	if !strings.Contains(string(zhIndex), `<html lang="zh">`) {
		t.Errorf("ZH index should declare lang=zh")
	}

	// Each version's toggle points at the OTHER language. Toggle
	// label is the OTHER language's self-name.
	if !strings.Contains(string(enIndex), `href="zh/index.html"`) ||
		!strings.Contains(string(enIndex), `>中文<`) {
		t.Errorf("EN toggle should link to zh/ and label '中文'")
	}
	if !strings.Contains(string(zhIndex), `href="../index.html"`) ||
		!strings.Contains(string(zhIndex), `>English<`) {
		t.Errorf("ZH toggle should link back to root and label 'English'")
	}

	// ZH chrome strings present (a couple of key ones).
	for _, want := range []string{"当前关注状态", "总览", "生成于"} {
		if !strings.Contains(string(zhIndex), want) {
			t.Errorf("ZH index missing chrome string %q", want)
		}
	}

	// Intent CONTENT must NOT be translated. The fixture's title is
	// English ("Title for int_a") — it must appear verbatim in BOTH
	// the EN and ZH renders.
	if !strings.Contains(string(enIndex), "Title for int_a") {
		t.Errorf("EN should carry the user-written title verbatim")
	}
	if !strings.Contains(string(zhIndex), "Title for int_a") {
		t.Errorf("ZH should carry the user-written title verbatim — content does not translate")
	}

	// Nested ZH page's stylesheet href must climb out of /zh/<sub>/.
	zhIntent, err := os.ReadFile(filepath.Join(out, "zh", "intents", "int_a.html"))
	if err != nil {
		t.Fatalf("ZH intent page missing: %v", err)
	}
	if !strings.Contains(string(zhIntent), `href="../../assets/style.css"`) {
		t.Errorf("nested ZH page should reach assets via ../../, got: %s",
			extractStylesheet(string(zhIntent)))
	}
}

func extractStylesheet(html string) string {
	const marker = `rel="stylesheet" href="`
	i := strings.Index(html, marker)
	if i < 0 {
		return "(no stylesheet link)"
	}
	rest := html[i+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// Hub signal-reduction guard: the dashboard MUST NOT contain the
// vanity sections we deliberately removed (hero metric grid card
// links, generic Risk radar card, Activity by actor block).
// Catches accidental re-additions in future PRs.
func TestHubDashboard_HasNoVanitySections(t *testing.T) {
	dir := t.TempDir()
	repoRoot := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".ml-cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := storage.New(repoRoot, nil)
	v := makeView(
		intentSealed("int_a", "merged", 1, []string{"a.go"}, []string{"r"}),
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
	idx := string(indexBytes)
	// Removed: the four-card hero metric grid (was inside class
	// "metric-grid"; if reintroduced this guard fires).
	if strings.Contains(idx, "class=\"metric-grid\"") {
		t.Errorf("dashboard must not have metric-grid card row")
	}
	// Removed: standalone Risk radar card (was section.risk_radar).
	if strings.Contains(idx, "section.risk_radar") || strings.Contains(idx, "Risk radar") {
		t.Errorf("dashboard must not have Risk radar card")
	}
	// Removed: Activity by actor card (rendered via class actor-list).
	if strings.Contains(idx, "actor-list") || strings.Contains(idx, "Activity by actor") {
		t.Errorf("dashboard must not have Activity by actor block")
	}
}

// Hub signal-reduction guard: /coverage.html and /digest.html must
// not appear in the sidebar nav. The pages still render (data dump
// useful) but should not be in the human nav surface.
func TestHubSidebar_HidesCoverageAndDigest(t *testing.T) {
	dir := t.TempDir()
	repoRoot := filepath.Join(dir, "repo")
	os.MkdirAll(filepath.Join(repoRoot, ".ml-cache"), 0o755)
	store := storage.New(repoRoot, nil)
	v := makeView(intentSealed("int_a", "merged", 1, []string{"a.go"}, nil))
	store.WriteMainlineView(v)
	out := filepath.Join(dir, "site")
	if _, err := Export(store, ExportOptions{OutputDir: out}); err != nil {
		t.Fatal(err)
	}
	idxBytes, _ := os.ReadFile(filepath.Join(out, "index.html"))
	// Sidebar markup uses href="coverage.html" / href="digest.html"
	// when nav entries exist; the test ensures they're gone.
	if strings.Contains(string(idxBytes), `href="coverage.html"`) {
		t.Errorf("sidebar must not link to /coverage.html")
	}
	if strings.Contains(string(idxBytes), `href="digest.html"`) {
		t.Errorf("sidebar must not link to /digest.html")
	}
}

// InheritedHotspots must be populated on the model when prior intents
// have anti_patterns whose touched files overlap recent work.
func TestBuildInheritedHotspots_PopulatesPerFile(t *testing.T) {
	now := time.Now()
	old := domain.IntentView{
		IntentID: "int_old",
		Status:   domain.StatusMerged,
		SealedAt: now.Add(-3 * 24 * time.Hour).UTC().Format(time.RFC3339),
		Summary: &domain.IntentSummary{
			Title: "old",
		},
		Fingerprint: &domain.SemanticFingerprint{FilesTouched: []string{"a.go"}},
	}
	v := &domain.MainlineView{
		Intents: []domain.IntentView{old},
		Constraints: []domain.Constraint{
			{
				ID:           "guard_high",
				What:         "Don't drop session middleware",
				Why:          "oauth needs it",
				Severity:     "high",
				Files:        []string{"a.go"},
				SourceIntent: "int_old",
				OpenedAt:     old.SealedAt,
			},
			{
				ID:           "guard_medium",
				What:         "Skip rotation",
				Why:          "replay risk",
				Severity:     "medium",
				Files:        []string{"a.go"},
				SourceIntent: "int_old",
				OpenedAt:     old.SealedAt,
			},
		},
	}
	m := buildHubModel(v)
	if len(m.InheritedHotspots) != 1 {
		t.Fatalf("want 1 hotspot for a.go, got %d (%+v)", len(m.InheritedHotspots), m.InheritedHotspots)
	}
	h := m.InheritedHotspots[0]
	if h.FilePath != "a.go" {
		t.Errorf("file path: %q", h.FilePath)
	}
	if h.ConstraintCount != 1 {
		t.Errorf("constraint count: want 1 (only high severity), got %d", h.ConstraintCount)
	}
	if h.HighSeverityCount != 1 {
		t.Errorf("high severity count: want 1, got %d", h.HighSeverityCount)
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
