// Package webhook implements the domain-event fan-out for mainline.
//
// Architecture:
//
//	hooks/engine -> Bus.Emit
//	             -> json envelope written to .ml-cache/webhook-queue/<id>.json
//	             -> fork(detached) `mainline __webhook-dispatch <id>`
//	     ↓ (separate process, the user's CLI returns immediately)
//	__webhook-dispatch:
//	     reads envelope, POSTs to each matching subscription with HMAC,
//	     retries with exponential backoff, renames to .failed.json on
//	     terminal failure.
//
// Why detached:
//   - Webhook destinations can be slow / down. Inline POSTs would
//     pin the user's `mainline seal --submit` for tens of seconds.
//   - The mainline CLI is run interactively (and as an agent hook —
//     where blocking is even worse). Fire-and-forget enqueue + a
//     background drain is the only humane shape.
//
// The queue is on-disk JSON because (a) the mainline CLI is
// short-lived and an in-process queue would lose pending events at
// exit, (b) the user can inspect / replay / debug queue contents
// trivially, (c) we already pay the .ml-cache I/O cost so adding one
// more directory is free.
package webhook

import (
	"encoding/json"
	"time"
)

// Envelope is one queue entry. The producer (engine / hooks) fills
// in Name + Data + Source/Agent/SessionID; the queue layer adds an
// EventID and EnqueuedAt. The sender adds AttemptCount / LastError.
//
// Schema is intentionally flat so a HTTP subscriber consuming raw
// envelopes via curl can read interesting fields without nesting
// deep into "data": {...}.
type Envelope struct {
	// EventID is a queue-local handle the sender uses for filenames
	// and retry resumption. Not exposed to subscribers in the body
	// directly, but sent as the X-Mainline-Event-Id header so a
	// subscriber can dedupe on retry.
	EventID string `json:"event_id"`

	// Name is the domain-event name (e.g. "intent_sealed",
	// "conflict_detected"). Subscribers filter on this.
	Name string `json:"name"`

	// EnqueuedAt is the producer's wall-clock when Bus.Emit was
	// called (RFC3339). DispatchedAt (set at delivery time, header
	// only) is separate.
	EnqueuedAt string `json:"enqueued_at"`

	// Source is "engine" | "hook". Lets a subscriber distinguish
	// "agent submitted a prompt" (hook) from "user ran mainline
	// seal --submit" (engine) without grepping data.
	Source string `json:"source,omitempty"`

	// Agent is set when Source == "hook" — the agent name (cursor,
	// claude-code, ...). Empty for engine-originated events.
	Agent string `json:"agent,omitempty"`

	// SessionID groups events from the same agent conversation.
	// Empty for engine events fired outside a hook context.
	SessionID string `json:"session_id,omitempty"`

	// Data is the event payload. We keep it as already-marshaled
	// JSON so the in-flight types in the engine package don't
	// have to be re-modeled here.
	Data json.RawMessage `json:"data,omitempty"`

	// AttemptCount tracks retries. The sender increments before
	// each POST and persists back to disk on failure so a process
	// restart picks up where it left off.
	AttemptCount int `json:"attempt_count,omitempty"`

	// LastError is the most recent delivery failure reason. Empty
	// on success (the file is deleted on success — only failed
	// entries persist).
	LastError string `json:"last_error,omitempty"`
}

// NewEnvelope stamps a fresh envelope with EventID and EnqueuedAt.
// Callers fill the rest. Centralized here so the timestamp format
// is consistent across producer call sites.
func NewEnvelope(name string, data json.RawMessage) *Envelope {
	return &Envelope{
		EventID:    generateEventID(),
		Name:       name,
		EnqueuedAt: time.Now().UTC().Format(time.RFC3339),
		Data:       data,
	}
}
