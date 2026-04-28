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
// Lint is *advisory* — it does not block seal --submit. The spec
// reserves enforcement for a future hook ("hooks may soft-remind,
// not hard-block"). Today lint runs on demand:
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
				Field: fmt.Sprintf("summary.decisions[%d].chose", i),
				Message: fmt.Sprintf("decisions[%d].chose is empty", i),
			})
			continue
		}
		if len(d.Chose) > rationaleThreshold && strings.TrimSpace(d.Rationale) == "" {
			out.Issues = append(out.Issues, LintIssue{
				Code: "decision_no_rationale", Severity: "warning",
				Field: fmt.Sprintf("summary.decisions[%d].rationale", i),
				Message: fmt.Sprintf("decisions[%d].chose is %d chars but no rationale recorded; longer choices should explain why", i, len(d.Chose)),
			})
		}
	}

	if len(summary.Risks) == 0 && len(summary.AntiPatterns) == 0 {
		out.Issues = append(out.Issues, LintIssue{
			Code: "no_constraints", Severity: "warning",
			Field:   "summary.risks",
			Message: "no risks or anti_patterns recorded; if the change truly carries no future-agent constraints, this is fine — but most non-trivial work has at least one",
		})
	}

	if fingerprint != nil {
		if len(fingerprint.Subsystems) == 0 {
			out.Issues = append(out.Issues, LintIssue{
				Code: "fingerprint_no_subsystems", Severity: "error",
				Field: "fingerprint.subsystems",
				Message: "fingerprint.subsystems is empty; phase-1 conflict detection cannot match",
			})
		}
		if len(fingerprint.FilesTouched) == 0 {
			out.Issues = append(out.Issues, LintIssue{
				Code: "fingerprint_no_files", Severity: "error",
				Field: "fingerprint.files_touched",
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
// SealResult before submit (when the agent runs `mainline lint`
// piped from `mainline seal --prepare`'s schema). The seal payload
// path is what `mainline lint` will be wired into for pre-submit
// checks once the seal-pipeline integration lands.
func LintSealResult(sr *domain.SealResult, viewIntents map[string]bool) LintResult {
	if sr == nil {
		return LintResult{Pass: false, Issues: []LintIssue{{
			Code: "missing_seal_result", Severity: "error",
			Message: "seal result is nil",
		}}}
	}
	return LintIntent(sr.IntentID, &sr.Summary, &sr.Fingerprint, "", viewIntents)
}
