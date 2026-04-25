package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var initActorName string

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize mainline in current repository",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		result, err := svc.Init(initActorName)
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			fmt.Printf("Mainline initialized in %s\n", result.RepoRoot)
			fmt.Printf("  Actor ID:    %s\n", result.ActorID)
			fmt.Printf("  Actor name:  %s\n", result.ActorName)
			fmt.Printf("  Main branch: %s\n", result.MainBranch)
		}
	},
}

func init() {
	initCmd.Flags().StringVar(&initActorName, "actor-name", "", "name for this actor identity")
}
