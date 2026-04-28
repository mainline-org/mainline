package engine

import (
	"encoding/json"
	"fmt"

	"github.com/mainline-org/mainline/internal/core"
	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/gitops"
)

// -----------------------------------------------------------
// Seal --prepare
// -----------------------------------------------------------

func (s *Service) SealPrepare(intentID string) (*domain.SealPreparePackage, error) {
	if _, err := s.requireIdentity(); err != nil {
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
	currentBranch, _ := s.Git.CurrentBranch()
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

	// v0.3 snapshot block: capture worktree state at prepare time so
	// SealSubmit can validate against drift and the audit trail has a
	// permanent record of whether evidence was complete.
	wt, _ := s.Git.WorktreeStatus()
	if wt == nil {
		wt = &gitops.WorktreeStatusReport{Status: "clean"}
	}
	dirty := append([]string{}, wt.DirtyFiles...)
	dirty = append(dirty, wt.Untracked...)
	snapshot := &domain.SealSnapshot{
		PreparedAt:         core.Now(),
		ChangedFiles:       changes,
		WorktreeStatus:     wt.Status,
		WorktreeDirtyFiles: dirty,
		EvidenceComplete:   wt.Status == "clean",
	}

	pkg := &domain.SealPreparePackage{
		Kind:          "mainline.seal.prepare",
		SchemaVersion: 2,
		Turns:         turnSummaries,
		ChangedFiles:  changes,
		Snapshot:      snapshot,
		Instruction:   sealInstruction(),
	}
	pkg.Intent.ID = draft.IntentID
	pkg.Intent.Goal = draft.Goal
	pkg.Intent.Thread = draft.Thread
	pkg.Intent.GitBranch = draft.GitBranch
	pkg.Intent.BaseCommit = draft.BaseCommit
	pkg.Intent.CurrentHead = head
	pkg.Intent.CurrentBranch = currentBranch

	pkg.DiffSummary.Files = stats.Files
	pkg.DiffSummary.Added = stats.Added
	pkg.DiffSummary.Removed = stats.Removed
	pkg.DiffSummary.FilesChanged = changedFiles

	// Persist the snapshot so SealSubmit can validate the live repo
	// against what prepare claimed. Overwrite-safe: re-running --prepare
	// updates the snapshot (intentional — agent may iterate).
	if err := s.Store.WritePrepareSnapshot(draft.IntentID, pkg); err != nil {
		// Persistence failure is not fatal — submit will treat the
		// snapshot as absent and skip the contract checks (degraded
		// safety, but the seal still works).
		_ = err
	}

	return pkg, nil
}

// validateSealSnapshot enforces the v0.3 seal-snapshot contract. Caller
// (SealSubmit) supplies the live repo state; this function compares it
// to the persisted prepare snapshot (if any) and returns a typed
// recoverable error on mismatch.
func validateSealSnapshot(
	prepare *domain.SealPreparePackage,
	currentHead, currentBranch string,
	wt *gitops.WorktreeStatusReport,
	allowDirty bool,
) error {
	// No prepare snapshot persisted → legacy path. Submit proceeds
	// without contract checks. This preserves the pre-v0.3 behaviour
	// for callers that skip --prepare (test fixtures, automation that
	// constructs SealResult directly). The recommended flow is
	// always prepare → submit, which IS contract-checked below.
	if prepare == nil {
		return nil
	}

	// HEAD must match what prepare recorded. Drift = stale prepare;
	// agent must re-run --prepare to refresh.
	if prepare.Intent.CurrentHead != "" && prepare.Intent.CurrentHead != currentHead {
		return domain.NewRecoverableError(
			domain.ErrInvalidStatus,
			fmt.Sprintf("STALE_PREPARE: HEAD moved since prepare (was %s, now %s)",
				short(prepare.Intent.CurrentHead), short(currentHead)),
			"re-run `mainline seal --prepare > seal.json` to refresh the snapshot",
		)
	}

	// Branch must match. Switching branches between prepare and submit
	// would silently change what `base..HEAD` means.
	if prepare.Intent.CurrentBranch != "" && prepare.Intent.CurrentBranch != currentBranch {
		return domain.NewRecoverableError(
			domain.ErrInvalidStatus,
			fmt.Sprintf("BRANCH_DRIFT: branch changed since prepare (was %s, now %s)",
				prepare.Intent.CurrentBranch, currentBranch),
			fmt.Sprintf("`git checkout %s` and try again, or re-run --prepare on the new branch", prepare.Intent.CurrentBranch),
		)
	}

	// Worktree must be clean unless --allow-dirty was passed.
	if !allowDirty && wt.Status != "clean" {
		return domain.NewRecoverableError(
			domain.ErrInvalidStatus,
			fmt.Sprintf("worktree is %s; refusing to seal with incomplete evidence", wt.Status),
			"commit or stash the changes, or pass --allow-dirty to proceed and record dirty status in the audit trail",
		)
	}
	return nil
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func sealInstruction() string {
	return `Analyze this intent and produce a SealResult JSON object with:
1. summary: title, what, why, user_goal, decisions, rejected alternatives, risks, followups
2. fingerprint: subsystems, files_touched, architectural_claims, behavioral_changes,
   api_changes, data_model_changes, security_implications, migration_notes, tags
3. confidence: summary (0-1), fingerprint (0-1)

Risk discipline:
- Put an item in summary.risks only when it names a concrete failure mode,
  compatibility break, data loss/corruption possibility, security/privacy issue,
  performance/scale regression, user-visible misbehavior, or maintenance hazard
  that a future reviewer should audit.
- Do not put verification notes, "tests not run", review guidance, cosmetic
  concerns, generic unknown-risk disclaimers, implementation summaries, scope
  limitations, or ordinary follow-up work in risks.
- If there is no concrete risk, use an empty risks array.
- Put deferred work in followups, and put evidence of testing or review context
  outside risks unless it directly describes one of the concrete hazards above.

Return ONLY valid JSON matching the SealResult schema.`
}

// -----------------------------------------------------------
// Seal --submit
// -----------------------------------------------------------

type SealSubmitResult struct {
	IntentID   string                `json:"intent_id"`
	Status     string                `json:"status"`
	Published  bool                  `json:"published"`
	CodeCommit string                `json:"code_commit"`
	EventID    string                `json:"event_id"`
	Hash       string                `json:"canonical_hash"`
	Warning    string                `json:"warning,omitempty"`
	SyncRan    bool                  `json:"sync_ran"`
	SyncError  string                `json:"sync_error,omitempty"`
	Conflicts  []domain.ConflictPair `json:"conflicts,omitempty"`
}

// SealSubmitOptions controls the rc5+ seal-submit augmentations.
//
//	Offline    skips the post-seal sync + phase1 check (CLI --offline)
//	AllowDirty bypasses the v0.3 snapshot-contract worktree check
//	           (CLI --allow-dirty). Dirty seals still proceed but the
//	           IntentSealedEvent permanently records the worktree state
//	           so reviewers see the audit trail.
type SealSubmitOptions struct {
	Offline    bool
	AllowDirty bool
}

// SealSubmit retains the original signature and runs with default
// options (auto sync + check on). Existing callers compile unchanged.
func (s *Service) SealSubmit(input json.RawMessage) (*SealSubmitResult, error) {
	return s.SealSubmitWithOptions(input, nil)
}

func (s *Service) SealSubmitWithOptions(input json.RawMessage, opts *SealSubmitOptions) (*SealSubmitResult, error) {
	// All validation (identity, JSON, draft state, snapshot contract)
	// MUST run before any draft mutation. Pre-this-fix the code wrote
	// `draft.Status = sealed_local` to disk and THEN called
	// getIdentity, so a fresh-clone-without-identity left the draft
	// in an unrecoverable sealed_local state with no actor-log event.
	identity, err := s.requireIdentity()
	if err != nil {
		return nil, err
	}
	cfg, err := s.getTeamConfig()
	if err != nil {
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

	// v0.3 snapshot-contract invariants: HEAD + branch + worktree state.
	// Validated against the live repo; failures abort BEFORE any state
	// mutation so retrying after `mainline seal --prepare` works cleanly.
	wt, _ := s.Git.WorktreeStatus()
	if wt == nil {
		wt = &gitops.WorktreeStatusReport{Status: "clean"}
	}
	currentBranch, _ := s.Git.CurrentBranch()
	currentHead, _ := s.Git.HeadCommit()
	allowDirty := opts != nil && opts.AllowDirty

	prepare, _ := s.Store.ReadPrepareSnapshot(sr.IntentID)
	if err := validateSealSnapshot(prepare, currentHead, currentBranch, wt, allowDirty); err != nil {
		return nil, err
	}

	codeCommit := currentHead

	// All gates passed. Mutate draft + write actor-log event + push.
	draft.Status = domain.StatusSealedLocal
	draft.LastModifiedAt = core.Now()
	if err := s.Store.WriteDraft(draft); err != nil {
		return nil, fmt.Errorf("update draft: %w", err)
	}

	eventID := core.GenerateEventID()
	dirty := append([]string{}, wt.DirtyFiles...)
	dirty = append(dirty, wt.Untracked...)
	event := domain.IntentSealedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       eventID,
			SchemaVersion: 1,
			EventType:     domain.EventIntentSealed,
			ActorID:       identity.ActorID,
			ActorName:     s.actorDisplayName(identity),
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

		// v0.3 audit-trail fields. evidence_complete is the seal-time
		// truth that reviewers can trust forever; --allow-dirty seals
		// permanently carry worktree_status="dirty" or "untracked".
		EvidenceComplete: wt.Status == "clean",
		WorktreeStatus:   wt.Status,
		SealedAtBranch:   currentBranch,
		DirtyFiles:       dirty,

		// v0.3 backfill: carry through the explicit commit list set
		// at start time so cross-actor sync sees it and Pin can pin
		// to those exact commits.
		BackfillCommits: draft.BackfillCommits,
	}

	if err := s.Store.AppendActorLogEvent(identity.ActorID, cfg.Mainline.ActorLogPrefix, event); err != nil {
		return nil, fmt.Errorf("write actor log event: %w", err)
	}

	// Snapshot consumed; remove it so a stale prepare cannot ride
	// through a future submit. Re-running seal on the same intent
	// requires a fresh --prepare.
	s.Store.DeletePrepareSnapshot(sr.IntentID)

	// Compute canonical hash
	hash, _ := core.CanonicalHash(sr)

	// Auto-publish: try to push actor log to remote
	published := false
	warning := ""
	finalStatus := domain.StatusSealedLocal

	offline := opts != nil && opts.Offline
	if s.Git.HasRemote(s.remoteName()) && !offline {
		ref := s.Store.ActorLogRef(identity.ActorID, cfg.Mainline.ActorLogPrefix)
		refspec := fmt.Sprintf("%s:%s", ref, ref)
		if err := s.Git.Push(s.remoteName(), refspec); err == nil {
			published = true
			finalStatus = domain.StatusProposed
		} else {
			warning = "Sealed locally but failed to publish. Run 'mainline publish' to retry."
		}
	} else if offline {
		warning = "Sealed locally (--offline). Run 'mainline publish' when online."
	}

	// Update draft status to final status. Best-effort: a write
	// failure here just means `mainline status` reads the previous
	// status until the next sync rebuilds the view from events.
	draft.Status = finalStatus
	draft.LastModifiedAt = core.Now()
	_ = s.Store.WriteDraft(draft)

	result := &SealSubmitResult{
		IntentID:   sr.IntentID,
		Status:     string(finalStatus),
		Published:  published,
		CodeCommit: codeCommit,
		EventID:    eventID,
		Hash:       hash,
		Warning:    warning,
	}

	// rc5 Patch 3: auto sync + phase1 check unless --offline.
	// Conflicts are advisory and never block the seal — this surface
	// just makes them visible at the moment they actually matter
	// (the user just promised the team they're doing this work).
	if !offline {
		syncResult, syncErr := s.Sync()
		result.SyncRan = true
		if syncErr != nil {
			result.SyncError = syncErr.Error()
		}
		// Re-read view (Sync just rewrote it) to detect against the
		// freshest remote state. Use the freshly-sealed fingerprint
		// directly — view's snapshot of this intent may not reflect
		// it yet (actor log replay race).
		view, _ := s.Store.ReadMainlineView()
		conflicts := s.detectSealedConflicts(sr.IntentID, &sr.Fingerprint, view, cfg.Check.Phase1Threshold)
		if len(conflicts) > 0 {
			result.Conflicts = conflicts
		}
		_ = syncResult // reserved for future surface (unused for now)
	}

	// Domain-event fan-out. intent_sealed is the headline event
	// most webhook subscribers care about; conflict_detected fires
	// for each phase1 hit so a notification center can page on
	// fresh conflicts at the moment they appear (the user just
	// promised the team they're doing this work).
	s.emit("intent_sealed", result)
	for _, p := range result.Conflicts {
		s.emit("conflict_detected", p)
	}

	return result, nil
}
