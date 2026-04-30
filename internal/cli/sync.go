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

		sp := newSpinner("Syncing...")
		svc.ProgressFunc = func(phase string) { sp.update(phase) }
		sp.start()
		result, err := svc.Sync()
		sp.done()

		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			if result.Fetched {
				fmt.Printf("Fetched from %s\n", svc.RemoteName())
			}
			fmt.Printf("View rebuilt: %d intents, %d proposed", result.IntentsInView, result.ProposedCount)
			if result.NewSealedSeen > 0 {
				fmt.Printf(" (+%d new since last sync)", result.NewSealedSeen)
			}
			fmt.Println()
			if len(result.AutoPinned) > 0 {
				fmt.Printf("Auto-pinned %d intent(s):\n", len(result.AutoPinned))
				for _, p := range result.AutoPinned {
					fmt.Printf("  %s -> %s (%s)\n", p.IntentID, p.Commit, p.MatchStrategy)
				}
			}
			if len(result.NewConflicts) == 0 {
				if result.NewSealedSeen > 0 {
					fmt.Println("No new conflicts detected.")
				}
			} else {
				fmt.Printf("\n⚠ %d potential conflict(s) detected:\n\n", len(result.NewConflicts))
				for _, c := range result.NewConflicts {
					fmt.Printf("  %s ↔ %s  score=%.2f confidence=%s\n",
						c.LocalIntent, c.RemoteIntent, c.OverlapScore, c.Confidence)
					fmt.Printf("    %s (local: %s, remote: %s)\n",
						c.Reason, c.LocalSource, c.RemoteStatus)
				}
				fmt.Println()
				fmt.Println("Run `mainline check <local_intent>` for full phase2 analysis.")
			}
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
				fmt.Printf("  Pushed to %s\n", svc.RemoteName())
			} else {
				fmt.Println("  (no remote; local only)")
			}
		}
	},
}

func init() {
	publishCmd.Flags().StringVar(&publishIntentID, "intent", "", "intent ID to publish")
}
