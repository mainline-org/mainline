package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var mergeIntentID string

var mergeCmd = &cobra.Command{
	Use:   "merge",
	Short: "Merge a sealed intent into main branch",
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

var reconcileCmd = &cobra.Command{
	Use:   "reconcile [intent commit]",
	Short: "Acknowledge merged intents (auto-match, or manual link with two args)",
	Long: `Without arguments: scan every proposed intent and try to associate it with
a main-branch commit using a strategy cascade (tree_hash → commit_hash →
goal_text). For each match, write a reconcile_auto note and push.

With two arguments (mainline reconcile <intent> <commit>): pin the named
intent to the named commit unconditionally. The note's via is set to
reconcile_manual and added_by records the calling actor.`,
	Args: cobra.MaximumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		if len(args) == 2 {
			link, err := svc.ReconcileManual(args[0], args[1])
			if err != nil {
				outputError(err)
				return
			}
			if jsonOutput {
				outputJSON(link)
			} else {
				fmt.Printf("Reconciled %s -> %s (manual)\n", link.IntentID, link.Commit)
			}
			return
		}
		if len(args) == 1 {
			outputError(fmt.Errorf("manual reconcile takes two args: <intent> <commit>"))
			return
		}

		result, err := svc.Reconcile()
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			if result.Reconciled == 0 {
				fmt.Println("Nothing to reconcile")
			} else {
				fmt.Printf("Reconciled %d intent(s)\n", result.Reconciled)
				for _, link := range result.Links {
					fmt.Printf("  %s -> %s (%s)\n", link.IntentID, link.Commit, link.MatchStrategy)
				}
			}
		}
	},
}

func init() {
	mergeCmd.Flags().StringVar(&mergeIntentID, "intent", "", "intent ID to merge")
}
