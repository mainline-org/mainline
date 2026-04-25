package core

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"

	"mainline/internal/domain"
)

// -----------------------------------------------------------
// Unit tests
// -----------------------------------------------------------

func TestGenerateIntentID(t *testing.T) {
	id := GenerateIntentID()
	if !strings.HasPrefix(id, "int_") {
		t.Errorf("intent ID should start with 'int_', got %q", id)
	}
	if len(id) != 12 { // "int_" + 8 hex chars
		t.Errorf("intent ID should be 12 chars, got %d: %q", len(id), id)
	}
}

func TestGenerateEventID(t *testing.T) {
	id := GenerateEventID()
	if !strings.HasPrefix(id, "evt_") {
		t.Errorf("event ID should start with 'evt_', got %q", id)
	}
}

func TestGenerateActorID(t *testing.T) {
	id := GenerateActorID()
	if !strings.HasPrefix(id, "actor_") {
		t.Errorf("actor ID should start with 'actor_', got %q", id)
	}
}

func TestIDsAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := GenerateIntentID()
		if seen[id] {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		seen[id] = true
	}
}

func TestCanonicalJSON(t *testing.T) {
	// Keys should be sorted
	input := map[string]interface{}{
		"z": 1,
		"a": 2,
		"m": 3,
	}
	data, err := CanonicalJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"a":2,"m":3,"z":1}`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}
}

func TestCanonicalJSONNested(t *testing.T) {
	input := map[string]interface{}{
		"b": map[string]interface{}{
			"d": 1,
			"c": 2,
		},
		"a": []interface{}{3, 2, 1},
	}
	data, err := CanonicalJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"a":[3,2,1],"b":{"c":2,"d":1}}`
	if string(data) != expected {
		t.Errorf("expected %s, got %s", expected, string(data))
	}
}

func TestCanonicalHashDeterministic(t *testing.T) {
	input := map[string]interface{}{"hello": "world", "foo": 42}
	h1, err := CanonicalHash(input)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := CanonicalHash(input)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("hashes should be deterministic: %s != %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("sha256 hex should be 64 chars, got %d", len(h1))
	}
}

func TestCanonicalHashOrderIndependent(t *testing.T) {
	a := map[string]interface{}{"x": 1, "y": 2, "z": 3}
	b := map[string]interface{}{"z": 3, "x": 1, "y": 2}
	ha, _ := CanonicalHash(a)
	hb, _ := CanonicalHash(b)
	if ha != hb {
		t.Error("canonical hash should be key-order independent")
	}
}

func TestValidateStateTransition(t *testing.T) {
	tests := []struct {
		from    domain.IntentStatus
		to      domain.IntentStatus
		wantErr bool
	}{
		{domain.StatusDrafting, domain.StatusSealedLocal, false},
		{domain.StatusDrafting, domain.StatusAbandoned, false},
		{domain.StatusDrafting, domain.StatusSuperseded, false},
		{domain.StatusSealedLocal, domain.StatusProposed, false},
		{domain.StatusSealedLocal, domain.StatusAbandoned, false},
		{domain.StatusProposed, domain.StatusMerged, false},
		{domain.StatusMerged, domain.StatusReverted, false},

		// Invalid transitions
		{domain.StatusDrafting, domain.StatusMerged, true},
		{domain.StatusDrafting, domain.StatusProposed, true},
		{domain.StatusMerged, domain.StatusDrafting, true},
		{domain.StatusAbandoned, domain.StatusDrafting, true},
	}

	for _, tc := range tests {
		err := ValidateStateTransition(tc.from, tc.to)
		if (err != nil) != tc.wantErr {
			t.Errorf("transition %s->%s: wantErr=%v, got err=%v", tc.from, tc.to, tc.wantErr, err)
		}
	}
}

func TestValidateSealResult(t *testing.T) {
	valid := &domain.SealResult{
		IntentID: "int_abc12345",
		Summary: domain.IntentSummary{
			Title: "Add feature X",
			What:  "Adds feature X to module Y",
			Why:   "Users need X",
		},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:   []string{"auth"},
			FilesTouched: []string{"auth.go"},
		},
		Confidence: domain.SealConfidence{Summary: 0.9, Fingerprint: 0.8},
	}
	if err := ValidateSealResult(valid); err != nil {
		t.Errorf("expected valid, got error: %v", err)
	}

	// Missing title
	invalid := *valid
	invalid.Summary.Title = ""
	if err := ValidateSealResult(&invalid); err == nil {
		t.Error("expected error for missing title")
	}

	// Empty subsystems
	invalid2 := *valid
	invalid2.Fingerprint.Subsystems = nil
	if err := ValidateSealResult(&invalid2); err == nil {
		t.Error("expected error for empty subsystems")
	}

	// Confidence out of range
	invalid3 := *valid
	invalid3.Confidence.Summary = 1.5
	if err := ValidateSealResult(&invalid3); err == nil {
		t.Error("expected error for confidence > 1")
	}
}

func TestValidateCheckJudgmentResult(t *testing.T) {
	valid := &domain.CheckJudgmentResult{
		CandidateIntent: "int_abc12345",
		Judgments: []domain.ConflictJudgment{
			{
				TaskID:      "task_1",
				HasConflict: false,
				Severity:    "low",
				Confidence:  0.9,
				Explanation: "No conflict",
			},
		},
		Overall: domain.CheckOverall{
			HasConflict:     false,
			HighestSeverity: "none",
		},
	}
	if err := ValidateCheckJudgmentResult(valid); err != nil {
		t.Errorf("expected valid, got error: %v", err)
	}
}

