// Package eval is the agent eval harness. It validates the product thesis:
// agents that read intent before code make fewer mistakes.
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
//     constraining intents + explicit constraints?". This is a
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

	// Task is the textual description an agent would receive. It is
	// used as query text for the synthetic context request.
	Task string

	// TaskFiles are the files the synthetic task is expected to touch.
	// When present, the harness uses file-targeted context retrieval
	// with Task as extra query text so explicit inherited constraints
	// can surface through the same file-overlap path agents use.
	TaskFiles []string

	// Expected pins what the harness looks for in the retrieval
	// result. At least one item per fixture; the scorer reports how
	// many of these are met.
	Expected []ExpectedItem

	// Forbidden lists constraints the agent must NOT violate. In v1
	// these are descriptive — the LLM-runner layer will compare
	// agent output against them.
	Forbidden []string
}

// SeedIntent is the data we synthesise into a scratch IntentView.
// Mirrors domain.IntentSummary + a few status fields the retrieval
// layer reads. Intentionally narrower than IntentView so fixtures
// stay readable.
type SeedIntent struct {
	ID                string
	Title             string
	Goal              string
	What              string
	Why               string
	Decisions         []domain.Decision
	Risks             []string
	AntiPatterns      []domain.AntiPattern
	Constraints       []SeedConstraint
	ExplicitRisks     []SeedRisk
	ExplicitFollowups []SeedFollowup
	Files             []string
	Subsystems        []string

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

// SeedConstraint is a human-promoted explicit constraint associated
// with a SeedIntent. BuildView fills SourceIntent, IDs, provenance,
// and timestamps so fixtures stay readable.
type SeedConstraint struct {
	ID         string
	What       string
	Why        string
	Severity   string
	Files      []string
	Source     string
	SourceNote string
}

// SeedRisk is an explicit review-facing risk associated with a
// SeedIntent.
type SeedRisk struct {
	ID        string
	Text      string
	Statement domain.RiskStatement
	Files     []string
	Source    string
}

// SeedFollowup is an explicit deferred work item associated with a
// SeedIntent.
type SeedFollowup struct {
	ID        string
	Text      string
	Statement domain.FollowupStatement
	Files     []string
	Source    string
}

type SignalKind string

const (
	SignalConstraint SignalKind = "constraint"
	SignalRisk       SignalKind = "risk"
	SignalFollowup   SignalKind = "followup"
)

// ExpectedSignal is a single explicit signal expectation attached to
// an expected intent.
type ExpectedSignal struct {
	// Kind is one of "constraint", "risk", or "followup".
	Kind SignalKind

	// Match is a case-insensitive substring matched against the
	// explicit signal's agent-facing text.
	Match string
}

// ExpectedItem is a single retrieval expectation. The scorer returns
// per-item pass/fail.
type ExpectedItem struct {
	// IntentID must appear in the RetrieveContext result.
	IntentID string

	// Signal optionally requires an explicit signal on the matched
	// intent. Legacy summary.anti_patterns are deliberately not active
	// eval expectations.
	Signal ExpectedSignal

	// MinStatus is the *minimum* retrieval status the matched
	// intent must carry. Empty means any. Useful for "this intent
	// must be marked stale", "this intent must be marked superseded".
	MinStatus string

	// Note is an optional explanation rendered in the score output.
	Note string
}

// ScoreResult is the per-fixture rollup the harness emits.
type ScoreResult struct {
	Fixture     string      `json:"fixture"`
	Description string      `json:"description"`
	Pass        bool        `json:"pass"`
	Items       []ScoreItem `json:"items"`
	Forbidden   []string    `json:"forbidden_summary,omitempty"`
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
		appendExplicitSignals(v, s, ts)
	}
	return v
}

