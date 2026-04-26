package webhook

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/hooks"
)

// Bus is the production EventBus / EventEmitter. It serializes the
// payload to disk under .ml-cache/webhook-queue/ and forks a
// detached `mainline __webhook-dispatch` so HTTP delivery happens
// outside the user's CLI process.
//
// Bus.Emit is intentionally cheap: marshal -> write file -> fork ->
// return. The whole operation is target-millisecond level so even
// emitting from inside `mainline status` does not noticeably slow
// the CLI.
type Bus struct {
	// QueueDir is the directory envelopes are written to. Caller
	// passes Store.WebhookQueueDir() so the path stays inside .ml-cache.
	QueueDir string

	// Subscriptions is the team's webhook list, read from
	// config.toml. The Bus uses it ONLY to short-circuit when no
	// subscriber matches the event Name — we do not POST inline.
	// The detached sender re-reads config from disk so its
	// behaviour stays consistent if the config changes between
	// enqueue and delivery.
	Subscriptions []domain.WebhookSubscription

	// SelfBinary is the path of the running mainline executable.
	// The Bus forks `<SelfBinary> __webhook-dispatch <event-id>`
	// for each enqueued envelope. Caller passes os.Executable()
	// (cli does this once at startup); fallback is a PATH lookup
	// of "mainline".
	SelfBinary string

	// Source identifies the producer ("engine" | "hook"). One Bus
	// is constructed per process; cli sets it once.
	Source string
}

// New returns a Bus pre-wired with the supplied queue dir and
// subscriptions. Subs may be empty — Emit is a no-op when no
// subscribers match, and the detached fork is also skipped because
// the sender would just exit immediately with nothing to send.
func New(queueDir string, subs []domain.WebhookSubscription, selfBinary string, source string) *Bus {
	return &Bus{
		QueueDir:      queueDir,
		Subscriptions: subs,
		SelfBinary:    selfBinary,
		Source:        source,
	}
}

// Emit implements engine.EventBus. Marshals data, builds an envelope,
// writes it to the queue, then forks the detached sender. Any error
// is logged to stderr but never returned — emitting is best-effort
// observability and must never break the calling command.
//
// We DO check `len(b.matchingSubs(name)) > 0` and skip enqueue when
// nothing matches, both as an optimization and to keep the queue
// directory tidy. Subscribers added later will only see events from
// that point forward; the queue is not a historical record.
func (b *Bus) Emit(name string, data any) {
	if b == nil {
		return
	}
	if !b.hasMatchingSub(name) {
		return
	}
	raw, err := marshal(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "webhook: marshal %s: %v\n", name, err)
		return
	}
	env := NewEnvelope(name, raw)
	env.Source = b.Source
	if err := b.write(env); err != nil {
		fmt.Fprintf(os.Stderr, "webhook: enqueue %s: %v\n", name, err)
		return
	}
	if err := b.spawnSender(env.EventID); err != nil {
		// Spawn failed (e.g. binary missing). The envelope is
		// already on disk; `mainline webhook retry` will pick it
		// up later. Don't print under normal CLI flow because the
		// failure is recoverable; do print under MAINLINE_HOOKS_DEBUG.
		if os.Getenv("MAINLINE_HOOKS_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "webhook: spawn dispatcher: %v\n", err)
		}
	}
}

// EmitDomain implements hooks.EventEmitter. The hooks Dispatcher
// emits a fully-built DomainEvent (it has agent / session_id context
// the engine lacks); we bridge into Emit by promoting those fields
// onto the envelope before enqueue.
func (b *Bus) EmitDomain(ev hooks.DomainEvent) {
	if b == nil {
		return
	}
	if !b.hasMatchingSub(ev.Name) {
		return
	}
	env := NewEnvelope(ev.Name, ev.Data)
	env.Source = ev.Source
	env.Agent = ev.Agent
	env.SessionID = ev.SessionID
	if ev.OccurredAt != "" {
		env.EnqueuedAt = ev.OccurredAt
	}
	if err := b.write(env); err != nil {
		fmt.Fprintf(os.Stderr, "webhook: enqueue %s: %v\n", ev.Name, err)
		return
	}
	_ = b.spawnSender(env.EventID)
}

// hasMatchingSub returns true if any subscription wants this event.
// Empty Events list on a subscription is treated as "all events" so
// observability dashboards (which want everything) don't have to
// hand-list every domain event.
func (b *Bus) hasMatchingSub(name string) bool {
	for _, sub := range b.Subscriptions {
		if matchesEvents(sub.Events, name) {
			return true
		}
	}
	return false
}

// matchesEvents is shared between Bus and the sender — Bus uses it
// for the "any match?" early exit; the sender uses it per-subscription
// during delivery.
func matchesEvents(filter []string, name string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, f := range filter {
		if f == name {
			return true
		}
	}
	return false
}

// write writes the envelope to <queue>/<event_id>.json. We use
// O_EXCL so a colliding event id (vanishingly unlikely with 16 hex
// chars but possible) does not silently overwrite a pending entry.
func (b *Bus) write(env *Envelope) error {
	if err := os.MkdirAll(b.QueueDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", b.QueueDir, err)
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(b.QueueDir, env.EventID+".json")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return nil
}

// spawnSender forks a detached `<binary> __webhook-dispatch <id>`.
// The child inherits no stdio (so output does not pollute the CLI),
// sets a new process group (so `Ctrl-C` of the parent does not kill
// it), and exits when delivery completes or terminal-fails.
func (b *Bus) spawnSender(eventID string) error {
	if b.SelfBinary == "" {
		// Fall back to PATH lookup. If mainline is not on PATH the
		// user has bigger problems; don't try to be clever.
		b.SelfBinary = "mainline"
	}
	cmd := exec.Command(b.SelfBinary, "__webhook-dispatch", eventID)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	// Detach: setsid on Unix puts the child in its own process
	// group + session so it survives the parent. The platform-
	// specific bits live in detach_*.go.
	detachAttrs(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Don't Wait — that's the whole point. cmd.Process is GC'd
	// when this Bus is dropped; the OS reaps the detached child.
	go func() { _ = cmd.Process.Release() }()
	return nil
}

func marshal(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(v)
}
