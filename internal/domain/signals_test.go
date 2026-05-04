package domain

import (
	"encoding/json"
	"testing"
)

func TestRiskStatement_LegacyStringJSON(t *testing.T) {
	var got RiskStatement
	if err := json.Unmarshal([]byte(`"old mobile clients still send session cookies"`), &got); err != nil {
		t.Fatalf("unmarshal legacy risk: %v", err)
	}
	if got.Text() != "old mobile clients still send session cookies" {
		t.Fatalf("legacy risk text mismatch: %q", got.Text())
	}
}

func TestValidateRiskStatement(t *testing.T) {
	valid := RiskStatement{
		FailureMode: "Changing auth middleware may break OAuth callback sessions",
		Impact:      "Existing login sessions could fail during callback handling",
		Validation:  "Covered by callback integration test",
	}
	if err := ValidateRiskStatement(valid); err != nil {
		t.Fatalf("valid risk rejected: %v", err)
	}

	cases := []RiskStatement{
		{},
		{FailureMode: "Risky"},
		{FailureMode: "Risky", Impact: "some users fail"},
	}
	for _, tc := range cases {
		if err := ValidateRiskStatement(tc); err == nil {
			t.Fatalf("invalid risk accepted: %+v", tc)
		}
	}
}

func TestFollowupStatement_LegacyStringJSON(t *testing.T) {
	var got FollowupStatement
	if err := json.Unmarshal([]byte(`"add refresh token support"`), &got); err != nil {
		t.Fatalf("unmarshal legacy follow-up: %v", err)
	}
	if got.Text() != "add refresh token support" {
		t.Fatalf("legacy follow-up text mismatch: %q", got.Text())
	}
}

func TestValidateFollowupStatement(t *testing.T) {
	explicitDefer := FollowupStatement{
		Task:       "Add the long-running migration test later",
		Source:     FollowupSourceExplicitDefer,
		SourceNote: "User said this time we should not do it and should record a follow-up.",
	}
	if err := ValidateFollowupStatement(explicitDefer); err != nil {
		t.Fatalf("valid explicit-defer follow-up rejected: %v", err)
	}

	externalReference := FollowupStatement{
		Task:      "Remove legacy callback session middleware",
		Source:    FollowupSourceExternalReference,
		Reference: "https://github.com/mainline-org/mainline/issues/123",
	}
	if err := ValidateFollowupStatement(externalReference); err != nil {
		t.Fatalf("valid external-reference follow-up rejected: %v", err)
	}

	cutScope := FollowupStatement{
		Task:       "Move large-repo profiling into the follow-up benchmark PR",
		Source:     FollowupSourceCutScope,
		SourceNote: "This PR deliberately cut profiling scope to keep the write-rule change reviewable.",
	}
	if err := ValidateFollowupStatement(cutScope); err != nil {
		t.Fatalf("valid cut-scope follow-up rejected: %v", err)
	}

	cases := []FollowupStatement{
		{},
		{Task: "Maybe add telemetry later", Source: "maybe_later"},
		{Task: "Deferred but no evidence", Source: FollowupSourceExplicitDefer},
		{Task: "External but no reference", Source: FollowupSourceExternalReference},
		{Task: "Cut but no evidence", Source: FollowupSourceCutScope},
	}
	for _, tc := range cases {
		if err := ValidateFollowupStatement(tc); err == nil {
			t.Fatalf("invalid follow-up accepted: %+v", tc)
		}
	}
}
