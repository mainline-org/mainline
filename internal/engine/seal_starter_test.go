package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestBuildSealStarterJSONAvoidsAmbiguousArrayShapes(t *testing.T) {
	starter := buildSealStarter("int_abc123", "seal schema hardening", nil)

	data, err := json.Marshal(starter)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(data)

	if strings.Contains(encoded, "null") {
		t.Fatalf("starter should render empty arrays instead of nulls: %s", encoded)
	}
	if !strings.Contains(encoded, `"decisions":[{"point":"","chose":""}]`) {
		t.Fatalf("starter should show summary.decisions as an object array, got: %s", encoded)
	}
	if !strings.Contains(encoded, `"review_notes":[]`) {
		t.Fatalf("starter should show summary.review_notes as a string array, got: %s", encoded)
	}
	for _, want := range []string{
		`"rejected":[]`,
		`"subsystems":[]`,
		`"files_touched":[]`,
		`"api_changes":[]`,
		`"data_model_changes":[]`,
	} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("starter missing %s in %s", want, encoded)
		}
	}
}

func TestSealUnmarshalErrorAddsArrayShapeHint(t *testing.T) {
	var sr domain.SealResult
	err := json.Unmarshal([]byte(`{"summary":{"decisions":["use strings"]}}`), &sr)
	if err == nil {
		t.Fatal("expected decisions string array to fail unmarshal")
	}

	msg := formatSealUnmarshalError(err)
	if !strings.Contains(msg, "summary.decisions must be Decision[]") {
		t.Fatalf("missing decisions shape hint: %s", msg)
	}
	if !strings.Contains(msg, "not string[]") {
		t.Fatalf("missing string[] warning: %s", msg)
	}
}

func TestSealUnmarshalErrorAddsReviewNotesHint(t *testing.T) {
	var sr domain.SealResult
	err := json.Unmarshal([]byte(`{"summary":{"review_notes":"use strings"}}`), &sr)
	if err == nil {
		t.Fatal("expected review_notes string to fail unmarshal")
	}

	msg := formatSealUnmarshalError(err)
	if !strings.Contains(msg, "summary.review_notes must be string[]") {
		t.Fatalf("missing review_notes shape hint: %s", msg)
	}
	if !strings.Contains(msg, "not a string") {
		t.Fatalf("missing scalar warning: %s", msg)
	}
}

func TestSealUnmarshalErrorAddsGenericStringArrayHint(t *testing.T) {
	var sr domain.SealResult
	err := json.Unmarshal([]byte(`{"fingerprint":{"tags":"auth"}}`), &sr)
	if err == nil {
		t.Fatal("expected tags string to fail unmarshal")
	}

	msg := formatSealUnmarshalError(err)
	if !strings.Contains(msg, "fingerprint.tags must be string[]") {
		t.Fatalf("missing generic string-array hint: %s", msg)
	}
}

func TestNormalizeSealResultForSubmitDropsBlankReviewNotes(t *testing.T) {
	sr := &domain.SealResult{
		Summary: domain.SealSummaryInput{
			ReviewNotes: []string{"", "  ", "review this path"},
		},
	}

	normalizeSealResultForSubmit(sr)

	if got, want := len(sr.Summary.ReviewNotes), 1; got != want {
		t.Fatalf("review note count = %d, want %d: %+v", got, want, sr.Summary.ReviewNotes)
	}
	if sr.Summary.ReviewNotes[0] != "review this path" {
		t.Fatalf("unexpected review notes: %+v", sr.Summary.ReviewNotes)
	}
}
