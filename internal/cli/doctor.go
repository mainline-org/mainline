package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/engine"
)

var doctorFix bool
var doctorStaleAfter time.Duration
var doctorSetup bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Inspect and repair local mainline state",
	Long: `Default mode: scans local drafts for orphans (referencing missing
git branches) and stale ones; --fix deletes orphans.

--setup mode: runs install / wiring sanity checks — verifies the git
remote refspec configuration, identity file, AGENTS.md, PR template,
and .gitignore. Combined with --fix, missing remote refspec entries
are rewired in place. Use this as the first step when 'mainline sync'
is not picking up team activity.`,
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		result, err := svc.Doctor(engine.DoctorOptions{
			Fix:        doctorFix,
			StaleAfter: doctorStaleAfter,
			Setup:      doctorSetup,
		})
		if err != nil {
			outputError(err)
			return
		}

		if jsonOutput {
			outputJSON(result)
			return
		}

		if result.Setup != nil {
			renderSetupReport(result.Setup, doctorFix)
			return
		}

		fmt.Printf("Checked local drafts: %d\n", result.CheckedDrafts)
		if len(result.OrphanDrafts) == 0 && len(result.StaleDrafts) == 0 {
			fmt.Println("No local draft issues found.")
			return
		}

		if len(result.OrphanDrafts) > 0 {
			fmt.Printf("Orphan drafts: %d\n", len(result.OrphanDrafts))
			for _, d := range result.OrphanDrafts {
				fmt.Printf("  %s [%s] %s (%s)\n", d.IntentID, d.Status, d.Goal, d.Reason)
			}
			if !doctorFix {
				fmt.Println("Run 'mainline doctor --fix' to delete orphan draft files.")
			}
		}

		if len(result.DeletedDrafts) > 0 {
			fmt.Printf("Deleted orphan drafts: %d\n", len(result.DeletedDrafts))
			for _, id := range result.DeletedDrafts {
				fmt.Printf("  %s\n", id)
			}
		}

		if len(result.StaleDrafts) > 0 {
			fmt.Printf("Stale drafts: %d\n", len(result.StaleDrafts))
			for _, d := range result.StaleDrafts {
				fmt.Printf("  %s [%s] %s (%s)\n", d.IntentID, d.Status, d.Goal, d.Reason)
			}
		}
	},
}

func renderSetupReport(r *engine.DoctorSetupReport, fixed bool) {
	check := func(ok bool, label string) {
		mark := "✗"
		if ok {
			mark = "✓"
		}
		fmt.Printf("  %s %s\n", mark, label)
	}
	fmt.Println("Setup check:")
	check(r.IdentityOK, fmt.Sprintf("identity present (%s)", r.IdentityActorID))
	check(r.AgentsMDOK, "AGENTS.md present at repo root")
	check(r.PRTemplateOK, ".github/PULL_REQUEST_TEMPLATE.md present")
	check(r.GitignoreOK, ".gitignore contains .ml-cache/")
	check(r.NotesDisplayRefOK, "git config notes.displayRef points at mainline")
	check(r.SSHMultiplexOK, "SSH ControlMaster configured (sync perf)")
	if r.HasRemote {
		check(r.NotesFetchOK, fmt.Sprintf("remote.%s.fetch covers refs/notes/mainline/*", r.RemoteName))
		check(r.NotesPushOK, fmt.Sprintf("remote.%s.push covers refs/notes/mainline/*", r.RemoteName))
		check(r.ActorFetchOK, fmt.Sprintf("remote.%s.fetch covers actor logs", r.RemoteName))
		check(r.ActorPushOK, fmt.Sprintf("remote.%s.push covers actor logs", r.RemoteName))
	} else {
		fmt.Printf("  ✗ no '%s' remote — cross-actor sync requires one\n", r.RemoteName)
	}

	if len(r.Fixed) > 0 {
		fmt.Printf("\nFixed %d refspec(s):\n", len(r.Fixed))
		for _, f := range r.Fixed {
			fmt.Printf("  + %s\n", f)
		}
	}

	if len(r.Issues) == 0 {
		fmt.Println("\nAll checks passed.")
	} else {
		fmt.Printf("\n%d issue(s) found:\n", len(r.Issues))
		for _, msg := range r.Issues {
			fmt.Printf("  - %s\n", msg)
		}
		if !fixed && r.HasRemote {
			fmt.Println("\nRun 'mainline doctor --setup --fix' to apply automatic fixes.")
		}
	}

	if len(r.Suggestions) > 0 {
		fmt.Printf("\n💡 Performance tip(s):\n")
		for _, msg := range r.Suggestions {
			fmt.Printf("  %s\n", msg)
		}
	}
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorFix, "fix", false, "delete orphan local draft files (default mode), or rewire refspecs (with --setup)")
	doctorCmd.Flags().DurationVar(&doctorStaleAfter, "stale-after", 24*time.Hour, "mark drafting intents stale after this duration")
	doctorCmd.Flags().BoolVar(&doctorSetup, "setup", false, "run install / wiring sanity checks (refspec, identity, AGENTS.md, PR template, .gitignore)")
}
