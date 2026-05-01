//go:build !quick

package engine

import (
	"fmt"
	"testing"

	"pgregory.net/rapid"

	"github.com/mainline-org/mainline/internal/core"
	"github.com/mainline-org/mainline/internal/domain"
)

// ---------------------------------------------------------------------------
// Property: rebuildView event replay produces the correct status for every
// possible event ordering. This is the state machine property — each intent
// transitions through (proposed → abandoned | superseded | merged), and the
// final status must match what the last relevant event dictates.
//
// The event processing loop in rebuildView uses two rules:
//   1. IntentSealedEvent creates/overwrites the entire IntentView (map write)
//   2. Other events mutate an existing entry (map update, requires prior sealed)
//
// This means:
//   - [sealed, abandoned] → abandoned ✓
//   - [abandoned, sealed] → proposed (abandoned has no entry to update, sealed creates fresh)
//   - [sealed₁, sealed₂] → proposed (second sealed overwrites first)
//   - [sealed, abandoned, sealed₂] → proposed (second sealed overwrites everything)
//
// The reference model captures these semantics.
// ---------------------------------------------------------------------------

func TestPropertyRebuildViewStatusStateMachine(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}

		intentID := fmt.Sprintf("int_%s", rapid.StringMatching(`[a-f0-9]{8}`).Draw(rt, "id"))
		actorID := "actor_test"
		prefix := "_mainline/actor"

		// Generate a random sequence of events for ONE intent
		nEvents := rapid.IntRange(1, 6).Draw(rt, "nEvents")
		eventTypes := []domain.EventType{
			domain.EventIntentSealed,
			domain.EventIntentAbandoned,
			domain.EventIntentSuperseded,
			domain.EventIntentMergeAcknowledged,
		}

		var events []domain.EventType
		for i := 0; i < nEvents; i++ {
			events = append(events, rapid.SampledFrom(eventTypes).Draw(rt, fmt.Sprintf("evt%d", i)))
		}

		// Compute expected status via reference model
		wantStatus := refReplayStatus(events)

		// Write events to the actor log
		for i, evtType := range events {
			ts := fmt.Sprintf("2026-01-01T00:%02d:00Z", i)
			evtID := fmt.Sprintf("evt_%d", i)

			switch evtType {
			case domain.EventIntentSealed:
				evt := domain.IntentSealedEvent{
					BaseEvent: domain.BaseEvent{
						EventID:       evtID,
						SchemaVersion: 1,
						EventType:     domain.EventIntentSealed,
						ActorID:       actorID,
						Timestamp:     ts,
					},
					IntentID:   intentID,
					Goal:       "test goal",
					GitBranch:  "main",
					CodeCommit: "abc123",
					SealedAt:   ts,
					Summary: domain.IntentSummary{
						Title: "test", What: "test", Why: "test",
					},
					Fingerprint: domain.SemanticFingerprint{
						Subsystems:   []string{"test"},
						FilesTouched: []string{"a.go"},
					},
				}
				if err := svc.Store.AppendActorLogEvent(actorID, prefix, evt); err != nil {
					rt.Fatalf("append sealed: %v", err)
				}

			case domain.EventIntentAbandoned:
				evt := domain.IntentAbandonedEvent{
					BaseEvent: domain.BaseEvent{
						EventID:       evtID,
						SchemaVersion: 1,
						EventType:     domain.EventIntentAbandoned,
						ActorID:       actorID,
						Timestamp:     ts,
					},
					IntentID: intentID,
				}
				if err := svc.Store.AppendActorLogEvent(actorID, prefix, evt); err != nil {
					rt.Fatalf("append abandoned: %v", err)
				}

			case domain.EventIntentSuperseded:
				evt := domain.IntentSupersededEvent{
					BaseEvent: domain.BaseEvent{
						EventID:       evtID,
						SchemaVersion: 1,
						EventType:     domain.EventIntentSuperseded,
						ActorID:       actorID,
						Timestamp:     ts,
					},
					IntentID:     intentID,
					SupersededBy: "int_newer",
				}
				if err := svc.Store.AppendActorLogEvent(actorID, prefix, evt); err != nil {
					rt.Fatalf("append superseded: %v", err)
				}

			case domain.EventIntentMergeAcknowledged:
				evt := domain.IntentMergeAcknowledgedEvent{
					BaseEvent: domain.BaseEvent{
						EventID:       evtID,
						SchemaVersion: 1,
						EventType:     domain.EventIntentMergeAcknowledged,
						ActorID:       actorID,
						Timestamp:     ts,
					},
					IntentID:    intentID,
					MergeCommit: "merge_abc",
				}
				if err := svc.Store.AppendActorLogEvent(actorID, prefix, evt); err != nil {
					rt.Fatalf("append merge_ack: %v", err)
				}
			}
		}

		// Rebuild the view
		cfg, _ := svc.getTeamConfig()
		view, err := svc.rebuildView(cfg)
		if err != nil {
			rt.Fatalf("rebuildView: %v", err)
		}

		// Check status
		gotStatus := findIntentStatus(view, intentID)
		if gotStatus != wantStatus {
			rt.Fatalf("status mismatch:\n  events: %v\n  got:    %s\n  want:   %s",
				events, gotStatus, wantStatus)
		}
	})
}

