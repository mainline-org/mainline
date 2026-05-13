package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
		SchemaVersion: 3,
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

	// Pre-fill what we can derive deterministically. Agent-judgment
	// fields stay empty so the schema is still a teaching aid, but
	// fingerprint.files_touched / fingerprint.subsystems / intent_id
	// are filled in — which removes ~50% of the typing for a
	// first-touch agent and sets the JSON shape correctly so the
	// validator's fingerprint checks pass on the first patch.
	pkg.Starter = buildSealStarter(draft.IntentID, draft.Goal, changedFiles)
	pkg.SealResultSchema = sealResultSchemaHints()

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
// Agent-judgment fields stay zero/empty or blank placeholders so the
// schema is visible (the agent sees the field names + types and
// patches in their content).
// summary.user_goal is not agent judgment: it mirrors the authoritative
// `mainline start` goal and SealSubmit enforces that value. Durable
// action signals are intentionally not part of the starter; creating
// one requires an explicit guard/risk/followup command.
//
// Subsystems are derived from path prefixes via the same helper
// the conflict-detection layer uses, so seal-time and check-time
// agree on what counts as a subsystem.
func buildSealStarter(intentID, userGoal string, files []string) *domain.SealResultStarter {
	subs := nonNilStrings(subsystemsFromFiles(files))
	return &domain.SealResultStarter{
		IntentID: intentID,
		Summary: domain.SealSummaryStarter{
			Title:                   "",
			What:                    "",
			Why:                     "",
			UserGoal:                userGoal,
			Decisions:               []domain.Decision{{Point: "", Chose: ""}},
			Rejected:                []domain.RejectedAlternative{},
			AcknowledgedConstraints: []domain.AcknowledgedConstraint{},
			ReviewNotes:             []string{},
		},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:           subs,
			FilesTouched:         nonNilStrings(files),
			ArchitecturalClaims:  []string{},
			BehavioralChanges:    []string{},
			APIChanges:           []domain.APIChange{},
			DataModelChanges:     []domain.DataModelChange{},
			SecurityImplications: []string{},
			MigrationNotes:       []string{},
			Tags:                 []string{},
		},
		Confidence: domain.SealConfidence{},
	}
}

func nonNilStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}

func sealResultSchemaHints() *domain.SealResultSchemaHints {
	return &domain.SealResultSchemaHints{
		Summary: domain.SealResultSummarySchemaHints{
			Decisions: []domain.Decision{{
				Point:     "decision point or question",
				Chose:     "chosen approach or accepted trade-off",
				Rationale: "why this choice was made",
				Rejected:  []string{"alternative considered and rejected"},
			}},
			Rejected: []domain.RejectedAlternative{{
				Alternative: "top-level alternative considered",
				Reason:      "why it was not chosen",
			}},
			AcknowledgedConstraints: []domain.AcknowledgedConstraint{{
				ConstraintID: "guard_xxx",
				Disposition:  "preserved|mitigated|not_applicable|intentionally_changed",
				Note:         "how this seal handled the inherited constraint",
			}},
			ReviewNotes: []string{"reviewer note, validation note, or scope explanation"},
		},
		Fingerprint: domain.SealResultFingerprintSchemaHints{
			APIChanges: []domain.APIChange{{
				Kind:          "added|modified|removed",
				Surface:       "http|function|class|cli|event|config",
				Signature:     "affected API, CLI, event, or function signature",
				Compatibility: "breaking|compatible|unknown",
			}},
			DataModelChanges: []domain.DataModelChange{{
				Kind:              "added|modified|removed",
				Name:              "model, table, field, or persisted state name",
				Location:          "file or storage location",
				Compatibility:     "breaking|compatible|unknown",
				MigrationRequired: false,
				MigrationNotes:    "migration or compatibility note, if any",
			}},
		},
	}
}

