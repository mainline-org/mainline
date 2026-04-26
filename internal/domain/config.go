package domain

// TeamConfig is stored at .mainline/config.toml, committed to repo.
type TeamConfig struct {
	Mainline MainlineSection `toml:"mainline"`
	Sync     SyncSection     `toml:"sync"`
	Check    CheckSection    `toml:"check"`
	Publish  PublishSection  `toml:"publish"`
	Merge    MergeSection    `toml:"merge"`
	Log      LogSection      `toml:"log"`
}

type MainlineSection struct {
	SchemaVersion     int    `toml:"schema_version"`
	MainBranch        string `toml:"main_branch"`
	ActorLogPrefix    string `toml:"actor_log_prefix"` // refs/heads/_mainline/actor
	RequireSealBefore string `toml:"require_seal_before"` // push|merge|never
}

type SyncSection struct {
	AutoSync bool   `toml:"auto_sync"`
	Interval string `toml:"interval"`
	// FreshnessSeconds gates the auto-before-command sync wrapper:
	// if a command in the auto-before list runs within FreshnessSeconds
	// of the last successful sync, the wrapper skips the network round
	// trip and uses the cached view. 0 means "always sync".
	FreshnessSeconds int `toml:"freshness_seconds"`
	// StaleThresholdSeconds is when `mainline status` starts marking
	// the sync state as stale in human output and JSON. Defaults to
	// 24 hours; teams with slower-moving repos can raise it.
	StaleThresholdSeconds int64 `toml:"stale_threshold_seconds"`
	// AutoCheckAfterSync runs phase1 conflict detection over newly
	// fetched proposed intents at the end of every sync. The list
	// of warnings is added to the SyncResult and printed.
	AutoCheckAfterSync bool `toml:"auto_check_after_sync"`
	// AutoPinAfterSync runs the Pin strategy cascade after the view
	// is rebuilt. Default true as of v0.2 — sync becomes the single
	// command users need for the GitHub PR + auto-pin workflow,
	// removing the need to invoke `mainline pin` separately.
	AutoPinAfterSync bool `toml:"auto_pin_after_sync"`
}

type CheckSection struct {
	AutoCheck          bool    `toml:"auto_check"`
	Lookback           int     `toml:"lookback"`
	Phase1Threshold    float64 `toml:"phase1_threshold"`
	RequireBeforeMerge bool    `toml:"require_before_merge"`
}

type PublishSection struct {
	AutoPublish bool `toml:"auto_publish"`
}

type MergeSection struct {
	Strategy string `toml:"strategy"` // squash|merge|rebase
}

type LogSection struct {
	DefaultLimit int `toml:"default_limit"`
}

// LocalConfig is stored at .mainline/local.toml, NOT committed.
type LocalConfig struct {
	Actor ActorSection `toml:"actor"`
}

type ActorSection struct {
	ID   string `toml:"id"`
	Name string `toml:"name"`
}

// Identity represents the local actor's identity.
type Identity struct {
	ActorID   string `json:"actor_id"`
	ActorName string `json:"actor_name"`
	CreatedAt string `json:"created_at"`
}

func DefaultTeamConfig() TeamConfig {
	return TeamConfig{
		Mainline: MainlineSection{
			SchemaVersion:     1,
			MainBranch:        "main",
			ActorLogPrefix:    "_mainline/actor",
			RequireSealBefore: "push",
		},
		Sync: SyncSection{
			AutoSync:              true,
			Interval:              "30s",
			FreshnessSeconds:      300,   // 5 min — cheap commands may chain in quick succession
			StaleThresholdSeconds: 86400, // 24h — flag in `mainline status`
			AutoCheckAfterSync:    true,
			AutoPinAfterSync:      true,
		},
		Check: CheckSection{
			AutoCheck: true,
			Lookback:  50,
			// Phase1Threshold lowered from 0.15 to 0.10 in rc4 dogfood:
			// real cross-PR pairs that should have triggered judgment
			// (same subsystem, overlapping files) scored ~0.146 under
			// the weighted-jaccard formula, narrowly missing 0.15. The
			// spec explicitly flags this value as "calibrate after 50+
			// real conflict cases via grid search"; until that data
			// exists, prefer false positives (extra phase2 judgment
			// tasks for the agent) over false negatives (missed
			// conflicts). See docs_for_ai/mainline-spec-v0.1-rc4-patch.md.
			Phase1Threshold:    0.10,
			RequireBeforeMerge: false,
		},
		Publish: PublishSection{
			AutoPublish: false,
		},
		Merge: MergeSection{
			Strategy: "squash",
		},
		Log: LogSection{
			DefaultLimit: 20,
		},
	}
}
