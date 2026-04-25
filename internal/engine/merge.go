package engine

import (
	"fmt"
	"strings"

	"mainline/internal/core"
	"mainline/internal/domain"
)

// -----------------------------------------------------------
// Publish
// -----------------------------------------------------------

type PublishResult struct {
	IntentID string `json:"intent_id"`
	Ref      string `json:"ref"`
	Pushed   bool   `json:"pushed"`
}

func (s *Service) Publish(intentID string) (*PublishResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	if intentID == "" {
		branch, _ := s.Git.CurrentBranch()
		drafts, _ := s.Store.ListDrafts()
		for _, id := range drafts {
			d, _ := s.Store.ReadDraft(id)
			if d != nil && d.GitBranch == branch && d.Status == domain.StatusSealedLocal {
				intentID = id
				break
			}
		}
	}

	if intentID == "" {
		return nil, domain.NewRecoverableError(domain.ErrNoActiveIntent,
			"no sealed intent found to publish",
			"mainline seal --prepare",
		)
	}

	identity, err := s.getIdentity()
	if err != nil {
		return nil, err
	}
	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}

	ref := s.Store.ActorLogRef(identity.ActorID, cfg.Mainline.ActorLogPrefix)

	pushed := false
	if s.Git.HasRemote("origin") {
		refspec := fmt.Sprintf("%s:%s", ref, ref)
		if err := s.Git.Push("origin", refspec); err == nil {
			pushed = true
		}
	}

	// Update draft status
	draft, _ := s.Store.ReadDraft(intentID)
	if draft != nil && draft.Status == domain.StatusSealedLocal {
		draft.Status = domain.StatusProposed
		draft.LastModifiedAt = core.Now()
		s.Store.WriteDraft(draft)
	}

	return &PublishResult{
		IntentID: intentID,
		Ref:      ref,
		Pushed:   pushed,
	}, nil
}

// -----------------------------------------------------------
// Merge
// -----------------------------------------------------------

type MergeResult struct {
	IntentID    string `json:"intent_id"`
	MergeCommit string `json:"merge_commit"`
	Strategy    string `json:"strategy"`
}

func (s *Service) Merge(intentID string) (*MergeResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}

	// Find the intent
	draft, _ := s.Store.ReadDraft(intentID)
	if draft == nil {
		return nil, domain.NewError(domain.ErrNoActiveIntent,
			fmt.Sprintf("intent %s not found", intentID))
	}

	if draft.Status != domain.StatusSealedLocal && draft.Status != domain.StatusProposed {
		return nil, domain.NewError(domain.ErrInvalidStatus,
			fmt.Sprintf("intent %s is in status %s, expected sealed_local or proposed", intentID, draft.Status))
	}

	// Perform merge using configured strategy
	strategy := cfg.Merge.Strategy
	branch := draft.GitBranch
	mainBranch := cfg.Mainline.MainBranch

	var mergeCommit string
	var mergeErr error

	switch strategy {
	case "squash":
		mergeCommit, mergeErr = s.squashMerge(branch, mainBranch, draft)
	case "merge":
		mergeCommit, mergeErr = s.regularMerge(branch, mainBranch, draft)
	default:
		mergeCommit, mergeErr = s.squashMerge(branch, mainBranch, draft)
	}

	if mergeErr != nil {
		return nil, domain.NewError(domain.ErrMergeFailed, mergeErr.Error())
	}

	// Update draft status
	draft.Status = domain.StatusMerged
	draft.LastModifiedAt = core.Now()
	s.Store.WriteDraft(draft)

	return &MergeResult{
		IntentID:    intentID,
		MergeCommit: mergeCommit,
		Strategy:    strategy,
	}, nil
}

func (s *Service) squashMerge(branch, mainBranch string, draft *domain.DraftIntent) (string, error) {
	// Checkout main, squash merge the branch
	if _, err := s.gitRun("checkout", mainBranch); err != nil {
		return "", fmt.Errorf("checkout %s: %w", mainBranch, err)
	}

	title := draft.Goal
	message := fmt.Sprintf("%s\n\nMainline-Intent: %s\nMainline-Thread: %s\n",
		title, draft.IntentID, draft.Thread)

	if _, err := s.gitRun("merge", "--squash", branch); err != nil {
		// Attempt to abort on failure
		s.gitRun("merge", "--abort")
		s.gitRun("checkout", branch)
		return "", fmt.Errorf("squash merge failed: %w", err)
	}

	if _, err := s.gitRun("commit", "-m", message); err != nil {
		s.gitRun("checkout", branch)
		return "", fmt.Errorf("commit failed: %w", err)
	}

	head, _ := s.Git.HeadCommit()

	// Return to original branch
	s.gitRun("checkout", branch)

	return head, nil
}