func sealInstruction() string {
	return `Analyze this intent and produce a SealResult JSON object.

Tip: copy the seal_result_starter field from this package as your
starting point — intent_id, fingerprint.files_touched, and
fingerprint.subsystems are pre-filled deterministically from the
diff. summary.user_goal is also pre-filled from mainline start and
SealSubmit enforces that authoritative goal. Patch in the
agent-judgment fields (title, what, why, decisions, rejected,
acknowledged_constraints when applicable, review_notes,
fingerprint details, confidence) and submit.
Use seal_result_schema only as an item-shape guide; do not submit it.

Default seal contract:
- Seal records history: what changed, why, decisions, rejected
  alternatives, inherited-constraint acknowledgements,
  validation/review notes, and semantic fingerprint.
- Seal summary is not a durable action-signal creation surface.
  Its schema intentionally has no risks, followups, or anti_patterns.
- If a signal is explicitly promoted, create it outside seal:
  mainline guard add   (human-confirmed constraints)
  mainline risks add   (structured reviewer-facing failure modes)
  mainline followups add (explicitly deferred work with provenance)

Required structure:
1. summary: title, what, why, user_goal, decisions, rejected alternatives,
   acknowledged_constraints when applicable, review_notes
2. fingerprint: subsystems, files_touched, architectural_claims, behavioral_changes,
   api_changes, data_model_changes, security_implications, migration_notes, tags
3. confidence: summary (0-1), fingerprint (0-1)

Array item shapes:
- summary.decisions is Decision[]: objects with point, chose, optional rationale,
  and optional rejected string[]. It is never string[].
- summary.rejected is RejectedAlternative[]: objects with alternative and optional
  reason. Use [] when there are no top-level rejected alternatives.
- summary.acknowledged_constraints is AcknowledgedConstraint[]: objects with
  constraint_id, disposition, and optional note. Use [] when none apply.
- summary.review_notes is string[]: reviewer notes, validation notes, or scope
  explanations. Use [] when none apply.
- fingerprint.api_changes and fingerprint.data_model_changes are object arrays.
  Use [] when none apply.

Field decision tree:
- "We chose X because Y" or "we shipped with limitation X" -> decisions
- "We considered B but ruled it out" -> rejected
- "Reviewer should focus on Z", validation notes, or scope explanation -> review_notes
- "This may fail when X" -> use mainline risks add only when it has a concrete
  failure mode with trigger/impact plus mitigation/validation/owner; otherwise
  mention it in review_notes only if it is useful reviewer context.
- "Do X later" -> use mainline followups add only when the user explicitly
  deferred it, an external issue/ticket/PR exists, or this PR cut real scope;
  otherwise mention it in the final response, not the ledger.
- "Future work MUST NOT do X" -> only a human-confirmed mainline guard add can
  create that constraint. Agents may propose the guard in the final response.

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
type SealSubmitOptions struct {
	Offline    bool
	AllowDirty bool
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

func validateNoLegacySealSummarySignals(input json.RawMessage) error {
	fields := legacySealSummarySignalKeys(input)
	if len(fields) == 0 {
		return nil
	}
	return domain.NewRecoverableError(
		domain.ErrSealFailed,
		fmt.Sprintf("seal summary no longer accepts legacy signal fields: %s", strings.Join(fields, ", ")),
		"re-run `mainline seal --prepare --json` to regenerate the current seal schema",
		"use review_notes for ephemeral reviewer context",
		"use `mainline risks add`, `mainline followups add`, or human-confirmed `mainline guard add` for promoted durable signals",
	)
}

func legacySealSummarySignalKeys(input json.RawMessage) []string {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(input, &root); err != nil {
		return nil
	}
	rawSummary, ok := root["summary"]
	if !ok {
		return nil
	}
	var summary map[string]json.RawMessage
	if err := json.Unmarshal(rawSummary, &summary); err != nil {
		return nil
	}
	var fields []string
	for _, key := range []string{"anti_patterns", "risks", "followups"} {
		if _, ok := summary[key]; ok {
			fields = append(fields, "summary."+key)
		}
	}
	return fields
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

	if err := validateNoLegacySealSummarySignals(input); err != nil {
		return nil, err
	}

	var sr domain.SealResult
	if err := json.Unmarshal(input, &sr); err != nil {
		return nil, domain.NewError(domain.ErrInvalidInput,
			formatSealUnmarshalError(err))
	}
	normalizeSealResultForSubmit(&sr)

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
		Summary:     sr.Summary.ToIntentSummary(),
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
		scope := s.mergedConflictScopeSinceBase(draft.BaseCommit, view)
		conflicts := s.detectSealedConflictsInScope(sr.IntentID, &sr.Fingerprint, view, cfg.Check.Phase1Threshold, scope)
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

func formatSealUnmarshalError(err error) string {
	msg := fmt.Sprintf("invalid SealResult JSON: %v", err)
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		if hint := sealUnmarshalTypeHint(typeErr); hint != "" {
			return msg + "; " + hint
		}
	}
	detail := err.Error()
	switch {
	case strings.Contains(detail, "SealSummaryInput.summary.decisions"):
		return msg + "; summary.decisions must be Decision[] (objects with point, chose, optional rationale, optional rejected), not string[]; copy seal_result_starter and use seal_result_schema from `mainline seal --prepare --json`"
	case strings.Contains(detail, "SealSummaryInput.summary.rejected"):
		return msg + "; summary.rejected must be RejectedAlternative[] (objects with alternative and optional reason), not string[]; use [] when none apply"
	case strings.Contains(detail, "SealSummaryInput.summary.acknowledged_constraints"):
		return msg + "; summary.acknowledged_constraints must be AcknowledgedConstraint[] objects, not string[]; use [] when none apply"
	case strings.Contains(detail, "SemanticFingerprint.fingerprint.api_changes"):
		return msg + "; fingerprint.api_changes must be APIChange[] objects, not string[]; use [] when none apply"
	case strings.Contains(detail, "SemanticFingerprint.fingerprint.data_model_changes"):
		return msg + "; fingerprint.data_model_changes must be DataModelChange[] objects, not string[]; use [] when none apply"
	default:
		return msg
	}
}

func normalizeSealResultForSubmit(sr *domain.SealResult) {
	if sr == nil {
		return
	}
	sr.Summary.ReviewNotes = compactNonEmptyStrings(sr.Summary.ReviewNotes)
}

func compactNonEmptyStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := make([]string, 0, len(in))
	for _, item := range in {
		if strings.TrimSpace(item) != "" {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		return []string{}
	}
	return out
}

func sealUnmarshalTypeHint(err *json.UnmarshalTypeError) string {
	if err == nil {
		return ""
	}
	field := err.Field
	switch field {
	case "summary.decisions":
		return "summary.decisions must be Decision[] (objects with point, chose, optional rationale, optional rejected), not string[]; copy seal_result_starter and use seal_result_schema from `mainline seal --prepare --json`"
	case "summary.rejected":
		return "summary.rejected must be RejectedAlternative[] (objects with alternative and optional reason), not string[]; use [] when none apply"
	case "summary.acknowledged_constraints":
		return "summary.acknowledged_constraints must be AcknowledgedConstraint[] objects, not string[]; use [] when none apply"
	case "summary.review_notes":
		return "summary.review_notes must be string[] (reviewer notes, validation notes, or scope explanations), not a string; use [] when none apply"
	case "fingerprint.api_changes":
		return "fingerprint.api_changes must be APIChange[] objects, not string[]; use [] when none apply"
	case "fingerprint.data_model_changes":
		return "fingerprint.data_model_changes must be DataModelChange[] objects, not string[]; use [] when none apply"
	}
	if err.Value == "string" && err.Type.Kind() == reflect.Slice {
		if err.Type.Elem().Kind() == reflect.String {
			return fmt.Sprintf("%s must be string[], not a string; use [] when none apply", field)
		}
		return fmt.Sprintf("%s must be an array of objects, not a string; use [] when none apply", field)
	}
	return ""
}
