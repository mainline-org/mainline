package engine

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mainline-org/mainline/internal/domain"
)

// `mainline lint` — deterministic seal-quality checks. Step 3 from
// docs_for_ai/mainline-spec-v0.2.md.
//
// Bounded context: lint is the *quality gate* for the input to
// retrieval. context_retrieval reads sealed intents; if seals are
// boilerplate or missing decisions, retrieval is rich-looking but
// useless. Lint is what stops that drift.
//
// The `mainline lint` command is *advisory* — it does not itself
// mutate or block anything. SealSubmit reuses the same deterministic
// checks for its pre-mutation hard gate, while this command remains
// the read-only inspection surface:
//
//   mainline lint                       # the active draft
//   mainline lint <intent_id>           # any intent in view or draft
//
// Checks are deliberately deterministic — no LLM scoring, no style
// critique. The cost-benefit of LLM-based linting only makes sense
// once the deterministic baseline has caught the obvious failures.

// LintIssue is one finding. Code is a stable identifier so
// downstream tooling can suppress / require / categorise; Field is
// a JSON-pointer-ish path useful for editor integrations later.
type LintIssue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"` // "error" | "warning" | "info"
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
}

// LintResult is the per-intent rollup. Pass is true iff there are
// zero error-severity issues — warnings and info do not flip Pass.
type LintResult struct {
	IntentID string      `json:"intent_id"`
	Issues   []LintIssue `json:"issues"`
	Pass     bool        `json:"pass"`
}

