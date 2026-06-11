package cli

import (
	"fmt"

	"github.com/mainline-org/mainline/internal/engine"
	"github.com/spf13/cobra"
)

// rc3: pr-trailer command removed. Metadata goes via git notes, not trailers.

var prDescIntentID string
var prCommentBase string
var prCommentHead string
var prCommentBranch string
var prImportPRNumber int
var prImportForkURL string
var prImportHeadRef string
var prImportHeadSHA string
var prImportActorID string

var prDescriptionCmd = &cobra.Command{
	Use:   "pr-description",
	Short: "Generate PR description for an intent (human-readable markdown)",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		if prDescIntentID == "" && len(args) > 0 {
			prDescIntentID = args[0]
		}
		if prDescIntentID == "" {
			outputError(fmt.Errorf("intent ID is required"))
			return
		}

		desc, err := svc.PRDescription(prDescIntentID)
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(map[string]string{"intent_id": prDescIntentID, "markdown": desc})
		} else {
			fmt.Println(desc)
		}
	},
}

var prCommentCmd = &cobra.Command{
	Use:   "pr-comment",
	Short: "Generate PR intent comment for a pull request (human-readable markdown)",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		comment, err := svc.PRComment(prCommentBase, prCommentHead, prCommentBranch)
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(map[string]string{"markdown": comment})
		} else {
			fmt.Println(comment)
		}
	},
}

var prImportCmd = &cobra.Command{
	Use:   "pr-import",
	Short: "Import a fork PR contributor's Mainline intent metadata",
	Long: `Import a fork PR contributor's author-sealed Mainline actor log.

This command is designed for upstream-side automation after a fork PR merges.
It treats PR metadata as locator information only, discovers actor logs from
the fork remote, picks a unique sealed intent matching the PR head branch,
commit, or tree, then delegates trust-boundary validation to actor import.`,
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		result, err := svc.ImportPullRequestIntent(engine.PullRequestImportOptions{
			PRNumber: prImportPRNumber,
			ForkURL:  prImportForkURL,
			HeadRef:  prImportHeadRef,
			HeadSHA:  prImportHeadSHA,
			ActorID:  prImportActorID,
		})
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(result)
			return
		}
		fmt.Printf("Fork PR import status: %s\n", result.Status)
		if result.Selected != nil {
			fmt.Printf("  Actor:  %s\n", result.Selected.ActorID)
			fmt.Printf("  Intent: %s\n", result.Selected.IntentID)
			fmt.Printf("  Match:  %v\n", result.Selected.MatchReasons)
		}
		if result.Import != nil {
			fmt.Printf("  Accepted: %t\n", result.Import.Accepted)
			if result.Import.Pushed {
				fmt.Println("  Pushed upstream metadata")
			}
		}
		for _, warning := range result.Warnings {
			fmt.Printf("  Warning: %s\n", warning)
		}
	},
}

func init() {
	prDescriptionCmd.Flags().StringVar(&prDescIntentID, "intent", "", "intent ID")
	prCommentCmd.Flags().StringVar(&prCommentBase, "base", "", "base commit SHA for the PR range")
	prCommentCmd.Flags().StringVar(&prCommentHead, "head", "", "head commit SHA for the PR range")
	prCommentCmd.Flags().StringVar(&prCommentBranch, "branch", "", "PR head branch name fallback")
	prImportCmd.Flags().IntVar(&prImportPRNumber, "pr", 0, "pull request number for diagnostics")
	prImportCmd.Flags().StringVar(&prImportForkURL, "fork-url", "", "fork repository URL to discover actor logs from")
	prImportCmd.Flags().StringVar(&prImportHeadRef, "head-ref", "", "pull request head branch name")
	prImportCmd.Flags().StringVar(&prImportHeadSHA, "head-sha", "", "pull request head commit SHA")
	prImportCmd.Flags().StringVar(&prImportActorID, "actor", "", "expected contributor actor ID; omit to discover from fork actor refs")
}
