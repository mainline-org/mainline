package engine

import (
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestBuildSealStarterOmitsActionSignals(t *testing.T) {
	starter := buildSealStarter("int_abc123", "ship the thing", []string{"internal/auth/session.go"})
	if len(starter.Summary.Risks) != 0 {
		t.Fatalf("starter should not include risks: %+v", starter.Summary.Risks)
	}
	if len(starter.Summary.Followups) != 0 {
		t.Fatalf("starter should not include followups: %+v", starter.Summary.Followups)
	}
	if len(starter.Summary.AntiPatterns) != 0 {
		t.Fatalf("starter should not include anti_patterns: %+v", starter.Summary.AntiPatterns)
	}
	instruction := sealInstruction()
	if strings.Contains(instruction, "Keep risks, anti_patterns, and\nfollowups as []") {
		t.Fatalf("instruction still nudges agents to fill old action signal fields")
	}
	if !strings.Contains(instruction, "Seal does not create durable action signals") {
		t.Fatalf("instruction should state the default seal contract")
	}
}

func TestValidateSealActionSignalContractRejectsSealSignals(t *testing.T) {
	base := &domain.SealResult{}
	if err := validateSealActionSignalContract(base); err != nil {
		t.Fatalf("empty signals should pass: %v", err)
	}

	cases := []domain.SealResult{
		{Summary: domain.IntentSummary{Risks: []string{"risk"}}},
		{Summary: domain.IntentSummary{Followups: []string{"followup"}}},
		{Summary: domain.IntentSummary{AntiPatterns: []domain.AntiPattern{{What: "x", Why: "y"}}}},
	}
	for _, tc := range cases {
		if err := validateSealActionSignalContract(&tc); err == nil {
			t.Fatalf("seal action signal accepted: %+v", tc.Summary)
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
