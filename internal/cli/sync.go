package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Fetch remote state and rebuild local views",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		result, err := svc.Sync()
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			if result.Fetched {
				fmt.Println("Fetched from origin")
			}
			fmt.Printf("View rebuilt: %d intents, %d proposed\n", result.IntentsInView, result.ProposedCount)
		}
	},
}

var publishIntentID string

var publishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Push actor log to remote",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		result, err := svc.Publish(publishIntentID)
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			fmt.Printf("Published intent %s\n", result.IntentID)
			fmt.Printf("  Ref:    %s\n", result.Ref)
			if result.Pushed {
				fmt.Println("  Pushed to origin")
			} else {
				fmt.Println("  (no remote; local only)")
			}
		}
	},
}

func init() {
	publishCmd.Flags().StringVar(&publishIntentID, "intent", "", "intent ID to publish")
}