// boilerplateWhat patterns lint flags as a "your seal does not
// describe the change" boilerplate. Tight allowlist — false
// positives are worse than false negatives because lint is
// advisory and a noisy lint gets ignored.
var boilerplateWhat = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\s*implemented?\s+(the\s+)?changes\.?\s*$`),
	regexp.MustCompile(`(?i)^\s*made\s+(the\s+)?(requested\s+)?changes\.?\s*$`),
	regexp.MustCompile(`(?i)^\s*see\s+(the\s+)?(diff|commits?|pr)\.?\s*$`),
	regexp.MustCompile(`(?i)^\s*as\s+(per|requested)\.?\s*$`),
	regexp.MustCompile(`(?i)^\s*tbd\.?\s*$`),
}

// rationaleThreshold is the length of a decision's `chose` field
// above which lint expects a non-empty rationale. Picked at 50
// characters because anything longer than a one-line choice
// almost always benefits from explaining why.
const rationaleThreshold = 50

// LintIntent runs the deterministic checks against a single
// intent. The intent can be a draft (Goal + maybe partial summary)
// or a sealed view entry (full Summary + Fingerprint). The
// `viewIntents` map is the universe of known intent IDs used to
// validate cross-intent references (e.g. supersedes); empty is
// fine — those checks are skipped.
//
// Pure, no I/O — easy to unit-test.
//
// LintIntent does NOT take a view because it must stay pure for
// unit tests. The inherited-anti-pattern check lives in
// LintWithInheritedConstraints below; Service.Lint composes both.
func LintIntent(id string, summary *domain.IntentSummary, fingerprint *domain.SemanticFingerprint, supersedesRef string, viewIntents map[string]bool) LintResult {
	out := LintResult{IntentID: id}

	if summary == nil {
		out.Issues = append(out.Issues, LintIssue{
			Code:     "missing_summary",
			Severity: "error",
			Field:    "summary",
			Message:  "intent has no summary; lint requires a sealed intent or a populated draft",
		})
		out.Pass = false
		return out
	}

	if strings.TrimSpace(summary.What) == "" {
		out.Issues = append(out.Issues, LintIssue{
			Code: "empty_what", Severity: "error", Field: "summary.what",
			Message: "summary.what is empty",
		})
	} else if isBoilerplate(summary.What) {
		out.Issues = append(out.Issues, LintIssue{
			Code: "boilerplate_what", Severity: "error", Field: "summary.what",
			Message: fmt.Sprintf("summary.what is boilerplate (%q); describe the actual change", strings.TrimSpace(summary.What)),
		})
	}

	if strings.TrimSpace(summary.Why) == "" {
		out.Issues = append(out.Issues, LintIssue{
			Code: "empty_why", Severity: "error", Field: "summary.why",
			Message: "summary.why is empty",
		})
	}

	if len(summary.Decisions) == 0 {
		out.Issues = append(out.Issues, LintIssue{
			Code: "no_decisions", Severity: "error", Field: "summary.decisions",
			Message: "no decisions recorded; every non-trivial seal carries at least one explicit decision",
		})
	}
	for i, d := range summary.Decisions {
		if strings.TrimSpace(d.Chose) == "" {
			out.Issues = append(out.Issues, LintIssue{
				Code: "decision_no_chose", Severity: "error",
				Field:   fmt.Sprintf("summary.decisions[%d].chose", i),
				Message: fmt.Sprintf("decisions[%d].chose is empty", i),
			})
			continue
		}
		if len(d.Chose) > rationaleThreshold && strings.TrimSpace(d.Rationale) == "" {
			out.Issues = append(out.Issues, LintIssue{
				Code: "decision_no_rationale", Severity: "warning",
				Field:   fmt.Sprintf("summary.decisions[%d].rationale", i),
				Message: fmt.Sprintf("decisions[%d].chose is %d chars but no rationale recorded; longer choices should explain why", i, len(d.Chose)),
			})
		}
	}

	// Risk quality lint: flag generic/boilerplate risks that add noise
	// without actionable information. Soft risks should state impact +
	// affected subsystem; ideally mention mitigation or test coverage.
	for i, risk := range summary.Risks {
		if isGenericRisk(risk) {
			out.Issues = append(out.Issues, LintIssue{
				Code: "generic_risk", Severity: "warning",
				Field:   fmt.Sprintf("summary.risks[%d]", i),
				Message: fmt.Sprintf("risk is too generic (%q); a good risk names the affected subsystem, the specific failure mode, and ideally a mitigation", truncate(risk)),
			})
		}
		if isAcceptableTradeoff(risk) {
			out.Issues = append(out.Issues, LintIssue{
				Code: "risk_self_acceptable", Severity: "warning",
				Field:   fmt.Sprintf("summary.risks[%d]", i),
				Message: fmt.Sprintf("risk text says the trade-off is acceptable (%s); consider moving to decisions[].chose with rationale instead", truncate(risk)),
			})
		}
		if isFollowup(risk) {
			out.Issues = append(out.Issues, LintIssue{
				Code: "risk_is_followup", Severity: "warning",
				Field:   fmt.Sprintf("summary.risks[%d]", i),
				Message: fmt.Sprintf("risk text looks like a follow-up item (%s); move it to followups only if it is explicit later work, otherwise remove it", truncate(risk)),
			})
		}
		if isReviewGuidance(risk) {
			out.Issues = append(out.Issues, LintIssue{
				Code: "risk_review_guidance", Severity: "warning",
				Field:   fmt.Sprintf("summary.risks[%d]", i),
				Message: fmt.Sprintf("risk text looks like review guidance (%s); consider moving to review_notes (ephemeral, not inherited)", truncate(risk)),
			})
		}
		if looksLikeAntiPattern(risk) {
			out.Issues = append(out.Issues, LintIssue{
				Code: "risk_looks_like_antipattern", Severity: "warning",
				Field:   fmt.Sprintf("summary.risks[%d]", i),
				Message: fmt.Sprintf("risk text contains rule-like language (%s); if this is a hard constraint, it belongs in anti_patterns", truncate(risk)),
			})
		}
	}

	// Anti-pattern quality: every anti_pattern must have both what and why.
	for i, ap := range summary.AntiPatterns {
		if strings.TrimSpace(ap.What) == "" {
			out.Issues = append(out.Issues, LintIssue{
				Code: "anti_pattern_no_what", Severity: "error",
				Field:   fmt.Sprintf("summary.anti_patterns[%d].what", i),
				Message: fmt.Sprintf("anti_patterns[%d].what is empty; hard constraints must state what is forbidden", i),
			})
		}
		if strings.TrimSpace(ap.Why) == "" {
			out.Issues = append(out.Issues, LintIssue{
				Code: "anti_pattern_no_why", Severity: "error",
				Field:   fmt.Sprintf("summary.anti_patterns[%d].why", i),
				Message: fmt.Sprintf("anti_patterns[%d].why is empty; without a reason the constraint will be ignored by future agents", i),
			})
		}
	}

	if fingerprint != nil {
		if len(fingerprint.Subsystems) == 0 {
			out.Issues = append(out.Issues, LintIssue{
				Code: "fingerprint_no_subsystems", Severity: "error",
				Field:   "fingerprint.subsystems",
				Message: "fingerprint.subsystems is empty; phase-1 conflict detection cannot match",
			})
		}
		if len(fingerprint.FilesTouched) == 0 {
			out.Issues = append(out.Issues, LintIssue{
				Code: "fingerprint_no_files", Severity: "error",
				Field:   "fingerprint.files_touched",
				Message: "fingerprint.files_touched is empty; per-file retrieval cannot match",
			})
		}
	}

	if supersedesRef != "" && len(viewIntents) > 0 && !viewIntents[supersedesRef] {
		out.Issues = append(out.Issues, LintIssue{
			Code: "supersedes_unknown", Severity: "error",
			Field:   "status_evidence.superseded_by_intent",
			Message: fmt.Sprintf("supersedes references unknown intent %q; check the id and run mainline sync if a peer just sealed it", supersedesRef),
		})
	}

	out.Pass = !hasErrors(out.Issues)
	return out
}

func isBoilerplate(s string) bool {
	for _, re := range boilerplateWhat {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func hasErrors(issues []LintIssue) bool {
	for _, i := range issues {
		if i.Severity == "error" {
			return true
		}
	}
	return false
}

// Lint is the Service entry point. Resolves the intent ID against
// the view (sealed) or drafts (in-flight) and runs LintIntent.
//
// Empty id means "the active draft on this branch".
func (s *Service) Lint(id string) (*LintResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}
	if id == "" {
		branch, _ := s.Git.CurrentBranch()
		d, _ := s.Store.FindActiveDraft(branch)
		if d == nil {
			return nil, domain.NewRecoverableError(
				domain.ErrNoActiveIntent,
				"no active draft on this branch",
				"start an intent first: mainline start \"<goal>\"",
				"or pass an intent id: mainline lint <intent_id>",
			)
		}
		id = d.IntentID
	}

	view, _ := s.Store.ReadMainlineView()
	knownIDs := map[string]bool{}
	if view != nil {
		for _, iv := range view.Intents {
			knownIDs[iv.IntentID] = true
		}
	}

	// Sealed view takes precedence: after seal, both the draft file
	// and the view entry exist; the view is the authoritative seal
	// payload, the draft is just history.
	if view != nil {
		for _, iv := range view.Intents {
			if iv.IntentID != id {
				continue
			}
			res := LintIntent(iv.IntentID, iv.Summary, iv.Fingerprint, iv.StatusEvidence.SupersededByIntent, knownIDs)
			// Inherited-AP acknowledgement check: any sealed intent
			// in the catalog whose touched files / subsystems
			// overlap this one's contributes anti_patterns the
			// current seal must have acknowledged.
			if iv.Fingerprint != nil {
				inherited := domain.BuildInheritedConstraints(view, iv.Fingerprint.FilesTouched, iv.Fingerprint.Subsystems, iv.IntentID)
				res.Issues = append(res.Issues, LintInheritedAcknowledgement(iv.Summary, inherited)...)
				res.Pass = !hasErrors(res.Issues)
			}
			return &res, nil
		}
	}

	// Pre-seal draft: surfaces an informational notice. Drafts
	// don't carry structured Summary/Fingerprint yet (only Goal and
	// turns). Once the seal pipeline runs lint as part of submit,
	// this branch becomes the LintSealResult path.
	if d, _ := s.Store.ReadDraft(id); d != nil {
		r := LintResult{IntentID: id, Pass: true}
		r.Issues = append(r.Issues, LintIssue{
			Code: "lint_pre_seal_unsupported", Severity: "info",
			Message: "draft intents do not yet have a structured summary; run lint after `mainline seal --prepare` and before `--submit` to check the seal payload",
		})
		return &r, nil
	}
	return nil, domain.NewRecoverableError(
		domain.ErrInvalidInput,
		fmt.Sprintf("intent %q not found in drafts or sealed view", id),
		"check the id (`mainline log` to browse)",
		"or run `mainline sync` if a peer just sealed it",
	)
}

// LintSealResult is the seal-payload variant: lints a fully-formed
// SealResult before submit. SealSubmit uses this deterministic gate
// before any mutation; the standalone `mainline lint` command remains
// advisory and reads sealed view entries after the fact.
func LintSealResult(sr *domain.SealResult, viewIntents map[string]bool) LintResult {
	if sr == nil {
		return LintResult{Pass: false, Issues: []LintIssue{{
			Code: "missing_seal_result", Severity: "error",
			Message: "seal result is nil",
		}}}
	}
	res := LintIntent(sr.IntentID, &sr.Summary, &sr.Fingerprint, "", viewIntents)
	res.Issues = append(res.Issues, sealActionSignalLintIssues(sr)...)
	res.Pass = !hasErrors(res.Issues)
	return res
}

func sealActionSignalLintIssues(sr *domain.SealResult) []LintIssue {
	if sr == nil {
		return nil
	}
	var out []LintIssue
	if len(sr.Summary.AntiPatterns) > 0 {
		out = append(out, LintIssue{
			Code:     "seal_action_signal_constraint",
			Severity: "error",
			Field:    "summary.anti_patterns",
			Message:  "seal cannot create constraints; use human-confirmed `mainline guard add`",
		})
	}
	if len(sr.Summary.Risks) > 0 {
		out = append(out, LintIssue{
			Code:     "seal_action_signal_risk",
			Severity: "error",
			Field:    "summary.risks",
			Message:  "seal cannot create risks; use `mainline risk add` with a structured failure mode",
		})
	}
	if len(sr.Summary.Followups) > 0 {
		out = append(out, LintIssue{
			Code:     "seal_action_signal_followup",
			Severity: "error",
			Field:    "summary.followups",
			Message:  "seal cannot create follow-ups; use `mainline followup add` with explicit provenance",
		})
	}
	return out
}

// LintSealResultWithView extends LintSealResult with inherited-constraint
// acknowledgement checks from the current view. These remain warning-level:
// they are surfaced before submit, but do not block sealing.
func LintSealResultWithView(sr *domain.SealResult, view *domain.MainlineView) LintResult {
	knownIDs := map[string]bool{}
	if view != nil {
		for _, iv := range view.Intents {
			knownIDs[iv.IntentID] = true
		}
	}
	res := LintSealResult(sr, knownIDs)
	if sr == nil || view == nil {
		return res
	}
	inherited := domain.BuildInheritedConstraints(view, sr.Fingerprint.FilesTouched, sr.Fingerprint.Subsystems, sr.IntentID)
	res.Issues = append(res.Issues, LintInheritedAcknowledgement(&sr.Summary, inherited)...)
	res.Pass = !hasErrors(res.Issues)
	return res
}

// LintInheritedAcknowledgement walks the supplied inherited
// constraints and checks whether each is explicitly acknowledged
// in the seal's AcknowledgedConstraints field (by ConstraintID).
//
// v2 design: only high-severity constraints are inherited (enforced
// by BuildInheritedConstraints), and acknowledgement is explicit
// rather than guessed via token overlap.
//
// Legacy fallback: if the seal has NO AcknowledgedConstraints at all,
// fall back to the v1 token-overlap heuristic so old sealed intents
// don't regress in lint output.
//
// Pure, no I/O.
func LintInheritedAcknowledgement(summary *domain.IntentSummary, inherited []domain.InheritedConstraint) []LintIssue {
	if summary == nil || len(inherited) == 0 {
		return nil
	}
	useLegacy := len(summary.AcknowledgedConstraints) == 0
	var issues []LintIssue
	for i, ic := range inherited {
		// Explicit acknowledgement check (v2)
		if !useLegacy {
			ack := findExplicitAck(summary.AcknowledgedConstraints, ic.ConstraintID)
			if ack != nil {
				sev := "info"
				msg := fmt.Sprintf("inherited constraint %s acknowledged (%s): %s",
					ic.ConstraintID, ack.Disposition, briefWhat(ic.What))
				if ack.Disposition == "intentionally_changed" {
					msg = fmt.Sprintf("⚠️ inherited constraint %s intentionally changed — reviewer attention needed: %s",
						ic.ConstraintID, briefWhat(ic.What))
				}
				issues = append(issues, LintIssue{
					Code:     "inherited_anti_pattern_acknowledged",
					Severity: sev,
					Field:    fmt.Sprintf("summary.acknowledged_constraints[%d]", i),
					Message:  msg,
				})
				continue
			}
			// Not acknowledged → warning
			issues = append(issues, LintIssue{
				Code:     "inherited_anti_pattern_not_acknowledged",
				Severity: "warning",
				Field:    fmt.Sprintf("summary.inherited[%d]", i),
				Message: fmt.Sprintf("high-severity inherited constraint %s not acknowledged: %s — add to acknowledged_constraints with disposition (preserved/mitigated/not_applicable/intentionally_changed)",
					ic.ConstraintID, briefWhat(ic.What)),
			})
			continue
		}
		// Legacy path: token-overlap heuristic for old seals
		ack := domain.AcknowledgementOf(ic, summary)
		if ack != domain.AckNone {
			issues = append(issues, LintIssue{
				Code:     "inherited_anti_pattern_acknowledged",
				Severity: "info",
				Field:    fmt.Sprintf("summary.inherited[%d]", i),
				Message: fmt.Sprintf("inherited high-severity anti_pattern from %s acknowledged via %s: %s",
					ic.SourceIntent, ack, briefWhat(ic.What)),
			})
			continue
		}
		issues = append(issues, LintIssue{
			Code:     "inherited_anti_pattern_not_acknowledged",
			Severity: "warning",
			Field:    fmt.Sprintf("summary.inherited[%d]", i),
			Message: fmt.Sprintf("high-severity inherited anti_pattern from %s not acknowledged in this seal: %s — add to acknowledged_constraints",
				ic.SourceIntent, briefWhat(ic.What)),
		})
	}
	return issues
}

// findExplicitAck finds an AcknowledgedConstraint matching the given ID.
func findExplicitAck(acks []domain.AcknowledgedConstraint, constraintID string) *domain.AcknowledgedConstraint {
	for i := range acks {
		if acks[i].ConstraintID == constraintID {
			return &acks[i]
		}
	}
	return nil
}

func briefWhat(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 100 {
		return s
	}
	return s[:99] + "…"
}

// genericRiskPatterns catches boilerplate risks that provide no
// actionable information. A good risk names the specific failure
// mode, affected subsystem, and ideally mentions mitigation.
var genericRiskPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(there\s+)?(may|might|could)\s+(be\s+)?(a\s+)?bugs?\.?\s*$`),
	regexp.MustCompile(`(?i)^(may|might|could)\s+(affect|impact|break)\s+(users?|customers?|compatibility)\.?\s*$`),
	regexp.MustCompile(`(?i)^(possible|potential)\s+(breaking\s+change|regression|issue|bug)s?\.?\s*$`),
	regexp.MustCompile(`(?i)^unknown\s+(risk|impact|consequences?)\.?\s*$`),
	regexp.MustCompile(`(?i)^needs?\s+(more\s+)?testing\.?\s*$`),
	regexp.MustCompile(`(?i)^not\s+(fully\s+)?tested\.?\s*$`),
	regexp.MustCompile(`(?i)^(could|may|might)\s+have\s+(unintended\s+)?(side\s+)?effects?\.?\s*$`),
}

