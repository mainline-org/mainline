package engine

import (
	"encoding/json"
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

	// rc3: write git note to the merge commit (not trailer)
	identity, _ := s.getIdentity()
	hash, _ := core.CanonicalHash(draft)
	note := domain.CommitNote{
		SchemaVersion: 1,
		Kind:          "mainline.commit_note",
		Intents: []domain.IntentReference{
			{IntentID: draft.IntentID, SealResultHash: "sha256:" + hash},
		},
		AddedAt: core.Now(),
		AddedBy: identity.ActorID,
		Via:     "merge",
	}
	noteJSON, _ := json.Marshal(note)
	if err := s.Git.NotesAdd(mergeCommit, string(noteJSON)); err != nil {
		// Non-fatal: note write failure doesn't block merge
	}

	// Update draft status
	draft.Status = domain.StatusMerged
	draft.LastModifiedAt = core.Now()
	s.Store.WriteDraft(draft)

	// Push notes if remote exists
	if s.Git.HasRemote("origin") {
		s.Git.Push("origin", "refs/notes/mainline/intents")
	}

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

	// rc3: clean commit message, no Mainline-* fields
	message := draft.Goal

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

	// rc3: clean commit message, no Mainline-* fields
	message := fmt.Sprintf("Merge %s: %s", branch, draft.Goal)

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

	cfg, _ := s.getTeamConfig()
	identity, err := s.getIdentity()
	if err != nil {
		return nil, err
	}

	// rc3: reconcile writes git notes to commits (not actor log events)
	// Find proposed intents that appear to be merged but lack notes
	view, _ := s.Store.ReadMainlineView()
	if view == nil {
		return &ReconcileResult{}, nil
	}

	var reconciled []string
	entries, _ := s.Git.LogOneline(cfg.Mainline.MainBranch, cfg.Check.Lookback)

	for _, iv := range view.Intents {
		if iv.Status != domain.StatusProposed {
			continue
		}
		if iv.ActorID != identity.ActorID {
			continue
		}

		// Try to find a commit on main that matches this intent's code
		for _, entry := range entries {
			// Check if commit already has a note for this intent
			noteContent, _ := s.Git.NotesShow(entry.Hash)
			if noteContent != "" {
				var existing domain.CommitNote
				if json.Unmarshal([]byte(noteContent), &existing) == nil {
					for _, ref := range existing.Intents {
						if ref.IntentID == iv.IntentID {
							goto nextIntent // already noted
						}
					}
				}
			}

			// Heuristic: check if the commit message contains the intent goal
			msg, _ := s.Git.FullCommitMessage(entry.Hash)
			if !strings.Contains(msg, iv.Goal) && iv.CodeCommit != entry.Hash {
				continue
			}

			// Write reconcile note
			hash, _ := core.CanonicalHash(iv)
			note := domain.CommitNote{
				SchemaVersion: 1,
				Kind:          "mainline.commit_note",
				Intents: []domain.IntentReference{
					{IntentID: iv.IntentID, SealResultHash: "sha256:" + hash},
				},
				AddedAt:      core.Now(),
				AddedBy:      identity.ActorID,
				Via:          "reconcile",
				ReconciledAt: core.Now(),
				ReconciledBy: identity.ActorID,
			}
			noteJSON, _ := json.Marshal(note)
			if err := s.Git.NotesAdd(entry.Hash, string(noteJSON)); err == nil {
				reconciled = append(reconciled, iv.IntentID)
			}
			break
		}
	nextIntent:
	}

	// Push notes
	if len(reconciled) > 0 && s.Git.HasRemote("origin") {
		s.Git.Push("origin", "refs/notes/mainline/intents")
	}

	return &ReconcileResult{
		Reconciled: len(reconciled),
		IntentIDs:  reconciled,
	}, nil
}

// -----------------------------------------------------------
// PR description (rc3: no trailers, pure human-readable markdown)
// -----------------------------------------------------------

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
		return fmt.Sprintf("## Mainline Intent\n\n**Intent:** `%s`\n**Goal:** %s\n",
			draft.IntentID, draft.Goal), nil
	}

	return "", domain.NewError(domain.ErrNoActiveIntent, fmt.Sprintf("intent %s not found", intentID))
}

// rc3: pure human-readable markdown, no Mainline-* fields
func formatPRDescription(intentID string, summary *domain.IntentSummary, fp *domain.SemanticFingerprint) string {
	var sb strings.Builder
	sb.WriteString("## Mainline Intent\n\n")
	sb.WriteString(fmt.Sprintf("**Intent:** `%s`\n", intentID))
	sb.WriteString(fmt.Sprintf("**Title:** %s\n\n", summary.Title))

	sb.WriteString("### What changed\n\n")
	sb.WriteString(summary.What + "\n\n")

	sb.WriteString("### Why\n\n")
	sb.WriteString(summary.Why + "\n\n")

	if len(summary.Decisions) > 0 {
		sb.WriteString("### Decisions\n\n")
		for _, d := range summary.Decisions {
			sb.WriteString(fmt.Sprintf("- **%s:** %s", d.Point, d.Chose))
			if d.Rationale != "" {
				sb.WriteString(fmt.Sprintf(" (%s)", d.Rationale))
			}
			sb.WriteString("\n")
			for _, rej := range d.Rejected {
				sb.WriteString(fmt.Sprintf("  - Rejected: %s\n", rej))
			}
		}
		sb.WriteString("\n")
	}

	if len(summary.Risks) > 0 {
		sb.WriteString("### Risks\n\n")
		for _, r := range summary.Risks {
			sb.WriteString(fmt.Sprintf("- %s\n", r))
		}
		sb.WriteString("\n")
	}

	if len(summary.Followups) > 0 {
		sb.WriteString("### Follow-ups\n\n")
		for _, f := range summary.Followups {
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
		sb.WriteString("\n")
	}

	if fp != nil && len(fp.Subsystems) > 0 {
		sb.WriteString(fmt.Sprintf("**Subsystems:** %s\n", strings.Join(fp.Subsystems, ", ")))
	}

	return sb.String()
}
