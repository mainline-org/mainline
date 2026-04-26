package engine

import (
	"fmt"
	"os"
	"strings"

	"github.com/mainline-org/mainline/internal/core"
	"github.com/mainline-org/mainline/internal/domain"
)

// -----------------------------------------------------------
// Start
// -----------------------------------------------------------

type StartResult struct {
	IntentID  string `json:"intent_id"`
	Thread    string `json:"thread"`
	GitBranch string `json:"git_branch"`
	Goal      string `json:"goal"`
	// v0.3 backfill: when the user passed --commits or --range, the
	// list of commits this intent will retroactively cover.
	BackfillCommits []string `json:"backfill_commits,omitempty"`
}

// StartOptions controls v0.3 backfill behaviour. Pass via StartWithOptions.
//
//	BackfillCommits: explicit commits this intent should cover after
//	                 seal. The seal flow records them on the sealed
//	                 event; Sync's auto-pin step pins the intent to
//	                 each one. Used for retroactively covering
//	                 pre-existing main commits ("mainline start --commits").
type StartOptions struct {
	BackfillCommits []string
}

// Start retains the original signature; new callers wanting the v0.3
// backfill flow use StartWithOptions.
func (s *Service) Start(goal string, thread string) (*StartResult, error) {
	return s.StartWithOptions(goal, thread, nil)
}

func (s *Service) StartWithOptions(goal string, thread string, opts *StartOptions) (*StartResult, error) {
	if _, err := s.requireIdentity(); err != nil {
		return nil, err
	}

	branch, err := s.Git.CurrentBranch()
	if err != nil {
		return nil, fmt.Errorf("get branch: %w", err)
	}

	// If thread already has a drafting intent, return existing (idempotent)
	existing, _ := s.Store.FindActiveDraft(branch)
	if existing != nil {
		return &StartResult{
			IntentID:        existing.IntentID,
			Thread:          existing.Thread,
			GitBranch:       existing.GitBranch,
			Goal:            existing.Goal,
			BackfillCommits: existing.BackfillCommits,
		}, nil
	}

	if thread == "" {
		thread = branch
	}

	base, _ := s.Git.HeadCommit()
	var backfill []string
	if opts != nil && len(opts.BackfillCommits) > 0 {
		// Backfill flow: base_commit is best-set to the parent of the
		// earliest listed commit so diff-stat semantics still make sense
		// to humans reading the seal output. Errors are tolerated —
		// downstream code does not require base for backfill intents.
		backfill = append([]string{}, opts.BackfillCommits...)
		if parent, err := s.Git.Run("rev-parse", backfill[0]+"^"); err == nil {
			base = strings.TrimSpace(parent)
		}
	}

	intentID := core.GenerateIntentID()
	now := core.Now()

	draft := &domain.DraftIntent{
		IntentID:        intentID,
		SchemaVersion:   1,
		Status:          domain.StatusDrafting,
		Thread:          thread,
		GitBranch:       branch,
		BaseCommit:      base,
		Goal:            goal,
		Turns:           nil,
		CreatedAt:       now,
		LastModifiedAt:  now,
		BackfillCommits: backfill,
	}

	if err := s.Store.WriteDraft(draft); err != nil {
		return nil, fmt.Errorf("write draft: %w", err)
	}

	// Ensure thread record exists
	t, _ := s.Store.ReadThread(thread)
	if t == nil {
		t = &domain.Thread{
			Name:       thread,
			GitBranch:  branch,
			BaseCommit: base,
			Intents:    []string{intentID},
			Status:     "active",
			CreatedAt:  now,
		}
		s.Store.WriteThread(t)
	} else {
		t.Intents = append(t.Intents, intentID)
		s.Store.WriteThread(t)
	}

	res := &StartResult{
		IntentID:        intentID,
		Thread:          thread,
		GitBranch:       branch,
		Goal:            goal,
		BackfillCommits: backfill,
	}
	s.emit("intent_started", res)
	return res, nil
}

// -----------------------------------------------------------
// Append
// -----------------------------------------------------------

