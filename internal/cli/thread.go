package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var threadCmd = &cobra.Command{
	Use:   "thread",
	Short: "Manage threads",
}

var threadNewCmd = &cobra.Command{
	Use:   "new [name]",
	Short: "Create a new thread",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		result, err := svc.ThreadNew(args[0])
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			fmt.Printf("Thread created: %s\n", result.Name)
			fmt.Printf("  Git branch: %s\n", result.GitBranch)
		}
	},
}

var threadListCmd = &cobra.Command{
	Use:   "list",
	Short: "List threads",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		threads, err := svc.ThreadList()
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(threads)
		} else {
			if len(threads) == 0 {
				fmt.Println("No threads.")
				return
			}
			for _, t := range threads {
				fmt.Printf("%-20s [%-8s] branch=%s intents=%d\n",
					t.Name, t.Status, t.GitBranch, len(t.Intents))
			}
		}
	},
}

var threadCloseCmd = &cobra.Command{
	Use:   "close [name]",
	Short: "Close a thread",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		if err := svc.ThreadClose(args[0]); err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(map[string]string{"status": "closed", "name": args[0]})
		} else {
			fmt.Printf("Thread %s closed\n", args[0])
		}
	},
}

func init() {
	threadCmd.AddCommand(threadNewCmd)
	threadCmd.AddCommand(threadListCmd)
	threadCmd.AddCommand(threadCloseCmd)
}
