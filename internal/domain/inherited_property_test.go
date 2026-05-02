//go:build !quick

package domain

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// PBTs for the inherited-constraints subsystem (BuildInheritedConstraints,
// hasExplicitAck, requiredTokenOverlap, AcknowledgementOf). These
// supplement the example-based cases in inherited_test.go: examples
// pin the obvious shapes; PBTs explore the input space rapid considers
// most adversarial.
//
// Properties intentionally NOT tested here:
//   - "token overlap means violation" — by design, token overlap is
//     for *awareness*, not conviction (see int_c49f96b8 high-severity
//     anti_pattern). v2 prefers explicit acknowledged_constraints[].
//   - any property that would require subsystem-only matching to
//     propagate — v2 removed that path on purpose (see int_b9d4625b).

// -----------------------------------------------------------
// BuildInheritedConstraints
// -----------------------------------------------------------

// Determinism: same (view, files, subsystems, excludeID) must yield
// the same output across repeated calls. A future hidden-state
// regression (caching, time read, env var) flips this loud.
func TestPropertyBuildInheritedConstraints_Deterministic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		view := drawInheritedView(rt)
		files, subs := drawTouchedFilesAndSubs(rt)
		excludeID := drawExcludeID(rt, view)

		first := BuildInheritedConstraints(view, files, subs, excludeID)
		for i := 0; i < 3; i++ {
			again := BuildInheritedConstraints(view, files, subs, excludeID)
			if !equalConstraints(first, again) {
				rt.Fatalf("non-deterministic on iter %d:\n  first=%+v\n  again=%+v", i, first, again)
			}
		}
	})
}

// HIGH-only: every output entry has severity equal-fold "high". v2
// dropped medium/low propagation to keep the checklist short.
func TestPropertyBuildInheritedConstraints_HighOnly(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		view := drawInheritedView(rt)
		files, subs := drawTouchedFilesAndSubs(rt)
		excludeID := drawExcludeID(rt, view)

		out := BuildInheritedConstraints(view, files, subs, excludeID)
		for _, c := range out {
			if !strings.EqualFold(strings.TrimSpace(c.Severity), "high") {
				rt.Fatalf("non-high severity in output: %q (constraint=%+v)", c.Severity, c)
			}
		}
	})
}

// Subsystems argument is ignored: same view+files+excludeID must
// produce the same output regardless of subsystems. v2 made matching
// file-only on purpose; this property is a regression guard against
// any future change that re-introduces a subsystem code path.
func TestPropertyBuildInheritedConstraints_SubsystemArgIgnored(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		view := drawInheritedView(rt)
		files, _ := drawTouchedFilesAndSubs(rt)
		excludeID := drawExcludeID(rt, view)

		a := BuildInheritedConstraints(view, files, []string{"alpha", "beta"}, excludeID)
		b := BuildInheritedConstraints(view, files, []string{"gamma"}, excludeID)
		c := BuildInheritedConstraints(view, files, nil, excludeID)
		if !equalConstraints(a, b) || !equalConstraints(a, c) {
			rt.Fatalf("subsystems must not affect output:\n  a=%+v\n  b=%+v\n  c=%+v", a, b, c)
		}
	})
}

// File-overlap requirement: a file that no source intent touches
// produces an empty output, regardless of view contents.
func TestPropertyBuildInheritedConstraints_RequiresFileOverlap(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		view := drawInheritedView(rt)
		// Use a file name that drawSourceIntent's pool never produces.
		out := BuildInheritedConstraints(view, []string{"zzz_disjoint_unique_path.go"}, nil, "")
		if len(out) != 0 {
			rt.Fatalf("disjoint file must yield empty output, got %d: %+v", len(out), out)
		}
	})
}

