package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/engine"
)

var (
	actorImportActorID   string
	actorImportRemote    string
	actorImportSourceRef string
	actorImportImportRef string
	actorImportForce     bool
)

var actorCmd = &cobra.Command{
	Use:   "actor",
	Short: "Import or repair actor-log metadata",
}

var actorImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Explicitly accept a contributor actor log from a fork or import ref",
	Long: `Explicitly accept another actor's Mainline actor log into this
repository's actor-log namespace.

This is the trust-boundary path for fork PRs where the contributor also
used Mainline locally. Normal sync fetches only the configured upstream
actor refs; this command lets an upstream maintainer fetch a specific fork
actor ref, validate that the events belong to the expected actor, accept
that ref, rebuild the view, and run the normal auto-pin cascade.

It imports author-sealed Mainline metadata. It does not parse GitHub PR
body templates and it does not copy fork git notes into upstream. When a
remote is provided, it also best-effort fetches fork branches referenced by
sealed intents into refs/mainline/imports/<actor>/branches/* so squash/rebase
PRs can still auto-pin by tree/content.`,
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		result, err := svc.ImportActorLog(engine.ActorLogImportOptions{
			ActorID:   actorImportActorID,
			Remote:    actorImportRemote,
			SourceRef: actorImportSourceRef,
			ImportRef: actorImportImportRef,
			Force:     actorImportForce,
		})
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(result)
			return
		}
		fmt.Printf("Accepted actor log for %s\n", result.ActorID)
		if result.SourceRemote != "" {
			fmt.Printf("  Remote:  %s\n", result.SourceRemote)
		}
		fmt.Printf("  Source:  %s\n", result.SourceRef)
		if result.ImportRef != "" {
			fmt.Printf("  Import:  %s\n", result.ImportRef)
		}
		if len(result.ImportedBranchRefs) > 0 {
			fmt.Printf("  Code refs: %v\n", result.ImportedBranchRefs)
		}
		fmt.Printf("  Target:  %s\n", result.TargetRef)
		fmt.Printf("  Events:  %d (%d sealed intent(s))\n", result.EventCount, result.SealedIntentCount)
		if len(result.SealedIntentIDs) > 0 {
			fmt.Printf("  Intents: %v\n", result.SealedIntentIDs)
		}
		for _, warning := range result.ObjectFetchWarnings {
			fmt.Printf("  Warning: %s\n", warning)
		}
		if len(result.AutoPinned) > 0 {
			fmt.Printf("  Auto-pinned %d intent(s):\n", len(result.AutoPinned))
			for _, p := range result.AutoPinned {
				fmt.Printf("    %s -> %s (%s)\n", p.IntentID, p.Commit, p.MatchStrategy)
			}
		}
		if result.Pushed {
			fmt.Printf("  Pushed accepted actor metadata to %s\n", svc.RemoteName())
		}
	},
}

func init() {
	actorImportCmd.Flags().StringVar(&actorImportActorID, "actor", "", "actor ID to accept (required)")
	actorImportCmd.Flags().StringVar(&actorImportRemote, "remote", "", "fork remote name or URL to fetch from")
	actorImportCmd.Flags().StringVar(&actorImportSourceRef, "source-ref", "", "source actor-log ref (defaults to this actor's Mainline actor ref when --remote is set)")
	actorImportCmd.Flags().StringVar(&actorImportImportRef, "import-ref", "", "local temporary ref for fetched actor log")
	actorImportCmd.Flags().BoolVar(&actorImportForce, "force", false, "replace a divergent existing actor log after manual review")
	actorCmd.AddCommand(actorImportCmd)
}
