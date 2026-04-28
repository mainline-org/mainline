package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/engine"
	"github.com/mainline-org/mainline/internal/eval"
	"github.com/mainline-org/mainline/internal/storage"
)

// `mainline eval` — Step 5 from docs_for_ai/mainline-spec-v0.2.md.
//
// v1 ships the substrate: fixture catalog + precondition scorer
// (does retrieval surface the constraints?). The agent-side
// validation (does an intent-first agent actually act on the
// constraint?) needs an LLM runner and lands in a follow-up.

var evalCmd = &cobra.Command{
	Use:   "eval",
	Short: "Run the agent eval harness against the embedded fixture catalog",
	Long: `Run agent-eval fixtures end-to-end against retrieval. Each fixture
seeds a synthetic intent view, runs ` + "`mainline context --query`" + `
on the fixture's task description, and scores whether retrieval
surfaces the expected constraining intents and their anti_patterns.

This is the *precondition* test for the product thesis: a constraint
that retrieval cannot surface is one no agent can respect, regardless
of prompt. Once this passes, the next layer (LLM runner; future PR)
drives a code-first vs intent-first agent on the same fixtures and
compares outcomes.

Subcommands:

  mainline eval list             # show every fixture (populated + stubs)
  mainline eval run [name]       # run all (or one); exit 2 on any failure`,
}

var evalListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the embedded fixture catalog",
	Run: func(cmd *cobra.Command, args []string) {
		fs := eval.Fixtures()
		if jsonOutput {
			outputJSON(fs)
			return
		}
		fmt.Printf("%d fixtures:\n", len(fs))
		for _, f := range fs {
			tag := "  "
			if len(f.Intents) == 0 {
				tag = "· "
			}
			fmt.Printf("%s%-26s  %s\n", tag, f.Name, f.Description)
		}
		fmt.Println("\n· = stub (not yet runnable)")
	},
}

var evalRunCmd = &cobra.Command{
	Use:   "run [name]",
	Short: "Run all fixtures (or one named fixture)",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fs := eval.Fixtures()
		if len(args) == 1 {
			matched := []eval.Fixture{}
			for _, f := range fs {
				if f.Name == args[0] {
					matched = append(matched, f)
				}
			}
			if len(matched) == 0 {
				outputError(fmt.Errorf("eval: fixture %q not found (run `mainline eval list`)", args[0]))
				return
			}
			fs = matched
		}
		summary, err := runEvalSet(fs)
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(summary)
		} else {
			renderEvalSummary(summary)
		}
		if !summary.AllPassed {
			os.Exit(2)
		}
	},
}

// runEvalSet wires each fixture to a fresh synthetic engine.Service
// and calls eval.RunFixture. Each fixture gets its own scratch
// repo so seeded views don't leak across cases.
func runEvalSet(fs []eval.Fixture) (eval.Summary, error) {
	out := eval.Summary{}
	for _, f := range fs {
		if len(f.Intents) == 0 {
			out.Results = append(out.Results, eval.ScoreResult{
				Fixture:     f.Name,
				Description: f.Description,
				Pass:        false,
			})
			out.Skipped++
			continue
		}
		retriever, err := newFixtureRetriever(f)
		if err != nil {
			return out, fmt.Errorf("setup %s: %w", f.Name, err)
		}
		res, err := eval.RunFixture(f, retriever, 10)
		if err != nil {
			return out, fmt.Errorf("run %s: %w", f.Name, err)
		}
		out.Results = append(out.Results, res)
		if res.Pass {
			out.Passed++
		} else {
			out.Failed++
		}
	}
	out.AllPassed = out.Failed == 0 && out.Skipped == 0
	return out, nil
}

// fixtureRetriever wraps a real engine.Service against a synthetic
// view written to a scratch repo dir. The Service is not a full
// init'd repo — it has no git, no remote, no actor log — but it
// has a Store with the synthesised view, which is all
// RetrieveContext reads.
type fixtureRetriever struct {
	svc *engine.Service
}

func newFixtureRetriever(f eval.Fixture) (*fixtureRetriever, error) {
	dir, err := os.MkdirTemp("", "mainline-eval-"+f.Name+"-*")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ml-cache", "views"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, ".mainline"), 0o755); err != nil {
		return nil, err
	}
	// Minimal team config so requireInit passes and getTeamConfig
	// returns sensible defaults.
	cfg := []byte(`[mainline]
main_branch = "main"
actor_log_prefix = "actors"
`)
	if err := os.WriteFile(filepath.Join(dir, ".mainline", "config.toml"), cfg, 0o644); err != nil {
		return nil, err
	}
	store := storage.New(dir, nil)
	if err := store.WriteMainlineView(eval.BuildView(f)); err != nil {
		return nil, err
	}
	svc := engine.NewServiceFromRoot(dir)
	return &fixtureRetriever{svc: svc}, nil
}

// RetrieveByQuery satisfies eval.Retriever. The harness only uses
// query mode; --files / --current modes are out of scope for
// fixture scoring (we don't have a working tree).
func (f *fixtureRetriever) RetrieveByQuery(query string, limit int) ([]eval.Retrieved, error) {
	res, err := f.svc.RetrieveContext(engine.ContextRetrievalRequest{
		Mode:  "query",
		Query: query,
		Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]eval.Retrieved, 0, len(res.RelevantIntents))
	for _, ri := range res.RelevantIntents {
		out = append(out, eval.Retrieved{
			IntentID:     ri.IntentID,
			Status:       ri.Status,
			AntiPatterns: ri.AntiPatterns,
		})
	}
	return out, nil
}

func renderEvalSummary(s eval.Summary) {
	for _, r := range s.Results {
		marker := "✓"
		if !r.Pass {
			if len(r.Items) == 0 {
				marker = "·"
			} else {
				marker = "✗"
			}
		}
		fmt.Printf("%s  %-26s  %s\n", marker, r.Fixture, r.Description)
		for _, item := range r.Items {
			ms := "  ✓"
			if !item.Pass {
				ms = "  ✗"
			}
			line := fmt.Sprintf("    %s  %-26s  %s", ms, item.IntentID, item.Reason)
			if item.Note != "" {
				line += fmt.Sprintf("  — %s", item.Note)
			}
			fmt.Println(line)
		}
	}
	fmt.Printf("\nPassed=%d  Failed=%d  Skipped=%d  (all=%v)\n",
		s.Passed, s.Failed, s.Skipped, s.AllPassed)
	if s.Skipped > 0 {
		fmt.Println("Skipped fixtures are stubs in the catalog; no Intents seeded yet.")
	}
	if !s.AllPassed && s.Failed > 0 {
		fmt.Println("\nNext: investigate the ✗ rows above; either retrieval is missing a constraint")
		fmt.Println("the fixture expects, or the fixture's expectation is mis-specified.")
	}
}

func init() {
	evalCmd.AddCommand(evalListCmd, evalRunCmd)
}
