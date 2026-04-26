package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mainline-org/mainline/internal/core"
	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/gitops"
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

	draft, err := s.Store.ReadDraft(intentID)
	if err != nil || draft == nil {
		return nil, domain.NewError(domain.ErrNoActiveIntent,
			fmt.Sprintf("intent %s not found", intentID))
	}
	if draft.Status != domain.StatusSealedLocal && draft.Status != domain.StatusProposed {
		return nil, domain.NewError(domain.ErrInvalidStatus,
			fmt.Sprintf("intent %s is in status %s, expected sealed_local or proposed", intentID, draft.Status))
	}

	pushed := false
	if s.Git.HasRemote(s.remoteName()) {
		refspec := fmt.Sprintf("%s:%s", ref, ref)
		if err := s.Git.Push(s.remoteName(), refspec); err != nil {
			return nil, domain.NewRecoverableError(domain.ErrPublishFailed,
				fmt.Sprintf("failed to push actor log %s: %v", ref, err),
				"check remote access",
				"retry mainline publish --intent "+intentID,
			)
		}
		pushed = true
	}

	if pushed && draft.Status == domain.StatusSealedLocal {
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
	// Non-fatal: note write failure doesn't block merge.
	_ = upsertCommitNote(s.Git, mergeCommit, note)

	// Update draft status
	draft.Status = domain.StatusMerged
	draft.LastModifiedAt = core.Now()
	s.Store.WriteDraft(draft)

	// Push notes if remote exists
	if s.Git.HasRemote(s.remoteName()) {
		s.Git.Push(s.remoteName(), "refs/notes/mainline/intents")
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
// Pin  (formerly Reconcile — see Patch 7 in the rc4 spec patch).
// -----------------------------------------------------------
//
// Naming: at the user-facing layer this operation is now called Pin —
// the action is "pin an intent to a main commit" via a git note. The
// on-disk via values written by future calls reflect that
// (pin_auto / pin_explicit). Older notes still on the ref carry the
// historical reconcile_auto / reconcile_manual / reconcile / manual
// values; sync.normaliseVia maps every flavour onto the view-layer
// merged_via=pin bucket, so readers do not need to care which name
// produced the note.

// PinnedCommit records one (intent, commit, strategy) triple produced
// by Pin. Strategy is the rule that won (see CommitNote.MatchStrategy).
type PinnedCommit struct {
	IntentID      string `json:"intent_id"`
	Commit        string `json:"commit"`
	MatchStrategy string `json:"match_strategy"`
}

type PinResult struct {
	Pinned    int            `json:"pinned"`
	IntentIDs []string       `json:"intent_ids"`
	Links     []PinnedCommit `json:"links,omitempty"`
}

// pinStrategies is the priority-ordered list of rules tried by Pin.
// tree_hash is first because squash merge preserves the tree exactly,
// and it is what GitHub's web UI does by default — the case Pin must
// handle to be useful in practice.
var pinStrategies = []string{"tree_hash", "commit_hash", "goal_text"}

// Pin scans every proposed intent in the materialised view and tries
// to associate it with a main-branch commit using a cascade of rules
// (tree_hash → commit_hash → goal_text). The first matching commit wins.
//
// Pin is not restricted to intents owned by the calling actor: notes
// live on shared main commits and any teammate may attach one. The
// note's added_by field still records who performed the pin so the
// audit trail is preserved.
func (s *Service) Pin() (*PinResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	cfg, _ := s.getTeamConfig()
	identity, err := s.getIdentity()
	if err != nil {
		return nil, err
	}

	view, _ := s.Store.ReadMainlineView()
	if view == nil {
		return &PinResult{}, nil
	}

	entries, _ := s.Git.LogOneline(cfg.Mainline.MainBranch, cfg.Check.Lookback)

	// One `log --no-walk` for every tree hash — replaces N forks with 1
	// during the auto-pin sweep over recent main commits.
	hashes := make([]string, 0, len(entries))
	for _, e := range entries {
		hashes = append(hashes, e.Hash)
	}
	treeOf, _ := s.Git.CommitTreeHashes(hashes)
	if treeOf == nil {
		treeOf = map[string]string{}
	}

	result := &PinResult{}
	for _, iv := range view.Intents {
		// Only proposed and merged intents go through the auto-pin
		// path. Proposed → run the strategy cascade to find a primary
		// commit. Merged → use the recorded MergedMainCommit as the
		// primary so retroactive same-tree expansion can fix the
		// canonical merge-commit + squash-content-commit pair where
		// only one of the pair got the note historically.
		if iv.Status != domain.StatusProposed && iv.Status != domain.StatusMerged {
			continue
		}

		// v0.3 backfill: when the intent was started with --commits,
		// pin to each listed commit explicitly. Bypasses the
		// tree_hash/commit_hash/goal_text cascade — backfill IS the
		// claim, not a heuristic match. Each commit gets the same
		// intent ref via upsertCommitNote (multi-intent shared notes
		// already supported).
		if len(iv.BackfillCommits) > 0 {
			pinnedAny := false
			for _, target := range iv.BackfillCommits {
				if alreadyHasIntent(s.Git, target, iv.IntentID) {
					continue
				}
				hash, _ := core.CanonicalHash(iv)
				note := domain.CommitNote{
					SchemaVersion: 1,
					Kind:          "mainline.commit_note",
					Intents: []domain.IntentReference{
						{IntentID: iv.IntentID, SealResultHash: "sha256:" + hash},
					},
					AddedAt:       core.Now(),
					AddedBy:       identity.ActorID,
					Via:           "pin_backfill",
					MatchStrategy: "backfill_explicit",
					ReconciledAt:  core.Now(),
					ReconciledBy:  identity.ActorID,
				}
				if err := upsertCommitNote(s.Git, target, note); err != nil {
					continue
				}
				pinnedAny = true
				result.Links = append(result.Links, PinnedCommit{
					IntentID:      iv.IntentID,
					Commit:        target,
					MatchStrategy: "backfill_explicit",
				})
			}
			if pinnedAny {
				result.IntentIDs = append(result.IntentIDs, iv.IntentID)
			}
			continue
		}

		// Two cases share the same expand-and-write tail:
		//
		//   proposed → run the strategy cascade to FIND a match,
		//              then expand to same-tree neighbors.
		//   merged   → use the existing MergedMainCommit as the
		//              primary, expand to same-tree neighbors, and
		//              pin any that don't yet have the note. This
		//              retroactively covers the canonical
		//              GitHub merge-commit + squash-content-commit
		//              pair, where the merge note historically
		//              landed only on the merge commit.
		var primary, strategy string
		switch iv.Status {
		case domain.StatusProposed:
			primary, strategy = s.findPinMatch(iv, entries, treeOf)
			if primary == "" {
				continue
			}
		case domain.StatusMerged:
			primary = iv.StatusEvidence.MergedMainCommit
			if primary == "" || treeOf[primary] == "" {
				// Either no recorded pin commit, or it's outside
				// the lookback — skip retroactive expansion.
				continue
			}
			strategy = "tree_hash"
		default:
			continue
		}

		// expandToSameTreeNeighbors: a merge commit and the squash
		// content commit it merged share an identical tree (no new
		// content was added by the merge). Pinning all same-tree
		// commits in the lookback closes the coverage gap where
		// only one of the pair gets a note.
		targets := []string{primary}
		primaryTree := treeOf[primary]
		if primaryTree != "" {
			for _, e := range entries {
				if e.Hash == primary {
					continue
				}
				if treeOf[e.Hash] == primaryTree {
					targets = append(targets, e.Hash)
				}
			}
		}

		hash, _ := core.CanonicalHash(iv)
		pinnedAny := false
		for _, target := range targets {
			if alreadyHasIntent(s.Git, target, iv.IntentID) {
				continue
			}
			note := domain.CommitNote{
				SchemaVersion: 1,
				Kind:          "mainline.commit_note",
				Intents: []domain.IntentReference{
					{IntentID: iv.IntentID, SealResultHash: "sha256:" + hash},
				},
				AddedAt:       core.Now(),
				AddedBy:       identity.ActorID,
				Via:           "pin_auto",
				MatchStrategy: strategy,
				ReconciledAt:  core.Now(),
				ReconciledBy:  identity.ActorID,
			}
			if err := upsertCommitNote(s.Git, target, note); err != nil {
				continue
			}
			pinnedAny = true
			result.Links = append(result.Links, PinnedCommit{
				IntentID:      iv.IntentID,
				Commit:        target,
				MatchStrategy: strategy,
			})
		}
		if pinnedAny {
			result.IntentIDs = append(result.IntentIDs, iv.IntentID)
		}
	}
	result.Pinned = len(result.IntentIDs)

	if result.Pinned > 0 && s.Git.HasRemote(s.remoteName()) {
		s.Git.Push(s.remoteName(), "refs/notes/mainline/intents")
	}

	return result, nil
}

// PinExplicit writes a pin_explicit note pinning intentID to
// commitHash without consulting the heuristic cascade. It is the
// escape hatch when the automatic match cannot reach the right commit
// (e.g. a rebase scrambled the tree, or the agent never recorded
// code_commit).
//
// The intent must currently be in the proposed state and the commit
// must exist; the note's added_by records the calling actor regardless
// of who owns the intent.
func (s *Service) PinExplicit(intentID, commitHash string) (*PinnedCommit, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}
	if intentID == "" || commitHash == "" {
		return nil, domain.NewError(domain.ErrInvalidInput,
			"pin explicit requires both intent and commit")
	}

	identity, err := s.getIdentity()
	if err != nil {
		return nil, err
	}

	resolved, err := s.Git.Run("rev-parse", "--verify", commitHash+"^{commit}")
	if err != nil {
		return nil, domain.NewError(domain.ErrInvalidInput,
			fmt.Sprintf("commit %q not found: %v", commitHash, err))
	}
	resolved = strings.TrimSpace(resolved)

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
	if iv == nil {
		return nil, domain.NewError(domain.ErrNoActiveIntent,
			fmt.Sprintf("intent %s not found in view; run mainline sync first", intentID))
	}
	if iv.Status != domain.StatusProposed {
		return nil, domain.NewError(domain.ErrInvalidStatus,
			fmt.Sprintf("intent %s is in status %s; only proposed intents can be pinned",
				intentID, iv.Status))
	}

	if alreadyHasIntent(s.Git, resolved, intentID) {
		return &PinnedCommit{
			IntentID:      intentID,
			Commit:        resolved,
			MatchStrategy: "manual",
		}, nil
	}

	hash, _ := core.CanonicalHash(iv)
	note := domain.CommitNote{
		SchemaVersion: 1,
		Kind:          "mainline.commit_note",
		Intents: []domain.IntentReference{
			{IntentID: intentID, SealResultHash: "sha256:" + hash},
		},
		AddedAt:       core.Now(),
		AddedBy:       identity.ActorID,
		Via:           "pin_explicit",
		MatchStrategy: "manual",
		ReconciledAt:  core.Now(),
		ReconciledBy:  identity.ActorID,
	}
	if err := upsertCommitNote(s.Git, resolved, note); err != nil {
		return nil, fmt.Errorf("write note: %w", err)
	}
	if s.Git.HasRemote(s.remoteName()) {
		s.Git.Push(s.remoteName(), "refs/notes/mainline/intents")
	}

	return &PinnedCommit{
		IntentID:      intentID,
		Commit:        resolved,
		MatchStrategy: "manual",
	}, nil
}

// findPinMatch walks pinStrategies in order and returns the first
// matching commit hash plus the strategy name that won. Returns
// ("", "") when no strategy matches any candidate commit.
func (s *Service) findPinMatch(iv domain.IntentView, entries []gitops.LogEntry, treeOf map[string]string) (string, string) {
	for _, strategy := range pinStrategies {
		switch strategy {
		case "tree_hash":
			if iv.CodeCommit == "" {
				continue
			}
			intentTree, err := s.Git.CommitTreeHash(iv.CodeCommit)
			if err != nil || intentTree == "" {
				continue
			}
			for _, entry := range entries {
				if treeOf[entry.Hash] == intentTree {
					return entry.Hash, strategy
				}
			}
		case "commit_hash":
			if iv.CodeCommit == "" {
				continue
			}
			for _, entry := range entries {
				if entry.Hash == iv.CodeCommit {
					return entry.Hash, strategy
				}
			}
		case "goal_text":
			if iv.Goal == "" {
				continue
			}
			for _, entry := range entries {
				msg, _ := s.Git.FullCommitMessage(entry.Hash)
				if strings.Contains(msg, iv.Goal) {
					return entry.Hash, strategy
				}
			}
		}
	}
	return "", ""
}

// alreadyHasIntent returns true if the existing note on commit already
// references intentID — Reconcile must not double-stamp the same intent on
// the same commit (otherwise NotesAdd's `-f` would overwrite an unrelated
// intent that happened to share the commit).
func alreadyHasIntent(git *gitops.Git, commit, intentID string) bool {
	noteContent, _ := git.NotesShow(commit)
	if noteContent == "" {
		return false
	}
	var existing domain.CommitNote
	if err := json.Unmarshal([]byte(noteContent), &existing); err != nil {
		return false
	}
	for _, ref := range existing.Intents {
		if ref.IntentID == intentID {
			return true
		}
	}
	return false
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
