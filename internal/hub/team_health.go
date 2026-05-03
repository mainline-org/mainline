package hub

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Team-health computation per docs_for_ai/hub-team-health spec.
//
// Pure: every input is data already on the HubModel; no I/O. Built
// once in BuildHubModel and reused by both the JSON dump and the
// dashboard renderer.
//
// What this file does NOT do:
//
// Coverage data is wired in by the export step (engine.CoverageWindow
// → HubCoverageSummary); when the engine layer hasn't supplied it the
// summary stays Available=false and renderer shows the partial-data
// wording rather than a fake "100% covered".
//
// Actor activity + Lifecycle health are populated here too — see
// populateActorActivity and populateLifecycle. They are explicitly
// non-rankings: actor activity shows distribution of in-flight work,
// lifecycle shows status mix + supersession/abandonment ratios. Spec
// §11/§12 reminder: never read like a productivity panel.

const (
	// digestWindowDays is the rolling time window for the weekly
	// digest section on the hub dashboard. Spec §10 default is 7
	// days. populateDigest is parametric (BuildDigest takes a
	// windowDays arg) so `mainline digest --since 30d` reuses the
	// same code path with a different window.
	digestWindowDays = 7

	// agingProposedWarningHours / agingProposedStaleHours / etc.
	// drive the review-queue and open-work aging buckets. Spec §7.3
	// values, hardcoded for v1.
	agingProposedWarningHours  = 12
	agingProposedStaleHours    = 24
	agingProposedCriticalHours = 48
	agingProposedCleanupHours  = 72
	agingOpenStaleHours        = 24
	agingOpenCriticalHours     = 72

	// hotspotsTopN bounds the number of hot files surfaced in the
	// digest's "hot files this window" rollup. Dashboard's main
	// hot files list keeps its own (larger) cap.
	digestHotspotsTopN = 5

	// digestImportantDecisionsN / digestRisksN cap the prose-y
	// digest sections so the dashboard stays scannable.
	digestImportantDecisionsN = 5
	digestRisksN              = 5
	digestAbandonedN          = 3
)

// buildTeamHealth populates HubTeamHealth from the rest of the
// HubModel. Must be called AFTER Intents / OpenIntents / FileIndex
// have been populated, because every metric reads from them.
//
// "now" is the reference time. Tests pass an explicit value so age
// buckets are reproducible; production passes time.Now().
func buildTeamHealth(m *HubModel, now time.Time) HubTeamHealth {
	t := HubTeamHealth{
		TotalIntents:     len(m.Intents),
		OpenIntents:      len(m.OpenIntents),
		ProposedIntents:  m.Dashboard.ProposedIntents,
		RiskIntentCount:  m.Dashboard.RiskIntents,
		FilesWithHistory: m.Dashboard.FileCount,
	}

	t.populateAging(m, now)
	t.populateRisk(m, now)
	t.Digest = BuildDigest(m.Intents, digestWindowDays, now)
	t.populateActorActivity(m, now)
	t.populateLifecycle(m)
	// Coverage may already be set by the export step. When it is not
	// (Available=false), populateHealthLevel surfaces the partial-
	// data wording rather than pretending healthy.
	t.populateHealthLevel()

	return t
}

func (t *HubTeamHealth) populateAging(m *HubModel, now time.Time) {
	oldestProposedH := 0
	for _, in := range m.Intents {
		if in.Status != "proposed" {
			continue
		}
		hours := ageHours(in.SealedAt, now)
		if hours <= 0 {
			continue
		}
		if hours > oldestProposedH {
			oldestProposedH = hours
		}
		if hours >= agingProposedWarningHours {
			t.ProposedOlderThan12h++
		}
		if hours >= agingProposedStaleHours {
			t.ProposedOlderThan24h++
		}
		if hours >= agingProposedCriticalHours {
			t.ProposedOlderThan48h++
		}
		if hours >= agingProposedCleanupHours {
			t.ProposedOlderThan72h++
		}
	}
	t.OldestProposedHours = oldestProposedH

	for _, op := range m.OpenIntents {
		hours := openIntentAgeHours(op, now)
		if hours <= 0 {
			continue
		}
		if hours >= agingOpenStaleHours {
			t.OpenOlderThan24h++
		}
		if hours >= agingOpenCriticalHours {
			t.OpenOlderThan72h++
		}
	}
}

