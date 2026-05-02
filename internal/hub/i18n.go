package hub

// Hub UI internationalisation. Keeps the same model and rendering
// pipeline; renders the static site twice, once per supported
// language. The intent CONTENT (intent title / what / why /
// decisions / risks / anti_patterns) stays in whatever language
// the user sealed it with — that's the AGENTS.md language rule.
// Only the UI chrome (nav, section headers, button labels, aging
// units) is translated.
//
// Why a flat map and not gettext / go-i18n: Hub is a static site
// with a fixed, small string set; the dependency cost of a real
// i18n library is not worth it. New strings are added by editing
// translations[] in Go, not by external .po files.

const (
	LangEN = "en"
	LangZH = "zh"
)

// SupportedLanguages is the canonical list driving the second-pass
// render. Order matters for the header toggle (en first → zh
// renders the toggle as "中文").
var SupportedLanguages = []string{LangEN, LangZH}

// LanguageLabel is what each language calls itself in the
// language-toggle button. Self-naming so a Chinese reader sees
// "English" to switch to it, an English reader sees "中文".
var LanguageLabel = map[string]string{
	LangEN: "English",
	LangZH: "中文",
}

// translations holds every UI string used in any template. Keys are
// dot-namespaced so a future extraction tool could lift them. New
// keys go here, not inline in templates.
var translations = map[string]map[string]string{
	// Top-bar / sidebar
	"header.tagline": {
		LangEN: "Human reading surface for intent memory",
		LangZH: "Intent memory 的人类阅读入口",
	},
	"header.main_at": {
		LangEN: "main",
		LangZH: "main 分支",
	},
	"header.generated": {
		LangEN: "generated",
		LangZH: "生成于",
	},
	"nav.dashboard":     {LangEN: "Dashboard", LangZH: "总览"},
	"nav.intents":       {LangEN: "All intents", LangZH: "全部 intent"},
	"nav.open_work":     {LangEN: "Open work", LangZH: "进行中"},
	"nav.review_queue":  {LangEN: "Review queue", LangZH: "待审"},
	"nav.files":         {LangEN: "Files", LangZH: "文件"},
	"nav.risk_review":   {LangEN: "Constraints & risks", LangZH: "约束与风险"},
	"nav.coverage":      {LangEN: "Coverage", LangZH: "覆盖率"},
	"nav.digest":        {LangEN: "Digest", LangZH: "周报"},
	"nav.relationships": {LangEN: "Relationships", LangZH: "关系图"},
	"nav.data":          {LangEN: "Data", LangZH: "数据"},

	// Dashboard (index page)
	"dashboard.eyebrow": {
		LangEN: "Project intent map",
		LangZH: "项目 intent 全景",
	},
	"dashboard.headline": {
		LangEN: "What changed, why it changed, and what needs attention.",
		LangZH: "改了什么、为什么改、现在哪里需要注意。",
	},
	"dashboard.lead": {
		LangEN: "Hub is the human reading surface for Mainline intent memory. Use it to review pending work, understand file-level constraints, and orient around important decisions. Use the CLI when you need to act: start, seal, lint, sync, or retrieve context.",
		LangZH: "Hub 是 Mainline intent memory 的人类阅读入口。用它理解项目状态、review 待审 intent、查看文件背后的历史约束和重要决策。需要 start / seal / lint / sync / context 时，再回到 CLI 执行动作。",
	},
	"dashboard.sealed_intents": {LangEN: "sealed intents", LangZH: "已封存 intents"},
	"dashboard.open":           {LangEN: "open", LangZH: "进行中"},
	"dashboard.proposed":       {LangEN: "proposed", LangZH: "提案中"},
	"dashboard.with_risks":     {LangEN: "with constraints or risks", LangZH: "带约束或风险"},

	"team_health.label":     {LangEN: "Current attention status", LangZH: "当前关注状态"},
	"team_health.healthy":   {LangEN: "Good overall", LangZH: "整体良好"},
	"team_health.attention": {LangEN: "Needs attention", LangZH: "需要注意"},
	"team_health.critical":  {LangEN: "Critical", LangZH: "严重"},
	"team_health.partial":   {LangEN: "Partial data", LangZH: "数据不完整"},

	"metric.open_work":            {LangEN: "Open work", LangZH: "进行中工作"},
	"metric.review_queue":         {LangEN: "Review queue", LangZH: "待审队列"},
	"metric.risk_bearing_intents": {LangEN: "Intents with constraints or risks", LangZH: "带约束或风险的 intents"},
	"metric.files_with_history":   {LangEN: "Files with history", LangZH: "有历史的文件"},
	"metric.oldest_prefix":        {LangEN: "oldest", LangZH: "最久"},
	"metric.proposed_suffix":      {LangEN: "proposed", LangZH: "提案"},

	"section.needs_attention": {LangEN: "Needs attention", LangZH: "需要关注"},
	"section.needs_attention_empty": {
		LangEN: "No proposed intents, unacknowledged constraints, or recently merged intents to highlight.",
		LangZH: "暂无需要重点关注的提案、未确认约束或新合并的 intent。",
	},
	"section.decision_hotspots": {LangEN: "Decision hotspots", LangZH: "决策热点文件"},
	"section.decision_hotspots_lead": {
		LangEN: "Files with the most intent history. These are areas with concentrated decisions, constraints, or repeated changes.",
		LangZH: "intent 历史最多的文件。这些区域聚集了决策、约束，或者反复变更。",
	},
	"section.decision_hotspots_empty": {
		LangEN: "No file fingerprints recorded yet.",
		LangZH: "还没有文件 fingerprint 记录。",
	},

	"section.intent_coverage": {LangEN: "Intent coverage", LangZH: "Intent 覆盖率"},
	"section.review_aging":    {LangEN: "Review queue aging", LangZH: "待审时长"},
	"section.risk_radar":      {LangEN: "Constraints & risks needing review", LangZH: "需要 review 的约束与风险"},

	"coverage.unavailable_headline": {
		LangEN: "Coverage data unavailable in this Hub build.",
		LangZH: "本次 Hub 构建未携带覆盖率数据。",
	},
	"coverage.unavailable_hint_prefix": {
		LangEN: "Run",
		LangZH: "运行",
	},
	"coverage.unavailable_hint_suffix": {
		LangEN: "on the same repo to see uncovered commits with rescue options.",
		LangZH: "在同一仓库下查看未覆盖的 commit 与补救建议。",
	},
	"coverage.covered_suffix":      {LangEN: "covered", LangZH: "已覆盖"},
	"coverage.uncovered_commits":   {LangEN: "uncovered commit", LangZH: "未覆盖 commit"},
	"coverage.high_risk_uncovered": {LangEN: "possibly high-impact uncovered change", LangZH: "可能高影响的未覆盖变更"},

	"coverage.eyebrow": {LangEN: "Coverage", LangZH: "覆盖率"},
	"coverage.headline": {
		LangEN: "How much of main is captured by sealed intents.",
		LangZH: "main 上有多少 commit 被已封存 intent 覆盖。",
	},
	"coverage.lead": {
		LangEN: "Each main-branch commit is covered, skipped, or uncovered. Uncovered commits are the ones that need an intent.",
		LangZH: "main 上的每个 commit 都会被分类为已覆盖、已跳过或未覆盖。未覆盖的 commit 是需要补 intent 的目标。",
	},
	"coverage.covered_count":   {LangEN: "covered commits", LangZH: "已覆盖 commit"},
	"coverage.uncovered_count": {LangEN: "uncovered commits", LangZH: "未覆盖 commit"},
	"coverage.recent_commits":  {LangEN: "Recent main-branch commits", LangZH: "main 近期 commit"},
	"coverage.window_prefix":   {LangEN: "Last", LangZH: "最近"},
	"coverage.window_suffix":   {LangEN: "commits scanned.", LangZH: "个 commit。"},
	"coverage.col_state":       {LangEN: "State", LangZH: "状态"},
	"coverage.col_commit":      {LangEN: "Commit", LangZH: "Commit"},
	"coverage.col_subject":     {LangEN: "Subject", LangZH: "标题"},
	"coverage.col_author":      {LangEN: "Author", LangZH: "作者"},
	"coverage.col_when":        {LangEN: "When", LangZH: "时间"},
	"coverage.rescue_heading":  {LangEN: "Rescue uncovered commits", LangZH: "补救未覆盖 commit"},
	"coverage.rescue_lead": {
		LangEN: "Run for ready-to-paste commands per uncovered commit:",
		LangZH: "运行下面命令查看每个未覆盖 commit 的补救建议：",
	},

	"digest.eyebrow":  {LangEN: "Digest", LangZH: "周报"},
	"digest.headline": {LangEN: "Recent intent activity at a glance.", LangZH: "近期 intent 活动一览。"},
	"digest.lead": {
		LangEN: "Use mainline digest --since 14d / 30d on the CLI for wider windows.",
		LangZH: "命令行运行 mainline digest --since 14d / 30d 可以查看更长时间窗口。",
	},
	"digest.day_unit":         {LangEN: "days", LangZH: "天"},
	"digest.cli_hint_heading": {LangEN: "From the CLI", LangZH: "命令行入口"},
	"digest.cli_hint_lead": {
		LangEN: "Same data, different windows:",
		LangZH: "同样的数据，可以指定不同的时间窗口：",
	},

	"review.proposed_suffix":           {LangEN: "proposed", LangZH: "提案"},
	"review.no_proposed":               {LangEN: "No proposed intents waiting review.", LangZH: "无待审 intent。"},
	"review.oldest_waiting":            {LangEN: "oldest waiting", LangZH: "最久等待"},
	"review.waiting_over_12h":          {LangEN: "waiting >12h", LangZH: "等待 >12h"},
	"review.waiting_over_24h":          {LangEN: "waiting >24h", LangZH: "等待 >24h"},
	"review.waiting_over_48h_critical": {LangEN: "waiting >48h (critical)", LangZH: "等待 >48h (严重)"},

	"risk.intents_suffix":     {LangEN: "intents with constraints or risks", LangZH: "intent 带约束或风险"},
	"risk.proposed_suffix":    {LangEN: "constraint-bearing proposed", LangZH: "带约束的提案"},
	"risk.this_week_suffix":   {LangEN: "this week", LangZH: "本周"},
	"risk.heavy_files_suffix": {LangEN: "constraint-heavy file", LangZH: "约束密集文件"},
	"risk.missing_mitigation": {LangEN: "risks missing mitigation", LangZH: "风险缺少缓解方案"},

	"digest.this_week":            {LangEN: "This week", LangZH: "本周"},
	"digest.sealed":               {LangEN: "sealed", LangZH: "封存"},
	"digest.proposed":             {LangEN: "proposed", LangZH: "提案"},
	"digest.abandoned":            {LangEN: "abandoned", LangZH: "放弃"},
	"digest.superseded":           {LangEN: "superseded", LangZH: "被替代"},
	"digest.risk_bearing":         {LangEN: "with constraints/risks", LangZH: "带约束/风险"},
	"digest.important_decisions":  {LangEN: "Important decisions", LangZH: "重要决策"},
	"digest.risks_to_watch":       {LangEN: "Soft risks to watch", LangZH: "需关注的软风险"},
	"digest.abandoned_approaches": {LangEN: "Abandoned approaches", LangZH: "被放弃的方案"},
	"digest.files_heating_up":     {LangEN: "Files heating up", LangZH: "升温中的文件"},
	"digest.intents_this_window":  {LangEN: "intents this window", LangZH: "本窗口 intents"},

	"recent.heading":    {LangEN: "Recent intents", LangZH: "近期 intents"},
	"recent.col_id":     {LangEN: "ID", LangZH: "ID"},
	"recent.col_title":  {LangEN: "Title", LangZH: "标题"},
	"recent.col_status": {LangEN: "Status", LangZH: "状态"},
	"recent.col_actor":  {LangEN: "Actor", LangZH: "作者"},
	"recent.col_thread": {LangEN: "Thread", LangZH: "Thread"},
	"recent.col_sealed": {LangEN: "Sealed", LangZH: "封存"},
	"recent.col_files":  {LangEN: "Files", LangZH: "文件"},
	"recent.col_risks":  {LangEN: "Constraints", LangZH: "约束"},

	"toggle.aria": {LangEN: "Switch language", LangZH: "切换语言"},

	"notice.in_flight_singular": {LangEN: "open local intent still in flight", LangZH: "个本地 intent 仍在进行中"},
	"notice.in_flight_plural":   {LangEN: "open local intents still in flight", LangZH: "个本地 intents 仍在进行中"},
	"notice.view_in_flight":     {LangEN: "View in-flight work", LangZH: "查看进行中的工作"},

	"heatmap.heading": {LangEN: "Inherited constraints heatmap", LangZH: "继承约束热点"},
	"heatmap.lead": {
		LangEN: "Files with anti_patterns from prior intents that future work must acknowledge. These are not automatic violation judgments — they flag files where reviewers should check whether inherited constraints were considered.",
		LangZH: "这些文件带有历史 intent 留下的硬约束。「未确认约束触碰」表示最近有 intent 改到该文件，但没有明确说明已考虑这些约束。这不是自动判定违规，而是提醒 reviewer 重点检查。",
	},
	"heatmap.constraints":   {LangEN: "constraints", LangZH: "条约束"},
	"heatmap.high_severity": {LangEN: "high-severity", LangZH: "高严重度"},
	"heatmap.unack_recent":  {LangEN: "unacknowledged constraint touch", LangZH: "未确认约束触碰"},
	"heatmap.cta":           {LangEN: "Before editing this file", LangZH: "查看编辑前须知"},

	"lifecycle.abandonment":  {LangEN: "Abandonment", LangZH: "Abandonment 比例"},
	"lifecycle.supersession": {LangEN: "Supersession", LangZH: "Supersession 比例"},
	"lifecycle.queue_size":   {LangEN: "Review queue", LangZH: "待审队列"},
	"lifecycle.oldest":       {LangEN: "oldest", LangZH: "最久"},

	"recent.expand":       {LangEN: "Show full intent table", LangZH: "展开全部 intent 表格"},
	"sidebar.browse_all":  {LangEN: "Browse all intents", LangZH: "浏览全部 intent"},
	"intents.heading":     {LangEN: "All intents", LangZH: "全部 intent"},
	"intents.lead_suffix": {LangEN: "sealed intents in this view, newest first.", LangZH: "条已封存 intent，最新在前。"},
	"intents.empty":       {LangEN: "No intents recorded yet.", LangZH: "尚无 intent 记录。"},

	"digest.important_decisions_lead": {
		LangEN: "Recent intents that recorded a real decision with rationale.",
		LangZH: "最近留下了真实决策与理由的 intent。",
	},

	"intent.history_heading": {LangEN: "Intent history", LangZH: "Intent 历史"},

	"file.inherited_heading": {LangEN: "Inherited constraints for this file", LangZH: "本文件的继承约束"},
	"file.inherited_lead": {
		LangEN: "Anti_patterns recorded by prior intents that touched this file. Read these before editing.",
		LangZH: "历史 intent 在该文件上留下的硬约束。改这个文件前先看一遍。",
	},
	"file.inherited_empty": {LangEN: "No inherited constraints recorded for this file.", LangZH: "本文件暂无继承约束。"},

	"lifecycle.heading":      {LangEN: "Long-term lifecycle signals", LangZH: "长期生命周期信号"},
	"lifecycle.sealed_total": {LangEN: "sealed intents", LangZH: "已封存 intents"},
	"lifecycle.merged":       {LangEN: "merged", LangZH: "已合并"},
	"lifecycle.proposed":     {LangEN: "proposed", LangZH: "提案中"},
	"lifecycle.abandoned":    {LangEN: "abandoned", LangZH: "放弃"},
	"lifecycle.superseded":   {LangEN: "superseded", LangZH: "被替代"},
	"lifecycle.reverted":     {LangEN: "reverted", LangZH: "回滚"},
	"lifecycle.note": {
		LangEN: "Long-term trend, not current alert. Abandonment and supersession rates reflect rework / decision evolution — not per-person scores.",
		LangZH: "长期趋势，非当前告警。abandonment 和 supersession 比例反映返工/决策演化，不是个人评分。",
	},

	"intent.risks": {LangEN: "Soft risks", LangZH: "软风险"},

	"inherited.heading": {LangEN: "Inherited constraints considered", LangZH: "继承的硬约束"},
	"inherited.lead": {
		LangEN: "Anti-patterns from prior intents whose touched files or subsystems overlap with this intent. High-severity items must be acknowledged in decisions, rejected_alternatives, risks, or your own anti_patterns.",
		LangZH: "来自历史 intent 的硬约束，因为修改的文件或子系统与本 intent 重叠而被继承下来。高风险项必须在 decisions / rejected_alternatives / risks / 自己的 anti_patterns 之一里被确认。",
	},
	"inherited.severity_unspecified": {LangEN: "unspecified", LangZH: "未标注"},
	"inherited.from":                 {LangEN: "from", LangZH: "来自"},
	"inherited.matched_by":           {LangEN: "matched by", LangZH: "命中"},
	"inherited.ack_via":              {LangEN: "acknowledged via", LangZH: "已通过"},
	"inherited.unack":                {LangEN: "not acknowledged", LangZH: "未确认"},

	"actor.heading": {LangEN: "Activity by actor", LangZH: "成员活动"},
	"actor.lead": {
		LangEN: "Distribution of in-flight and recently sealed work. Sorted alphabetically — not a leaderboard.",
		LangZH: "进行中与近期封存工作的分布。按字母序排列，不是排行榜。",
	},
	"actor.in_review":          {LangEN: "in review", LangZH: "待审"},
	"actor.sealed_window":      {LangEN: "sealed this week", LangZH: "本周封存"},
	"actor.no_recent_activity": {LangEN: "no recent activity", LangZH: "本周无活动"},

	"hotfile.intents_singular":   {LangEN: "intent", LangZH: "intent"},
	"hotfile.intents_plural":     {LangEN: "intents", LangZH: "intents"},
	"hotfile.with_risk_singular": {LangEN: "with constraint", LangZH: "带约束"},
	"hotfile.with_risk_plural":   {LangEN: "with constraints", LangZH: "带约束"},

	// Open work, Review, Files, Risks, Graph — minimal chrome only.
	"open.heading":   {LangEN: "Open work", LangZH: "进行中工作"},
	"open.empty":     {LangEN: "No open intents on disk.", LangZH: "本地无进行中 intent。"},
	"review.heading": {LangEN: "Review queue", LangZH: "待审队列"},
	"files.heading":  {LangEN: "Files", LangZH: "文件"},
	"risks.heading":  {LangEN: "Constraints & risks needing review", LangZH: "需要 review 的约束与风险"},
	"graph.heading":  {LangEN: "Relationships", LangZH: "关系图"},

	// File briefing (§6)
	"briefing.heading":              {LangEN: "Before editing this file", LangZH: "编辑前须知"},
	"briefing.lead":                 {LangEN: "Read these before making changes to this file.", LangZH: "改这个文件前，先看看这些。"},
	"briefing.effective_decisions":  {LangEN: "Current effective decisions", LangZH: "当前有效决策"},
	"briefing.abandoned_approaches": {LangEN: "Abandoned approaches", LangZH: "已放弃方案"},
	"briefing.superseded_decisions": {LangEN: "Superseded decisions", LangZH: "已被取代的旧决策"},
	"briefing.recent_proposed":      {LangEN: "Recent proposed work", LangZH: "最近待审 work"},

	// Search (§7)
	"search.placeholder":      {LangEN: "Search intents, files, decisions, risks, anti-patterns...", LangZH: "搜索 intents、文件、决策、风险、anti-patterns..."},
	"search.no_results":       {LangEN: "No results found.", LangZH: "未找到结果。"},
	"search.group_intent":     {LangEN: "Intents", LangZH: "Intents"},
	"search.group_file":       {LangEN: "Files", LangZH: "文件"},
	"search.group_constraint": {LangEN: "Constraints", LangZH: "约束"},
}

// translate is the function `t` template helper resolves to. Falls
// back to the key itself when an entry is missing — that surfaces
// the missing translation in the rendered page rather than silently
// printing empty.
func translate(lang, key string) string {
	if entry, ok := translations[key]; ok {
		if val, ok := entry[lang]; ok && val != "" {
			return val
		}
		// Specific lang missing → English fallback.
		if val, ok := entry[LangEN]; ok && val != "" {
			return val
		}
	}
	return "[" + key + "]"
}
