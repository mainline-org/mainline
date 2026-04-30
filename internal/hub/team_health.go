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
//   - Coverage data computation. Coverage requires git-log access
//     (uncovered commits = main HEAD's last N commits with no
//     pinned intent), which lives in the engine layer. The spec is
//     explicit: "v1 can support partial; do not rewrite coverage
//     engine". TeamHealth.Coverage.Available stays false when the
//     hub layer has no coverage input — Hub renders the partial-
//     data wording instead of a fake "100% covered".
//
//   - Per-actor activity / lifecycle health. Optional in the spec
//     and explicitly forbidden from being a productivity panel;
//     deferred to a follow-up that can carefully word it.

const (
	// digestWindowDays is the rolling time window for the weekly
	// digest section. Spec §10 default is 7 days; we hardcode
	// because the digest is render-time; future per-call windows
	// (`mainline digest --since`) would compute this independently.
	digestWindowDays = 7

	// agingProposedWarningHours / agingProposedStaleHours / etc.
	// drive the review-queue and open-work aging buckets. Spec §7.3
	// values, hardcoded for v1.
	agingProposedWarningHours = 12
	agingProposedStaleHours   = 24
	agingProposedCriticalHours = 48
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
	t.populateDigest(m, now)
	// Coverage stays unavailable in this layer — engine wires it
	// later when real gaps data flows through. Default zero-value
	// has Available=false.
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
		isRisky := len(in.Risks) > 0 || hasAnyAntiPattern(in)
		if !isRisky {
			continue
		}
		// proposed-with-risks subset is the most actionable surface.
		if in.Status == "proposed" {
			t.Risk.RiskBearingProposed++
			t.Risk.RiskBearingProposedRows = append(t.Risk.RiskBearingProposedRows, focusFromIntent(in,
				"risk-bearing proposed intent waiting review", now))
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
		path  string
		count int
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

func (t *HubTeamHealth) populateDigest(m *HubModel, now time.Time) {
	cutoff := now.AddDate(0, 0, -digestWindowDays)
	t.Digest.WindowDays = digestWindowDays

	hotByPath := map[string]int{}

	for _, in := range m.Intents {
		ts, ok := parseTime(in.SealedAt)
		if !ok || ts.Before(cutoff) {
			continue
		}
		switch in.Status {
		case "merged":
			t.Digest.SealedThisWindow++
		case "proposed":
			t.Digest.ProposedThisWindow++
		case "abandoned":
			t.Digest.AbandonedThisWindow++
			if len(t.Digest.AbandonedApproaches) < digestAbandonedN {
				t.Digest.AbandonedApproaches = append(t.Digest.AbandonedApproaches,
					focusFromIntent(in, "abandoned this window", now))
			}
		case "superseded":
			t.Digest.SupersededThisWindow++
		}
		if len(in.Risks) > 0 || hasAnyAntiPattern(in) {
			t.Digest.RiskBearingThisWindow++
			if len(t.Digest.RisksToWatch) < digestRisksN {
				reason := "risk-bearing"
				if len(in.Risks) > 0 {
					reason = trimReason(in.Risks[0])
				}
				t.Digest.RisksToWatch = append(t.Digest.RisksToWatch, focusFromIntent(in, reason, now))
			}
		}
		// Important-decisions: an intent with at least one decision
		// AND a non-empty rationale is the heuristic for "this seal
		// recorded a real choice", not just a status flip.
		if hasMaterialDecision(in) && len(t.Digest.ImportantDecisions) < digestImportantDecisionsN {
			d := in.Decisions[0]
			reason := d.Point + ": " + d.Chose
			t.Digest.ImportantDecisions = append(t.Digest.ImportantDecisions,
				focusFromIntent(in, trimReason(reason), now))
		}
		for _, f := range in.FilesTouched {
			hotByPath[f]++
		}
	}

	// Hot files in this window — the digest's "files heating up"
	// rollup. Different from Dashboard.HotFiles which spans all
	// time; this one shows churn THIS week.
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
		t.Digest.HotFilesThisWindow = append(t.Digest.HotFilesThisWindow, HubHotFile{
			Path:        w.path,
			IntentCount: w.count,
		})
	}
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
	if t.ProposedOlderThan48h > 0 {
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
	if t.ProposedOlderThan24h > 0 {
		attentionBits = append(attentionBits,
			fmt.Sprintf("%d proposed waiting >24h", t.ProposedOlderThan24h))
	}
	if t.OpenOlderThan24h > 0 {
		attentionBits = append(attentionBits,
			fmt.Sprintf("%d open work older than 24h", t.OpenOlderThan24h))
	}
	if t.Risk.RiskBearingProposed > 0 {
		attentionBits = append(attentionBits,
			fmt.Sprintf("%d risk-bearing proposed intents", t.Risk.RiskBearingProposed))
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
	highRisk := len(in.Risks) > 0
	return HubFocusIntent{
		ID:        in.ID,
		Title:     firstNonEmpty(in.Title, in.Goal, in.ID),
		Status:    in.Status,
		Reason:    reason,
		AgeHours:  hours,
		RiskCount: len(in.Risks),
		FileCount: len(in.FilesTouched),
		HighRisk:  highRisk,
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
