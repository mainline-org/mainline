package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mainline-org/mainline/internal/domain"
)

// -----------------------------------------------------------
// trace (rc7)
// -----------------------------------------------------------
//
// `mainline trace` is the timeline view of one intent: when each turn
// was recorded, how long elapsed between them, what the seal/abandon/
// supersede event landed.
//
// `show` answers "what did this intent decide?" (decisions, risks,
// fingerprint). `trace` answers "how did this intent unfold over
// time?". Three commands, three orthogonal jobs:
//
//   log    — cross-intent list (horizontal)
//   show   — one intent's semantic conclusions (vertical, summary)
//   trace  — one intent's time series (vertical, timeline)
//
// Data sources, per the rc7 spec's principle of "no new schema":
//
//   - own intents: .ml-cache/drafts/<id>.json (CreatedAt = start)
//                  .ml-cache/drafts/<id>.turns.jsonl (append turns
//                  with their own timestamps)
//   - actor log:   IntentSealedEvent / IntentAbandonedEvent /
//                  IntentSupersededEvent / IntentMergeAcknowledgedEvent
//                  (terminal lifecycle events, both for own and
//                  cross-actor intents)
//
// Note: actor log has NO IntentStartedEvent or IntentAppendedEvent —
// start/append are local-only operations on the drafts directory. So
// cross-actor traces show start (synthesised from sealed_at as a
// floor) + the seal event only; per-append detail is not available
// for intents whose drafts live on someone else's machine.

// TraceTurnType identifies which lifecycle event a turn corresponds to.
type TraceTurnType string

const (
	TraceTurnStart            TraceTurnType = "start"
	TraceTurnAppend           TraceTurnType = "append"
	TraceTurnSeal             TraceTurnType = "seal"
	TraceTurnAbandon          TraceTurnType = "abandon"
	TraceTurnSupersede        TraceTurnType = "supersede"
	TraceTurnMergeAcknowledge TraceTurnType = "merge_acknowledged"
)

// TraceTurn is one row of the timeline.
type TraceTurn struct {
	Index                  int                    `json:"index"`
	Type                   TraceTurnType          `json:"type"`
	Timestamp              string                 `json:"timestamp"`
	ElapsedFromStartSec    int64                  `json:"elapsed_from_start_seconds"`
	ElapsedFromPreviousSec int64                  `json:"elapsed_from_previous_seconds"`
	Description            string                 `json:"description"`
	Metadata               map[string]interface{} `json:"metadata,omitempty"`
}

// TraceResult is the full timeline view of one intent.
type TraceResult struct {
	IntentID         string `json:"intent_id"`
	Title            string `json:"title,omitempty"`
	Status           string `json:"status"`
	StatusReason     string `json:"status_reason,omitempty"`
	SupersededBy     string `json:"superseded_by,omitempty"`
	ActorID          string `json:"actor_id,omitempty"`
	ActorName        string `json:"actor_display_name,omitempty"`
	Thread           string `json:"thread,omitempty"`
	GitBranch        string `json:"git_branch,omitempty"`
	BaseCommit       string `json:"base_commit,omitempty"`
	CodeCommit       string `json:"code_commit,omitempty"`
	MergedMainCommit string `json:"merged_main_commit,omitempty"`

	StartedAt       string `json:"started_at,omitempty"`
	SealedAt        string `json:"sealed_at,omitempty"`
	DurationSeconds int64  `json:"duration_seconds,omitempty"`

	Turns []TraceTurn `json:"turns"`

	Summary TraceSummary `json:"summary"`
}