func (t *HubTeamHealth) populateRisk(m *HubModel, now time.Time) {
	cutoff := now.AddDate(0, 0, -digestWindowDays)
	t.Risk.RiskBearingIntents = m.Dashboard.RiskIntents

	rowsByPath := map[string]int{}
	recentRiskHistByPath := map[string]int{}

	for _, in := range m.Intents {
		isRisky := isEffectiveRiskBearing(in)
		if !isRisky {
			continue
		}
		// proposed-with-risks subset is the most actionable surface.
		if in.Status == "proposed" {
			t.Risk.RiskBearingProposed++
			t.Risk.RiskBearingProposedRows = append(t.Risk.RiskBearingProposedRows, focusFromIntent(in,
				"proposed intent with constraints or risks waiting review", now))
		}
		// recent risk-bearing within the digest window.
		if t, ok := parseTime(in.SealedAt); ok && !t.Before(cutoff) {
			t2 := t // avoid shadowing in closure
			_ = t2
			// keep the count; recent-risk panel renders in digest
		}
		if t2, ok := parseTime(in.SealedAt); ok && !t2.Before(cutoff) {
			t.Risk.RecentRiskBearing++
		}
		// Risk-heavy files: count risk-bearing intents that touched
		// each file path; surface the top files.
		for _, f := range in.FilesTouched {
			rowsByPath[f]++
			if t2, ok := parseTime(in.SealedAt); ok && !t2.Before(cutoff) {
				recentRiskHistByPath[f]++
			}
		}
	}

	// Top risk-heavy files: sort by risk-bearing-intent count desc,
	// break ties by alphabetical so output is deterministic.
	type riskFile struct {
		path   string
		count  int
		recent int
	}
	files := make([]riskFile, 0, len(rowsByPath))
	for p, c := range rowsByPath {
		files = append(files, riskFile{p, c, recentRiskHistByPath[p]})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].count != files[j].count {
			return files[i].count > files[j].count
		}
		return files[i].path < files[j].path
	})
	for i, f := range files {
		if i >= 5 {
			break
		}
		if f.count < 2 {
			break // single-intent files are noise here
		}
		t.Risk.RiskHeavyFiles = append(t.Risk.RiskHeavyFiles, HubHotFile{
			Path:            f.path,
			IntentCount:     f.count,
			RiskIntentCount: f.count,
			RecentCount:     f.recent,
		})
	}

	// RisksMissingMitigation: heuristic per spec §8.2 (left nil-able
	// because the heuristic is fragile). Render-side hides the line
	// when nil. v1 leaves it nil — opt-in once the heuristic proves
	// itself.
	t.Risk.RisksMissingMitigation = nil

	// Stable order on the proposed-rows list: oldest first within
	// proposed (matches review-queue ordering).
	sort.SliceStable(t.Risk.RiskBearingProposedRows, func(i, j int) bool {
		return t.Risk.RiskBearingProposedRows[i].AgeHours > t.Risk.RiskBearingProposedRows[j].AgeHours
	})
}

// CoverageInputCommit is the engine→hub bridge type for coverage. It
// is intentionally narrow (covered/skipped/uncovered + commit + risk
// flag) so the engine layer can build it from CommitCoverage without
// the hub package importing engine.
type CoverageInputCommit struct {
	Commit      string `json:"commit"`
	Subject     string `json:"subject"`
	Author      string `json:"author"`
	CommittedAt string `json:"committed_at"`
	State       string `json:"state"` // "covered" | "skipped" | "uncovered"
	HighRisk    bool   `json:"high_risk,omitempty"`
	SkipReason  string `json:"skip_reason,omitempty"`
}

