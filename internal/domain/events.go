package domain

// EventType identifies the kind of actor-log event.
type EventType string

const (
	EventIntentSealed            EventType = "intent.sealed"
	EventIntentSuperseded        EventType = "intent.superseded"
	EventIntentAbandoned         EventType = "intent.abandoned"
	EventIntentMergeAcknowledged EventType = "intent.merge_acknowledged"
	EventCheckJudgment           EventType = "check.judgment"
	EventConstraintAdded         EventType = "constraint.added"
	EventRiskAdded               EventType = "risk.added"
	EventRiskResolved            EventType = "risk.resolved"
	EventFollowupAdded           EventType = "followup.added"
	EventFollowupResolved        EventType = "followup.resolved"
	EventActorLogAccepted        EventType = "actor_log.accepted"
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

	// v0.4 risk lifecycle: risks resolved atomically with this seal.
	// Carried on the sealed event (not as separate events) so the
	// write is one actor-log append — no partial-resolution risk.
	ResolvesRisks []RiskResolutionInput `json:"resolves_risks,omitempty"`

	// Follow-up lifecycle: follow-ups completed atomically with this seal.
	// Kept parallel to risk resolution so old summary.followups stay
	// immutable while the effective open queue can shrink.
	ResolvesFollowups []FollowupResolutionInput `json:"resolves_followups,omitempty"`
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

// ConstraintAddedEvent records a human-promoted guard. It is separate
// from IntentSealedEvent so agents cannot create constraints by filling
// seal.summary.anti_patterns.
type ConstraintAddedEvent struct {
	BaseEvent
	ConstraintID string   `json:"constraint_id"`
	IntentID     string   `json:"intent_id,omitempty"`
	Files        []string `json:"files,omitempty"`
	What         string   `json:"what"`
	Why          string   `json:"why"`
	Severity     string   `json:"severity,omitempty"`
	Source       string   `json:"source,omitempty"`
	SourceNote   string   `json:"source_note,omitempty"`
}

// RiskAddedEvent records an explicit review-facing risk.
type RiskAddedEvent struct {
	BaseEvent
	RiskID    string        `json:"risk_id"`
	IntentID  string        `json:"intent_id,omitempty"`
	Files     []string      `json:"files,omitempty"`
	Statement RiskStatement `json:"statement"`
	Source    string        `json:"source,omitempty"`
}

// RiskResolvedEvent records a manual risk resolution via
// `mainline risks resolve`. Seal-time resolutions are carried on
// IntentSealedEvent.ResolvesRisks instead (atomic with the seal).
type RiskResolvedEvent struct {
	BaseEvent
	RiskID           string `json:"risk_id"`                      // "int_xxx#0"
	ResolvedByIntent string `json:"resolved_by_intent,omitempty"` // optional: the intent whose work resolved it
	Rationale        string `json:"rationale,omitempty"`
}

// FollowupAddedEvent records an explicit deferred work item.
type FollowupAddedEvent struct {
	BaseEvent
	FollowupID string            `json:"followup_id"`
	IntentID   string            `json:"intent_id,omitempty"`
	Files      []string          `json:"files,omitempty"`
	Statement  FollowupStatement `json:"statement"`
	Source     string            `json:"source,omitempty"`
}

// FollowupResolvedEvent records a manual follow-up resolution via
// `mainline followups resolve`. Seal-time resolutions are carried on
// IntentSealedEvent.ResolvesFollowups instead (atomic with the seal).
type FollowupResolvedEvent struct {
	BaseEvent
	FollowupID       string `json:"followup_id"`                  // "int_xxx#0"
	ResolvedByIntent string `json:"resolved_by_intent,omitempty"` // optional: the intent whose work resolved it
	Rationale        string `json:"rationale,omitempty"`
}

// ActorLogAcceptedEvent records an upstream trust-boundary decision:
// the current actor explicitly accepted another actor's Mainline actor
// log from a fork/remote/import ref into this repo's actor-log
// namespace. The accepted actor's own sealed events keep their
// original actor_id; this event only explains provenance in the
// upstream view.
type ActorLogAcceptedEvent struct {
	BaseEvent
	AcceptedActorID    string   `json:"accepted_actor_id"`
	SourceRemote       string   `json:"source_remote,omitempty"`
	SourceRef          string   `json:"source_ref"`
	SourceHead         string   `json:"source_head"`
	TargetRef          string   `json:"target_ref"`
	PreviousTargetHead string   `json:"previous_target_head,omitempty"`
	EventCount         int      `json:"event_count"`
	SealedIntentIDs    []string `json:"sealed_intent_ids,omitempty"`
	Verified           bool     `json:"verified"`
	AuthorSealed       bool     `json:"author_sealed"`
}

// ActorLogEntry wraps a raw event stored in an actor log blob.
type ActorLogEntry struct {
	BaseEvent
	Payload map[string]interface{} `json:"payload,omitempty"`
	Raw     []byte                 `json:"-"`
}
