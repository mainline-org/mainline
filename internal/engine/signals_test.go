package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestBuildSealStarterOmitsActionSignals(t *testing.T) {
	starter := buildSealStarter("int_abc123", "ship the thing", []string{"internal/auth/session.go"})
	data, err := json.Marshal(starter.Summary)
	if err != nil {
		t.Fatal(err)
	}
	for _, legacyKey := range []string{"risks", "followups", "anti_patterns"} {
		if strings.Contains(string(data), `"`+legacyKey+`"`) {
			t.Fatalf("starter summary must not expose legacy key %q: %s", legacyKey, data)
		}
	}
	schema := sealResultSchemaHints()
	schemaData, err := json.Marshal(schema.Summary)
	if err != nil {
		t.Fatal(err)
	}
	for _, legacyKey := range []string{"risks", "followups", "anti_patterns"} {
		if strings.Contains(string(schemaData), `"`+legacyKey+`"`) {
			t.Fatalf("seal_result_schema summary must not expose legacy key %q: %s", legacyKey, schemaData)
		}
	}
	instruction := sealInstruction()
	if strings.Contains(instruction, "Keep risks, anti_patterns, and\nfollowups as []") {
		t.Fatalf("instruction still nudges agents to fill old action signal fields")
	}
	if !strings.Contains(instruction, "Seal summary is not a durable action-signal creation surface") {
		t.Fatalf("instruction should state the default seal contract")
	}
}

func TestValidateNoLegacySealSummarySignalsRejectsKeys(t *testing.T) {
	base := json.RawMessage(`{"intent_id":"int_x","summary":{"title":"t"}}`)
	if err := validateNoLegacySealSummarySignals(base); err != nil {
		t.Fatalf("payload without legacy keys should pass: %v", err)
	}

	for _, key := range []string{"risks", "followups", "anti_patterns"} {
		payload := json.RawMessage(`{"intent_id":"int_x","summary":{"title":"t","` + key + `":[]}}`)
		if err := validateNoLegacySealSummarySignals(payload); err == nil {
			t.Fatalf("legacy seal summary key %q accepted", key)
		}
	}
}

func TestExplicitSignalEventsMaterialize(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("test-agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Start("change auth middleware", ""); err != nil {
		t.Fatal(err)
	}

	risk, err := svc.AddRisk(AddRiskInput{
		Files: []string{"internal/auth/session.go"},
		Statement: domain.RiskStatement{
			FailureMode: "Changing auth middleware may break OAuth callback sessions",
			Trigger:     "OAuth callback path uses existing session state",
			Mitigation:  "Keep callback integration test coverage",
		},
	})
	if err != nil {
		t.Fatalf("add risk: %v", err)
	}
	if !strings.HasPrefix(risk.ID, "risk_") {
		t.Fatalf("explicit risk should get risk_ id, got %s", risk.ID)
	}

	followup, err := svc.AddFollowup(AddFollowupInput{
		Files: []string{"internal/auth/session.go"},
		Statement: domain.FollowupStatement{
			Task:       "Remove legacy session middleware after callback migration",
			Source:     domain.SignalSourceExplicitDefer,
			SourceNote: "user said this PR should not do it",
		},
	})
	if err != nil {
		t.Fatalf("add followup: %v", err)
	}
	if !strings.HasPrefix(followup.ID, "followup_") {
		t.Fatalf("explicit follow-up should get followup_ id, got %s", followup.ID)
	}

	constraint, err := svc.AddConstraint(AddConstraintInput{
		Files:    []string{"internal/auth/session.go"},
		What:     "Do not remove legacy session middleware on OAuth callback paths",
		Why:      "OAuth callback handler still requires session state",
		Severity: "high",
		Source:   domain.SignalSourceExplicitUser,
	})
	if err != nil {
		t.Fatalf("add constraint: %v", err)
	}
	if !strings.HasPrefix(constraint.ID, "guard_") {
		t.Fatalf("explicit constraint should get guard_ id, got %s", constraint.ID)
	}

	view, err := svc.Store.ReadMainlineView()
	if err != nil {
		t.Fatal(err)
	}
	if got := materializeRisks(view, "internal/auth"); len(got) != 1 || got[0].ID != risk.ID {
		t.Fatalf("materialized risks = %+v, want %s", got, risk.ID)
	}
	if got := materializeFollowups(view, "internal/auth"); len(got) != 1 || got[0].ID != followup.ID {
		t.Fatalf("materialized followups = %+v, want %s", got, followup.ID)
	}
	inherited := domain.BuildInheritedConstraints(view, []string{"internal/auth/session.go"}, nil, "")
	if len(inherited) != 1 || inherited[0].ConstraintID != constraint.ID {
		t.Fatalf("inherited constraints = %+v, want %s", inherited, constraint.ID)
	}
}
