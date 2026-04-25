package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current mainline status",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		result, err := svc.Status()
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			if !result.Initialized {
				fmt.Println("Mainline not initialized. Run 'mainline init'.")
				return
			}
			fmt.Printf("Branch:    %s\n", result.Branch)
			fmt.Printf("Actor:     %s\n", result.ActorID)
			if result.ActiveIntent != nil {
				fmt.Printf("Intent:    %s (%s)\n", result.ActiveIntent.IntentID, result.ActiveIntent.Status)
				fmt.Printf("  Goal:    %s\n", result.ActiveIntent.Goal)
				fmt.Printf("  Turns:   %d\n", result.TurnCount)
			} else {
				fmt.Println("Intent:    (none active)")
			}
			fmt.Printf("Proposed:  %d intents\n", result.ProposedCount)
		}
	},
}
