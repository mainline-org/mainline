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

  mainline hub export <dir>        # write site under <dir>
  mainline hub open                # open .mainline/hub/index.html

Hub v1 is local, read-only, and rebuildable from the synced view.
No server, no DB, no writes.`,
}

var hubExportCmd = &cobra.Command{
	Use:   "export <dir>",
	Short: "Export the local intent view as a static HTML site",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		res, err := hub.Export(svc.Store, hub.ExportOptions{OutputDir: args[0]})
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
	Short: "Build (if needed) and open the default Hub at .mainline/hub/",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		out := filepath.Join(svc.Git.RepoRoot, ".mainline", "hub")
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
