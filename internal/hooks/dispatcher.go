package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// EngineFacade is the subset of engine.Service the Dispatcher needs.
// Defined as an interface here so the hooks package can be unit-tested
// without depending on the engine implementation, and so engine can
// import hooks (for EventBus wiring) without an import cycle.
//
// Methods are intentionally narrow: the Dispatcher only ever runs the
// "auto-flow" subset of operations. Anything that requires agent
// semantic judgment (seal --submit fingerprint, check verdict) stays
// out of scope and remains a manual CLI call by the agent.
type EngineFacade interface {
	// Sync runs the auto-flow team-state refresh. Used by SessionStart.
	Sync() (any, error)

	// Status returns a marshallable status report. Used to surface
	// in-flight prepare snapshots and active intent at SessionStart.
	Status() (any, error)

	// Start opens an intent for the given goal on the current
	// branch. The dispatcher passes the user's first prompt as the
	// goal. Idempotent — starting a second intent on a branch with
	// an active draft is a no-op in the engine.
	Start(goal, thread string) (any, error)

	// Append records a turn against the current active intent.
	// Returns ErrNoActiveIntent if none exists; the dispatcher
	// treats that as a non-fatal skip.
	Append(description string) (any, error)

	// SealPrepare snapshots the current draft into a SealResult
	// template the agent can fill. The dispatcher does NOT call
	// SealSubmit — that needs semantic fingerprint generation.
	SealPrepare(intentID string) (any, error)

	// ActiveDraftIntentID returns the intent id of the active draft
	// on the current branch, or "" if none. Lets the dispatcher
	// decide whether TurnStart should call Start.
	ActiveDraftIntentID() (string, error)
}

// Logger is a minimal sink for dispatcher diagnostics. Hooks run in
// the agent's process — we never want to print to stdout because
// some agents capture stdout as part of their UI. Stderr is the only
// place we can talk to the user, but we still want it filterable.
//
// The default logger writes nothing (silent). The CLI swaps in a
// stderr logger when MAINLINE_HOOKS_DEBUG=1 is set.
type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Debugf(string, ...any) {}
func (nopLogger) Infof(string, ...any)  {}
func (nopLogger) Warnf(string, ...any)  {}

// Notifier sinks human-facing one-line messages. The CLI binds this
// to stderr when not in --json mode; tests bind it to a buffer.
// Intentionally separate from Logger because most hook fires should
// be silent, but a "auto-started intent X" notification IS something
// the user wants to see.
type Notifier interface {
	Notify(line string)
}

type nopNotifier struct{}

func (nopNotifier) Notify(string) {}

// Dispatcher is the central event-to-action router. One instance per
// hook invocation; constructed by the cli, fed an Event by the
// agent's ParseEvent, and produces engine state changes + emits
// domain events on the configured EventBus.
type Dispatcher struct {
	Engine   EngineFacade
	Bus      EventEmitter
	Log      Logger
	Notify   Notifier
	Settings DispatchSettings
}

// DispatchSettings mirror the [hooks] section of .mainline/config.toml.
// Pulled out here so the engine config types can stay where they are
// (in domain/) and the dispatcher just receives a plain struct.
type DispatchSettings struct {
	// Enabled is the soft kill-switch. False makes Dispatch a no-op
	// regardless of event type. The on-disk hook entries still fire
	// — they just exit immediately. Lets users pause automation
	// without touching every agent's config file.
	Enabled bool

	// AutoStartIntent controls TurnStart → mainline start. Off if
	// the team prefers explicit intent creation but still wants
	// auto-append / auto-seal-prepare.
	AutoStartIntent bool

	// AutoAppendTurn controls TurnEnd / SubagentEnd → mainline append.
	AutoAppendTurn bool

	// AutoSealPrepare controls SessionEnd → mainline seal --prepare.
	AutoSealPrepare bool

	// AutoSyncOnSessionStart controls SessionStart → mainline sync.
	// Defaults true; can be disabled on slow networks or for users
	// who prefer to run sync manually.
	AutoSyncOnSessionStart bool
}

