package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/engine"
	"github.com/mainline-org/mainline/internal/webhook"
)

var (
	jsonOutput bool
	quietMode  bool
	cwdPath    string
	noSync     bool
)

// autoSyncCommands is the list of commands that auto-trigger
// a sync (subject to the freshness window) before running. Hardcoded
// rather than config because it's a product behaviour, not team policy.
// Any command whose Use line first word appears here is wrapped.
//
// Membership criterion: a stale answer would be functionally wrong
// for the command's primary use case.
//
//   - check  — phase1 must compare against the freshest remote
//              intents, otherwise it under-reports conflicts.
//   - status — rc7+ daily entry point. The Recent sealed intents
//              block is "what just landed across the team", and
//              the Suggestions block reads its own staleness state.
//              A stale answer means "agent thinks team is idle when
//              someone just shipped" — exactly the failure mode the
//              command exists to prevent.
//   - gaps   — reads main HEAD's last 30 commits against the local
//              view's intent list. When `git fetch` has pulled in a
//              new merge commit + its note but the view rebuild has
//              not run yet, the note's intent is not in liveIntents
//              and the commit reports as uncovered. Auto-sync (gated
//              by freshness) keeps that false-uncovered window
//              within the 300s budget.
//   - hub export, hub open — both rebuild a static snapshot from
//              the local intent view. Stale data on the index page
//              ("recent intents") and the per-file history pages is
//              the same failure mode `status` exists to prevent —
//              human reader opens the hub and thinks the team is
//              idle when someone just shipped. Subcommand paths
//              live in this map keyed as "<parent> <name>" because
//              cobra's cmd.Name() returns just the leaf
//              ("export"/"open") which would collide if another
//              top-level command ever used those names.
//
// Notably absent:
//
//   - context, list-proposals — read-only displays often called from
//     scripts where a network round-trip is unwelcome. Same case as
//     status arguably applies; tracked as a future enhancement.
//   - log — has its own opt-in `--sync` flag (logSync) so users who
//     want fresh log opt in explicitly; default off keeps the
//     command instant for repeated browsing.
//   - pin — the v0.2 user-surface is the manual-fallback variant
//     `pin <intent> <commit>`; the user already knows the target
//     commit, sync would not change the answer.
//
// The freshness window (Sync.FreshnessSeconds, default 300s) caps
// the cost: even when wrapped, repeated calls within 5 minutes
// reuse the local view. Network failure is non-fatal — the wrapped
// command falls through to local data with a stderr warning.
// Users can always opt out per-call with `--no-sync`.
var autoSyncCommands = map[string]bool{
	"check":      true,
	"status":     true,
	"gaps":       true,
	"digest":     true,
	"hub export": true,
	"hub open":   true,
	"pr-comment": true,
}

var rootCmd = &cobra.Command{
	Use:   "mainline",
	Short: "Distributed intent ledger for coding agents",
	Long:  "Mainline coordinates multiple AI coding agents by recording, checking, and merging their work intents.",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if cwdPath != "" {
			// If chdir fails, subsequent commands run in the original
			// directory and surface their own clearer errors. No need
			// to escalate here.
			_ = os.Chdir(cwdPath)
		}
		maybeAutoSync(cmd)
	},
}

// maybeAutoSync triggers a sync before commands listed in
// autoSyncCommands, unless --no-sync is set or the last sync is within
// the configured freshness window. Failures are non-fatal and printed
// to stderr — the wrapped command always runs, possibly against stale
// data (network being down should never block local-data queries).
func maybeAutoSync(cmd *cobra.Command) {
	if noSync {
		return
	}
	if !shouldAutoSync(cmd) {
		return
	}
	svc, err := getService()
	if err != nil {
		// Service init failure (no .mainline) — let the wrapped
		// command surface the real error.
		return
	}
	cfg, err := svc.GetTeamConfigForCLI()
	if err != nil {
		return
	}
	freshness := time.Duration(cfg.Sync.FreshnessSeconds) * time.Second
	if freshness > 0 {
		ls, _ := svc.GetLastSyncForCLI()
		if ls != nil {
			if t, err := time.Parse(time.RFC3339, ls.At); err == nil {
				if time.Since(t) < freshness {
					return // still fresh
				}
			}
		}
	}
	if !jsonOutput {
		fmt.Fprintln(os.Stderr, "Syncing with team...")
	}
	if _, err := svc.Sync(); err != nil {
		if !jsonOutput {
			fmt.Fprintf(os.Stderr, "⚠ sync failed (%v); using local data\n", err)
		}
	}
}