// TraceSummary is the per-intent rollup. Per the rc7 principle
// "v1 不要求 diff stats", FilesTouched carries paths only.
type TraceSummary struct {
	TotalTurns        int      `json:"total_turns"`
	FilesTouchedCount int      `json:"files_touched_count,omitempty"`
	FilesTouched      []string `json:"files_touched,omitempty"`

	// AppendTurnsRecordedTogether is the rc7 "honest signal" field:
	// when all append turns share the same second timestamp, they
	// were batch-written right before seal rather than as live
	// progress events. Not a warning — turn discipline as currently
	// designed normalises to this. Renders as a one-line note.
	AppendTurnsRecordedTogether bool `json:"append_turns_recorded_together"`

	// CrossActor indicates the trace lacks per-append detail because
	// the intent's drafts directory lives on another actor's machine.
	// Sealed metadata (files_touched, decisions count) is still
	// available; the timeline is reduced to start + terminal event.
	CrossActor bool `json:"cross_actor"`

	// LimitApplied is set when --limit truncated the timeline.
	// TotalTurns reports the un-truncated count for accuracy.
	LimitApplied bool `json:"limit_applied,omitempty"`
}

// TraceOptions controls Trace's behaviour.
type TraceOptions struct {
	// Limit, if > 0, caps how many turns are returned. The full count
	// is preserved in Summary.TotalTurns and Summary.LimitApplied is
	// set so the CLI can render "(showing N of M)".
	Limit int
}

