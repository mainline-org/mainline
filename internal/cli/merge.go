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

// pinCmd is the rc4 Patch 7 rename of `reconcile`. Both names register
// the same Run function; see reconcileCmd below.
var pinCmd = &cobra.Command{
	Use:   "pin [intent commit]",
	Short: "Pin sealed intents to their main-branch commits",
	Long: `Without arguments: scan every proposed intent and try to associate
it with a main-branch commit using a strategy cascade (tree_hash →
commit_hash → goal_text). Each match writes a pin_auto note and pushes.

With two arguments (mainline pin <intent> <commit>): pin the named
intent to the named commit unconditionally. The note's via is set to
pin_explicit and added_by records the calling actor.

Replaces 'mainline reconcile', which is kept as a deprecated alias.`,
	Args: cobra.MaximumNArgs(2),
	Run:  runPinOrReconcile,
}

// reconcileCmd is the deprecated pre-rc4 spelling. It runs the exact
// same code as pinCmd; the only difference is the help text. Scripts
// that already call `mainline reconcile ...` keep working unchanged.
var reconcileCmd = &cobra.Command{
	Use:        "reconcile [intent commit]",
	Short:      "(deprecated) alias for `mainline pin`",
	Args:       cobra.MaximumNArgs(2),
	Hidden:     true,
	Deprecated: "use `mainline pin` instead — see docs_for_ai/mainline-spec-v0.1-rc4-patch.md §Patch 7",
	Run:        runPinOrReconcile,
}

func runPinOrReconcile(cmd *cobra.Command, args []string) {
	svc, err := getService()
	if err != nil {
		outputError(err)
		return
	}

	if len(args) == 2 {
		pinned, err := svc.PinExplicit(args[0], args[1])
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(pinned)
		} else {
			fmt.Printf("Pinned %s -> %s (explicit)\n", pinned.IntentID, pinned.Commit)
		}
		return
	}
	if len(args) == 1 {
		outputError(fmt.Errorf("explicit pin takes two args: <intent> <commit>"))
		return
	}

	result, err := svc.Pin()
	if err != nil {
		outputError(err)
		return
	}

	if jsonOutput {
		outputJSON(result)
	} else {
		if result.Pinned == 0 {
			fmt.Println("Nothing to pin")
		} else {
			fmt.Printf("Pinned %d intent(s)\n", result.Pinned)
			for _, l := range result.Links {
				fmt.Printf("  %s -> %s (%s)\n", l.IntentID, l.Commit, l.MatchStrategy)
			}
		}
	}
}

func init() {
	mergeCmd.Flags().StringVar(&mergeIntentID, "intent", "", "intent ID to merge")
}