func (s *Service) regularMerge(branch, mainBranch string, draft *domain.DraftIntent) (string, error) {
	if _, err := s.gitRun("checkout", mainBranch); err != nil {
		return "", fmt.Errorf("checkout %s: %w", mainBranch, err)
	}

	message := fmt.Sprintf("Merge %s: %s\n\nMainline-Intent: %s\nMainline-Thread: %s\n",
		branch, draft.Goal, draft.IntentID, draft.Thread)

	if _, err := s.gitRun("merge", "--no-ff", branch, "-m", message); err != nil {
		s.gitRun("merge", "--abort")
		s.gitRun("checkout", branch)
		return "", fmt.Errorf("merge failed: %w", err)
	}

	head, _ := s.Git.HeadCommit()
	s.gitRun("checkout", branch)
	return head, nil
}

func (s *Service) gitRun(args ...string) (string, error) {
	return s.Git.Run(args...)
}

// -----------------------------------------------------------
// Reconcile
// -----------------------------------------------------------

type ReconcileResult struct {
	Reconciled int      `json:"reconciled"`
	IntentIDs  []string `json:"intent_ids"`
}

func (s *Service) Reconcile() (*ReconcileResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}
	identity, err := s.getIdentity()
	if err != nil {
		return nil, err
	}

	view, _ := s.Store.ReadMainlineView()
	if view == nil {
		return &ReconcileResult{}, nil
	}

	var reconciled []string
	for _, iv := range view.Intents {
		if iv.Status != domain.StatusMerged {
			continue
		}
		if iv.StatusEvidence.MergedConfidence == "acknowledged" {
			continue
		}
		if iv.ActorID != identity.ActorID {
			continue
		}

		// Write merge acknowledged event
		eventID := core.GenerateEventID()
		event := domain.IntentMergeAcknowledgedEvent{
			BaseEvent: domain.BaseEvent{
				EventID:       eventID,
				SchemaVersion: 1,
				EventType:     domain.EventIntentMergeAcknowledged,
				ActorID:       identity.ActorID,
				Timestamp:     core.Now(),
			},
			IntentID:    iv.IntentID,
			MergeCommit: iv.StatusEvidence.MergedMainCommit,
		}
		s.Store.AppendActorLogEvent(identity.ActorID, cfg.Mainline.ActorLogPrefix, event)
		reconciled = append(reconciled, iv.IntentID)
	}

	return &ReconcileResult{
		Reconciled: len(reconciled),
		IntentIDs:  reconciled,
	}, nil
}

// -----------------------------------------------------------
// PR helpers
// -----------------------------------------------------------

func (s *Service) PRTrailer(intentID string) (string, error) {
	if err := s.requireInit(); err != nil {
		return "", err
	}

	draft, _ := s.Store.ReadDraft(intentID)
	if draft == nil {
		return "", domain.NewError(domain.ErrNoActiveIntent, fmt.Sprintf("intent %s not found", intentID))
	}

	trailer := fmt.Sprintf("Mainline-Intent: %s\nMainline-Thread: %s", draft.IntentID, draft.Thread)
	return trailer, nil
}

func (s *Service) PRDescription(intentID string) (string, error) {
	if err := s.requireInit(); err != nil {
		return "", err
	}

	// Try to find summary from sealed event in view
	view, _ := s.Store.ReadMainlineView()
	if view != nil {
		for _, iv := range view.Intents {
			if iv.IntentID == intentID && iv.Summary != nil {
				return formatPRDescription(iv.IntentID, iv.Summary, iv.Fingerprint), nil
			}
		}
	}

	// Fallback to draft info
	draft, _ := s.Store.ReadDraft(intentID)
	if draft != nil {
		return fmt.Sprintf("## %s\n\n**Goal:** %s\n\n---\nMainline-Intent: %s\nMainline-Thread: %s\n",
			draft.Goal, draft.Goal, draft.IntentID, draft.Thread), nil
	}

	return "", domain.NewError(domain.ErrNoActiveIntent, fmt.Sprintf("intent %s not found", intentID))
}

func formatPRDescription(intentID string, summary *domain.IntentSummary, fp *domain.SemanticFingerprint) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %s\n\n", summary.Title))
	sb.WriteString(fmt.Sprintf("**What:** %s\n\n", summary.What))
	sb.WriteString(fmt.Sprintf("**Why:** %s\n\n", summary.Why))

	if len(summary.Decisions) > 0 {
		sb.WriteString("### Decisions\n")
		for _, d := range summary.Decisions {
			sb.WriteString(fmt.Sprintf("- **%s**: chose %s", d.Point, d.Chose))
			if d.Rationale != "" {
				sb.WriteString(fmt.Sprintf(" (%s)", d.Rationale))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if len(summary.Risks) > 0 {
		sb.WriteString("### Risks\n")
		for _, r := range summary.Risks {
			sb.WriteString(fmt.Sprintf("- %s\n", r))
		}
		sb.WriteString("\n")
	}

	if fp != nil && len(fp.Subsystems) > 0 {
		sb.WriteString(fmt.Sprintf("**Subsystems:** %s\n\n", strings.Join(fp.Subsystems, ", ")))
	}

	sb.WriteString(fmt.Sprintf("---\nMainline-Intent: %s\n", intentID))
	return sb.String()
}
