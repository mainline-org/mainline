package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	followupsFileFilter string
	followupsShowAll    bool
)

var followupsCmd = &cobra.Command{
	Use:   "followups",
	Short: "List open follow-ups across sealed intents",
	Long: `List follow-ups from the sealed intent catalog with lifecycle status.

By default shows only open follow-ups. Use --all to include resolved and expired.

Follow-up lifecycle:
  open      - no resolution event; source intent is active
  resolved  - explicitly completed by a later intent or manual action
  expired   - source intent was superseded, abandoned, or reverted

Follow-up IDs are deterministic: "{intent_id}#{array_index}".`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		followups, err := svc.ListFollowups(followupsFileFilter, followupsShowAll)
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(followups)
			return
		}
		if len(followups) == 0 {
			if followupsShowAll {
				fmt.Println("No follow-ups in the catalog.")
			} else {
				fmt.Println("No open follow-ups. Use --all to include resolved/expired.")
			}
			return
		}
		for _, f := range followups {
			marker := "*"
			switch f.Status {
			case "resolved":
				marker = "x"
			case "expired":
				marker = "o"
			}
			fmt.Printf("  %s [%s] %s\n", marker, f.Status, f.ID)
			text := truncateWithSuffix(f.Text, 100, "...")
			fmt.Printf("    %s\n", text)
			fmt.Printf("    source: %s", f.SourceIntent)
			if f.OpenedAt != "" {
				fmt.Printf("  sealed: %s", f.OpenedAt[:10])
			}
			if len(f.ResolvedBy) > 0 {
				var resolvers []string
				for _, rr := range f.ResolvedBy {
					if rr.IntentID != "" {
						resolvers = append(resolvers, rr.IntentID)
					} else {
						resolvers = append(resolvers, "manual")
					}
				}
				fmt.Printf("  resolved-by: %s", strings.Join(resolvers, ", "))
			}
			fmt.Println()
			fmt.Println()
		}
		var openCount, resolvedCount, expiredCount int
		for _, f := range followups {
			switch f.Status {
			case "open":
				openCount++
			case "resolved":
				resolvedCount++
			case "expired":
				expiredCount++
			}
		}
		fmt.Printf("%d follow-ups", len(followups))
		if followupsShowAll {
			fmt.Printf(" (%d open, %d resolved, %d expired)", openCount, resolvedCount, expiredCount)
		}
		fmt.Println()
	},
}

var (
	followupResolveByIntent  string
	followupResolveRationale string
)

var followupsResolveCmd = &cobra.Command{
	Use:   "resolve <followup_id>",
	Short: "Mark a follow-up as completed",
	Long: `Mark a follow-up as completed. The follow-up ID has the format "int_<hex>#<index>".

Use --by-intent to record which intent's work completed it.
Use --rationale to explain how the follow-up was addressed.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		followupID := args[0]
		if err := svc.ResolveFollowup(followupID, followupResolveByIntent, followupResolveRationale); err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(map[string]interface{}{
				"ok":          true,
				"followup_id": followupID,
				"status":      "resolved",
			})
			return
		}
		fmt.Printf("Follow-up %s resolved\n", followupID)
	},
}

func init() {
	followupsCmd.Flags().StringVar(&followupsFileFilter, "file", "", "filter follow-ups by file path prefix")
	followupsCmd.Flags().BoolVar(&followupsShowAll, "all", false, "include resolved and expired follow-ups")

	followupsResolveCmd.Flags().StringVar(&followupResolveByIntent, "by-intent", "", "intent ID whose work completed this follow-up")
	followupsResolveCmd.Flags().StringVar(&followupResolveRationale, "rationale", "", "explanation of how the follow-up was addressed")

	followupsCmd.AddCommand(followupsResolveCmd)
}
