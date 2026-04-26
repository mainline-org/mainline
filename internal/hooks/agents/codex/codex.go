// Package codex wires OpenAI Codex into mainline's hooks subsystem.
package codex

import (
	"encoding/json"

	"github.com/mainline-org/mainline/internal/hooks"
)

const AgentName = "codex"

const DisplayName = "Codex"

const (
	HookSessionStart     = "session-start"
	HookUserPromptSubmit = "user-prompt-submit"
	HookStop             = "stop"
)

var nativeHookKey = map[string]string{
	HookSessionStart:     "SessionStart",
	HookUserPromptSubmit: "UserPromptSubmit",
	HookStop:             "Stop",
}

type Agent struct{}

func (Agent) Name() string { return AgentName }

func (Agent) DisplayName() string { return DisplayName }

func (Agent) HookNames() []string {
	return []string{
		HookSessionStart,
		HookUserPromptSubmit,
		HookStop,
	}
}

func (Agent) RenderHookOutput(hookName string, d *hooks.Dispatcher, _ *hooks.Event, _ any) ([]byte, error) {
	if hookName != HookSessionStart || d == nil {
		return nil, nil
	}
	md := d.RenderSessionStartContext(d.LastSync(), d.LastStatus())
	if md == "" {
		return nil, nil
	}
	return json.Marshal(map[string]any{
		"continue": true,
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "SessionStart",
			"additionalContext": md,
		},
	})
}

func init() {
	hooks.Register(Agent{})
}