// No truncation: when excludeID is empty (so the temporal filter is
// off), the output count equals the number of qualifying high APs
// in the view. Catches a regression that would silently drop entries.
func TestPropertyBuildInheritedConstraints_NeverTruncated(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		view := drawInheritedView(rt)
		files, subs := drawTouchedFilesAndSubs(rt)
		// Force excludeID empty to disable the temporal cutoff for
		// this property — temporal filter is exercised separately.
		out := BuildInheritedConstraints(view, files, subs, "")

		fileSet := map[string]bool{}
		for _, f := range files {
			if f != "" {
				fileSet[f] = true
			}
		}
		expected := 0
		if len(fileSet) > 0 {
			for i := range view.Intents {
				iv := &view.Intents[i]
				if iv.Status == StatusAbandoned || iv.Status == StatusReverted {
					continue
				}
				if iv.Summary == nil || iv.Fingerprint == nil {
					continue
				}
				overlap := false
				for _, f := range iv.Fingerprint.FilesTouched {
					if fileSet[f] {
						overlap = true
						break
					}
				}
				if !overlap {
					continue
				}
				for _, ap := range iv.Summary.AntiPatterns {
					if strings.TrimSpace(ap.What) == "" {
						continue
					}
					if !strings.EqualFold(strings.TrimSpace(ap.Severity), "high") {
						continue
					}
					expected++
				}
			}
		}
		if len(out) != expected {
			rt.Fatalf("truncation mismatch: expected %d, got %d\n  files=%v\n  out=%+v",
				expected, len(out), files, out)
		}
	})
}

// Self-exclusion: when excludeID matches an intent in the view, that
// intent's anti_patterns never appear in the output (an intent does
// not inherit from itself).
func TestPropertyBuildInheritedConstraints_SelfExcluded(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		view := drawInheritedView(rt)
		if len(view.Intents) == 0 {
			return
		}
		idx := rapid.IntRange(0, len(view.Intents)-1).Draw(rt, "exIdx")
		excludeID := view.Intents[idx].IntentID
		files, subs := drawTouchedFilesAndSubs(rt)

		out := BuildInheritedConstraints(view, files, subs, excludeID)
		for _, c := range out {
			if c.SourceIntent == excludeID {
				rt.Fatalf("excluded source %q appeared in output: %+v", excludeID, c)
			}
		}
	})
}

// Abandoned / reverted source intents must not propagate constraints.
func TestPropertyBuildInheritedConstraints_AbandonedAndRevertedFiltered(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		view := drawInheritedView(rt)
		// Force every intent to abandoned or reverted.
		for i := range view.Intents {
			if rapid.Bool().Draw(rt, fmt.Sprintf("aban-%d", i)) {
				view.Intents[i].Status = StatusAbandoned
			} else {
				view.Intents[i].Status = StatusReverted
			}
		}
		files, subs := drawTouchedFilesAndSubs(rt)
		out := BuildInheritedConstraints(view, files, subs, "")
		if len(out) != 0 {
			rt.Fatalf("all-abandoned/reverted view must produce empty output, got: %+v", out)
		}
	})
}

// Output structure: sorted ascending by ConstraintID, ConstraintIDs
// have the "<source>#<idx>" shape, and every MatchedBy entry is
// "file:<path>" prefixed and sorted (no subsystem reasons in v2).
func TestPropertyBuildInheritedConstraints_OutputStructure(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		view := drawInheritedView(rt)
		files, subs := drawTouchedFilesAndSubs(rt)
		excludeID := drawExcludeID(rt, view)

		out := BuildInheritedConstraints(view, files, subs, excludeID)

		// Sorted ascending by ConstraintID, no duplicates.
		for i := 1; i < len(out); i++ {
			if out[i-1].ConstraintID >= out[i].ConstraintID {
				rt.Fatalf("ConstraintIDs not strictly ascending: %q >= %q",
					out[i-1].ConstraintID, out[i].ConstraintID)
			}
		}
		// "<source>#<idx>" format and idx within source's AP range.
		idxByID := map[string]int{}
		for i, iv := range view.Intents {
			idxByID[iv.IntentID] = i
		}
		for _, c := range out {
			parts := strings.SplitN(c.ConstraintID, "#", 2)
			if len(parts) != 2 || parts[0] != c.SourceIntent {
				rt.Fatalf("constraint id format violated: id=%q source=%q",
					c.ConstraintID, c.SourceIntent)
			}
			// MatchedBy entries: file:-prefixed and sorted.
			if len(c.MatchedBy) == 0 {
				rt.Fatalf("matched_by must be non-empty: %+v", c)
			}
			for j, m := range c.MatchedBy {
				if !strings.HasPrefix(m, "file:") {
					rt.Fatalf("matched_by entry %q lacks file: prefix (subsystem leak?)", m)
				}
				if j > 0 && c.MatchedBy[j-1] >= m {
					rt.Fatalf("matched_by not sorted: %v", c.MatchedBy)
				}
			}
		}
	})
}

// -----------------------------------------------------------
// hasExplicitAck
// -----------------------------------------------------------

