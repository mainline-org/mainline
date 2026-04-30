package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/domain"
)

var appendGoal string
var appendRefs []string

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

		// Collect references from --ref flags + env auto-discovery
		var refs []domain.Reference
		for _, r := range appendRefs {
			ref := parseRefFlag(r)
			if ref.Kind != "" && (ref.Ref != "" || ref.URL != "") {
				refs = append(refs, ref)
			}
		}
		refs = append(refs, discoverSessionRefs()...)

		// If --goal is provided and no active intent, auto-create one
		if appendGoal != "" {
			result, err := svc.AppendWithAutoStart(description, appendGoal, refs)
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
				if len(refs) > 0 {
					fmt.Printf("  References: %d attached\n", len(refs))
				}
			}
			return
		}

		result, err := svc.AppendWithRefs(description, refs)
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			fmt.Printf("Turn appended: %s (index %d)\n", result.TurnID, result.Index)
			fmt.Printf("  Intent: %s\n", result.IntentID)
			if len(refs) > 0 {
				fmt.Printf("  References: %d attached\n", len(refs))
			}
		}
	},
}

func init() {
	appendCmd.Flags().StringVar(&appendGoal, "goal", "", "auto-create intent if none active")
	appendCmd.Flags().StringArrayVar(&appendRefs, "ref", nil, "attach reference (format: kind:client:ref)")
}
