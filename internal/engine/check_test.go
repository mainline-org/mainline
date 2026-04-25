package engine

import (
	"testing"

	"mainline/internal/domain"
)

func TestJaccard(t *testing.T) {
	tests := []struct {
		a, b   []string
		expect float64
	}{
		{nil, nil, 0},
		{[]string{"a"}, []string{"a"}, 1},
		{[]string{"a"}, []string{"b"}, 0},
		{[]string{"a", "b"}, []string{"b", "c"}, 1.0 / 3.0},
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}, 1},
	}
	for i, tc := range tests {
		got := jaccard(tc.a, tc.b)
		if diff := got - tc.expect; diff > 0.001 || diff < -0.001 {
			t.Errorf("case %d: expected %f, got %f", i, tc.expect, got)
		}
	}
}

func TestJaccardSymmetry(t *testing.T) {
	a := []string{"x", "y", "z"}
	b := []string{"y", "z", "w"}
	if jaccard(a, b) != jaccard(b, a) {
		t.Error("jaccard should be symmetric")
	}
}

func TestCheckPrepareRequiresInit(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	_, err := svc.CheckPrepare("int_12345678")
	if err == nil {
		t.Error("check prepare should fail without init")
	}
}

func TestFingerprintOverlapEmptyVsFull(t *testing.T) {
	empty := &domain.SemanticFingerprint{}
	full := &domain.SemanticFingerprint{
		Subsystems:   []string{"auth", "db"},
		FilesTouched: []string{"a.go"},
		Tags:         []string{"security"},
	}
	score := FingerprintOverlap(empty, full)
	if score != 0 {
		t.Errorf("empty vs full should be 0, got %f", score)
	}
}
