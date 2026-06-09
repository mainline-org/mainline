package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/engine"
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
	hubExportOpen                bool
	hubExternalContributionsPath string
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
<os-temp>/mainline-hub/<repo-basename>.

Use --external-contributions <file.json> to include imported or
inferred fork PR contribution records. Hub displays those records
with provenance/trust labels and does not treat them as author-sealed
Mainline intents.`,
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
		opts := buildHubExportOptions(svc, out)
		if err := addExternalContributionsToHubOptions(&opts); err != nil {
			outputError(err)
			return
		}
		res, err := hub.Export(svc.Store, opts)
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(res)
		} else {
			fmt.Printf("Hub exported to %s\n", res.OutputDir)
			fmt.Printf("  intents:        %d\n  local drafts:   %d\n  sibling drafts: %d\n  files:          %d\n  actors:         %d\n  constraints:    %d\n",
				res.IntentCount, res.OpenCount, res.SiblingDraftCount, res.FileCount, res.ActorCount, res.RiskCount)
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
	Long: `Build the default Hub export and open it in your browser.

Use --external-contributions <file.json> to include imported or
inferred fork PR contribution records in the opened Hub.`,
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		out := defaultHubDir(svc.Git.RepoRoot)
		opts := buildHubExportOptions(svc, out)
		if err := addExternalContributionsToHubOptions(&opts); err != nil {
			outputError(err)
			return
		}
		res, err := hub.Export(svc.Store, opts)
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

func buildHubExportOptions(svc *engine.Service, out string) hub.ExportOptions {
	covRows, covWin := buildHubCoverageInput(svc)
	branch, _ := svc.Git.CurrentBranch()
	lastSync, _ := svc.GetLastSyncForCLI()
	source := hub.HubSource{
		RepoPath:                         svc.Git.RepoRoot,
		Branch:                           branch,
		CurrentWorktreeDraftsDir:         svc.StoreDraftsDirForCLI(),
		IncludesSiblingWorktreeDraftList: true,
	}
	if lastSync != nil {
		source.LastSyncAt = lastSync.At
	}
	return hub.ExportOptions{
		OutputDir:      out,
		CoverageRows:   covRows,
		CoverageWindow: covWin,
		Source:         source,
		SiblingDrafts:  hubSiblingDrafts(svc.SiblingDraftsForCLI()),
	}
}

func addExternalContributionsToHubOptions(opts *hub.ExportOptions) error {
	if hubExternalContributionsPath == "" {
		return nil
	}
	contribs, err := readExternalContributionsFile(hubExternalContributionsPath)
	if err != nil {
		return err
	}
	opts.ExternalContributions = contribs
	return nil
}

func hubSiblingDrafts(in []engine.WorktreeDraft) []hub.HubWorktreeDraft {
	out := make([]hub.HubWorktreeDraft, 0, len(in))
	for _, d := range in {
		out = append(out, hub.HubWorktreeDraft{
			ID:             d.IntentID,
			Goal:           d.Goal,
			Status:         d.Status,
			GitBranch:      d.GitBranch,
			Thread:         d.Thread,
			WorktreePath:   d.WorktreePath,
			DraftPath:      d.DraftPath,
			TurnCount:      d.TurnCount,
			LastModifiedAt: d.LastModifiedAt,
		})
	}
	return out
}

func readExternalContributionsFile(path string) ([]hub.HubExternalContribution, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read external contributions: %w", err)
	}
	var list []hub.HubExternalContribution
	if err := json.Unmarshal(data, &list); err != nil {
		var wrapped struct {
			ExternalContributions []hub.HubExternalContribution `json:"external_contributions"`
		}
		if err2 := json.Unmarshal(data, &wrapped); err2 != nil {
			return nil, fmt.Errorf("parse external contributions: %w", err)
		}
		list = wrapped.ExternalContributions
	}
	for i := range list {
		list[i].AuthorSealed = false
		list[i].NotAuthorSealed = true
		list[i].Verified = false
		if strings.TrimSpace(list[i].Provenance) == "" {
			list[i].Provenance = "github_pr_imported"
		}
		if strings.TrimSpace(list[i].Source) == "" {
			list[i].Source = "github"
		}
	}
	return list, nil
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

// buildHubCoverageInput pulls the engine's CoverageWindow output and
// flattens it into hub.CoverageInputCommit rows. Best-effort: any
// error returns (nil, 0) and Hub falls back to the partial-data
// rendering — coverage is a nice-to-have for the dashboard, never a
// blocker. HighRisk is set when an uncovered commit's subject
// suggests a high-impact area (security/auth/payment/etc).
func buildHubCoverageInput(svc *engine.Service) ([]hub.CoverageInputCommit, int) {
	view, _ := svc.Store.ReadMainlineView()
	cfg, _ := svc.GetTeamConfigForCLI()
	if view == nil || cfg == nil {
		return nil, 0
	}
	cov, err := svc.CoverageWindow(engine.CoverageWindowSize, view, cfg)
	if err != nil {
		return nil, 0
	}
	risky := map[string]bool{}
	for i := range view.Intents {
		iv := &view.Intents[i]
		if iv.Summary != nil && len(iv.Summary.Risks) > 0 {
			risky[iv.IntentID] = true
		}
	}
	out := make([]hub.CoverageInputCommit, 0, len(cov))
	for _, c := range cov {
		row := hub.CoverageInputCommit{
			Commit:      c.Commit,
			Subject:     c.Subject,
			Author:      c.Author,
			CommittedAt: c.CommittedAt,
			State:       string(c.State),
			SkipReason:  c.SkipReason,
		}
		// HighRisk applies to uncovered commits — a covered commit's
		// constraints live on its intent page. We mark uncovered as
		// high-impact when its commit subject text suggests sensitive
		// areas (security/auth/payment/etc); cheap heuristic.
		if c.State == engine.CoverageUncovered {
			row.HighRisk = isLikelyHighRisk(c.Subject)
		}
		out = append(out, row)
	}
	return out, engine.CoverageWindowSize
}

// isLikelyHighRisk is a conservative subject-line scanner for
// uncovered commits that probably need an intent the most. False
// positives are cheap (the page just shows the commit with a flag).
//
// The flag is exposed in the UI as "possibly high-impact" rather than
// a definitive verdict — keyword match is a weak signal. The
// keywords cover both English and Chinese commit subjects since the
// repo is dogfooded across both languages.
func isLikelyHighRisk(subject string) bool {
	englishKeywords := []string{"security", "auth", "payment", "credential",
		"secret", "vuln", "permission", "migrat", "schema",
		"production", "rollback", "hotfix", "incident", "outage"}
	lower := strings.ToLower(subject)
	for _, k := range englishKeywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	// Chinese keywords: scan against the original (case-insensitive
	// is a no-op for CJK). Curated to mirror the English set —
	// authentication/authorisation, payment/billing, migration,
	// database/schema, security, rollback/downgrade, deletion of
	// data, and large refactors. The list stays short on purpose;
	// over-broad keywords like 改 / 修 would flag every commit.
	chineseKeywords := []string{
		"认证", "鉴权", "权限", "登录",
		"账单", "计费", "支付",
		"迁移", "数据库", "数据模型", "schema",
		"安全", "漏洞", "凭据", "凭证", "密钥", "敏感",
		"回滚", "降级", "兼容", "热修", "热修复",
		"删除", "重构", "数据丢失",
	}
	for _, k := range chineseKeywords {
		if strings.Contains(subject, k) {
			return true
		}
	}
	return false
}

func init() {
	hubExportCmd.Flags().BoolVar(&hubExportOpen, "open", false,
		"open the generated index.html after export")
	hubExportCmd.Flags().StringVar(&hubExternalContributionsPath, "external-contributions", "",
		"JSON file of imported/inferred external PR contributions to show in Hub")
	hubOpenCmd.Flags().StringVar(&hubExternalContributionsPath, "external-contributions", "",
		"JSON file of imported/inferred external PR contributions to show in Hub")
	hubCmd.AddCommand(hubExportCmd, hubOpenCmd)
}
