package domain

// ConflictPair is a phase1 fingerprint-overlap warning between a
// "local" intent (the candidate the user is working on or has just
// sealed) and a "remote" intent already in the materialised view.
//
// rc5 introduces it for two surfaces:
//   - Service.Sync writes new pairs to SyncResult.NewConflicts when
//     auto_check_after_sync is on.
//   - Service.SealSubmit writes pairs to SealSubmitResult.Conflicts
//     after running phase1 against the freshly-sealed fingerprint.
//
// ConflictPair is purely advisory — phase1 only screens; phase2 (the
// agent-driven `mainline check --submit`) is what decides whether the
// pair is a real semantic conflict. Seal/sync never block on a pair.
type ConflictPair struct {
	LocalIntent      string   `json:"local_intent"`
	RemoteIntent     string   `json:"remote_intent"`
	OverlapScore     float64  `json:"overlap_score"`
	Confidence       string   `json:"confidence"` // "high" | "medium" | "low"
	Reason           string   `json:"reason"`
	LocalSource      string   `json:"local_source"`  // "sealed" | "draft"
	RemoteStatus     string   `json:"remote_status"` // "proposed" | "merged"
	SuggestedActions []string `json:"suggested_actions,omitempty"`
}

// PartialFingerprint is the best-effort fingerprint inferred from a
// DraftIntent (no SealResult yet). It carries strictly less signal
// than a SemanticFingerprint — only the dimensions a draft can
// honestly populate. The IsPartial flag tells the conflict scorer to
// apply a lower threshold and tag any resulting ConflictPair with
// confidence "low".
//
// rc5 produces one of these per active local draft so sync's
// auto-check can surface "the work you're doing might collide with
// something a teammate just sealed" before the user even reaches
// `mainline seal --prepare`.
type PartialFingerprint struct {
	FilesTouched []string `json:"files_touched"`
	Keywords     []string `json:"keywords"`
	Subsystems   []string `json:"subsystems"`
	IsPartial    bool     `json:"is_partial"`
}

// LastSync is persisted at .ml-cache/views/last-sync.json after every
// successful Service.Sync. The file is the basis for two related
// behaviours: the freshness-window short-circuit in the
// auto-before-command CLI wrapper, and the `mainline status`
// staleness indicator.
type LastSync struct {
	At            string `json:"at"` // RFC3339
	ByActor       string `json:"by_actor"`
	MainHead      string `json:"main_head"`
	NewSealedSeen int    `json:"new_sealed_seen"` // delta vs prior LastSync
}

// Phase1WarningsCache snapshots every cross-actor phase1 ConflictPair
// reachable from the current view. Persisted at
// .ml-cache/views/phase1-warnings.json after every Service.Sync that
// runs auto-check. Lifetime is "until next sync" — phase1 results are
// transient (next sync may add or drop pairs as actor logs evolve), so
// the file is overwritten in full each time, never appended to.
//
// The render layer asks "does intent X currently have any phase1
// warning?" by scanning Pairs for either side equal to X. With < 100
// pairs and < 50 active intents this is trivially fast.
type Phase1WarningsCache struct {
	SchemaVersion int            `json:"schema_version"`
	UpdatedAt     string         `json:"updated_at"`
	Pairs         []ConflictPair `json:"pairs"`
}
