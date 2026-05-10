package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/engine"
)

var preflightCmd = &cobra.Command{
	Use:   "preflight",
	Short: "Run a low-noise readiness check before work or seal",
	Long: `Run a read-only readiness check for common collaboration risks:
branch drift, stale sync state, notes rewrite drift, proposed intent overlap,
upstream merged overlap, and dirty-only seal evidence. Business risk is
reported through level / ok_to_continue, not through the process exit code.`,
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		result, err := svc.Preflight()
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(result)
			return
		}
		fmt.Printf("Preflight: %s", result.Level)
		if result.OKToContinue {
			fmt.Print(" (ok to continue)")
		} else {
			fmt.Print(" (review before continuing)")
		}
		fmt.Println()
		if line := engine.AgentAuthorityPlainLine(result.AgentAuthority); line != "" {
			fmt.Println(line)
			for _, warning := range result.AgentAuthority.Warnings {
				fmt.Printf("  warning: %s\n", warning)
			}
		}
		if result.Facts.Branch != "" {
			fmt.Printf("Branch:    %s\n", result.Facts.Branch)
		}
		if result.Facts.ActiveIntentID != "" {
			fmt.Printf("Intent:    %s\n", result.Facts.ActiveIntentID)
		}
		if len(result.Facts.CurrentFiles) > 0 {
			fmt.Printf("Files:     %s\n", strings.Join(result.Facts.CurrentFiles, ", "))
		}
		if len(result.Findings) > 0 {
			fmt.Println("\nFindings:")
			for _, f := range result.Findings {
				fmt.Printf("  [%s] %s — %s\n", f.Level, f.Code, f.Message)
				if len(f.Files) > 0 {
					fmt.Printf("        files: %s\n", strings.Join(f.Files, ", "))
				}
			}
		}
		if len(result.Overlaps) > 0 {
			fmt.Println("\nOverlaps:")
			for _, o := range result.Overlaps {
				files := ""
				if len(o.MatchedFiles) > 0 {
					files = " (" + strings.Join(o.MatchedFiles, ", ") + ")"
				}
				fmt.Printf("  [%s] %s %s — %s%s\n", o.Level, o.Kind, o.IntentID, truncate(o.Title, 70), files)
			}
		}
		if len(result.RecommendedNext) > 0 {
			fmt.Println("\nRecommended next:")
			for _, next := range result.RecommendedNext {
				fmt.Printf("  %s\n", next)
			}
		}
	},
}
