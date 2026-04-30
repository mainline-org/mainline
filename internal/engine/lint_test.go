package engine

import (
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

// LintIntent is the pure core of `mainline lint`. These tests
// exercise it directly so we don't need a repo fixture for the
// per-rule coverage.

func TestLintIntent_EmptyWhatIsError(t *testing.T) {
	r := LintIntent("int_x", &domain.IntentSummary{What: "", Why: "y", Decisions: nonEmptyDecisions()}, nonEmptyFP(), "", nil)
	if r.Pass {
		t.Fatalf("empty what should fail: %+v", r)
	}
	if !hasCode(r.Issues, "empty_what") {
		t.Errorf("missing empty_what issue: %+v", r.Issues)
	}
}

func TestLintIntent_BoilerplateWhatRejected(t *testing.T) {
	cases := []string{
		"implemented changes",
		"Made the changes.",
		"see diff",
		"As requested.",
		"TBD",
	}
	for _, what := range cases {
		t.Run(what, func(t *testing.T) {
			r := LintIntent("int_x", &domain.IntentSummary{What: what, Why: "y", Decisions: nonEmptyDecisions()}, nonEmptyFP(), "", nil)
			if r.Pass {
				t.Errorf("boilerplate %q should fail: %+v", what, r)
			}
			if !hasCode(r.Issues, "boilerplate_what") {
				t.Errorf("missing boilerplate_what for %q: %+v", what, r.Issues)
			}
		})
	}
}

func TestLintIntent_RealisticWhatPasses(t *testing.T) {
	r := LintIntent("int_x", &domain.IntentSummary{
		What: "Replace session middleware with JWT validation on /api routes; legacy /oauth path keeps the session cookie.",
		Why:  "Sessions don't scale across regions; JWT is stateless and works behind any LB.",
		Decisions: []domain.Decision{
			{Point: "auth shape", Chose: "JWT", Rationale: "stateless"},
		},
		Risks: []string{"old mobile clients still send session cookie"},
	}, nonEmptyFP(), "", nil)
	if !r.Pass {
		t.Errorf("realistic seal should pass: %+v", r)
	}
}

func TestLintIntent_NoDecisionsIsError(t *testing.T) {
	r := LintIntent("int_x", &domain.IntentSummary{What: "did real work", Why: "y", Decisions: nil}, nonEmptyFP(), "", nil)
	if r.Pass {
		t.Fatalf("no decisions should fail: %+v", r)
	}
	if !hasCode(r.Issues, "no_decisions") {
		t.Errorf("missing no_decisions: %+v", r.Issues)
	}
}

func TestLintIntent_LongChoseWithoutRationaleWarns(t *testing.T) {
	long := strings.Repeat("x", rationaleThreshold+1)
	r := LintIntent("int_x", &domain.IntentSummary{
		What: "did real work", Why: "y",
		Decisions: []domain.Decision{
			{Point: "p", Chose: long, Rationale: ""},
		},
	}, nonEmptyFP(), "", nil)
	if !r.Pass {
		t.Errorf("warnings should not flip Pass: %+v", r)
	}
	if !hasCode(r.Issues, "decision_no_rationale") {
		t.Errorf("missing decision_no_rationale: %+v", r.Issues)
	}
	if sevFor(r.Issues, "decision_no_rationale") != "warning" {
		t.Errorf("decision_no_rationale should be warning")
	}
}

func TestLintIntent_ShortChoseDoesNotRequireRationale(t *testing.T) {
	r := LintIntent("int_x", &domain.IntentSummary{
		What: "did real work", Why: "y",
		Decisions: []domain.Decision{
			{Point: "p", Chose: "JWT", Rationale: ""},
		},
	}, nonEmptyFP(), "", nil)
	if hasCode(r.Issues, "decision_no_rationale") {
		t.Errorf("short chose should not require rationale: %+v", r.Issues)
	}
}

func TestLintIntent_NoConstraintsIsWarning(t *testing.T) {
	r := LintIntent("int_x", &domain.IntentSummary{
		What:      "did real work",
		Why:       "y",
		Decisions: nonEmptyDecisions(),
		Risks:     nil, AntiPatterns: nil,
	}, nonEmptyFP(), "", nil)
	if !r.Pass {
		t.Errorf("warning-only should pass: %+v", r)
	}
	if !hasCode(r.Issues, "no_constraints") {
		t.Errorf("missing no_constraints warning: %+v", r.Issues)
	}
	if sevFor(r.Issues, "no_constraints") != "warning" {
		t.Errorf("no_constraints should be warning, got %s", sevFor(r.Issues, "no_constraints"))
	}
}

func TestLintIntent_RisksOrAntiPatternsClearWarning(t *testing.T) {
	r := LintIntent("int_x", &domain.IntentSummary{
		What: "did work", Why: "y",
		Decisions: nonEmptyDecisions(),
		AntiPatterns: []domain.AntiPattern{
			{What: "delete x", Why: "x is load-bearing"},
		},
	}, nonEmptyFP(), "", nil)
	if hasCode(r.Issues, "no_constraints") {
		t.Errorf("anti_patterns alone should clear no_constraints: %+v", r.Issues)
	}
}

func TestLintIntent_FingerprintEmpty(t *testing.T) {
	r := LintIntent("int_x", &domain.IntentSummary{
		What: "real work", Why: "y", Decisions: nonEmptyDecisions(),
	}, &domain.SemanticFingerprint{}, "", nil)
	if r.Pass {
		t.Errorf("empty fingerprint should fail: %+v", r)
	}
	if !hasCode(r.Issues, "fingerprint_no_subsystems") || !hasCode(r.Issues, "fingerprint_no_files") {
		t.Errorf("expected both fingerprint issues: %+v", r.Issues)
	}
}

func TestLintIntent_SupersedesUnknownIsError(t *testing.T) {
	known := map[string]bool{"int_a": true, "int_b": true}
	r := LintIntent("int_x", &domain.IntentSummary{
		What: "real work", Why: "y", Decisions: nonEmptyDecisions(),
	}, nonEmptyFP(), "int_does_not_exist", known)
	if r.Pass {
		t.Errorf("unknown supersedes should fail: %+v", r)
	}
	if !hasCode(r.Issues, "supersedes_unknown") {
		t.Errorf("missing supersedes_unknown: %+v", r.Issues)
	}
}

func TestLintIntent_SupersedesKnownIsSilent(t *testing.T) {
	known := map[string]bool{"int_a": true, "int_b": true}
	r := LintIntent("int_x", &domain.IntentSummary{
		What: "real work", Why: "y", Decisions: nonEmptyDecisions(),
		Risks: []string{"r"},
	}, nonEmptyFP(), "int_a", known)
	if hasCode(r.Issues, "supersedes_unknown") {
		t.Errorf("known supersedes should be silent: %+v", r.Issues)
	}
	if !r.Pass {
		t.Errorf("known supersedes should pass: %+v", r)
	}
}

// LintSealResult is the pre-submit variant. Smoke-test it goes
// through the same checks so a future seal-pipeline integration
// inherits the per-rule coverage above.
func TestLintSealResult_Wraps(t *testing.T) {
	r := LintSealResult(&domain.SealResult{
		IntentID: "int_x",
		Summary: domain.IntentSummary{
			What: "implemented changes", Why: "y", Decisions: nonEmptyDecisions(),
		},
		Fingerprint: domain.SemanticFingerprint{Subsystems: []string{"s"}, FilesTouched: []string{"f"}},
	}, nil)
	if r.Pass {
		t.Errorf("boilerplate should fail through LintSealResult: %+v", r)
	}
}

// helpers

func nonEmptyDecisions() []domain.Decision {
	return []domain.Decision{{Point: "p", Chose: "X", Rationale: ""}}
}

func nonEmptyFP() *domain.SemanticFingerprint {
	return &domain.SemanticFingerprint{Subsystems: []string{"s"}, FilesTouched: []string{"f.go"}}
}

func hasCode(issues []LintIssue, code string) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

func sevFor(issues []LintIssue, code string) string {
	for _, i := range issues {
		if i.Code == code {
			return i.Severity
		}
	}
	return ""
}

// LintInheritedAcknowledgement is the v1 awareness-not-conviction
// gate: when a sealed intent's files overlap a prior intent's
// anti_patterns, the seal must acknowledge the high-severity ones.

func TestLintInherited_NoConstraints_NoIssues(t *testing.T) {
	got := LintInheritedAcknowledgement(&domain.IntentSummary{}, nil)
	if len(got) != 0 {
		t.Errorf("expected zero issues, got %v", got)
	}
}

func TestLintInherited_HighUnacknowledgedWarns(t *testing.T) {
	summary := &domain.IntentSummary{
		Decisions: []domain.Decision{{Point: "tests", Chose: "added unit tests"}},
	}
	inherited := []domain.InheritedConstraint{{
		SourceIntent: "int_old",
		What:         "Removing legacy session middleware on /oauth path",
		Severity:     "high",
	}}
	issues := LintInheritedAcknowledgement(summary, inherited)
	if !hasCode(issues, "inherited_anti_pattern_not_acknowledged") {
		t.Errorf("expected warn issue, got %v", issues)
	}
	if sevFor(issues, "inherited_anti_pattern_not_acknowledged") != "warning" {
		t.Errorf("v1 must be warning, not error")
	}
}

func TestLintInherited_HighAcknowledgedViaDecision(t *testing.T) {
	summary := &domain.IntentSummary{
		Decisions: []domain.Decision{{
			Point: "session middleware",
			Chose: "kept the legacy session middleware in place",
		}},
	}
	inherited := []domain.InheritedConstraint{{
		SourceIntent: "int_old",
		What:         "Removing legacy session middleware on /oauth path",
		Severity:     "high",
	}}
	issues := LintInheritedAcknowledgement(summary, inherited)
	if hasCode(issues, "inherited_anti_pattern_not_acknowledged") {
		t.Errorf("acknowledged constraint must NOT warn: %v", issues)
	}
	if !hasCode(issues, "inherited_anti_pattern_acknowledged") {
		t.Errorf("acknowledged-info issue should be present: %v", issues)
	}
}

func TestLintInherited_MediumNeverWarns(t *testing.T) {
	summary := &domain.IntentSummary{}
	inherited := []domain.InheritedConstraint{{
		SourceIntent: "int_old",
		What:         "Skip token rotation",
		Severity:     "medium",
	}}
	issues := LintInheritedAcknowledgement(summary, inherited)
	if hasCode(issues, "inherited_anti_pattern_not_acknowledged") {
		t.Errorf("v1 only warns on high-severity; got %v", issues)
	}
	if !hasCode(issues, "inherited_anti_pattern_surfaced") {
		t.Errorf("medium should still surface as info: %v", issues)
	}
	if sevFor(issues, "inherited_anti_pattern_surfaced") != "info" {
		t.Errorf("medium-severity surface must be info severity")
	}
}