type AppendResult struct {
	TurnID   string `json:"turn_id"`
	IntentID string `json:"intent_id"`
	Index    int    `json:"index"`
}

func (s *Service) Append(description string) (*AppendResult, error) {
	if _, err := s.requireIdentity(); err != nil {
		return nil, err
	}

	branch, err := s.Git.CurrentBranch()
	if err != nil {
		return nil, fmt.Errorf("get branch: %w", err)
	}

	draft, err := s.Store.FindActiveDraft(branch)
	if err != nil || draft == nil {
		return nil, domain.NewRecoverableError(domain.ErrNoActiveIntent,
			"no active intent on current branch",
			"mainline start --goal 'your goal'",
		)
	}

	// Compute diff stats from base commit
	head, _ := s.Git.HeadCommit()
	stats, changes, _ := s.Git.DiffStatAgainst(draft.BaseCommit, head)

	existingTurns, _ := s.Store.ReadTurns(draft.IntentID)
	idx := len(existingTurns)

	cwd, _ := os.Getwd()
	pid := os.Getpid()

	turn := &domain.Turn{
		ID:           core.GenerateTurnID(),
		IntentID:     draft.IntentID,
		Index:        idx,
		CreatedAt:    core.Now(),
		Description:  description,
		FilesChanged: changes,
		DiffStats:    stats,
		Caller: domain.CallerInfo{
			PID: pid,
			Cwd: cwd,
		},
	}

	if err := s.Store.AppendTurn(turn); err != nil {
		return nil, fmt.Errorf("append turn: %w", err)
	}

	// Update draft
	draft.LastModifiedAt = core.Now()
	draft.Turns = append(draft.Turns, *turn)
	if err := s.Store.WriteDraft(draft); err != nil {
		return nil, fmt.Errorf("update draft: %w", err)
	}

	res := &AppendResult{
		TurnID:   turn.ID,
		IntentID: draft.IntentID,
		Index:    idx,
	}
	s.emit("turn_appended", res)
	return res, nil
}

// -----------------------------------------------------------
// AppendWithAutoStart creates an intent if none exists, then appends.
// -----------------------------------------------------------

type AppendAutoResult struct {
	TurnID        string `json:"turn_id"`
	IntentID      string `json:"intent_id"`
	Index         int    `json:"index"`
	IntentCreated bool   `json:"intent_created"`
}

func (s *Service) AppendWithAutoStart(description, goal string) (*AppendAutoResult, error) {
	if _, err := s.requireIdentity(); err != nil {
		return nil, err
	}

	branch, _ := s.Git.CurrentBranch()
	draft, _ := s.Store.FindActiveDraft(branch)

	created := false
	if draft == nil {
		_, err := s.Start(goal, "")
		if err != nil {
			return nil, err
		}
		created = true
	}

	result, err := s.Append(description)
	if err != nil {
		return nil, err
	}

	return &AppendAutoResult{
		TurnID:        result.TurnID,
		IntentID:      result.IntentID,
		Index:         result.Index,
		IntentCreated: created,
	}, nil
}

// -----------------------------------------------------------
// Show
// -----------------------------------------------------------

type ShowResult struct {
	Intent *domain.DraftIntent `json:"intent,omitempty"`
	View   *domain.IntentView  `json:"view,omitempty"`
	Turns  []domain.Turn       `json:"turns,omitempty"`
}

func (s *Service) Show(intentID string) (*ShowResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	// Try local draft first
	draft, err := s.Store.ReadDraft(intentID)
	if err == nil && draft != nil {
		turns, _ := s.Store.ReadTurns(intentID)
		return &ShowResult{Intent: draft, Turns: turns}, nil
	}

	// Try mainline view
	view, _ := s.Store.ReadMainlineView()
	if view != nil {
		for _, iv := range view.Intents {
			if iv.IntentID == intentID {
				return &ShowResult{View: &iv}, nil
			}
		}
	}

	return nil, domain.NewError(domain.ErrInvalidInput, fmt.Sprintf("intent %s not found", intentID))
}

// -----------------------------------------------------------
// Abandon
// -----------------------------------------------------------

