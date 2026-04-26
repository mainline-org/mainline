package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/webhook"
)

var (
	webhookAddURL     string
	webhookAddID      string
	webhookAddEvents  []string
	webhookAddSecret  string
	webhookAddTimeout int

	webhookRemoveID string
	webhookTestID   string
	webhookTestName string
	webhookRetryID  string
	webhookRetryAll bool
)

// webhookCmd is the parent of the webhook-management subtree. The
// subscription list lives in .mainline/config.toml under [[webhook]];
// this CLI is the recommended way to edit it (read-modify-write of
// TOML by hand is tedious and easy to break).
var webhookCmd = &cobra.Command{
	Use:   "webhook",
	Short: "Manage webhook subscriptions for domain-event fan-out",
	Long: `Mainline emits domain events (intent_started, turn_appended, intent_sealed,
sync_completed, conflict_detected, check_judged, plus hook lifecycle events
when agent hooks are installed) and can fan them out to HTTP endpoints
configured here.

Each subscription is one row of [[webhook]] in .mainline/config.toml. The
events filter is empty by default ("send everything"); narrow it for
production paging targets.`,
}

// webhookAddCmd appends a new subscription to the team config. We
// deliberately push the user toward stable IDs (auto-generated from
// 8 random hex when not provided) so subsequent test/remove/retry
// commands have something to address. The URL is required; everything
// else has reasonable defaults.
//
// Accepts the URL either as `--url <url>` or as the first positional
// argument so both shapes documented historically (`webhook add
// --url X` and `webhook add X`) keep working.
var webhookAddCmd = &cobra.Command{
	Use:          "add [url]",
	Short:        "Add a webhook subscription to .mainline/config.toml",
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, args []string) error {
		if webhookAddURL == "" && len(args) == 1 {
			webhookAddURL = args[0]
		}
		if webhookAddURL == "" {
			return fmt.Errorf("a destination URL is required (--url <url> or positional arg)")
		}
		svc, err := getService()
		if err != nil {
			return err
		}
		cfg, err := svc.Store.ReadTeamConfig()
		if err != nil {
			return err
		}
		id := webhookAddID
		if id == "" {
			id = newSubID()
		}
		for _, w := range cfg.Webhooks {
			if w.ID == id {
				return fmt.Errorf("subscription %s already exists; use --id or remove first", id)
			}
		}
		sub := domain.WebhookSubscription{
			ID:             id,
			URL:            webhookAddURL,
			Events:         webhookAddEvents,
			Secret:         webhookAddSecret,
			TimeoutSeconds: webhookAddTimeout,
		}
		cfg.Webhooks = append(cfg.Webhooks, sub)
		if err := svc.Store.WriteTeamConfig(cfg); err != nil {
			return err
		}
		if jsonOutput {
			outputJSON(sub)
		} else {
			fmt.Printf("Added webhook %s -> %s\n", sub.ID, sub.URL)
			if len(sub.Events) > 0 {
				fmt.Printf("  Events: %s\n", strings.Join(sub.Events, ", "))
			} else {
				fmt.Println("  Events: (all)")
			}
		}
		return nil
	},
}

// webhookListCmd prints the configured subscriptions. The Secret is
// elided so a screen-share / `mainline webhook list --json` does not
// leak HMAC keys. Users who need to inspect the actual value should
// open the config file directly.
var webhookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured webhook subscriptions",
	RunE: func(*cobra.Command, []string) error {
		svc, err := getService()
		if err != nil {
			return err
		}
		cfg, err := svc.Store.ReadTeamConfig()
		if err != nil {
			return err
		}
		out := make([]domain.WebhookSubscription, 0, len(cfg.Webhooks))
		for _, w := range cfg.Webhooks {
			redacted := w
			if redacted.Secret != "" {
				redacted.Secret = "<redacted>"
			}
			out = append(out, redacted)
		}
		if jsonOutput {
			outputJSON(out)
			return nil
		}
		if len(out) == 0 {
			fmt.Println("(no subscriptions)")
			return nil
		}
		for _, w := range out {
			ev := "(all)"
			if len(w.Events) > 0 {
				ev = strings.Join(w.Events, ",")
			}
			fmt.Printf("%-12s %s\n", w.ID, w.URL)
			fmt.Printf("    events=%s timeout=%ds secret=%s\n",
				ev, w.TimeoutSeconds, ifEmpty(w.Secret, "(none)"))
		}
		return nil
	},
}

var webhookRemoveCmd = &cobra.Command{
	Use:          "remove [id]",
	Short:        "Remove a webhook subscription by ID",
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, args []string) error {
		if webhookRemoveID == "" && len(args) == 1 {
			webhookRemoveID = args[0]
		}
		if webhookRemoveID == "" {
			return fmt.Errorf("a subscription id is required (--id <id> or positional arg)")
		}
		svc, err := getService()
		if err != nil {
			return err
		}
		cfg, err := svc.Store.ReadTeamConfig()
		if err != nil {
			return err
		}
		filtered := cfg.Webhooks[:0]
		removed := false
		for _, w := range cfg.Webhooks {
			if w.ID == webhookRemoveID {
				removed = true
				continue
			}
			filtered = append(filtered, w)
		}
		if !removed {
			return fmt.Errorf("subscription %s not found", webhookRemoveID)
		}
		cfg.Webhooks = filtered
		if err := svc.Store.WriteTeamConfig(cfg); err != nil {
			return err
		}
		if !jsonOutput {
			fmt.Printf("Removed webhook %s.\n", webhookRemoveID)
		}
		return nil
	},
}

