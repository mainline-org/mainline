package core

import (
	"testing"

	"mainline/internal/domain"
)

func BenchmarkGenerateIntentID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GenerateIntentID()
	}
}

func BenchmarkGenerateEventID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GenerateEventID()
	}
}

func BenchmarkGenerateActorID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GenerateActorID()
	}
}

func BenchmarkCanonicalJSONSmall(b *testing.B) {
	obj := map[string]interface{}{
		"intent_id": "int_12345678",
		"status":    "drafting",
		"goal":      "implement feature X",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CanonicalJSON(obj)
	}
}

func BenchmarkCanonicalJSONLarge(b *testing.B) {
	obj := map[string]interface{}{
		"intent_id": "int_12345678",
		"summary": map[string]interface{}{
			"title": "Add authentication",
			"what":  "JWT-based auth with refresh tokens",
			"why":   "Security requirement",
			"decisions": []interface{}{
				map[string]interface{}{
					"point":     "Token storage",
					"chose":     "HttpOnly cookies",
					"rationale": "XSS protection",
				},
			},
		},
		"fingerprint": map[string]interface{}{
			"subsystems":   []interface{}{"auth", "middleware", "db"},
			"files_touched": []interface{}{"auth.go", "middleware.go", "db/users.go", "config.go"},
			"tags":          []interface{}{"security", "feature", "breaking-change"},
			"api_changes": []interface{}{
				map[string]interface{}{
					"kind":          "added",
					"surface":       "http",
					"signature":     "POST /api/v1/auth/login",
					"compatibility": "compatible",
				},
			},
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CanonicalJSON(obj)
	}
}

func BenchmarkCanonicalHashSmall(b *testing.B) {
	obj := map[string]interface{}{"hello": "world"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CanonicalHash(obj)
	}
}

func BenchmarkCanonicalHashLarge(b *testing.B) {
	obj := map[string]interface{}{
		"intent_id": "int_12345678",
		"summary": map[string]interface{}{
			"title": "title", "what": "what", "why": "why",
		},
		"fingerprint": map[string]interface{}{
			"subsystems":   []interface{}{"a", "b", "c", "d", "e"},
			"files_touched": []interface{}{"a.go", "b.go", "c.go", "d.go"},
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CanonicalHash(obj)
	}
}

func BenchmarkValidateStateTransition(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ValidateStateTransition(domain.StatusDrafting, domain.StatusSealedLocal)
	}
}

func BenchmarkValidateSealResult(b *testing.B) {
	sr := &domain.SealResult{
		IntentID: "int_12345678",
		Summary: domain.IntentSummary{
			Title: "title", What: "what", Why: "why",
		},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:   []string{"auth"},
			FilesTouched: []string{"auth.go"},
		},
		Confidence: domain.SealConfidence{Summary: 0.9, Fingerprint: 0.8},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ValidateSealResult(sr)
	}
}
