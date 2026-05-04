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

	// Pre-fill only what we can derive deterministically:
	// fingerprint.files_touched / fingerprint.subsystems / intent_id.
	// Agent-judgment fields stay empty. Explicit-only structured
	// signals (risks, anti_patterns, followups) are not shown in the
	// starter at all, so agents don't treat them as blanks to fill.
	pkg.Starter = buildSealStarter(draft.IntentID, draft.Goal, changedFiles)

	// v0.4 risk lifecycle: surface open risks on files this intent
	// touches so the agent can resolve them via resolves_risks.
	// Best-effort from the local view — may be stale if no recent sync.
	if view, _ := s.Store.ReadMainlineView(); view != nil {
		viewUsed := false
		openRisks := materializeOpenRisks(view, changedFiles)
		if len(openRisks) > 0 {
			pkg.ApplicableOpenRisks = openRisks
			viewUsed = true
		}
		openFollowups := materializeOpenFollowups(view, changedFiles)
		if len(openFollowups) > 0 {
			pkg.ApplicableOpenFollowups = openFollowups
			viewUsed = true
		}
		if viewUsed {
			pkg.ViewRebuiltAt = view.RebuiltAt
		}
	}

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

// buildSealStarter pre-populates a SealResult with the fields the
// engine can derive from the draft + diff:
//
//   - intent_id, summary.user_goal, fingerprint.files_touched,
//     fingerprint.subsystems
//
// Agent-judgment fields stay zero/empty. Explicit-only signal fields are not
// included in the starter; their absence is intentional, not a schema gap.
// summary.user_goal is not agent judgment: it mirrors the authoritative
// `mainline start` goal and SealSubmit enforces that value.
//
// Subsystems are derived from path prefixes via the same helper
// the conflict-detection layer uses, so seal-time and check-time
// agree on what counts as a subsystem.
func buildSealStarter(intentID, userGoal string, files []string) *domain.SealResult {
	subs := subsystemsFromFiles(files)
	return &domain.SealResult{
		IntentID: intentID,
		Summary: domain.IntentSummary{
			Title:       "",
			What:        "",
			Why:         "",
			UserGoal:    userGoal,
			Decisions:   []domain.Decision{},
			Rejected:    []domain.RejectedAlternative{},
			ReviewNotes: []string{},
		},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:           subs,
			FilesTouched:         append([]string(nil), files...),
			ArchitecturalClaims:  []string{},
			BehavioralChanges:    []string{},
			SecurityImplications: []string{},
			MigrationNotes:       []string{},
			Tags:                 []string{},
		},
		Confidence: domain.SealConfidence{},
	}
}