// webhookTestCmd enqueues a synthetic event and immediately runs the
// detached sender INLINE so the user sees the result. The request
// body is the same envelope shape production uses; the only
// difference is the event Name (`webhook.test` by default).
//
// Crucial: this command bypasses each subscription's events filter.
// A `test` is a connectivity check. The subscriber's filter exists
// for production fan-out, where mismatched events should be silently
// ignored — but for `test` ignoring would be misleading ("you said
// delivered, where's my POST?"). We send to every selected sub
// regardless of filter and report matched vs skipped explicitly.
//
// Accepts subscription id either as `--id <id>` or first positional
// arg so `webhook test wh_xxx` works as documented.
var webhookTestCmd = &cobra.Command{
	Use:          "test [id]",
	Short:        "Send a synthetic test event to one or all subscriptions",
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, args []string) error {
		if webhookTestID == "" && len(args) == 1 {
			webhookTestID = args[0]
		}
		svc, err := getService()
		if err != nil {
			return err
		}
		cfg, err := svc.Store.ReadTeamConfig()
		if err != nil {
			return err
		}
		subs := cfg.Webhooks
		if webhookTestID != "" {
			filtered := subs[:0]
			for _, w := range subs {
				if w.ID == webhookTestID {
					filtered = append(filtered, w)
					break
				}
			}
			if len(filtered) == 0 {
				return fmt.Errorf("subscription %s not found", webhookTestID)
			}
			subs = filtered
		}
		if len(subs) == 0 {
			return fmt.Errorf("no subscriptions to test")
		}
		name := webhookTestName
		if name == "" {
			name = "webhook.test"
		}

		env := webhook.NewEnvelope(name, json.RawMessage(`{"test":true}`))
		env.Source = "engine"
		// Inline send rather than fork: a `webhook test` should give
		// the user immediate feedback. Use the same Sender so
		// timeouts / HMAC / retries match production.
		queueDir := svc.Store.WebhookQueueDir()
		if err := os.MkdirAll(queueDir, 0o755); err != nil {
			return err
		}
		path := filepath.Join(queueDir, env.EventID+".json")
		if buf, err := json.MarshalIndent(env, "", "  "); err == nil {
			os.WriteFile(path, buf, 0o644)
		}
		sender := webhook.DefaultSender(svc.Store, subs)
		// bypass filter: a test is a connectivity probe, not a
		// production fan-out. Subscribers' Events filter exists to
		// suppress real production noise, not to defeat the user's
		// own probe.
		res, dispatchErr := sender.Dispatch(context.Background(), env.EventID, true /*bypassFilter*/)
		out := map[string]any{
			"event_id":   env.EventID,
			"event_name": name,
			"matched":    0,
			"skipped":    0,
			"failures":   []string{},
			"ok":         false,
			"sub_count":  len(subs),
		}
		if res != nil {
			out["matched"] = res.Matched
			out["skipped"] = res.Skipped
			out["failures"] = res.Failures
		}
		if dispatchErr != nil {
			out["error"] = dispatchErr.Error()
			if jsonOutput {
				outputJSON(out)
			} else {
				fmt.Printf("✗ test delivery failed: %v\n", dispatchErr)
				fmt.Printf("  failed envelope: .ml-cache/webhook-queue/%s.failed.json\n", env.EventID)
			}
			return dispatchErr
		}
		out["ok"] = true
		if jsonOutput {
			outputJSON(out)
		} else {
			fmt.Printf("✓ delivered test event %q to %d subscription(s)\n", name, res.Matched)
		}
		return nil
	},
}

