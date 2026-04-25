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
	AutoSync bool `toml:"auto_sync"`
	Interval string `toml:"interval"`
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
			AutoSync: true,
			Interval: "30s",
		},
		Check: CheckSection{
			AutoCheck:          true,
			Lookback:           50,
			Phase1Threshold:    0.15,
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
