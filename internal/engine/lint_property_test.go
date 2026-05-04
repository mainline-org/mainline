//go:build !quick

package engine

import (
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/mainline-org/mainline/internal/core"
	"github.com/mainline-org/mainline/internal/domain"
)

// PBTs for the lint subsystem and the AntiPattern validator. These
// supplement the example-based tests in lint_test.go and
// context_retrieval_test.go: examples pin obvious cases for fast
// feedback; PBTs explore the input space rapid considers most
// adversarial.

// LintIntent is documented as pure (same arguments → same issue
// list). Rapid generates random IntentSummary + SemanticFingerprint
// + supersedes ref + viewIDs and asserts two consecutive calls
// return the same Pass / issue list. A future hidden-state
// regression (caching, time-of-day, env-var read) flips this loud.
func TestPropertyLintIntentDeterministic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		summary := drawSummaryForLint(rt, "summary")
		fingerprint := drawFingerprintForLint(rt, "fp")
		supersedes := rapid.SampledFrom([]string{"", "int_known", "int_unknown"}).Draw(rt, "supersedes")
		viewIDs := map[string]bool{"int_known": true}

		first := LintIntent("int_x", summary, fingerprint, supersedes, viewIDs)
		for i := 0; i < 5; i++ {
			again := LintIntent("int_x", summary, fingerprint, supersedes, viewIDs)
			if first.Pass != again.Pass {
				rt.Fatalf("lint Pass flipped on rerun: first=%v then=%v", first.Pass, again.Pass)
			}
			if len(first.Issues) != len(again.Issues) {
				rt.Fatalf("issue count flipped on rerun (iter %d): first=%d then=%d", i, len(first.Issues), len(again.Issues))
			}
			for j := range first.Issues {
				if first.Issues[j].Code != again.Issues[j].Code {
					rt.Fatalf("issue[%d].Code flipped on rerun: %q → %q", j, first.Issues[j].Code, again.Issues[j].Code)
				}
				if first.Issues[j].Severity != again.Issues[j].Severity {
					rt.Fatalf("issue[%d].Severity flipped on rerun: %q → %q", j, first.Issues[j].Severity, again.Issues[j].Severity)
				}
			}
		}
	})
}

// LintIntent must never flip Pass to false based purely on warnings.
// The contract is: errors fail Pass, warnings/info do not. Property:
// for any input that produces only warning/info issues, Pass must
// be true.
func TestPropertyLintWarningsNeverFailPass(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		summary := drawSummaryForLint(rt, "summary")
		fingerprint := drawFingerprintForLint(rt, "fp")
		supersedes := rapid.SampledFrom([]string{"", "int_known"}).Draw(rt, "supersedes")
		viewIDs := map[string]bool{"int_known": true}

		res := LintIntent("int_x", summary, fingerprint, supersedes, viewIDs)
		hasError := false
		for _, iss := range res.Issues {
			if iss.Severity == "error" {
				hasError = true
				break
			}
		}
		if !hasError && !res.Pass {
			rt.Fatalf("Pass must be true when there are no errors: issues=%+v", res.Issues)
		}
		if hasError && res.Pass {
			rt.Fatalf("Pass must be false when any error is present: issues=%+v", res.Issues)
		}
	})
}

// AntiPattern validation: ValidateSealResult must reject any
// AntiPattern with empty `what` OR empty `why`, and must accept any
// AntiPattern with non-empty both fields and a canonical severity.
// Property test asserts the rule across random inputs.
func TestPropertyAntiPatternValidation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// A baseline-valid SealResult so we isolate the anti_pattern
		// rules under test.
		base := func() *domain.SealResult {
			return &domain.SealResult{
				IntentID: "int_x12345678",
				Summary: domain.IntentSummary{
					Title: "t", What: "w", Why: "y",
				},
				Fingerprint: domain.SemanticFingerprint{
					Subsystems:   []string{"s"},
					FilesTouched: []string{"f.go"},
				},
				Confidence: domain.SealConfidence{Summary: 0.9, Fingerprint: 0.9},
			}
		}

		whatRaw := rapid.SampledFrom([]string{"", " ", "do not delete X", "Removing the /oauth path"}).Draw(rt, "what")
		whyRaw := rapid.SampledFrom([]string{"", "  ", "OAuth callback needs it", "Production constraint"}).Draw(rt, "why")
		severity := rapid.SampledFrom([]string{"", "low", "medium", "high", "catastrophic", "highish"}).Draw(rt, "sev")

		sr := base()
		sr.Summary.AntiPatterns = []domain.AntiPattern{
			{What: whatRaw, Why: whyRaw, Severity: severity},
		}
		err := importedValidate(sr)

		whatEmpty := strings.TrimSpace(whatRaw) == ""
		whyEmpty := strings.TrimSpace(whyRaw) == ""
		sevValid := severity == "" || severity == "low" || severity == "medium" || severity == "high"

		switch {
		case whatEmpty:
			if err == nil {
				rt.Fatalf("empty what must be rejected: what=%q", whatRaw)
			}
			if !strings.Contains(err.Error(), "what") {
				rt.Errorf("error should mention 'what' for empty-what case: %v", err)
			}
		case whyEmpty:
			if err == nil {
				rt.Fatalf("empty why must be rejected: why=%q", whyRaw)
			}
			if !strings.Contains(err.Error(), "why") {
				rt.Errorf("error should mention 'why' for empty-why case: %v", err)
			}
		case !sevValid:
			if err == nil {
				rt.Fatalf("non-canonical severity must be rejected: sev=%q", severity)
			}
		default:
			if err != nil {
				rt.Fatalf("valid anti_pattern must pass: what=%q why=%q sev=%q got %v",
					whatRaw, whyRaw, severity, err)
			}
		}
	})
}

