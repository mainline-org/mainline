package hub

import (
	_ "embed"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed templates/base.html
var tplBase string

//go:embed templates/index.html
var tplIndex string

//go:embed templates/intent.html
var tplIntent string

//go:embed templates/file.html
var tplFile string

//go:embed templates/files.html
var tplFiles string

//go:embed templates/review.html
var tplReview string

//go:embed templates/actor.html
var tplActor string

//go:embed templates/open.html
var tplOpen string

//go:embed templates/risks.html
var tplRisks string

//go:embed templates/graph.html
var tplGraph string

//go:embed assets/style.css
var embeddedCSS string

// renderAll writes every page in the site. Templates compose via the
// shared base layout. Each page passes a small context struct so the
// template can reach the surrounding model when it needs to (e.g.
// resolving an intent ID it links to into a title).
func renderAll(dir string, m *HubModel) error {
	tpl, err := buildTemplates()
	if err != nil {
		return fmt.Errorf("hub: parse templates: %w", err)
	}
	intentByID := indexByID(m.Intents)

	if err := renderTo(filepath.Join(dir, "index.html"), tpl, "index", indexCtx(m)); err != nil {
		return err
	}
	if err := renderTo(filepath.Join(dir, "open.html"), tpl, "open", openCtx(m)); err != nil {
		return err
	}
	if err := renderTo(filepath.Join(dir, "files.html"), tpl, "files", filesCtx(m)); err != nil {
		return err
	}
	if err := renderTo(filepath.Join(dir, "review.html"), tpl, "review", reviewCtx(m)); err != nil {
		return err
	}
	for i := range m.Intents {
		in := m.Intents[i]
		path := filepath.Join(dir, "intents", in.ID+".html")
		if err := renderTo(path, tpl, "intent", intentCtx(m, in, intentByID)); err != nil {
			return err
		}
	}
	for _, f := range m.FileIndex {
		path := filepath.Join(dir, "files", fileSlug(f.Path)+".html")
		if err := renderTo(path, tpl, "file", fileCtx(m, f, intentByID)); err != nil {
			return err
		}
	}
	for _, a := range m.ActorIndex {
		path := filepath.Join(dir, "actors", actorSlug(a.ActorID)+".html")
		if err := renderTo(path, tpl, "actor", actorCtx(m, a, intentByID)); err != nil {
			return err
		}
	}
	if err := renderTo(filepath.Join(dir, "risks.html"), tpl, "risks", risksCtx(m, intentByID)); err != nil {
		return err
	}
	if err := renderTo(filepath.Join(dir, "graph.html"), tpl, "graph", graphCtx(m, intentByID)); err != nil {
		return err
	}
	return nil
}

func buildTemplates() (*template.Template, error) {
	funcs := template.FuncMap{
		"shortID":     shortID,
		"shortCommit": shortCommit,
		"fileSlug":    fileSlug,
		"actorSlug":   actorSlug,
		"timeAgo":     timeAgo,
		"join":        func(s []string, sep string) string { return strings.Join(s, sep) },
		"hasPrefix":   strings.HasPrefix,
	}
	tpl := template.New("hub").Funcs(funcs)
	for _, src := range []string{tplBase, tplIndex, tplOpen, tplIntent, tplFile, tplFiles, tplReview, tplActor, tplRisks, tplGraph} {
		var err error
		tpl, err = tpl.Parse(src)
		if err != nil {
			return nil, err
		}
	}
	return tpl, nil
}

func renderTo(path string, tpl *template.Template, name string, data any) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("hub: create %s: %w", path, err)
	}
	defer f.Close()
	return tpl.ExecuteTemplate(f, name, data)
}

// pageCtx is the shape every template receives; embedding the model
// keeps lookups (intent-by-id) one indirection away in the template.
//
// RootPath is "" for top-level pages (index, risks, graph) and "../"
// for pages nested one level deep (intents/<id>.html, files/*.html,
// actors/*.html). Templates concatenate it before any href so the
// site is fully relative — no Origin assumed.
type pageCtx struct {
	Title       string
	GeneratedAt string
	MainBranch  string
	MainHead    string
	NavActive   string
	RootPath    string

	Dashboard   HubDashboard
	Intents     []HubIntent
	OpenIntents []HubOpenIntent
	FileIndex   []HubFileEntry
	ReviewRows  []HubIntent

	Intent       *HubIntent
	RelatedFiles []string
	IntentLinks  []intentLink

	File  *HubFileEntry
	Actor *HubActorEntry

	RiskRows []riskRow

	Relations []relationRow
}

type intentLink struct {
	ID    string
	Title string
}

type riskRow struct {
	Intent HubIntent
	Risks  []string
}

type relationRow struct {
	From intentLink
	Kind string
	To   intentLink
}

func indexByID(intents []HubIntent) map[string]HubIntent {
	out := make(map[string]HubIntent, len(intents))
	for _, in := range intents {
		out[in.ID] = in
	}
	return out
}

func indexCtx(m *HubModel) pageCtx {
	return pageCtx{
		Title:       "Recent intents",
		GeneratedAt: m.GeneratedAt,
		MainBranch:  m.MainBranch,
		MainHead:    m.MainHead,
		NavActive:   "index",
		RootPath:    "",
		Dashboard:   m.Dashboard,
		Intents:     m.Intents,
		OpenIntents: m.OpenIntents,
	}
}

