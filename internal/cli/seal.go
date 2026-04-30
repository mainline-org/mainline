package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/engine"
)

var sealPrepare bool
var sealSubmit bool
var sealIntentID string
var sealOffline bool
var sealAllowDirty bool

var sealCmd = &cobra.Command{
	Use:   "seal",
	Short: "Seal an intent (freeze code + generate summary)",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		if sealPrepare {
			pkg, err := svc.SealPrepare(sealIntentID)
			if err != nil {
				outputError(err)
				return
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(pkg)
			return
		}

		if sealSubmit {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				outputError(fmt.Errorf("read stdin: %w", err))
				return
			}
			result, err := svc.SealSubmitWithOptions(json.RawMessage(data),
				&engine.SealSubmitOptions{Offline: sealOffline, AllowDirty: sealAllowDirty})
			if err != nil {
				outputError(err)
				return
			}
			if jsonOutput {
				outputJSON(result)
			} else {
				fmt.Printf("Intent sealed: %s\n", result.IntentID)
				fmt.Printf("  Status:     %s\n", result.Status)
				fmt.Printf("  Published:  %v\n", result.Published)
				fmt.Printf("  Code commit: %s\n", result.CodeCommit)
				fmt.Printf("  Event ID:   %s\n", result.EventID)
				fmt.Printf("  Hash:       %s\n", result.Hash)
				if result.SyncRan && result.SyncError != "" {
					fmt.Printf("  Sync warn:  %s\n", result.SyncError)
				}
				if result.Warning != "" {
					fmt.Printf("  Warning:    %s\n", result.Warning)
				}
				// Soft-lint: spec §9 Step 4 — "hooks may soft-remind,
				// not hard-block". Inline lint is the same shape: we
				// run lint on the freshly-sealed payload and surface
				// warnings/errors as a hint, but never block submit.
				// First-touch users discover lint here without
				// reading the Mainline skill or `--help`.
				renderSealLintHint(svc, result.IntentID)
				if len(result.Conflicts) > 0 {
					fmt.Printf("\n⚠ %d potential conflict(s) detected (intent is sealed; review when convenient):\n",
						len(result.Conflicts))
					for _, c := range result.Conflicts {
						fmt.Printf("  ↔ %s  score=%.2f confidence=%s (%s)\n",
							c.RemoteIntent, c.OverlapScore, c.Confidence, c.RemoteStatus)
						fmt.Printf("    %s\n", c.Reason)
					}
					fmt.Println("\nIf any of these look like a real semantic conflict, run:")
					fmt.Printf("  mainline check --prepare --intent %s\n", result.IntentID)
				}
				renderSealNextSteps(svc, result)
			}
			return
		}

		// Default: show help
		_ = cmd.Help()
	},
}

// renderSealLintHint runs lint silently against the freshly-sealed
// intent and surfaces a one-line summary. Soft-remind only: errors
// here never block submit. The point is discoverability — first-
// touch users learn `mainline lint` exists at the moment it would
// have caught a low-quality seal.
func renderSealLintHint(svc *engine.Service, intentID string) {
	res, err := svc.Lint(intentID)
	if err != nil || res == nil {
		return
	}
	errs, warns := 0, 0
	for _, iss := range res.Issues {
		switch iss.Severity {
		case "error":
			errs++
		case "warning":
			warns++
		}
	}
	if errs == 0 && warns == 0 {
		return
	}
	fmt.Println()
	switch {
	case errs > 0 && warns > 0:
		fmt.Printf("⚠ Lint: %d error(s), %d warning(s) — `mainline lint %s`\n", errs, warns, intentID)
	case errs > 0:
		fmt.Printf("⚠ Lint: %d error(s) — `mainline lint %s`\n", errs, intentID)
	default:
		fmt.Printf("· Lint: %d warning(s) — `mainline lint %s`\n", warns, intentID)
	}
}

// renderSealNextSteps drops the "what do I do now?" breadcrumb a
// first-time user needs after a successful submit. Keep Git branch
// pushes framed as a separately-authorized action; Mainline publish
// only covers metadata refs. We print these *after* conflicts/lint so
// the last thing on screen is actionable. SealSubmitResult does not
// carry the branch name, so we read it from git directly — at this
// point the working tree is the same one the user just sealed
// from, so CurrentBranch is the right answer.
func renderSealNextSteps(svc *engine.Service, result *engine.SealSubmitResult) {
	branch, _ := svc.Git.CurrentBranch()
	if branch == "" {
		branch = "<your branch>"
	}
	fmt.Println()
	fmt.Println("Next:")
	fmt.Println("  · Branch is sealed and ready for push/PR.")
	fmt.Printf("  · If Git branch push is authorized: `git push -u origin %s`, then open a PR.\n", branch)
	fmt.Println("  · `mainline publish`/`seal --submit` publish metadata only; they do not authorize branch push.")
	fmt.Println("  · The next `mainline sync` auto-pins the merge commit.")
	if !result.Published {
		fmt.Println("  · Status: sealed_local — the actor log was not pushed (no remote, or sync skipped).")
		fmt.Println("    Run `mainline sync` once a remote is configured to publish.")
	}
}

func init() {
	sealCmd.Flags().BoolVar(&sealPrepare, "prepare", false, "output seal prepare package (JSON)")
	sealCmd.Flags().BoolVar(&sealSubmit, "submit", false, "submit seal result from stdin (JSON)")
	sealCmd.Flags().StringVar(&sealIntentID, "intent", "", "intent ID (default: active intent on current branch)")
	sealCmd.Flags().BoolVar(&sealOffline, "offline", false, "skip the auto sync+check inside --submit (sealed_local only)")
	sealCmd.Flags().BoolVar(&sealAllowDirty, "allow-dirty", false, "submit even when worktree is dirty/untracked (records dirty status in audit trail)")
}