// -----------------------------------------------------------
// Property-based tests
// -----------------------------------------------------------

func TestPropertyCanonicalJSONIdempotent(t *testing.T) {
	// Property: canonical(canonical(x)) == canonical(x)
	for i := 0; i < 100; i++ {
		obj := randomObject(rand.Intn(5))
		first, err := CanonicalJSON(obj)
		if err != nil {
			t.Fatal(err)
		}
		var parsed interface{}
		json.Unmarshal(first, &parsed)
		second, err := CanonicalJSON(parsed)
		if err != nil {
			t.Fatal(err)
		}
		if string(first) != string(second) {
			t.Errorf("canonical JSON not idempotent:\n  first:  %s\n  second: %s", first, second)
		}
	}
}

func TestPropertyCanonicalHashDeterministic(t *testing.T) {
	// Property: hash(x) == hash(x) always
	for i := 0; i < 100; i++ {
		obj := randomObject(rand.Intn(5))
		h1, _ := CanonicalHash(obj)
		h2, _ := CanonicalHash(obj)
		if h1 != h2 {
			t.Error("hash should be deterministic")
		}
	}
}

func TestPropertyCanonicalHashKeyOrderIndependent(t *testing.T) {
	// Property: changing key insertion order doesn't change hash
	for i := 0; i < 50; i++ {
		keys := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
		val := rand.Intn(1000)

		a := make(map[string]interface{})
		b := make(map[string]interface{})
		for _, k := range keys {
			a[k] = val
		}
		// Reverse order
		for j := len(keys) - 1; j >= 0; j-- {
			b[keys[j]] = val
		}

		ha, _ := CanonicalHash(a)
		hb, _ := CanonicalHash(b)
		if ha != hb {
			t.Error("hash should be key-order independent")
		}
	}
}

func TestPropertyStateTransitionsAsymmetric(t *testing.T) {
	// Property: if A->B is valid, B->A is not necessarily valid
	states := []domain.IntentStatus{
		domain.StatusDrafting, domain.StatusSealedLocal,
		domain.StatusProposed, domain.StatusMerged,
		domain.StatusAbandoned, domain.StatusSuperseded,
	}
	for _, a := range states {
		for _, b := range states {
			if a == b {
				continue
			}
			fwd := ValidateStateTransition(a, b)
			bwd := ValidateStateTransition(b, a)
			// At least one direction should be invalid for terminal states
			if a == domain.StatusAbandoned || a == domain.StatusSuperseded || a == domain.StatusReverted {
				if fwd == nil {
					t.Errorf("terminal state %s should not transition to %s", a, b)
				}
			}
			_ = bwd // just ensuring no panic
		}
	}
}

func TestPropertyAllIntentIDsHavePrefix(t *testing.T) {
	for i := 0; i < 500; i++ {
		id := GenerateIntentID()
		if !strings.HasPrefix(id, "int_") {
			t.Fatalf("generated ID without prefix: %s", id)
		}
	}
}

func TestPropertySealValidationRejectsEmptyFields(t *testing.T) {
	// Property: a SealResult with any required field empty should fail
	base := domain.SealResult{
		IntentID: "int_12345678",
		Summary: domain.IntentSummary{
			Title: "title", What: "what", Why: "why",
		},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:   []string{"s"},
			FilesTouched: []string{"f"},
		},
		Confidence: domain.SealConfidence{Summary: 0.5, Fingerprint: 0.5},
	}

	// Should pass as-is
	if err := ValidateSealResult(&base); err != nil {
		t.Fatalf("base should be valid: %v", err)
	}

	// Blank each required field
	fields := []func(*domain.SealResult){
		func(sr *domain.SealResult) { sr.IntentID = "" },
		func(sr *domain.SealResult) { sr.Summary.Title = "" },
		func(sr *domain.SealResult) { sr.Summary.What = "" },
		func(sr *domain.SealResult) { sr.Summary.Why = "" },
		func(sr *domain.SealResult) { sr.Fingerprint.Subsystems = nil },
		func(sr *domain.SealResult) { sr.Fingerprint.FilesTouched = nil },
	}
	for i, blank := range fields {
		cp := base
		blank(&cp)
		if err := ValidateSealResult(&cp); err == nil {
			t.Errorf("field %d: expected validation error when blanked", i)
		}
	}
}

// randomObject generates a random JSON-like object for property testing.
func randomObject(depth int) map[string]interface{} {
	obj := make(map[string]interface{})
	n := rand.Intn(5) + 1
	for i := 0; i < n; i++ {
		key := randomString(rand.Intn(8) + 1)
		if depth > 0 && rand.Float64() < 0.3 {
			obj[key] = randomObject(depth - 1)
		} else if rand.Float64() < 0.3 {
			arr := make([]interface{}, rand.Intn(4))
			for j := range arr {
				arr[j] = rand.Intn(100)
			}
			obj[key] = arr
		} else {
			obj[key] = rand.Intn(1000)
		}
	}
	return obj
}

func randomString(n int) string {
	letters := "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
