package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var initActorName string
var initRewire bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize mainline in current repository",
	Long: `Initialise mainline in the current git repository: writes .mainline/
config, generates an actor identity, configures notes / actor-log
fetch+push refspecs on origin (if origin is configured), and writes
AGENTS.md plus a PR template.

If you ran 'mainline init' before adding your git remote, the refspec
configuration step was skipped silently. Re-run with --rewire to fix
that without re-creating identity or team config.`,
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		if initRewire {
			r, err := svc.Rewire()
			if err != nil {
				outputError(err)
				return
			}
			if jsonOutput {
				outputJSON(r)
			} else {
				if !r.HadRemote {
					fmt.Println("No 'origin' remote configured — refspecs not written.")
					fmt.Println("Add a remote first, then re-run 'mainline init --rewire'.")
				} else if len(r.RefspecsAdded) == 0 {
					fmt.Println("Refspecs already configured. AGENTS.md and PR template re-written.")
				} else {
					fmt.Printf("Wired up %d refspec(s):\n", len(r.RefspecsAdded))
					for _, s := range r.RefspecsAdded {
						fmt.Printf("  + %s\n", s)
					}
					fmt.Println("AGENTS.md and PR template re-written.")
				}
			}
			return
		}

		result, err := svc.Init(initActorName)
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			fmt.Printf("Mainline initialized in %s\n", result.RepoRoot)
			fmt.Printf("  Actor ID:    %s\n", result.ActorID)
			fmt.Printf("  Actor name:  %s\n", result.ActorName)
			fmt.Printf("  Main branch: %s\n", result.MainBranch)
			remote := svc.RemoteName()
			if !svc.Git.HasRemote(remote) {
				fmt.Println()
				fmt.Printf("Note: no '%s' remote configured yet. After you add one,\n", remote)
				fmt.Println("      run 'mainline init --rewire' to configure notes and")
				fmt.Println("      actor-log refspecs so cross-actor sync works.")
				fmt.Println("      (Use a different remote name? Set [mainline] remote in")
				fmt.Println("       .mainline/config.toml.)")
			}
		}
	},
}

func init() {
	initCmd.Flags().StringVar(&initActorName, "actor-name", "", "name for this actor identity")
	initCmd.Flags().BoolVar(&initRewire, "rewire", false, "(re-)apply remote refspec config + AGENTS.md + PR template on an already-initialised repo")
}
