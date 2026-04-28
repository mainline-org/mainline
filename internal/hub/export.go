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
// OutputDir is required; other fields are reserved for future flags
// (--limit, --since, etc) that v1 does not implement.
type ExportOptions struct {
	OutputDir string
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
	sort.SliceStable(m.Intents, func(i, j int) bool {
		return intentSortKey(m.Intents[i]) > intentSortKey(m.Intents[j])
	})
	m.FileIndex = buildFileIndex(m.Intents)
	m.ActorIndex = buildActorIndex(m.Intents)
	m.RiskIntents = buildRiskList(m.Intents)
	m.Relations = buildRelations(m.Intents)
	m.Dashboard = buildDashboard(m)
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

	seen := map[string]bool{}
	addFocus := func(in HubIntent, reason string) {
		if len(d.Focus) >= 6 || seen[in.ID] {
			return
		}
		seen[in.ID] = true
		d.Focus = append(d.Focus, HubFocusIntent{
			ID:     in.ID,
			Title:  in.Title,
			Status: in.Status,
			Reason: reason,
		})
	}
	for _, in := range m.Intents {
		if in.Status == string(domain.StatusProposed) {
			addFocus(in, "waiting for review")
		}
	}
	for _, in := range m.Intents {
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
	for _, f := range files {
		if len(d.HotFiles) >= 8 {
			break
		}
		d.HotFiles = append(d.HotFiles, HubHotFile{Path: f.Path, IntentCount: len(f.IntentIDs)})
	}
	return d
}

// buildRelations turns the SupersededByIntent links into the simple
// text-graph rows used by graph.html. Both directions are emitted so
// either intent's page can show the link without a second lookup.
func buildRelations(intents []HubIntent) []HubRelationRow {
	out := make([]HubRelationRow, 0)
	for _, in := range intents {
		if to := in.SupersededByIntent; to != "" {
			out = append(out, HubRelationRow{From: in.ID, Kind: "superseded_by", To: to})
			out = append(out, HubRelationRow{From: to, Kind: "supersedes", To: in.ID})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// writeSite emits the static directory layout. JSON dump goes first
// (cheap, useful for debugging even if HTML rendering fails); then
// CSS, then HTML pages.
func writeSite(dir string, m *HubModel) error {
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "intents"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "actors"), 0o755); err != nil {
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
