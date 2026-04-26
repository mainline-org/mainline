package claudecode

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
		return nil, fmt.Errorf("read claude code hook stdin: %w", err)
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
			StopHookActive bool `json:"stop_hook_active"`
		}
		_ = json.Unmarshal(raw, &sub)
		if sub.StopHookActive {
			ev.Status = "continued"
		} else {
			ev.Status = "completed"
		}
		return ev, nil

	case HookSubagentStop:
		ev := hooks.NewEvent(hooks.SubagentEnd, AgentName)
		ev.Raw = json.RawMessage(raw)
		hydrate(raw, ev)
		var sub struct {
			StopHookActive bool   `json:"stop_hook_active"`
			AgentType      string `json:"agent_type"`
		}
		_ = json.Unmarshal(raw, &sub)
		if sub.StopHookActive {
			ev.Status = "continued"
		} else {
			ev.Status = "completed"
		}
		ev.Reason = sub.AgentType
		return ev, nil

	case HookPreCompact:
		ev := hooks.NewEvent(hooks.Compaction, AgentName)
		ev.Raw = json.RawMessage(raw)
		hydrate(raw, ev)
		var sub struct {
			Trigger string `json:"trigger"`
		}
		_ = json.Unmarshal(raw, &sub)
		ev.Reason = sub.Trigger
		return ev, nil

	case HookSessionEnd:
		ev := hooks.NewEvent(hooks.SessionEnd, AgentName)
		ev.Raw = json.RawMessage(raw)
		hydrate(raw, ev)
		var sub struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(raw, &sub)
		ev.Reason = sub.Reason
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
