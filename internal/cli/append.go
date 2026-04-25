package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var appendGoal string

var appendCmd = &cobra.Command{
	Use:   "append [description]",
	Short: "Append a turn to the active intent",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		description := args[0]

		// If --goal is provided and no active intent, auto-create one
		if appendGoal != "" {
			result, err := svc.AppendWithAutoStart(description, appendGoal)
			if err != nil {
				outputError(err)
				return
			}
			if jsonOutput {
				outputJSON(result)
			} else {
				if result.IntentCreated {
					fmt.Printf("Intent auto-created: %s\n", result.IntentID)
				}
				fmt.Printf("Turn appended: %s (index %d)\n", result.TurnID, result.Index)
			}
			return
		}

		result, err := svc.Append(description)
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			fmt.Printf("Turn appended: %s (index %d)\n", result.TurnID, result.Index)
			fmt.Printf("  Intent: %s\n", result.IntentID)
		}
	},
}

func init() {
	appendCmd.Flags().StringVar(&appendGoal, "goal", "", "auto-create intent if none active")
}
