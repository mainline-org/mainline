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
// Methods are deliberately tiny: hooks run as a child process WITHOUT
// LLM intelligence, so they must never produce semantic content
// (intent goal, append description, fingerprint). All the dispatcher
// can do is execute mechanical, deterministic operations and surface
// state for the agent to read. Everything else (start, append, seal
// prepare/submit, check) is an agent decision and stays a manual CLI
// call described in AGENTS.md.
type EngineFacade interface {
	// Sync runs the team-state refresh. Used by SessionStart.
	Sync() (any, error)

	// Status returns a marshallable status report. Used at SessionStart
	// to give the agent a snapshot of in-flight work (active draft,
	// proposed intents, synced head) so it can decide on its own
	// whether to start / append / seal — exactly as AGENTS.md
	// instructs it to in the no-hook flow.
	Status() (any, error)

	// ListProposals returns a marshallable, read-only snapshot of
	// proposed team intents. Used by agents that can inject fresh
	// per-prompt context. This stays mechanical: it only reports
	// already-recorded intent metadata and never decides whether the
	// current prompt overlaps with it.
	ListProposals() (any, error)

	// BinaryStaleness returns a hint about whether the running
	// mainline binary is older than the latest main commit. Pure
	// mechanical (file mtime vs git commit date) — produces the
	// data, not the decision. The dispatcher prepends a warning to
	// session_start additional_context when the hint says stale, so
	// the agent (which has an LLM) can decide what to do with it.
	//
	// Returns (any, error) so the dispatcher does not have to import
	// the engine package; concrete type is *engine.BinaryStalenessReport,
	// detected via a structural interface in RenderSessionStartContext.
	BinaryStaleness() (any, error)
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
// Kept separate from Logger because most hook fires should be silent,
// but a "sync surfaced N new merges" notification IS something the
// user wants to see.
type Notifier interface {
	Notify(line string)
}

type nopNotifier struct{}

func (nopNotifier) Notify(string) {}

// Dispatcher is the central event-to-action router. One instance per
// hook invocation; constructed by the cli, fed an Event by the
// agent's ParseEvent, and produces engine state changes + emits
// domain events on the configured EventBus.
//
// Hooks dispatch is split into two halves:
//
//   - SessionStart does mechanical work (sync + status) and caches
//     the result on the dispatcher. The agent-specific renderer
//     reads the cached result via RenderSessionStartContext and
//     turns it into agent-protocol stdout (e.g. cursor's
//     additional_context).
//   - All other event types are pure observer signals on the webhook
//     bus. The dispatcher does NOT call Start / Append / SealPrepare
//     because those require semantic judgment that hooks cannot
//     perform without an LLM.
type Dispatcher struct {
	Engine   EngineFacade
	Bus      EventEmitter
	Log      Logger
	Notify   Notifier
	Settings DispatchSettings

	// Cached SessionStart results so the agent renderer can build
	// additional_context without re-running sync/status. Set by
	// onSessionStart, consumed by RenderSessionStartContext.
	lastSync      any
	lastSyncErr   error
	lastStatus    any
	lastStaleness any
	lastSessionEv *Event
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

	// AutoSyncOnSessionStart controls SessionStart → mainline sync.
	// Defaults true; can be disabled on slow networks or for users
	// who prefer to run sync manually.
	AutoSyncOnSessionStart bool
}

// DefaultDispatchSettings: hooks installed implies sync-on-start. No
// other auto-flow toggles exist — start/append/seal are agent jobs.
func DefaultDispatchSettings() DispatchSettings {
	return DispatchSettings{
		Enabled:                true,
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

// Dispatch routes a normalized Event to the appropriate handler.
// Errors are surfaced to the caller so the cli can decide whether to
// print them; the dispatcher does NOT write to stderr itself (that
// is the CLI's job, gated on log level).
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
		// when the hook fires in a non-mainline repo. We still
		// want it to exit cleanly.
		return nil
	}

	switch ev.Type {
	case SessionStart:
		return d.onSessionStart(ctx, ev)
	case TurnStart:
		// Webhook-only signal. Hooks cannot judge whether the
		// prompt is a goal or a procedural ask — that's the
		// agent's job per AGENTS.md.
		d.Bus.Emit(d.envelope("turn_started", ev, nil))
		return nil
	case TurnEnd, SubagentEnd:
		// Webhook-only signal. Hooks cannot judge whether the
		// turn warrants a mainline append — that's the agent's
		// job per AGENTS.md ("after each meaningful logical
		// change … record one turn").
		d.Bus.Emit(d.envelope("turn_ended", ev, nil))
		return nil
	case SessionEnd:
		// Webhook-only signal. Seal --prepare requires a goal-aware
		// view of the draft and seal --submit requires a semantic
		// fingerprint — both agent jobs.
		d.Bus.Emit(d.envelope("session_ended", ev, nil))
		return nil
	case Compaction, SubagentStart:
		// Reserved for future use. Having the branches present
		// means new behaviour can land without a taxonomy change.
		d.Log.Debugf("event %s noop", ev.Type)
		return nil
	default:
		d.Log.Debugf("unknown event type %q", ev.Type)
		return nil
	}
}

// -----------------------------------------------------------
// SessionStart: refresh team state + cache snapshot for renderer
// -----------------------------------------------------------

func (d *Dispatcher) onSessionStart(_ context.Context, ev *Event) error {
	d.Bus.Emit(d.envelope("session_started", ev, nil))
	d.lastSessionEv = ev

	if !d.Settings.AutoSyncOnSessionStart {
		// Even without sync we still want a status snapshot so the
		// agent renderer has something to inject — consistent UX
		// regardless of network state.
		if status, err := d.Engine.Status(); err == nil && status != nil {
			d.lastStatus = status
			d.Bus.Emit(d.envelope("status_snapshot", ev, status))
		}
		return nil
	}

	syncResult, err := d.Engine.Sync()
	if err != nil {
		// Network being down on session start should NEVER block
		// the agent. Log + emit and continue with status only.
		d.Log.Warnf("auto-sync on session start: %v", err)
		d.lastSyncErr = err
		d.Bus.Emit(d.envelope("sync_failed", ev, map[string]any{"error": err.Error()}))
	} else {
		d.lastSync = syncResult
		d.Bus.Emit(d.envelope("sync_completed", ev, syncResult))
	}

	if status, err := d.Engine.Status(); err == nil && status != nil {
		d.lastStatus = status
		d.Bus.Emit(d.envelope("status_snapshot", ev, status))
	}

	// Cheap mechanical staleness check. Errors here are silently
	// dropped: a session that starts fine should not be polluted by
	// "could not stat the binary" warnings — worst case the agent
	// just doesn't get a stale-binary hint this session and any
	// genuine staleness becomes visible the next time the user does
	// something the stale binary mishandles.
	if rep, err := d.Engine.BinaryStaleness(); err == nil && rep != nil {
		d.lastStaleness = rep
	}
	return nil
}

// -----------------------------------------------------------
// SessionStart context renderer — agent-protocol-agnostic
// -----------------------------------------------------------

// LastSync returns the cached SessionStart sync result (nil if sync
// was disabled or failed). Renderers use it to compose context.
func (d *Dispatcher) LastSync() any { return d.lastSync }

// LastSyncErr returns the cached SessionStart sync error (nil if
// sync succeeded or was disabled).
func (d *Dispatcher) LastSyncErr() error { return d.lastSyncErr }

// LastStatus returns the cached SessionStart status snapshot.
func (d *Dispatcher) LastStatus() any { return d.lastStatus }

// LastBinaryStaleness returns the cached SessionStart staleness
// report. Renderers cast through stalenessHinter to surface a warning
// without importing the engine package.
func (d *Dispatcher) LastBinaryStaleness() any { return d.lastStaleness }

// stalenessHinter is the structural interface any staleness report
// must satisfy to participate in renderer warnings. Defined here in
// the hooks package so the dispatcher does not need an import-cycle
// with engine — the actual report type lives in engine and is
// detected by interface match at runtime.
type stalenessHinter interface {
	IsStale() bool
	StaleReason() string
}

// RenderSessionStartContext composes a markdown blob the agent can
// inject as system context at session start. It contains ONLY:
//
//   - the deterministic state snapshot the agent would otherwise have
//     to fetch by running `mainline status --json`;
//   - the deterministic sync summary (or sync error);
//   - a scenario hint that points the agent at AGENTS.md and the
//     two CLI commands it should run before deciding (`mainline log`
//     and `mainline show`).
//
// It does NOT make decisions for the agent: no goal text, no append
// description, no fingerprint. Those are LLM jobs and the markdown
// stays neutral so the agent's reasoning is the source of truth.
func (d *Dispatcher) RenderSessionStartContext(syncResult any, status any) string {
	var b strings.Builder
	b.WriteString("# Mainline session-start context\n\n")
	b.WriteString("Hooks ran `mainline sync` and `mainline status` for you. ")
	b.WriteString("Use this snapshot to orient yourself. The full agent contract is in AGENTS.md ")
	b.WriteString("— hooks do not replace any step there; they only save you the two CLI calls below.\n\n")

	// Stale-binary warning sits at the top because if the running
	// hook process is on stale code, every other section in this
	// context blob may itself be wrong (the auto-Start regression
	// that motivated this check produced phantom intents AND a
	// status snapshot that already reflected those phantom intents).
	// Surfacing this first lets the agent re-examine subsequent
	// sections with the appropriate skepticism.
	if hinter, ok := d.lastStaleness.(stalenessHinter); ok && hinter.IsStale() {
		b.WriteString("> **stale-binary warning**: ")
		b.WriteString(strings.ReplaceAll(hinter.StaleReason(), "\n", "\n> "))
		b.WriteString("\n>\n> If the user reports behaviour you thought was already fixed, this is the most likely cause — ask them to rebuild before debugging deeper.\n\n")
	}

	b.WriteString("## status snapshot\n\n")
	if status == nil {
		b.WriteString("_status unavailable_\n\n")
	} else if raw, err := json.MarshalIndent(status, "", "  "); err == nil {
		b.WriteString("```json\n")
		b.Write(raw)
		b.WriteString("\n```\n\n")
	} else {
		b.WriteString("_status unmarshallable: ")
		b.WriteString(err.Error())
		b.WriteString("_\n\n")
	}

	b.WriteString("## sync summary\n\n")
	if d.lastSyncErr != nil {
		b.WriteString("Sync FAILED: `")
		b.WriteString(d.lastSyncErr.Error())
		b.WriteString("`. Treat this snapshot as potentially stale and re-run `mainline sync` once your network is healthy.\n\n")
	} else if syncResult == nil {
		b.WriteString("_sync was disabled or skipped_\n\n")
	} else if raw, err := json.MarshalIndent(syncResult, "", "  "); err == nil {
		b.WriteString("```json\n")
		b.Write(raw)
		b.WriteString("\n```\n\n")
	} else {
		b.WriteString("_sync result unmarshallable: ")
		b.WriteString(err.Error())
		b.WriteString("_\n\n")
	}

	b.WriteString("## what to do next\n\n")
	b.WriteString("Follow AGENTS.md exactly — hooks change nothing about the workflow:\n\n")
	b.WriteString("- Before non-trivial work, run `mainline log --json --limit 30` and `mainline show <intent_id> --json` for any prior intents that touch your area. Do this even when the prompt looks small; the cost is two CLI calls and the payoff is not duplicating someone else's just-finished work.\n")
	b.WriteString("- If `active_intent` above is empty and your turn is real work (not a one-off question or procedural ask), run `mainline start \"<goal>\"`. If it is non-empty, append against it instead.\n")
	b.WriteString("- After each meaningful logical change, run `mainline append \"<what changed>\"`. The hooks DO NOT do this for you — only your judgment can decide what counts as a meaningful change.\n")
	b.WriteString("- When the task is complete, commit code, then `mainline seal --prepare --json`, fill the SealResult (fingerprint generously), then `mainline seal --submit --json < seal.json`. If the response carries a `conflicts` array, surface it to the user verbatim.\n")
	b.WriteString("- Re-run `mainline status` whenever you are about to make an architectural decision; sessionStart context is a one-shot snapshot, not a live view.\n")
	return b.String()
}

// RenderTurnStartContext composes the small per-prompt reminder used
// by agents that support UserPromptSubmit additionalContext. It is
// intentionally much shorter than RenderSessionStartContext: enough to
// keep the current active intent / proposal count visible, but not a
// substitute for the agent's required log/show pass before non-trivial
// work.
func (d *Dispatcher) RenderTurnStartContext(status any, proposals any, statusErr error, proposalsErr error) string {
	var b strings.Builder
	b.WriteString("# Mainline per-prompt context\n\n")
	b.WriteString("Codex refreshed lightweight Mainline state before this prompt. ")
	b.WriteString("Use it as a reminder; AGENTS.md remains the workflow authority.\n\n")

	b.WriteString("## status summary\n\n")
	if statusErr != nil {
		b.WriteString("Status FAILED: `")
		b.WriteString(statusErr.Error())
		b.WriteString("`.\n\n")
	} else if raw, ok := compactStatusJSON(status); ok {
		b.WriteString("```json\n")
		b.Write(raw)
		b.WriteString("\n```\n\n")
	} else {
		b.WriteString("_status unavailable_\n\n")
	}

	b.WriteString("## proposals summary\n\n")
	if proposalsErr != nil {
		b.WriteString("ListProposals FAILED: `")
		b.WriteString(proposalsErr.Error())
		b.WriteString("`.\n\n")
	} else if raw, ok := compactProposalsJSON(proposals); ok {
		b.WriteString("```json\n")
		b.Write(raw)
		b.WriteString("\n```\n\n")
	} else {
		b.WriteString("_proposals unavailable_\n\n")
	}

	b.WriteString("## reminder\n\n")
	b.WriteString("- If this prompt is real work and there is no `active_intent`, decide whether to run `mainline start \"<goal>\"` before editing.\n")
	b.WriteString("- If there is an `active_intent`, append only after a meaningful logical change; hooks still do not decide that for you.\n")
	b.WriteString("- Before non-trivial changes, still run `mainline log --json --limit 30` and `mainline show <intent_id> --json` for relevant prior intents.\n")
	return b.String()
}

func compactStatusJSON(status any) ([]byte, bool) {
	if status == nil {
		return nil, false
	}
	raw, err := json.Marshal(status)
	if err != nil {
		return nil, false
	}
	var in struct {
		Branch       string `json:"branch,omitempty"`
		ActorID      string `json:"actor_id,omitempty"`
		ActiveIntent *struct {
			IntentID string `json:"intent_id,omitempty"`
			Status   string `json:"status,omitempty"`
			Thread   string `json:"thread,omitempty"`
			Goal     string `json:"goal,omitempty"`
		} `json:"active_intent,omitempty"`
		TurnCount     int  `json:"turn_count"`
		ProposedCount int  `json:"proposed_count"`
		SyncStale     bool `json:"sync_stale"`
		Coverage      *struct {
			UncoveredCount int `json:"uncovered_count"`
		} `json:"coverage,omitempty"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, false
	}
	out := struct {
		Branch         string `json:"branch,omitempty"`
		ActorID        string `json:"actor_id,omitempty"`
		ActiveIntent   any    `json:"active_intent,omitempty"`
		TurnCount      int    `json:"turn_count"`
		ProposedCount  int    `json:"proposed_count"`
		SyncStale      bool   `json:"sync_stale"`
		UncoveredCount *int   `json:"uncovered_count,omitempty"`
	}{
		Branch:        in.Branch,
		ActorID:       in.ActorID,
		ActiveIntent:  in.ActiveIntent,
		TurnCount:     in.TurnCount,
		ProposedCount: in.ProposedCount,
		SyncStale:     in.SyncStale,
	}
	if in.Coverage != nil {
		out.UncoveredCount = &in.Coverage.UncoveredCount
	}
	raw, err = json.MarshalIndent(out, "", "  ")
	return raw, err == nil
}

func compactProposalsJSON(proposals any) ([]byte, bool) {
	if proposals == nil {
		return nil, false
	}
	raw, err := json.Marshal(proposals)
	if err != nil {
		return nil, false
	}
	var in struct {
		Proposals []struct {
			IntentID string `json:"intent_id,omitempty"`
			Title    string `json:"title,omitempty"`
			Thread   string `json:"thread,omitempty"`
			Status   string `json:"status,omitempty"`
		} `json:"proposals"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, false
	}
	limit := len(in.Proposals)
	if limit > 5 {
		limit = 5
	}
	out := struct {
		Count     int `json:"count"`
		Shown     int `json:"shown"`
		Proposals any `json:"proposals"`
	}{
		Count:     len(in.Proposals),
		Shown:     limit,
		Proposals: in.Proposals[:limit],
	}
	raw, err = json.MarshalIndent(out, "", "  ")
	return raw, err == nil
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
