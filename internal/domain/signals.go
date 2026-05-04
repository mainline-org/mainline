package domain

import (
	"fmt"
	"strings"
)

const (
	SignalSourceCommand           = "command"
	SignalSourceExplicitUser      = "explicit_user"
	SignalSourceReviewerPromote   = "reviewer_promote"
	SignalSourceExplicitDefer     = "explicit_defer"
	SignalSourceExternalReference = "external_reference"
	SignalSourceCutScope          = "cut_scope"
)

// Constraint is a human-promoted future behavior rule. Unlike legacy
// summary.anti_patterns, constraints are not produced by seal prose.
type Constraint struct {
	ID           string   `json:"id"`
	What         string   `json:"what"`
	Why          string   `json:"why"`
	Severity     string   `json:"severity,omitempty"`
	Files        []string `json:"files,omitempty"`
	SourceIntent string   `json:"source_intent,omitempty"`
	OpenedAt     string   `json:"opened_at,omitempty"`
	OpenedBy     string   `json:"opened_by,omitempty"`
	Source       string   `json:"source,omitempty"`
	SourceNote   string   `json:"source_note,omitempty"`
}

// RiskStatement is the explicit risk shape agents and humans must use
// when they decide a review-facing warning is worth preserving.
type RiskStatement struct {
	FailureMode string `json:"failure_mode"`
	Trigger     string `json:"trigger,omitempty"`
	Impact      string `json:"impact,omitempty"`
	Mitigation  string `json:"mitigation,omitempty"`
	Validation  string `json:"validation,omitempty"`
	Owner       string `json:"owner,omitempty"`
}

func (r RiskStatement) Text() string {
	var parts []string
	if s := strings.TrimSpace(r.FailureMode); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(r.Trigger); s != "" {
		parts = append(parts, "trigger: "+s)
	}
	if s := strings.TrimSpace(r.Impact); s != "" {
		parts = append(parts, "impact: "+s)
	}
	if s := strings.TrimSpace(r.Mitigation); s != "" {
		parts = append(parts, "mitigation: "+s)
	}
	if s := strings.TrimSpace(r.Validation); s != "" {
		parts = append(parts, "validation: "+s)
	}
	if s := strings.TrimSpace(r.Owner); s != "" {
		parts = append(parts, "owner: "+s)
	}
	return strings.Join(parts, " | ")
}

func ValidateRiskStatement(r RiskStatement) error {
	if strings.TrimSpace(r.FailureMode) == "" {
		return fmt.Errorf("risk.failure_mode is required")
	}
	if strings.TrimSpace(r.Trigger) == "" && strings.TrimSpace(r.Impact) == "" {
		return fmt.Errorf("risk requires trigger or impact")
	}
	if strings.TrimSpace(r.Mitigation) == "" &&
		strings.TrimSpace(r.Validation) == "" &&
		strings.TrimSpace(r.Owner) == "" {
		return fmt.Errorf("risk requires mitigation, validation, or owner")
	}
	return nil
}

// FollowupStatement is an explicit deferred work item with provenance.
type FollowupStatement struct {
	Task       string `json:"task"`
	Source     string `json:"source"`
	SourceNote string `json:"source_note,omitempty"`
	Reference  string `json:"reference,omitempty"`
	Owner      string `json:"owner,omitempty"`
	Due        string `json:"due,omitempty"`
}

func (f FollowupStatement) Text() string {
	var parts []string
	if s := strings.TrimSpace(f.Task); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(f.Source); s != "" {
		parts = append(parts, "source: "+s)
	}
	if s := strings.TrimSpace(f.SourceNote); s != "" {
		parts = append(parts, "note: "+s)
	}
	if s := strings.TrimSpace(f.Reference); s != "" {
		parts = append(parts, "reference: "+s)
	}
	if s := strings.TrimSpace(f.Owner); s != "" {
		parts = append(parts, "owner: "+s)
	}
	if s := strings.TrimSpace(f.Due); s != "" {
		parts = append(parts, "due: "+s)
	}
	return strings.Join(parts, " | ")
}

func ValidateFollowupStatement(f FollowupStatement) error {
	if strings.TrimSpace(f.Task) == "" {
		return fmt.Errorf("followup.task is required")
	}
	switch strings.TrimSpace(f.Source) {
	case SignalSourceExplicitDefer, SignalSourceCutScope:
		if strings.TrimSpace(f.SourceNote) == "" {
			return fmt.Errorf("followup.source_note is required for source %q", f.Source)
		}
	case SignalSourceExternalReference:
		if strings.TrimSpace(f.Reference) == "" {
			return fmt.Errorf("followup.reference is required for source %q", f.Source)
		}
	default:
		return fmt.Errorf("followup.source must be one of %s, %s, %s",
			SignalSourceExplicitDefer, SignalSourceExternalReference, SignalSourceCutScope)
	}
	return nil
}
