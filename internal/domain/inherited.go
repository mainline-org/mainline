package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Inherited-constraint propagation. High-severity AntiPatterns from
// past sealed intents become "inherited constraints" for any future
// change that touches the same files. Surfaced to the agent via
// `mainline context`, to the reviewer via Hub + PR description, and
// to the linter as a warning when not explicitly acknowledged.
//
// Design principles (v2):
//   - Only HIGH severity propagates — the checklist must be short.
//   - Only FILE overlap triggers inheritance — subsystem matching was
//     too coarse and generated noise that trained users to ignore it.
//   - Acknowledgement is EXPLICIT: the seal carries acknowledged_constraints[]
//     keyed by stable constraint_id ("int_xxx#N"), not guessed via
//     token overlap.
//   - Legacy fallback: old seals without acknowledged_constraints still
//     get the v1 token-overlap heuristic in lint for backward compat.
//
// Violation detection remains out of scope — awareness + explicit
// acknowledgement is the load-bearing contract.

// BuildInheritedConstraints aggregates high-severity AntiPatterns
// from prior sealed intents whose FilesTouched overlap with the
// supplied files of the change in progress.
//
// v2 design: only HIGH severity anti_patterns propagate (the
// checklist must be short and hard), and matching is FILE-ONLY
// (subsystem matching was too coarse and generated noise). This
// means the function no longer uses the `subsystems` parameter —
// it is kept in the signature for API stability but ignored.
//
// excludeID is the intent currently being sealed/edited (its own
// anti_patterns are not "inherited" — they're the intent's own
// constraints). When excludeID corresponds to a sealed intent in
// the view, this function ALSO filters out source intents that
// were sealed AFTER excludeID — a future constraint cannot have
// been acknowledged by the current intent because it did not yet
// exist. For in-flight retrieval (excludeID = "" or matches an
// active draft), no temporal filter is applied; the agent is the
// future relative to every sealed intent.
//
// Output ordering: by SourceIntent ID for determinism. Never truncated.
//
// Pure: no I/O. The view is the only state.
func BuildInheritedConstraints(view *MainlineView, files, subsystems []string, excludeID string) []InheritedConstraint {
	if view == nil {
		return nil
	}
	fileSet := make(map[string]bool, len(files))
	for _, f := range files {
		if f != "" {
			fileSet[f] = true
		}
	}
	if len(fileSet) == 0 {
		return nil
	}

	// If excludeID matches a sealed intent in the view, derive its
	// SealedAt to use as the temporal cutoff. Source intents sealed
	// strictly after this time are dropped — a future constraint
	// could not have been acknowledged by the current intent.
	var cutoff time.Time
	for i := range view.Intents {
		iv := &view.Intents[i]
		if iv.IntentID == excludeID && iv.SealedAt != "" {
			if t, err := time.Parse(time.RFC3339, iv.SealedAt); err == nil {
				cutoff = t
			}
			break
		}
	}

	type bucket struct {
		ic     InheritedConstraint
		reasons map[string]bool
	}
	merged := map[string]*bucket{}
	for i := range view.Intents {
		iv := &view.Intents[i]
		if iv.IntentID == excludeID {
			continue
		}
		if iv.Status == StatusAbandoned || iv.Status == StatusReverted {
			continue
		}
		if iv.Summary == nil || len(iv.Summary.AntiPatterns) == 0 {
			continue
		}
		if !cutoff.IsZero() && iv.SealedAt != "" {
			if t, err := time.Parse(time.RFC3339, iv.SealedAt); err == nil && t.After(cutoff) {
				continue
			}
		}
		// File-only matching: check if this intent touched any of our files
		matchedFiles := matchedFileReasons(iv, fileSet)
		if len(matchedFiles) == 0 {
			continue
		}
		// Only inherit HIGH severity anti_patterns
		for apIdx, ap := range iv.Summary.AntiPatterns {
			if strings.TrimSpace(ap.What) == "" {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(ap.Severity), "high") {
				continue
			}
			constraintID := fmt.Sprintf("%s#%d", iv.IntentID, apIdx)
			key := constraintID
			b, ok := merged[key]
			if !ok {
				b = &bucket{
					ic: InheritedConstraint{
						ConstraintID: constraintID,
						SourceIntent: iv.IntentID,
						What:         ap.What,
						Why:          ap.Why,
						Severity:     ap.Severity,
					},
					reasons: map[string]bool{},
				}
				merged[key] = b
			}
			for _, r := range matchedFiles {
				b.reasons[r] = true
			}
		}
	}

	out := make([]InheritedConstraint, 0, len(merged))
	for _, b := range merged {
		reasons := make([]string, 0, len(b.reasons))
		for r := range b.reasons {
			reasons = append(reasons, r)
		}
		sort.Strings(reasons)
		b.ic.MatchedBy = reasons
		out = append(out, b.ic)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ConstraintID < out[j].ConstraintID
	})
	return out
}

