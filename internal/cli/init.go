package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	isatty "github.com/mattn/go-isatty"
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

		resolvedName, actorNameSource := resolveInitActorName(svc, initActorName)

		result, err := svc.InitWithOptions(resolvedName, engine.InitOptions{
			InstallAgentIntegrations: true,
			Progress:                 initProgressPrinter(),
		})
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
		} else {
			if !result.Created {
				fmt.Printf("Mainline already initialized in %s\n", result.RepoRoot)
				if result.IdentityUpdated {
					fmt.Printf("  Updated local actor name: %s\n", result.ActorName)
				} else {
					fmt.Printf("  Local actor name already: %s\n", result.ActorName)
				}
				fmt.Println()
				fmt.Println("Next: start work with your agent, then inspect recorded intent with `mainline log` or `mainline hub open`.")
				return
			}
			fmt.Printf("Mainline initialized in %s\n", result.RepoRoot)
			fmt.Printf("  Actor ID:    %s\n", result.ActorID)
			fmt.Printf("  Actor name:  %s\n", result.ActorName)
			fmt.Printf("  Main branch: %s\n", result.MainBranch)
			switch actorNameSource {
			case "env":
				fmt.Printf("  (actor name picked up from $%s)\n", envActorName)
			case "git":
				fmt.Println("  (actor name picked up from git config user.name)")
			case "prompt":
				fmt.Println("  (actor name entered at the terminal)")
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
			if len(result.LocalOnlyFiles) > 0 {
				fmt.Println()
				fmt.Println("Local-only agent hook files:")
				for _, p := range result.LocalOnlyFiles {
					fmt.Printf("  · %s\n", p)
				}
				fmt.Println("  (excluded in this clone via .git/info/exclude)")
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
			if result.ActorName == "default-agent" {
				fmt.Println()
				fmt.Println("⚠ No --actor-name passed; defaulted to 'default-agent'.")
				fmt.Println("  Run `mainline init --actor-name \"<your name>\"` to update")
				fmt.Println("  this clone-local identity, or export $" + envActorName + " in your shell.")
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
			fmt.Println()
			fmt.Println("Next: start work with your agent, then inspect recorded intent with `mainline log` or `mainline hub open`.")
		}
	},
}

func init() {
	initCmd.Flags().StringVar(&initActorName, "actor-name", "", "name for this actor identity (or export "+envActorName+")")
	initCmd.Flags().BoolVar(&initRewire, "rewire", false, "(re-)apply remote refspec config on an already-initialised repo")
}

func resolveInitActorName(svc *engine.Service, explicit string) (string, string) {
	if name := strings.TrimSpace(explicit); name != "" {
		return name, "flag"
	}
	if envName := strings.TrimSpace(os.Getenv(envActorName)); envName != "" {
		return envName, "env"
	}
	gitName := strings.TrimSpace(svc.Git.ConfigGetOne("user.name"))
	if shouldPromptInitActorName() {
		defaultName := gitName
		if defaultName == "" {
			defaultName = "default-agent"
		}
		fmt.Fprintf(os.Stdout, "Actor name [%s]: ", defaultName)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if name := strings.TrimSpace(line); name != "" {
			return name, "prompt"
		}
		if gitName != "" {
			return gitName, "git"
		}
		return "", "default"
	}
	if gitName != "" {
		return gitName, "git"
	}
	return "", "default"
}

func shouldPromptInitActorName() bool {
	return !jsonOutput &&
		isatty.IsTerminal(os.Stdin.Fd()) &&
		isatty.IsTerminal(os.Stdout.Fd())
}

func initProgressPrinter() func(message string) {
	if jsonOutput {
		return nil
	}
	return func(message string) {
		fmt.Fprintf(os.Stderr, "mainline init: %s\n", message)
	}
}

func renderInitAgentIntegrations(r *engine.AgentIntegrationInstallResult) {
	if r == nil {
		return
	}
	fmt.Println("Agent integrations:")
	switch {
	case r.Skill.Installed:
		fmt.Printf("  ✓ skill: installed via `%s`\n", strings.Join(r.Skill.Command, " "))
	case r.Skill.Skipped:
		fmt.Printf("  · skill: skipped (%s)\n", r.Skill.Error)
	case r.Skill.Error != "":
		fmt.Printf("  ✗ skill: %s\n", r.Skill.Error)
	default:
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
