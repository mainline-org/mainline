package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

var sealPrepare bool
var sealSubmit bool
var sealIntentID string

var sealCmd = &cobra.Command{
	Use:   "seal",
	Short: "Seal an intent (freeze code + generate summary)",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		if sealPrepare {
			pkg, err := svc.SealPrepare(sealIntentID)
			if err != nil {
				outputError(err)
				return
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(pkg)
			return
		}

		if sealSubmit {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				outputError(fmt.Errorf("read stdin: %w", err))
				return
			}
			result, err := svc.SealSubmit(json.RawMessage(data))
			if err != nil {
				outputError(err)
				return
			}
			if jsonOutput {
				outputJSON(result)
			} else {
				fmt.Printf("Intent sealed: %s\n", result.IntentID)
				fmt.Printf("  Status:     %s\n", result.Status)
				fmt.Printf("  Published:  %v\n", result.Published)
				fmt.Printf("  Code commit: %s\n", result.CodeCommit)
				fmt.Printf("  Event ID:   %s\n", result.EventID)
				fmt.Printf("  Hash:       %s\n", result.Hash)
				if result.Warning != "" {
					fmt.Printf("  Warning:    %s\n", result.Warning)
				}
			}
			return
		}

		// Default: show help
		cmd.Help()
	},
}

func init() {
	sealCmd.Flags().BoolVar(&sealPrepare, "prepare", false, "output seal prepare package (JSON)")
	sealCmd.Flags().BoolVar(&sealSubmit, "submit", false, "submit seal result from stdin (JSON)")
	sealCmd.Flags().StringVar(&sealIntentID, "intent", "", "intent ID (default: active intent on current branch)")
}
