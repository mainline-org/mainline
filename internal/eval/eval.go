// Package eval is the agent eval harness — Step 5 from
// docs_for_ai/mainline-spec-v0.2.md. It validates the product
// thesis: agents that read intent before code make fewer mistakes.
//
// The full validation needs an LLM runner that drives both a
// code-first and an intent-first agent across the same fixtures
// and compares outcomes. That runner is the next layer; this
// package ships the substrate it needs:
//
//   - A Fixture type that captures (sealed intents to seed +
//     task description + expected/forbidden outcomes) in code,
//     so fixtures live next to the harness and stay in sync.
//
//   - A precondition Scorer that answers "given the fixture's
//     task description, does mainline context return the
//     constraining intents + their anti_patterns?". This is a
//     deterministic test of retrieval, but it is *load-bearing*:
//     a constraint retrieval cannot surface is one no agent can
//     respect regardless of prompt.
//
//   - An embedded fixture catalog (Fixtures()) so callers don't
//     reach into filesystem state for a built-in test set.
//
// What this package is NOT:
//
//   - A LLM client. The agent runs are out of scope here; this
//     is the substrate they will use.
//   - A full repository simulator. Fixtures populate a synthetic
//     MainlineView, not a real seal/commit pipeline. The retrieval
//     layer doesn't read commits; the saving stays cheap.
package eval

import (
	"fmt"
	"strings"
	"time"

	"github.com/mainline-org/mainline/internal/domain"
)

// Fixture is one eval scenario — sealed intents to seed plus a task
// description plus the expected outcomes. Fixtures are pure data.
type Fixture struct {
	// Name is the canonical, kebab-cased identifier. Used by the CLI
	// (`mainline eval run <name>`) and as the seed-key for any per-
	// fixture scratch directories the LLM runner will create.
	Name string

	// Description is a one-line human-readable summary used in list
	// output. Keep it tight.
	Description string

	// Intents to seed into the scratch view. Order is significant:
	// later intents have a more recent SealedAt, which is what the
	// retrieval-status classifier reads to decide stale.
	Intents []SeedIntent

	// Task is the textual description an agent would receive (and
	// the seed for `mainline context --query "<task>"`).
	Task string

	// Expected pins what the harness looks for in the retrieval
	// result. At least one item per fixture; the scorer reports how
	// many of these are met.
	Expected []ExpectedItem

	// Forbidden lists anti-patterns the agent must NOT repeat. In v1
	// these are descriptive — the LLM-runner layer will compare
	// agent output against them.
	Forbidden []string
}

// SeedIntent is the data we synthesise into a scratch IntentView.
// Mirrors domain.IntentSummary + a few status fields the retrieval
// layer reads. Intentionally narrower than IntentView so fixtures
// stay readable.
type SeedIntent struct {
	ID           string
	Title        string
	Goal         string
	What         string
	Why          string
	Decisions    []domain.Decision
	Risks        []string
	AntiPatterns []domain.AntiPattern
	Files        []string
	Subsystems   []string

	// Lifecycle. Status is one of merged/proposed/superseded/
	// abandoned/reverted. SupersededBy + AbandonedReason are
	// status-conditional.
	Status       domain.IntentStatus
	SupersededBy string

	// AgeDays controls SealedAt = now - ageDays*24h. Lets a fixture
	// build a "stale" or "current" intent without hand-coding
	// timestamps.
	AgeDays int
}

// ExpectedItem is a single retrieval expectation. The scorer returns
// per-item pass/fail.
type ExpectedItem struct {
	// IntentID must appear in the RetrieveContext result.
	IntentID string

	// AntiPatternMatch is a substring; if non-empty, the scorer
	// looks for an AntiPattern on the matched intent whose `What`
	// contains this substring (case-insensitive).
	AntiPatternMatch string

	// MinStatus is the *minimum* retrieval status the matched
	// intent must carry. Empty means any. Useful for "this intent
	// must be marked stale", "this intent must be marked superseded".
	MinStatus string

	// Note is an optional explanation rendered in the score output.
	Note string
}

// ScoreResult is the per-fixture rollup the harness emits.
type ScoreResult struct {
	Fixture     string                `json:"fixture"`
	Description string                `json:"description"`
	Pass        bool                  `json:"pass"`
	Items       []ScoreItem           `json:"items"`
	Forbidden   []string              `json:"forbidden_summary,omitempty"`
}

// ScoreItem is one Expected check's outcome.
type ScoreItem struct {
	IntentID string `json:"intent_id"`
	Pass     bool   `json:"pass"`
	Reason   string `json:"reason"`
	Note     string `json:"note,omitempty"`
}

