package hub

import (
	_ "embed"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mainline-org/mainline/internal/domain"
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

//go:embed templates/intents.html
var tplIntents string

//go:embed templates/risks.html
var tplRisks string

//go:embed templates/graph.html
var tplGraph string

//go:embed templates/coverage.html
var tplCoverage string

//go:embed templates/digest.html
var tplDigest string

//go:embed assets/style.css
var embeddedCSS string

//go:embed assets/search.js
var embeddedSearchJS string

// renderAll writes every page in the site, once per supported
// language. EN renders to <dir>/<page>.html (canonical paths kept
// for backward compatibility); ZH renders to <dir>/zh/<page>.html.
// /assets/style.css and /data/intents.json stay shared at root —
// they don't have UI text so duplicating them would just bloat the
// site.
//
// RootPath / OtherLangPath wiring is per-page: top-level vs nested
// each have a different path back to assets/data (RootPath) and a
// different relative path to the same page in the other language
// (OtherLangPath). Pre-computed at context-build time so templates
// don't have to know the directory tree.
func renderAll(dir string, m *HubModel) error {
	tpl, err := buildTemplates()
	if err != nil {
		return fmt.Errorf("hub: parse templates: %w", err)
	}
	for _, lang := range SupportedLanguages {
		if err := renderForLang(dir, m, tpl, lang); err != nil {
			return err
		}
	}
	return nil
}

// renderForLang renders the full site once for the given language.
// EN goes to dir/, ZH to dir/zh/. Per-page contexts get Lang +
// OtherLangPath baked in.
func renderForLang(dir string, m *HubModel, tpl *template.Template, lang string) error {
	intentByID := indexByID(m.Intents)

	// Per-language base directory.
	base := dir
	if lang != LangEN {
		base = filepath.Join(dir, lang)
	}

	render := func(rel, name string, ctx pageCtx) error {
		ctx.Lang = lang
		ctx.Source = m.Source
		ctx.SiblingDrafts = m.SiblingDrafts
		// RootPath and OtherLangPath depend on (lang, depth).
		nested := strings.Contains(rel, "/")
		ctx.RootPath = rootPathFor(lang, nested)
		ctx.LangRoot = langRootFor(nested)
		ctx.OtherLangPath = otherLangPath(lang, rel)
		ctx.OtherLangLabel = LanguageLabel[otherLang(lang)]
		return renderTo(filepath.Join(base, rel), tpl, name, ctx)
	}

	if err := render("index.html", "index", indexCtx(m)); err != nil {
		return err
	}
	if err := render("open.html", "open", openCtx(m)); err != nil {
		return err
	}
	if err := render("intents.html", "intents", intentsCtx(m)); err != nil {
		return err
	}
	if err := render("files.html", "files", filesCtx(m)); err != nil {
		return err
	}
	if err := render("review.html", "review", reviewCtx(m)); err != nil {
		return err
	}
	for i := range m.Intents {
		in := m.Intents[i]
		if err := render("intents/"+in.ID+".html", "intent", intentCtx(m, in, intentByID)); err != nil {
			return err
		}
	}
	for _, f := range m.FileIndex {
		if err := render("files/"+fileSlug(f.Path)+".html", "file", fileCtx(m, f, intentByID)); err != nil {
			return err
		}
	}
	for _, a := range m.ActorIndex {
		if err := render("actors/"+actorSlug(a.ActorID)+".html", "actor", actorCtx(m, a, intentByID)); err != nil {
			return err
		}
	}
	if err := render("risks.html", "risks", risksCtx(m, intentByID)); err != nil {
		return err
	}
	if err := render("graph.html", "graph", graphCtx(m, intentByID)); err != nil {
		return err
	}
	if err := render("coverage.html", "coverage", coverageCtx(m)); err != nil {
		return err
	}
	if err := render("digest.html", "digest", digestCtx(m)); err != nil {
		return err
	}
	return nil
}

// rootPathFor returns the relative path back to dir (the root
// containing /assets and /data) for a page rendered under
// (lang, nested). EN top-level: ""; EN nested: "../"; ZH
// top-level (under /zh/): "../"; ZH nested (under /zh/intents/):
// "../../".
func rootPathFor(lang string, nested bool) string {
	depth := 0
	if lang != LangEN {
		depth++ // /zh/
	}
	if nested {
		depth++ // /intents/, /files/, /actors/
	}
	switch depth {
	case 0:
		return ""
	case 1:
		return "../"
	case 2:
		return "../../"
	default:
		return strings.Repeat("../", depth)
	}
}

// langRootFor returns the relative path back to the SAME-language
// top-level (where index/open/files/etc live). Sidebar nav uses this
// instead of RootPath so a Chinese reader staying inside /zh/ does
// not get bounced back into the EN tree. Only the nested dimension
// matters — the language dimension is a no-op since same-language
// nav stays inside its own subtree.
func langRootFor(nested bool) string {
	if nested {
		return "../"
	}
	return ""
}

// otherLangPath computes the href to the same page rendered in the
// other language, relative to the current page's location. Drives
// the language-toggle button.
func otherLangPath(lang, rel string) string {
	other := otherLang(lang)
	nested := strings.Contains(rel, "/")
	switch {
	case lang == LangEN && other == LangZH:
		// /<rel> → /zh/<rel>; /intents/X.html → ../zh/intents/X.html
		if !nested {
			return "zh/" + rel
		}
		return "../zh/" + rel
	case lang == LangZH && other == LangEN:
		// /zh/<rel> → /<rel>; /zh/intents/X.html → ../../intents/X.html
		if !nested {
			return "../" + rel
		}
		return "../../" + rel
	}
	return rel
}

// otherLang returns the canonical "other" language code given a
// current-language code. Two-language v1; if more languages land
// later this becomes a more complex choice.
func otherLang(lang string) string {
	if lang == LangEN {
		return LangZH
	}
	return LangEN
}

func buildTemplates() (*template.Template, error) {
	funcs := template.FuncMap{
		"shortID":        shortID,
		"shortCommit":    shortCommit,
		"fileSlug":       fileSlug,
		"actorSlug":      actorSlug,
		"timeAgo":        timeAgo,
		"join":           strings.Join,
		"hasPrefix":      strings.HasPrefix,
		"healthLabel":    healthLabel,
		"ageLabel":       ageLabel,
		"coverageRatio":  coverageRatio,
		"lifecycleRatio": lifecycleRatio,
		"deref":          derefInt,
		"t":              translate,
		"tHealth":        translateHealthLabel,
	}
	tpl := template.New("hub").Funcs(funcs)
	for _, src := range []string{tplBase, tplIndex, tplOpen, tplIntents, tplIntent, tplFile, tplFiles, tplReview, tplActor, tplRisks, tplGraph, tplCoverage, tplDigest} {
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

	// LangRoot is the relative path back to the same-language top-
	// level (where index/open/etc live). Used by the sidebar nav so
	// links stay within the current language's subtree. Differs from
	// RootPath only when the current page is under /zh/: RootPath
	// climbs out to root (where /assets and /data live), LangRoot
	// stays inside /zh/.
	LangRoot string

	// Lang is the UI language of the current render: "en" or "zh".
	// Templates pass this to the `t` helper for chrome strings;
	// intent CONTENT (titles, what/why, decisions, risks,
	// anti_patterns) stays as the user wrote it — only chrome
	// translates.
	Lang string

	// OtherLangPath is the relative href from this page to the
	// SAME page in the other language. Used by the language-toggle
	// button in the header. Pre-computed at context-build time so
	// templates don't have to know the directory tree.
	OtherLangPath string

	// OtherLangLabel is what the toggle button shows — the OTHER
	// language's self-name ("English" / "中文"). Computed alongside
	// OtherLangPath so a single template variable drives the link.
	OtherLangLabel string

	Dashboard             HubDashboard
	TeamHealth            HubTeamHealth
	InheritedHotspots     []HubInheritedHotspot
	Intents               []HubIntent
	OpenIntents           []HubOpenIntent
	SiblingDrafts         []HubWorktreeDraft
	ExternalContributions []HubExternalContribution
	Source                HubSource
	FileIndex             []HubFileEntry
	ReviewRows            []HubIntent

	Intent       *HubIntent
	RelatedFiles []string
	IntentLinks  []intentLink

	File          *HubFileEntry
	FileInherited []HubInheritedConstraint
	FileBriefing  *fileBriefing
	Actor         *HubActorEntry

	RiskRows []riskRow

	Relations      []relationRow
	RelationGroups []relationGroup

	CoverageDetail HubCoverageDetail
}

type intentLink struct {
	ID    string
	Title string
	// Authorship — surfaced wherever the link is rendered so reviewers
	// can see who proposed an intent without clicking through.
	ActorID   string
	ActorName string
}

type riskRow struct {
	Intent HubIntent
	Risks  []domain.Risk
}

type relationRow struct {
	From intentLink
	Kind string
	To   intentLink
	Note string
}

// relationGroup is a kind-bucketed list of relationRows so the
// template can render each kind under its own heading without
// running stateful kind-transition logic in template syntax.
type relationGroup struct {
	Kind  string
	Title string
	Lead  string
	Rows  []relationRow
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
		Title:                 "Recent intents",
		GeneratedAt:           m.GeneratedAt,
		MainBranch:            m.MainBranch,
		MainHead:              m.MainHead,
		NavActive:             "index",
		RootPath:              "",
		Dashboard:             m.Dashboard,
		TeamHealth:            m.TeamHealth,
		InheritedHotspots:     m.InheritedHotspots,
		Intents:               m.Intents,
		OpenIntents:           m.OpenIntents,
		ExternalContributions: m.ExternalContributions,
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

// intentsCtx renders the flat all-intents browse page. Sidebar caps
// recent intents at 30; this page is the canonical "show me everything"
// surface so reviewers can scan the whole catalog without paging.
func intentsCtx(m *HubModel) pageCtx {
	return pageCtx{
		Title:       "All intents",
		GeneratedAt: m.GeneratedAt,
		MainBranch:  m.MainBranch,
		MainHead:    m.MainHead,
		NavActive:   "intents",
		RootPath:    "",
		Intents:     m.Intents,
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
	// Pull this file's inherited-constraint list out of the heatmap
	// roll-up so the file page can list each anti_pattern with
	// severity and source — the load-bearing "read before editing"
	// surface for the file.
	var inherited []HubInheritedConstraint
	for _, h := range m.InheritedHotspots {
		if h.FilePath == f.Path {
			inherited = h.Constraints
			break
		}
	}
	briefing := buildFileBriefing(f, byID)
	return pageCtx{
		Title:         f.Path,
		GeneratedAt:   m.GeneratedAt,
		MainBranch:    m.MainBranch,
		MainHead:      m.MainHead,
		NavActive:     "files",
		RootPath:      "../",
		File:          &f,
		FileInherited: inherited,
		FileBriefing:  briefing,
		IntentLinks:   links,
	}
}

// fileBriefing holds the "Before editing this file" data extracted
// from all intents that touched a file, grouped by lifecycle status.
type fileBriefing struct {
	EffectiveDecisions  []briefingDecision
	AbandonedApproaches []briefingApproach
	SupersededDecisions []briefingApproach
	RecentProposed      []intentLink
}

type briefingDecision struct {
	Point     string
	Chose     string
	Rationale string
	IntentID  string
	ActorID   string
	ActorName string
}

type briefingApproach struct {
	Title     string
	Reason    string
	IntentID  string
	ActorID   string
	ActorName string
}

func (b *fileBriefing) HasContent() bool {
	return len(b.EffectiveDecisions) > 0 || len(b.AbandonedApproaches) > 0 ||
		len(b.SupersededDecisions) > 0 || len(b.RecentProposed) > 0
}

func buildFileBriefing(f HubFileEntry, byID map[string]HubIntent) *fileBriefing {
	b := &fileBriefing{}
	for _, id := range f.IntentIDs {
		in, ok := byID[id]
		if !ok {
			continue
		}
		switch in.Status {
		case "merged":
			for _, d := range in.Decisions {
				b.EffectiveDecisions = append(b.EffectiveDecisions, briefingDecision{
					Point: d.Point, Chose: d.Chose, Rationale: d.Rationale, IntentID: in.ID,
					ActorID: in.ActorID, ActorName: in.ActorName,
				})
			}
		case "abandoned":
			reason := in.Why
			if reason == "" && len(in.Risks) > 0 {
				reason = in.Risks[0]
			}
			b.AbandonedApproaches = append(b.AbandonedApproaches, briefingApproach{
				Title: in.Title, Reason: reason, IntentID: in.ID,
				ActorID: in.ActorID, ActorName: in.ActorName,
			})
		case "superseded":
			reason := ""
			if in.SupersededByIntent != "" {
				if by, ok := byID[in.SupersededByIntent]; ok {
					reason = "→ " + by.Title
				}
			}
			if reason == "" {
				reason = in.Why
			}
			b.SupersededDecisions = append(b.SupersededDecisions, briefingApproach{
				Title: in.Title, Reason: reason, IntentID: in.ID,
				ActorID: in.ActorID, ActorName: in.ActorName,
			})
		case "proposed":
			title := in.Title
			if title == "" {
				title = in.ID
			}
			b.RecentProposed = append(b.RecentProposed, intentLink{
				ID: in.ID, Title: title,
				ActorID: in.ActorID, ActorName: in.ActorName,
			})
		}
	}
	if !b.HasContent() {
		return nil
	}
	return b
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
			rows = append(rows, riskRow{Intent: in, Risks: in.OpenRisks})
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

func coverageCtx(m *HubModel) pageCtx {
	return pageCtx{
		Title:          "Coverage",
		GeneratedAt:    m.GeneratedAt,
		MainBranch:     m.MainBranch,
		MainHead:       m.MainHead,
		NavActive:      "coverage",
		RootPath:       "",
		TeamHealth:     m.TeamHealth,
		CoverageDetail: m.CoverageDetail,
	}
}

func digestCtx(m *HubModel) pageCtx {
	return pageCtx{
		Title:       "Digest",
		GeneratedAt: m.GeneratedAt,
		MainBranch:  m.MainBranch,
		MainHead:    m.MainHead,
		NavActive:   "digest",
		RootPath:    "",
		TeamHealth:  m.TeamHealth,
	}
}

func graphCtx(m *HubModel, byID map[string]HubIntent) pageCtx {
	rows := make([]relationRow, 0, len(m.Relations))
	for _, r := range m.Relations {
		rows = append(rows, relationRow{
			From: linkFor(byID, r.From, ""),
			Kind: r.Kind,
			To:   linkFor(byID, r.To, ""),
			Note: r.Note,
		})
	}
	groups := groupRelations(rows)
	return pageCtx{
		Title:          "Intent relationships",
		GeneratedAt:    m.GeneratedAt,
		MainBranch:     m.MainBranch,
		MainHead:       m.MainHead,
		NavActive:      "graph",
		RootPath:       "",
		Relations:      rows,
		RelationGroups: groups,
	}
}

// groupRelations buckets relation rows by kind so the template can
// render each kind under its own heading. Within each group rows
// keep the deterministic order produced by buildRelations.
//
// `superseded_by` and `supersedes` are merged into one user-facing
// "Supersessions" group because the page already shows both ends of
// every link; splitting them just doubles the heading without
// adding information.
func groupRelations(rows []relationRow) []relationGroup {
	specs := []struct {
		kinds []string
		title string
		lead  string
	}{
		{[]string{"supersedes", "superseded_by"}, "Supersessions",
			"Explicit replaces / replaced-by recorded by the engine. Strongest signal — the agent wrote this."},
		{[]string{"conflicts_with"}, "Conflicts (latest check)",
			"Phase-2 check judgments. Investigate before merging either side."},
		{[]string{"shares_file"}, "Shared files",
			"Implicit overlap. Often benign, but useful to spot competing work on the same surface."},
	}
	groups := make([]relationGroup, 0, len(specs))
	for _, sp := range specs {
		var bucket []relationRow
		for _, r := range rows {
			for _, k := range sp.kinds {
				if r.Kind == k {
					// shares_file is symmetric; keep only one
					// direction so the page shows each pair once.
					if r.Kind == "shares_file" && r.From.ID >= r.To.ID {
						break
					}
					bucket = append(bucket, r)
					break
				}
			}
		}
		if len(bucket) == 0 {
			continue
		}
		groups = append(groups, relationGroup{
			Kind:  sp.kinds[0],
			Title: sp.title,
			Lead:  sp.lead,
			Rows:  bucket,
		})
	}
	return groups
}

func linkFor(byID map[string]HubIntent, id, prefix string) intentLink {
	if in, ok := byID[id]; ok {
		title := in.Title
		if title == "" {
			title = id
		}
		return intentLink{
			ID: id, Title: prefix + title,
			ActorID: in.ActorID, ActorName: in.ActorName,
		}
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

// translateHealthLabel is healthLabel's i18n-aware sibling: takes
// (lang, level) instead of just level. Templates use this when the
// page has a Lang context; legacy callers can still use healthLabel
// for the English-only fallback.
func translateHealthLabel(lang, level string) string {
	switch level {
	case "healthy":
		return translate(lang, "team_health.healthy")
	case "attention":
		return translate(lang, "team_health.attention")
	case "critical":
		return translate(lang, "team_health.critical")
	default:
		return translate(lang, "team_health.partial")
	}
}

// healthLabel maps the team-health enum to user-facing copy. Spec
// §4.3 three buckets; empty value (partial-data path) becomes
// "Partial data" so the dashboard never reads as healthy when core
// inputs are missing.
func healthLabel(level string) string {
	switch level {
	case "healthy":
		return "Good overall"
	case "attention":
		return "Needs attention"
	case "critical":
		return "Critical"
	default:
		return "Partial data"
	}
}

// ageLabel renders an integer-hours age as a short human string.
// "1h", "23h", "2d", "5d". Days are truncated, not rounded — under-
// promising on age is more useful than over.
func ageLabel(hours int) string {
	if hours <= 0 {
		return "<1h"
	}
	if hours < 48 {
		return fmt.Sprintf("%dh", hours)
	}
	days := hours / 24
	return fmt.Sprintf("%dd", days)
}

// lifecycleRatio formats a 0..1 float as a one-decimal percentage
// for the lifecycle card. Different from coverageRatio because the
// numbers are smaller (typical 5-15% range) and one decimal carries
// meaningful signal: "5.5% abandoned" reads better than "6%".
func lifecycleRatio(r float64) string {
	if r <= 0 {
		return "0%"
	}
	pct := r * 100
	if pct >= 100 {
		return "100%"
	}
	return fmt.Sprintf("%.1f%%", pct)
}

// coverageRatio formats a 0..1 float as a percentage with one
// decimal when needed, otherwise integer percent. 0.967 → "97%",
// 0.9678 → "97%", 1.0 → "100%".
func coverageRatio(r float64) string {
	if r <= 0 {
		return "0%"
	}
	if r >= 1 {
		return "100%"
	}
	pct := r * 100
	return fmt.Sprintf("%d%%", int(pct+0.5))
}

// derefInt unwraps an *int for the Risks-missing-mitigation field.
// nil reads as 0 in templates by accident; explicit deref makes the
// "field not available" branch obvious in the template.
func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
