package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/hub"
)

// `mainline hub` is the human-reader complement to the agent-facing
// CLI: it exports the local synced intent view as a static HTML site
// and (optionally) opens it in the system browser.
//
// Hub v1 is deliberately read-only and local. v2 will replace the
// static export with a hosted ingest pipeline; the model layer (see
// internal/hub/model.go) is the contract we plan to keep.

var (
	hubExportOpen bool
)

var hubCmd = &cobra.Command{
	Use:   "hub",
	Short: "Browse the local intent view as a static HTML site",
	Long: `Mainline Hub is a read-only static site over the local
synced intent view. It validates whether a centralised, browsable
view of intent history pulls its weight for human readers; if it
does, Hub v2 becomes a hosted service.

Subcommands:

  mainline hub export [dir]        # write site (default: OS temp dir)
  mainline hub open                # build + open in the default browser

The default output dir is <os-temp>/mainline-hub/<repo-basename>.
This keeps the static site OUT of the repo (the prior default of
.mainline/hub polluted the config dir) while remaining cheap to
re-export and predictable across repos.

Hub v1 is local, read-only, and rebuildable from the synced view.
No server, no DB, no writes.`,
}

// defaultHubDir is the predictable per-repo location for hub output
// when the user runs `mainline hub open` or `mainline hub export`
// without an explicit path. We deliberately put it in os.TempDir()
// rather than under the repo so:
//
//   - the static site never enters git;
//   - multiple `hub` runs across repos don't clobber each other
//     (basename namespace);
//   - the OS reaps stale exports on its own schedule.
//
// Cross-platform: os.TempDir() resolves to /tmp on Linux, /var/tmp
// or /private/tmp on macOS, %TEMP% on Windows.
func defaultHubDir(repoRoot string) string {
	return filepath.Join(os.TempDir(), "mainline-hub", filepath.Base(repoRoot))
}

var hubExportCmd = &cobra.Command{
	Use:   "export [dir]",
	Short: "Export the local intent view as a static HTML site",
	Long: `Export the local intent view as a static HTML site.

If [dir] is omitted, the site is written to
<os-temp>/mainline-hub/<repo-basename>.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		out := defaultHubDir(svc.Git.RepoRoot)
		if len(args) == 1 {
			out = args[0]
		}
		res, err := hub.Export(svc.Store, hub.ExportOptions{OutputDir: out})
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(res)
		} else {
			fmt.Printf("Hub exported to %s\n", res.OutputDir)
			fmt.Printf("  intents: %d\n  files:   %d\n  actors:  %d\n  risks:   %d\n",
				res.IntentCount, res.FileCount, res.ActorCount, res.RiskCount)
			fmt.Printf("\nOpen %s in a browser, or run `mainline hub open`.\n", res.IndexPath)
		}
		if hubExportOpen {
			openInBrowser(res.IndexPath)
		}
	},
}

var hubOpenCmd = &cobra.Command{
	Use:   "open",
	Short: "Build (if needed) and open the default Hub in your browser",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		out := defaultHubDir(svc.Git.RepoRoot)
		res, err := hub.Export(svc.Store, hub.ExportOptions{OutputDir: out})
		if err != nil {
			outputError(err)
			return
		}
		if !jsonOutput {
			fmt.Printf("Hub at %s (%d intents)\n", res.OutputDir, res.IntentCount)
		}
		openInBrowser(res.IndexPath)
	},
}

// openInBrowser asks the OS to open the file. Best-effort: a missing
// `open` / `xdg-open` is not a Mainline failure; the user already
// has the path printed above.
func openInBrowser(path string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", path)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		c = exec.Command("xdg-open", path)
	}
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	_ = c.Start()
}

func init() {
	hubExportCmd.Flags().BoolVar(&hubExportOpen, "open", false,
		"open the generated index.html after export")
	hubCmd.AddCommand(hubExportCmd, hubOpenCmd)
}
