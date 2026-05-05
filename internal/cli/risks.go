package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// `mainline risks` — add, list, and resolve explicit risks.

var (
	risksFileFilter string
	risksShowAll    bool
)

var risksCmd = &cobra.Command{
	Use:   "risks",
	Short: "Manage explicit risks",
	Long: `List risks with lifecycle status.

By default shows only open risks. Use --all to include resolved and expired.
Risks are created with "mainline risks add".

Risk lifecycle:
  open      — no resolution event; source intent is active
  resolved  — explicitly resolved by a later intent or manual action
  expired   — source intent was superseded, abandoned, or reverted

Risk IDs are "risk_<hex>".`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		risks, err := svc.ListRisks(risksFileFilter, risksShowAll)
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(risks)
			return
		}
		if len(risks) == 0 {
			if risksShowAll {
				fmt.Println("No risks in the catalog.")
			} else {
				fmt.Println("No open risks. Use --all to include resolved/expired.")
			}
			return
		}
		for _, r := range risks {
			marker := "●"
			switch r.Status {
			case "resolved":
				marker = "✓"
			case "expired":
				marker = "○"
			}
			fmt.Printf("  %s [%s] %s\n", marker, r.Status, r.ID)
			text := truncate(r.Text, 100)
			fmt.Printf("    %s\n", text)
			fmt.Printf("    source: %s", r.SourceIntent)
			if r.OpenedAt != "" {
				fmt.Printf("  sealed: %s", r.OpenedAt[:10])
			}
			if len(r.ResolvedBy) > 0 {
				var resolvers []string
				for _, rr := range r.ResolvedBy {
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
		// Summary
		var openCount, resolvedCount, expiredCount int
		for _, r := range risks {
			switch r.Status {
			case "open":
				openCount++
			case "resolved":
				resolvedCount++
			case "expired":
				expiredCount++
			}
		}
		fmt.Printf("%d risks", len(risks))
		if risksShowAll {
			fmt.Printf(" (%d open, %d resolved, %d expired)", openCount, resolvedCount, expiredCount)
		}
		fmt.Println()
	},
}

var (
	resolveByIntent  string
	resolveRationale string
)

var risksResolveCmd = &cobra.Command{
	Use:   "resolve <risk_id>",
	Short: "Manually resolve a risk",
	Long: `Mark a risk as resolved. Risk IDs are "risk_<hex>".

Use --by-intent to record which intent's work resolved it.
Use --rationale to explain how the risk was addressed.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		riskID := args[0]
		if err := svc.ResolveRisk(riskID, resolveByIntent, resolveRationale); err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(map[string]interface{}{
				"ok":      true,
				"risk_id": riskID,
				"status":  "resolved",
			})
			return
		}
		fmt.Printf("✓ Risk %s resolved\n", riskID)
	},
}

func init() {
	risksCmd.Flags().StringVar(&risksFileFilter, "file", "", "filter risks by file path prefix")
	risksCmd.Flags().BoolVar(&risksShowAll, "all", false, "include resolved and expired explicit risks")

	risksResolveCmd.Flags().StringVar(&resolveByIntent, "by-intent", "", "intent ID whose work resolved this risk")
	risksResolveCmd.Flags().StringVar(&resolveRationale, "rationale", "", "explanation of how the risk was addressed")

	risksCmd.AddCommand(risksResolveCmd)
}
