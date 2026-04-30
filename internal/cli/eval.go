package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
  mainline eval run [name]       # precondition scorer (does retrieval surface the constraints?)
  mainline eval agent --runner <path> [name]
                                  # LLM runner: drive code-first vs intent-first prompts
                                  # against your runner binary; score forbidden-list violations`,
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

var (
	evalAgentRunnerPath string
	evalAgentScratchDir string
	evalAgentJudgePath  string
	evalAgentOutputDir  string
	evalAgentModel      string
	evalAgentSeed       int
)

var evalAgentCmd = &cobra.Command{
	Use:   "agent [name]",
	Short: "Drive code-first vs intent-first prompts against your runner binary",
	Long: `For each populated fixture, invoke your runner binary twice
(once with the code-first prompt, once with the intent-first prompt)
and score forbidden-list violations + ContextRetrieved.

The runner binary's contract:

  - stdin: a JSON envelope { "fixture": <Fixture>, "prompt": "<full
    prompt text>", "prompt_key": "code_first"|"intent_first",
    "scratch_dir": "<absolute path>" }
  - stdout: either an AgentRunResult JSON object or free-form text;
    text containing "mainline context" infers ContextRetrieved=true.

This is the seam any LLM CLI plugs into — write a small wrapper that
reads stdin, drives your favourite LLM, writes stdout.

Scorer v2 (--judge):

  When --judge is provided, the harness uses an LLM-as-judge binary
  instead of substring matching. The judge classifies each (output,
  forbidden_item) pair as PROPOSED (violation) or DECLINED-WITH-
  REFERENCE (not a violation). This eliminates the false positives
  that plague substring scoring.

  Judge binary contract:
  - stdin: { "agent_output": "...", "forbidden_item": "...",
    "fixture_name": "...", "prompt_key": "...", "task": "..." }
  - stdout: { "proposed": bool, "referenced_but_rejected": bool,
    "evidence_quote": "...", "confidence": 0.0-1.0 }`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if evalAgentRunnerPath == "" {
			outputError(fmt.Errorf("eval agent: --runner <path> is required"))
			return
		}
		fs := eval.Fixtures()
		if len(args) == 1 {
			matched := []eval.Fixture{}
			for _, f := range fs {
				if f.Name == args[0] {
					matched = append(matched, f)
				}
			}
			if len(matched) == 0 {
				outputError(fmt.Errorf("eval: fixture %q not found", args[0]))
				return
			}
			fs = matched
		}
		runner := &eval.CommandRunner{Path: evalAgentRunnerPath}
		if evalAgentModel != "" {
			runner.Env = append(runner.Env, "EVAL_MODEL="+evalAgentModel)
		}
		if evalAgentSeed > 0 {
			runner.Env = append(runner.Env, fmt.Sprintf("EVAL_SEED=%d", evalAgentSeed))
		}
		scratch := evalAgentScratchDir
		if scratch == "" {
			scratch = filepath.Join(os.TempDir(), "mainline-eval-agent")
		}
		_ = os.MkdirAll(scratch, 0o755)

		// v2 path: if --judge is provided, use LLM-as-judge scorer
		if evalAgentJudgePath != "" {
			judge := &eval.CommandJudge{Path: evalAgentJudgePath}
			scores := eval.RunWithJudge(fs, runner, judge, scratch)
			summary := eval.Summarize(scores)

			// Persist output if --output-dir specified
			if evalAgentOutputDir != "" {
				persistEvalOutput(scores, summary)
			}

			if jsonOutput {
				outputJSON(eval.EvalRunOutput{
					Metadata: eval.EvalRunMetadata{
						Timestamp:    time.Now().UTC().Format(time.RFC3339),
						Model:        evalAgentModel,
						RunnerPath:   evalAgentRunnerPath,
						JudgePath:    evalAgentJudgePath,
						Seed:         evalAgentSeed,
						FixtureCount: len(fs),
					},
					Scores:  scores,
					Summary: summary,
				})
			} else {
				renderJudgedScores(scores, summary)
			}
			if summary.CodeFirstViolations > 0 || summary.IntentFirstViolations > 0 {
				os.Exit(2)
			}
			return
		}

		// v1 path: substring scorer (legacy, preserved for comparison)
		scores := eval.RunWithAgent(fs, runner, scratch)
		if jsonOutput {
			outputJSON(scores)
		} else {
			renderAgentScores(scores)
		}
		for _, s := range scores {
			if len(s.ForbiddenViolations) > 0 || s.RunError != "" {
				os.Exit(2)
			}
		}
	},
}

