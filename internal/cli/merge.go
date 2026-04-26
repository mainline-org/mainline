package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var mergeIntentID string

var mergeCmd = &cobra.Command{
	Use:   "merge",
	Short: "(advanced) Squash-merge a sealed intent into main and write its note in one step",
	Long: `For most teams this command is unnecessary. After merging a PR via
the GitHub/GitLab web UI, run 'mainline sync' (or any auto-syncing
command) and 'mainline pin' will automatically link the merged commit
to the intent — no special merge command required.

Use 'mainline merge' only when you don't have a PR system, are
scripting an automation pipeline, or want a single-step squash-merge
that also writes the mainline note. The PR-driven path is the
supported default.`,
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		if mergeIntentID == "" && len(args) > 0 {
			mergeIntentID = args[0]
		}
		if mergeIntentID == "" {
			outputError(fmt.Errorf("--intent or intent ID argument is required"))
			return
		}

		result, err := svc.Merge(mergeIntentID)
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			fmt.Printf("Merged intent %s\n", result.IntentID)
			fmt.Printf("  Commit:   %s\n", result.MergeCommit)
			fmt.Printf("  Strategy: %s\n", result.Strategy)
		}
	},
}

// pinCmd is the manual-fallback escape hatch — required arguments
// `<intent> <commit>`. Auto-pin (the no-args variant that scanned all
// proposed intents) was removed in v0.2 because it is now an internal
// step inside `mainline sync`; nothing the user needs to invoke.
//
// The deprecated `reconcile` alias has also been removed. Notes
// already on the remote with via=reconcile_* continue to render
// correctly via sync.normaliseVia.
//
// Use cases for the manual command (rare):
//   - tree_hash / commit_hash / goal_text all missed (heavily
//     rebased history, cherry-pick across forks, etc.)
//   - reviewer pins another actor's intent without waiting for them
//   - automation / GitHub Action scripts pinning a known commit
var pinCmd = &cobra.Command{
	Use:   "pin <intent> <commit>",
	Short: "Manually pin an intent to a main-branch commit (auto-pin runs inside sync)",
	Long: `Pin the named intent to the named commit unconditionally. Use this
when the automatic strategies (tree_hash → commit_hash → goal_text)
inside 'mainline sync' cannot find a match — heavily rebased history,
cherry-picks across forks, or any other case where heuristic matching
fails.

For the 99 % case, you do not need this command. 'mainline sync' runs
the same matching cascade automatically and writes pin_auto notes.

The note's via is set to pin_explicit and added_by records the calling
actor.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		pinned, err := svc.PinExplicit(args[0], args[1])
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(pinned)
		} else {
			fmt.Printf("Pinned %s -> %s (%s)\n", pinned.IntentID, pinned.Commit, pinned.MatchStrategy)
		}
	},
}

func init() {
	mergeCmd.Flags().StringVar(&mergeIntentID, "intent", "", "intent ID to merge")
}
