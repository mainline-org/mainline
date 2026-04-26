package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var abandonReason string

var abandonCmd = &cobra.Command{
	Use:   "abandon <intent-id>",
	Short: "Abandon an intent (drop drafts; write abandon event for sealed/proposed)",
	Long: `Abandon an intent so it stops appearing in the proposed/active set.

Behaviour depends on the intent's current state:

  drafting       The draft is local-only — files are deleted; no event
                 is written (nothing was ever published).

  sealed_local   An IntentAbandonedEvent is appended to the actor log
  proposed       and auto-published to the team. Other actors see the
                 abandon on their next sync.

Already-merged intents cannot be abandoned (revert the code change
instead — main is the source of truth once a commit lands).`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		result, err := svc.Abandon(args[0], abandonReason)
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(result)
			return
		}
		if result.PriorStatus == "drafting" {
			fmt.Printf("Draft %s deleted.\n", result.IntentID)
		} else {
			fmt.Printf("Intent abandoned: %s\n", result.IntentID)
			fmt.Printf("  Prior status: %s\n", result.PriorStatus)
			fmt.Printf("  Event ID:     %s\n", result.EventID)
			fmt.Printf("  Published:    %v\n", result.Published)
		}
		if result.Reason != "" {
			fmt.Printf("  Reason:       %s\n", result.Reason)
		}
		if result.Warning != "" {
			fmt.Printf("  Warning:      %s\n", result.Warning)
		}
	},
}

func init() {
	abandonCmd.Flags().StringVar(&abandonReason, "reason", "", "free-text reason recorded on the abandon event (recommended)")
}