// ---------------------------------------------------------------------------
// Property: multiple intents processed independently.
// Events for intent A should not affect intent B's status.
// ---------------------------------------------------------------------------

func TestPropertyRebuildViewIntentIsolation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}

		actorID := "actor_test"
		prefix := "_mainline/actor"

		// Create two intents with different event sequences
		intentA := "int_aaaa0001"
		intentB := "int_bbbb0002"

		// Seal both
		for i, id := range []string{intentA, intentB} {
			evt := domain.IntentSealedEvent{
				BaseEvent: domain.BaseEvent{
					EventID:       fmt.Sprintf("evt_seal_%d", i),
					SchemaVersion: 1,
					EventType:     domain.EventIntentSealed,
					ActorID:       actorID,
					Timestamp:     fmt.Sprintf("2026-01-01T00:0%d:00Z", i),
				},
				IntentID:   id,
				Goal:       fmt.Sprintf("goal %d", i),
				GitBranch:  "main",
				CodeCommit: fmt.Sprintf("code_%d", i),
				SealedAt:   core.Now(),
				Summary:    domain.IntentSummary{Title: "t", What: "w", Why: "y"},
				Fingerprint: domain.SemanticFingerprint{
					Subsystems:   []string{"test"},
					FilesTouched: []string{fmt.Sprintf("%d.go", i)},
				},
			}
			svc.Store.AppendActorLogEvent(actorID, prefix, evt)
		}

		// Abandon intent A
		svc.Store.AppendActorLogEvent(actorID, prefix, domain.IntentAbandonedEvent{
			BaseEvent: domain.BaseEvent{
				EventID: "evt_abandon_a", SchemaVersion: 1,
				EventType: domain.EventIntentAbandoned,
				ActorID:   actorID, Timestamp: "2026-01-01T00:05:00Z",
			},
			IntentID: intentA,
		})

		cfg, _ := svc.getTeamConfig()
		view, err := svc.rebuildView(cfg)
		if err != nil {
			rt.Fatalf("rebuildView: %v", err)
		}

		statusA := findIntentStatus(view, intentA)
		statusB := findIntentStatus(view, intentB)

		if statusA != domain.StatusAbandoned {
			rt.Fatalf("intentA should be abandoned, got %s", statusA)
		}
		if statusB != domain.StatusProposed {
			rt.Fatalf("intentB should be proposed (unaffected by A's abandon), got %s", statusB)
		}
	})
}

// ---------------------------------------------------------------------------
// Property: CheckJudgment last-write-wins.
// If multiple CheckJudgmentEvents arrive for the same candidate,
// the final one's data should be what the view shows.
// ---------------------------------------------------------------------------

func TestPropertyRebuildViewCheckJudgmentLastWriteWins(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}

		actorID := "actor_test"
		prefix := "_mainline/actor"
		intentID := "int_check_lww"

		// Seal the intent first
		svc.Store.AppendActorLogEvent(actorID, prefix, domain.IntentSealedEvent{
			BaseEvent: domain.BaseEvent{
				EventID: "evt_seal", SchemaVersion: 1,
				EventType: domain.EventIntentSealed,
				ActorID:   actorID, Timestamp: "2026-01-01T00:00:00Z",
			},
			IntentID:    intentID,
			Goal:        "test",
			GitBranch:   "main",
			CodeCommit:  "code1",
			SealedAt:    "2026-01-01T00:00:00Z",
			Summary:     domain.IntentSummary{Title: "t", What: "w", Why: "y"},
			Fingerprint: domain.SemanticFingerprint{Subsystems: []string{"s"}, FilesTouched: []string{"f"}},
		})

		// Write N check judgments with varying hasConflict
		nChecks := rapid.IntRange(2, 5).Draw(rt, "nChecks")
		var lastHasConflict bool
		var lastEventID string
		for i := 0; i < nChecks; i++ {
			hasConflict := rapid.Bool().Draw(rt, fmt.Sprintf("conflict%d", i))
			evtID := fmt.Sprintf("evt_check_%d", i)
			svc.Store.AppendActorLogEvent(actorID, prefix, domain.CheckJudgmentEvent{
				BaseEvent: domain.BaseEvent{
					EventID: evtID, SchemaVersion: 1,
					EventType: domain.EventCheckJudgment,
					ActorID:   actorID,
					Timestamp: fmt.Sprintf("2026-01-01T00:%02d:00Z", i+1),
				},
				CandidateIntent: intentID,
				Overall: domain.CheckOverall{
					HasConflict: hasConflict,
				},
			})
			lastHasConflict = hasConflict
			lastEventID = evtID
		}

		cfg, _ := svc.getTeamConfig()
		view, err := svc.rebuildView(cfg)
		if err != nil {
			rt.Fatalf("rebuildView: %v", err)
		}

		iv := findIntentView(view, intentID)
		if iv == nil {
			rt.Fatal("intent not in view")
		}
		if iv.LastCheck == nil {
			rt.Fatal("LastCheck is nil after check judgments")
		}
		if iv.LastCheck.HasConflict != lastHasConflict {
			rt.Fatalf("LastCheck.HasConflict: got %v, want %v (last event %s)",
				iv.LastCheck.HasConflict, lastHasConflict, lastEventID)
		}
		if iv.LastCheck.EventID != lastEventID {
			rt.Fatalf("LastCheck.EventID: got %s, want %s", iv.LastCheck.EventID, lastEventID)
		}
	})
}