// DefaultDispatchSettings are the "hook-package-installs-everything"
// defaults. Each toggle exists so users can opt out of one piece
// while keeping the rest. The product position is: when hooks are
// installed, mainline takes over — all four are on.
func DefaultDispatchSettings() DispatchSettings {
	return DispatchSettings{
		Enabled:                true,
		AutoStartIntent:        true,
		AutoAppendTurn:         true,
		AutoSealPrepare:        true,
		AutoSyncOnSessionStart: true,
	}
}

// NewDispatcher fills in nil dependencies with no-op stand-ins so the
// caller cannot accidentally panic when testing or when only some of
// the deps are wired (e.g. webhook bus disabled by config).
func NewDispatcher(eng EngineFacade, bus EventEmitter, settings DispatchSettings) *Dispatcher {
	d := &Dispatcher{
		Engine:   eng,
		Bus:      bus,
		Log:      nopLogger{},
		Notify:   nopNotifier{},
		Settings: settings,
	}
	if d.Bus == nil {
		d.Bus = nopEmitter{}
	}
	return d
}

// Dispatch routes a normalized Event to the appropriate auto-flow
// handler. Errors are surfaced to the caller so the cli can decide
// whether to print them; the dispatcher does NOT write to stderr
// itself (that is the CLI's job, gated on log level).
//
// nil event is a valid input — agents return nil for native hooks
// that have no normalized mapping (preToolUse etc). Dispatch returns
// nil immediately in that case so the cli's main path stays trivial.
func (d *Dispatcher) Dispatch(ctx context.Context, ev *Event) error {
	if ev == nil {
		return nil
	}
	if !d.Settings.Enabled {
		d.Log.Debugf("hooks disabled, skipping %s", ev.Type)
		return nil
	}
	if d.Engine == nil {
		// No engine wired — cli builds dispatcher without engine
		// when --no-engine is set (e.g. for `mainline hooks <agent>
		// <event>` in a non-mainline repo, where we still want the
		// hook to exit cleanly).
		return nil
	}

	switch ev.Type {
	case SessionStart:
		return d.onSessionStart(ctx, ev)
	case TurnStart:
		return d.onTurnStart(ctx, ev)
	case TurnEnd, SubagentEnd:
		return d.onTurnEnd(ctx, ev)
	case SessionEnd:
		return d.onSessionEnd(ctx, ev)
	case Compaction, SubagentStart:
		// Reserved for future use (compaction = flush; subagent_start
		// = informational). No automation today, but having the
		// branches present means new behaviour can land without a
		// taxonomy change.
		d.Log.Debugf("event %s noop", ev.Type)
		return nil
	default:
		d.Log.Debugf("unknown event type %q", ev.Type)
		return nil
	}
}

// -----------------------------------------------------------
// SessionStart: refresh team state + surface in-flight work
// -----------------------------------------------------------

func (d *Dispatcher) onSessionStart(_ context.Context, ev *Event) error {
	d.Bus.Emit(d.envelope("session_started", ev, nil))
	if !d.Settings.AutoSyncOnSessionStart {
		return nil
	}
	syncResult, err := d.Engine.Sync()
	if err != nil {
		// Network being down on session start should NEVER block
		// the agent. Log + emit and continue.
		d.Log.Warnf("auto-sync on session start: %v", err)
		d.Bus.Emit(d.envelope("sync_failed", ev, map[string]any{"error": err.Error()}))
		return nil
	}
	d.Bus.Emit(d.envelope("sync_completed", ev, syncResult))

	// Surface "you have a stale prepare from last session" — that's
	// the only way the agent learns it should seal --submit before
	// starting new work. Without this nudge the workflow falls back
	// to relying on the agent's memory, which is exactly the
	// problem we set out to solve.
	if status, err := d.Engine.Status(); err == nil && status != nil {
		// Status returns a marshallable struct; we don't model it
		// directly here to keep the facade narrow. Just route it
		// through a webhook event for observers.
		d.Bus.Emit(d.envelope("status_snapshot", ev, status))
	}
	return nil
}