// BuildCoverageSummary collapses a slice of per-commit coverage rows
// into the HubCoverageSummary shape. Keep separate from BuildCoverage
// for callers that only need the aggregate counts (e.g. JSON dump
// and the team-health card).
func BuildCoverageSummary(rows []CoverageInputCommit) HubCoverageSummary {
	out := HubCoverageSummary{Available: true}
	for _, c := range rows {
		switch c.State {
		case "covered":
			out.CoveredCommits++
		case "uncovered":
			out.UncoveredCommits++
			if c.HighRisk {
				out.HighRiskUncoveredCommits++
			}
		}
	}
	total := out.CoveredCommits + out.UncoveredCommits
	if total > 0 {
		out.CoverageRatio = float64(out.CoveredCommits) / float64(total)
	}
	return out
}

// BuildDigest computes a HubWeeklyDigest over an arbitrary window.
// Pure: takes the intent slice + window in days + reference time and
// returns the rollup. Used by both the hub dashboard's 7-day digest
// and the `mainline digest --since` CLI (which can set
// windowDays = 7 / 14 / 30 / etc.).
//
// Counts are computed across SealedAt timestamps: an intent is "in
// the window" if SealedAt parses and is within [now-windowDays, now].
// HotFilesThisWindow / ImportantDecisions / RisksToWatch /
// AbandonedApproaches are bounded by the digest* caps so the
// dashboard view stays scannable; the CLI human-format render shows
// the same caps.
func BuildDigest(intents []HubIntent, windowDays int, now time.Time) HubWeeklyDigest {
	if windowDays <= 0 {
		windowDays = digestWindowDays
	}
	out := HubWeeklyDigest{WindowDays: windowDays}
	cutoff := now.AddDate(0, 0, -windowDays)
	hotByPath := map[string]int{}

	for _, in := range intents {
		ts, ok := parseTime(in.SealedAt)
		if !ok || ts.Before(cutoff) {
			continue
		}
		switch in.Status {
		case "merged":
			out.SealedThisWindow++
		case "proposed":
			out.ProposedThisWindow++
		case "abandoned":
			out.AbandonedThisWindow++
			if len(out.AbandonedApproaches) < digestAbandonedN {
				out.AbandonedApproaches = append(out.AbandonedApproaches,
					focusFromIntent(in, "abandoned this window", now))
			}
		case "superseded":
			out.SupersededThisWindow++
		}
		if isEffectiveRiskBearing(in) {
			out.RiskBearingThisWindow++
			if len(out.RisksToWatch) < digestRisksN {
				reason := "has constraints or risks"
				if len(in.OpenRisks) > 0 {
					reason = trimReason(in.OpenRisks[0].Text)
				}
				out.RisksToWatch = append(out.RisksToWatch, focusFromIntent(in, reason, now))
			}
		}
		if hasMaterialDecision(in) && len(out.ImportantDecisions) < digestImportantDecisionsN {
			d := in.Decisions[0]
			reason := d.Point + ": " + d.Chose
			out.ImportantDecisions = append(out.ImportantDecisions,
				focusFromIntent(in, trimReason(reason), now))
		}
		for _, f := range in.FilesTouched {
			if !isHotFileNoise(f) {
				hotByPath[f]++
			}
		}
	}

	type wp struct {
		path  string
		count int
	}
	wps := make([]wp, 0, len(hotByPath))
	for p, c := range hotByPath {
		wps = append(wps, wp{p, c})
	}
	sort.Slice(wps, func(i, j int) bool {
		if wps[i].count != wps[j].count {
			return wps[i].count > wps[j].count
		}
		return wps[i].path < wps[j].path
	})
	for i, w := range wps {
		if i >= digestHotspotsTopN {
			break
		}
		if w.count < 2 {
			break
		}
		out.HotFilesThisWindow = append(out.HotFilesThisWindow, HubHotFile{
			Path:        w.path,
			IntentCount: w.count,
		})
	}
	return out
}

