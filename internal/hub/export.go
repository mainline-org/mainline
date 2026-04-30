package hub

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/storage"
)

// ExportOptions are the inputs to a single `mainline hub export` run.
// OutputDir is required; CoverageRows is optional engine-supplied
// per-commit coverage data (see CoverageInputCommit). When nil, Hub
// renders the partial-data wording on coverage cards / page rather
// than fake zero counts.
type ExportOptions struct {
	OutputDir    string
	CoverageRows []CoverageInputCommit
	// CoverageWindow is the engine's window size (commits scanned).
	// Echoed into HubCoverageDetail.WindowSize so the page can show
	// "last N commits on main" honestly.
	CoverageWindow int
}

// ExportResult summarises what landed on disk. Returned to the CLI
// for human / JSON output.
type ExportResult struct {
	OutputDir   string `json:"output_dir"`
	IntentCount int    `json:"intent_count"`
	OpenCount   int    `json:"open_count"`
	FileCount   int    `json:"file_count"`
	ActorCount  int    `json:"actor_count"`
	RiskCount   int    `json:"risk_count"`
	IndexPath   string `json:"index_path"`
}

// Export builds the HubModel from the local synced view and writes
// the static site under opts.OutputDir.
//
// The Service argument is *storage.Store — not engine.Service —
// because Hub v1 only needs read-only view access. This keeps the
// hub package import-free of engine and lets the engine import hub
// later (e.g. for a future `mainline status --hub-link`) without a
// cycle.
func Export(store *storage.Store, opts ExportOptions) (*ExportResult, error) {
	if opts.OutputDir == "" {
		return nil, fmt.Errorf("hub: output dir required")
	}
	view, err := store.ReadMainlineView()
	if err != nil {
		return nil, fmt.Errorf("hub: read view: %w", err)
	}
	model := buildHubModel(view)
	model.OpenIntents = buildOpenIntents(store, view)
	model.Dashboard = buildDashboard(model)
	if len(opts.CoverageRows) > 0 {
		model.TeamHealth.Coverage = BuildCoverageSummary(opts.CoverageRows)
		model.CoverageDetail = HubCoverageDetail{
			WindowSize: opts.CoverageWindow,
			Commits:    coverageCommitsFromInput(opts.CoverageRows),
		}
		// Re-run health-level since coverage availability flipped.
		model.TeamHealth.populateHealthLevel()
	}

	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("hub: mkdir output: %w", err)
	}
	if err := writeSite(opts.OutputDir, model); err != nil {
		return nil, err
	}

	return &ExportResult{
		OutputDir:   opts.OutputDir,
		IntentCount: len(model.Intents),
		OpenCount:   len(model.OpenIntents),
		FileCount:   len(model.FileIndex),
		ActorCount:  len(model.ActorIndex),
		RiskCount:   len(model.RiskIntents),
		IndexPath:   filepath.Join(opts.OutputDir, "index.html"),
	}, nil
}

// buildHubModel is the pure model-derivation step: given a view,
// produce a HubModel. No I/O — kept testable in isolation.
func buildHubModel(view *domain.MainlineView) *HubModel {
	m := &HubModel{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		MainBranch:  view.MainBranch,
		MainHead:    view.MainHead,
	}
	for i := range view.Intents {
		m.Intents = append(m.Intents, hubIntentFromView(&view.Intents[i]))
	}
	// Inherited-constraint propagation: for each intent, surface
	// anti_patterns from prior sealed intents whose files/subsystems
	// overlap. Done after the flatten loop so we can reuse the
	// MainlineView's authoritative IntentSummary for acknowledgement
	// matching. O(N²) worst case but each call is bounded by the
	// number of intents that touch overlapping files; small in
	// practice.
	annotateInheritedConstraints(m, view)
	sort.SliceStable(m.Intents, func(i, j int) bool {
		return intentSortKey(m.Intents[i]) > intentSortKey(m.Intents[j])
	})
	m.FileIndex = buildFileIndex(m.Intents)
	m.ActorIndex = buildActorIndex(m.Intents)
	m.RiskIntents = buildRiskList(m.Intents)
	m.Relations = buildRelations(m.Intents)
	m.Dashboard = buildDashboard(m)
	// TeamHealth must run AFTER Dashboard / Intents / OpenIntents
	// have been populated — every field reads from them. Pure;
	// shares the same now() reference so age buckets line up
	// between the dashboard and the team-health summary.
	m.TeamHealth = buildTeamHealth(m, time.Now())
	return m
}

