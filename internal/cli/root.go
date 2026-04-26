package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/engine"
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
// Membership criterion: a stale answer would be functionally wrong.
//   - check: phase1 must compare against the freshest remote intents,
//     otherwise it under-reports conflicts.
//   - status / context / list-proposals: these are collaboration
//     awareness surfaces; stale output hides active team work.
//
// Notably absent:
//   - context, list-proposals, log: read-only displays. A stale answer
//     is just slightly out of date, not wrong; users who need fresh
//     team activity can run `mainline sync` first.
//   - pin (v0.2): the user-surface is now the manual-fallback variant
//     `pin <intent> <commit>` only. The user already knows which commit
//     they want — auto-syncing would not change the answer.
var autoSyncCommands = map[string]bool{
	"check": true,
}

var rootCmd = &cobra.Command{
	Use:   "mainline",
	Short: "Distributed intent ledger for coding agents",
	Long:  "Mainline coordinates multiple AI coding agents by recording, checking, and merging their work intents.",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if cwdPath != "" {
			os.Chdir(cwdPath)
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

	// Daily — what every flow uses. Mixed write (start/append/seal),
	// read (status/log/show/list-proposals/context/gaps), and team
	// (sync/check) commands. Humans and agents both run all of these.
	statusCmd.GroupID = groupDaily.ID
	startCmd.GroupID = groupDaily.ID
	appendCmd.GroupID = groupDaily.ID
	sealCmd.GroupID = groupDaily.ID
	logCmd.GroupID = groupDaily.ID
	showCmd.GroupID = groupDaily.ID
	syncCmd.GroupID = groupDaily.ID
	gapsCmd.GroupID = groupDaily.ID
	checkCmd.GroupID = groupDaily.ID
	contextCmd.GroupID = groupDaily.ID
	listProposalsCmd.GroupID = groupDaily.ID

	// Setup & repair — one-time per repo or when something breaks.
	initCmd.GroupID = groupSetup.ID
	doctorCmd.GroupID = groupSetup.ID

	// Advanced — manual fallbacks. AGENTS.md instructs agents NOT to
	// run these unless the user explicitly asks; the auto-flow
	// (auto-pin in sync, auto-publish in seal, GitHub PR for merge)
	// covers the normal case.
	pinCmd.GroupID = groupAdvanced.ID
	mergeCmd.GroupID = groupAdvanced.ID
	publishCmd.GroupID = groupAdvanced.ID
	prDescriptionCmd.GroupID = groupAdvanced.ID
	threadCmd.GroupID = groupAdvanced.ID

	// Hidden — debug utility, not in the user mental model.
	canonicalHashCmd.Hidden = true

	rootCmd.AddCommand(
		initCmd, statusCmd, startCmd, appendCmd, sealCmd, syncCmd,
		publishCmd, doctorCmd, checkCmd, mergeCmd, logCmd, showCmd,
		threadCmd, prDescriptionCmd, pinCmd, contextCmd,
		listProposalsCmd, canonicalHashCmd, gapsCmd,
	)
}

func getService() (*engine.Service, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}
	return engine.NewService(cwd)
}

func outputJSON(data interface{}) {
	resp := domain.JSONResponse{OK: true, Data: data}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(resp)
}

func outputError(err error) {
	if mlErr, ok := err.(*domain.MainlineError); ok {
		if jsonOutput {
			resp := domain.JSONErrorResponse{OK: false, Error: mlErr}
			enc := json.NewEncoder(os.Stderr)
			enc.SetIndent("", "  ")
			enc.Encode(resp)
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
			enc.Encode(resp)
		} else {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
		}
	}
	os.Exit(1)
}