// webhookRetryCmd is the manual escape hatch for envelopes the
// detached sender failed on. We re-read the .failed.json file, drop
// it back to a regular .json filename, then run the same sender. On
// success the .json (and .failed.json) are gone; on failure a new
// .failed.json carries the latest error and attempt count.
//
// Accepts the event id either as `--id <id>` or first positional
// arg, matching the rest of the webhook subcommands.
var webhookRetryCmd = &cobra.Command{
	Use:          "retry [event-id]",
	Short:        "Retry a failed webhook delivery",
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, args []string) error {
		if webhookRetryID == "" && len(args) == 1 {
			webhookRetryID = args[0]
		}
		svc, err := getService()
		if err != nil {
			return err
		}
		cfg, err := svc.Store.ReadTeamConfig()
		if err != nil {
			return err
		}
		queueDir := svc.Store.WebhookQueueDir()

		// Build the work list. Exactly one of --id or --all must
		// be set; default-empty means "show what's stuck".
		var ids []string
		entries, _ := os.ReadDir(queueDir)
		for _, e := range entries {
			n := e.Name()
			if !strings.HasSuffix(n, ".failed.json") {
				continue
			}
			id := strings.TrimSuffix(n, ".failed.json")
			if webhookRetryID != "" && id != webhookRetryID {
				continue
			}
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			if webhookRetryID != "" {
				return fmt.Errorf("no failed delivery for id %s", webhookRetryID)
			}
			if !jsonOutput {
				fmt.Println("No failed deliveries.")
			}
			return nil
		}
		if !webhookRetryAll && webhookRetryID == "" {
			if !jsonOutput {
				fmt.Printf("Found %d failed delivery(ies). Re-run with --all or --id to retry.\n", len(ids))
				for _, id := range ids {
					fmt.Printf("  %s\n", id)
				}
			} else {
				outputJSON(map[string]any{"failed": ids})
			}
			return nil
		}

		sender := webhook.DefaultSender(svc.Store, cfg.Webhooks)
		type result struct {
			EventID string `json:"event_id"`
			OK      bool   `json:"ok"`
			Error   string `json:"error,omitempty"`
		}
		var out []result
		for _, id := range ids {
			failedPath := filepath.Join(queueDir, id+".failed.json")
			retryPath := filepath.Join(queueDir, id+".json")
			if err := os.Rename(failedPath, retryPath); err != nil {
				out = append(out, result{EventID: id, Error: err.Error()})
				continue
			}
			if _, err := sender.Dispatch(context.Background(), id, false); err != nil {
				out = append(out, result{EventID: id, Error: err.Error()})
				continue
			}
			out = append(out, result{EventID: id, OK: true})
		}
		if jsonOutput {
			outputJSON(out)
			return nil
		}
		for _, r := range out {
			if r.OK {
				fmt.Printf("✓ %s\n", r.EventID)
			} else {
				fmt.Printf("✗ %s: %s\n", r.EventID, r.Error)
			}
		}
		return nil
	},
}

// -----------------------------------------------------------
// hidden __webhook-dispatch
// -----------------------------------------------------------

// webhookDispatchCmd is the entry point for the detached sender forked
// by webhook.Bus. The leading double-underscore is a convention for
// "internal command, not part of the user surface" — it's hidden from
// `mainline --help` and not promoted in any doc.
var webhookDispatchCmd = &cobra.Command{
	Use:    "__webhook-dispatch <event-id>",
	Short:  "(internal) deliver one queued webhook envelope",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		svc, err := getService()
		if err != nil {
			return err
		}
		cfg, err := svc.Store.ReadTeamConfig()
		if err != nil {
			return err
		}
		sender := webhook.DefaultSender(svc.Store, cfg.Webhooks)
		// Long context — the sender enforces per-attempt timeouts
		// internally, so the outer context only matters for SIGTERM
		// during shutdown. Production fan-out always honours each
		// subscription's events filter (bypassFilter=false).
		_, err = sender.Dispatch(context.Background(), args[0], false)
		return err
	},
}

// -----------------------------------------------------------
// helpers
// -----------------------------------------------------------

// newSubID generates a stable subscription id (`wh_<8 hex>`). Used
// when the user does not pass --id to `webhook add`.
func newSubID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "wh_unknown"
	}
	return "wh_" + hex.EncodeToString(b[:])
}

func ifEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	if strings.HasPrefix(s, "$ENV:") {
		return s + " (env-resolved at delivery)"
	}
	return "<set>"
}

func init() {
	webhookAddCmd.Flags().StringVar(&webhookAddURL, "url", "", "destination URL (required)")
	webhookAddCmd.Flags().StringVar(&webhookAddID, "id", "", "subscription id (auto-generated when omitted)")
	webhookAddCmd.Flags().StringSliceVar(&webhookAddEvents, "events", nil, "comma-separated event filter (empty = all)")
	webhookAddCmd.Flags().StringVar(&webhookAddSecret, "secret", "", "HMAC secret (use $ENV:NAME to indirect through env)")
	webhookAddCmd.Flags().IntVar(&webhookAddTimeout, "timeout-seconds", 0, "per-request timeout (0 = sender default)")

	webhookRemoveCmd.Flags().StringVar(&webhookRemoveID, "id", "", "subscription id to remove")

	webhookTestCmd.Flags().StringVar(&webhookTestID, "id", "", "test only this subscription")
	webhookTestCmd.Flags().StringVar(&webhookTestName, "event", "webhook.test", "event name on the synthetic envelope")

	webhookRetryCmd.Flags().StringVar(&webhookRetryID, "id", "", "retry only this event id")
	webhookRetryCmd.Flags().BoolVar(&webhookRetryAll, "all", false, "retry every queued .failed.json")

	webhookCmd.AddCommand(webhookAddCmd, webhookListCmd, webhookRemoveCmd, webhookTestCmd, webhookRetryCmd)
}
