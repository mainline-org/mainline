package domain

// IntentStatus represents the lifecycle state of an intent.
type IntentStatus string

const (
	StatusDrafting    IntentStatus = "drafting"
	StatusSealedLocal IntentStatus = "sealed_local"
	StatusProposed    IntentStatus = "proposed"
	StatusMerged      IntentStatus = "merged"
	StatusAbandoned   IntentStatus = "abandoned"
	StatusSuperseded  IntentStatus = "superseded"
	StatusReverted    IntentStatus = "reverted"
)

// DraftIntent is a local-only intent in progress. Lives in .ml-cache/drafts/.
type DraftIntent struct {
	IntentID       string       `json:"intent_id"`
	SchemaVersion  int          `json:"schema_version"`
	Status         IntentStatus `json:"status"`
	Thread         string       `json:"thread"`
	GitBranch      string       `json:"git_branch"`
	BaseCommit     string       `json:"base_commit"`
	Goal           string       `json:"goal"`
	Turns          []Turn       `json:"turns"`
	CreatedAt      string       `json:"created_at"`
	LastModifiedAt string       `json:"last_modified_at"`

	// v0.3 backfill: when set, the auto-pin step pins this intent to
	// these specific commits (overriding the tree_hash/commit_hash/
	// goal_text cascade). Used by `mainline start --commits` to cover
	// pre-existing main commits that landed without an intent.
	BackfillCommits []string `json:"backfill_commits,omitempty"`
}

// IntentView is the materialized view of an intent, derived from events + main history.
type IntentView struct {
	IntentID      string       `json:"intent_id"`
	SchemaVersion int          `json:"schema_version"`
	Status        IntentStatus `json:"status"`

	StatusEvidence StatusEvidence `json:"status_evidence"`
	Publication    string         `json:"publication"` // "local_only" | "published"

	ActorID   string `json:"actor_id"`
	ActorName string `json:"actor_name,omitempty"`
	Thread    string `json:"thread"`
	GitBranch string `json:"git_branch"`
	Goal      string `json:"goal"`
	SealedAt  string `json:"sealed_at,omitempty"`

	BaseCommit string `json:"base_commit,omitempty"`
	CodeCommit string `json:"code_commit,omitempty"`
	CodeTree   string `json:"code_tree,omitempty"`

	// v0.3: explicit list of commits the sealed intent claims to
	// cover (set by `mainline start --commits` backfill flow).
	// When non-empty, Pin uses these instead of the tree-hash cascade.
	BackfillCommits []string `json:"backfill_commits,omitempty"`

	Summary     *IntentSummary       `json:"summary,omitempty"`
	Fingerprint *SemanticFingerprint `json:"fingerprint,omitempty"`

	// LastCheck summarises the most recent CheckJudgmentEvent whose
	// candidate_intent equals this IntentID. Nil means no agent has run
	// `mainline check --submit` against this intent yet (or the event
	// log was lost). Replaces the silent black-hole behaviour where
	// CheckSubmit wrote an event no command could read back.
	LastCheck *CheckSummary `json:"last_check,omitempty"`

	// References to external materials (sessions, issues, PRs, docs, CI runs).
	References []Reference `json:"references,omitempty"`

	ViewRebuiltAt string `json:"view_rebuilt_at"`
}

// CheckSummary is the per-intent rollup of the latest phase2 judgment
// stored in IntentView.LastCheck.
type CheckSummary struct {
	EventID          string   `json:"event_id"`
	AtTime           string   `json:"at"`
	ByActor          string   `json:"by"`
	JudgmentCount    int      `json:"judgment_count"`
	HasConflict      bool     `json:"has_conflict"`
	HighestSeverity  string   `json:"highest_severity"`
	NeedsHumanReview bool     `json:"needs_human_review"`
	AgainstIntents   []string `json:"against_intents,omitempty"`
}