// Branch reachability for classifyRetrievalStatus: each of the four
// canonical statuses must be producible by some realistic
// IntentView. If a future refactor accidentally orphans a branch
// (e.g. drops the file-churn check), this property fails because
// `stale` becomes unreachable.
func TestPropertyClassifyRetrievalStatusAllBranchesReachable(t *testing.T) {
	now := nowForBranchTest()
	seen := map[string]bool{}

	// Drive at most a few hundred iterations; reachability is a
	// universal-existence claim (∃ input → status), so even one hit
	// per branch is enough.
	rapid.Check(t, func(rt *rapid.T) {
		iv := drawIntentView(rt, "iv")
		churn := drawChurnMap(rt, "churn", iv.IntentID)
		got := classifyRetrievalStatus(iv, churn, now)
		seen[got] = true

		// Once all four are seen, accept; otherwise rapid will keep
		// drawing. This is a one-way property — failure means
		// "rapid couldn't find an input for status X".
		if len(seen) == 4 {
			return
		}
	})

	for _, want := range []string{
		RetrievalStatusCurrent,
		RetrievalStatusSuperseded,
		RetrievalStatusAbandoned,
		RetrievalStatusStale,
	} {
		if !seen[want] {
			t.Errorf("status %q was unreachable across %d random inputs — branch may be orphaned",
				want, len(seen))
		}
	}
}

// -----------------------------------------------------------
// Generators (kept narrow — these PBTs cover lint + validation,
// not the full retrieval generator from
// context_retrieval_property_test.go)
// -----------------------------------------------------------

func drawSummaryForLint(rt *rapid.T, label string) *domain.IntentSummary {
	if !rapid.Bool().Draw(rt, label+".present") {
		return nil
	}
	out := &domain.IntentSummary{}
	out.What = rapid.SampledFrom([]string{
		"", " ", "implemented changes", "real meaningful what text",
	}).Draw(rt, label+".what")
	out.Why = rapid.SampledFrom([]string{"", "real reason"}).Draw(rt, label+".why")
	nDecisions := rapid.IntRange(0, 2).Draw(rt, label+".nDecisions")
	for i := 0; i < nDecisions; i++ {
		chose := rapid.SampledFrom([]string{
			"X",
			"a longer choice that should warrant a rationale because it goes past the threshold",
		}).Draw(rt, label+".dchose")
		rationale := rapid.SampledFrom([]string{"", "because reasons"}).Draw(rt, label+".drat")
		out.Decisions = append(out.Decisions, domain.Decision{Point: "p", Chose: chose, Rationale: rationale})
	}
	if rapid.Bool().Draw(rt, label+".hasRisks") {
		out.Risks = domain.LegacyRiskStatements("a risk")
	}
	if rapid.Bool().Draw(rt, label+".hasAPs") {
		out.AntiPatterns = []domain.AntiPattern{{What: "do not", Why: "load-bearing"}}
	}
	return out
}

func drawFingerprintForLint(rt *rapid.T, label string) *domain.SemanticFingerprint {
	if !rapid.Bool().Draw(rt, label+".present") {
		return nil
	}
	fp := &domain.SemanticFingerprint{}
	if rapid.Bool().Draw(rt, label+".hasSubs") {
		fp.Subsystems = []string{"sub"}
	}
	if rapid.Bool().Draw(rt, label+".hasFiles") {
		fp.FilesTouched = []string{"f.go"}
	}
	return fp
}

// importedValidate is a tiny shim so the property test can call
// core.ValidateSealResult without repeating the import expression
// at every call site.
func importedValidate(sr *domain.SealResult) error {
	return core.ValidateSealResult(sr)
}

func nowForBranchTest() time.Time {
	return time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)
}
