package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mainline-org/mainline/internal/domain"
)

// randID8 returns 8 hex chars of crypto-secure randomness. Panics on
// rand.Reader failure — for ID generation, silently producing all-zero
// bytes would corrupt the audit trail (every "new" intent would
// collide on a single id), so loud failure is the right semantic.
func randID8() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// GenerateIntentID returns a new intent ID of the form "int_<8 hex chars>".
func GenerateIntentID() string { return "int_" + randID8() }

// GenerateTurnID returns a new turn ID of the form "turn_<8 hex chars>".
func GenerateTurnID() string { return "turn_" + randID8() }

// GenerateEventID returns a new event ID of the form "evt_<8 hex chars>".
func GenerateEventID() string { return "evt_" + randID8() }

// GenerateActorID returns a new actor ID of the form "actor_<8 hex chars>".
func GenerateActorID() string { return "actor_" + randID8() }

// Now returns the current UTC time as an ISO 8601 string.
func Now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// CanonicalJSON serializes v to canonical JSON (sorted keys, no extra whitespace).
func CanonicalJSON(v interface{}) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return canonicalize(raw)
}

func canonicalize(v interface{}) ([]byte, error) {
	switch val := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var buf strings.Builder
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			vb, err := canonicalize(val[k])
			if err != nil {
				return nil, err
			}
			buf.Write(vb)
		}
		buf.WriteByte('}')
		return []byte(buf.String()), nil
	case []interface{}:
		var buf strings.Builder
		buf.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			ib, err := canonicalize(item)
			if err != nil {
				return nil, err
			}
			buf.Write(ib)
		}
		buf.WriteByte(']')
		return []byte(buf.String()), nil
	default:
		return json.Marshal(val)
	}
}

// CanonicalHash returns the SHA-256 hex digest of the canonical JSON of v.
func CanonicalHash(v interface{}) (string, error) {
	data, err := CanonicalJSON(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ValidateStateTransition checks if a status transition is allowed.
func ValidateStateTransition(from, to domain.IntentStatus) error {
	allowed := map[domain.IntentStatus][]domain.IntentStatus{
		domain.StatusDrafting:    {domain.StatusSealedLocal, domain.StatusAbandoned, domain.StatusSuperseded},
		domain.StatusSealedLocal: {domain.StatusProposed, domain.StatusAbandoned, domain.StatusSuperseded},
		domain.StatusProposed:    {domain.StatusMerged, domain.StatusAbandoned, domain.StatusSuperseded},
		domain.StatusMerged:      {domain.StatusReverted},
	}
	targets, ok := allowed[from]
	if !ok {
		return fmt.Errorf("no transitions from status %q", from)
	}
	for _, t := range targets {
		if t == to {
			return nil
		}
	}
	return fmt.Errorf("invalid transition: %s -> %s", from, to)
}

// ValidateSealResult checks that a SealResult has all required fields.
func ValidateSealResult(sr *domain.SealResult) error {
	if sr.IntentID == "" {
		return fmt.Errorf("seal_result: intent_id is required")
	}
	if sr.Summary.Title == "" {
		return fmt.Errorf("seal_result: summary.title is required")
	}
	if sr.Summary.What == "" {
		return fmt.Errorf("seal_result: summary.what is required")
	}
	if sr.Summary.Why == "" {
		return fmt.Errorf("seal_result: summary.why is required")
	}
	if len(sr.Fingerprint.Subsystems) == 0 {
		return fmt.Errorf("seal_result: fingerprint.subsystems must not be empty")
	}
	if len(sr.Fingerprint.FilesTouched) == 0 {
		return fmt.Errorf("seal_result: fingerprint.files_touched must not be empty")
	}
	if sr.Confidence.Summary < 0 || sr.Confidence.Summary > 1 {
		return fmt.Errorf("seal_result: confidence.summary must be in [0,1]")
	}
	if sr.Confidence.Fingerprint < 0 || sr.Confidence.Fingerprint > 1 {
		return fmt.Errorf("seal_result: confidence.fingerprint must be in [0,1]")
	}
	for i, ap := range sr.Summary.AntiPatterns {
		if strings.TrimSpace(ap.What) == "" {
			return fmt.Errorf("seal_result: summary.anti_patterns[%d].what is required", i)
		}
		if strings.TrimSpace(ap.Why) == "" {
			return fmt.Errorf("seal_result: summary.anti_patterns[%d].why is required for legacy anti-pattern records", i)
		}
		switch ap.Severity {
		case "", "low", "medium", "high":
		default:
			return fmt.Errorf("seal_result: summary.anti_patterns[%d].severity must be low|medium|high (got %q)", i, ap.Severity)
		}
	}
	return nil
}

// ValidateCheckJudgmentResult validates a check judgment result.
func ValidateCheckJudgmentResult(cr *domain.CheckJudgmentResult) error {
	if cr.CandidateIntent == "" {
		return fmt.Errorf("check_judgment: candidate_intent is required")
	}
	for i, j := range cr.Judgments {
		if j.TaskID == "" {
			return fmt.Errorf("check_judgment: judgments[%d].task_id is required", i)
		}
		if j.Severity == "" {
			return fmt.Errorf("check_judgment: judgments[%d].severity is required", i)
		}
		validSeverity := j.Severity == "low" || j.Severity == "medium" || j.Severity == "high"
		if j.HasConflict && !validSeverity {
			return fmt.Errorf("check_judgment: judgments[%d].severity must be low|medium|high", i)
		}
		if j.Confidence < 0 || j.Confidence > 1 {
			return fmt.Errorf("check_judgment: judgments[%d].confidence must be in [0,1]", i)
		}
	}
	return nil
}