// -----------------------------------------------------------
// TurnStart: auto-start an intent using the user prompt as goal
// -----------------------------------------------------------

func (d *Dispatcher) onTurnStart(_ context.Context, ev *Event) error {
	d.Bus.Emit(d.envelope("turn_started", ev, nil))
	if !d.Settings.AutoStartIntent {
		return nil
	}
	if strings.TrimSpace(ev.Prompt) == "" {
		// Headless mode (cursor -p) doesn't fire beforeSubmitPrompt
		// at all, so we typically never reach here without a prompt.
		// Belt-and-suspenders: if the prompt is empty for any other
		// reason, skip auto-start. We will not invent a goal.
		d.Log.Debugf("turn_start without prompt; skipping auto-start")
		return nil
	}
	if id, _ := d.Engine.ActiveDraftIntentID(); id != "" {
		// Already drafting on this branch; the user is iterating on
		// an existing intent. Auto-append in onTurnEnd will record
		// this turn against that intent.
		d.Log.Debugf("active intent %s already exists; not starting new", id)
		return nil
	}
	goal := summarizePromptAsGoal(ev.Prompt)
	res, err := d.Engine.Start(goal, "")
	if err != nil {
		d.Log.Warnf("auto-start: %v", err)
		return nil
	}
	d.Notify.Notify(fmt.Sprintf("mainline: auto-started intent for goal %q", goal))
	d.Bus.Emit(d.envelope("intent_started", ev, res))
	return nil
}

// -----------------------------------------------------------
// TurnEnd / SubagentEnd: auto-append a turn description
// -----------------------------------------------------------

func (d *Dispatcher) onTurnEnd(_ context.Context, ev *Event) error {
	d.Bus.Emit(d.envelope("turn_ended", ev, nil))
	if !d.Settings.AutoAppendTurn {
		return nil
	}
	desc := turnDescription(ev)
	if desc == "" {
		// Nothing concrete to record. Skip rather than write a
		// turn that says "(no description)" — that pollutes the
		// intent's narrative.
		return nil
	}
	res, err := d.Engine.Append(desc)
	if err != nil {
		// Most common cause: no active intent (user disabled
		// AutoStartIntent or a turn fired before session_start
		// resolved). Log and emit so observers see it.
		d.Log.Debugf("auto-append: %v", err)
		d.Bus.Emit(d.envelope("turn_append_skipped", ev, map[string]any{"error": err.Error()}))
		return nil
	}
	d.Bus.Emit(d.envelope("turn_appended", ev, res))
	return nil
}

// -----------------------------------------------------------
// SessionEnd: snapshot the draft for the agent to seal next session
// -----------------------------------------------------------

func (d *Dispatcher) onSessionEnd(_ context.Context, ev *Event) error {
	d.Bus.Emit(d.envelope("session_ended", ev, nil))
	if !d.Settings.AutoSealPrepare {
		return nil
	}
	id, err := d.Engine.ActiveDraftIntentID()
	if err != nil || id == "" {
		// No draft to prepare — nothing to do. Common case for
		// sessions that didn't end up auto-starting (user opened
		// agent for a quick chat, no edits).
		return nil
	}
	pkg, err := d.Engine.SealPrepare(id)
	if err != nil {
		d.Log.Warnf("auto seal --prepare: %v", err)
		return nil
	}
	d.Notify.Notify(fmt.Sprintf("mainline: prepared seal snapshot for %s; agent should run `mainline seal --submit` next session", id))
	d.Bus.Emit(d.envelope("seal_prepared", ev, pkg))
	return nil
}

// -----------------------------------------------------------
// Helpers
// -----------------------------------------------------------

