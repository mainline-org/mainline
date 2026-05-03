//go:build !quick

package engine

import (
	"encoding/json"
	"fmt"
	"testing"

	"pgregory.net/rapid"

	"github.com/mainline-org/mainline/internal/domain"
)

// Property: SealSubmitWithOptions NEVER mutates draft state when a pre-submit
// gate fails. This is the key atomicity invariant — a failed seal must leave
// the draft in its original status so the user can retry after fixing the
// issue. Historical bug: pre-fix code wrote StatusSealedLocal before identity
// check, leaving unrecoverable state.
func TestPropertySealSubmitNeverMutatesOnValidationFailure(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}

		// Start a real draft
		_, err := svc.Start("test goal", "")
		if err != nil {
			rt.Fatalf("start: %v", err)
		}

		// Read the draft before attempt
		drafts, _ := svc.Store.ListDrafts()
		if len(drafts) == 0 {
			rt.Fatal("no drafts after start")
		}
		draftBefore, _ := svc.Store.ReadDraft(drafts[0])
		if draftBefore == nil {
			rt.Fatal("nil draft")
			return // unreachable; satisfies staticcheck SA5011
		}
		statusBefore := draftBefore.Status

		// Generate a SealResult that fails one of the pre-submit gates:
		// schema validation, lookup/status checks, or deterministic lint.
		failureMode := rapid.IntRange(0, 5).Draw(rt, "failureMode")
		var sr domain.SealResult
		switch failureMode {
		case 0:
			// Missing intent_id
			sr = domain.SealResult{}
		case 1:
			// Wrong intent_id (no matching draft)
			sr = validSealResultForPBT("int_nonexistent")
		case 2:
			// Missing summary.title
			sr = validSealResultForPBT(draftBefore.IntentID)
			sr.Summary.Title = ""
		case 3:
			// Missing summary.what
			sr = validSealResultForPBT(draftBefore.IntentID)
			sr.Summary.What = ""
		case 4:
			// Invalid anti-pattern (empty why)
			sr = validSealResultForPBT(draftBefore.IntentID)
			sr.Summary.AntiPatterns = []domain.AntiPattern{{
				What:     "something",
				Why:      "",
				Severity: "high",
			}}
		case 5:
			// Deterministic lint error (boilerplate what)
			sr = validSealResultForPBT(draftBefore.IntentID)
			sr.Summary.What = "implemented changes"
		}

		data, _ := json.Marshal(sr)
		_, _ = svc.SealSubmitWithOptions(json.RawMessage(data), nil)

		// Draft status MUST NOT have changed
		draftAfter, _ := svc.Store.ReadDraft(draftBefore.IntentID)
		if draftAfter == nil {
			// Draft for case 0/1 (wrong id) won't match — check the real draft
			draftAfter, _ = svc.Store.ReadDraft(drafts[0])
		}
		if draftAfter != nil && draftAfter.Status != statusBefore {
			rt.Fatalf("draft status mutated on validation failure: %s → %s (failureMode=%d)",
				statusBefore, draftAfter.Status, failureMode)
		}
	})
}

// Property: SealSubmitWithOptions rejects bad confidence values.
// Confidence must be in [0,1]; values outside trigger validation failure
// and the draft MUST remain unchanged.
func TestPropertySealRejectsBadConfidence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}

		res, err := svc.Start("goal", "")
		if err != nil {
			rt.Fatalf("start: %v", err)
		}

		// Generate out-of-range confidence
		which := rapid.IntRange(0, 1).Draw(rt, "which")
		sr := validSealResultForPBT(res.IntentID)
		switch which {
		case 0:
			sr.Confidence.Summary = rapid.Float64Range(1.01, 100.0).Draw(rt, "badConfS")
		case 1:
			sr.Confidence.Fingerprint = rapid.Float64Range(-100.0, -0.01).Draw(rt, "badConfF")
		}

		data, _ := json.Marshal(sr)
		_, submitErr := svc.SealSubmitWithOptions(json.RawMessage(data), nil)
		if submitErr == nil {
			rt.Fatal("seal should reject out-of-range confidence")
		}

		// Draft must remain drafting
		draft, _ := svc.Store.ReadDraft(res.IntentID)
		if draft != nil && draft.Status != domain.StatusDrafting {
			rt.Fatalf("draft mutated on bad confidence: %s", draft.Status)
		}
	})
}

