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
