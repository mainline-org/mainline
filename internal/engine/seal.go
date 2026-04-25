package engine

import (
	"encoding/json"
	"fmt"

	"mainline/internal/core"
	"mainline/internal/domain"
)

// -----------------------------------------------------------
// Seal --prepare
// -----------------------------------------------------------

func (s *Service) SealPrepare(intentID string) (*domain.SealPreparePackage, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	var draft *domain.DraftIntent
	var err error

	if intentID != "" {
		draft, err = s.Store.ReadDraft(intentID)
	} else {
		branch, _ := s.Git.CurrentBranch()
		draft, err = s.Store.FindActiveDraft(branch)
	}
	if err != nil || draft == nil {
		return nil, domain.NewRecoverableError(domain.ErrNoActiveIntent,
			"no active intent found",
			"mainline start --goal 'your goal'",
		)
	}

	if draft.Status != domain.StatusDrafting {
		return nil, domain.NewError(domain.ErrInvalidStatus,
			fmt.Sprintf("intent %s is in status %s, expected drafting", draft.IntentID, draft.Status))
	}

	head, _ := s.Git.HeadCommit()
	stats, changes, _ := s.Git.DiffStatAgainst(draft.BaseCommit, head)
	changedFiles, _ := s.Git.DiffFilesAgainst(draft.BaseCommit, head)

	turns, _ := s.Store.ReadTurns(draft.IntentID)
	var turnSummaries []domain.TurnSummary
	for _, t := range turns {
		var files []string
		for _, fc := range t.FilesChanged {
			files = append(files, fc.Path)
		}
		turnSummaries = append(turnSummaries, domain.TurnSummary{
			Index:        t.Index,
			Description:  t.Description,
			FilesChanged: files,
		})
	}

	pkg := &domain.SealPreparePackage{
		Kind:          "mainline.seal.prepare",
		SchemaVersion: 1,
		Turns:         turnSummaries,
		ChangedFiles:  changes,
		Instruction:   sealInstruction(),
	}
	pkg.Intent.ID = draft.IntentID
	pkg.Intent.Goal = draft.Goal
	pkg.Intent.Thread = draft.Thread
	pkg.Intent.GitBranch = draft.GitBranch
	pkg.Intent.BaseCommit = draft.BaseCommit
	pkg.Intent.CurrentHead = head

	pkg.DiffSummary.Files = stats.Files
	pkg.DiffSummary.Added = stats.Added
	pkg.DiffSummary.Removed = stats.Removed
	pkg.DiffSummary.FilesChanged = changedFiles

	return pkg, nil
}

func sealInstruction() string {
	return `Analyze this intent and produce a SealResult JSON object with:
1. summary: title, what, why, user_goal, decisions, rejected alternatives, risks, followups
2. fingerprint: subsystems, files_touched, architectural_claims, behavioral_changes,
   api_changes, data_model_changes, security_implications, migration_notes, tags
3. confidence: summary (0-1), fingerprint (0-1)

Return ONLY valid JSON matching the SealResult schema.`
}

// -----------------------------------------------------------
// Seal --submit
// -----------------------------------------------------------

type SealSubmitResult struct {
	IntentID    string `json:"intent_id"`
	Status      string `json:"status"`
	Published   bool   `json:"published"`
	CodeCommit  string `json:"code_commit"`
	EventID     string `json:"event_id"`
	Hash        string `json:"canonical_hash"`
	Warning     string `json:"warning,omitempty"`
}

func (s *Service) SealSubmit(input json.RawMessage) (*SealSubmitResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	var sr domain.SealResult
	if err := json.Unmarshal(input, &sr); err != nil {
		return nil, domain.NewError(domain.ErrInvalidInput,
			fmt.Sprintf("invalid SealResult JSON: %v", err))
	}

	if err := core.ValidateSealResult(&sr); err != nil {
		return nil, domain.NewError(domain.ErrSealFailed, err.Error())
	}

	draft, err := s.Store.ReadDraft(sr.IntentID)
	if err != nil {
		return nil, domain.NewError(domain.ErrNoActiveIntent,
			fmt.Sprintf("intent %s not found", sr.IntentID))
	}

	if draft.Status != domain.StatusDrafting {
		return nil, domain.NewError(domain.ErrInvalidStatus,
			fmt.Sprintf("intent is in status %s, expected drafting", draft.Status))
	}

	head, _ := s.Git.HeadCommit()
	codeCommit := head

	// Transition to sealed_local
	draft.Status = domain.StatusSealedLocal
	draft.LastModifiedAt = core.Now()
	if err := s.Store.WriteDraft(draft); err != nil {
		return nil, fmt.Errorf("update draft: %w", err)
	}

	// Write sealed event to actor log
	identity, err := s.getIdentity()
	if err != nil {
		return nil, err
	}
	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}

	eventID := core.GenerateEventID()
	event := domain.IntentSealedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       eventID,
			SchemaVersion: 1,
			EventType:     domain.EventIntentSealed,
			ActorID:       identity.ActorID,
			Timestamp:     core.Now(),
		},
		IntentID:    sr.IntentID,
		Thread:      draft.Thread,
		Goal:        draft.Goal,
		GitBranch:   draft.GitBranch,
		BaseCommit:  draft.BaseCommit,
		CodeCommit:  codeCommit,
		Summary:     sr.Summary,
		Fingerprint: sr.Fingerprint,
		TurnCount:   len(draft.Turns),
		SealedAt:    core.Now(),
	}

	if err := s.Store.AppendActorLogEvent(identity.ActorID, cfg.Mainline.ActorLogPrefix, event); err != nil {
		return nil, fmt.Errorf("write actor log event: %w", err)
	}

	// Compute canonical hash
	hash, _ := core.CanonicalHash(sr)

	// Auto-publish: try to push actor log to remote
	published := false
	warning := ""
	finalStatus := domain.StatusSealedLocal

	if s.Git.HasRemote("origin") {
		ref := s.Store.ActorLogRef(identity.ActorID, cfg.Mainline.ActorLogPrefix)
		refspec := fmt.Sprintf("%s:%s", ref, ref)
		if err := s.Git.Push("origin", refspec); err == nil {
			published = true
			finalStatus = domain.StatusProposed
		} else {
			warning = "Sealed locally but failed to publish. Run 'mainline publish' to retry."
		}
	}

	// Update draft status to final status
	draft.Status = finalStatus
	draft.LastModifiedAt = core.Now()
	s.Store.WriteDraft(draft)

	return &SealSubmitResult{
		IntentID:   sr.IntentID,
		Status:     string(finalStatus),
		Published:  published,
		CodeCommit: codeCommit,
		EventID:    eventID,
		Hash:       hash,
		Warning:    warning,
	}, nil
}