// ---------------------------------------------------------------------------
// Property: events without a prior sealed event are silently skipped.
// Abandoned/superseded/merge_ack for an unknown intent should not create
// phantom entries in the view.
// ---------------------------------------------------------------------------

func TestPropertyRebuildViewOrphanEventsIgnored(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}

		actorID := "actor_test"
		prefix := "_mainline/actor"
		intentID := "int_orphan"

		// Write non-sealed events without a prior sealed event
		orphanTypes := []domain.EventType{
			domain.EventIntentAbandoned,
			domain.EventIntentSuperseded,
			domain.EventIntentMergeAcknowledged,
		}
		evtType := rapid.SampledFrom(orphanTypes).Draw(rt, "orphanType")

		switch evtType {
		case domain.EventIntentAbandoned:
			svc.Store.AppendActorLogEvent(actorID, prefix, domain.IntentAbandonedEvent{
				BaseEvent: domain.BaseEvent{
					EventID: "evt_orphan", SchemaVersion: 1,
					EventType: domain.EventIntentAbandoned,
					ActorID: actorID, Timestamp: "2026-01-01T00:00:00Z",
				},
				IntentID: intentID,
			})
		case domain.EventIntentSuperseded:
			svc.Store.AppendActorLogEvent(actorID, prefix, domain.IntentSupersededEvent{
				BaseEvent: domain.BaseEvent{
					EventID: "evt_orphan", SchemaVersion: 1,
					EventType: domain.EventIntentSuperseded,
					ActorID: actorID, Timestamp: "2026-01-01T00:00:00Z",
				},
				IntentID:     intentID,
				SupersededBy: "int_other",
			})
		case domain.EventIntentMergeAcknowledged:
			svc.Store.AppendActorLogEvent(actorID, prefix, domain.IntentMergeAcknowledgedEvent{
				BaseEvent: domain.BaseEvent{
					EventID: "evt_orphan", SchemaVersion: 1,
					EventType: domain.EventIntentMergeAcknowledged,
					ActorID: actorID, Timestamp: "2026-01-01T00:00:00Z",
				},
				IntentID:    intentID,
				MergeCommit: "merge_abc",
			})
		}

		cfg, _ := svc.getTeamConfig()
		view, err := svc.rebuildView(cfg)
		if err != nil {
			rt.Fatalf("rebuildView: %v", err)
		}

		// The orphan intent should NOT appear in the view
		if iv := findIntentView(view, intentID); iv != nil {
			rt.Fatalf("orphan event created phantom intent with status %s", iv.Status)
		}
	})
}

// ---------------------------------------------------------------------------
// Reference model and helpers
// ---------------------------------------------------------------------------

// refReplayStatus replays a sequence of event types for ONE intent and
// returns the expected final status, using the same semantics as
// rebuildView's event processing loop.
func refReplayStatus(events []domain.EventType) domain.IntentStatus {
	var exists bool
	var status domain.IntentStatus

	for _, evt := range events {
		switch evt {
		case domain.EventIntentSealed:
			// Creates/overwrites — always resets to proposed
			exists = true
			status = domain.StatusProposed
		case domain.EventIntentAbandoned:
			if exists {
				status = domain.StatusAbandoned
			}
		case domain.EventIntentSuperseded:
			if exists {
				status = domain.StatusSuperseded
			}
		case domain.EventIntentMergeAcknowledged:
			if exists {
				status = domain.StatusMerged
			}
		}
	}

	if !exists {
		return "" // intent never sealed → not in view
	}
	return status
}

func findIntentStatus(view *domain.MainlineView, intentID string) domain.IntentStatus {
	if iv := findIntentView(view, intentID); iv != nil {
		return iv.Status
	}
	return ""
}

func findIntentView(view *domain.MainlineView, intentID string) *domain.IntentView {
	if view == nil {
		return nil
	}
	for i := range view.Intents {
		if view.Intents[i].IntentID == intentID {
			return &view.Intents[i]
		}
	}
	return nil
}
