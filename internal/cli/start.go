package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/engine"
)

var startGoal string
var startThread string
var startCommits []string
var startRange string

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a new intent on the current branch",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		if startGoal == "" && len(args) > 0 {
			startGoal = args[0]
		}
		if startGoal == "" {
			outputError(fmt.Errorf("--goal is required"))
			return
		}

		// v0.3 backfill: --commits is the primitive, --range is sugar
		// that expands to a commit list via `git rev-list`. The two
		// flags are mutually exclusive — keeping callers honest about
		// which they intend.
		var backfill []string
		if len(startCommits) > 0 && startRange != "" {
			outputError(fmt.Errorf("--commits and --range are mutually exclusive"))
			return
		}
		for _, c := range startCommits {
			for _, p := range strings.Split(c, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					backfill = append(backfill, p)
				}
			}
		}
		if startRange != "" {
			out, err := svc.Git.Run("rev-list", "--reverse", startRange)
			if err != nil {
				outputError(fmt.Errorf("expand --range %s: %w", startRange, err))
				return
			}
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				if line != "" {
					backfill = append(backfill, strings.TrimSpace(line))
				}
			}
		}

		var result *engine.StartResult
		if len(backfill) > 0 {
			result, err = svc.StartWithOptions(startGoal, startThread, &engine.StartOptions{
				BackfillCommits: backfill,
			})
		} else {
			result, err = svc.Start(startGoal, startThread)
		}
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			fmt.Printf("Intent started: %s\n", result.IntentID)
			fmt.Printf("  Thread:  %s\n", result.Thread)
			fmt.Printf("  Branch:  %s\n", result.GitBranch)
			fmt.Printf("  Goal:    %s\n", result.Goal)
			if len(result.BackfillCommits) > 0 {
				fmt.Printf("  Backfill commits (%d):\n", len(result.BackfillCommits))
				for _, c := range result.BackfillCommits {
					fmt.Printf("    %s\n", shortHash(c))
				}
			}
			// First-touch breadcrumb: a brand-new user just claimed
			// work; the next steps are non-obvious without reading
			// AGENTS.md. Three lines is enough to drive the loop
			// without quoting the spec.
			fmt.Println()
			fmt.Println("Next:")
			fmt.Println("  1. Edit code; run `mainline append \"<what changed>\"` after each meaningful turn.")
			fmt.Println("  2. `git add … && git commit -m \"…\"` — Mainline does not commit for you.")
			fmt.Println("  3. `mainline seal --prepare > seal.json` → fill the template → `mainline seal --submit < seal.json`.")
		}
	},
}

func init() {
	startCmd.Flags().StringVar(&startGoal, "goal", "", "goal description for the intent")
	startCmd.Flags().StringVar(&startThread, "thread", "", "thread name (default: current branch)")
	startCmd.Flags().StringSliceVar(&startCommits, "commits", nil, "v0.3 backfill: comma-separated commit SHAs this intent claims to cover")
	startCmd.Flags().StringVar(&startRange, "range", "", "v0.3 backfill: commit range base..head (sugar for --commits expanding via git rev-list)")
}
