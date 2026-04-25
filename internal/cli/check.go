package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

var checkPrepare bool
var checkSubmit bool
var checkIntentID string

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run semantic conflict check",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		if checkPrepare {
			if checkIntentID == "" {
				outputError(fmt.Errorf("--intent is required for check --prepare"))
				return
			}
			pkg, err := svc.CheckPrepare(checkIntentID)
			if err != nil {
				outputError(err)
				return
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(pkg)
			return
		}

		if checkSubmit {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				outputError(fmt.Errorf("read stdin: %w", err))
				return
			}
			result, err := svc.CheckSubmit(json.RawMessage(data))
			if err != nil {
				outputError(err)
				return
			}
			if jsonOutput {
				outputJSON(result)
			} else {
				fmt.Printf("Check result for: %s\n", result.CandidateIntent)
				if result.HasConflict {
					fmt.Printf("  CONFLICT: severity=%s\n", result.HighestSeverity)
				} else {
					fmt.Println("  No conflicts detected")
				}
				fmt.Printf("  Judgments: %d\n", result.JudgmentCount)
				if result.NeedsHumanReview {
					fmt.Println("  ⚠ Needs human review")
				}
			}
			return
		}

		cmd.Help()
	},
}

func init() {
	checkCmd.Flags().BoolVar(&checkPrepare, "prepare", false, "output check prepare package (JSON)")
	checkCmd.Flags().BoolVar(&checkSubmit, "submit", false, "submit check judgment from stdin (JSON)")
	checkCmd.Flags().StringVar(&checkIntentID, "intent", "", "intent ID to check")
}