// Exact-match contract: every constraint_id present in the list must
// hit; ids not present must miss; nil acks always returns false.
func TestPropertyHasExplicitAck(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 6).Draw(rt, "n")
		acks := make([]AcknowledgedConstraint, 0, n)
		ids := make([]string, 0, n)
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("int_x%d#%d", i, i)
			ids = append(ids, id)
			acks = append(acks, AcknowledgedConstraint{
				ConstraintID: id,
				Disposition:  "preserved",
			})
		}
		// Every present id must hit.
		for _, id := range ids {
			if !hasExplicitAck(acks, id) {
				rt.Fatalf("present id %q should match", id)
			}
		}
		// nil acks always misses.
		if hasExplicitAck(nil, "int_x0#0") {
			rt.Fatal("nil acks must not match")
		}
		// Random absent id must miss (unless drawn happens to collide).
		probe := rapid.SampledFrom([]string{
			"int_zzz#9", "missing#0", "", "garbage", "int_x0#99",
		}).Draw(rt, "probe")
		expectMatch := false
		for _, id := range ids {
			if id == probe {
				expectMatch = true
				break
			}
		}
		if hasExplicitAck(acks, probe) != expectMatch {
			rt.Fatalf("probe %q: expectMatch=%v got=%v ids=%v",
				probe, expectMatch, !expectMatch, ids)
		}
	})
}

// -----------------------------------------------------------
// requiredTokenOverlap
// -----------------------------------------------------------

// Bounded by [0, n] and monotonic non-decreasing in n. Short-
// constraint rule: 1 or 2 tokens require all of them. These together
// pin the calibration: anything tighter than n=all-tokens loses the
// short-constraint guard; anything that decreases as n grows breaks
// the calibration ladder.
func TestPropertyRequiredTokenOverlap_BoundedAndMonotonic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 30).Draw(rt, "n")
		m := rapid.IntRange(0, 30).Draw(rt, "m")
		rn := requiredTokenOverlap(n)
		rm := requiredTokenOverlap(m)

		if rn < 0 {
			rt.Fatalf("required(%d) negative: %d", n, rn)
		}
		if n > 0 && rn > n {
			rt.Fatalf("required(%d)=%d exceeds n", n, rn)
		}
		if n == 0 && rn != 0 {
			rt.Fatalf("required(0) should be 0, got %d", rn)
		}
		if n >= 1 && n <= 2 && rn != n {
			rt.Fatalf("short-constraint rule: required(%d) should be %d, got %d", n, n, rn)
		}
		if n <= m && rn > rm {
			rt.Fatalf("non-monotonic: required(%d)=%d > required(%d)=%d", n, rn, m, rm)
		}
	})
}

// -----------------------------------------------------------
// AcknowledgementOf
// -----------------------------------------------------------

// Empty cases: nil summary or empty/whitespace constraint What must
// always return AckNone, never panic.
func TestPropertyAcknowledgementOf_EmptyCasesReturnNone(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		what := rapid.SampledFrom([]string{"", " ", "    \t\n  "}).Draw(rt, "what")
		ic := InheritedConstraint{What: what, Severity: "high"}
		if got := AcknowledgementOf(ic, nil); got != AckNone {
			rt.Fatalf("nil summary expected AckNone, got %q", got)
		}
		summary := &IntentSummary{
			Decisions: []Decision{{Point: "p", Chose: "c", Rationale: "r"}},
			Risks:     []string{"a risk", "another"},
		}
		if got := AcknowledgementOf(ic, summary); got != AckNone {
			rt.Fatalf("empty/blank What expected AckNone, got %q", got)
		}
	})
}

// Determinism: AcknowledgementOf must return the same form across
// repeated calls on the same input. Token-overlap matching is
// stateless; if a future change introduces a Map iteration order
// dependency, this property surfaces it.
func TestPropertyAcknowledgementOf_Deterministic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ic := InheritedConstraint{
			What: rapid.SampledFrom([]string{
				"Skip token rotation",
				"Removing legacy session middleware on /oauth path",
				"Avoid the legacy session middleware",
				"Don't drop session cookie on /oauth",
			}).Draw(rt, "what"),
			Severity: "high",
		}
		summary := &IntentSummary{
			Decisions: []Decision{{
				Point: "session",
				Chose: rapid.SampledFrom([]string{
					"kept the legacy session middleware in place",
					"completely unrelated rationale",
				}).Draw(rt, "chose"),
			}},
			Risks: []string{rapid.SampledFrom([]string{
				"legacy session middleware concerns under load",
				"no relevance",
			}).Draw(rt, "risk")},
		}
		first := AcknowledgementOf(ic, summary)
		for i := 0; i < 3; i++ {
			if got := AcknowledgementOf(ic, summary); got != first {
				rt.Fatalf("non-deterministic: first=%q now=%q", first, got)
			}
		}
	})
}

