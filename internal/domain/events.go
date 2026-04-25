package domain

// EventType identifies the kind of actor-log event.
type EventType string

const (
	EventIntentSealed           EventType = "intent.sealed"
	EventIntentSuperseded       EventType = "intent.superseded"
	EventIntentAbandoned        EventType = "intent.abandoned"
	EventIntentMergeAcknowledged EventType = "intent.merge_acknowledged"
	EventCheckJudgment          EventType = "check.judgment"
)

// BaseEvent holds fields common to every actor-log event.
type BaseEvent struct {
	EventID       string    `json:"event_id"`
	SchemaVersion int       `json:"schema_version"`
	EventType     EventType `json:"event_type"`
	ActorID       string    `json:"actor_id"`
	Timestamp     string    `json:"timestamp"`
}

// IntentSealedEvent records the sealing of an intent – the point at which
// its code, summary, and fingerprint are frozen.
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