func sealInstruction() string {
	return `Analyze this intent and produce a SealResult JSON object.

Tip: copy the seal_result_starter field from this package as your
starting point — intent_id, fingerprint.files_touched, and
fingerprint.subsystems are pre-filled deterministically from the
diff. summary.user_goal is also pre-filled from mainline start and
SealSubmit enforces that authoritative goal. Patch in the
default agent-judgment fields (title, what, why, decisions,
fingerprint details, confidence) and submit.

Default structure:
1. summary: title, what, why, user_goal, decisions, rejected alternatives, review_notes, acknowledged_constraints
2. fingerprint: subsystems, files_touched, architectural_claims, behavioral_changes,
   api_changes, data_model_changes, security_implications, migration_notes, tags
3. confidence: summary (0-1), fingerprint (0-1)

Default rule:
- Mainline records decisions by default. It does not let agents create repo-wide
  constraints, risks, or follow-up queues unless a human explicitly promotes
  that note into a structured signal.
- Do not add summary.risks, summary.anti_patterns, or summary.followups just
  because the old schema supports them. If you did not receive explicit user or
  reviewer approval for such a signal, omit the field entirely.
- If a reviewer needs temporary context for this PR, put it in review_notes.
- If you accepted a trade-off or scope limit, record it as a decision with
  rationale.

Field decision tree — for each observation, pick the lowest-authority field:

  "We chose to ship with limitation X (acceptable)"   → decisions[].chose with rationale
  "Reviewer should focus on Z" / scope explanation     → review_notes (ephemeral, not inherited)
  "We considered B but ruled it out"                  → rejected
  "I saw inherited constraint X and handled it"       → acknowledged_constraints

  If you can't pick: it's probably a decision (you made a judgment call).

Acknowledged constraints:
- If mainline context surfaced inherited_constraints, acknowledge each here.
- Format: {"constraint_id": "int_xxx#N", "disposition": "preserved|mitigated|not_applicable|intentionally_changed", "note": "..."}
- "intentionally_changed" signals reviewer attention needed.

Explicit structured signals:
- A constraint is a future behavior rule. Default seals cannot create one. Only
  include summary.anti_patterns when the user or reviewer explicitly approved
  promoting that rule, and submit with --allow-structured-signals.
- A risk is a present-review warning with a concrete failure mode plus a
  mitigation, validation, or owner. Only include summary.risks after explicit
  approval, and submit with --allow-structured-signals.
- A follow-up is deferred work the user explicitly asked to track, an external
  issue/ticket, or scope deliberately cut from this change. Only include
  summary.followups after explicit approval, and submit with
  --allow-structured-signals.
- Agent-invented "maybe later", "consider", "nice to have", dogfood, or
  telemetry ideas must not become structured signals.

Risk resolution:
- If applicable_open_risks lists risks on files you touched, and your
  work resolves any of them, add a resolves_risks entry:
    "resolves_risks": [{"risk_id": "int_xxx#0", "rationale": "shipped X"}]

Follow-up resolution:
- If applicable_open_followups lists follow-ups on files you touched, and
  your work completes any of them, add a resolves_followups entry:
    "resolves_followups": [{"followup_id": "int_xxx#0", "rationale": "shipped X"}]

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
	LintIssues []LintIssue           `json:"lint_issues,omitempty"`
}

// SealSubmitOptions controls the rc5+ seal-submit augmentations.
//
//	Offline    skips the post-seal sync + phase1 check (CLI --offline)
//	AllowDirty bypasses the v0.3 snapshot-contract worktree check
//	           (CLI --allow-dirty). Dirty seals still proceed but the
//	           IntentSealedEvent permanently records the worktree state
//	           so reviewers see the audit trail.
//	RejectStructuredSignals rejects seal-time creation of summary.risks,
//	           summary.anti_patterns, and summary.followups. The CLI enables
//	           this by default so these long-lived action signals require an
//	           explicit opt-in flag.
type SealSubmitOptions struct {
	Offline                 bool
	AllowDirty              bool
	RejectStructuredSignals bool
}

// SealSubmit retains the original signature and runs with default
// options (auto sync + check on). Existing callers compile unchanged.
func (s *Service) SealSubmit(input json.RawMessage) (*SealSubmitResult, error) {
	return s.SealSubmitWithOptions(input, nil)
}

func blockingLintIssues(issues []LintIssue) []LintIssue {
	var out []LintIssue
	for _, issue := range issues {
		if issue.Severity == "error" {
			out = append(out, issue)
		}
	}
	return out
}

func nonBlockingLintIssues(issues []LintIssue) []LintIssue {
	var out []LintIssue
	for _, issue := range issues {
		if issue.Severity != "error" {
			out = append(out, issue)
		}
	}
	return out
}

func formatBlockingLintIssues(issues []LintIssue) string {
	if len(issues) == 0 {
		return "seal lint failed"
	}
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		parts = append(parts, fmt.Sprintf("%s: %s", issue.Code, issue.Message))
	}
	return "seal lint failed: " + strings.Join(parts, "; ")
}

func structuredSignalCounts(summary domain.IntentSummary) (risks, constraints, followups int) {
	return len(summary.Risks), len(summary.AntiPatterns), len(summary.Followups)
}

func structuredSignalSummary(summary domain.IntentSummary) string {
	risks, constraints, followups := structuredSignalCounts(summary)
	var parts []string
	if risks > 0 {
		parts = append(parts, fmt.Sprintf("summary.risks=%d", risks))
	}
	if constraints > 0 {
		parts = append(parts, fmt.Sprintf("summary.anti_patterns=%d", constraints))
	}
	if followups > 0 {
		parts = append(parts, fmt.Sprintf("summary.followups=%d", followups))
	}
	return strings.Join(parts, ", ")
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

	if opts != nil && opts.RejectStructuredSignals {
		if summary := structuredSignalSummary(sr.Summary); summary != "" {
			return nil, domain.NewRecoverableError(
				domain.ErrSealFailed,
				"structured signals are explicit-only in default seal submit: "+summary,
				"remove summary.risks, summary.anti_patterns, and summary.followups unless the user or reviewer explicitly approved creating them",
				"if they were explicitly approved, re-run `mainline seal --submit --allow-structured-signals`",
			)
		}
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

	// UserGoal is a lifecycle fact, not agent-authored prose. The
	// authoritative value is fixed by `mainline start`; seal JSON may
	// omit it or contain a bad mirror, but it must never redefine it.
	sr.Summary.UserGoal = draft.Goal

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

	view, _ := s.Store.ReadMainlineView()
	lintResult := LintSealResultWithView(&sr, view)
	if blocking := blockingLintIssues(lintResult.Issues); len(blocking) > 0 {
		return nil, domain.NewRecoverableError(
			domain.ErrSealFailed,
			formatBlockingLintIssues(blocking),
			"edit the seal payload to address the deterministic lint errors",
			"re-run `mainline seal --submit` after updating title/what/why/decisions/fingerprint fields",
		)
	}

	codeCommit := currentHead

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

		// References attached by the agent via SealResult.
		References: sr.References,

		// v0.4 risk lifecycle: risk resolutions are atomic with the
		// seal event — no separate events, no partial-resolution risk.
		ResolvesRisks: sr.ResolvesRisks,

		// Follow-up lifecycle: completion is also atomic with the
		// seal event so the work and queue update land together.
		ResolvesFollowups: sr.ResolvesFollowups,
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

	// Update draft status only after the durable actor-log event exists.
	// Best-effort: a write failure here just means `mainline status`
	// reads the previous draft status until the next sync rebuilds the
	// view from events.
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
		LintIssues: nonBlockingLintIssues(lintResult.Issues),
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
