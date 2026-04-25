package engine

import (
	"testing"

	"mainline/internal/domain"
)

func BenchmarkFingerprintOverlap(b *testing.B) {
	a := &domain.SemanticFingerprint{
		Subsystems:          []string{"auth", "db", "api"},
		FilesTouched:        []string{"auth.go", "db.go", "api.go", "config.go"},
		ArchitecturalClaims: []string{"JWT-based auth", "middleware pattern"},
		BehavioralChanges:   []string{"login flow", "session management"},
		Tags:                []string{"security", "feature"},
		APIChanges: []domain.APIChange{
			{Kind: "added", Surface: "http", Signature: "POST /auth/login"},
			{Kind: "added", Surface: "http", Signature: "POST /auth/logout"},
		},
	}
	bb := &domain.SemanticFingerprint{
		Subsystems:          []string{"auth", "middleware", "config"},
		FilesTouched:        []string{"auth.go", "middleware.go", "config.go"},
		ArchitecturalClaims: []string{"JWT-based auth", "rate limiting"},
		BehavioralChanges:   []string{"login flow", "rate limiting"},
		Tags:                []string{"security", "performance"},
		APIChanges: []domain.APIChange{
			{Kind: "added", Surface: "http", Signature: "POST /auth/login"},
			{Kind: "modified", Surface: "http", Signature: "GET /api/users"},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FingerprintOverlap(a, bb)
	}
}

func BenchmarkFingerprintOverlapLarge(b *testing.B) {
	files := make([]string, 50)
	for i := range files {
		files[i] = "file_" + string(rune('a'+i%26)) + ".go"
	}
	a := &domain.SemanticFingerprint{
		Subsystems:   []string{"auth", "db", "api", "ui", "config", "sync", "check", "core"},
		FilesTouched: files,
		Tags:         []string{"security", "feature", "performance", "refactor"},
	}
	bb := &domain.SemanticFingerprint{
		Subsystems:   []string{"db", "api", "config", "migration", "sync"},
		FilesTouched: files[:30],
		Tags:         []string{"feature", "migration", "breaking"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FingerprintOverlap(a, bb)
	}
}

func BenchmarkJaccard(b *testing.B) {
	a := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	bb := []string{"c", "d", "e", "f", "g", "h", "i", "j"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		jaccard(a, bb)
	}
}

func BenchmarkJaccardLarge(b *testing.B) {
	a := make([]string, 100)
	bb := make([]string, 100)
	for i := 0; i < 100; i++ {
		a[i] = "file_" + string(rune('a'+i%26)) + ".go"
		bb[i] = "file_" + string(rune('a'+(i+10)%26)) + ".go"
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		jaccard(a, bb)
	}
}