func appendExplicitSignals(v *domain.MainlineView, s SeedIntent, ts string) {
	for i, c := range s.Constraints {
		id := c.ID
		if id == "" {
			id = fmt.Sprintf("guard_eval_%s_%d", s.ID, i)
		}
		severity := c.Severity
		if severity == "" {
			severity = "high"
		}
		source := c.Source
		if source == "" {
			source = domain.SignalSourceExplicitUser
		}
		files := c.Files
		if len(files) == 0 {
			files = s.Files
		}
		v.Constraints = append(v.Constraints, domain.Constraint{
			ID:           id,
			What:         c.What,
			Why:          c.Why,
			Severity:     severity,
			Files:        append([]string(nil), files...),
			SourceIntent: s.ID,
			OpenedAt:     ts,
			OpenedBy:     "actor_eval",
			Source:       source,
			SourceNote:   c.SourceNote,
		})
	}
	for i, r := range s.ExplicitRisks {
		id := r.ID
		if id == "" {
			id = fmt.Sprintf("risk_eval_%s_%d", s.ID, i)
		}
		source := r.Source
		if source == "" {
			source = domain.SignalSourceCommand
		}
		files := r.Files
		if len(files) == 0 {
			files = s.Files
		}
		statement := r.Statement
		v.Risks = append(v.Risks, domain.Risk{
			ID:           id,
			Text:         r.Text,
			Statement:    &statement,
			Status:       "open",
			SourceIntent: s.ID,
			Files:        append([]string(nil), files...),
			OpenedBy:     "actor_eval",
			OpenedAt:     ts,
			Source:       source,
		})
	}
	for i, f := range s.ExplicitFollowups {
		id := f.ID
		if id == "" {
			id = fmt.Sprintf("followup_eval_%s_%d", s.ID, i)
		}
		source := f.Source
		if source == "" {
			source = f.Statement.Source
		}
		if source == "" {
			source = domain.SignalSourceExplicitDefer
		}
		files := f.Files
		if len(files) == 0 {
			files = s.Files
		}
		statement := f.Statement
		v.Followups = append(v.Followups, domain.Followup{
			ID:           id,
			Text:         f.Text,
			Statement:    &statement,
			Status:       "open",
			SourceIntent: s.ID,
			Files:        append([]string(nil), files...),
			OpenedBy:     "actor_eval",
			OpenedAt:     ts,
			Source:       source,
		})
	}
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
	Constraints  []domain.InheritedConstraint
	Risks        []domain.Risk
	Followups    []domain.Followup
}

// ScoreFixture compares the fixture's Expected list against the
// retrieved results. Pure, no I/O.
//
// Scoring rules:
//
//   - Each Expected.IntentID must appear in `got` for that item to
//     pass.
//   - If Expected.Signal is set, the matched intent must have an
//     explicit signal of that kind whose agent-facing text contains
//     the substring (case-insensitive).
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
		if e.Signal.Kind != "" || e.Signal.Match != "" {
			seen, reason := signalMatches(r, e.Signal)
			if !seen {
				item.Pass = false
				item.Reason = reason
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

func signalMatches(r Retrieved, want ExpectedSignal) (bool, string) {
	if want.Kind == "" {
		return false, fmt.Sprintf("intent %s expected signal match %q without signal kind", r.IntentID, want.Match)
	}
	match := strings.ToLower(want.Match)
	if match == "" {
		return false, fmt.Sprintf("intent %s expected %s signal with empty match", r.IntentID, want.Kind)
	}
	switch want.Kind {
	case SignalConstraint:
		for _, c := range r.Constraints {
			if containsSignalText(match, c.What, c.Why) {
				return true, ""
			}
		}
	case SignalRisk:
		for _, risk := range r.Risks {
			if containsSignalText(match, risk.Text, riskStatementText(risk.Statement)) {
				return true, ""
			}
		}
	case SignalFollowup:
		for _, followup := range r.Followups {
			if containsSignalText(match, followup.Text, followupStatementText(followup.Statement)) {
				return true, ""
			}
		}
	default:
		return false, fmt.Sprintf("intent %s expected unknown signal kind %q", r.IntentID, want.Kind)
	}
	return false, fmt.Sprintf("intent %s has no explicit %s matching %q", r.IntentID, want.Kind, want.Match)
}

func containsSignalText(match string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(strings.ToLower(part), match) {
			return true
		}
	}
	return false
}

func riskStatementText(statement *domain.RiskStatement) string {
	if statement == nil {
		return ""
	}
	return statement.Text()
}

func followupStatementText(statement *domain.FollowupStatement) string {
	if statement == nil {
		return ""
	}
	return statement.Text()
}
