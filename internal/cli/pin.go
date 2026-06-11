package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// pinCmd is the manual-fallback escape hatch — required arguments
// `<intent> <commit>`. Auto-pin (the no-args variant that scanned all
// proposed intents) was removed in v0.2 because it is now an internal
// step inside `mainline sync`; nothing the user needs to invoke.
//
// The deprecated `reconcile` alias has also been removed. Notes
// already on the remote with via=reconcile_* continue to render
// correctly via sync.normaliseVia.
//
// Use cases for the manual command (rare):
//   - tree_hash / commit_hash / goal_text all missed (heavily
//     rebased history, cherry-pick across forks, etc.)
//   - reviewer pins another actor's intent without waiting for them
//   - automation / GitHub Action scripts pinning a known commit
var pinCmd = &cobra.Command{
	Use:   "pin <intent> <commit>",
	Short: "Manually pin an intent to a main-branch commit (auto-pin runs inside sync)",
	Long: `Pin the named intent to the named commit unconditionally. Use this
when the automatic strategies (tree_hash → commit_hash → goal_text)
inside 'mainline sync' cannot find a match — heavily rebased history,
cherry-picks across forks, or any other case where heuristic matching
fails.

For the 99 % case, you do not need this command. 'mainline sync' runs
the same matching cascade automatically and writes pin_auto notes.

The note's via is set to pin_explicit and added_by records the calling
actor.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		pinned, err := svc.PinExplicit(args[0], args[1])
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(pinned)
		} else {
			fmt.Printf("Pinned %s -> %s (%s)\n", pinned.IntentID, pinned.Commit, pinned.MatchStrategy)
		}
	},
}