// BuildInheritedHeatmap returns the per-file hotspot roll-up over
// the catalog. For each file in the FileIndex, it collects the
// inherited anti_patterns whose source intent's FilesTouched
// contains that file (subsystem-only matches don't make the heatmap
// because they don't pin to a single path) and counts how many
// recent (within `recentWindow`) intents touched the file without
// acknowledging at least one applicable high-severity constraint.
//
// recentWindow is the cutoff time: intents sealed at or after it are
// "recent". Pass time.Now().AddDate(0, 0, -7) for the dashboard's
// 7-day window.
//
// Output is sorted by HighSeverityCount desc, UnacknowledgedRecent
// desc, then file path asc. Caller can truncate for display.
//
// Pure: no I/O.
func BuildInheritedHeatmap(view *MainlineView, recentWindow time.Time) []InheritedConstraintHotspot {
	if view == nil {
		return nil
	}
	// Per-file constraint roll-up. We collect distinct (source, what)
	// pairs; later runs that re-touch the same file under the same
	// constraint don't multiply the count.
	type cKey struct {
		source string
		what   string
	}
	perFile := map[string]map[cKey]InheritedConstraint{}
	// Recent touches by file: which intents sealed within the window
	// touched each file. We need the IntentView pointer so we can
	// later check whether the intent acknowledged its applicable
	// inherited constraints.
	recentByFile := map[string][]*IntentView{}

	for i := range view.Intents {
		iv := &view.Intents[i]
		if iv.Status == StatusAbandoned || iv.Status == StatusReverted {
			continue
		}
		if iv.Fingerprint == nil {
			continue
		}
		isRecent := false
		if iv.SealedAt != "" {
			if t, err := time.Parse(time.RFC3339, iv.SealedAt); err == nil && !t.Before(recentWindow) {
				isRecent = true
			}
		}
		// Source role: this intent contributes high-severity
		// anti_patterns to every file it touched.
		if iv.Summary != nil {
			for apIdx, ap := range iv.Summary.AntiPatterns {
				if strings.TrimSpace(ap.What) == "" {
					continue
				}
				if !strings.EqualFold(strings.TrimSpace(ap.Severity), "high") {
					continue
				}
				constraintID := fmt.Sprintf("%s#%d", iv.IntentID, apIdx)
				for _, f := range iv.Fingerprint.FilesTouched {
					m, ok := perFile[f]
					if !ok {
						m = map[cKey]InheritedConstraint{}
						perFile[f] = m
					}
					m[cKey{iv.IntentID, ap.What}] = InheritedConstraint{
						ConstraintID: constraintID,
						SourceIntent: iv.IntentID,
						What:         ap.What,
						Why:          ap.Why,
						Severity:     ap.Severity,
						MatchedBy:    []string{"file:" + f},
					}
				}
			}
		}
		// Recent-touch role: every recent intent contributes to
		// per-file recent-touch counts.
		if isRecent {
			for _, f := range iv.Fingerprint.FilesTouched {
				recentByFile[f] = append(recentByFile[f], iv)
			}
		}
	}

	out := make([]InheritedConstraintHotspot, 0, len(perFile))
	for path, constraints := range perFile {
		hs := InheritedConstraintHotspot{FilePath: path, ConstraintCount: len(constraints)}
		highList := make([]InheritedConstraint, 0, len(constraints))
		for _, c := range constraints {
			if strings.EqualFold(strings.TrimSpace(c.Severity), "high") {
				hs.HighSeverityCount++
				highList = append(highList, c)
			}
			hs.Constraints = append(hs.Constraints, c)
		}
		// Stable order on Constraints: severity then source intent.
		sort.SliceStable(hs.Constraints, func(i, j int) bool {
			ri := severityRank(hs.Constraints[i].Severity)
			rj := severityRank(hs.Constraints[j].Severity)
			if ri != rj {
				return ri < rj
			}
			return hs.Constraints[i].SourceIntent < hs.Constraints[j].SourceIntent
		})

		// UnacknowledgedRecentTouches: of the recent intents
		// touching this file, how many failed to acknowledge ANY
		// applicable high-severity inherited constraint?
		recent := recentByFile[path]
		hs.RecentTouches = len(recent)
		for _, iv := range recent {
			anyUnack := false
			for _, hc := range highList {
				if hc.SourceIntent == iv.IntentID {
					continue
				}
				if iv.Summary == nil {
					anyUnack = true
					break
				}
				// Prefer explicit acknowledgement; fall back to
				// token overlap for legacy intents without the field.
				if hasExplicitAck(iv.Summary.AcknowledgedConstraints, hc.ConstraintID) {
					continue
				}
				if len(iv.Summary.AcknowledgedConstraints) == 0 && AcknowledgementOf(hc, iv.Summary) != AckNone {
					continue
				}
				anyUnack = true
				break
			}
			if anyUnack {
				hs.UnacknowledgedRecentTouches++
			}
		}
		out = append(out, hs)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].HighSeverityCount != out[j].HighSeverityCount {
			return out[i].HighSeverityCount > out[j].HighSeverityCount
		}
		if out[i].UnacknowledgedRecentTouches != out[j].UnacknowledgedRecentTouches {
			return out[i].UnacknowledgedRecentTouches > out[j].UnacknowledgedRecentTouches
		}
		return out[i].FilePath < out[j].FilePath
	})
	return out
}

