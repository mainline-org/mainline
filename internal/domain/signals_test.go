package domain

import "testing"

func TestValidateRiskStatement(t *testing.T) {
	valid := RiskStatement{
		FailureMode: "Changing auth middleware may break OAuth callback sessions",
		Trigger:     "OAuth callback path uses existing session state",
		Mitigation:  "Keep callback integration test coverage",
	}
	if err := ValidateRiskStatement(valid); err != nil {
		t.Fatalf("valid risk rejected: %v", err)
	}

	cases := []RiskStatement{
		{Trigger: "when x", Mitigation: "test y"},
		{FailureMode: "breaks x", Mitigation: "test y"},
		{FailureMode: "breaks x", Trigger: "when y"},
	}
	for _, tc := range cases {
		if err := ValidateRiskStatement(tc); err == nil {
			t.Fatalf("invalid risk accepted: %+v", tc)
		}
	}
}

func TestValidateFollowupStatement(t *testing.T) {
	valid := []FollowupStatement{
		{Task: "Remove legacy middleware", Source: SignalSourceExplicitDefer, SourceNote: "user said later"},
		{Task: "Remove legacy middleware", Source: SignalSourceExternalReference, Reference: "https://github.com/org/repo/issues/1"},
		{Task: "Remove legacy middleware", Source: SignalSourceCutScope, SourceNote: "cut from this PR"},
	}
	for _, tc := range valid {
		if err := ValidateFollowupStatement(tc); err != nil {
			t.Fatalf("valid follow-up rejected: %+v: %v", tc, err)
		}
	}

	invalid := []FollowupStatement{
		{Source: SignalSourceExplicitDefer, SourceNote: "user said later"},
		{Task: "Remove legacy middleware", Source: SignalSourceExplicitDefer},
		{Task: "Remove legacy middleware", Source: SignalSourceExternalReference},
		{Task: "Remove legacy middleware", Source: "maybe_later"},
	}
	for _, tc := range invalid {
		if err := ValidateFollowupStatement(tc); err == nil {
			t.Fatalf("invalid follow-up accepted: %+v", tc)
		}
	}
}
