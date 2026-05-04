package domain

import (
	"testing"
	"time"
)

// Test fixture builders. Keep them dumb — every test should be able
// to read its setup top-to-bottom without chasing helpers.

func mkAP(what, why, sev string) AntiPattern {
	return AntiPattern{What: what, Why: why, Severity: sev}
}

func mkSummary(decisions []Decision, rejected []RejectedAlternative, aps []AntiPattern, risks []string) *IntentSummary {
	return &IntentSummary{
		Title:        "t",
		What:         "w",
		Why:          "y",
		Decisions:    decisions,
		Rejected:     rejected,
		Risks:        risks,
		AntiPatterns: aps,
	}
}

func mkIntent(id string, sealedAt time.Time, files, subs []string, aps []AntiPattern) IntentView {
	iv := IntentView{
		IntentID: id,
		Status:   StatusMerged,
		Summary:  mkSummary(nil, nil, aps, nil),
		Fingerprint: &SemanticFingerprint{
			FilesTouched: files,
			Subsystems:   subs,
		},
	}
	if !sealedAt.IsZero() {
		iv.SealedAt = sealedAt.Format(time.RFC3339)
	}
	return iv
}

func mkConstraint(id, source string, openedAt time.Time, files []string, what, why, sev string) Constraint {
	c := Constraint{
		ID:           id,
		SourceIntent: source,
		Files:        files,
		What:         what,
		Why:          why,
		Severity:     sev,
	}
	if !openedAt.IsZero() {
		c.OpenedAt = openedAt.Format(time.RFC3339)
	}
	return c
}

// ------- BuildInheritedConstraints -------

func TestBuildInheritedConstraints_FileOverlap(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	view := &MainlineView{
		Constraints: []Constraint{
			mkConstraint("guard_old", "int_old", t0, []string{"a.go"}, "Don't drop session cookie on /oauth", "Breaks SSO", "high"),
		},
	}
	out := BuildInheritedConstraints(view, []string{"a.go"}, nil, "")
	if len(out) != 1 {
		t.Fatalf("want 1 constraint, got %d", len(out))
	}
	if out[0].SourceIntent != "int_old" || out[0].Severity != "high" {
		t.Errorf("unexpected: %+v", out[0])
	}
	if out[0].ConstraintID != "guard_old" {
		t.Errorf("constraint_id: want guard_old, got %s", out[0].ConstraintID)
	}
	if len(out[0].MatchedBy) != 1 || out[0].MatchedBy[0] != "file:a.go" {
		t.Errorf("matched_by: want [file:a.go], got %v", out[0].MatchedBy)
	}
}

func TestBuildInheritedConstraints_SubsystemOverlapRemoved(t *testing.T) {
	// v2: subsystem matching is removed. Only file overlap propagates.
	view := &MainlineView{
		Constraints: []Constraint{
			mkConstraint("guard_old", "int_old", time.Time{}, []string{"a.go"}, "Skip token rotation", "Replay attacks", "high"),
		},
	}
	// Same subsystem but different file → no match
	out := BuildInheritedConstraints(view, []string{"unrelated.go"}, []string{"auth"}, "")
	if len(out) != 0 {
		t.Fatalf("subsystem-only match should no longer propagate; got %d: %+v", len(out), out)
	}
}

func TestBuildInheritedConstraints_NoOverlapMisses(t *testing.T) {
	view := &MainlineView{
		Constraints: []Constraint{
			mkConstraint("guard_old", "int_old", time.Time{}, []string{"x.go"}, "Don't X", "Why", "high"),
		},
	}
	out := BuildInheritedConstraints(view, []string{"y.go"}, []string{"y"}, "")
	if len(out) != 0 {
		t.Errorf("want 0, got %d (%+v)", len(out), out)
	}
}

func TestBuildInheritedConstraints_TemporalFilter(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	view := &MainlineView{
		Intents: []IntentView{
			mkIntent("int_focus", t1.Add(15*24*time.Hour), []string{"a.go"}, nil, nil),
		},
		Constraints: []Constraint{
			mkConstraint("guard_old", "int_old", t1, []string{"a.go"}, "old constraint", "old why", "high"),
			mkConstraint("guard_new", "int_new", t2, []string{"a.go"}, "new constraint", "new why", "high"),
		},
	}
	// excluding int_focus (sealed mid-Jan) → int_old is inherited,
	// int_new (Feb) is in the future and must be filtered out.
	out := BuildInheritedConstraints(view, []string{"a.go"}, nil, "int_focus")
	if len(out) != 1 {
		t.Fatalf("want 1 (only int_old), got %d: %+v", len(out), out)
	}
	if out[0].ConstraintID != "guard_old" {
		t.Errorf("want guard_old, got %s", out[0].ConstraintID)
	}
}

func TestBuildInheritedConstraints_OnlyHighSeverity(t *testing.T) {
	// v2: only high severity propagates.
	view := &MainlineView{
		Constraints: []Constraint{
			mkConstraint("guard_a", "int_a", time.Time{}, []string{"a.go"}, "A low", "", "low"),
			mkConstraint("guard_b", "int_b", time.Time{}, []string{"a.go"}, "B high", "", "high"),
			mkConstraint("guard_c", "int_c", time.Time{}, []string{"a.go"}, "C medium", "", "medium"),
		},
	}
	out := BuildInheritedConstraints(view, []string{"a.go"}, nil, "")
	if len(out) != 1 {
		t.Fatalf("want 1 (only high), got %d: %+v", len(out), out)
	}
	if out[0].Severity != "high" || out[0].What != "B high" {
		t.Errorf("unexpected: %+v", out[0])
	}
}

