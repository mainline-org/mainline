package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/engine"
)

var logLimit int
var logStatus string
var logSync bool

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "Show intent history",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		result, err := svc.Log(logLimit, logStatus)
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
				author := entry.Author
				if author == "" {
					author = entry.ActorID
				}
				// rc6: terminal-state intents drop the [check:...] segment
				// entirely (checkMarker returns "" for merged / abandoned
				// / superseded / reverted). Pre-merge intents always show
				// a marker in {?, ~, ok, !, human?}.
				checkSegment := ""
				if entry.Check != "" {
					checkSegment = " [check:" + entry.Check + "]"
				}
				fmt.Printf("%-12s [%s]%s %s %s  %s",
					entry.IntentID, status, checkSegment,
					formatLogTime(entry.ActivityAt), author, title)
				if entry.Thread != "" {
					fmt.Printf(" (%s)", entry.Thread)
				}
				fmt.Println()
			}
		}
	},
}

func formatLogTime(value string) string {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return t.Local().Format("2006-01-02 15:04")
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
				if len(v.References) > 0 {
					fmt.Println("References:")
					for _, ref := range v.References {
						label := ref.Label
						if label == "" {
							label = ref.Kind
						}
						detail := ref.Ref
						if detail == "" {
							detail = ref.URL
						}
						if ref.Client != "" {
							fmt.Printf("  - %s · %s\n    %s:%s\n", ref.Kind, label, ref.Client, detail)
						} else {
							fmt.Printf("  - %s · %s\n    %s\n", ref.Kind, label, detail)
						}
					}
				}
				if v.LastCheck != nil {
					lc := v.LastCheck
					verdict := "no_conflict"
					if lc.HasConflict {
						verdict = "conflict (" + lc.HighestSeverity + ")"
					}
					if lc.NeedsHumanReview {
						verdict += " · human review"
					}
					fmt.Printf("Check:   %s · %d judgment(s) by %s at %s\n",
						verdict, lc.JudgmentCount, lc.ByActor, lc.AtTime)
					if len(lc.AgainstIntents) > 0 {
						fmt.Printf("         against: %s\n", strings.Join(lc.AgainstIntents, ", "))
					}
				}
			}
		}
	},
}

var (
	contextCurrent bool
	contextFiles   []string
	contextQuery   string
	contextLimit   int
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Retrieve relevant historical intents before editing code",
	Long: `Intent-first context retrieval for coding agents.

Three modes — pick one:

  --current               retrieve intents relevant to the current
                          repo state (active draft goal + diff vs main)
  --files <path>...       retrieve intents that touched these files
  --query "<text>"        retrieve intents whose decisions / summary /
                          fingerprint match these keywords

Output is compact and ranked. Each intent comes with its top
decisions / fingerprint plus copy-paste follow-ups
(` + "`mainline show`" + ` / ` + "`trace`" + `) for full detail.

Default workflow for agents:

  1. mainline status                       (overall state)
  2. mainline context --current --json     (relevant prior intents)
  3. read decisions and explicit inherited constraints
  4. THEN grep / read code to verify against current implementation
  5. THEN edit

Bare ` + "`mainline context`" + ` (no flags) is the legacy state-dump form
kept for backwards compatibility; new agent guidance points at the
mode flags above.`,
	Run: func(cmd *cobra.Command, args []string) {
		req, err := contextRetrievalRequestFromFlags(contextCurrent, contextFiles, contextQuery, contextLimit, args)
		if err != nil {
			outputError(err)
			return
		}

		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		if req != nil {
			result, err := svc.RetrieveContext(*req)
			if err != nil {
				outputError(err)
				return
			}
			if jsonOutput {
				outputJSON(result)
				return
			}
			printContextRetrievalText(result)
			return
		}

		// Legacy state-dump path (no mode flag).
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
			fmt.Println()
			fmt.Println("Tip: for ranked retrieval before editing, run:")
			fmt.Println("  mainline context --current --json")
		}
	},
}

