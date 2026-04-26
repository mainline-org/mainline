package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current mainline status",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		result, err := svc.Status()
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			if !result.Initialized {
				fmt.Println("Mainline not initialized. Run 'mainline init'.")
				return
			}
			if !result.IdentityConfigured {
				fmt.Println("⚠ This clone has no Mainline actor identity.")
				fmt.Println("  Run `mainline init --actor-name <your name>` before starting work.")
				fmt.Println()
			}
			fmt.Printf("Branch:    %s\n", result.Branch)
			actorDisplay := result.ActorID
			if actorDisplay == "" {
				actorDisplay = "(missing — run `mainline init --actor-name <name>`)"
			}
			fmt.Printf("Actor:     %s\n", actorDisplay)
			if result.LocalHead != "" {
				fmt.Printf("Local:     %s\n", shortHash(result.LocalHead))
			}
			if result.MainHead != "" {
				fmt.Printf("Synced:    %s\n", shortHash(result.MainHead))
			}
			if result.ActiveIntent != nil {
				fmt.Printf("Intent:    %s (%s)\n", result.ActiveIntent.IntentID, result.ActiveIntent.Status)
				fmt.Printf("  Goal:    %s\n", result.ActiveIntent.Goal)
				fmt.Printf("  Turns:   %d\n", result.TurnCount)
			} else {
				fmt.Println("Intent:    (none active)")
			}
			fmt.Printf("Proposed:  %d intents\n", result.ProposedCount)
			if result.LastSync == nil {
				fmt.Println("Sync:      never synced — run 'mainline sync' to see team activity")
			} else {
				marker := ""
				if result.SyncStale {
					marker = " (stale)"
				}
				fmt.Printf("Sync:      %s ago%s\n",
					formatElapsed(result.SyncStaleSeconds), marker)
			}

			if result.Coverage != nil {
				c := result.Coverage
				fmt.Printf("\nCoverage (last %d commits on main):\n", c.WindowSize)
				fmt.Printf("  ✓ Covered:    %d\n", c.CoveredCount)
				if c.SkippedCount > 0 {
					fmt.Printf("  ⏭ Skipped:    %d\n", c.SkippedCount)
				}
				if c.UncoveredCount > 0 {
					fmt.Printf("  ⚠ Uncovered:  %d\n", c.UncoveredCount)
					for _, uc := range c.Uncovered {
						fmt.Printf("    %s  %s\n", shortHash(uc.Commit), truncate(uc.Subject, 60))
					}
					fmt.Println("\n  Run `mainline gaps` for details + rescue options.")
				}
			}
		}
	},
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func shortHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

// formatElapsed renders an int64 second count as a short human string
// like "12m" / "3h" / "2d". Granularity matches what's useful in a
// status one-liner; not a general-purpose duration formatter.
func formatElapsed(seconds int64) string {
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh", seconds/3600)
	default:
		return fmt.Sprintf("%dd", seconds/86400)
	}
}
