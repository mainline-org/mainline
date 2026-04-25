package engine

import (
	"fmt"
	"os"

	"mainline/internal/core"
	"mainline/internal/domain"
)

// -----------------------------------------------------------
// Start
// -----------------------------------------------------------

type StartResult struct {
	IntentID  string `json:"intent_id"`
	Thread    string `json:"thread"`
	GitBranch string `json:"git_branch"`
	Goal      string `json:"goal"`
}

func (s *Service) Start(goal string, thread string) (*StartResult, error) {
	if err := s.requireInit(); err != nil {
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
			IntentID:  existing.IntentID,
			Thread:    existing.Thread,
			GitBranch: existing.GitBranch,
			Goal:      existing.Goal,
		}, nil
	}

	if thread == "" {
		thread = branch
	}

	base, _ := s.Git.HeadCommit()
	intentID := core.GenerateIntentID()
	now := core.Now()

	draft := &domain.DraftIntent{
		IntentID:       intentID,
		SchemaVersion:  1,
		Status:         domain.StatusDrafting,
		Thread:         thread,
		GitBranch:      branch,
		BaseCommit:     base,
		Goal:           goal,
		Turns:          nil,
		CreatedAt:      now,
		LastModifiedAt: now,
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

	return &StartResult{
		IntentID:  intentID,
		Thread:    thread,
		GitBranch: branch,
		Goal:      goal,
	}, nil
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
	if err := s.requireInit(); err != nil {
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
		ID:          core.GenerateTurnID(),
		IntentID:    draft.IntentID,
		Index:       idx,
		CreatedAt:   core.Now(),
		Description: description,
		FilesChanged: changes,
		DiffStats:   stats,
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

	return &AppendResult{
		TurnID:   turn.ID,
		IntentID: draft.IntentID,
		Index:    idx,
	}, nil
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
	if err := s.requireInit(); err != nil {
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
		draft, _ = s.Store.FindActiveDraft(branch)
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

func (s *Service) Abandon(intentID string, reason string) error {
	if err := s.requireInit(); err != nil {
		return err
	}

	draft, err := s.Store.ReadDraft(intentID)
	if err != nil {
		return domain.NewError(domain.ErrNoActiveIntent, fmt.Sprintf("intent %s not found", intentID))
	}

	if err := core.ValidateStateTransition(draft.Status, domain.StatusAbandoned); err != nil {
		return domain.NewError(domain.ErrInvalidStatus, err.Error())
	}

	draft.Status = domain.StatusAbandoned
	draft.LastModifiedAt = core.Now()
	return s.Store.WriteDraft(draft)
}
