package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"mainline/internal/domain"
	"mainline/internal/engine"
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
//   - pin / reconcile: pinning needs the latest main commits; missing a
//     fresh commit means a proposed intent stays unmatched.
//   - check: phase1 must compare against the freshest remote intents,
//     otherwise it under-reports conflicts.
//   - status / context / list-proposals: these are collaboration
//     awareness surfaces; stale output hides active team work.
//
// log is intentionally opt-in via `mainline log --sync`: full history
// browsing should stay cheap, while reviewer scans can request freshness.
var autoSyncCommands = map[string]bool{
	"check":          true,
	"context":        true,
	"list-proposals": true,
	"pin":            true,
	"reconcile":      true,
	"status":         true,
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

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "output in JSON format")
	rootCmd.PersistentFlags().BoolVar(&quietMode, "quiet", false, "suppress non-error output")
	rootCmd.PersistentFlags().StringVar(&cwdPath, "cwd", "", "set working directory")
	rootCmd.PersistentFlags().BoolVar(&noSync, "no-sync", false, "skip the auto-sync some commands run before executing")

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(appendCmd)
	rootCmd.AddCommand(sealCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(publishCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(mergeCmd)
	rootCmd.AddCommand(logCmd)
	rootCmd.AddCommand(showCmd)
	rootCmd.AddCommand(threadCmd)
	// rc3: pr-trailer removed, metadata goes via git notes
	rootCmd.AddCommand(prDescriptionCmd)
	rootCmd.AddCommand(pinCmd)
	// reconcile is kept as a hidden deprecated alias of pin.
	rootCmd.AddCommand(reconcileCmd)
	rootCmd.AddCommand(contextCmd)
	rootCmd.AddCommand(listProposalsCmd)
	rootCmd.AddCommand(canonicalHashCmd)
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