// matchedFileReasons returns the list of "file:<path>" strings
// describing why the source intent's anti_patterns propagate to the
// current context. v2: file-only matching (subsystem removed).
func matchedFileReasons(iv *IntentView, files map[string]bool) []string {
	if iv.Fingerprint == nil {
		return nil
	}
	out := make([]string, 0, 4)
	for _, f := range iv.Fingerprint.FilesTouched {
		if files[f] {
			out = append(out, "file:"+f)
		}
	}
	return out
}

// matchedReasons is the legacy version that includes subsystem matching.
// Kept for BuildInheritedHeatmap which still uses intent-level matching
// for its per-file roll-up (subsystem matches don't appear in heatmap
// anyway because they can't pin to a single path).
func matchedReasons(iv *IntentView, files, subs map[string]bool) []string {
	if iv.Fingerprint == nil {
		return nil
	}
	out := make([]string, 0, 4)
	for _, f := range iv.Fingerprint.FilesTouched {
		if files[f] {
			out = append(out, "file:"+f)
		}
	}
	for _, s := range iv.Fingerprint.Subsystems {
		if subs[s] {
			out = append(out, "subsystem:"+s)
		}
	}
	return out
}

// hasExplicitAck checks whether the acknowledged_constraints list
// contains an entry for the given constraint ID. Exact match only.
func hasExplicitAck(acks []AcknowledgedConstraint, constraintID string) bool {
	for _, a := range acks {
		if a.ConstraintID == constraintID {
			return true
		}
	}
	return false
}

// severityRank maps anti_pattern severity to a sort key. Lower rank
// sorts first ("high" before "medium" before "low"). Empty / unknown
// severity sorts last so reviewers see the explicit signals first.
func severityRank(sev string) int {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "high":
		return 0
	case "medium":
		return 1
	case "low":
		return 2
	default:
		return 3
	}
}

// AcknowledgementForm reports which field acknowledged an inherited
// constraint, or empty when none did. The order matters — we prefer
// the strongest form (decision > rejected_alternative > own
// anti_pattern > risk) so the reviewer-facing badge picks the most
// load-bearing acknowledgement.
type AcknowledgementForm string

const (
	AckNone           AcknowledgementForm = ""
	AckDecision       AcknowledgementForm = "decision"
	AckRejected       AcknowledgementForm = "rejected_alternative"
	AckAntiPattern    AcknowledgementForm = "anti_pattern"
	AckRisk           AcknowledgementForm = "risk"
)

// AcknowledgementOf checks whether the supplied summary's
// decisions / rejected / anti_patterns / risks reference the
// inherited constraint. Returns the strongest matching form, or
// AckNone.
//
// Match strategy: extract the set of significant content tokens
// from the constraint's `What` (lowercased, stripped of stopwords
// and punctuation, deduped). For a haystack to count as an
// acknowledgement, it must contain *enough* of those tokens — see
// requiredTokenOverlap for the threshold. The substring-of-needle
// approach we tried first was too strict: word order is rarely
// preserved across "constraint says X" → "decision references X",
// e.g. "Removing legacy session middleware" vs. "kept the legacy
// session middleware in place". Set-overlap is the cheapest match
// that catches paraphrases without firing on coincidental noun
// reuse.
func AcknowledgementOf(ic InheritedConstraint, summary *IntentSummary) AcknowledgementForm {
	if summary == nil {
		return AckNone
	}
	tokens := constraintTokens(ic.What)
	if len(tokens) == 0 {
		return AckNone
	}
	required := requiredTokenOverlap(len(tokens))
	// Decision is the strongest form: where the agent records
	// "what we chose and why". A constraint mention here is the
	// most load-bearing acknowledgement.
	for _, d := range summary.Decisions {
		hay := strings.ToLower(d.Point + " " + d.Chose + " " + d.Rationale)
		for _, rj := range d.Rejected {
			hay += " " + strings.ToLower(rj)
		}
		if hasTokenOverlap(hay, tokens, required) {
			return AckDecision
		}
	}
	for _, r := range summary.Rejected {
		hay := strings.ToLower(r.Alternative + " " + r.Reason)
		if hasTokenOverlap(hay, tokens, required) {
			return AckRejected
		}
	}
	for _, ap := range summary.AntiPatterns {
		hay := strings.ToLower(ap.What + " " + ap.Why)
		if hasTokenOverlap(hay, tokens, required) {
			return AckAntiPattern
		}
	}
	for _, r := range summary.Risks {
		if hasTokenOverlap(strings.ToLower(r), tokens, required) {
			return AckRisk
		}
	}
	return AckNone
}

