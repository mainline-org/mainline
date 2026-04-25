package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var prTrailerIntentID string

var prTrailerCmd = &cobra.Command{
	Use:   "pr-trailer",
	Short: "Output PR trailer for an intent",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		if prTrailerIntentID == "" && len(args) > 0 {
			prTrailerIntentID = args[0]
		}
		if prTrailerIntentID == "" {
			outputError(fmt.Errorf("intent ID is required"))
			return
		}

		trailer, err := svc.PRTrailer(prTrailerIntentID)
		if err != nil {
			outputError(err)
			return
		}

		fmt.Println(trailer)
	},
}

var prDescIntentID string

var prDescriptionCmd = &cobra.Command{
	Use:   "pr-description",
	Short: "Generate PR description for an intent",
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

		fmt.Println(desc)
	},
}

func init() {
	prTrailerCmd.Flags().StringVar(&prTrailerIntentID, "intent", "", "intent ID")
	prDescriptionCmd.Flags().StringVar(&prDescIntentID, "intent", "", "intent ID")
}