// populateActorActivity surfaces how work is distributed across
// agents/humans WITHOUT framing the section as a productivity panel.
// The wording is "who is in flight right now" / "who sealed in the
// window", not rankings or output counts. Spec §11 reminder: this
// must never read as performance review fodder; we omit any "best /
// most" ordering and present alphabetically by actor.
func (t *HubTeamHealth) populateActorActivity(m *HubModel, now time.Time) {
	cutoff := now.AddDate(0, 0, -digestWindowDays)
	openByActor := map[string]int{}
	sealedByActor := map[string]int{}
	nameByActor := map[string]string{}

	for _, op := range m.OpenIntents {
		// Open intents on disk don't carry an actor label in the v1
		// HubOpenIntent shape; skip them. The view-side intents below
		// cover the merged/proposed/etc. surface that does have actor.
		_ = op
	}
	for _, in := range m.Intents {
		if in.ActorID == "" {
			continue
		}
		if in.ActorName != "" {
			nameByActor[in.ActorID] = in.ActorName
		}
		if in.Status == "proposed" {
			openByActor[in.ActorID]++
		}
		if ts, ok := parseTime(in.SealedAt); ok && !ts.Before(cutoff) && in.Status == "merged" {
			sealedByActor[in.ActorID]++
		}
	}

	rows := make([]HubActorActivity, 0, len(nameByActor))
	for id := range nameByActor {
		rows = append(rows, HubActorActivity{
			ActorID:          id,
			ActorName:        nameByActor[id],
			OpenProposed:     openByActor[id],
			SealedThisWindow: sealedByActor[id],
		})
	}
	// Alphabetical by ActorID — deliberate. Ranking by counts would
	// turn the section into a leaderboard; we only show distribution.
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].ActorID < rows[j].ActorID
	})
	t.ActorActivity = rows
}

// populateLifecycle is the status-mix card. Counts of intents in each
// terminal state plus two derived ratios (supersession rate,
// abandonment rate) over the full sealed catalog. Used by the
// dashboard "Lifecycle health" card and as a precursor to the
// historical view that v2 will offer.
func (t *HubTeamHealth) populateLifecycle(m *HubModel) {
	out := HubLifecycleHealth{}
	for _, in := range m.Intents {
		switch in.Status {
		case "proposed":
			out.Proposed++
		case "merged":
			out.Merged++
		case "abandoned":
			out.Abandoned++
		case "superseded":
			out.Superseded++
		case "reverted":
			out.Reverted++
		}
	}
	total := out.Proposed + out.Merged + out.Abandoned + out.Superseded + out.Reverted
	out.Total = total
	if total > 0 {
		out.SupersessionRate = float64(out.Superseded) / float64(total)
		out.AbandonmentRate = float64(out.Abandoned+out.Reverted) / float64(total)
	}
	// Verdict heuristic: spec §12. Not tied to coverage availability;
	// purely lifecycle distribution. Critical when supersession or
	// abandonment + reverted dominate (>30% combined). Attention when
	// either rate alone is over 15%. Otherwise healthy.
	combined := out.SupersessionRate + out.AbandonmentRate
	switch {
	case total < 5:
		out.Verdict = "" // not enough sample to judge
	case combined >= 0.30:
		out.Verdict = "critical"
	case out.AbandonmentRate >= 0.15 || out.SupersessionRate >= 0.15:
		out.Verdict = "attention"
	default:
		out.Verdict = "healthy"
	}
	t.Lifecycle = out
}

