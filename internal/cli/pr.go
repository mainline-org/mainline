package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// rc3: pr-trailer command removed. Metadata goes via git notes, not trailers.

var prDescIntentID string
var prCommentBase string
var prCommentHead string
var prCommentBranch string

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

func init() {
	prDescriptionCmd.Flags().StringVar(&prDescIntentID, "intent", "", "intent ID")
	prCommentCmd.Flags().StringVar(&prCommentBase, "base", "", "base commit SHA for the PR range")
	prCommentCmd.Flags().StringVar(&prCommentHead, "head", "", "head commit SHA for the PR range")
	prCommentCmd.Flags().StringVar(&prCommentBranch, "branch", "", "PR head branch name fallback")
}