type StatusEvidence struct {
	SealedEventID      string `json:"sealed_event_id,omitempty"`
	SupersededByIntent string `json:"superseded_by_intent,omitempty"`
	AbandonedEventID   string `json:"abandoned_event_id,omitempty"`
	MergedMainCommit   string `json:"merged_main_commit,omitempty"`
	MergedVia          string `json:"merged_via,omitempty"` // "merge" | "pin"
	RevertedMainCommit string `json:"reverted_main_commit,omitempty"`

	// v0.3: seal-time worktree state, surfaced in `mainline show`.
	// EvidenceComplete is the seal-time truth that survives forever.
	// Legacy intents (sealed before v0.3) default to "complete / clean".
	EvidenceComplete bool   `json:"evidence_complete,omitempty"`
	WorktreeStatus   string `json:"worktree_status,omitempty"` // "clean" | "dirty" | "untracked"
	SealedAtBranch   string `json:"sealed_at_branch,omitempty"`
}

// CommitNote is the structured JSON attached as a git note to main commits.
// Stored at refs/notes/mainline/intents.
type CommitNote struct {
	SchemaVersion int               `json:"schema_version"`
	Kind          string            `json:"kind"` // "mainline.commit_note"
	Intents       []IntentReference `json:"intents"`
	Reverts       []string          `json:"reverts,omitempty"`
	AddedAt       string            `json:"added_at"`
	AddedBy       string            `json:"added_by"`
	// Via records how the note came to exist:
	//   "merge"             — written by Service.Merge.
	//   "reconcile_auto"    — written by Service.Reconcile after a
	//                         high-confidence automatic match.
	//   "reconcile_manual"  — written by Service.ReconcileManual on an
	//                         explicit (intent, commit) pairing.
	//   "reconcile"         — legacy single-bucket value still emitted by
	//                         older versions and treated as a synonym for
	//                         "reconcile_auto" by current readers.
	//   "manual"            — pre-rc4 ad-hoc value, treated like "reconcile_manual".
	Via string `json:"via,omitempty"`
	// MatchStrategy records which automated rule connected the intent to
	// this commit (only set when Via is reconcile_auto). Values:
	//   "tree_hash"   — commit tree equals intent.code_commit's tree.
	//   "commit_hash" — commit hash equals intent.code_commit.
	//   "goal_text"   — commit message contains intent.goal verbatim.
	//   "manual"      — operator pinned the link via ReconcileManual.
	MatchStrategy string `json:"match_strategy,omitempty"`
	ReconciledAt  string `json:"reconciled_at,omitempty"`
	ReconciledBy  string `json:"reconciled_by,omitempty"`
}

type IntentReference struct {
	IntentID       string `json:"intent_id"`
	SealResultHash string `json:"seal_result_hash"`
}

// Turn is the minimal record unit of one meaningful agent work fragment.
type Turn struct {
	ID           string       `json:"id"`
	IntentID     string       `json:"intent_id"`
	Index        int          `json:"index"`
	CreatedAt    string       `json:"created_at"`
	Description  string       `json:"description"`
	FilesChanged []FileChange `json:"files_changed"`
	DiffStats    DiffStats    `json:"diff_stats"`
	Caller       CallerInfo   `json:"caller"`
	References   []Reference  `json:"references,omitempty"`
}

type TurnSummary struct {
	Index        int      `json:"index"`
	Description  string   `json:"description"`
	FilesChanged []string `json:"files_changed"`
}

type FileChange struct {
	Path         string `json:"path"`
	Status       string `json:"status"` // added|modified|deleted|renamed|copied
	PreviousPath string `json:"previous_path,omitempty"`
	Added        int    `json:"added,omitempty"`
	Removed      int    `json:"removed,omitempty"`
}

