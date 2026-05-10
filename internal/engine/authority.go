package engine

import (
	"fmt"
	"strings"

	"github.com/mainline-org/mainline/internal/domain"
)

const (
	AgentAuthoritySchemaVersion = 1

	AgentAutonomyAssist  = "assist"
	AgentAutonomyHandoff = "handoff"
	AgentAutonomyReview  = "review"

	AgentBoundaryBeforeCommit   = "before_commit"
	AgentBoundaryProposedIntent = "proposed_intent"
	AgentBoundaryOpenedPR       = "opened_pr"
	AgentBoundaryInspectOrStop  = "inspect_or_stop"
)

type AgentAuthority struct {
	SchemaVersion int                     `json:"schema_version"`
	AdvisoryOnly  bool                    `json:"advisory_only"`
	Team          AgentAuthorityTeam      `json:"team"`
	Local         *AgentAuthorityLocal    `json:"local,omitempty"`
	Effective     AgentAuthorityEffective `json:"effective"`
	Current       AgentAuthorityCurrent   `json:"current"`
	Warnings      []string                `json:"warnings,omitempty"`
}

type AgentAuthorityTeam struct {
	Autonomy    string `json:"autonomy"`
	MaxAutonomy string `json:"max_autonomy"`
	Source      string `json:"source"`
}

type AgentAuthorityLocal struct {
	Autonomy string `json:"autonomy"`
	Source   string `json:"source"`
}

type AgentAuthorityEffective struct {
	Autonomy string `json:"autonomy"`
	StopLine string `json:"stop_line"`
}

type AgentAuthorityCurrent struct {
	AllowedBoundary    string `json:"allowed_boundary"`
	BlockedByPreflight bool   `json:"blocked_by_preflight"`
}

func buildAgentAuthority(team *domain.TeamConfig, local *domain.LocalConfig) *AgentAuthority {
	if team == nil {
		return nil
	}
	var warnings []string
	teamAutonomy, ok := normalizeAgentAutonomy(team.Agent.Autonomy)
	if !ok {
		if strings.TrimSpace(team.Agent.Autonomy) != "" {
			warnings = append(warnings, fmt.Sprintf("invalid team agent.autonomy %q; using handoff", team.Agent.Autonomy))
		}
		teamAutonomy = AgentAutonomyHandoff
	}
	maxAutonomy, ok := normalizeAgentAutonomy(team.Agent.MaxAutonomy)
	if !ok {
		if strings.TrimSpace(team.Agent.MaxAutonomy) != "" {
			warnings = append(warnings, fmt.Sprintf("invalid team agent.max_autonomy %q; using review", team.Agent.MaxAutonomy))
		}
		maxAutonomy = AgentAutonomyReview
	}

	preferred := teamAutonomy
	var localOut *AgentAuthorityLocal
	if local != nil && local.Agent != nil && strings.TrimSpace(local.Agent.Autonomy) != "" {
		localAutonomy, ok := normalizeAgentAutonomy(local.Agent.Autonomy)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("invalid local agent.autonomy %q; ignoring local override", local.Agent.Autonomy))
		} else {
			preferred = localAutonomy
			localOut = &AgentAuthorityLocal{
				Autonomy: localAutonomy,
				Source:   ".mainline/local.toml",
			}
		}
	}

	effective := minAgentAutonomy(preferred, maxAutonomy)
	stopLine := agentAutonomyStopLine(effective)
	return &AgentAuthority{
		SchemaVersion: AgentAuthoritySchemaVersion,
		AdvisoryOnly:  true,
		Team: AgentAuthorityTeam{
			Autonomy:    teamAutonomy,
			MaxAutonomy: maxAutonomy,
			Source:      ".mainline/config.toml",
		},
		Local: localOut,
		Effective: AgentAuthorityEffective{
			Autonomy: effective,
			StopLine: stopLine,
		},
		Current: AgentAuthorityCurrent{
			AllowedBoundary: stopLine,
		},
		Warnings: warnings,
	}
}

func agentAuthorityWithPreflightBoundary(in *AgentAuthority, blocked bool) *AgentAuthority {
	if in == nil {
		return nil
	}
	out := *in
	if in.Local != nil {
		local := *in.Local
		out.Local = &local
	}
	out.Warnings = append([]string{}, in.Warnings...)
	out.Current = in.Current
	out.Current.BlockedByPreflight = blocked
	if blocked {
		out.Current.AllowedBoundary = AgentBoundaryInspectOrStop
	} else if out.Current.AllowedBoundary == "" {
		out.Current.AllowedBoundary = out.Effective.StopLine
	}
	return &out
}

func normalizeAgentAutonomy(value string) (string, bool) {
	switch strings.TrimSpace(value) {
	case AgentAutonomyAssist:
		return AgentAutonomyAssist, true
	case AgentAutonomyHandoff:
		return AgentAutonomyHandoff, true
	case AgentAutonomyReview:
		return AgentAutonomyReview, true
	default:
		return "", false
	}
}

func minAgentAutonomy(a, b string) string {
	if agentAutonomyRank(a) <= agentAutonomyRank(b) {
		return a
	}
	return b
}

func agentAutonomyRank(value string) int {
	switch value {
	case AgentAutonomyAssist:
		return 0
	case AgentAutonomyHandoff:
		return 1
	case AgentAutonomyReview:
		return 2
	default:
		return 1
	}
}

func agentAutonomyStopLine(value string) string {
	switch value {
	case AgentAutonomyAssist:
		return AgentBoundaryBeforeCommit
	case AgentAutonomyReview:
		return AgentBoundaryOpenedPR
	default:
		return AgentBoundaryProposedIntent
	}
}

func AgentAuthorityPlainLine(a *AgentAuthority) string {
	if a == nil {
		return ""
	}
	if a.Current.BlockedByPreflight {
		return fmt.Sprintf("Agent autonomy: %s - blocked now; resolve preflight findings before lifecycle advancement.", a.Effective.Autonomy)
	}
	switch a.Effective.Autonomy {
	case AgentAutonomyAssist:
		return "Agent autonomy: assist - may analyze/edit/verify; stop before commit/seal."
	case AgentAutonomyReview:
		return "Agent autonomy: review - may push a non-main branch and open/update PR; stop before merge/release."
	default:
		return "Agent autonomy: handoff - may commit/seal/propose; stop before push/PR."
	}
}