// isGenericRisk returns true if the risk text is too vague to be
// useful for retrieval or reviewer guidance.
func isGenericRisk(risk string) bool {
	risk = strings.TrimSpace(risk)
	// Too short to be meaningful (under 15 chars)
	if len(risk) < 15 {
		return true
	}
	for _, re := range genericRiskPatterns {
		if re.MatchString(risk) {
			return true
		}
	}
	return false
}

func truncate(s string) string {
	s = strings.TrimSpace(s)
	const n = 60
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// --- v0.4 risk noise detection patterns ---

// acceptableTradeoffPatterns match risk text that describes an accepted
// trade-off rather than a live hazard. These belong in decisions.
var acceptableTradeoffPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)可接受`),
	regexp.MustCompile(`(?i)\bacceptable\b`),
	regexp.MustCompile(`(?i)intended\s+trade-?off`),
	regexp.MustCompile(`(?i)有意取舍`),
	regexp.MustCompile(`(?i)\bno\s+concern\b`),
	regexp.MustCompile(`(?i)trade-?off\s+accepted`),
}

func isAcceptableTradeoff(risk string) bool {
	for _, re := range acceptableTradeoffPatterns {
		if re.MatchString(risk) {
			return true
		}
	}
	return false
}

// followupPatterns match risk text that is actually a follow-up item.
var followupPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)后续[可要如果]`),
	regexp.MustCompile(`(?i)后续可加`),
	regexp.MustCompile(`(?i)可能需要`),
	regexp.MustCompile(`(?i)再[加做调]`),
	regexp.MustCompile(`(?i)\bfollow-?up\b`),
	regexp.MustCompile(`(?i)\blater\b.*\b(add|do|adjust|implement)\b`),
	regexp.MustCompile(`(?i)\bfuture\s+enhancement\b`),
	regexp.MustCompile(`(?i)需要后续`),
}

