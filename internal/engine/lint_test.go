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

func TestLintIntent_NoConstraintsIsInfo(t *testing.T) {
	r := LintIntent("int_x", &domain.IntentSummary{
		What:      "did real work",
		Why:       "y",
		Decisions: nonEmptyDecisions(),
		Risks:     nil, AntiPatterns: nil,
	}, nonEmptyFP(), "", nil)
	if !r.Pass {
		t.Errorf("info-only should pass: %+v", r)
	}
	if !hasCode(r.Issues, "no_constraints") {
		t.Errorf("missing no_constraints info: %+v", r.Issues)
	}
	if sevFor(r.Issues, "no_constraints") != "info" {
		t.Errorf("no_constraints should be info, got %s", sevFor(r.Issues, "no_constraints"))
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

func TestLintIntent_GenericRiskWarns(t *testing.T) {
	cases := []struct {
		risk string
		warn bool
	}{
		{"may break compatibility", true},
		{"needs testing", true},
		{"possible bug", true},
		{"could have side effects", true},
		{"too short", true}, // under 15 chars
		{"OAuth callback still depends on session state; removing middleware would break login redirect. Mitigation: keep /oauth session path.", false},
		{"The new batch git calls may race if two syncs run in parallel on the same repo.", false},
	}
	for _, tc := range cases {
		r := LintIntent("int_x", &domain.IntentSummary{
			What: "real work", Why: "y",
			Decisions: nonEmptyDecisions(),
			Risks:     []string{tc.risk},
		}, nonEmptyFP(), "", nil)
		got := hasCode(r.Issues, "generic_risk")
		if got != tc.warn {
			t.Errorf("risk=%q: expected generic_risk=%v, got %v (issues: %+v)", tc.risk, tc.warn, got, r.Issues)
		}
	}
}

func TestLintIntent_AntiPatternEmptyWhatOrWhyErrors(t *testing.T) {
	r := LintIntent("int_x", &domain.IntentSummary{
		What: "real work", Why: "y",
		Decisions: nonEmptyDecisions(),
		AntiPatterns: []domain.AntiPattern{
			{What: "", Why: "some reason"},
			{What: "don't do X", Why: ""},
		},
	}, nonEmptyFP(), "", nil)
	if r.Pass {
		t.Errorf("anti_pattern with empty what/why should fail")
	}
	if !hasCode(r.Issues, "anti_pattern_no_what") {
		t.Errorf("missing anti_pattern_no_what")
	}
	if !hasCode(r.Issues, "anti_pattern_no_why") {
		t.Errorf("missing anti_pattern_no_why")
	}
}

// --- v0.4 risk noise lint tests ---

func TestLintIntent_RiskSelfAcceptable(t *testing.T) {
	cases := []struct {
		risk string
		warn bool
	}{
		{"this is an acceptable trade-off for performance", true},
		{"可接受的技术债务", true},
		{"intended trade-off: less caching", true},
		{"trade-off accepted for now", true},
		{"有意取舍：简化实现", true},
		{"no concern about this approach", true},
		{"the batch endpoint may timeout under heavy load", false},
	}
	for _, tc := range cases {
		t.Run(tc.risk, func(t *testing.T) {
			r := LintIntent("int_x", &domain.IntentSummary{
				What: "real work", Why: "y",
				Decisions: nonEmptyDecisions(),
				Risks:     []string{tc.risk},
			}, nonEmptyFP(), "", nil)
			got := hasCode(r.Issues, "risk_self_acceptable")
			if got != tc.warn {
				t.Errorf("risk=%q: expected risk_self_acceptable=%v, got %v", tc.risk, tc.warn, got)
			}
		})
	}
}

func TestLintIntent_RiskIsFollowup(t *testing.T) {
	cases := []struct {
		risk string
		warn bool
	}{
		{"后续可加 retry 逻辑", true},
		{"可能需要加日志", true},
		{"再加一层缓存", true},
		{"follow-up: add rate limiting", true},
		{"later add pagination to the list endpoint", true},
		{"future enhancement for batch processing", true},
		{"需要后续补充测试", true},
		{"the endpoint returns 500 on malformed input", false},
	}
	for _, tc := range cases {
		t.Run(tc.risk, func(t *testing.T) {
			r := LintIntent("int_x", &domain.IntentSummary{
				What: "real work", Why: "y",
				Decisions: nonEmptyDecisions(),
				Risks:     []string{tc.risk},
			}, nonEmptyFP(), "", nil)
			got := hasCode(r.Issues, "risk_is_followup")
			if got != tc.warn {
				t.Errorf("risk=%q: expected risk_is_followup=%v, got %v", tc.risk, tc.warn, got)
			}
		})
	}
}

func TestLintIntent_RiskReviewGuidance(t *testing.T) {
	cases := []struct {
		risk string
		warn bool
	}{
		{"Reviewer should check the migration SQL", true},
		{"review focus: the auth middleware changes", true},
		{"评审重点：数据库迁移", true},
		{"本次修改的 fingerprint 需要确认", true},
		{"本分支 fingerprint 覆盖了 OAuth 模块", true},
		{"the session cookie path changed from / to /api", false},
	}
	for _, tc := range cases {
		t.Run(tc.risk, func(t *testing.T) {
			r := LintIntent("int_x", &domain.IntentSummary{
				What: "real work", Why: "y",
				Decisions: nonEmptyDecisions(),
				Risks:     []string{tc.risk},
			}, nonEmptyFP(), "", nil)
			got := hasCode(r.Issues, "risk_review_guidance")
			if got != tc.warn {
				t.Errorf("risk=%q: expected risk_review_guidance=%v, got %v", tc.risk, tc.warn, got)
			}
		})
	}
}

func TestLintIntent_RiskLooksLikeAntipattern(t *testing.T) {
	cases := []struct {
		risk string
		warn bool
	}{
		{"callers must not remove the legacy middleware", true},
		{"do not delete the session table", true},
		{"never call sync during seal", true},
		{"requires discipline to maintain ordering", true},
		{"this is an anti-pattern if used without guards", true},
		{"禁止删除旧的会话中间件", true},
		{"the new batch endpoint bypasses auth", false},
	}
	for _, tc := range cases {
		t.Run(tc.risk, func(t *testing.T) {
			r := LintIntent("int_x", &domain.IntentSummary{
				What: "real work", Why: "y",
				Decisions: nonEmptyDecisions(),
				Risks:     []string{tc.risk},
			}, nonEmptyFP(), "", nil)
			got := hasCode(r.Issues, "risk_looks_like_antipattern")
			if got != tc.warn {
				t.Errorf("risk=%q: expected risk_looks_like_antipattern=%v, got %v", tc.risk, tc.warn, got)
			}
		})
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
