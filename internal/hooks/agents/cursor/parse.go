package cursor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mainline-org/mainline/internal/hooks"
)

// ParseEvent decodes Cursor's hook stdin payload and maps it onto the
// normalized hooks.Event taxonomy. Each hookName branch knows the
// exact cursor JSON shape (documented at cursor's hook reference) and
// extracts the fields the dispatcher needs.
//
// We intentionally decode into a permissive raw-message struct rather
// than a sealed type per hook: cursor adds fields over time and a
// strict decoder would refuse new payloads on every minor cursor
// release. The ground rule is "be tolerant of additions, surface
// what we recognize, preserve everything in Event.Raw".
func (Agent) ParseEvent(_ context.Context, hookName string, stdin io.Reader) (*hooks.Event, error) {
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("read cursor hook stdin: %w", err)
	}

	switch hookName {
	case HookSessionStart:
		ev := hooks.NewEvent(hooks.SessionStart, AgentName)
		ev.Raw = json.RawMessage(raw)
		hydrate(raw, ev)
		return ev, nil

	case HookSessionEnd:
		ev := hooks.NewEvent(hooks.SessionEnd, AgentName)
		ev.Raw = json.RawMessage(raw)
		hydrate(raw, ev)
		// session-end carries a `reason` field in cursor: "user",
		// "logout", "prompt_input_exit". We surface it on the
		// envelope so a webhook subscriber can tell apart "user
		// finished" from "session abandoned".
		var sub struct {
			Reason string `json:"reason"`
			Status string `json:"status"`
		}
		_ = json.Unmarshal(raw, &sub)
		ev.Reason = sub.Reason
		if sub.Status != "" {
			ev.Status = sub.Status
		}
		return ev, nil

	case HookBeforeSubmitPrompt:
		ev := hooks.NewEvent(hooks.TurnStart, AgentName)
		ev.Raw = json.RawMessage(raw)
		hydrate(raw, ev)
		// Cursor's beforeSubmitPrompt payload is `{"prompt": "..."}`
		// with optional metadata. The dispatcher uses Prompt to
		// derive an auto-Start goal; if cursor ever drops the
		// field (e.g. headless mode, where the hook does not fire
		// at all but defensive coding never hurts), we leave Prompt
		// empty and the dispatcher's onTurnStart skips auto-start.
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
		// `stop` payload is sparse; cursor surfaces a status string
		// ("completed", "interrupted") and not much else. We don't
		// have a per-turn summary here — the dispatcher relies on
		// engine.Service.Append computing diff stats against the
		// base commit, so a "no summary" turn still records what
		// changed in the repo.
		var sub struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(raw, &sub)
		ev.Status = sub.Status
		return ev, nil

	case HookSubagentStop:
		ev := hooks.NewEvent(hooks.SubagentEnd, AgentName)
		ev.Raw = json.RawMessage(raw)
		hydrate(raw, ev)
		// subagent-stop is the richest payload cursor gives us per
		// turn: a structured summary text plus the explicit list of
		// files the subagent modified. We pass both through; the
		// dispatcher prefers Summary over file-count as the turn
		// description.
		var sub struct {
			Summary       string   `json:"summary"`
			Status        string   `json:"status"`
			ModifiedFiles []string `json:"modified_files"`
		}
		_ = json.Unmarshal(raw, &sub)
		ev.Summary = sub.Summary
		ev.Status = sub.Status
		ev.ModifiedFiles = sub.ModifiedFiles
		return ev, nil

	default:
		// Unknown hook id — cursor added a new lifecycle event we
		// don't yet map. Dispatcher tolerates nil ev (treats as
		// noop), so returning here is safe; we still preserve the
		// raw bytes in case a webhook subscriber wants to forward
		// the unknown event.
		ev := &hooks.Event{Agent: AgentName, Raw: json.RawMessage(raw)}
		return ev, nil
	}
}

// hydrate fills in cursor-common fields (session_id, conversation_id)
// from the raw payload onto ev. Every cursor hook payload includes
// session_id; we want it on the Event for log correlation and webhook
// fan-out without each branch repeating the decoding boilerplate.
func hydrate(raw []byte, ev *hooks.Event) {
	if len(raw) == 0 {
		return
	}
	var sub struct {
		SessionID      string `json:"session_id"`
		ConversationID string `json:"conversation_id"`
	}
	if err := json.Unmarshal(raw, &sub); err != nil {
		return
	}
	if sub.SessionID != "" {
		ev.SessionID = sub.SessionID
	} else if sub.ConversationID != "" {
		// Older cursor builds use "conversation_id" instead of
		// "session_id". Treat them as equivalent for our purposes.
		ev.SessionID = sub.ConversationID
	}
}
