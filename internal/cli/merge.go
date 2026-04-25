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
	Use:   "reconcile",
	Short: "Acknowledge merged intents",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
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
				for _, id := range result.IntentIDs {
					fmt.Printf("  %s\n", id)
				}
			}
		}
	},
}

func init() {
	mergeCmd.Flags().StringVar(&mergeIntentID, "intent", "", "intent ID to merge")
}