// renderJudgedScores prints the v2 judge-scored results.
func renderJudgedScores(scores []eval.JudgedScore, summary eval.EvalRunSummary) {
	byFixture := map[string]map[eval.AgentRunPrompt]eval.JudgedScore{}
	order := []string{}
	for _, s := range scores {
		if _, seen := byFixture[s.Fixture]; !seen {
			order = append(order, s.Fixture)
			byFixture[s.Fixture] = map[eval.AgentRunPrompt]eval.JudgedScore{}
		}
		byFixture[s.Fixture][s.Prompt] = s
	}
	for _, name := range order {
		row := byFixture[name]
		cf := row[eval.AgentPromptCodeFirst]
		intf := row[eval.AgentPromptIntentFirst]
		fmt.Printf("\n%s\n", name)
		fmt.Printf("  code-first    violations=%d  declined=%d  context=%v  ms=%d\n",
			cf.ViolationCount, cf.DeclinedCount, cf.ContextRetrieved, cf.DurationMillis)
		fmt.Printf("  intent-first  violations=%d  declined=%d  context=%v  ms=%d\n",
			intf.ViolationCount, intf.DeclinedCount, intf.ContextRetrieved, intf.DurationMillis)
		for _, v := range cf.Verdicts {
			if v.Proposed {
				fmt.Printf("    ✗ CF PROPOSED: %s (%.0f%%)\n", v.ForbiddenItem, v.Confidence*100)
				if v.EvidenceQuote != "" {
					fmt.Printf("      evidence: %q\n", v.EvidenceQuote)
				}
			}
		}
		for _, v := range intf.Verdicts {
			if v.Proposed {
				fmt.Printf("    ✗ IF PROPOSED: %s (%.0f%%)\n", v.ForbiddenItem, v.Confidence*100)
				if v.EvidenceQuote != "" {
					fmt.Printf("      evidence: %q\n", v.EvidenceQuote)
				}
			}
		}
	}
	fmt.Printf("\n─── Summary ───\n")
	fmt.Printf("  code-first:   %d violations across %d fixtures\n",
		summary.CodeFirstViolations, summary.CodeFirstFixtures)
	fmt.Printf("  intent-first: %d violations across %d fixtures\n",
		summary.IntentFirstViolations, summary.IntentFirstFixtures)
	fmt.Printf("  Δ = %d  verdict: %s\n", summary.Delta, summary.Verdict)
}

// persistEvalOutput writes the full eval output to the output directory.
func persistEvalOutput(scores []eval.JudgedScore, summary eval.EvalRunSummary) {
	dir := evalAgentOutputDir
	_ = os.MkdirAll(dir, 0o755)

	output := eval.EvalRunOutput{
		Metadata: eval.EvalRunMetadata{
			Timestamp:    time.Now().UTC().Format(time.RFC3339),
			Model:        evalAgentModel,
			RunnerPath:   evalAgentRunnerPath,
			JudgePath:    evalAgentJudgePath,
			Seed:         evalAgentSeed,
			FixtureCount: len(scores) / 2,
		},
		Scores:  scores,
		Summary: summary,
	}
	data, _ := json.MarshalIndent(output, "", "  ")
	outPath := filepath.Join(dir, "eval-run.json")
	_ = os.WriteFile(outPath, data, 0o644)
	fmt.Fprintf(os.Stderr, "eval: output written to %s\n", outPath)
}

// renderAgentScores prints a per-(fixture, prompt) table comparing
// code-first to intent-first. The headline question this answers is
// "did intent-first avoid the forbidden actions code-first hit?"
func renderAgentScores(scores []eval.AgentScore) {
	byFixture := map[string]map[eval.AgentRunPrompt]eval.AgentScore{}
	order := []string{}
	for _, s := range scores {
		if _, seen := byFixture[s.Fixture]; !seen {
			order = append(order, s.Fixture)
			byFixture[s.Fixture] = map[eval.AgentRunPrompt]eval.AgentScore{}
		}
		byFixture[s.Fixture][s.Prompt] = s
	}
	for _, name := range order {
		row := byFixture[name]
		cf := row[eval.AgentPromptCodeFirst]
		intf := row[eval.AgentPromptIntentFirst]
		fmt.Printf("\n%s\n", name)
		fmt.Printf("  code-first    violations=%d  context_retrieved=%v  ms=%d\n",
			len(cf.ForbiddenViolations), cf.ContextRetrieved, cf.DurationMillis)
		fmt.Printf("  intent-first  violations=%d  context_retrieved=%v  ms=%d\n",
			len(intf.ForbiddenViolations), intf.ContextRetrieved, intf.DurationMillis)
		if len(cf.ForbiddenViolations) > 0 {
			fmt.Println("  code-first violations:")
			for _, v := range cf.ForbiddenViolations {
				fmt.Printf("    ✗ %s\n", v)
			}
		}
		if len(intf.ForbiddenViolations) > 0 {
			fmt.Println("  intent-first violations:")
			for _, v := range intf.ForbiddenViolations {
				fmt.Printf("    ✗ %s\n", v)
			}
		}
		if cf.RunError != "" || intf.RunError != "" {
			if cf.RunError != "" {
				fmt.Printf("  code-first error: %s\n", cf.RunError)
			}
			if intf.RunError != "" {
				fmt.Printf("  intent-first error: %s\n", intf.RunError)
			}
		}
	}
}

func init() {
	evalAgentCmd.Flags().StringVar(&evalAgentRunnerPath, "runner", "",
		"path to a runner binary (reads JSON envelope on stdin, writes agent response on stdout)")
	evalAgentCmd.Flags().StringVar(&evalAgentScratchDir, "scratch", "",
		"scratch directory for runner artifacts (default: <os-temp>/mainline-eval-agent)")
	evalAgentCmd.Flags().StringVar(&evalAgentJudgePath, "judge", "",
		"path to a judge binary for scorer v2 (reads JudgeRequest on stdin, writes JudgeVerdict on stdout)")
	evalAgentCmd.Flags().StringVar(&evalAgentOutputDir, "output-dir", "",
		"directory to persist eval run output (scores, verdicts, summary)")
	evalAgentCmd.Flags().StringVar(&evalAgentModel, "model", "",
		"model name passed to runner via EVAL_MODEL env var")
	evalAgentCmd.Flags().IntVar(&evalAgentSeed, "seed", 0,
		"seed number passed to runner via EVAL_SEED env var (for multi-seed runs)")
	evalCmd.AddCommand(evalListCmd, evalRunCmd, evalAgentCmd)
}
