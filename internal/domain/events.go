package domain

// EventType identifies the kind of actor-log event.
type EventType string

const (
	EventIntentSealed            EventType = "intent.sealed"
	EventIntentSuperseded        EventType = "intent.superseded"
	EventIntentAbandoned         EventType = "intent.abandoned"
	EventIntentMergeAcknowledged EventType = "intent.merge_acknowledged"
	EventCheckJudgment           EventType = "check.judgment"
)

// BaseEvent holds fields common to every actor-log event.
type BaseEvent struct {
	EventID       string    `json:"event_id"`
	SchemaVersion int       `json:"schema_version"`
	EventType     EventType `json:"event_type"`
	ActorID       string    `json:"actor_id"`
	ActorName     string    `json:"actor_name,omitempty"`
	Timestamp     string    `json:"timestamp"`
}

// IntentSealedEvent records the sealing of an intent – the point at which
// its code, summary, and fingerprint are frozen.
//
// v0.3 fields (EvidenceComplete / WorktreeStatus / SealedAtBranch /
// DirtyFiles) make the seal-time worktree state permanently auditable:
// reviewers can tell from the audit trail whether an intent was sealed
// against a clean, committed-only worktree or with `--allow-dirty`.
// Legacy events without these fields default to clean / complete /
// branch=GitBranch via view-rebuild compatibility.
type IntentSealedEvent struct {
	BaseEvent
	IntentID    string              `json:"intent_id"`
	Thread      string              `json:"thread"`
	Goal        string              `json:"goal"`
	GitBranch   string              `json:"git_branch"`
	BaseCommit  string              `json:"base_commit"`
	CodeCommit  string              `json:"code_commit"`
	CodeTree    string              `json:"code_tree"`
	Summary     IntentSummary       `json:"summary"`
	Fingerprint SemanticFingerprint `json:"fingerprint"`
	TurnCount   int                 `json:"turn_count"`
	SealedAt    string              `json:"sealed_at"`

	// v0.3 audit-trail additions:
	EvidenceComplete bool     `json:"evidence_complete,omitempty"`
	WorktreeStatus   string   `json:"worktree_status,omitempty"`
	SealedAtBranch   string   `json:"sealed_at_branch,omitempty"`
	DirtyFiles       []string `json:"dirty_files,omitempty"`

	// v0.3 backfill: explicit list of commits this sealed intent
	// claims to cover. When set, auto-pin pins this intent to each
	// listed commit instead of running the strategy cascade. Used
	// by `mainline start --commits` for retroactive coverage of
	// commits that landed without an intent.
	BackfillCommits []string `json:"backfill_commits,omitempty"`

	// References to external materials (sessions, issues, PRs, docs, CI runs).
	References []Reference `json:"references,omitempty"`
}

// IntentSupersededEvent records that an intent was replaced by a new one.
type IntentSupersededEvent struct {
	BaseEvent
	IntentID     string `json:"intent_id"`
	SupersededBy string `json:"superseded_by"`
	Reason       string `json:"reason,omitempty"`
}

// IntentAbandonedEvent records that an intent was explicitly abandoned.
type IntentAbandonedEvent struct {
	BaseEvent
	IntentID string `json:"intent_id"`
	Reason   string `json:"reason,omitempty"`
}

// IntentMergeAcknowledgedEvent is written when an actor observes their
// intent in the main branch.
type IntentMergeAcknowledgedEvent struct {
	BaseEvent
	IntentID    string `json:"intent_id"`
	MergeCommit string `json:"merge_commit"`
}

// CheckJudgmentEvent records a semantic conflict check judgment.
type CheckJudgmentEvent struct {
	BaseEvent
	CandidateIntent string             `json:"candidate_intent"`
	Judgments       []ConflictJudgment `json:"judgments"`
	Overall         CheckOverall       `json:"overall"`
}

// ActorLogEntry wraps a raw event stored in an actor log blob.
type ActorLogEntry struct {
	BaseEvent
	Payload map[string]interface{} `json:"payload,omitempty"`
	Raw     []byte                 `json:"-"`
}
