package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	FollowupSourceExplicitDefer     = "explicit_defer"
	FollowupSourceExternalReference = "external_reference"
	FollowupSourceCutScope          = "cut_scope"
)

func (r *RiskStatement) UnmarshalJSON(data []byte) error {
	var legacy string
	if err := json.Unmarshal(data, &legacy); err == nil {
		r.FailureMode = legacy
		r.LegacyText = legacy
		return nil
	}
	type alias RiskStatement
	var out alias
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	*r = RiskStatement(out)
	return nil
}

func (r RiskStatement) MarshalJSON() ([]byte, error) {
	type alias RiskStatement
	return json.Marshal(alias(r))
}

func (r RiskStatement) Text() string {
	if strings.TrimSpace(r.LegacyText) != "" &&
		strings.TrimSpace(r.Trigger) == "" &&
		strings.TrimSpace(r.Impact) == "" &&
		strings.TrimSpace(r.Mitigation) == "" &&
		strings.TrimSpace(r.Validation) == "" &&
		strings.TrimSpace(r.Owner) == "" {
		return strings.TrimSpace(r.LegacyText)
	}
	parts := []string{strings.TrimSpace(r.FailureMode)}
	if v := strings.TrimSpace(r.Trigger); v != "" {
		parts = append(parts, "Trigger: "+v)
	}
	if v := strings.TrimSpace(r.Impact); v != "" {
		parts = append(parts, "Impact: "+v)
	}
	if v := strings.TrimSpace(r.Mitigation); v != "" {
		parts = append(parts, "Mitigation: "+v)
	}
	if v := strings.TrimSpace(r.Validation); v != "" {
		parts = append(parts, "Validation: "+v)
	}
	if v := strings.TrimSpace(r.Owner); v != "" {
		parts = append(parts, "Owner: "+v)
	}
	return strings.Join(nonEmpty(parts), " ")
}

func (r RiskStatement) SearchText() string {
	return strings.Join(nonEmpty([]string{
		r.FailureMode,
		r.Trigger,
		r.Impact,
		r.Mitigation,
		r.Validation,
		r.Owner,
		r.LegacyText,
	}), " ")
}

func (f *FollowupStatement) UnmarshalJSON(data []byte) error {
	var legacy string
	if err := json.Unmarshal(data, &legacy); err == nil {
		f.Task = legacy
		f.LegacyText = legacy
		return nil
	}
	type alias FollowupStatement
	var out alias
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	*f = FollowupStatement(out)
	return nil
}

func (f FollowupStatement) MarshalJSON() ([]byte, error) {
	type alias FollowupStatement
	return json.Marshal(alias(f))
}

func (f FollowupStatement) Text() string {
	if strings.TrimSpace(f.LegacyText) != "" &&
		strings.TrimSpace(f.Source) == "" &&
		strings.TrimSpace(f.Reference) == "" &&
		strings.TrimSpace(f.SourceNote) == "" &&
		strings.TrimSpace(f.Owner) == "" &&
		strings.TrimSpace(f.Due) == "" {
		return strings.TrimSpace(f.LegacyText)
	}
	parts := []string{strings.TrimSpace(f.Task)}
	if v := strings.TrimSpace(f.Source); v != "" {
		parts = append(parts, "Source: "+v)
	}
	if v := strings.TrimSpace(f.Reference); v != "" {
		parts = append(parts, "Reference: "+v)
	}
	if v := strings.TrimSpace(f.SourceNote); v != "" {
		parts = append(parts, "Note: "+v)
	}
	if v := strings.TrimSpace(f.Owner); v != "" {
		parts = append(parts, "Owner: "+v)
	}
	if v := strings.TrimSpace(f.Due); v != "" {
		parts = append(parts, "Due: "+v)
	}
	return strings.Join(nonEmpty(parts), " ")
}

func (f FollowupStatement) SearchText() string {
	return strings.Join(nonEmpty([]string{
		f.Task,
		f.Source,
		f.Reference,
		f.SourceNote,
		f.Owner,
		f.Due,
		f.LegacyText,
	}), " ")
}

func RiskTextList(risks []RiskStatement) []string {
	out := make([]string, 0, len(risks))
	for _, r := range risks {
		if text := strings.TrimSpace(r.Text()); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func FollowupTextList(followups []FollowupStatement) []string {
	out := make([]string, 0, len(followups))
	for _, f := range followups {
		if text := strings.TrimSpace(f.Text()); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func LegacyRiskStatements(texts ...string) []RiskStatement {
	out := make([]RiskStatement, 0, len(texts))
	for _, text := range texts {
		out = append(out, RiskStatement{FailureMode: text, LegacyText: text})
	}
	return out
}

func LegacyFollowupStatements(texts ...string) []FollowupStatement {
	out := make([]FollowupStatement, 0, len(texts))
	for _, text := range texts {
		out = append(out, FollowupStatement{Task: text, LegacyText: text})
	}
	return out
}

func ValidateRiskStatement(r RiskStatement) error {
	if strings.TrimSpace(r.FailureMode) == "" {
		return fmt.Errorf("failure_mode is required")
	}
	if strings.TrimSpace(r.Trigger) == "" && strings.TrimSpace(r.Impact) == "" {
		return fmt.Errorf("trigger or impact is required")
	}
	if strings.TrimSpace(r.Mitigation) == "" &&
		strings.TrimSpace(r.Validation) == "" &&
		strings.TrimSpace(r.Owner) == "" {
		return fmt.Errorf("mitigation, validation, or owner is required")
	}
	return nil
}

func ValidateFollowupStatement(f FollowupStatement) error {
	if strings.TrimSpace(f.Task) == "" {
		return fmt.Errorf("task is required")
	}
	switch strings.TrimSpace(f.Source) {
	case FollowupSourceExplicitDefer:
		if strings.TrimSpace(f.SourceNote) == "" {
			return fmt.Errorf("source_note is required for explicit_defer follow-ups")
		}
	case FollowupSourceCutScope:
		if strings.TrimSpace(f.SourceNote) == "" {
			return fmt.Errorf("source_note is required for cut_scope follow-ups")
		}
	case FollowupSourceExternalReference:
		if strings.TrimSpace(f.Reference) == "" {
			return fmt.Errorf("reference is required for external_reference follow-ups")
		}
	default:
		return fmt.Errorf("source must be %q, %q, or %q", FollowupSourceExplicitDefer, FollowupSourceExternalReference, FollowupSourceCutScope)
	}
	return nil
}

func nonEmpty(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s := strings.TrimSpace(item); s != "" {
			out = append(out, s)
		}
	}
	return out
}