func openCtx(m *HubModel) pageCtx {
	return pageCtx{
		Title:       "Open work",
		GeneratedAt: m.GeneratedAt,
		MainBranch:  m.MainBranch,
		MainHead:    m.MainHead,
		NavActive:   "open",
		RootPath:    "",
		OpenIntents: m.OpenIntents,
	}
}

func filesCtx(m *HubModel) pageCtx {
	return pageCtx{
		Title:       "Files",
		GeneratedAt: m.GeneratedAt,
		MainBranch:  m.MainBranch,
		MainHead:    m.MainHead,
		NavActive:   "files",
		RootPath:    "",
		FileIndex:   m.FileIndex,
	}
}

func reviewCtx(m *HubModel) pageCtx {
	rows := make([]HubIntent, 0)
	for _, in := range m.Intents {
		if in.Status == "proposed" {
			rows = append(rows, in)
		}
	}
	return pageCtx{
		Title:       "Review queue",
		GeneratedAt: m.GeneratedAt,
		MainBranch:  m.MainBranch,
		MainHead:    m.MainHead,
		NavActive:   "review",
		RootPath:    "",
		ReviewRows:  rows,
	}
}

func intentCtx(m *HubModel, in HubIntent, byID map[string]HubIntent) pageCtx {
	links := []intentLink{}
	if to := in.SupersededByIntent; to != "" {
		links = append(links, linkFor(byID, to, "superseded by "))
	}
	for _, r := range m.Relations {
		if r.From == in.ID && r.Kind == "supersedes" {
			links = append(links, linkFor(byID, r.To, "supersedes "))
		}
	}
	return pageCtx{
		Title:       in.ID,
		GeneratedAt: m.GeneratedAt,
		MainBranch:  m.MainBranch,
		MainHead:    m.MainHead,
		NavActive:   "intent",
		RootPath:    "../",
		Intent:      &in,
		IntentLinks: links,
	}
}

func fileCtx(m *HubModel, f HubFileEntry, byID map[string]HubIntent) pageCtx {
	links := make([]intentLink, 0, len(f.IntentIDs))
	for _, id := range f.IntentIDs {
		links = append(links, linkFor(byID, id, ""))
	}
	return pageCtx{
		Title:       f.Path,
		GeneratedAt: m.GeneratedAt,
		MainBranch:  m.MainBranch,
		MainHead:    m.MainHead,
		NavActive:   "files",
		RootPath:    "../",
		File:        &f,
		IntentLinks: links,
	}
}

func actorCtx(m *HubModel, a HubActorEntry, byID map[string]HubIntent) pageCtx {
	links := make([]intentLink, 0, len(a.IntentIDs))
	for _, id := range a.IntentIDs {
		links = append(links, linkFor(byID, id, ""))
	}
	return pageCtx{
		Title:       displayActor(a),
		GeneratedAt: m.GeneratedAt,
		MainBranch:  m.MainBranch,
		MainHead:    m.MainHead,
		NavActive:   "actors",
		RootPath:    "../",
		Actor:       &a,
		IntentLinks: links,
	}
}

func risksCtx(m *HubModel, byID map[string]HubIntent) pageCtx {
	rows := make([]riskRow, 0, len(m.RiskIntents))
	for _, id := range m.RiskIntents {
		if in, ok := byID[id]; ok {
			rows = append(rows, riskRow{Intent: in, Risks: in.Risks})
		}
	}
	return pageCtx{
		Title:       "Risk-heavy intents",
		GeneratedAt: m.GeneratedAt,
		MainBranch:  m.MainBranch,
		MainHead:    m.MainHead,
		NavActive:   "risks",
		RootPath:    "",
		RiskRows:    rows,
	}
}

func graphCtx(m *HubModel, byID map[string]HubIntent) pageCtx {
	rows := make([]relationRow, 0, len(m.Relations))
	for _, r := range m.Relations {
		rows = append(rows, relationRow{
			From: linkFor(byID, r.From, ""),
			Kind: r.Kind,
			To:   linkFor(byID, r.To, ""),
		})
	}
	return pageCtx{
		Title:       "Intent relationships",
		GeneratedAt: m.GeneratedAt,
		MainBranch:  m.MainBranch,
		MainHead:    m.MainHead,
		NavActive:   "graph",
		RootPath:    "",
		Relations:   rows,
	}
}

func linkFor(byID map[string]HubIntent, id, prefix string) intentLink {
	if in, ok := byID[id]; ok {
		title := in.Title
		if title == "" {
			title = id
		}
		return intentLink{ID: id, Title: prefix + title}
	}
	return intentLink{ID: id, Title: prefix + id}
}

func displayActor(a HubActorEntry) string {
	if a.ActorName != "" {
		return a.ActorName + " (" + a.ActorID + ")"
	}
	return a.ActorID
}

func shortID(id string) string {
	if strings.HasPrefix(id, "int_") && len(id) > 12 {
		return id[:12]
	}
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

// timeAgo returns a coarse human-readable interval like "2h ago" or
// "3d ago". Falls back to the raw string when parsing fails so the
// template never silently drops a value.
func timeAgo(s string) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