func shouldAutoSync(cmd *cobra.Command) bool {
	if autoSyncCommands[cmd.Name()] {
		return true
	}
	// Subcommand path lookup: cobra's cmd.Name() returns just the
	// leaf, so a command like `mainline hub export` would only
	// match "export" — colliding with any other top-level command
	// ever named that. Match on "<parent> <leaf>" instead so the
	// map can carry full paths unambiguously.
	if parent := cmd.Parent(); parent != nil {
		if autoSyncCommands[parent.Name()+" "+cmd.Name()] {
			return true
		}
	}
	return cmd.Name() == "log" && logSync
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// Help-output groups. Cobra renders commands grouped by these
// titles when `--help` is invoked, in the order added below.
//
// The split is by FREQUENCY, not audience. Agents and humans run
// mostly the same commands; the question is "do you reach for this
// daily" vs "is this a one-time setup or manual fallback". Agents
// inspect with `status` / `log` / `show` / `context` /
// `list-proposals` / `gaps` exactly the same way humans do.
//
// Internal/debug-only commands set Hidden = true (canonical-hash) so
// they remain runnable for debugging but do not pollute the help.
var (
	groupDaily    = &cobra.Group{ID: "daily", Title: "Daily commands:"}
	groupSetup    = &cobra.Group{ID: "setup", Title: "Setup & repair (rare):"}
	groupAdvanced = &cobra.Group{ID: "advanced", Title: "Advanced (manual fallbacks; auto-flow normally handles these):"}
)

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "output in JSON format")
	rootCmd.PersistentFlags().BoolVar(&quietMode, "quiet", false, "suppress non-error output")
	rootCmd.PersistentFlags().StringVar(&cwdPath, "cwd", "", "set working directory")
	rootCmd.PersistentFlags().BoolVar(&noSync, "no-sync", false, "skip the auto-sync some commands run before executing")

	rootCmd.AddGroup(groupDaily, groupSetup, groupAdvanced)

	// Daily — what every flow uses. Mixed write (start/append/seal/
	// abandon), read (status/log/show/list-proposals/context/gaps),
	// and team (sync/check) commands. Humans and agents both run all
	// of these.
	statusCmd.GroupID = groupDaily.ID
	startCmd.GroupID = groupDaily.ID
	appendCmd.GroupID = groupDaily.ID
	sealCmd.GroupID = groupDaily.ID
	abandonCmd.GroupID = groupDaily.ID
	logCmd.GroupID = groupDaily.ID
	showCmd.GroupID = groupDaily.ID
	traceCmd.GroupID = groupDaily.ID
	syncCmd.GroupID = groupDaily.ID
	gapsCmd.GroupID = groupDaily.ID
	digestCmd.GroupID = groupDaily.ID
	checkCmd.GroupID = groupDaily.ID
	contextCmd.GroupID = groupDaily.ID
	listProposalsCmd.GroupID = groupDaily.ID

	// Setup & repair — one-time per repo or when something breaks.
	initCmd.GroupID = groupSetup.ID
	doctorCmd.GroupID = groupSetup.ID
	agentsCmd.GroupID = groupSetup.ID

	// Advanced — manual fallbacks. AGENTS.md instructs agents NOT to
	// run these unless the user explicitly asks; the auto-flow
	// (auto-pin in sync, auto-publish in seal, GitHub PR for merge)
	// covers the normal case.
	pinCmd.GroupID = groupAdvanced.ID
	mergeCmd.GroupID = groupAdvanced.ID
	publishCmd.GroupID = groupAdvanced.ID
	prDescriptionCmd.GroupID = groupAdvanced.ID
	prCommentCmd.GroupID = groupAdvanced.ID
	threadCmd.GroupID = groupAdvanced.ID

	// Hidden — debug utility, not in the user mental model.
	canonicalHashCmd.Hidden = true

	// Setup group for the hooks integration: rare per-repo install,
	// then forgotten about.
	hooksCmd.GroupID = groupSetup.ID
	webhookCmd.GroupID = groupSetup.ID

	// Hub is a human-reader surface over the local intent view —
	// occasional use, not part of the daily agent loop.
	hubCmd.GroupID = groupSetup.ID

	// Lint is a setup/repair-style tool: occasional use, not part of
	// the daily agent loop today (the agent loop already runs
	// retrieval); ship under the setup group so it is visible but
	// not at the top of the help.
	lintCmd.GroupID = groupSetup.ID

	// Eval is the agent-eval harness — meta-tooling, not part of the
	// daily agent loop. Setup group keeps it visible without
	// crowding the daily commands.
	evalCmd.GroupID = groupSetup.ID

	rootCmd.AddCommand(
		initCmd, statusCmd, startCmd, appendCmd, sealCmd, syncCmd,
		publishCmd, doctorCmd, checkCmd, mergeCmd, logCmd, showCmd,
		threadCmd, prDescriptionCmd, prCommentCmd, pinCmd, contextCmd,
		listProposalsCmd, canonicalHashCmd, gapsCmd, digestCmd, abandonCmd,
		traceCmd, agentsCmd,
		hooksCmd, webhookCmd, webhookDispatchCmd,
		hubCmd, lintCmd, evalCmd,
	)
}

