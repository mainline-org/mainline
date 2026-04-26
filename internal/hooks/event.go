package hooks

import (
	"context"
	"encoding/json"
	"io"
	"time"
)

// EventType is the normalized lifecycle event taxonomy. Each agent's
// native hook payload is mapped onto exactly one of these by the
// agent's ParseEvent. Adding a new agent does not extend this enum.
//
// The taxonomy is intentionally smaller than any single agent's set:
// we only normalize events the dispatcher knows how to act on. Native
// hooks with no automation mapping (e.g. preToolUse, postToolUse) can
// still be installed by an Agent — its ParseEvent just returns nil.
type EventType string

const (
	// SessionStart fires when the user opens a new conversation. The
	// dispatcher uses this to refresh team state (auto-sync) and
	// surface any in-flight prepare snapshots from a prior session.
	SessionStart EventType = "session_start"

	// TurnStart fires when the user submits a prompt. Carries the
	// raw prompt text so the dispatcher can auto-`mainline start`
	// using the prompt as the goal when no active intent exists.
	TurnStart EventType = "turn_start"

	// TurnEnd fires when one agent loop completes. The dispatcher
	// auto-appends a turn summary derived from git status + the
	// optional summary text the agent provided.
	TurnEnd EventType = "turn_end"

	// SessionEnd fires when the conversation ends. The dispatcher
	// auto-runs `mainline seal --prepare` and leaves a snapshot for
	// the next session to surface.
	SessionEnd EventType = "session_end"

	// SubagentStart / SubagentEnd are subagent (Task tool) bookends.
	// SubagentEnd is treated like TurnEnd by default — it is the
	// only place we get a structured summary + modified_files list.
	SubagentStart EventType = "subagent_start"
	SubagentEnd   EventType = "subagent_end"

	// Compaction fires before context compaction. The dispatcher
	// uses this to flush any pending state to the actor log so a
	// post-compaction agent has a consistent view.
	Compaction EventType = "compaction"
)

// Event is the normalized hook payload. Fields are populated as
// available — TurnStart sets Prompt; SessionEnd sets Reason and
// FinalStatus; SubagentEnd sets Summary + ModifiedFiles. Always
// safe to access optional fields: zero value means "agent did not
// provide it".
type Event struct {
	// Type is the normalized lifecycle event.
	Type EventType `json:"type"`

	// Agent is the canonical agent name (e.g. "cursor"). The
	// Dispatcher uses this for logging and for routing to
	// agent-specific Service overrides if any are added later.
	Agent string `json:"agent"`

	// SessionID identifies the agent conversation. Used for
	// per-session state under .ml-cache/hooks/<agent>/<session>/.
	SessionID string `json:"session_id,omitempty"`

	// OccurredAt is the local time the dispatcher received the
	// event (we cannot fully trust the agent's clock). RFC3339.
	OccurredAt string `json:"occurred_at,omitempty"`

	// Prompt is the user-submitted prompt text. Populated for
	// TurnStart only. May be empty in headless mode where the
	// underlying agent does not surface beforeSubmitPrompt.
	Prompt string `json:"prompt,omitempty"`

	// Summary is the agent-generated turn / subagent summary.
	// Populated for SubagentEnd; some agents may also populate it
	// for TurnEnd. Empty otherwise.
	Summary string `json:"summary,omitempty"`

	// ModifiedFiles lists files the agent reports having touched
	// during the turn / subagent. Cursor's subagentStop populates
	// this; the dispatcher prefers `git status` for ground truth
	// when this is empty.
	ModifiedFiles []string `json:"modified_files,omitempty"`

	// Status / Reason are agent-provided lifecycle hints
	// ("completed", "interrupted"). Threaded through to webhook
	// payloads for observability without being interpreted.
	Status string `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`

	// Raw is the original JSON payload the agent passed on stdin.
	// Preserved so debugging tooling and the webhook fan-out can
	// access agent-specific fields without us re-modeling them.
	Raw json.RawMessage `json:"raw,omitempty"`
}

// NewEvent stamps OccurredAt with the current wall clock. Agents
// should construct Events through here so the dispatcher's audit
// log always has a timestamp.
func NewEvent(t EventType, agent string) *Event {
	return &Event{
		Type:       t,
		Agent:      agent,
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// EventParser is the per-agent hook-payload decoder. Each Agent
// implementation provides its own; the central hooks-cmd entry point
// reads stdin and delegates to ParseEvent.
//
// Returning (nil, nil) is allowed and means "this is a native hook we
// install but have no normalized mapping for" — see preToolUse etc.
// The dispatcher skips nil events without warning.
type EventParser interface {
	ParseEvent(ctx context.Context, hookName string, stdin io.Reader) (*Event, error)
}