// Trace returns the timeline + sealed metadata for one intent.
//
// intentRef may be a full id ("int_abc12345") or a prefix
// ("int_abc1"). Prefix is resolved against the live mainline view —
// if exactly one matches it wins; multiple match returns an
// AMBIGUOUS_PREFIX error listing all candidates.
func (s *Service) Trace(intentRef string, opts *TraceOptions) (*TraceResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}
	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}

	intentID, err := s.resolveIntentID(intentRef)
	if err != nil {
		return nil, err
	}

	res := &TraceResult{
		IntentID: intentID,
		Status:   "unknown",
	}

	// Pull view metadata first so we know who owns this intent and
	// what status it has. The actor log walk fills in timestamps;
	// the view fills in semantic context.
	view, _ := s.Store.ReadMainlineView()
	var iv *domain.IntentView
	if view != nil {
		for i := range view.Intents {
			if view.Intents[i].IntentID == intentID {
				iv = &view.Intents[i]
				break
			}
		}
	}
	if iv != nil {
		if iv.Summary != nil {
			res.Title = iv.Summary.Title
		}
		res.Status = string(iv.Status)
		res.ActorID = iv.ActorID
		res.ActorName = iv.ActorName
		res.Thread = iv.Thread
		res.GitBranch = iv.GitBranch
		res.BaseCommit = iv.BaseCommit
		res.CodeCommit = iv.CodeCommit
		res.MergedMainCommit = iv.StatusEvidence.MergedMainCommit
		res.SealedAt = iv.SealedAt
		res.SupersededBy = iv.StatusEvidence.SupersededByIntent
	}

	// --- Build the timeline ----------------------------------------
	var turns []TraceTurn

	// Local draft path. If we own this intent and the draft files
	// still exist, we get full per-turn detail with real timestamps.
	draft, _ := s.Store.ReadDraft(intentID)
	if draft != nil {
		turns = append(turns, TraceTurn{
			Type:        TraceTurnStart,
			Timestamp:   draft.CreatedAt,
			Description: draft.Goal,
		})
		if res.StartedAt == "" {
			res.StartedAt = draft.CreatedAt
		}
		if res.Status == "unknown" {
			res.Status = string(draft.Status)
		}
		if res.Title == "" {
			res.Title = draft.Goal
		}
		// Read append turns from the JSONL log. ReadTurns returns
		// them in insertion order, which is also chronological since
		// the JSONL is append-only.
		appendTurns, _ := s.Store.ReadTurns(intentID)
		for _, t := range appendTurns {
			turns = append(turns, TraceTurn{
				Type:        TraceTurnAppend,
				Timestamp:   t.CreatedAt,
				Description: t.Description,
			})
		}
	}

	// Actor-log walk: pick out terminal events for this intent.
	events, _ := s.collectAllEvents(cfg.Mainline.ActorLogPrefix)
	for _, raw := range events {
		var base domain.BaseEvent
		if err := json.Unmarshal(raw, &base); err != nil {
			continue
		}
		switch base.EventType {
		case domain.EventIntentSealed:
			var evt domain.IntentSealedEvent
			if err := json.Unmarshal(raw, &evt); err != nil {
				continue
			}
			if evt.IntentID != intentID {
				continue
			}
			// If we don't have a local draft, derive a synthetic
			// start. The honest answer is "we don't know when this
			// started" — surface that to the caller via CrossActor=
			// true and use SealedAt as a placeholder so the timeline
			// is still ordered.
			if draft == nil {
				res.Summary.CrossActor = true
				if res.StartedAt == "" {
					res.StartedAt = evt.SealedAt
				}
				turns = append(turns, TraceTurn{
					Type:      TraceTurnStart,
					Timestamp: evt.SealedAt,
					Description: fmt.Sprintf(
						"(start time not available — cross-actor intent; %d append turn(s) recorded by author)",
						evt.TurnCount),
				})
			}
			res.SealedAt = evt.SealedAt
			res.Summary.FilesTouched = evt.Fingerprint.FilesTouched
			res.Summary.FilesTouchedCount = len(evt.Fingerprint.FilesTouched)
			turns = append(turns, TraceTurn{
				Type:        TraceTurnSeal,
				Timestamp:   evt.SealedAt,
				Description: "Sealed",
				Metadata: map[string]interface{}{
					"files_touched_count": len(evt.Fingerprint.FilesTouched),
					"decisions_count":     len(evt.Summary.Decisions),
					"risks_count":         len(evt.Summary.Risks),
					"turn_count":          evt.TurnCount,
					"evidence_complete":   evt.EvidenceComplete,
					"worktree_status":     evt.WorktreeStatus,
				},
			})
			if res.ActorID == "" {
				res.ActorID = evt.ActorID
				res.ActorName = evt.ActorName
				res.Thread = evt.Thread
				res.GitBranch = evt.GitBranch
				res.BaseCommit = evt.BaseCommit
				res.CodeCommit = evt.CodeCommit
				res.Title = evt.Summary.Title
			}

		case domain.EventIntentAbandoned:
			var evt domain.IntentAbandonedEvent
			if err := json.Unmarshal(raw, &evt); err != nil {
				continue
			}
			if evt.IntentID != intentID {
				continue
			}
			meta := map[string]interface{}{}
			if evt.Reason != "" {
				meta["reason"] = evt.Reason
				res.StatusReason = evt.Reason
			}
			turns = append(turns, TraceTurn{
				Type:        TraceTurnAbandon,
				Timestamp:   evt.Timestamp,
				Description: "Abandoned",
				Metadata:    meta,
			})

		case domain.EventIntentSuperseded:
			var evt domain.IntentSupersededEvent
			if err := json.Unmarshal(raw, &evt); err != nil {
				continue
			}
			if evt.IntentID != intentID {
				continue
			}
			meta := map[string]interface{}{
				"superseded_by": evt.SupersededBy,
			}
			if evt.Reason != "" {
				meta["reason"] = evt.Reason
			}
			res.SupersededBy = evt.SupersededBy
			turns = append(turns, TraceTurn{
				Type:        TraceTurnSupersede,
				Timestamp:   evt.Timestamp,
				Description: fmt.Sprintf("Superseded by %s", evt.SupersededBy),
				Metadata:    meta,
			})

		case domain.EventIntentMergeAcknowledged:
			var evt domain.IntentMergeAcknowledgedEvent
			if err := json.Unmarshal(raw, &evt); err != nil {
				continue
			}
			if evt.IntentID != intentID {
				continue
			}
			turns = append(turns, TraceTurn{
				Type:      TraceTurnMergeAcknowledge,
				Timestamp: evt.Timestamp,
				Description: fmt.Sprintf(
					"Merge acknowledged at %s", short(evt.MergeCommit)),
				Metadata: map[string]interface{}{
					"merge_commit": evt.MergeCommit,
				},
			})
		}
	}

	if len(turns) == 0 {
		return nil, domain.NewRecoverableError(
			domain.ErrNoActiveIntent,
			fmt.Sprintf("intent %s has no recorded events (not in any local actor log)", intentID),
			"check `mainline log` for the intent id",
			"`mainline sync` to fetch remote actor logs",
		)
	}

	// Sort by timestamp; SliceStable so input order is preserved when
	// timestamps tie (matches the rc7 spec's batched-turn note).
	sort.SliceStable(turns, func(i, j int) bool {
		return turns[i].Timestamp < turns[j].Timestamp
	})

	// Assign 1-based indices in display order.
	for i := range turns {
		turns[i].Index = i + 1
	}

	// Compute elapsed-from-start and elapsed-from-previous (seconds)
	// in a single pass. Parse each timestamp once.
	parsed := make([]time.Time, len(turns))
	for i, t := range turns {
		parsed[i], _ = time.Parse(time.RFC3339, t.Timestamp)
	}
	if len(parsed) > 0 {
		start := parsed[0]
		var prev time.Time = start
		for i := range turns {
			if !parsed[i].IsZero() && !start.IsZero() {
				turns[i].ElapsedFromStartSec = int64(parsed[i].Sub(start).Seconds())
			}
			if i > 0 && !parsed[i].IsZero() && !prev.IsZero() {
				turns[i].ElapsedFromPreviousSec = int64(parsed[i].Sub(prev).Seconds())
			}
			prev = parsed[i]
		}
	}

	// Detect "all append turns recorded in the same second" — the rc7
	// honest-signal flag. Only meaningful when there are 2+ append
	// turns; with 0 or 1 the question doesn't apply.
	var appendTimestamps []string
	for _, t := range turns {
		if t.Type == TraceTurnAppend {
			appendTimestamps = append(appendTimestamps, t.Timestamp)
		}
	}
	if len(appendTimestamps) >= 2 {
		first := appendTimestamps[0]
		allSame := true
		for _, ts := range appendTimestamps[1:] {
			if ts != first {
				allSame = false
				break
			}
		}
		res.Summary.AppendTurnsRecordedTogether = allSame
	}

	// Duration: start → seal (or terminal event if no seal).
	if len(parsed) >= 2 {
		startTime := parsed[0]
		endTime := parsed[len(parsed)-1]
		if !startTime.IsZero() && !endTime.IsZero() {
			res.DurationSeconds = int64(endTime.Sub(startTime).Seconds())
		}
	}

	// Apply limit AFTER computing summary fields, so the rollup is
	// honest about the un-truncated state of the world.
	res.Summary.TotalTurns = len(turns)
	if opts != nil && opts.Limit > 0 && opts.Limit < len(turns) {
		turns = turns[:opts.Limit]
		res.Summary.LimitApplied = true
	}
	res.Turns = turns

	return res, nil
}

