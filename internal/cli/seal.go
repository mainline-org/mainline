package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/engine"
)

var sealPrepare bool
var sealSubmit bool
var sealIntentID string
var sealOffline bool
var sealAllowDirty bool
var sealAllowStructuredSignals bool
var sealRefs []string

var sealCmd = &cobra.Command{
	Use:   "seal",
	Short: "Seal an intent (freeze code + generate summary)",
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}

		if sealAllowStructuredSignals {
			outputError(domain.NewRecoverableError(domain.ErrInvalidInput,
				"--allow-structured-signals is deprecated",
				"seal summary no longer accepts durable signal creation fields",
				"use `mainline risks add`, `mainline followups add`, or human-confirmed `mainline guard add` instead",
			))
			return
		}

		if sealPrepare {
			pkg, err := svc.SealPrepare(sealIntentID)
			if err != nil {
				outputError(err)
				return
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(pkg)
			return
		}

		if sealSubmit {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				outputError(fmt.Errorf("read stdin: %w", err))
				return
			}

			// Inject references from --ref flags and environment auto-discovery.
			refs := collectSealReferences()
			if len(refs) > 0 {
				var sr domain.SealResult
				if err := json.Unmarshal(data, &sr); err == nil {
					sr.References = append(sr.References, refs...)
					data, _ = json.Marshal(sr)
				}
			}

			result, err := svc.SealSubmitWithOptions(json.RawMessage(data),
				&engine.SealSubmitOptions{Offline: sealOffline, AllowDirty: sealAllowDirty})
			if err != nil {
				outputError(err)
				return
			}
			if jsonOutput {
				outputJSON(result)
			} else {
				fmt.Printf("Intent sealed: %s\n", result.IntentID)
				fmt.Printf("  Status:     %s\n", result.Status)
				fmt.Printf("  Published:  %v\n", result.Published)
				fmt.Printf("  Code commit: %s\n", result.CodeCommit)
				fmt.Printf("  Event ID:   %s\n", result.EventID)
				fmt.Printf("  Hash:       %s\n", result.Hash)
				if result.SyncRan && result.SyncError != "" {
					fmt.Printf("  Sync warn:  %s\n", result.SyncError)
				}
				if result.Warning != "" {
					fmt.Printf("  Warning:    %s\n", result.Warning)
				}
				// SealSubmit now blocks deterministic lint errors
				// before mutation. Inline lint here only surfaces the
				// remaining warning/info issues that did not block.
				renderSealLintHint(result.LintIssues, result.IntentID)
				if len(result.Conflicts) > 0 {
					fmt.Printf("\n⚠ %d potential conflict(s) detected (intent is sealed; review when convenient):\n",
						len(result.Conflicts))
					for _, c := range result.Conflicts {
						fmt.Printf("  ↔ %s  score=%.2f confidence=%s (%s)\n",
							c.RemoteIntent, c.OverlapScore, c.Confidence, c.RemoteStatus)
						fmt.Printf("    %s\n", c.Reason)
					}
					fmt.Println("\nIf any of these look like a real semantic conflict, run:")
					fmt.Printf("  mainline check --prepare --intent %s\n", result.IntentID)
				}
				renderSealNextSteps(result)
			}
			return
		}

		// Default: show help
		_ = cmd.Help()
	},
}

// renderSealLintHint surfaces the non-blocking lint issues that were
// already computed during SealSubmit's pre-mutation gate.
func renderSealLintHint(issues []engine.LintIssue, intentID string) {
	errs, warns := 0, 0
	for _, iss := range issues {
		switch iss.Severity {
		case "error":
			errs++
		case "warning":
			warns++
		}
	}
	if errs == 0 && warns == 0 {
		return
	}
	fmt.Println()
	switch {
	case errs > 0 && warns > 0:
		fmt.Printf("⚠ Lint: %d error(s), %d warning(s) — `mainline lint %s`\n", errs, warns, intentID)
	case errs > 0:
		fmt.Printf("⚠ Lint: %d error(s) — `mainline lint %s`\n", errs, intentID)
	default:
		fmt.Printf("· Lint: %d warning(s) — `mainline lint %s`\n", warns, intentID)
	}
}