func buildOpenIntents(store *storage.Store, view *domain.MainlineView) []HubOpenIntent {
	ids, err := store.ListDrafts()
	if err != nil || len(ids) == 0 {
		return nil
	}
	viewStatus := make(map[string]domain.IntentStatus)
	if view != nil {
		for _, iv := range view.Intents {
			viewStatus[iv.IntentID] = iv.Status
		}
	}

	out := make([]HubOpenIntent, 0)
	for _, id := range ids {
		d, err := store.ReadDraft(id)
		if err != nil || d == nil {
			continue
		}
		if !hubOpenStatus(d.Status) {
			continue
		}
		if vs, ok := viewStatus[id]; ok && hubTerminalStatus(vs) {
			continue
		}
		turns, _ := store.ReadTurns(id)
		updatedAt := d.LastModifiedAt
		if updatedAt == "" {
			updatedAt = d.CreatedAt
		}
		out = append(out, HubOpenIntent{
			ID:             d.IntentID,
			Goal:           d.Goal,
			Status:         string(d.Status),
			Thread:         d.Thread,
			GitBranch:      d.GitBranch,
			CreatedAt:      d.CreatedAt,
			LastModifiedAt: updatedAt,
			TurnCount:      len(turns),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		ki := openSortKey(out[i])
		kj := openSortKey(out[j])
		if ki != kj {
			return ki > kj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func hubOpenStatus(s domain.IntentStatus) bool {
	return s == domain.StatusDrafting || s == domain.StatusSealedLocal || s == domain.StatusProposed
}

func hubTerminalStatus(s domain.IntentStatus) bool {
	return s == domain.StatusMerged ||
		s == domain.StatusAbandoned ||
		s == domain.StatusSuperseded ||
		s == domain.StatusReverted
}

func openSortKey(i HubOpenIntent) string {
	if i.LastModifiedAt != "" {
		return i.LastModifiedAt
	}
	if i.CreatedAt != "" {
		return i.CreatedAt
	}
	return i.ID
}

// intentSortKey returns the timestamp string we sort intents by
// (newest first across the whole UI). SealedAt is the right anchor:
// it is set the moment the agent finishes the work, regardless of
// whether the PR has merged yet. Falling back to the ID for
// deterministic ordering when timestamps are missing.
func intentSortKey(i HubIntent) string {
	if i.SealedAt != "" {
		return i.SealedAt
	}
	return i.ID
}

func buildFileIndex(intents []HubIntent) []HubFileEntry {
	byPath := map[string][]string{}
	for _, in := range intents {
		for _, p := range in.FilesTouched {
			byPath[p] = append(byPath[p], in.ID)
		}
	}
	out := make([]HubFileEntry, 0, len(byPath))
	for path, ids := range byPath {
		out = append(out, HubFileEntry{Path: path, IntentIDs: ids})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func buildActorIndex(intents []HubIntent) []HubActorEntry {
	byActor := map[string]*HubActorEntry{}
	for _, in := range intents {
		key := in.ActorID
		if key == "" {
			continue
		}
		e, ok := byActor[key]
		if !ok {
			e = &HubActorEntry{ActorID: in.ActorID, ActorName: in.ActorName}
			byActor[key] = e
		}
		if e.ActorName == "" && in.ActorName != "" {
			e.ActorName = in.ActorName
		}
		e.IntentIDs = append(e.IntentIDs, in.ID)
	}
	out := make([]HubActorEntry, 0, len(byActor))
	for _, e := range byActor {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ActorID < out[j].ActorID })
	return out
}

// buildRiskList returns the subset of intent IDs whose summary has
// at least one risk string. Order matches the input order, which is
// already newest-first.
func buildRiskList(intents []HubIntent) []string {
	out := make([]string, 0)
	for _, in := range intents {
		if len(in.Risks) > 0 {
			out = append(out, in.ID)
		}
	}
	return out
}

func buildDashboard(m *HubModel) HubDashboard {
	d := HubDashboard{
		TotalIntents: len(m.Intents),
		OpenIntents:  len(m.OpenIntents),
		RiskIntents:  len(m.RiskIntents),
		FileCount:    len(m.FileIndex),
		ActorCount:   len(m.ActorIndex),
	}
	counts := map[string]int{}
	for _, in := range m.Intents {
		counts[in.Status]++
		switch in.Status {
		case string(domain.StatusProposed):
			d.ProposedIntents++
		case string(domain.StatusMerged):
			d.MergedIntents++
		}
	}
	for _, status := range []string{
		string(domain.StatusProposed),
		string(domain.StatusMerged),
		string(domain.StatusSuperseded),
		string(domain.StatusAbandoned),
		string(domain.StatusReverted),
	} {
		if n := counts[status]; n > 0 {
			d.StatusCounts = append(d.StatusCounts, HubStatusCount{Status: status, Count: n})
		}
	}

	now := time.Now()
	seen := map[string]bool{}
	addFocus := func(in HubIntent, reason string) {
		if len(d.Focus) >= 6 || seen[in.ID] {
			return
		}
		seen[in.ID] = true
		hours := ageHours(in.SealedAt, now)
		d.Focus = append(d.Focus, HubFocusIntent{
			ID:        in.ID,
			Title:     in.Title,
			Status:    in.Status,
			Reason:    reason,
			AgeHours:  hours,
			RiskCount: len(in.Risks),
			FileCount: len(in.FilesTouched),
			HighRisk:  len(in.Risks) > 0,
		})
	}
	// Order matters per spec §5.1: proposed first (review queue),
	// high-risk proposed next, then risk-bearing, then recent
	// merged. Within proposed, prefer the oldest so the review
	// queue order matches the dashboard rendering.
	proposed := make([]HubIntent, 0)
	for _, in := range m.Intents {
		if in.Status == string(domain.StatusProposed) {
			proposed = append(proposed, in)
		}
	}
	sort.SliceStable(proposed, func(i, j int) bool {
		ai := ageHours(proposed[i].SealedAt, now)
		aj := ageHours(proposed[j].SealedAt, now)
		// High-risk proposed first; within each bucket, oldest
		// first (largest age).
		hi := len(proposed[i].Risks) > 0
		hj := len(proposed[j].Risks) > 0
		if hi != hj {
			return hi
		}
		return ai > aj
	})
	for _, in := range proposed {
		reason := "waiting for review"
		if len(in.Risks) > 0 {
			reason = fmt.Sprintf("waiting for review · %d recorded risk", len(in.Risks))
		}
		addFocus(in, reason)
	}
	for _, in := range m.Intents {
		if in.Status == string(domain.StatusProposed) {
			continue
		}
		if len(in.Risks) > 0 {
			addFocus(in, fmt.Sprintf("%d recorded risk", len(in.Risks)))
		}
	}
	for _, in := range m.Intents {
		if in.Status == string(domain.StatusMerged) {
			addFocus(in, "recently merged")
		}
	}

	files := append([]HubFileEntry(nil), m.FileIndex...)
	sort.SliceStable(files, func(i, j int) bool {
		if len(files[i].IntentIDs) != len(files[j].IntentIDs) {
			return len(files[i].IntentIDs) > len(files[j].IntentIDs)
		}
		return files[i].Path < files[j].Path
	})
	// Pre-index risk + recent counts per intent so we can populate
	// the decision-hotspots metadata in one pass without an
	// O(intents * files) walk inside the cap loop.
	cutoff := time.Now().AddDate(0, 0, -digestWindowDays)
	intentRisky := map[string]bool{}
	intentRecent := map[string]bool{}
	for _, in := range m.Intents {
		if len(in.Risks) > 0 {
			intentRisky[in.ID] = true
		}
		if t, err := time.Parse(time.RFC3339, in.SealedAt); err == nil && !t.Before(cutoff) {
			intentRecent[in.ID] = true
		}
	}
	for _, f := range files {
		if len(d.HotFiles) >= 8 {
			break
		}
		risk, recent := 0, 0
		for _, id := range f.IntentIDs {
			if intentRisky[id] {
				risk++
			}
			if intentRecent[id] {
				recent++
			}
		}
		d.HotFiles = append(d.HotFiles, HubHotFile{
			Path:            f.Path,
			IntentCount:     len(f.IntentIDs),
			RiskIntentCount: risk,
			RecentCount:     recent,
		})
	}
	return d
}

// buildRelations emits the per-intent edges that the graph view
// renders. Three kinds, in priority order:
//
//  1. supersedes / superseded_by — explicit, recorded by the engine
//     in StatusEvidence. Rendered first because it's the only edge
//     the agent actively wrote.
//  2. conflicts_with — sourced from each intent's LastCheck.
//     AgainstIntents (the phase-2 check judgments). Bidirectional.
//     Skipped if HasConflict is false (a clean check still produces a
//     CheckSummary but should not pollute the graph).
//  3. shares_file — implicit overlap. Emitted when two intents
//     touched ≥1 of the same file. Note carries the count of shared
//     files so the renderer can rank. We cap to the top sharesFileCap
//     overlaps per intent so a fingerprint-heavy repo does not blow
//     up the page; the cap is generous enough for v1 readers and
//     v2 hosted will replace this with a queryable index anyway.
func buildRelations(intents []HubIntent) []HubRelationRow {
	out := make([]HubRelationRow, 0)
	seen := map[string]bool{}
	emit := func(from, kind, to, note string) {
		if from == "" || to == "" || from == to {
			return
		}
		key := from + "|" + kind + "|" + to
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, HubRelationRow{From: from, Kind: kind, To: to, Note: note})
	}

	for _, in := range intents {
		if to := in.SupersededByIntent; to != "" {
			emit(in.ID, "superseded_by", to, "")
			emit(to, "supersedes", in.ID, "")
		}
		if c := in.LastCheck; c != nil && c.HasConflict {
			for _, against := range c.AgainstIntents {
				note := ""
				if c.HighestSeverity != "" {
					note = c.HighestSeverity
				}
				emit(in.ID, "conflicts_with", against, note)
				emit(against, "conflicts_with", in.ID, note)
			}
		}
	}

	for _, row := range buildSharedFileRows(intents) {
		emit(row.From, row.Kind, row.To, row.Note)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return relationKindRank(out[i].Kind) < relationKindRank(out[j].Kind)
		}
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

// sharesFileCap bounds the number of shares_file rows emitted from
// any single intent. Picked low enough to keep the graph readable on
// repos where many intents touch a hot file (e.g. internal/cli/root.go);
// raise once the page has client-side sort/filter.
const sharesFileCap = 3

// sharesFileMinOverlap is the minimum number of shared files required
// to emit a shares_file edge. One shared file is too weak a signal —
// every PR touches root.go or AGENTS.md eventually. Two or more is a
// real co-occurrence pattern worth surfacing.
const sharesFileMinOverlap = 2

func relationKindRank(k string) int {
	switch k {
	case "supersedes", "superseded_by":
		return 0
	case "conflicts_with":
		return 1
	case "shares_file":
		return 2
	}
	return 3
}

// sharedPair is the (a,b,count) triple buildSharedFileRows uses to
// rank file overlaps. Package-level so the per-intent sort closure
// can take *sharedPair values without an anonymous-struct type
// mismatch.
type sharedPair struct {
	a, b  string
	count int
}

func (p *sharedPair) other(id string) string {
	if p.a == id {
		return p.b
	}
	return p.a
}

// buildSharedFileRows finds intent pairs that touched at least one
// common file and emits one bidirectional `shares_file` edge per
// pair, with the shared-file count carried in Note. Pairs are sorted
// by descending overlap weight; we keep up to sharesFileCap pairs
// per intent so a hot file does not produce O(n²) noise.
func buildSharedFileRows(intents []HubIntent) []HubRelationRow {
	pairs := map[string]*sharedPair{}
	files := map[string][]string{}
	for _, in := range intents {
		for _, f := range in.FilesTouched {
			files[f] = append(files[f], in.ID)
		}
	}
	for _, ids := range files {
		if len(ids) < 2 {
			continue
		}
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				a, b := ids[i], ids[j]
				if a > b {
					a, b = b, a
				}
				key := a + "|" + b
				p, ok := pairs[key]
				if !ok {
					p = &sharedPair{a: a, b: b}
					pairs[key] = p
				}
				p.count++
			}
		}
	}

	// Drop pairs that don't clear the minimum-overlap threshold.
	// One-shared-file pairs are dominant noise on real repos.
	for k, p := range pairs {
		if p.count < sharesFileMinOverlap {
			delete(pairs, k)
		}
	}

	// Bucket pair lists per intent so we can cap fan-out per node.
	perIntent := map[string][]*sharedPair{}
	for _, p := range pairs {
		perIntent[p.a] = append(perIntent[p.a], p)
		perIntent[p.b] = append(perIntent[p.b], p)
	}
	keep := map[*sharedPair]bool{}
	for id, list := range perIntent {
		sort.Slice(list, func(i, j int) bool {
			if list[i].count != list[j].count {
				return list[i].count > list[j].count
			}
			return list[i].other(id) < list[j].other(id)
		})
		for i, p := range list {
			if i >= sharesFileCap {
				break
			}
			keep[p] = true
		}
	}

	out := make([]HubRelationRow, 0, len(keep)*2)
	for p := range keep {
		note := fmt.Sprintf("%d shared files", p.count)
		if p.count == 1 {
			note = "1 shared file"
		}
		out = append(out, HubRelationRow{From: p.a, Kind: "shares_file", To: p.b, Note: note})
		out = append(out, HubRelationRow{From: p.b, Kind: "shares_file", To: p.a, Note: note})
	}
	return out
}

// writeSite emits the static directory layout. JSON dump goes first
// (cheap, useful for debugging even if HTML rendering fails); then
// CSS, then HTML pages.
//
// Directory layout (i18n):
//
//   <dir>/
//     assets/style.css
//     data/intents.json
//     index.html  intents/X.html  files/X.html  actors/X.html  …  (EN)
//     zh/
//       index.html  intents/X.html  files/X.html  actors/X.html …  (ZH)
//
// /assets and /data are shared at root — they don't have UI text so
// duplicating them would just bloat the site.
func writeSite(dir string, m *HubModel) error {
	// Per-language directory trees. EN at root; ZH under /zh/.
	for _, lang := range SupportedLanguages {
		base := dir
		if lang != LangEN {
			base = filepath.Join(dir, lang)
		}
		for _, sub := range []string{"intents", "files", "actors"} {
			if err := os.MkdirAll(filepath.Join(base, sub), 0o755); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		return err
	}

	if err := writeJSONDump(dir, m); err != nil {
		return err
	}
	if err := writeAsset(dir, "assets/style.css", embeddedCSS); err != nil {
		return err
	}
	return renderAll(dir, m)
}

func writeJSONDump(dir string, m *HubModel) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("hub: marshal model: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "data", "intents.json"), data, 0o644)
}

func writeAsset(dir, rel, body string) error {
	return os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644)
}

// fileSlug encodes a repo file path into a single safe filename.
// `/` becomes `__`, other punctuation that is unsafe on common
// filesystems is replaced by `_`. Collisions are theoretically
// possible (e.g. `a__b.go` vs `a/b.go`) but extremely unlikely in
// practice and acceptable for v1; v2 will move to API URLs.
func fileSlug(path string) string {
	r := strings.NewReplacer("/", "__", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return r.Replace(path)
}

func actorSlug(id string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return r.Replace(id)
}

// annotateInheritedConstraints walks every HubIntent and attaches
// the inherited anti_patterns from prior intents whose touched
// files / subsystems overlap. Acknowledgement form is computed
// against the source IntentSummary in the view (not the flattened
// HubIntent) so we don't lose any field.
func annotateInheritedConstraints(m *HubModel, view *domain.MainlineView) {
	if view == nil {
		return
	}
	summaryByID := map[string]*domain.IntentSummary{}
	for i := range view.Intents {
		iv := &view.Intents[i]
		summaryByID[iv.IntentID] = iv.Summary
	}
	for i := range m.Intents {
		hi := &m.Intents[i]
		if len(hi.FilesTouched) == 0 && len(hi.Subsystems) == 0 {
			continue
		}
		ics := domain.BuildInheritedConstraints(view, hi.FilesTouched, hi.Subsystems, hi.ID)
		if len(ics) == 0 {
			continue
		}
		out := make([]HubInheritedConstraint, 0, len(ics))
		s := summaryByID[hi.ID]
		for _, ic := range ics {
			h := HubInheritedConstraint{
				SourceIntent: ic.SourceIntent,
				What:         ic.What,
				Why:          ic.Why,
				Severity:     ic.Severity,
				MatchedBy:    append([]string(nil), ic.MatchedBy...),
			}
			if s != nil {
				h.Acknowledgement = string(domain.AcknowledgementOf(ic, s))
			}
			out = append(out, h)
		}
		hi.InheritedConstraints = out
	}
}

// coverageCommitsFromInput shapes engine-supplied rows into the page
// type. Order is preserved (engine returns newest-first already).
func coverageCommitsFromInput(rows []CoverageInputCommit) []HubCoverageCommit {
	out := make([]HubCoverageCommit, 0, len(rows))
	for _, r := range rows {
		out = append(out, HubCoverageCommit{
			Commit:      r.Commit,
			Subject:     r.Subject,
			Author:      r.Author,
			CommittedAt: r.CommittedAt,
			State:       r.State,
			HighRisk:    r.HighRisk,
			SkipReason:  r.SkipReason,
		})
	}
	return out
}