// resolveIntentID accepts either a full intent id or a prefix and
// returns the canonical full id. The view is the authoritative source
// of "which intents are known"; drafts directory is a fallback for
// intents that haven't been sync'd / sealed yet.
func (s *Service) resolveIntentID(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", domain.NewError(domain.ErrInvalidInput, "intent id is required")
	}

	// Collect all known ids: view + local drafts.
	seen := make(map[string]bool)
	var all []string
	if view, _ := s.Store.ReadMainlineView(); view != nil {
		for _, iv := range view.Intents {
			if !seen[iv.IntentID] {
				seen[iv.IntentID] = true
				all = append(all, iv.IntentID)
			}
		}
	}
	if drafts, _ := s.Store.ListDrafts(); len(drafts) > 0 {
		for _, id := range drafts {
			if !seen[id] {
				seen[id] = true
				all = append(all, id)
			}
		}
	}

	// Exact match wins.
	for _, id := range all {
		if id == ref {
			return id, nil
		}
	}

	// Prefix match.
	var matches []string
	for _, id := range all {
		if strings.HasPrefix(id, ref) {
			matches = append(matches, id)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", domain.NewRecoverableError(
			domain.ErrNoActiveIntent,
			fmt.Sprintf("intent %q not found", ref),
			"`mainline log` to list known intents",
			"`mainline sync` to fetch remote intents",
		)
	default:
		return "", domain.NewRecoverableError(
			domain.ErrInvalidInput,
			fmt.Sprintf("ambiguous prefix %q matches %d intents: %s",
				ref, len(matches), strings.Join(matches, ", ")),
			"use a longer prefix or the full intent id",
		)
	}
}