// renderSealNextSteps drops the "what do I do now?" breadcrumb a
// first-time user needs after a successful submit. It must not
// prescribe a Git hosting workflow: Mainline records intent, but it
// does not require users to push, open PRs, or merge through GitHub.
func renderSealNextSteps(result *engine.SealSubmitResult) {
	fmt.Println()
	fmt.Println("View intent:")
	fmt.Printf("  mainline show %s\n", result.IntentID)
	fmt.Println("  mainline hub open")
	fmt.Println()
	fmt.Println("Next:")
	fmt.Printf("  · If your workflow opens or updates a PR: `mainline pr-description --intent %s > .ml-cache/pr-description.md`\n", result.IntentID)
	fmt.Println("    Use that file as the PR body so GitHub does not need a duplicate Mainline comment.")
	fmt.Println("  · Otherwise continue with your normal review, release, or local workflow.")
	fmt.Println("  · After the change lands, the next `mainline sync` auto-pins the merge commit.")
	if !result.Published {
		fmt.Println("  · Status: sealed_local — the actor log was not pushed (no remote, or sync skipped).")
		fmt.Println("    Run `mainline sync` once a remote is configured to publish.")
	}
}

func init() {
	sealCmd.Flags().BoolVar(&sealPrepare, "prepare", false, "output seal prepare package (JSON)")
	sealCmd.Flags().BoolVar(&sealSubmit, "submit", false, "submit seal result from stdin (JSON)")
	sealCmd.Flags().StringVar(&sealIntentID, "intent", "", "intent ID (default: active intent on current branch)")
	sealCmd.Flags().BoolVar(&sealOffline, "offline", false, "skip the auto sync+check inside --submit (sealed_local only)")
	sealCmd.Flags().BoolVar(&sealAllowDirty, "allow-dirty", false, "submit even when worktree is dirty/untracked (records dirty status in audit trail)")
	sealCmd.Flags().BoolVar(&sealAllowStructuredSignals, "allow-structured-signals", false, "deprecated: seal no longer creates durable signals; use explicit signal commands")
	sealCmd.Flags().StringArrayVar(&sealRefs, "ref", nil, "attach reference (format: kind:client:ref, e.g. session:claude-code:sess_abc123)")
}

// collectSealReferences builds references from --ref flags and env auto-discovery.
func collectSealReferences() []domain.Reference {
	var refs []domain.Reference

	// From --ref flags: "kind:client:ref" or "kind::ref"
	for _, r := range sealRefs {
		ref := parseRefFlag(r)
		if ref.Kind != "" && (ref.Ref != "" || ref.URL != "") {
			refs = append(refs, ref)
		}
	}

	// Auto-discover from environment variables (only attach if real).
	refs = append(refs, discoverSessionRefs()...)
	return refs
}

// parseRefFlag parses "kind:client:ref" into a Reference.
func parseRefFlag(s string) domain.Reference {
	parts := strings.SplitN(s, ":", 3)
	switch len(parts) {
	case 3:
		return domain.Reference{
			Kind:   parts[0],
			Client: parts[1],
			Ref:    parts[2],
			Label:  autoLabel(parts[0], parts[1]),
		}
	case 2:
		return domain.Reference{Kind: parts[0], Ref: parts[1], Label: autoLabel(parts[0], "")}
	default:
		return domain.Reference{}
	}
}

// discoverSessionRefs checks well-known environment variables for session IDs.
func discoverSessionRefs() []domain.Reference {
	var refs []domain.Reference
	envMap := map[string]string{
		"CLAUDE_SESSION_ID":    "claude-code",
		"CODEX_SESSION_ID":     "codex",
		"CURSOR_SESSION_ID":    "cursor",
		"COPILOT_SESSION_ID":   "copilot",
		"MAINLINE_SESSION_REF": "",
	}
	for envVar, client := range envMap {
		val := os.Getenv(envVar)
		if val == "" {
			continue
		}
		ref := domain.Reference{
			Kind:   "session",
			Client: client,
			Ref:    val,
			Label:  autoLabel("session", client),
		}
		// MAINLINE_SESSION_REF can be a URL
		if envVar == "MAINLINE_SESSION_REF" && (strings.HasPrefix(val, "http") || strings.HasPrefix(val, "file://")) {
			ref.URL = val
			ref.Ref = ""
			ref.Client = os.Getenv("MAINLINE_SESSION_CLIENT")
			ref.Label = autoLabel("session", ref.Client)
		}
		refs = append(refs, ref)
	}
	return refs
}

func autoLabel(kind, client string) string {
	switch kind {
	case "session":
		if client != "" {
			// Convert "claude-code" → "Claude Code session"
			parts := strings.Split(client, "-")
			for i, p := range parts {
				if len(p) > 0 {
					parts[i] = strings.ToUpper(p[:1]) + p[1:]
				}
			}
			return strings.Join(parts, " ") + " session"
		}
		return "Agent session"
	case "issue":
		return "Issue reference"
	case "pr":
		return "Pull request"
	case "doc":
		return "Document"
	case "ci":
		return "CI run"
	default:
		return "Reference"
	}
}
