package engine

import (
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestAgentAuthorityLocalReviewOverridesTeamHandoff(t *testing.T) {
	team := domain.DefaultTeamConfig()
	team.Agent.Autonomy = AgentAutonomyHandoff
	team.Agent.MaxAutonomy = ""
	local := &domain.LocalConfig{
		Agent: &domain.AgentSection{Autonomy: AgentAutonomyReview},
	}

	got := buildAgentAuthority(&team, local)
	if got == nil {
		t.Fatal("expected authority")
	}
	if got.Team.MaxAutonomy != AgentAutonomyReview {
		t.Fatalf("max autonomy should default to review, got %q", got.Team.MaxAutonomy)
	}
	if got.Local == nil || got.Local.Autonomy != AgentAutonomyReview {
		t.Fatalf("expected local review override, got %+v", got.Local)
	}
	if got.Effective.Autonomy != AgentAutonomyReview || got.Effective.StopLine != AgentBoundaryOpenedPR {
		t.Fatalf("effective authority mismatch: %+v", got.Effective)
	}
}

func TestAgentAuthorityTeamMaxCapsLocalReview(t *testing.T) {
	team := domain.DefaultTeamConfig()
	team.Agent.Autonomy = AgentAutonomyHandoff
	team.Agent.MaxAutonomy = AgentAutonomyHandoff
	local := &domain.LocalConfig{
		Agent: &domain.AgentSection{Autonomy: AgentAutonomyReview},
	}

	got := buildAgentAuthority(&team, local)
	if got.Effective.Autonomy != AgentAutonomyHandoff {
		t.Fatalf("team max should cap local review to handoff, got %+v", got.Effective)
	}
	if got.Effective.StopLine != AgentBoundaryProposedIntent {
		t.Fatalf("handoff stop line: got %q", got.Effective.StopLine)
	}
}

func TestAgentAuthorityInvalidConfigWarnsAndFallsBack(t *testing.T) {
	team := domain.DefaultTeamConfig()
	team.Agent.Autonomy = "turbo"
	team.Agent.MaxAutonomy = "ship"
	local := &domain.LocalConfig{
		Agent: &domain.AgentSection{Autonomy: "whatever"},
	}

	got := buildAgentAuthority(&team, local)
	if got.Effective.Autonomy != AgentAutonomyHandoff {
		t.Fatalf("invalid team autonomy should fall back to handoff, got %+v", got.Effective)
	}
	if got.Team.MaxAutonomy != AgentAutonomyReview {
		t.Fatalf("invalid max autonomy should fall back to review, got %q", got.Team.MaxAutonomy)
	}
	warnings := strings.Join(got.Warnings, "\n")
	for _, want := range []string{
		`invalid team agent.autonomy "turbo"; using handoff`,
		`invalid team agent.max_autonomy "ship"; using review`,
		`invalid local agent.autonomy "whatever"; ignoring local override`,
	} {
		if !strings.Contains(warnings, want) {
			t.Fatalf("missing warning %q in %q", want, warnings)
		}
	}
}

func TestAgentAuthorityPreflightBlockLowersCurrentBoundary(t *testing.T) {
	team := domain.DefaultTeamConfig()
	team.Agent.Autonomy = AgentAutonomyReview
	authority := buildAgentAuthority(&team, nil)

	got := agentAuthorityWithPreflightBoundary(authority, true)
	if got.Effective.Autonomy != AgentAutonomyReview {
		t.Fatalf("preflight block must not mutate effective autonomy: %+v", got.Effective)
	}
	if got.Current.AllowedBoundary != AgentBoundaryInspectOrStop || !got.Current.BlockedByPreflight {
		t.Fatalf("blocked current boundary mismatch: %+v", got.Current)
	}
}
