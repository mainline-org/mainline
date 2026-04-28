package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// `mainline lint` — Step 3 from docs_for_ai/mainline-spec-v0.2.md.
// Advisory deterministic checks against a sealed (or in-flight)
// intent's quality. Does not block seal --submit; the spec reserves
// hard enforcement for a future hook.

var lintCmd = &cobra.Command{
	Use:   "lint [intent_id]",
	Short: "Check a sealed intent's quality (no decisions, boilerplate what, missing rationale, …)",
	Long: `Run deterministic quality checks against a sealed intent (or the
active draft if no id is given). Lint is advisory — it never blocks
seal submission. The point is to catch boilerplate / shallow seals
before they pollute future retrieval.

Checks (v1):

  empty_what                summary.what is empty
  boilerplate_what          summary.what is "implemented changes" / "see diff" / etc
  empty_why                 summary.why is empty
  no_decisions              no decisions recorded
  decision_no_chose         a decision has no chose
  decision_no_rationale     a long decision (>50 chars) has no rationale (warning)
  no_constraints            no risks AND no anti_patterns recorded (warning)
  fingerprint_no_subsystems fingerprint.subsystems is empty
  fingerprint_no_files      fingerprint.files_touched is empty
  supersedes_unknown        supersedes references an unknown intent`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		id := ""
		if len(args) == 1 {
			id = args[0]
		}
		res, err := svc.Lint(id)
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(res)
			return
		}
		fmt.Printf("Lint %s\n", res.IntentID)
		if len(res.Issues) == 0 {
			fmt.Println("  ✓ no issues")
			return
		}
		for _, iss := range res.Issues {
			marker := "⚠"
			switch iss.Severity {
			case "error":
				marker = "✗"
			case "info":
				marker = "·"
			}
			line := fmt.Sprintf("  %s [%s] %s: %s", marker, iss.Severity, iss.Code, iss.Message)
			if iss.Field != "" {
				line += fmt.Sprintf("  (%s)", iss.Field)
			}
			fmt.Println(line)
		}
		fmt.Println()
		if res.Pass {
			fmt.Println("Pass (no errors; review warnings if any).")
		} else {
			fmt.Println("Fail (errors above; lint is advisory but the seal will be hard to retrieve later).")
			os.Exit(2)
		}
	},
}
