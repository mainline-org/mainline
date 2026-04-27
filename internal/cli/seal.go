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
				if len(result.Conflicts) > 0 {
					fmt.Printf("\n⚠ %d potential conflict(s) detected (intent is sealed; review when convenient):\n",
						len(result.Conflicts))
					for _, c := range result.Conflicts {
						fmt.Printf("  ↔ %s  score=%.2f confidence=%s (%s)\n",
							c.RemoteIntent, c.OverlapScore, c.Confidence, c.RemoteStatus)
						fmt.Printf("    %s\n", c.Reason)
					}
				}
			}
			return
		}

		// Default: show help
		_ = cmd.Help()
	},
}

func init() {
	sealCmd.Flags().BoolVar(&sealPrepare, "prepare", false, "output seal prepare package (JSON)")
	sealCmd.Flags().BoolVar(&sealSubmit, "submit", false, "submit seal result from stdin (JSON)")
	sealCmd.Flags().StringVar(&sealIntentID, "intent", "", "intent ID (default: active intent on current branch)")
	sealCmd.Flags().BoolVar(&sealOffline, "offline", false, "skip the auto sync+check inside --submit (sealed_local only)")
	sealCmd.Flags().BoolVar(&sealAllowDirty, "allow-dirty", false, "submit even when worktree is dirty/untracked (records dirty status in audit trail)")
}
