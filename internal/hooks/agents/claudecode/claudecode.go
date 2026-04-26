// Package claudecode wires Claude Code into mainline's hooks subsystem.
package claudecode

import (
	"encoding/json"

	"github.com/mainline-org/mainline/internal/hooks"
)

const AgentName = "claudecode"

const DisplayName = "Claude Code"

const (
	HookSessionStart     = "session-start"
	HookUserPromptSubmit = "user-prompt-submit"
	HookStop             = "stop"
	HookSubagentStop     = "subagent-stop"
	HookPreCompact       = "pre-compact"
	HookSessionEnd       = "session-end"
)

var nativeHookKey = map[string]string{
	HookSessionStart:     "SessionStart",
	HookUserPromptSubmit: "UserPromptSubmit",
	HookStop:             "Stop",
	HookSubagentStop:     "SubagentStop",
	HookPreCompact:       "PreCompact",
	HookSessionEnd:       "SessionEnd",
}

type Agent struct{}

func (Agent) Name() string { return AgentName }

func (Agent) DisplayName() string { return DisplayName }

func (Agent) HookNames() []string {
	return []string{
		HookSessionStart,
		HookUserPromptSubmit,
		HookStop,
		HookSubagentStop,
		HookPreCompact,
		HookSessionEnd,
	}
}

func (Agent) RenderHookOutput(hookName string, d *hooks.Dispatcher, _ *hooks.Event, _ any) ([]byte, error) {
	if d == nil || !d.Settings.Enabled {
		return nil, nil
	}

	var eventName string
	var md string
	switch hookName {
	case HookSessionStart:
		eventName = "SessionStart"
		md = d.RenderSessionStartContext(d.LastSync(), d.LastStatus())
	case HookUserPromptSubmit:
		if d.Engine == nil {
			return nil, nil
		}
		status, statusErr := d.Engine.Status()
		proposals, proposalsErr := d.Engine.ListProposals()
		eventName = "UserPromptSubmit"
		md = d.RenderTurnStartContext(status, proposals, statusErr, proposalsErr)
	default:
		return nil, nil
	}

	if md == "" {
		return nil, nil
	}
	return json.Marshal(map[string]any{
		"continue": true,
		"hookSpecificOutput": map[string]any{
			"hookEventName":     eventName,
			"additionalContext": md,
		},
	})
}

func init() {
	hooks.Register(Agent{})
}