// Result canonicality: AcknowledgementOf returns one of the five
// declared AcknowledgementForm constants — never a surprise string.
// Catches a refactor that returns a freshly typed string by accident.
func TestPropertyAcknowledgementOf_OnlyCanonicalForms(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ic := InheritedConstraint{
			What:     rapid.SampledFrom([]string{"", "Skip rotation", "Removing legacy session middleware"}).Draw(rt, "what"),
			Severity: "high",
		}
		summary := drawSummaryWithMaybeAck(rt, "summary")
		got := AcknowledgementOf(ic, summary)
		switch got {
		case AckNone, AckDecision, AckRejected, AckAntiPattern, AckRisk:
		default:
			rt.Fatalf("non-canonical AcknowledgementForm: %q", got)
		}
	})
}

// Priority: when the same overlap could be matched by both Decision
// and Risk fields, AcknowledgementOf must pick AckDecision (the
// strongest, load-bearing form). This is the contract the reviewer-
// facing badge relies on; weakening it would silently demote the
// most reliable acknowledgement signal.
func TestPropertyAcknowledgementOf_DecisionDominatesRisk(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Pick a constraint with enough tokens that the matcher will
		// fire on either field independently.
		what := rapid.SampledFrom([]string{
			"Removing legacy session middleware on /oauth path",
			"Avoid removing the session token rotation logic",
		}).Draw(rt, "what")
		ic := InheritedConstraint{What: what, Severity: "high"}

		// Both fields contain the same load-bearing phrase — Decision
		// must win.
		summary := &IntentSummary{
			Decisions: []Decision{{
				Point: "session",
				Chose: what,
			}},
			Risks: []string{what},
		}
		if got := AcknowledgementOf(ic, summary); got != AckDecision {
			rt.Fatalf("decision must dominate risk, got %q", got)
		}

		// Removing the Decision field should drop priority to AckRisk.
		summary.Decisions = nil
		if got := AcknowledgementOf(ic, summary); got != AckRisk {
			rt.Fatalf("with no decisions, risk should win, got %q", got)
		}
	})
}

// -----------------------------------------------------------
// Generators (kept narrow — these PBTs cover only the inherited-
// constraints subsystem)
// -----------------------------------------------------------

func drawInheritedView(rt *rapid.T) *MainlineView {
	n := rapid.IntRange(0, 5).Draw(rt, "view.n")
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	intents := make([]IntentView, 0, n)
	for i := 0; i < n; i++ {
		intents = append(intents, drawSourceIntent(rt, fmt.Sprintf("iv-%d", i), base))
	}
	return &MainlineView{Intents: intents}
}

func drawSourceIntent(rt *rapid.T, label string, baseTime time.Time) IntentView {
	statuses := []IntentStatus{
		StatusMerged, StatusProposed, StatusSealedLocal,
		StatusAbandoned, StatusReverted, StatusSuperseded,
	}
	st := rapid.SampledFrom(statuses).Draw(rt, label+".status")

	// Files: at least one, drawn from a small pool to encourage
	// overlap with the test's file argument. Dedup so the source's
	// FilesTouched slice is well-formed.
	pool := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	nFiles := rapid.IntRange(1, 3).Draw(rt, label+".nFiles")
	seen := map[string]bool{}
	files := make([]string, 0, nFiles)
	for i := 0; i < nFiles; i++ {
		f := rapid.SampledFrom(pool).Draw(rt, fmt.Sprintf("%s.f-%d", label, i))
		if !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}

	// 0..3 anti-patterns with mixed severity / blank-What variants
	// so the high-only / blank filters get exercised.
	nAPs := rapid.IntRange(0, 3).Draw(rt, label+".nAPs")
	aps := make([]AntiPattern, 0, nAPs)
	for i := 0; i < nAPs; i++ {
		aps = append(aps, AntiPattern{
			What: rapid.SampledFrom([]string{
				"", " ",
				"Don't drop session cookie",
				"Skip token rotation",
				"Avoid the legacy session middleware",
			}).Draw(rt, fmt.Sprintf("%s.ap-%d.what", label, i)),
			Why: rapid.SampledFrom([]string{
				"", "load-bearing reason",
			}).Draw(rt, fmt.Sprintf("%s.ap-%d.why", label, i)),
			Severity: rapid.SampledFrom([]string{
				"low", "medium", "high", "Low", "HIGH", "", "weird",
			}).Draw(rt, fmt.Sprintf("%s.ap-%d.sev", label, i)),
		})
	}

	var sealedAt string
	if rapid.Bool().Draw(rt, label+".hasSealed") {
		offsetDays := rapid.IntRange(-100, 100).Draw(rt, label+".offset")
		sealedAt = baseTime.Add(time.Duration(offsetDays) * 24 * time.Hour).Format(time.RFC3339)
	}

	return IntentView{
		IntentID: "int_" + label,
		Status:   st,
		SealedAt: sealedAt,
		Summary: &IntentSummary{
			Title:        "t-" + label,
			What:         "w",
			Why:          "y",
			AntiPatterns: aps,
		},
		Fingerprint: &SemanticFingerprint{
			FilesTouched: files,
		},
	}
}