// envelope builds a webhook event payload. The bus uses this directly;
// the dispatcher does not enforce any schema beyond name + data.
func (d *Dispatcher) envelope(name string, ev *Event, data any) DomainEvent {
	return DomainEvent{
		Name:       name,
		OccurredAt: ev.OccurredAt,
		Source:     "hook",
		Agent:      ev.Agent,
		SessionID:  ev.SessionID,
		Data:       toJSON(data),
	}
}

// summarizePromptAsGoal trims the prompt down to a one-line goal
// string. mainline goals are short — humans skim `mainline log`
// titles first. We keep the first non-empty line of the prompt and
// cap it at 200 chars; the full prompt is preserved in the turn
// description on TurnEnd.
func summarizePromptAsGoal(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 200 {
			return strings.TrimSpace(line[:200]) + "…"
		}
		return line
	}
	if len(prompt) > 200 {
		return strings.TrimSpace(prompt[:200]) + "…"
	}
	return strings.TrimSpace(prompt)
}

// turnDescription synthesizes the text we record on a turn. Cursor's
// subagentStop carries a structured Summary + ModifiedFiles list; the
// regular `stop` hook does not, so we fall back to the agent-supplied
// status. The dispatcher does NOT shell out to `git status` here —
// that's already what engine.Service.Append computes (DiffStatAgainst)
// and we'd just be double-counting.
func turnDescription(ev *Event) string {
	if s := strings.TrimSpace(ev.Summary); s != "" {
		return s
	}
	if len(ev.ModifiedFiles) > 0 {
		return fmt.Sprintf("agent turn modified %d file(s)", len(ev.ModifiedFiles))
	}
	if s := strings.TrimSpace(ev.Status); s != "" {
		return fmt.Sprintf("agent turn (%s)", s)
	}
	return ""
}

func toJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// -----------------------------------------------------------
// EventBus / DomainEvent (fan-out target)
// -----------------------------------------------------------

// DomainEvent is the unit of the webhook fan-out stream. Both the
// dispatcher (lifecycle synthesis) and the engine (actual business
// transitions) emit these. Schema is intentionally flat-ish — each
// subscriber filters by Name and inspects Data as opaque JSON.
type DomainEvent struct {
	// Name is a stable identifier for the event class. Examples:
	//   "intent_started", "turn_appended", "intent_sealed",
	//   "sync_completed", "conflict_detected", "check_judged",
	//   "session_started", "session_ended".
	Name string `json:"name"`

	// OccurredAt is the RFC3339 timestamp the source captured. The
	// webhook sender will add its own DispatchedAt at delivery.
	OccurredAt string `json:"occurred_at,omitempty"`

	// Source identifies the producer ("hook", "engine"). Lets a
	// subscriber tell apart "user submitted a prompt" (hook) from
	// "user ran mainline seal --submit" (engine).
	Source string `json:"source,omitempty"`

	// Agent is set when Source == "hook" — the agent name the event
	// originated from. Empty for engine events.
	Agent string `json:"agent,omitempty"`

	// SessionID groups events from the same agent conversation.
	// Empty for engine events fired outside a hook context.
	SessionID string `json:"session_id,omitempty"`

	// Data is the per-event payload. Already-marshaled JSON so
	// observers can deserialize against their own schemas without
	// us having to hand-write Go types for every shape.
	Data json.RawMessage `json:"data,omitempty"`
}

// EventEmitter is the engine's view of the webhook bus. Defined here
// so engine can import hooks (one-way) without an import cycle. The
// real implementation lives in internal/webhook.
type EventEmitter interface {
	Emit(ev DomainEvent)
}

type nopEmitter struct{}

func (nopEmitter) Emit(DomainEvent) {}

// ReadStdinJSON is a tiny helper that ParseEvent implementations can
// use. Reads everything from r and unmarshals into v. Returns nil if
// the input is empty (some agents fire hooks with no payload).
func ReadStdinJSON(r io.Reader, v any) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read hook stdin: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode hook stdin: %w", err)
	}
	return nil
}
