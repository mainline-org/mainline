package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/engine"
)

// `mainline agents …` is the upgrade-safe family for the
// lightweight Mainline-managed block inside AGENTS.md (and the four IDE
// pointer stubs).
//
// Contract: the user owns AGENTS.md; Mainline owns one versioned,
// checksummed block inside it. install / check / diff / update
// operate on that block exclusively. User content above and below
// the markers is preserved byte-for-byte.

var agentsCmd = &cobra.Command{
	Use:   "agents",
	Short: "Manage Mainline's lightweight agent guidance (AGENTS.md, CLAUDE.md, IDE stubs)",
	Long: `Manage Mainline's lightweight agent guidance for coding agents.

These commands install, check, diff, and update the Mainline-owned
guidance block inside AGENTS.md, CLAUDE.md, Cursor rules, Windsurf
rules, and Copilot instructions. The full workflow lives in the Mainline
agent skill; these repo-local files are project markers and bootstrap
reminders. Updates preserve user-edited content outside the block.

The guidance block is wrapped in version + checksum markers so:

  - Binary upgrades surface "update available" via mainline status.
  - Edits inside the block flag the file as locally modified —
    update refuses to overwrite without --theirs.
  - Every diff/update is auditable before it lands.

Subcommands:

  mainline agents install          # add lightweight guidance to AGENTS.md
  mainline agents check            # report state per file
  mainline agents diff             # show old vs new body
  mainline agents update           # update unmodified files; refuse modified
  mainline agents update --theirs  # overwrite even when locally modified

The five guidance surfaces are AGENTS.md, CLAUDE.md,
.cursor/rules/mainline.md, .windsurfrules, and
.github/copilot-instructions.md.`,
}

var agentsCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Report the agent guidance state for every target",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		res, err := svc.AgentsCheck()
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(res)
			return
		}
		fmt.Printf("Agent guidance template version: %d\n\n", res.CurrentVersion)
		for _, f := range res.Files {
			marker := "✓"
			tail := ""
			switch f.State {
			case engine.AgentsBlockStateNotInstalled:
				marker = "·"
				tail = "  (run `mainline agents install`)"
			case engine.AgentsBlockStateLegacy:
				marker = "↑"
				tail = "  (legacy format — run `mainline agents update` to migrate)"
			case engine.AgentsBlockStateUpdateAvailable:
				marker = "↑"
				tail = fmt.Sprintf("  v%d → v%d   (run `mainline agents diff`)", f.InstalledVersion, res.CurrentVersion)
			case engine.AgentsBlockStateLocallyModified:
				marker = "✎"
				tail = "  (locally modified — review before update)"
			}
			fmt.Printf("  %s  %-44s  %s%s\n", marker, f.Path, f.State, tail)
		}
	},
}

var agentsInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install Mainline's lightweight guidance into AGENTS.md and IDE stubs",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		res, err := svc.AgentsInstall()
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(res)
			return
		}
		fmt.Printf("Installed Mainline lightweight agent guidance (template v%d)\n\n", res.CurrentVersion)
		for _, c := range res.Files {
			renderAgentsChange(c)
		}
		fmt.Println("\nNext: `mainline agents check` to confirm; `mainline agents diff` to review future updates.")
	},
}

var (
	agentsUpdateTheirs bool
)

var agentsUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update Mainline's lightweight guidance to the binary's template version",
	Long: `Update Mainline's lightweight agent guidance in AGENTS.md and the IDE stubs.

Default policy:

  - in_sync          skipped, no work to do
  - update_available updated to the embedded template version
  - legacy           migrated to the versioned marker form
  - locally_modified REFUSED unless --theirs is passed

User content outside the markers is never touched. Re-running update
is a no-op when everything is already in sync.`,
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		res, err := svc.AgentsUpdate(engine.AgentsUpdateOptions{Theirs: agentsUpdateTheirs})
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(res)
			return
		}
		fmt.Printf("Agent guidance update (template v%d)\n\n", res.CurrentVersion)
		anyRefused := false
		for _, c := range res.Files {
			renderAgentsChange(c)
			if c.Action == "refused" {
				anyRefused = true
			}
		}
		if anyRefused {
			fmt.Println("\nSome files refused: agent guidance had local edits.")
			fmt.Println("Pass --theirs to overwrite, or hand-merge the changes after `mainline agents diff`.")
		}
	},
}

var agentsDiffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Show installed vs template body for every agent guidance target that would change",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		res, err := svc.AgentsDiff()
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(res)
			return
		}
		if len(res.Files) == 0 {
			fmt.Println("Agent guidance is in sync with the embedded template.")
			return
		}
		for _, f := range res.Files {
			fmt.Printf("=== %s   [%s]\n", f.Path, f.State)
			fmt.Println("--- installed body")
			fmt.Println(prefixLines(f.Old, "- "))
			fmt.Println("+++ template body")
			fmt.Println(prefixLines(f.New, "+ "))
			fmt.Println()
		}
	},
}

func renderAgentsChange(c engine.AgentsFileChange) {
	marker := "  "
	switch c.Action {
	case "installed":
		marker = "+ "
	case "updated":
		marker = "↑ "
	case "migrated":
		marker = "→ "
	case "skipped":
		marker = "= "
	case "refused":
		marker = "✗ "
	}
	line := fmt.Sprintf("%s%-44s  %s", marker, c.Path, c.Action)
	if c.Reason != "" {
		line += "  — " + c.Reason
	}
	fmt.Println(line)
}

// prefixLines tags every line of body with prefix. Used to render
// the old/new bodies in the simple diff output. Not a true diff —
// the user can pipe through `diff` if they want hunks; this is the
// minimal "see what each side says" view.
func prefixLines(body, prefix string) string {
	if body == "" {
		return prefix + "(empty)"
	}
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, prefix+l)
	}
	return strings.Join(out, "\n")
}

func init() {
	agentsUpdateCmd.Flags().BoolVar(&agentsUpdateTheirs, "theirs", false,
		"overwrite locally-modified agent guidance (edits inside the markers are lost)")

	agentsCmd.AddCommand(agentsInstallCmd, agentsCheckCmd, agentsDiffCmd, agentsUpdateCmd)
}
