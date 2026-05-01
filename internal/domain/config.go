package domain

// TeamConfig is stored at .mainline/config.toml, committed to repo.
type TeamConfig struct {
	Mainline MainlineSection `toml:"mainline"`
	Sync     SyncSection     `toml:"sync"`
	Check    CheckSection    `toml:"check"`
	Publish  PublishSection  `toml:"publish"`
	Merge    MergeSection    `toml:"merge"`
	Log      LogSection      `toml:"log"`

	// Hooks controls the agent-hooks subsystem (cursor / codex /
	// claude-code etc). When the hook entries are installed in an
	// agent's config (.cursor/hooks.json and friends), the agent
	// invokes `mainline hooks <agent> <event>` at lifecycle points
	// and the dispatcher runs the matching mainline command. The
	// section is empty in pre-hook configs and gets populated with
	// defaults the first time the hook flow runs.
	Hooks HooksSection `toml:"hooks"`

	// Webhooks is a per-team list of HTTP destinations that should
	// receive domain events (intent_started / turn_appended /
	// intent_sealed / sync_completed / conflict_detected /
	// check_judged). Each entry is one subscription; the same event
	// fans out to all matching subscriptions. The list is committed
	// alongside config.toml because hook outputs are a team
	// observability concern, not a per-developer one.
	Webhooks []WebhookSubscription `toml:"webhook"`
}

type MainlineSection struct {
	SchemaVersion     int    `toml:"schema_version"`
	MainBranch        string `toml:"main_branch"`
	ActorLogPrefix    string `toml:"actor_log_prefix"`    // refs/heads/_mainline/actor
	RequireSealBefore string `toml:"require_seal_before"` // push|merge|never
	// Remote is the git remote name mainline reads/writes notes and
	// actor-log refs to. Defaults to "origin" — what `git clone`
	// produces. Teams that use a different remote name (e.g. forks
	// with `upstream`, multi-remote setups, GitLab/Gitea conventions)
	// set this explicitly.
	Remote string `toml:"remote"`
	// Skip controls v0.3 coverage classification: which commits on
	// main are treated as deliberately exempt from the
	// "every commit needs an explaining intent" invariant. See spec
	// docs_for_ai/mainline-spec-v0.3-coverage-and-snapshot.md §D.
	Skip MainlineSkipSection `toml:"skip"`
}

// MainlineSkipSection lists regex patterns matched against commit
// subject. A subject matching any pattern classifies the commit as
// `skipped` (rather than `uncovered`) in coverage output. Per-commit
// `Mainline-Skip:` trailers are the other entry point and do not need
// config — they live in the commit message itself.
type MainlineSkipSection struct {
	Patterns []string `toml:"patterns"`
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

// HooksSection mirrors the dispatch toggles in internal/hooks. We
// keep them in domain so config parsing has a single home; the hooks
// package consumes them via a plain DispatchSettings struct (no
// import dependency back into domain).
//
// The section is deliberately tiny because hooks DO NOT make semantic
// decisions for the agent. They run mechanical operations (sync) and
// inject context for the agent to read at session start; everything
// else (intent start / append / seal-prepare / seal-submit / check
// verdicts) is the agent's job per the Mainline skill workflow,
// regardless of whether hooks are installed.
type HooksSection struct {
	// Enabled is the soft kill-switch. When false, the on-disk hook
	// commands still fire but the dispatcher exits immediately. Lets
	// users pause automation without uninstalling.
	Enabled bool `toml:"enabled" json:"enabled"`

	// AutoSyncOnSessionStart: SessionStart -> mainline sync. The
	// only mechanical auto-flow toggle that survives, because sync
	// is deterministic — no semantic judgment involved.
	AutoSyncOnSessionStart bool `toml:"auto_sync_on_session_start" json:"auto_sync_on_session_start"`
}

// WebhookSubscription is one HTTP destination for the domain-event
// fan-out. Multiple entries are supported; each event is delivered
// to every subscription whose Events filter matches.
type WebhookSubscription struct {
	// ID is a stable handle the CLI uses for `mainline webhook
	// remove <id>` / `test <id>` / `retry --id <id>`. If the user
	// does not set one, the cli generates "wh_<8>" on add.
	ID string `toml:"id" json:"id"`

	// URL is the destination endpoint. HTTP and HTTPS only — no
	// schemes like file://. The sender does NOT pre-resolve DNS;
	// network errors are caught at delivery time.
	URL string `toml:"url" json:"url"`

	// Events filters which DomainEvent.Name values to deliver. An
	// empty list means "deliver everything", which is fine for a
	// monitoring dashboard but probably not for a paging system.
	Events []string `toml:"events" json:"events,omitempty"`

	// Secret signs the body with HMAC-SHA256; subscribers verify
	// via the X-Mainline-Signature header. Supports the literal
	// "$ENV:VAR_NAME" form so secrets do not have to live in the
	// committed config.toml. Empty disables signing.
	Secret string `toml:"secret" json:"secret,omitempty"`

	// TimeoutSeconds caps each delivery attempt. 0 means use the
	// sender's default (5s). Long timeouts on slow webhooks would
	// otherwise hold the detached sender process for minutes.
	TimeoutSeconds int `toml:"timeout_seconds" json:"timeout_seconds,omitempty"`
}

// DefaultHooksSection returns the defaults that ship the first time
// `mainline hooks install` writes the section. Users can flip
// AutoSyncOnSessionStart off (slow networks etc.) without disabling
// the whole subsystem.
func DefaultHooksSection() HooksSection {
	return HooksSection{
		Enabled:                true,
		AutoSyncOnSessionStart: true,
	}
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
			Remote:            "origin",
			Skip: MainlineSkipSection{
				// Defaults cover the most common low-information commit
				// shapes that would otherwise drown the gaps surface
				// in noise. Teams customise via [mainline.skip].patterns.
				//
				// `^mainline: init` is the commit `mainline init` itself
				// writes — it has no underlying agent intent and would
				// otherwise show as uncovered on every fresh-repo status,
				// which the alpha walkthrough flagged as a false-alarm
				// first impression.
				Patterns: []string{
					"^Merge pull request ",
					"^Merge branch ",
					"^chore: bump version",
					"^mainline: init",
				},
			},
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