func TestBuildInheritedConstraints_LegacyAntiPatternsDoNotPropagate(t *testing.T) {
	view := &MainlineView{
		Intents: []IntentView{
			{
				IntentID: "int_legacy",
				Status:   StatusMerged,
				Summary:  mkSummary(nil, nil, []AntiPattern{mkAP("AP", "", "high")}, nil),
				Fingerprint: &SemanticFingerprint{
					FilesTouched: []string{"a.go"},
				},
			},
		},
	}
	out := BuildInheritedConstraints(view, []string{"a.go"}, nil, "")
	if len(out) != 0 {
		t.Errorf("legacy anti_patterns should not propagate; got %v", out)
	}
}

func TestBuildInheritedConstraints_NeverTruncated(t *testing.T) {
	constraints := make([]Constraint, 50)
	for i := 0; i < 50; i++ {
		id := "guard_" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		constraints[i] = mkConstraint(id, "int_src", time.Time{}, []string{"a.go"}, "AP "+string(rune('a'+i%26)), "why", "high")
	}
	view := &MainlineView{Constraints: constraints}
	out := BuildInheritedConstraints(view, []string{"a.go"}, nil, "")
	if len(out) < 50 {
		t.Errorf("expected 50+, got %d (truncation regression)", len(out))
	}
}

// ------- AcknowledgementOf -------

func TestAcknowledgementOf_DecisionWins(t *testing.T) {
	ic := InheritedConstraint{
		What:     "Removing legacy session middleware on /oauth path",
		Severity: "high",
	}
	summary := mkSummary(
		[]Decision{{
			Point:     "session middleware",
			Chose:     "kept the legacy session middleware in place to preserve oauth state",
			Rationale: "callback handler reads session token",
		}},
		nil, nil, nil,
	)
	if got := AcknowledgementOf(ic, summary); got != AckDecision {
		t.Errorf("want AckDecision, got %q", got)
	}
}

func TestAcknowledgementOf_RejectedAlternative(t *testing.T) {
	ic := InheritedConstraint{What: "Removing legacy session middleware"}
	summary := mkSummary(nil,
		[]RejectedAlternative{{
			Alternative: "remove the session middleware entirely",
			Reason:      "would break SSO callbacks",
		}},
		nil, nil,
	)
	if got := AcknowledgementOf(ic, summary); got != AckRejected {
		t.Errorf("want AckRejected, got %q", got)
	}
}

func TestAcknowledgementOf_OwnAntiPattern(t *testing.T) {
	ic := InheritedConstraint{What: "Skip token rotation"}
	summary := mkSummary(nil, nil,
		[]AntiPattern{mkAP("Skipping token rotation on the auth flow", "replay risk", "high")},
		nil,
	)
	if got := AcknowledgementOf(ic, summary); got != AckAntiPattern {
		t.Errorf("want AckAntiPattern, got %q", got)
	}
}

func TestAcknowledgementOf_RiskFallback(t *testing.T) {
	ic := InheritedConstraint{What: "Skip token rotation"}
	summary := mkSummary(nil, nil, nil,
		[]string{"token rotation is brittle on the auth flow under load"},
	)
	if got := AcknowledgementOf(ic, summary); got != AckRisk {
		t.Errorf("want AckRisk, got %q", got)
	}
}

func TestAcknowledgementOf_NoMatch(t *testing.T) {
	ic := InheritedConstraint{What: "Removing legacy session middleware on /oauth path"}
	summary := mkSummary(
		[]Decision{{Point: "tests", Chose: "added more unit tests"}},
		nil, nil, nil,
	)
	if got := AcknowledgementOf(ic, summary); got != AckNone {
		t.Errorf("want AckNone, got %q", got)
	}
}

func TestAcknowledgementOf_PreferDecisionOverRisk(t *testing.T) {
	ic := InheritedConstraint{What: "Removing legacy session middleware"}
	summary := mkSummary(
		[]Decision{{
			Point: "session",
			Chose: "kept legacy session middleware",
		}},
		nil, nil,
		[]string{"legacy session middleware concerns"},
	)
	// Both fields contain enough overlap; the function must prefer
	// AckDecision over AckRisk because that is the load-bearing form.
	if got := AcknowledgementOf(ic, summary); got != AckDecision {
		t.Errorf("want AckDecision (preferred), got %q", got)
	}
}

func TestAcknowledgementOf_ShortConstraintNeedsAllTokens(t *testing.T) {
	// Two-token constraint requires both to be present.
	ic := InheritedConstraint{What: "Skip rotation"}
	// "skip" alone in another context must not count.
	summary := mkSummary(
		[]Decision{{Point: "ci", Chose: "skip the lint step"}},
		nil, nil, nil,
	)
	if got := AcknowledgementOf(ic, summary); got != AckNone {
		t.Errorf("want AckNone for partial single-token match, got %q", got)
	}
}

func TestHasExplicitAck(t *testing.T) {
	acks := []AcknowledgedConstraint{
		{ConstraintID: "int_abc#0", Disposition: "preserved", Note: "kept it"},
		{ConstraintID: "int_abc#1", Disposition: "not_applicable"},
	}
	if !hasExplicitAck(acks, "int_abc#0") {
		t.Error("should find int_abc#0")
	}
	if !hasExplicitAck(acks, "int_abc#1") {
		t.Error("should find int_abc#1")
	}
	if hasExplicitAck(acks, "int_abc#2") {
		t.Error("should not find int_abc#2")
	}
	if hasExplicitAck(nil, "int_abc#0") {
		t.Error("nil acks should return false")
	}
}
