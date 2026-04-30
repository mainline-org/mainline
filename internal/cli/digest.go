package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/hub"
)

// `mainline digest` is the CLI surface over hub.BuildDigest. Same
// logic as the dashboard's "This week" card, but parameterizable
// window (`--since 7d` / `14d` / `30d` / `Nh` for hours). Reads
// from the local synced view; auto-syncs upfront via the
// autoSyncCommands map so the window data is fresh.

var (
	digestSince  string
	digestFormat string
)

var digestCmd = &cobra.Command{
	Use:   "digest",
	Short: "Show a rolling digest of recent intent activity",
	Long: `Roll up the recent intent ledger into a human-readable digest:
sealed / proposed / abandoned / superseded counts, hot files, important
decisions, risks to watch, abandoned approaches.

Reads only the local synced view — same data the Hub dashboard renders.
Window defaults to 7 days; pass --since 14d / 30d / 48h to widen.`,
	Run: func(cmd *cobra.Command, args []string) {
		days, err := parseSinceDays(digestSince)
		if err != nil {
			outputError(err)
			return
		}
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		view, err := svc.Store.ReadMainlineView()
		if err != nil {
			outputError(err)
			return
		}
		if view == nil {
			outputError(fmt.Errorf("no synced view; run `mainline sync`"))
			return
		}
		intents := make([]hub.HubIntent, 0, len(view.Intents))
		for i := range view.Intents {
			intents = append(intents, hub.HubIntentFromView(&view.Intents[i]))
		}
		dig := hub.BuildDigest(intents, days, time.Now())
		if jsonOutput {
			outputJSON(map[string]any{
				"window_days": dig.WindowDays,
				"digest":      dig,
			})
			return
		}
		printDigest(dig)
	},
}

// parseSinceDays accepts "7d" / "30d" / "48h" / a bare number of days
// ("14"). Empty defaults to 7. Negative values are rejected.
func parseSinceDays(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 7, nil
	}
	switch {
	case strings.HasSuffix(s, "d"):
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid --since %q (want e.g. 7d, 14d, 30d, 48h)", s)
		}
		return n, nil
	case strings.HasSuffix(s, "h"):
		n, err := strconv.Atoi(strings.TrimSuffix(s, "h"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid --since %q (want e.g. 7d, 14d, 30d, 48h)", s)
		}
		days := n / 24
		if n%24 != 0 || days == 0 {
			days = 1
		}
		return days, nil
	default:
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid --since %q (want e.g. 7d, 14d, 30d, 48h)", s)
		}
		return n, nil
	}
}

func printDigest(d hub.HubWeeklyDigest) {
	fmt.Printf("Digest — last %d day", d.WindowDays)
	if d.WindowDays != 1 {
		fmt.Print("s")
	}
	fmt.Println()
	fmt.Printf("  sealed:        %d\n", d.SealedThisWindow)
	fmt.Printf("  proposed:      %d\n", d.ProposedThisWindow)
	fmt.Printf("  abandoned:     %d\n", d.AbandonedThisWindow)
	fmt.Printf("  superseded:    %d\n", d.SupersededThisWindow)
	fmt.Printf("  risk-bearing:  %d\n", d.RiskBearingThisWindow)
	if len(d.ImportantDecisions) > 0 {
		fmt.Println("\nImportant decisions:")
		for _, x := range d.ImportantDecisions {
			fmt.Printf("  • %s — %s\n    %s\n", x.ID, x.Title, x.Reason)
		}
	}
	if len(d.RisksToWatch) > 0 {
		fmt.Println("\nRisks to watch:")
		for _, x := range d.RisksToWatch {
			fmt.Printf("  • %s — %s\n    %s\n", x.ID, x.Title, x.Reason)
		}
	}
	if len(d.AbandonedApproaches) > 0 {
		fmt.Println("\nAbandoned approaches:")
		for _, x := range d.AbandonedApproaches {
			fmt.Printf("  • %s — %s\n", x.ID, x.Title)
		}
	}
	if len(d.HotFilesThisWindow) > 0 {
		fmt.Println("\nFiles heating up:")
		for _, f := range d.HotFilesThisWindow {
			fmt.Printf("  • %s — %d intents\n", f.Path, f.IntentCount)
		}
	}
}

func init() {
	digestCmd.Flags().StringVar(&digestSince, "since", "7d",
		"window for the digest (e.g. 7d, 14d, 30d, 48h)")
	digestCmd.Flags().StringVar(&digestFormat, "format", "human",
		"output format: human | json (deprecated alias for --json)")
}