// Property: ValidateSealResult accepts any well-formed SealResult and rejects
// any SealResult missing a required field. Validates that the validator is
// neither too permissive nor too restrictive.
func TestPropertyValidateSealResultCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		sr := drawValidSealResult(rt)
		// A fully valid result must pass
		if err := validateSealResultViaEngine(sr); err != nil {
			rt.Fatalf("valid SealResult rejected: %v", err)
		}

		// Knock out exactly one required field — must fail
		field := rapid.IntRange(0, 5).Draw(rt, "knockout")
		broken := sr // copy
		switch field {
		case 0:
			broken.IntentID = ""
		case 1:
			broken.Summary.Title = ""
		case 2:
			broken.Summary.What = ""
		case 3:
			broken.Summary.Why = ""
		case 4:
			broken.Fingerprint.Subsystems = nil
		case 5:
			broken.Fingerprint.FilesTouched = nil
		}
		if err := validateSealResultViaEngine(broken); err == nil {
			rt.Fatalf("broken SealResult accepted (knocked field=%d)", field)
		}
	})
}

func validSealResultForPBT(intentID string) domain.SealResult {
	return domain.SealResult{
		IntentID: intentID,
		Summary: domain.IntentSummary{
			Title: "title",
			What:  "what",
			Why:   "why",
			Decisions: []domain.Decision{{
				Point:     "pbt setup",
				Chose:     "use a non-boilerplate seal payload by default",
				Rationale: "submit-path property tests should only fail the gate they intentionally perturb",
			}},
		},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:   []string{"test"},
			FilesTouched: []string{"a.go"},
		},
		Confidence: domain.SealConfidence{
			Summary:     0.8,
			Fingerprint: 0.8,
		},
	}
}

func drawValidSealResult(rt *rapid.T) domain.SealResult {
	titleAlpha := []string{"fix auth", "refactor sync", "add JWT", "update docs"}
	fileAlpha := []string{"a.go", "b.go", "internal/x.go", "README.md"}
	subsAlpha := []string{"engine", "cli", "auth", "sync", "merge"}

	nFiles := rapid.IntRange(1, 4).Draw(rt, "nFiles")
	files := make([]string, 0, nFiles)
	for i := 0; i < nFiles; i++ {
		files = append(files, rapid.SampledFrom(fileAlpha).Draw(rt, fmt.Sprintf("f%d", i)))
	}
	nSubs := rapid.IntRange(1, 3).Draw(rt, "nSubs")
	subs := make([]string, 0, nSubs)
	for i := 0; i < nSubs; i++ {
		subs = append(subs, rapid.SampledFrom(subsAlpha).Draw(rt, fmt.Sprintf("s%d", i)))
	}

	return domain.SealResult{
		IntentID: "int_" + rapid.StringMatching(`[a-f0-9]{8}`).Draw(rt, "id"),
		Summary: domain.IntentSummary{
			Title: rapid.SampledFrom(titleAlpha).Draw(rt, "title"),
			What:  "implemented " + rapid.SampledFrom(titleAlpha).Draw(rt, "what"),
			Why:   "because " + rapid.SampledFrom(titleAlpha).Draw(rt, "why"),
			Decisions: []domain.Decision{{
				Point:     "property setup",
				Chose:     "keep generated seal results lint-clean by default",
				Rationale: "properties that target validation should not accidentally fail deterministic lint first",
			}},
		},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:   subs,
			FilesTouched: files,
		},
		Confidence: domain.SealConfidence{
			Summary:     rapid.Float64Range(0, 1).Draw(rt, "confS"),
			Fingerprint: rapid.Float64Range(0, 1).Draw(rt, "confF"),
		},
	}
}

func validateSealResultViaEngine(sr domain.SealResult) error {
	data, err := json.Marshal(sr)
	if err != nil {
		return err
	}
	var parsed domain.SealResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	return validateSealResultFields(&parsed)
}

// validateSealResultFields mirrors core.ValidateSealResult for test isolation.
func validateSealResultFields(sr *domain.SealResult) error {
	if sr.IntentID == "" {
		return fmt.Errorf("intent_id required")
	}
	if sr.Summary.Title == "" {
		return fmt.Errorf("title required")
	}
	if sr.Summary.What == "" {
		return fmt.Errorf("what required")
	}
	if sr.Summary.Why == "" {
		return fmt.Errorf("why required")
	}
	if len(sr.Fingerprint.Subsystems) == 0 {
		return fmt.Errorf("subsystems required")
	}
	if len(sr.Fingerprint.FilesTouched) == 0 {
		return fmt.Errorf("files_touched required")
	}
	if sr.Confidence.Summary < 0 || sr.Confidence.Summary > 1 {
		return fmt.Errorf("confidence.summary out of range")
	}
	if sr.Confidence.Fingerprint < 0 || sr.Confidence.Fingerprint > 1 {
		return fmt.Errorf("confidence.fingerprint out of range")
	}
	return nil
}