// nowFunc is overridable so tests can pin "now" deterministically.
// Default is time.Now.
var nowFunc = time.Now

// BuildView synthesises a *domain.MainlineView from a Fixture.
// Pure: no I/O. The result is what RetrieveContext would read after
// a sync; tests and the eval CLI both use this so the harness
// substrate doesn't depend on the seal/commit pipeline.
func BuildView(f Fixture) *domain.MainlineView {
	now := nowFunc()
	v := &domain.MainlineView{
		SchemaVersion: 1,
		MainBranch:    "main",
		MainHead:      "fixture-head",
		RebuiltAt:     now.UTC().Format(time.RFC3339),
	}
	for _, s := range f.Intents {
		ts := now.Add(-time.Duration(s.AgeDays) * 24 * time.Hour).UTC().Format(time.RFC3339)
		iv := domain.IntentView{
			IntentID:      s.ID,
			Status:        s.Status,
			ActorID:       "actor_eval",
			ActorName:     "eval-fixture",
			Thread:        "fixture/" + f.Name,
			GitBranch:     "fixture/" + f.Name,
			Goal:          s.Goal,
			SealedAt:      ts,
			ViewRebuiltAt: now.UTC().Format(time.RFC3339),
			StatusEvidence: domain.StatusEvidence{
				SupersededByIntent: s.SupersededBy,
			},
			Summary: &domain.IntentSummary{
				Title:        s.Title,
				What:         s.What,
				Why:          s.Why,
				Decisions:    s.Decisions,
				Risks:        s.Risks,
				AntiPatterns: s.AntiPatterns,
			},
			Fingerprint: &domain.SemanticFingerprint{
				FilesTouched: s.Files,
				Subsystems:   s.Subsystems,
			},
		}
		v.Intents = append(v.Intents, iv)
	}
	return v
}

// Retrieved is the minimum shape ScoreFixture needs from a
// retrieval result. We intentionally don't import engine here — eval
// is a thin substrate layer, and engine depends on storage which
// depends on git, which would make eval expensive to import from
// tests. The harness CLI maps engine.ContextRelevant → Retrieved.
type Retrieved struct {
	IntentID     string
	Status       string
	AntiPatterns []domain.AntiPattern
}

// ScoreFixture compares the fixture's Expected list against the
// retrieved results. Pure, no I/O.
//
// Scoring rules:
//
//   - Each Expected.IntentID must appear in `got` for that item to
//     pass.
//   - If Expected.AntiPatternMatch is set, the matched intent must
//     have at least one AntiPattern whose What contains the
//     substring (case-insensitive).
//   - If Expected.MinStatus is set, the matched intent's Status
//     must equal it.
//
// The fixture passes iff every Expected item passes. Forbidden is
// pass-through — the LLM runner will use it to score agent output
// in a later layer.
func ScoreFixture(f Fixture, got []Retrieved) ScoreResult {
	res := ScoreResult{
		Fixture:     f.Name,
		Description: f.Description,
		Pass:        true,
		Forbidden:   append([]string(nil), f.Forbidden...),
	}
	byID := map[string]Retrieved{}
	for _, r := range got {
		byID[r.IntentID] = r
	}
	for _, e := range f.Expected {
		item := ScoreItem{IntentID: e.IntentID, Note: e.Note}
		r, ok := byID[e.IntentID]
		if !ok {
			item.Pass = false
			item.Reason = fmt.Sprintf("intent %s not in retrieval result", e.IntentID)
			res.Items = append(res.Items, item)
			res.Pass = false
			continue
		}
		if e.MinStatus != "" && r.Status != e.MinStatus {
			item.Pass = false
			item.Reason = fmt.Sprintf("intent %s expected status=%s, got %s", e.IntentID, e.MinStatus, r.Status)
			res.Items = append(res.Items, item)
			res.Pass = false
			continue
		}
		if e.AntiPatternMatch != "" {
			match := strings.ToLower(e.AntiPatternMatch)
			seen := false
			for _, ap := range r.AntiPatterns {
				if strings.Contains(strings.ToLower(ap.What), match) {
					seen = true
					break
				}
			}
			if !seen {
				item.Pass = false
				item.Reason = fmt.Sprintf("intent %s has no anti_pattern matching %q", e.IntentID, e.AntiPatternMatch)
				res.Items = append(res.Items, item)
				res.Pass = false
				continue
			}
		}
		item.Pass = true
		item.Reason = "ok"
		res.Items = append(res.Items, item)
	}
	return res
}