type DiffStats struct {
	Files   int `json:"files"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

type CallerInfo struct {
	PID         int    `json:"pid,omitempty"`
	ProcessName string `json:"process_name,omitempty"`
	Cwd         string `json:"cwd"`
}

// Thread is a group of related intents, equivalent to a git branch.
type Thread struct {
	Name         string   `json:"name"`
	GitBranch    string   `json:"git_branch"`
	WorktreePath string   `json:"worktree_path,omitempty"`
	BaseCommit   string   `json:"base_commit,omitempty"`
	Intents      []string `json:"intents"`
	Status       string   `json:"status"` // active|merged|abandoned
	CreatedAt    string   `json:"created_at"`
	ClosedAt     string   `json:"closed_at,omitempty"`
}

// IntentSummary is the structured history of an intent, generated by agents.
// The default seal contract records decisions and review context. Risks,
// anti_patterns, and followups remain in the schema for legacy records only;
// new durable action signals are created through explicit signal commands.
type IntentSummary struct {
	Title        string                `json:"title"`
	What         string                `json:"what"`
	Why          string                `json:"why"`
	UserGoal     string                `json:"user_goal"`
	Decisions    []Decision            `json:"decisions"`
	Rejected     []RejectedAlternative `json:"rejected"`
	Risks        []string              `json:"risks,omitempty"`
	AntiPatterns []AntiPattern         `json:"anti_patterns,omitempty"`
	Followups    []string              `json:"followups,omitempty"`

	// AcknowledgedConstraints records how the agent handled each
	// inherited high-severity constraint it saw during this seal.
	// Keyed by stable ConstraintID ("guard_xxx"). Persists through
	// seal → event → view so lint, Hub, and heatmap can audit.
	AcknowledgedConstraints []AcknowledgedConstraint `json:"acknowledged_constraints,omitempty"`

	// ReviewNotes are ephemeral observations for this PR's reviewer —
	// scope explanations, test-run context, "reviewer should focus on X".
	// They do NOT propagate to inherited constraints, hub heatmap, or
	// context retrieval. After the PR merges they are effectively dead
	// (still stored in the ledger, but no query surface touches them).
	ReviewNotes []string `json:"review_notes,omitempty"`
}

// AcknowledgedConstraint records how the agent handled a specific
// inherited constraint. The ConstraintID is stable ("guard_xxx") so
// matching is exact rather than text-guessing.
type AcknowledgedConstraint struct {
	ConstraintID string `json:"constraint_id"` // "guard_xxx"
	Disposition  string `json:"disposition"`   // preserved | mitigated | not_applicable | intentionally_changed
	Note         string `json:"note,omitempty"`
}

// AntiPattern is the legacy seal-embedded shape for a hard constraint.
// New constraints should be created with `mainline guard add`, not by
// agent-authored seal prose.
type AntiPattern struct {
	What     string `json:"what"`               // the action to avoid
	Why      string `json:"why"`                // load-bearing reason, must be non-empty
	Severity string `json:"severity,omitempty"` // "high" | "medium" | "low"
}

// InheritedConstraintHotspot is the per-file roll-up of inherited explicit
// constraints. The Hub dashboard renders the top files sorted by
// HighSeverityCount (desc) then UnacknowledgedRecentTouches (desc), so
// reviewers land on the surfaces where unack'd hard constraints pile up. This
// is the load-bearing answer to "which file should the reviewer look at first?".
//
// HighSeverityCount counts distinct (source_intent, what) pairs —
// duplicate constraints from the same source that match by multiple files
// collapse to one. UnacknowledgedRecentTouches counts intents sealed within the
// digest window that touched this file AND failed to acknowledge at least one
// applicable high-severity inherited constraint.
type InheritedConstraintHotspot struct {
	FilePath                    string                `json:"file_path"`
	ConstraintCount             int                   `json:"constraint_count"`
	HighSeverityCount           int                   `json:"high_severity_count"`
	UnacknowledgedRecentTouches int                   `json:"unacknowledged_recent_touches"`
	RecentTouches               int                   `json:"recent_touches"`
	Constraints                 []InheritedConstraint `json:"constraints,omitempty"`
}

// InheritedConstraint is a human-promoted guard that the current change is at
// risk of touching, surfaced to the agent during context retrieval and to the
// linter / Hub / PR description as "this constraint pre-dates your work, you
// must at least acknowledge it".
//
// SourceIntent is the intent associated with the original guard. MatchedBy
// lists the file reasons this constraint propagated to the current context. A
// single constraint can match by multiple files; we keep the full list so the
// linter and the Hub can show "matched 3 files" without re-scanning.
//
// What/Why/Severity mirror the explicit Constraint fields, annotated with
// provenance and match reasons.
type InheritedConstraint struct {
	ConstraintID string   `json:"constraint_id"` // "guard_xxx" — stable ID for acknowledgement
	SourceIntent string   `json:"source_intent"`
	What         string   `json:"what"`
	Why          string   `json:"why"`
	Severity     string   `json:"severity,omitempty"`
	MatchedBy    []string `json:"matched_by"`
}

type Decision struct {
	Point     string   `json:"point"`
	Chose     string   `json:"chose"`
	Rationale string   `json:"rationale,omitempty"`
	Rejected  []string `json:"rejected,omitempty"`
}

type RejectedAlternative struct {
	Alternative string `json:"alternative"`
	Reason      string `json:"reason,omitempty"`
}

// SemanticFingerprint is a structured summary for fast conflict pre-screening.
type SemanticFingerprint struct {
	Subsystems           []string            `json:"subsystems"`
	FilesTouched         []string            `json:"files_touched"`
	ArchitecturalClaims  []string            `json:"architectural_claims"`
	BehavioralChanges    []string            `json:"behavioral_changes"`
	APIChanges           []APIChange         `json:"api_changes"`
	DataModelChanges     []DataModelChange   `json:"data_model_changes"`
	SecurityImplications []string            `json:"security_implications"`
	MigrationNotes       []string            `json:"migration_notes"`
	Tags                 []string            `json:"tags"`
	Quality              *FingerprintQuality `json:"quality,omitempty"`
}

type FingerprintQuality struct {
	CompletenessScore        float64  `json:"completeness_score,omitempty"`
	SuspectedMissingSections []string `json:"suspected_missing_sections,omitempty"`
	NeedsHumanReview         bool     `json:"needs_human_review"`
}

type APIChange struct {
	Kind          string `json:"kind"`    // added|modified|removed
	Surface       string `json:"surface"` // http|function|class|cli|event|config
	Signature     string `json:"signature"`
	Compatibility string `json:"compatibility"` // breaking|compatible|unknown
}

type DataModelChange struct {
	Kind              string `json:"kind"` // added|modified|removed
	Name              string `json:"name"`
	Location          string `json:"location,omitempty"`
	Compatibility     string `json:"compatibility"` // breaking|compatible|unknown
	MigrationRequired bool   `json:"migration_required"`
	MigrationNotes    string `json:"migration_notes,omitempty"`
}

// SealResult is the JSON submitted by agents to seal an intent.
type SealResult struct {
	IntentID          string              `json:"intent_id"`
	Summary           IntentSummary       `json:"summary"`
	Fingerprint       SemanticFingerprint `json:"fingerprint"`
	Confidence        SealConfidence      `json:"confidence"`
	UnsupportedClaims []string            `json:"unsupported_claims,omitempty"`
	References        []Reference         `json:"references,omitempty"`

	// ResolvesRisks declares that this intent's work resolves one or
	// more previously-open risks. Each entry carries the risk ID
	// ("risk_<hex>" for explicit risks, "int_<hex>#<index>" for legacy)
	// and an optional rationale.
	// Processed atomically with the sealed event — no separate event.
	ResolvesRisks []RiskResolutionInput `json:"resolves_risks,omitempty"`

	// ResolvesFollowups declares that this intent's work completes one
	// or more previously-open follow-ups. Processed atomically with the
	// sealed event, parallel to ResolvesRisks.
	ResolvesFollowups []FollowupResolutionInput `json:"resolves_followups,omitempty"`
}

// RiskResolutionInput is what agents submit in SealResult.ResolvesRisks
// to declare that their work resolves a previously-open risk.
type RiskResolutionInput struct {
	RiskID    string `json:"risk_id"` // "risk_xxx" or "int_xxx#0"
	Rationale string `json:"rationale,omitempty"`
}

// RiskResolution records that a risk was resolved — either as part
// of a seal (IntentID set) or manually via CLI (ActorID from event).
type RiskResolution struct {
	IntentID  string `json:"intent_id,omitempty"`
	Rationale string `json:"rationale,omitempty"`
	At        string `json:"at,omitempty"`
}

// Risk is the materialised view of a risk entry. Explicit risks are
// stored as signal events; legacy risks are derived from
// IntentSummary.Risks + resolution events.
type Risk struct {
	ID           string           `json:"id"` // "risk_xxx" or "int_xxx#0"
	Text         string           `json:"text"`
	Statement    *RiskStatement   `json:"statement,omitempty"`
	Status       string           `json:"status"` // "open" | "resolved" | "expired"
	SourceIntent string           `json:"source_intent"`
	Files        []string         `json:"files,omitempty"`
	OpenedBy     string           `json:"opened_by,omitempty"`
	OpenedAt     string           `json:"opened_at,omitempty"`
	Source       string           `json:"source,omitempty"`
	ResolvedBy   []RiskResolution `json:"resolved_by,omitempty"`
}

// FollowupResolutionInput is what agents submit in
// SealResult.ResolvesFollowups to declare that their work completes a
// previously-open follow-up.
type FollowupResolutionInput struct {
	FollowupID string `json:"followup_id"` // "followup_xxx" or "int_xxx#0"
	Rationale  string `json:"rationale,omitempty"`
}

// FollowupResolution records that a follow-up was completed — either as
// part of a seal (IntentID set) or manually via CLI.
type FollowupResolution struct {
	IntentID  string `json:"intent_id,omitempty"`
	Rationale string `json:"rationale,omitempty"`
	At        string `json:"at,omitempty"`
}

// Followup is the materialised view of a follow-up entry. Explicit
// follow-ups are stored as signal events; legacy follow-ups are derived
// from IntentSummary.Followups + resolution events.
type Followup struct {
	ID           string               `json:"id"` // "followup_xxx" or "int_xxx#0"
	Text         string               `json:"text"`
	Statement    *FollowupStatement   `json:"statement,omitempty"`
	Status       string               `json:"status"` // "open" | "resolved" | "expired"
	SourceIntent string               `json:"source_intent"`
	Files        []string             `json:"files,omitempty"`
	OpenedBy     string               `json:"opened_by,omitempty"`
	OpenedAt     string               `json:"opened_at,omitempty"`
	Source       string               `json:"source,omitempty"`
	ResolvedBy   []FollowupResolution `json:"resolved_by,omitempty"`
}

type SealConfidence struct {
	Summary     float64 `json:"summary"`
	Fingerprint float64 `json:"fingerprint"`
}

// Reference is an external material linked to an intent. Mainline
// stores only the reference metadata — it never reads, parses, or
// indexes the referenced content. Each reference must have at least
// Ref or URL non-empty.
type Reference struct {
	Kind   string `json:"kind"`             // session | issue | pr | doc | ci | other
	Label  string `json:"label,omitempty"`  // human-readable description
	Client string `json:"client,omitempty"` // agent client (claude-code, cursor, codex, copilot, gemini-cli, opencode)
	Ref    string `json:"ref,omitempty"`    // session/checkpoint/provider id
	URL    string `json:"url,omitempty"`    // file URL / http URL / provider URL
}

// SealPreparePackage is returned by `mainline seal --prepare`.
//
// schema_version 2 (v0.3) added the Snapshot block + Intent.CurrentBranch.
// Older readers still parse v2 packages because the new fields are
// additive; older packages (v1) are still valid input to SealSubmit
// because Snapshot is optional and CurrentBranch defaults to GitBranch.
type SealPreparePackage struct {
	Kind          string `json:"kind"` // "mainline.seal.prepare"
	SchemaVersion int    `json:"schema_version"`

	Intent struct {
		ID            string `json:"id"`
		Goal          string `json:"goal"`
		Thread        string `json:"thread"`
		GitBranch     string `json:"git_branch"`
		BaseCommit    string `json:"base_commit"`
		CurrentHead   string `json:"current_head"`
		CurrentBranch string `json:"current_branch,omitempty"`
	} `json:"intent"`

	Turns       []TurnSummary `json:"turns"`
	DiffSummary struct {
		Files        int      `json:"files"`
		Added        int      `json:"added"`
		Removed      int      `json:"removed"`
		FilesChanged []string `json:"files_changed"`
	} `json:"diff_summary"`
	ChangedFiles []FileChange  `json:"changed_files"`
	Snapshot     *SealSnapshot `json:"snapshot,omitempty"`
	Instruction  string        `json:"instruction"`

	// Starter is a pre-populated SealResult skeleton an agent can
	// mutate-and-submit instead of building from scratch. Fields
	// the engine can derive deterministically (intent_id,
	// fingerprint.files_touched, path-derived subsystems) come
	// pre-filled; fields that need agent judgment (title, what,
	// why, decisions) are present as empty
	// strings / empty arrays so the schema is visible and the
	// editing target is clear. Durable action signals are deliberately
	// absent from this starter; use guard/risk/followup commands when
	// a human explicitly promotes one.
	Starter *SealResult `json:"seal_result_starter,omitempty"`

	// ApplicableOpenRisks lists open risks on files this intent touches.
	// Populated at prepare time so the agent can decide whether to
	// resolve any of them via SealResult.ResolvesRisks. May be stale
	// if the view hasn't synced recently — ViewRebuiltAt carries the
	// timestamp for the agent to gauge freshness.
	ApplicableOpenRisks []Risk `json:"applicable_open_risks,omitempty"`
	ViewRebuiltAt       string `json:"view_rebuilt_at,omitempty"`

	// ApplicableOpenFollowups lists open follow-ups on files this intent
	// touches. Agents can complete any of them via
	// SealResult.ResolvesFollowups.
	ApplicableOpenFollowups []Followup `json:"applicable_open_followups,omitempty"`
}

// SealSnapshot captures the worktree state at prepare time. SealSubmit
// validates these fields against the live repo to prevent stale-prepare
// submissions and silently dirty seals.
type SealSnapshot struct {
	PreparedAt         string       `json:"prepared_at"`
	ChangedFiles       []FileChange `json:"changed_files"`
	WorktreeStatus     string       `json:"worktree_status"` // "clean" | "dirty" | "untracked"
	WorktreeDirtyFiles []string     `json:"worktree_dirty_files,omitempty"`
	EvidenceComplete   bool         `json:"evidence_complete"`
}

// CheckJudgmentResult is submitted by agents after semantic conflict analysis.
type CheckJudgmentResult struct {
	CandidateIntent string             `json:"candidate_intent"`
	Judgments       []ConflictJudgment `json:"judgments"`
	Overall         CheckOverall       `json:"overall"`
}

type CheckOverall struct {
	HasConflict      bool   `json:"has_conflict"`
	HighestSeverity  string `json:"highest_severity"` // none|low|medium|high
	NeedsHumanReview bool   `json:"needs_human_review"`
}

type ConflictJudgment struct {
	TaskID            string             `json:"task_id"`
	HasConflict       bool               `json:"has_conflict"`
	Type              string             `json:"type,omitempty"` // architectural|behavioral|api_breaking|data_model|security|intent_contradiction
	Severity          string             `json:"severity"`       // low|medium|high
	Confidence        float64            `json:"confidence"`
	Explanation       string             `json:"explanation"`
	Evidence          []ConflictEvidence `json:"evidence"`
	ResolutionOptions []string           `json:"resolution_options"`
	NeedsHumanReview  bool               `json:"needs_human_review"`
}

type ConflictEvidence struct {
	MainlineIntent  string `json:"mainline_intent"`
	CandidateIntent string `json:"candidate_intent"`
	MainlineAspect  string `json:"mainline_aspect"`
	CandidateAspect string `json:"candidate_aspect"`
	WhyIncompatible string `json:"why_incompatible"`
}

// CheckPreparePackage is returned by `mainline check --prepare`.
type CheckPreparePackage struct {
	Kind          string `json:"kind"` // "mainline.check.prepare"
	SchemaVersion int    `json:"schema_version"`

	CandidateIntent struct {
		ID          string              `json:"id"`
		Title       string              `json:"title"`
		Summary     IntentSummary       `json:"summary"`
		Fingerprint SemanticFingerprint `json:"fingerprint"`
	} `json:"candidate_intent"`

	Phase1 struct {
		Lookback        int `json:"lookback"`
		BelowThreshold  int `json:"below_threshold"`
		SuspiciousPairs int `json:"suspicious_pairs"`
	} `json:"phase1"`

	JudgmentTasks []CheckTask `json:"judgment_tasks"`
	Instruction   string      `json:"instruction"`
}

type CheckTask struct {
	TaskID string `json:"task_id"`

	MainlineIntent struct {
		ID          string              `json:"id"`
		Title       string              `json:"title"`
		Status      string              `json:"status"` // merged|proposed
		Fingerprint SemanticFingerprint `json:"fingerprint"`
	} `json:"mainline_intent"`

	CandidateIntent struct {
		ID string `json:"id"`
	} `json:"candidate_intent"`

	FingerprintOverlapScore float64 `json:"fingerprint_overlap_score"`
	Instruction             string  `json:"instruction"`
}