func isFollowup(risk string) bool {
	for _, re := range followupPatterns {
		if re.MatchString(risk) {
			return true
		}
	}
	return false
}

// reviewGuidancePatterns match risk text that is reviewer guidance.
var reviewGuidancePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^reviewers?\b`),
	regexp.MustCompile(`(?i)^评审`),
	regexp.MustCompile(`(?i)^本(分支|次)\s*(fingerprint|修改|改动)`),
	regexp.MustCompile(`(?i)^review\s+focus\b`),
}

func isReviewGuidance(risk string) bool {
	risk = strings.TrimSpace(risk)
	for _, re := range reviewGuidancePatterns {
		if re.MatchString(risk) {
			return true
		}
	}
	return false
}

// antiPatternPatterns match risk text with rule-like language that
// suggests the item should be an anti_pattern, not a risk.
var antiPatternPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bmust\s+not\b`),
	regexp.MustCompile(`(?i)\bdo\s+not\b`),
	regexp.MustCompile(`(?i)\bnever\b`),
	regexp.MustCompile(`(?i)\bdiscipline\b`),
	regexp.MustCompile(`(?i)\banti-?pattern\b`),
	regexp.MustCompile(`(?i)不[可能]以`),
	regexp.MustCompile(`(?i)禁止`),
}

func looksLikeAntiPattern(risk string) bool {
	for _, re := range antiPatternPatterns {
		if re.MatchString(risk) {
			return true
		}
	}
	return false
}
