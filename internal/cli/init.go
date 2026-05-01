package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/engine"
)

// envActorName is the env-var fallback for --actor-name. Wired here
// so CI scripts that already export MAINLINE_ACTOR_NAME (a common
// pattern from the seal-and-publish daemons) don't have to thread
// the flag through every invocation, and so a forgetful first-time
// user with the var exported in their shell still gets a real
// identity instead of the silent "default-agent" fallback.
const envActorName = "MAINLINE_ACTOR_NAME"

var initActorName string
var initRewire bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize mainline in current repository",
	Long: `Initialise mainline in the current git repository: writes .mainline/
config, generates an actor identity, configures notes / actor-log
fetch+push refspecs on origin (if origin is configured), then installs
the default Mainline skill and repo-local hook integrations.

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
					fmt.Println("No remote configured — refspecs not written.")
					fmt.Println("Add a remote first, then re-run 'mainline init --rewire'.")
				} else if len(r.RefspecsAdded) == 0 {
					fmt.Println("Refspecs already configured.")
				} else {
					fmt.Printf("Wired up %d refspec(s):\n", len(r.RefspecsAdded))
					for _, s := range r.RefspecsAdded {
						fmt.Printf("  + %s\n", s)
					}
				}
			}
			return
		}

		// Resolve actor name: explicit --actor-name wins; fall back
		// to MAINLINE_ACTOR_NAME if exported; otherwise svc.Init
		// will use its internal default ("default-agent") and we
		// warn loudly below.
		resolvedName := initActorName
		usedEnvFallback := false
		if resolvedName == "" {
			if envName := strings.TrimSpace(os.Getenv(envActorName)); envName != "" {
				resolvedName = envName
				usedEnvFallback = true
			}
		}

		result, err := svc.InitWithOptions(resolvedName, engine.InitOptions{
			InstallAgentIntegrations: true,
		})
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
			if usedEnvFallback {
				fmt.Printf("  (actor name picked up from $%s)\n", envActorName)
			}
			// Surface what Init actually wrote to git. Pre-this-fix
			// the success message was silent about the new commit;
			// trial users ran `git status` next, saw a clean tree,
			// and didn't know what had changed in their repo. Now we
			// print the staged paths and the commit SHA so the
			// before/after is visible without leaving the terminal.
			if len(result.FilesStaged) > 0 {
				fmt.Println()
				fmt.Println("Files written and staged:")
				for _, p := range result.FilesStaged {
					fmt.Printf("  + %s\n", p)
				}
				if result.CommitHash != "" {
					fmt.Printf("Committed as %s (\"mainline: init\").\n", shortHash(result.CommitHash))
				}
			} else if result.CommitHash == "" {
				// Re-init against a repo where every managed file
				// was already tracked: nothing to stage, nothing to
				// commit. Tell the user explicitly so they don't
				// wonder if init silently did nothing.
				fmt.Println()
				fmt.Println("(All Mainline-managed files were already tracked; no new commit.)")
			}
			if result.AgentIntegrations != nil {
				fmt.Println()
				renderInitAgentIntegrations(result.AgentIntegrations)
			}
			// Surface the default-actor-name fallback. Pre-this-fix
			// the alpha walkthrough caught: a fresh user runs bare
			// `mainline init` and silently becomes "default-agent"
			// in every actor log + commit note, with no prompt to
			// fix it. Now we say so loudly.
			if resolvedName == "" {
				fmt.Println()
				fmt.Println("⚠ No --actor-name passed; defaulted to 'default-agent'.")
				fmt.Println("  Re-run with --actor-name \"<your name>\" to claim a")
				fmt.Println("  recognisable identity, or export $" + envActorName + " in your shell.")
				fmt.Println("  (it shows up in `mainline log`, on commit notes, and in the audit trail).")
			}
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
	initCmd.Flags().StringVar(&initActorName, "actor-name", "", "name for this actor identity (or export "+envActorName+")")
	initCmd.Flags().BoolVar(&initRewire, "rewire", false, "(re-)apply remote refspec config on an already-initialised repo")
}

func renderInitAgentIntegrations(r *engine.AgentIntegrationInstallResult) {
	if r == nil {
		return
	}
	fmt.Println("Agent integrations:")
	if r.Skill.Installed {
		fmt.Println("  ✓ skill: installed via `npx skills add mainline`")
	} else if r.Skill.Skipped {
		fmt.Printf("  · skill: skipped (%s)\n", r.Skill.Error)
	} else if r.Skill.Error != "" {
		fmt.Printf("  ✗ skill: %s\n", r.Skill.Error)
	} else {
		fmt.Println("  · skill: no change")
	}
	for _, h := range r.Hooks {
		if h.Error != "" {
			fmt.Printf("  ✗ hook %-12s %s\n", h.Agent+":", h.Error)
			continue
		}
		state := "installed"
		if h.Report.AlreadyInstalled {
			state = "already up to date"
		}
		fmt.Printf("  ✓ hook %-12s %s (%d entries)\n", h.Agent+":", state, h.Report.HookCount)
	}
	fmt.Println("  `mainline agents install` remains an explicit repo-policy opt-in for AGENTS.md.")
}