func drawTouchedFilesAndSubs(rt *rapid.T) ([]string, []string) {
	pool := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	n := rapid.IntRange(0, 3).Draw(rt, "tf.n")
	seen := map[string]bool{}
	files := make([]string, 0, n)
	for i := 0; i < n; i++ {
		f := rapid.SampledFrom(pool).Draw(rt, fmt.Sprintf("tf-%d", i))
		if !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	nSubs := rapid.IntRange(0, 2).Draw(rt, "tf.nSubs")
	subs := make([]string, 0, nSubs)
	for i := 0; i < nSubs; i++ {
		subs = append(subs, rapid.SampledFrom([]string{
			"alpha", "beta", "gamma",
		}).Draw(rt, fmt.Sprintf("sub-%d", i)))
	}
	return files, subs
}

func drawExcludeID(rt *rapid.T, view *MainlineView) string {
	if len(view.Intents) == 0 || rapid.Bool().Draw(rt, "noExclude") {
		return ""
	}
	idx := rapid.IntRange(0, len(view.Intents)-1).Draw(rt, "excludeIdx")
	return view.Intents[idx].IntentID
}

func drawSummaryWithMaybeAck(rt *rapid.T, label string) *IntentSummary {
	if !rapid.Bool().Draw(rt, label+".present") {
		return nil
	}
	out := &IntentSummary{Title: "t", What: "w", Why: "y"}
	if rapid.Bool().Draw(rt, label+".hasDec") {
		out.Decisions = []Decision{{
			Point: "p",
			Chose: rapid.SampledFrom([]string{
				"unrelated",
				"kept the legacy session middleware",
				"removed token rotation",
			}).Draw(rt, label+".dchose"),
		}}
	}
	if rapid.Bool().Draw(rt, label+".hasRej") {
		out.Rejected = []RejectedAlternative{{
			Alternative: rapid.SampledFrom([]string{
				"alt unrelated", "remove the session middleware entirely",
			}).Draw(rt, label+".ralt"),
			Reason: "would break flows",
		}}
	}
	if rapid.Bool().Draw(rt, label+".hasAP") {
		out.AntiPatterns = []AntiPattern{{
			What:     "Skipping token rotation on the auth flow",
			Why:      "replay risk",
			Severity: "high",
		}}
	}
	if rapid.Bool().Draw(rt, label+".hasRisk") {
		out.Risks = []string{rapid.SampledFrom([]string{
			"unrelated risk",
			"token rotation under load is brittle",
		}).Draw(rt, label+".risk")}
	}
	return out
}

func equalConstraints(a, b []InheritedConstraint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ConstraintID != b[i].ConstraintID ||
			a[i].SourceIntent != b[i].SourceIntent ||
			a[i].What != b[i].What ||
			a[i].Why != b[i].Why ||
			a[i].Severity != b[i].Severity {
			return false
		}
		if len(a[i].MatchedBy) != len(b[i].MatchedBy) {
			return false
		}
		for j := range a[i].MatchedBy {
			if a[i].MatchedBy[j] != b[i].MatchedBy[j] {
				return false
			}
		}
	}
	return true
}
