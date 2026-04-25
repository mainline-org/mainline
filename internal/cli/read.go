package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var logLimit int

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "Show intent history",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		result, err := svc.Log(logLimit)
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			if len(result.Intents) == 0 {
				fmt.Println("No intents recorded yet.")
				return
			}
			for _, entry := range result.Intents {
				status := string(entry.Status)
				title := entry.Goal
				if entry.Title != "" {
					title = entry.Title
				}
				fmt.Printf("%-12s [%-12s] %s", entry.IntentID, status, title)
				if entry.Thread != "" {
					fmt.Printf("  (%s)", entry.Thread)
				}
				fmt.Println()
			}
		}
	},
}

var showCmd = &cobra.Command{
	Use:   "show [intent-id]",
	Short: "Show details of an intent",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		result, err := svc.Show(args[0])
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			if result.Intent != nil {
				d := result.Intent
				fmt.Printf("Intent:  %s\n", d.IntentID)
				fmt.Printf("Status:  %s\n", d.Status)
				fmt.Printf("Goal:    %s\n", d.Goal)
				fmt.Printf("Thread:  %s\n", d.Thread)
				fmt.Printf("Branch:  %s\n", d.GitBranch)
				fmt.Printf("Base:    %s\n", d.BaseCommit)
				fmt.Printf("Created: %s\n", d.CreatedAt)
				if len(result.Turns) > 0 {
					fmt.Printf("Turns:   %d\n", len(result.Turns))
					for _, t := range result.Turns {
						fmt.Printf("  [%d] %s - %s\n", t.Index, t.ID, t.Description)
					}
				}
			} else if result.View != nil {
				v := result.View
				fmt.Printf("Intent:  %s\n", v.IntentID)
				fmt.Printf("Status:  %s\n", v.Status)
				fmt.Printf("Goal:    %s\n", v.Goal)
				fmt.Printf("Actor:   %s\n", v.ActorID)
				if v.Summary != nil {
					fmt.Printf("Title:   %s\n", v.Summary.Title)
					fmt.Printf("What:    %s\n", v.Summary.What)
					fmt.Printf("Why:     %s\n", v.Summary.Why)
				}
			}
		}
	},
}

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Show full context for agent consumption",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		result, err := svc.Context()
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			fmt.Printf("Repo:       %s\n", result.RepoRoot)
			fmt.Printf("Branch:     %s\n", result.Branch)
			fmt.Printf("Main:       %s\n", result.MainBranch)
			fmt.Printf("Actor:      %s\n", result.ActorID)
			if result.ActiveIntent != nil {
				fmt.Printf("Active:     %s (%s) - %s\n",
					result.ActiveIntent.IntentID, result.ActiveIntent.Status, result.ActiveIntent.Goal)
			}
			if len(result.ProposedIntents) > 0 {
				fmt.Printf("Proposed:   %d intent(s)\n", len(result.ProposedIntents))
			}
			if len(result.MergedRecent) > 0 {
				fmt.Printf("Merged:     %d intent(s)\n", len(result.MergedRecent))
			}
		}
	},
}

var listProposalsCmd = &cobra.Command{
	Use:   "list-proposals",
	Short: "List all proposed (not yet merged) intents",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		result, err := svc.ListProposals()
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			if len(result.Proposals) == 0 {
				fmt.Println("No proposed intents.")
				return
			}
			for _, p := range result.Proposals {
				title := p.Goal
				if p.Title != "" {
					title = p.Title
				}
				fmt.Printf("%-12s %s\n", p.IntentID, title)
			}
		}
	},
}

var canonicalHashCmd = &cobra.Command{
	Use:   "canonical-hash [intent-id]",
	Short: "Compute canonical hash of an intent",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		hash, err := svc.CanonicalHashIntent(args[0])
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.Encode(map[string]string{"hash": hash, "intent_id": args[0]})
		} else {
			fmt.Println(hash)
		}
	},
}

func init() {
	logCmd.Flags().IntVar(&logLimit, "limit", 0, "max intents to show (default: from config)")
}
