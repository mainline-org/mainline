package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/buildinfo"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show build and version information",
	Run: func(cmd *cobra.Command, args []string) {
		info := buildinfo.Current()
		if jsonOutput {
			outputJSON(info)
			return
		}
		fmt.Printf("mainline %s\n", info.Version)
		if info.Commit != "" {
			fmt.Printf("commit: %s\n", info.Commit)
		}
		if info.Date != "" {
			fmt.Printf("built: %s\n", info.Date)
		}
	},
}