func getService() (*engine.Service, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}
	svc, err := engine.NewService(cwd)
	if err != nil {
		return nil, err
	}
	attachWebhookBus(svc, "engine")
	return svc, nil
}

// attachWebhookBus wires the production webhook event bus when the
// team config carries any [[webhook]] subscriptions. No-op otherwise:
// the engine treats nil Bus as "drop events on the floor", and the
// CLI never pays the cost of writing to .ml-cache/webhook-queue/ when
// no one is listening.
//
// source is "engine" for normal CLI commands and "hook" for events
// originating from the hooks Dispatcher; the value rides through the
// envelope and is exposed to subscribers as the X-Mainline-Source
// header in the sender.
func attachWebhookBus(svc *engine.Service, source string) {
	cfg, err := svc.Store.ReadTeamConfig()
	if err != nil || cfg == nil || len(cfg.Webhooks) == 0 {
		return
	}
	binary, _ := os.Executable()
	bus := webhook.New(
		svc.Store.WebhookQueueDir(),
		cfg.Webhooks,
		binary,
		source,
	)
	svc.SetBus(bus)
}

func outputJSON(data interface{}) {
	resp := domain.JSONResponse{OK: true, Data: data}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	// stdout encode is best-effort: a closed pipe is not a mainline
	// failure and would already have prevented earlier output too.
	_ = enc.Encode(resp)
}

func outputError(err error) {
	if mlErr, ok := err.(*domain.MainlineError); ok {
		if jsonOutput {
			resp := domain.JSONErrorResponse{OK: false, Error: mlErr}
			enc := json.NewEncoder(os.Stderr)
			enc.SetIndent("", "  ")
			_ = enc.Encode(resp)
		} else {
			fmt.Fprintf(os.Stderr, "error [%s]: %s\n", mlErr.Code, mlErr.Message)
			for _, a := range mlErr.SuggestedActions {
				fmt.Fprintf(os.Stderr, "  suggestion: %s\n", a)
			}
		}
	} else {
		if jsonOutput {
			resp := domain.JSONErrorResponse{OK: false, Error: &domain.MainlineError{
				Code: domain.ErrIOError, Message: err.Error(),
			}}
			enc := json.NewEncoder(os.Stderr)
			enc.SetIndent("", "  ")
			_ = enc.Encode(resp)
		} else {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
		}
	}
	os.Exit(1)
}
