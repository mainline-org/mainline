package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/engine"
)

var migrateNotesCommitMap string
var migrateNotesInfer bool
var migrateNotesDryRun bool
var migrateNotesWrite bool
var migrateNotesPush bool

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run explicit Mainline metadata migrations",
}

var migrateNotesCmd = &cobra.Command{
	Use:   "notes",
	Short: "Recover mainline git notes after history rewrites",
	Long: `Recover refs/notes/mainline/intents after history rewrites or force-pushes.

Default mode is a dry run. It reports safe migrations, review-required
candidates, and unresolved notes. --write applies only safe migrations
locally. --push also pushes the notes ref with --force-with-lease.`,
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		if migrateNotesDryRun && (migrateNotesWrite || migrateNotesPush) {
			outputError(fmt.Errorf("--dry-run cannot be combined with --write or --push"))
			return
		}
		result, err := svc.MigrateNotes(engine.NotesMigrationOptions{
			CommitMapPath: migrateNotesCommitMap,
			Infer:         migrateNotesInfer,
			Write:         migrateNotesWrite || migrateNotesPush,
			Push:          migrateNotesPush,
		})
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(result)
			return
		}
		renderNotesMigrationResult(result)
	},
}

func renderNotesMigrationResult(r *engine.NotesMigrationResult) {
	if r == nil || r.Plan == nil {
		fmt.Println("No migration plan produced.")
		return
	}
	renderNotesMigrationPlan(r.Plan)
	if !r.Wrote {
		fmt.Println("\nDry run only. Add --write to apply safe migrations locally.")
		return
	}
	fmt.Println("\nApplied safe migrations locally.")
	if r.OldLocalNotesRef != "" || r.NewLocalNotesRef != "" {
		fmt.Printf("  notes: %s -> %s\n", shortHash(r.OldLocalNotesRef), shortHash(r.NewLocalNotesRef))
	}
	if r.Pushed {
		fmt.Printf("Pushed notes to %s with force-with-lease.\n", r.RemoteName)
	} else {
		fmt.Println("Not pushed. Add --push to update the remote notes ref.")
	}
}

func renderNotesMigrationPlan(plan *engine.NotesMigrationPlan) {
	fmt.Println("\nNotes migration plan:")
	fmt.Printf("  mode:            %s\n", plan.Mode)
	if plan.MainHead != "" {
		fmt.Printf("  main:            %s @ %s\n", plan.MainRef, shortHash(plan.MainHead))
	}
	if plan.NotesRef != "" {
		fmt.Printf("  notes:           %s\n", shortHash(plan.NotesRef))
	}
	fmt.Printf("  safe:            %d\n", len(plan.SafeMigrations))
	fmt.Printf("  review required: %d\n", len(plan.ReviewRequired))
	fmt.Printf("  unresolved:      %d\n", len(plan.Unresolved))

	for _, m := range plan.SafeMigrations {
		fmt.Printf("  + %s -> %s  %s %s\n",
			shortHash(m.OldCommit), shortHash(m.NewCommit), m.Strategy, m.Confidence)
		if len(m.IntentIDs) > 0 {
			fmt.Printf("    intents: %v\n", m.IntentIDs)
		}
	}
	for _, item := range plan.ReviewRequired {
		fmt.Printf("  ? %s  %s (%s)\n", shortHash(item.OldCommit), item.Strategy, item.Reason)
		for _, c := range item.Candidates {
			fmt.Printf("    candidate: %s\n", shortHash(c))
		}
	}
	for _, item := range plan.Unresolved {
		fmt.Printf("  ! %s  %s\n", shortHash(item.OldCommit), item.Reason)
	}
}

func init() {
	migrateNotesCmd.Flags().StringVar(&migrateNotesCommitMap, "commit-map", "", "git-filter-repo commit-map to migrate notes exactly")
	migrateNotesCmd.Flags().BoolVar(&migrateNotesInfer, "infer", false, "infer migrations by tree hash and patch-id")
	migrateNotesCmd.Flags().BoolVar(&migrateNotesDryRun, "dry-run", false, "preview the migration plan without writing (default)")
	migrateNotesCmd.Flags().BoolVar(&migrateNotesWrite, "write", false, "apply safe migrations locally")
	migrateNotesCmd.Flags().BoolVar(&migrateNotesPush, "push", false, "apply safe migrations and push notes with force-with-lease")
	migrateCmd.AddCommand(migrateNotesCmd)
}
