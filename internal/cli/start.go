package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var startGoal string
var startThread string

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a new intent on the current branch",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		if startGoal == "" && len(args) > 0 {
			startGoal = args[0]
		}
		if startGoal == "" {
			outputError(fmt.Errorf("--goal is required"))
			return
		}

		result, err := svc.Start(startGoal, startThread)
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			fmt.Printf("Intent started: %s\n", result.IntentID)
			fmt.Printf("  Thread:  %s\n", result.Thread)
			fmt.Printf("  Branch:  %s\n", result.GitBranch)
			fmt.Printf("  Goal:    %s\n", result.Goal)
		}
	},
}

func init() {
	startCmd.Flags().StringVar(&startGoal, "goal", "", "goal description for the intent")
	startCmd.Flags().StringVar(&startThread, "thread", "", "thread name (default: current branch)")
}