// constraintTokens returns the deduped set of significant content
// tokens from an anti_pattern's `What`. Output is lowercased,
// stripped of stopwords / punctuation, and lightly stemmed so
// "removing" / "remove" / "removed" all collapse to the same
// match key.
func constraintTokens(what string) []string {
	s := strings.ToLower(strings.TrimSpace(what))
	if s == "" {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, 8)
	for _, raw := range strings.Fields(s) {
		t := stripTokenPunct(raw)
		if t == "" || isStopword(t) {
			continue
		}
		stem := lightStem(t)
		if stem == "" || seen[stem] {
			continue
		}
		seen[stem] = true
		out = append(out, stem)
	}
	return out
}

func stripTokenPunct(t string) string {
	return strings.TrimFunc(t, func(r rune) bool {
		switch r {
		case '.', ',', ';', ':', '!', '?', '"', '\'', '(', ')', '/', '\\':
			return true
		}
		return false
	})
}

// lightStem applies tiny English suffix stripping: "removing" →
// "remov", "removed" → "remov", "removes" → "remove" (no change),
// "session" stays. Sufficient to merge the morphological variants
// agents use across decisions / constraints without pulling a real
// stemmer dependency. Skipped when the token is already short
// (under 5 chars) so we don't strip "lints" → "lin" or "ed" → "".
func lightStem(t string) string {
	if len(t) < 5 {
		return t
	}
	// Order matters: check "ing" before "es"/"s" so "removing" →
	// "remov" not "removin".
	switch {
	case strings.HasSuffix(t, "ing"):
		return t[:len(t)-3]
	case strings.HasSuffix(t, "ed"):
		return t[:len(t)-2]
	case strings.HasSuffix(t, "es"):
		return t[:len(t)-1]
	case strings.HasSuffix(t, "s") && !strings.HasSuffix(t, "ss"):
		return t[:len(t)-1]
	}
	return t
}

// requiredTokenOverlap returns how many of an anti_pattern's
// content tokens must appear in a haystack to count as
// acknowledgement. Tuned for the v1 goal "awareness, not
// conviction": short constraints (1-2 tokens) require all tokens
// because anything less is a trivial coincidence; longer
// constraints accept a 2- or 3-token overlap because the agent's
// own paraphrase will rarely include every noun the constraint
// named.
func requiredTokenOverlap(n int) int {
	switch {
	case n <= 0:
		return 0
	case n <= 2:
		return n
	case n <= 4:
		return 2
	case n <= 7:
		return 3
	default:
		return 4
	}
}

// hasTokenOverlap returns true when the haystack contains at least
// `required` distinct stemmed tokens from the needles set. Substring
// containment is performed against the stemmed haystack so a needle
// "remov" matches a hay containing "remove" or "removing".
func hasTokenOverlap(hay string, needles []string, required int) bool {
	if required <= 0 {
		return false
	}
	stemmedHay := stemHaystack(hay)
	hits := 0
	for _, t := range needles {
		if strings.Contains(stemmedHay, t) {
			hits++
			if hits >= required {
				return true
			}
		}
	}
	return false
}

// stemHaystack walks the haystack word-by-word and rebuilds it with
// every word replaced by its lightStem form. Spaces preserved so
// substring matches still work for multi-token needles, even though
// the v1 matcher checks single tokens only.
func stemHaystack(hay string) string {
	if hay == "" {
		return ""
	}
	parts := strings.Fields(hay)
	for i, p := range parts {
		parts[i] = lightStem(stripTokenPunct(p))
	}
	return strings.Join(parts, " ")
}

// isStopword is a tiny English/Chinese-mixed stopword set. We keep
// it minimal — over-aggressive filtering would discard signal words
// for short constraints like "Skip token rotation".
func isStopword(t string) bool {
	switch t {
	case "the", "a", "an", "of", "to", "in", "on", "at", "for",
		"and", "or", "but", "is", "are", "be", "this", "that",
		"these", "those", "with", "without", "do", "not", "no",
		"don't", "must", "should", "can", "will":
		return true
	}
	return false
}
