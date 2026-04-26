package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"mainline/internal/engine"
)

var doctorFix bool
var doctorStaleAfter time.Duration

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Inspect and repair local mainline state",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		result, err := svc.Doctor(engine.DoctorOptions{
			Fix:        doctorFix,
			StaleAfter: doctorStaleAfter,
		})
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
			return
		}

		fmt.Printf("Checked local drafts: %d\n", result.CheckedDrafts)
		if len(result.OrphanDrafts) == 0 && len(result.StaleDrafts) == 0 {
			fmt.Println("No local draft issues found.")
			return
		}

		if len(result.OrphanDrafts) > 0 {
			fmt.Printf("Orphan drafts: %d\n", len(result.OrphanDrafts))
			for _, d := range result.OrphanDrafts {
				fmt.Printf("  %s [%s] %s (%s)\n", d.IntentID, d.Status, d.Goal, d.Reason)
			}
			if !doctorFix {
				fmt.Println("Run 'mainline doctor --fix' to delete orphan draft files.")
			}
		}

		if len(result.DeletedDrafts) > 0 {
			fmt.Printf("Deleted orphan drafts: %d\n", len(result.DeletedDrafts))
			for _, id := range result.DeletedDrafts {
				fmt.Printf("  %s\n", id)
			}
		}

		if len(result.StaleDrafts) > 0 {
			fmt.Printf("Stale drafts: %d\n", len(result.StaleDrafts))
			for _, d := range result.StaleDrafts {
				fmt.Printf("  %s [%s] %s (%s)\n", d.IntentID, d.Status, d.Goal, d.Reason)
			}
		}
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorFix, "fix", false, "delete orphan local draft files")
	doctorCmd.Flags().DurationVar(&doctorStaleAfter, "stale-after", 24*time.Hour, "mark drafting intents stale after this duration")
}