// AbandonResult is what the CLI / agents need to display after an
// abandon: the prior state matters (drafting was local-only;
// sealed/proposed wrote an event), and Published tells the user
// whether other actors will see this on their next sync.
type AbandonResult struct {
	IntentID    string `json:"intent_id"`
	PriorStatus string `json:"prior_status"`
	Reason      string `json:"reason,omitempty"`
	EventID     string `json:"event_id,omitempty"`
	Published   bool   `json:"published"`
	Warning     string `json:"warning,omitempty"`
}

// Abandon transitions an intent to the abandoned terminal state.
//
// State-dependent behaviour:
//   - drafting: local-only; nothing was published, so we just delete
//     the local draft and return. No event written.
//   - sealed_local / proposed: writes an IntentAbandonedEvent to the
//     actor log and auto-publishes. Without this, sync's view-rebuild
//     in OTHER clones would keep showing the intent as proposed
//     forever — a silent bug pre-v0.3 the CLI surface forced into
//     the open.
func (s *Service) Abandon(intentID string, reason string) (*AbandonResult, error) {
	if _, err := s.requireIdentity(); err != nil {
		return nil, err
	}

	draft, err := s.Store.ReadDraft(intentID)
	if err != nil {
		return nil, domain.NewError(domain.ErrNoActiveIntent, fmt.Sprintf("intent %s not found", intentID))
	}

	if err := core.ValidateStateTransition(draft.Status, domain.StatusAbandoned); err != nil {
		return nil, domain.NewError(domain.ErrInvalidStatus, err.Error())
	}

	res := &AbandonResult{
		IntentID:    intentID,
		PriorStatus: string(draft.Status),
		Reason:      reason,
	}

	// Drafting → fully local. No event needed; the draft files are
	// the entire footprint. Delete them (DeleteDraft also wipes the
	// turns + prepare snapshot if any).
	if draft.Status == domain.StatusDrafting {
		if err := s.Store.DeleteDraft(intentID); err != nil {
			return nil, fmt.Errorf("delete draft: %w", err)
		}
		return res, nil
	}

	// Sealed/proposed → write an actor-log event so cross-actor sync
	// rebuilds the view with status=abandoned for everyone, not just
	// this clone.
	identity, err := s.getIdentity()
	if err != nil {
		return nil, err
	}
	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}
	eventID := core.GenerateEventID()
	event := domain.IntentAbandonedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       eventID,
			SchemaVersion: 1,
			EventType:     domain.EventIntentAbandoned,
			ActorID:       identity.ActorID,
			ActorName:     s.actorDisplayName(identity),
			Timestamp:     core.Now(),
		},
		IntentID: intentID,
		Reason:   reason,
	}
	if err := s.Store.AppendActorLogEvent(identity.ActorID, cfg.Mainline.ActorLogPrefix, event); err != nil {
		return nil, fmt.Errorf("write actor log event: %w", err)
	}
	res.EventID = eventID

	// Auto-publish so other actors see the abandon promptly. Mirrors
	// the seal --submit auto-publish path; failures are non-fatal
	// (the event is in the local actor log and will publish on next
	// `mainline publish` or `seal --submit`).
	if s.Git.HasRemote(s.remoteName()) {
		ref := s.Store.ActorLogRef(identity.ActorID, cfg.Mainline.ActorLogPrefix)
		refspec := fmt.Sprintf("%s:%s", ref, ref)
		if err := s.Git.Push(s.remoteName(), refspec); err == nil {
			res.Published = true
		} else {
			res.Warning = "Abandoned locally but failed to publish. Run `mainline publish` to retry."
		}
	} else {
		res.Warning = "No remote configured. Run `mainline publish` once you set one up."
	}

	// Update the local draft last so the file mirrors the event we
	// just wrote — keeps `mainline status` and `show` in sync without
	// waiting for a Sync round-trip.
	draft.Status = domain.StatusAbandoned
	draft.LastModifiedAt = core.Now()
	if err := s.Store.WriteDraft(draft); err != nil {
		return nil, fmt.Errorf("update draft: %w", err)
	}

	return res, nil
}
