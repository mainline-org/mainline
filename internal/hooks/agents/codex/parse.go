package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mainline-org/mainline/internal/hooks"
)

func (Agent) ParseEvent(_ context.Context, hookName string, stdin io.Reader) (*hooks.Event, error) {
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("read codex hook stdin: %w", err)
	}

	switch hookName {
	case HookSessionStart:
		ev := hooks.NewEvent(hooks.SessionStart, AgentName)
		ev.Raw = json.RawMessage(raw)
		hydrate(raw, ev)
		var sub struct {
			Source string `json:"source"`
		}
		_ = json.Unmarshal(raw, &sub)
		ev.Reason = sub.Source
		return ev, nil

	case HookUserPromptSubmit:
		ev := hooks.NewEvent(hooks.TurnStart, AgentName)
		ev.Raw = json.RawMessage(raw)
		hydrate(raw, ev)
		var sub struct {
			Prompt string `json:"prompt"`
		}
		_ = json.Unmarshal(raw, &sub)
		ev.Prompt = sub.Prompt
		return ev, nil

	case HookStop:
		ev := hooks.NewEvent(hooks.TurnEnd, AgentName)
		ev.Raw = json.RawMessage(raw)
		hydrate(raw, ev)
		var sub struct {
			LastAssistantMessage *string `json:"last_assistant_message"`
			StopHookActive       bool    `json:"stop_hook_active"`
		}
		_ = json.Unmarshal(raw, &sub)
		if sub.LastAssistantMessage != nil {
			ev.Summary = *sub.LastAssistantMessage
		}
		if sub.StopHookActive {
			ev.Status = "continued"
		} else {
			ev.Status = "completed"
		}
		return ev, nil

	default:
		ev := &hooks.Event{Agent: AgentName, Raw: json.RawMessage(raw)}
		return ev, nil
	}
}

func hydrate(raw []byte, ev *hooks.Event) {
	if len(raw) == 0 {
		return
	}
	var sub struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &sub); err != nil {
		return
	}
	ev.SessionID = sub.SessionID
}
