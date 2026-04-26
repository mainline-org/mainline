package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var gapsCmd = &cobra.Command{
	Use:   "gaps",
	Short: "Show uncovered commits on main and how to rescue them",
	Long: `List commits on main that are not covered by any sealed intent and
not marked as skipped. For each uncovered commit, shows three rescue
paths ordered by reversibility (reset > backfill > skip).`,
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		result, err := svc.Gaps()
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(result)
			return
		}
		if len(result.Uncovered) == 0 {
			fmt.Printf("No uncovered commits in the last %d on main.\n", result.WindowSize)
			fmt.Printf("  Covered: %d   Skipped: %d\n", result.Covered, len(result.Skipped))
			return
		}
		fmt.Printf("Uncovered commits in the last %d on main (oldest first):\n\n",
			result.WindowSize)
		// Reverse so the oldest is shown first — easier to walk through
		// chronologically when the user is fixing them.
		for i := len(result.Uncovered) - 1; i >= 0; i-- {
			u := result.Uncovered[i]
			fmt.Printf("  %s  %s\n", shortHash(u.Commit), u.Subject)
			fmt.Printf("           by %s  at %s\n", u.Author, u.CommittedAt)
			fmt.Println("           Suggested actions (best first):")
			for _, sug := range u.Suggestions {
				fmt.Printf("             %s — %s\n", sug.Action, sug.Applicable)
				fmt.Printf("                 %s\n", sug.Command)
			}
			fmt.Println()
		}
		fmt.Printf("Summary: %d covered, %d skipped, %d uncovered.\n",
			result.Covered, len(result.Skipped), len(result.Uncovered))
	},
}

