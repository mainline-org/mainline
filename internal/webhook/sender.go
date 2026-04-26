package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/storage"
)

// Sender is the detached process's view of one envelope's delivery.
// Constructed in cli.runWebhookDispatch with a fresh Store + config
// read each invocation so subscription edits between enqueue and
// delivery take effect.
type Sender struct {
	Store         *storage.Store
	Subscriptions []domain.WebhookSubscription
	HTTP          *http.Client

	// MaxAttempts caps retries. With exp backoff starting at 1s,
	// MaxAttempts=5 means delivery has up to ~16s of trying before
	// the envelope lands in the .failed bucket.
	MaxAttempts int

	// BaseDelay is the first retry's wait. Doubles each attempt.
	// 1s is a polite default for transient gateway hiccups; pure
	// down-server failures will hit max attempts and tap out
	// quickly without holding the process for minutes.
	BaseDelay time.Duration
}

// DefaultSender is the production wiring used by the cli's hidden
// __webhook-dispatch command. Five attempts, 1s base, 5s per-request
// timeout (overridable per subscription). Each parameter is a
// product decision, not user-tunable; if a user needs different
// behaviour they should run their own queue consumer.
func DefaultSender(store *storage.Store, subs []domain.WebhookSubscription) *Sender {
	return &Sender{
		Store:         store,
		Subscriptions: subs,
		HTTP:          &http.Client{Timeout: 5 * time.Second},
		MaxAttempts:   5,
		BaseDelay:     1 * time.Second,
	}
}

// DispatchResult breaks a Dispatch call's outcome down to the level
// `webhook test` needs to produce a non-misleading report: total
// subscribers, how many matched the event filter, how many were
// skipped, how many delivery attempts failed. The detached sender
// in production ignores this struct (only checks err); the CLI uses
// it to print "delivered to 1 of 3 (2 skipped by filter)" instead of
// the historical "delivered_to: 3, ok: true" lie.
type DispatchResult struct {
	Total    int      `json:"total"`
	Matched  int      `json:"matched"`
	Skipped  int      `json:"skipped"`
	Failures []string `json:"failures,omitempty"`
}

// Dispatch is the detached process entrypoint. Reads the envelope by
// id, fans out to matching subscriptions, retries with exponential
// backoff, and renames the queue file to .failed.json on terminal
// failure (so `mainline webhook retry` can pick it up later).
//
// bypassFilter forces every subscription to receive the event even
// if its Events filter would have rejected it. The production caller
// (the detached __webhook-dispatch sender) always passes false; the
// CLI's `webhook test` command passes true so users can verify
// connectivity without temporarily editing their event filter.
//
// Returns a structured DispatchResult plus a multi-error string only
// for caller-side logging; the detached child's exit code is
// informational because nobody is watching it.
func (s *Sender) Dispatch(ctx context.Context, eventID string, bypassFilter bool) (*DispatchResult, error) {
	queuePath := filepath.Join(s.Store.WebhookQueueDir(), eventID+".json")
	data, err := os.ReadFile(queuePath)
	if err != nil {
		return nil, fmt.Errorf("read queue entry: %w", err)
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("decode envelope: %w", err)
	}

	res := &DispatchResult{Total: len(s.Subscriptions)}
	for _, sub := range s.Subscriptions {
		if !bypassFilter && !matchesEvents(sub.Events, env.Name) {
			res.Skipped++
			continue
		}
		res.Matched++
		if err := s.deliver(ctx, sub, &env); err != nil {
			res.Failures = append(res.Failures, fmt.Sprintf("%s: %v", subID(sub), err))
		}
	}

	if len(res.Failures) > 0 {
		// Persist the envelope with attempt count + last error so
		// `mainline webhook retry --id <event>` shows actionable
		// detail instead of just "failed".
		env.LastError = strings.Join(res.Failures, "; ")
		failedPath := filepath.Join(s.Store.WebhookQueueDir(), eventID+".failed.json")
		if buf, err := json.MarshalIndent(env, "", "  "); err == nil {
			os.WriteFile(failedPath, buf, 0o644)
		}
		os.Remove(queuePath)
		return res, fmt.Errorf("delivery failed: %s", env.LastError)
	}
	// All subscribers acked (or no matched subscribers) — the
	// envelope has done its job. The queue file is the only
	// persistent record; once removed there is nothing to retry.
	os.Remove(queuePath)
	return res, nil
}

// deliver POSTs one envelope to one subscription with retries. The
// subscription's TimeoutSeconds overrides the client's default
// timeout per call so a slow webhook doesn't drag the whole queue.
func (s *Sender) deliver(ctx context.Context, sub domain.WebhookSubscription, env *Envelope) error {
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	client := s.HTTP
	if sub.TimeoutSeconds > 0 {
		client = &http.Client{Timeout: time.Duration(sub.TimeoutSeconds) * time.Second}
	}

	var lastErr error
	for attempt := 1; attempt <= s.MaxAttempts; attempt++ {
		env.AttemptCount = attempt
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "mainline-webhook/1")
		req.Header.Set("X-Mainline-Event", env.Name)
		req.Header.Set("X-Mainline-Event-Id", env.EventID)
		req.Header.Set("X-Mainline-Delivery-Attempt", fmt.Sprintf("%d", attempt))
		req.Header.Set("X-Mainline-Dispatched-At", time.Now().UTC().Format(time.RFC3339))
		if secret := resolveSecret(sub.Secret); secret != "" {
			req.Header.Set("X-Mainline-Signature", signHMAC(body, secret))
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			// 2xx == success; everything else is a delivery
			// failure subject to retry. We don't try to be
			// clever about 4xx-vs-5xx — a 401/403 means the
			// subscriber's credentials are wrong, which is a
			// human problem we can't paper over with retries,
			// but waiting briefly before giving up is harmless.
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("http %d", resp.StatusCode)
		}

		if attempt == s.MaxAttempts {
			break
		}
		// Exponential backoff: BaseDelay * 2^(attempt-1). Cancel
		// fast if the parent context is gone (process being shut
		// down) — sleeping through SIGTERM would just delay the
		// inevitable.
		delay := s.BaseDelay * (1 << uint(attempt-1))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return lastErr
}

// signHMAC returns hex-encoded HMAC-SHA256 of body with key. The
// header value is bare hex (no `sha256=` prefix); subscribers
// implementing this scheme typically `hmac.compare(hex, expected)`
// directly.
func signHMAC(body []byte, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// resolveSecret expands `$ENV:NAME` references so the committed
// config.toml does not have to carry literal secrets. Plain strings
// pass through unchanged. The expansion is per-call so an env-var
// rotation between two events takes effect immediately.
func resolveSecret(s string) string {
	const prefix = "$ENV:"
	if strings.HasPrefix(s, prefix) {
		return os.Getenv(strings.TrimPrefix(s, prefix))
	}
	return s
}

// subID is a human-friendly subscription identifier for error logs.
// Prefers explicit ID, falls back to URL.
func subID(sub domain.WebhookSubscription) string {
	if sub.ID != "" {
		return sub.ID
	}
	return sub.URL
}