func contextRetrievalRequestFromFlags(current bool, files []string, query string, limit int, args []string) (*engine.ContextRetrievalRequest, error) {
	mode := ""
	setMode := func(next string) error {
		if mode != "" {
			return domain.NewRecoverableError(
				domain.ErrInvalidInput,
				"context accepts exactly one retrieval mode flag",
				"use exactly one of --current, --files, or --query",
			)
		}
		mode = next
		return nil
	}

	if current {
		if err := setMode("current"); err != nil {
			return nil, err
		}
	}
	if len(files) > 0 {
		if err := setMode("files"); err != nil {
			return nil, err
		}
	}
	if query != "" {
		if err := setMode("query"); err != nil {
			return nil, err
		}
	}

	if mode == "" {
		if len(args) > 0 {
			return nil, domain.NewRecoverableError(
				domain.ErrInvalidInput,
				fmt.Sprintf("unexpected argument %q", args[0]),
				"use --current, --files, or --query for ranked context retrieval",
				"run bare `mainline context` without positional arguments for the legacy state dump",
			)
		}
		return nil, nil
	}

	switch mode {
	case "files":
		// Cobra/pflag consumes the first value after --files. Because
		// `mainline context` has no positional operands of its own,
		// any remaining args in files mode are additional paths, matching
		// the advertised `--files <path>...` contract.
		files = append(append([]string(nil), files...), args...)
	case "current", "query":
		if len(args) > 0 {
			return nil, domain.NewRecoverableError(
				domain.ErrInvalidInput,
				fmt.Sprintf("unexpected argument %q", args[0]),
				"--current and --query do not accept positional path arguments",
				"use --files <path>... when retrieving context by file",
			)
		}
	}

	return &engine.ContextRetrievalRequest{
		Mode:  mode,
		Files: files,
		Query: query,
		Limit: limit,
	}, nil
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
			_ = enc.Encode(map[string]string{"hash": hash, "intent_id": args[0]})
		} else {
			fmt.Println(hash)
		}
	},
}

func init() {
	logCmd.Flags().IntVar(&logLimit, "limit", 0, "max intents to show (default: from config)")
	logCmd.Flags().StringVar(&logStatus, "status", "", "filter by status (drafting, sealed_local, proposed, merged, abandoned, superseded, reverted)")
	logCmd.Flags().BoolVar(&logSync, "sync", false, "sync with team before showing the log")

	contextCmd.Flags().BoolVar(&contextCurrent, "current", false,
		"retrieve intents relevant to the current repo state (active draft + diff vs main)")
	contextCmd.Flags().StringSliceVar(&contextFiles, "files", nil,
		"retrieve intents that touched these files (repeat, comma-separate, or pass additional paths after the first)")
	contextCmd.Flags().StringVar(&contextQuery, "query", "",
		"retrieve intents whose decisions / risks / summary match these keywords")
	contextCmd.Flags().IntVar(&contextLimit, "limit", 0,
		"max intents to return (default 5)")
}

// printContextRetrievalText renders ContextRetrievalResult as flat
// human-readable text. JSON is the agent default; text exists for
// quick interactive inspection.
func printContextRetrievalText(r *engine.ContextRetrievalResult) {
	fmt.Printf("Mode: %s", r.Query.Mode)
	if len(r.Query.Files) > 0 {
		fmt.Printf("  files=%v", r.Query.Files)
	}
	if r.Query.Text != "" {
		fmt.Printf("  text=%q", r.Query.Text)
	}
	fmt.Println()
	fmt.Println()

	if len(r.RelevantIntents) == 0 {
		fmt.Println("No relevant intents found above the relevance threshold.")
		fmt.Println("Run `mainline log` to browse the full intent history.")
	} else {
		fmt.Printf("Relevant intents (%d):\n", len(r.RelevantIntents))
		for _, ri := range r.RelevantIntents {
			fmt.Printf("\n  %s  [%s]  score=%.2f\n", ri.IntentID, ri.Status, ri.Relevance.Score)
			if ri.Title != "" {
				fmt.Printf("    %s\n", ri.Title)
			}
			if ri.Guidance != "" {
				fmt.Printf("    → %s\n", ri.Guidance)
			}
			if len(ri.Relevance.Reasons) > 0 {
				fmt.Printf("    why: %s\n", strings.Join(ri.Relevance.Reasons, "; "))
			}
			if ri.Summary != "" {
				fmt.Printf("    %s\n", ri.Summary)
			}
			if len(ri.AntiPatterns) > 0 {
				fmt.Println("    legacy anti-patterns (historical only):")
				for _, ap := range ri.AntiPatterns {
					sev := ""
					if ap.Severity != "" {
						sev = " [" + ap.Severity + "]"
					}
					fmt.Printf("      ✗ %s%s\n", ap.What, sev)
					if ap.Why != "" {
						fmt.Printf("         why: %s\n", ap.Why)
					}
				}
			}
			if len(ri.Decisions) > 0 {
				fmt.Println("    decisions:")
				for _, d := range ri.Decisions {
					fmt.Printf("      - %s\n", d)
				}
			}
			if len(ri.Risks) > 0 {
				fmt.Println("    risks:")
				for _, k := range ri.Risks {
					fmt.Printf("      - %s\n", k)
				}
			}
			if len(ri.OpenFollowups) > 0 {
				fmt.Println("    open follow-ups:")
				for _, f := range ri.OpenFollowups {
					fmt.Printf("      - %s\n", f)
				}
			}
			if ri.SupersededBy != "" {
				fmt.Printf("    superseded by: %s\n", ri.SupersededBy)
			}
			if show := ri.Followups["show"]; show != "" {
				fmt.Printf("    full: %s\n", show)
			}
		}
	}

	if len(r.Notes) > 0 {
		fmt.Println()
		for _, n := range r.Notes {
			fmt.Printf("⚠ %s\n", n)
		}
	}
}
