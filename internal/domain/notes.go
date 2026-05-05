package domain

// NotesHealth is a cached, lightweight summary of the Mainline git notes ref.
// It is rebuilt during sync and read by status/preflight. Repair commands still
// run their own live diagnosis before proposing or writing any migration.
type NotesHealth struct {
	NotesTotal               int    `json:"notes_total"`
	ReachableNotes           int    `json:"reachable_notes"`
	UnreachableNotes         int    `json:"unreachable_notes,omitempty"`
	UnreachableMainlineNotes int    `json:"unreachable_mainline_notes"`
	InvalidMainlineNotes     int    `json:"invalid_mainline_notes,omitempty"`
	ProposedCount            int    `json:"proposed_count,omitempty"`
	SuspiciousProposedCount  int    `json:"suspicious_proposed_count,omitempty"`
	LikelyHistoryRewrite     bool   `json:"likely_history_rewrite"`
	RecommendedCommand       string `json:"recommended_command,omitempty"`
	CheckedAt                string `json:"checked_at,omitempty"`
}