// populateHealthLevel: spec §4.3 three-bucket classifier. Order
// matters — critical signals override attention signals. When core
// inputs are unavailable (coverage opt-in), we mark the level empty
// and the renderer says "Partial data" rather than pretending
// healthy.
func (t *HubTeamHealth) populateHealthLevel() {
	criticalSignals := 0
	if t.Coverage.Available && t.Coverage.HighRiskUncoveredCommits > 0 {
		criticalSignals++
	}
	if t.ProposedOlderThan72h > 0 {
		criticalSignals++
	}
	if t.OpenOlderThan72h > 0 {
		criticalSignals++
	}

	attentionSignals := 0
	if t.Coverage.Available && t.Coverage.UncoveredCommits > 0 {
		attentionSignals++
	}
	if t.ProposedOlderThan24h > 0 {
		attentionSignals++
	}
	if t.OpenOlderThan24h > 0 {
		attentionSignals++
	}
	if t.Risk.RiskBearingProposed > 0 {
		attentionSignals++
	}

	switch {
	case criticalSignals > 0:
		t.HealthLevel = "critical"
		t.HealthSummary = formatHealthSummary("Needs attention", t)
	case attentionSignals > 0:
		t.HealthLevel = "attention"
		t.HealthSummary = formatHealthSummary("Needs attention", t)
	default:
		t.HealthLevel = "healthy"
		t.HealthSummary = formatHealthSummary("Good overall", t)
	}

	if !t.Coverage.Available {
		// Honesty signal: prepend a partial-data note. Spec §4.3:
		// data unavailability never reads as healthy without saying
		// so.
		t.HealthSummary = "Coverage data unavailable. " + t.HealthSummary
	}
}

func formatHealthSummary(prefix string, t *HubTeamHealth) string {
	parts := []string{prefix + "."}
	parts = append(parts,
		fmt.Sprintf("%d sealed intents, %d open work, %d proposed.",
			t.TotalIntents, t.OpenIntents, t.ProposedIntents))
	attentionBits := []string{}
	if t.ProposedOlderThan72h > 0 {
		attentionBits = append(attentionBits,
			fmt.Sprintf("%d proposed waiting >72h; run proposal doctor", t.ProposedOlderThan72h))
	} else if t.ProposedOlderThan24h > 0 {
		attentionBits = append(attentionBits,
			fmt.Sprintf("%d proposed waiting >24h", t.ProposedOlderThan24h))
	}
	if t.OpenOlderThan24h > 0 {
		attentionBits = append(attentionBits,
			fmt.Sprintf("%d open work older than 24h", t.OpenOlderThan24h))
	}
	if t.Risk.RiskBearingProposed > 0 {
		attentionBits = append(attentionBits,
			fmt.Sprintf("%d proposed with constraints/risks", t.Risk.RiskBearingProposed))
	}
	if t.Coverage.Available && t.Coverage.UncoveredCommits > 0 {
		attentionBits = append(attentionBits,
			fmt.Sprintf("%d uncovered commits", t.Coverage.UncoveredCommits))
	}
	if len(attentionBits) > 0 {
		parts = append(parts, "Attention needed: "+strings.Join(attentionBits, ", ")+".")
	}
	return strings.Join(parts, " ")
}

// -----------------------------------------------------------
// helpers
// -----------------------------------------------------------

func ageHours(rfc3339 string, now time.Time) int {
	t, ok := parseTime(rfc3339)
	if !ok {
		return 0
	}
	d := now.Sub(t)
	if d < 0 {
		return 0
	}
	return int(d.Hours())
}

func openIntentAgeHours(op HubOpenIntent, now time.Time) int {
	if op.LastModifiedAt != "" {
		return ageHours(op.LastModifiedAt, now)
	}
	if op.CreatedAt != "" {
		return ageHours(op.CreatedAt, now)
	}
	return 0
}

func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func hasAnyAntiPattern(in HubIntent) bool {
	return len(in.AntiPatterns) > 0
}

func focusFromIntent(in HubIntent, reason string, now time.Time) HubFocusIntent {
	hours := ageHours(in.SealedAt, now)
	highRisk := isEffectiveRiskBearing(in)
	return HubFocusIntent{
		ID:        in.ID,
		Title:     firstNonEmpty(in.Title, in.Goal, in.ID),
		Status:    in.Status,
		Reason:    reason,
		AgeHours:  hours,
		RiskCount: len(in.OpenRisks),
		FileCount: len(in.FilesTouched),
		HighRisk:  highRisk,
		ActorID:   in.ActorID,
		ActorName: in.ActorName,
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func hasMaterialDecision(in HubIntent) bool {
	for _, d := range in.Decisions {
		if strings.TrimSpace(d.Rationale) != "" {
			return true
		}
	}
	return false
}

func trimReason(s string) string {
	const max = 110
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
