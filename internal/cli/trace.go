package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/engine"
)

var traceLimit int

var traceCmd = &cobra.Command{
	Use:   "trace <intent-id>",
	Short: "Show one intent's turn timeline (when each turn happened, how long elapsed)",
	Long: `Show the turn timeline for one intent.

  log    — list of intents (horizontal)
  show   — one intent's decisions / risks / fingerprint (vertical, summary)
  trace  — one intent's time series (vertical, timeline)

Use show to understand what the intent decided. Use trace to
understand how it unfolded over time.

Cross-actor intents (sealed by another actor whose drafts directory
lives on their machine) show start + terminal event only — per-append
turn detail is not available locally.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		opts := &engine.TraceOptions{Limit: traceLimit}
		result, err := svc.Trace(args[0], opts)
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(result)
			return
		}
		printTraceText(result)
	},
}

func init() {
	traceCmd.Flags().IntVar(&traceLimit, "limit", 0, "limit the timeline to the first N turns (0 = show all)")
}

// printTraceText renders the rc7-style human-readable trace. Intentionally
// flat — no box-drawing characters, no colour, no ASCII art. The
// information density carries the value, not the presentation.
func printTraceText(r *engine.TraceResult) {
	fmt.Printf("Intent: %s\n", r.IntentID)
	if r.Title != "" {
		fmt.Printf("Title: %s\n", r.Title)
	}
	statusLine := r.Status
	switch {
	case r.MergedMainCommit != "":
		statusLine += fmt.Sprintf(" (in commit %s)", short12(r.MergedMainCommit))
	case r.SupersededBy != "":
		statusLine += fmt.Sprintf(" (by %s)", r.SupersededBy)
	case r.StatusReason != "":
		statusLine += fmt.Sprintf(" (reason: %q)", r.StatusReason)
	}
	fmt.Printf("Status: %s\n", statusLine)
	if r.ActorName != "" {
		fmt.Printf("Author: %s\n", r.ActorName)
	}
	if r.Thread != "" {
		fmt.Printf("Thread: %s\n", r.Thread)
	}
	if r.BaseCommit != "" {
		fmt.Printf("Base:   %s\n", short12(r.BaseCommit))
	}

	fmt.Println()
	fmt.Println("Timeline:")
	if r.StartedAt != "" {
		fmt.Printf("  Started:  %s\n", r.StartedAt)
	}
	if r.SealedAt != "" {
		fmt.Printf("  Sealed:   %s\n", r.SealedAt)
	}
	if r.DurationSeconds > 0 {
		fmt.Printf("  Duration: %s\n", formatDuration(r.DurationSeconds))
	}
	fmt.Println()

	if r.Summary.LimitApplied {
		fmt.Printf("Turns: %d (showing first %d)\n", r.Summary.TotalTurns, len(r.Turns))
	} else {
		fmt.Printf("Turns: %d\n", r.Summary.TotalTurns)
	}
	fmt.Println(strings.Repeat("─", 60))
	for _, t := range r.Turns {
		printTurnLine(t)
	}
	fmt.Println(strings.Repeat("─", 60))

	// rc7 honest-signal note: only show when relevant.
	if r.Summary.AppendTurnsRecordedTogether {
		fmt.Println()
		fmt.Println("Note: append turns share timestamps, indicating they were")
		fmt.Println("recorded together rather than as live progress events.")
		fmt.Println("This is normal — turns serve as a sealing checklist,")
		fmt.Println("not a live activity log.")
	}
	if r.Summary.CrossActor {
		fmt.Println()
		fmt.Println("Note: this intent's drafts directory lives on another actor's")
		fmt.Println("machine. Per-append turn detail is not available locally;")
		fmt.Println("only the start (synthesised from the seal event) and the")
		fmt.Println("terminal lifecycle event are shown.")
	}

	// Files-touched, paths only — per the rc7 principle "v1 不要求 diff stats".
	if r.Summary.FilesTouchedCount > 0 {
		fmt.Println()
		fmt.Println("Files touched (from sealed event):")
		shown := r.Summary.FilesTouched
		const cap = 8
		if len(shown) > cap {
			shown = shown[:cap]
		}
		for _, f := range shown {
			fmt.Printf("  %s\n", f)
		}
		if r.Summary.FilesTouchedCount > len(shown) {
			fmt.Printf("  ... %d more file(s)\n", r.Summary.FilesTouchedCount-len(shown))
		}
	}

	if r.Status == "merged" || r.Status == "sealed_local" || r.Status == "proposed" {
		fmt.Println()
		fmt.Printf("Run `mainline show %s` for decisions / risks / fingerprint.\n", r.IntentID)
	}
}

func printTurnLine(t engine.TraceTurn) {
	timeShort := t.Timestamp
	if len(timeShort) >= 19 {
		// "2026-04-25T14:00:12Z" → "14:00:12"
		timeShort = timeShort[11:19]
	}
	delta := ""
	if t.ElapsedFromPreviousSec > 0 {
		delta = " (+" + formatDuration(t.ElapsedFromPreviousSec) + ")"
	}
	fmt.Printf("  #%d  %s  %s%s\n", t.Index, timeShort, t.Type, delta)
	if t.Description != "" {
		fmt.Printf("      %s\n", truncateLine(t.Description, 200))
	}
	// Surface the seal-time rollup inline so users don't need to
	// switch to `mainline show` to know how big the seal was.
	if t.Type == engine.TraceTurnSeal && len(t.Metadata) > 0 {
		fmt.Printf("      Sealed with: %v files touched, %v decisions, %v risks\n",
			t.Metadata["files_touched_count"],
			t.Metadata["decisions_count"],
			t.Metadata["risks_count"])
	}
	if t.Type == engine.TraceTurnAbandon && t.Metadata["reason"] != nil {
		fmt.Printf("      Reason: %v\n", t.Metadata["reason"])
	}
}

// formatDuration renders a seconds count as the smallest natural unit
// for the magnitude. Mirrors the AGENTS.md log output style.
func formatDuration(seconds int64) string {
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm%ds", seconds/60, seconds%60)
	default:
		hours := seconds / 3600
		mins := (seconds % 3600) / 60
		secs := seconds % 60
		return fmt.Sprintf("%dh%02dm%02ds", hours, mins, secs)
	}
}

func short12(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func truncateLine(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
