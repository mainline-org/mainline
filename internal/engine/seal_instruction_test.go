package engine

import (
	"strings"
	"testing"
)

func TestSealInstructionTightensRiskGuidance(t *testing.T) {
	instruction := sealInstruction()
	for _, want := range []string{
		"Risk discipline:",
		"concrete failure mode",
		"compatibility break",
		"data loss/corruption",
		"security/privacy issue",
		"performance/scale regression",
		"user-visible misbehavior",
		"maintenance hazard",
		"Do not put verification notes",
		"ordinary follow-up work",
		"empty risks array",
		"Put deferred work in followups",
	} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("seal instruction missing %q:\n%s", want, instruction)
		}
	}
}
