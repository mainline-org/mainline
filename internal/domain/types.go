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

// IntentSummary is the structured summary of an intent, generated by agents.
//
// Risks vs AntiPatterns is a deliberate split:
//
//   - Risks are *soft warnings* — things that could go wrong, things
//     a reviewer should keep an eye on. Free-form English, scored as
//     a relevance signal in retrieval.
//   - AntiPatterns are *hard constraints* — things a future agent
//     MUST NOT do when working in this area. Each carries a why and
//     a severity so retrieval can surface them prominently and never
//     truncate them out.
//
// The distinction is the difference between "this might break old
// clients" (risk) and "do not delete the legacy session middleware
// on /oauth — OAuth callback handler still requires session state"
// (anti-pattern).
type IntentSummary struct {
	Title        string                `json:"title"`
	What         string                `json:"what"`
	Why          string                `json:"why"`
	UserGoal     string                `json:"user_goal"`
	Decisions    []Decision            `json:"decisions"`
	Rejected     []RejectedAlternative `json:"rejected"`
	Risks        []string              `json:"risks"`
	AntiPatterns []AntiPattern         `json:"anti_patterns,omitempty"`
	Followups    []string              `json:"followups"`
}

// AntiPattern is a hard constraint future agents MUST avoid when
// working in this intent's area. Distinct from Risk, which is a
// soft warning. Mainline's retrieval always carries the full
// AntiPattern list — they are never truncated by top-N caps because
// the load-bearing safety property is "the agent saw the constraint
// before editing".
type AntiPattern struct {
	What     string `json:"what"`               // the action to avoid
	Why      string `json:"why"`                // load-bearing reason, must be non-empty
	Severity string `json:"severity,omitempty"` // "high" | "medium" | "low"
}

// InheritedConstraint is an anti_pattern from a *prior* sealed intent
// that the current change is at risk of touching, surfaced to the
// agent during context retrieval and to the linter / Hub / PR
// description as "this constraint pre-dates your work, you must at
// least acknowledge it".
//
// SourceIntent is the intent that sealed the original anti_pattern.
// MatchedBy lists the reasons this constraint propagated to the
// current context — typically `file:<path>` for a touched-file match
// or `subsystem:<name>` for a subsystem match. A single anti_pattern
// can match by multiple files; we keep the full list so the linter
// and the Hub can show "matched 3 files" without re-scanning.
//
// What/Why/Severity mirror AntiPattern verbatim — same shape, just
// annotated with provenance and match reasons.
type InheritedConstraint struct {
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
}

type SealConfidence struct {
	Summary     float64 `json:"summary"`
	Fingerprint float64 `json:"fingerprint"`
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
	// why, decisions, risks, anti_patterns) are present as empty
	// strings / empty arrays so the schema is visible and the
	// editing target is clear. Optional in the schema so older
	// readers still parse the package.
	Starter *SealResult `json:"seal_result_starter,omitempty"`
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
