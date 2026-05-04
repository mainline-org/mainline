package engine

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mainline-org/mainline/internal/domain"
)

// -----------------------------------------------------------
// Intent-first context retrieval
// -----------------------------------------------------------
//
// The product positioning here is sharp:
//
//   Mainline gives coding agents the historical *why* before they
//   inspect the current *what*.
//
// Agent default workflow becomes:
//
//   mainline status
//     → see overall state, get a hint to run context
//   mainline context --current --json
//     → list of intents relevant to the current change
//   read decisions / fingerprint of those intents
//   notice explicit inherited constraints and lifecycle warnings
//   THEN grep / read code to verify against current implementation
//   THEN edit
//
// `mainline context --files <paths>` is the file-targeted variant
// (when the user names a file, retrieve intents that touched it).
// `mainline context --query <text>` is the keyword variant
// (when the user names a feature area, retrieve intents that
// decided / risked / fingerprinted that area).
//
// What this command is NOT (per the v1 scope):
//   - embedding / vector search — deterministic only
//   - interactive UI — JSON / text only
//   - hard hook blocking — agent guidance plus explicit CLI gates,
//     not hidden hook magic
//   - per-turn diffs — same fingerprint files_touched semantics
//     the rest of mainline uses

// ContextRetrievalRequest selects which mode and what input to feed
// the retrieval scorer. Mode is the only required field; Files /
// Query supply mode-specific input.
type ContextRetrievalRequest struct {
	Mode  string   // "current" | "files" | "query"
	Files []string // populated for "files" mode (or synthesised for "current")
	Query string   // populated for "query" mode (or synthesised for "current")

	// Limit caps the number of intents in the result. Defaults to
	// ContextRetrievalDefaultLimit when zero.
	Limit int
}

// ContextRetrievalResult is the agent-friendly retrieval output.
// Compact by design — every field carries decision-relevant signal.
//
// Notes is the top-level reminder list ("verify against current
// code", etc.) — repo-wide guidance the agent should hold while
// reading the result. Per-intent guidance lives on ContextRelevant.
type ContextRetrievalResult struct {
	Query           ContextQueryEcho   `json:"query"`
	QueryDebug      *ContextQueryDebug `json:"query_debug,omitempty"`
	RelevantIntents []ContextRelevant  `json:"relevant_intents"`
	// InheritedConstraints lists high-severity human-promoted
	// constraints whose files overlap with the current change. Each
	// carries a stable constraint_id for explicit acknowledgement.
	InheritedConstraints []domain.InheritedConstraint `json:"inherited_constraints,omitempty"`
	Notes                []string                     `json:"notes"`
}

// ContextQueryEcho echoes back what mode/inputs were used so an agent
// can audit "is this the retrieval I asked for?" without re-running.
type ContextQueryEcho struct {
	Mode  string   `json:"mode"`
	Files []string `json:"files,omitempty"`
	Text  string   `json:"text,omitempty"`
}

// ContextQueryDebug exposes query-mode tokenisation, expansion, and
// candidate prefilter decisions so agents can audit why a semantic
// query did or did not retrieve a prior intent.
type ContextQueryDebug struct {
	Raw               string               `json:"raw"`
	EffectiveKeywords []string             `json:"effective_keywords"`
	DroppedTerms      []ContextDroppedTerm `json:"dropped_terms"`
	ExpandedTerms     map[string][]string  `json:"expanded_terms"`
	CandidateCount    int                  `json:"candidate_count"`
}

// ContextDroppedTerm explains why a textual term did not become an
// effective keyword under the current query tokenizer.
type ContextDroppedTerm struct {
	Term   string `json:"term"`
	Reason string `json:"reason"`
}

// ContextRelevant is one ranked intent in the retrieval result.
//
// The Status field here is the *retrieval status* — one of four
// values that tell the agent how to use this intent right now:
//
//   - "current"     this intent is the current effective decision;
//     verify against current code, then apply.
//   - "superseded"  this intent was replaced by SupersededBy; read
//     that one instead and only use this for context.
//   - "abandoned"   this approach was tried and abandoned; do not
//     repeat it without first understanding why.
//   - "stale"       this intent is current but its files have been
//     churning or it is old enough that its decisions
//     may no longer hold; verify before acting.
//
// Decisions are top-N truncated. Explicit constraints are surfaced
// through InheritedConstraints. Legacy risks / followups /
// anti_patterns remain on the struct for wire compatibility but
// default retrieval does not populate them; use `mainline show` or
// the dedicated signal commands for those surfaces. Followups are
// command suggestions the agent can copy-paste to drill into the full
// record. Guidance is the single-line advisory derived from Status.
type ContextRelevant struct {
	IntentID      string               `json:"intent_id"`
	Title         string               `json:"title"`
	Status        string               `json:"status"`
	Relevance     ContextRelevance     `json:"relevance"`
	Summary       string               `json:"summary"`
	Decisions     []string             `json:"decisions,omitempty"`
	Risks         []string             `json:"risks,omitempty"`
	OpenFollowups []string             `json:"open_followups,omitempty"`
	AntiPatterns  []domain.AntiPattern `json:"anti_patterns,omitempty"`
	Guidance      string               `json:"guidance,omitempty"`
	Followups     map[string]string    `json:"followups,omitempty"`

	// Status-conditional surface:
	AbandonedReason string `json:"abandoned_reason,omitempty"`
	SupersededBy    string `json:"superseded_by,omitempty"`
}

// Retrieval-status constants. Distinct from domain.IntentStatus
// (which is the lifecycle status: drafting / sealed_local / merged
// / etc.) — the retrieval status is what the agent needs to decide
// "should I trust this intent right now?".
const (
	RetrievalStatusCurrent    = "current"
	RetrievalStatusSuperseded = "superseded"
	RetrievalStatusAbandoned  = "abandoned"
	RetrievalStatusStale      = "stale"
)

// ContextRelevance captures the score and human-readable reasons.
// Scores are not normalised across calls — they're meant to be
// compared within a single result, not across different queries.
type ContextRelevance struct {
	Score     float64                    `json:"score"`
	Breakdown *ContextRelevanceBreakdown `json:"breakdown,omitempty"`
	Reasons   []string                   `json:"reasons"`
}

// ContextRelevanceBreakdown mirrors the scorer's additive signals.
// Risk / Followup / AntiPattern are retained for older JSON consumers
// but no longer contribute to default pre-edit retrieval.
// Final score is the additive fields plus lineage/same_thread boosts,
// minus status_penalty, rounded to two decimals for output.
type ContextRelevanceBreakdown struct {
	File          float64 `json:"file"`
	Subsystem     float64 `json:"subsystem"`
	Title         float64 `json:"title"`
	Summary       float64 `json:"summary"`
	Decision      float64 `json:"decision"`
	Risk          float64 `json:"risk"`
	Followup      float64 `json:"followup"`
	AntiPattern   float64 `json:"anti_pattern"`
	Recency       float64 `json:"recency"`
	SameThread    float64 `json:"same_thread"`
	Lineage       float64 `json:"lineage"`
	StatusPenalty float64 `json:"status_penalty"`
}

const (
	// ContextRetrievalDefaultLimit caps how many intents the
	// retrieval returns by default. Five is "enough to remind the
	// agent of relevant history" without producing a context dump
	// that drowns out the actual coding task.
	ContextRetrievalDefaultLimit = 5

	// contextDecisionLimit caps per-intent decision surface so a
	// 20-decision intent doesn't fill the agent's window. The agent
	// can `mainline show <id>` for the rest.
	contextDecisionLimit = 3

	// contextRelevanceThreshold filters out intents below this
	// score. Tuned against the dogfood repo: 0.05 keeps anything
	// with at least one weak signal (subsystem match or recency
	// boost) and drops noise. Query mode applies an extra guard:
	// recency can only boost a content match, not create one.
	contextRelevanceThreshold = 0.05

	// staleAge is the wall-clock threshold at which a non-superseded,
	// non-abandoned intent is considered stale. Picked at 90 days
	// because most repos see meaningful refactor cycles inside that
	// window — anything older needs a verify-against-current-code
	// nudge before the agent treats it as load-bearing truth.
	staleAge = 90 * 24 * time.Hour

	// staleFileChurnThreshold marks an intent as stale when any one
	// of its FilesTouched has been re-touched by this many newer
	// intents. Three is the heuristic ceiling: one re-touch is
	// normal, two is signal, three says "the file has moved on".
	staleFileChurnThreshold = 3
)

// RetrieveContext is the engine entry point for `mainline context`.
// All three modes go through here — the difference is which inputs
// the caller populates on the request.
func (s *Service) RetrieveContext(req ContextRetrievalRequest) (*ContextRetrievalResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}
	if req.Limit <= 0 {
		req.Limit = ContextRetrievalDefaultLimit
	}

	// Mode-specific synthesis: --current derives files + query from
	// the live repo state so the agent doesn't have to. Files / query
	// modes pass the inputs through verbatim.
	files := req.Files
	query := req.Query
	switch req.Mode {
	case "current":
		if len(files) == 0 {
			files = s.currentRelevantFiles()
		}
		if query == "" {
			query = s.currentRelevantQuery()
		}
	case "files":
		// caller provides files; nothing to synthesise.
	case "query":
		// caller provides query; nothing to synthesise.
	default:
		return nil, domain.NewRecoverableError(
			domain.ErrInvalidInput,
			fmt.Sprintf("context mode %q not recognised — use one of: --current, --files, --query", req.Mode),
			"--current to retrieve intents relevant to the active draft + current diff",
			"--files <paths...> to retrieve intents that touched these files",
			"--query \"<text>\" to retrieve intents whose decisions / summary / fingerprint match these keywords",
		)
	}

	view, _ := s.Store.ReadMainlineView()
	if view == nil {
		var queryDebug *ContextQueryDebug
		if req.Mode == "query" {
			queryDebug = buildContextQueryDebug(query, 0)
		}
		return &ContextRetrievalResult{
			Query:      ContextQueryEcho{Mode: req.Mode, Files: files, Text: query},
			QueryDebug: queryDebug,
			Notes:      contextNotes(),
		}, nil
	}

	// Pre-compute per-file churn for stale detection. Cheap: O(N×F)
	// across the view, done once per call.
	churn := buildFileChurnIndex(view)
	now := time.Now()

	// Decide the candidate set the scorer iterates over. For --files
	// and --query, prefer the SQLite reverse indexes so we only score
	// intents that have at least one matching surface, instead of
	// every sealed intent in the view. JSON-view scan stays as the
	// fallback when the SQLite cache is missing — semantics identical
	// because both paths deserialise the same IntentView struct.
	var qTerms queryTerms
	var queryKeywords []string
	if req.Mode == "query" {
		qTerms = queryTermsFromText(query)
		queryKeywords = qTerms.scoringKeywords()
	}

	candidates := candidateSetForRetrieval(s.Store, req.Mode, files, query, qTerms, view)
	var queryDebug *ContextQueryDebug
	if req.Mode == "query" {
		queryDebug = buildContextQueryDebugFromTerms(query, qTerms, len(candidates))
	}

	// Score every non-drafting intent; rank; truncate to Limit.
	// Abandoned and superseded intents stay in the result set —
	// they are valuable signal ("this was tried", "this was
	// replaced") — but ranked below current intents of comparable
	// raw score by the multiplier in scoreIntentRelevance, and
	// labelled with the retrieval status that tells the agent how
	// to use them.
	includeBreakdown := req.Mode == "query"
	scored := make([]ContextRelevant, 0, len(candidates))
	branch, _ := s.Git.CurrentBranch()
	for _, iv := range candidates {
		if iv.Status == domain.StatusDrafting {
			continue
		}
		score, reasons, breakdown := scoreIntentRelevanceWithRiskLifecycle(iv, files, keywordsFromText(query), branch, view.RiskResolutions)
		if req.Mode == "query" {
			score, reasons, breakdown = scoreIntentRelevanceWithRiskLifecycle(iv, files, queryKeywords, branch, view.RiskResolutions)
		}
		if req.Mode == "current" && branch != "" && iv.Thread == branch {
			score += 0.15
			breakdown.SameThread += 0.15
			reasons = append(reasons, "same thread")
		}
		if req.Mode == "query" && !hasQueryContentSignal(breakdown) {
			continue
		}
		if score < contextRelevanceThreshold {
			continue
		}
		retrStatus := classifyRetrievalStatus(iv, churn, now)
		scored = append(scored, packRelevant(iv, score, optionalRelevanceBreakdown(includeBreakdown, breakdown), reasons, retrStatus, view.RiskResolutions, view.FollowupResolutions))
	}

	// Explicit supersession links are lineage, not independent
	// relevance matches. If a superseder is relevant, include the
	// intents it replaced even when a SQLite candidate prefilter or
	// relevance threshold would otherwise drop them.
	scored = includeSupersededLineage(scored, view, churn, now, files, query, queryKeywords, branch, includeBreakdown)

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Relevance.Score > scored[j].Relevance.Score
	})

	// Apply Property 3 (Superseded 不上位): when A is superseded by B
	// and both are in the result, B must rank above A as a hard
	// ordering constraint.
	enforceSupersessionRanking(scored)

	if len(scored) > req.Limit {
		scored = scored[:req.Limit]
	}

	// Aggregate explicit inherited constraints whose files overlap
	// with the current change. Files come from the request (or from
	// `currentRelevantFiles` for --current); subsystems are derived
	// only to preserve the public helper signature.
	subsystems := subsystemsFromFiles(files)
	excludeID := ""
	if req.Mode == "current" {
		if d, _ := s.Store.FindActiveDraft(branch); d != nil {
			excludeID = d.IntentID
		}
	}
	inherited := domain.BuildInheritedConstraints(view, files, subsystems, excludeID)

	notes := contextNotes()
	if len(inherited) > 0 {
		notes = append(notes,
			"Inherited high-severity constraints surfaced — each has a constraint_id. "+
				"When sealing, add each to acknowledged_constraints[] with: "+
				`{"constraint_id": "<id>", "disposition": "preserved|mitigated|not_applicable|intentionally_changed", "note": "..."}`)
	}

	return &ContextRetrievalResult{
		Query:                ContextQueryEcho{Mode: req.Mode, Files: files, Text: query},
		QueryDebug:           queryDebug,
		RelevantIntents:      scored,
		InheritedConstraints: inherited,
		Notes:                notes,
	}, nil
}

// candidateSetForRetrieval picks the intents the scorer iterates
// over. For --files and --query, the SQLite reverse indexes shrink
// the set to "intents with at least one matching surface" — a
// material speedup on big repos. For --current, the file/query
// signals come from the live diff and synthesised text, but those
// vary turn-by-turn and the candidate set on a hot loop is just as
// likely to be the whole catalog as not, so we keep the full
// view.Intents path for predictability.
//
// On any SQLite error or cache miss we fall back to view.Intents.
// This preserves the contract "retrieval results never depend on
// whether the cache is present"; the cache only changes the cost.
func candidateSetForRetrieval(store interface {
	ReadIntentViewsByFiles(paths []string) ([]domain.IntentView, error)
	ReadIntentViewsByQuery(keyword string) ([]domain.IntentView, error)
}, mode string, files []string, query string, qTerms queryTerms, view *domain.MainlineView) []domain.IntentView {
	switch mode {
	case "files":
		if len(files) == 0 {
			return view.Intents
		}
		got, err := store.ReadIntentViewsByFiles(files)
		if err != nil || len(got) == 0 {
			return view.Intents
		}
		return got
	case "query":
		// Use the query-specific keywords and expansions as a SQLite
		// filter; the in-memory scorer still handles relative weights.
		// We union every keyword hit so the cache path cannot drop an
		// intent merely because its best signal was not the
		// alphabetically first token in the user's task.
		keywords := qTerms.scoringKeywords()
		if len(keywords) == 0 && query != "" {
			keywords = queryTermsFromText(query).scoringKeywords()
		}
		if len(keywords) == 0 {
			return view.Intents
		}
		seen := map[string]bool{}
		out := make([]domain.IntentView, 0)
		for _, kw := range keywords {
			got, err := store.ReadIntentViewsByQuery(kw)
			if err != nil {
				return view.Intents
			}
			for _, iv := range got {
				if seen[iv.IntentID] {
					continue
				}
				seen[iv.IntentID] = true
				out = append(out, iv)
			}
		}
		if len(out) == 0 {
			return view.Intents
		}
		out = appendRecentQueryCandidates(out, seen, view, time.Now())
		return out
	}
	return view.Intents
}

func appendRecentQueryCandidates(out []domain.IntentView, seen map[string]bool, view *domain.MainlineView, now time.Time) []domain.IntentView {
	for _, iv := range view.Intents {
		if seen[iv.IntentID] {
			continue
		}
		if !hasQueryRecencySignal(iv, now) {
			continue
		}
		seen[iv.IntentID] = true
		out = append(out, iv)
	}
	return out
}

func hasQueryRecencySignal(iv domain.IntentView, now time.Time) bool {
	if iv.SealedAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, iv.SealedAt)
	if err != nil {
		return false
	}
	return now.Sub(t) < 30*24*time.Hour
}

// classifyRetrievalStatus maps a domain IntentView to one of four
// retrieval-status values. This is what tells the agent how to
// USE the intent now, distinct from the lifecycle status.
//
// Priority (first match wins):
//
//  1. abandoned   — domain status says abandoned/reverted
//  2. superseded  — explicit StatusEvidence.SupersededByIntent
//  3. stale       — wall-clock age >= staleAge OR any of its files
//     has been re-touched by >= staleFileChurnThreshold
//     later sealed intents
//  4. current     — the default
func classifyRetrievalStatus(iv domain.IntentView, churn map[string]int, now time.Time) string {
	switch iv.Status {
	case domain.StatusAbandoned, domain.StatusReverted:
		return RetrievalStatusAbandoned
	}
	if iv.StatusEvidence.SupersededByIntent != "" {
		return RetrievalStatusSuperseded
	}
	// Wall-clock stale: this intent is old enough that its
	// surrounding code has likely moved.
	if iv.SealedAt != "" {
		if t, err := time.Parse(time.RFC3339, iv.SealedAt); err == nil {
			if now.Sub(t) >= staleAge {
				return RetrievalStatusStale
			}
		}
	}
	// File-churn stale: at least one of this intent's files has been
	// touched by `staleFileChurnThreshold` strictly-later intents.
	if iv.Fingerprint != nil {
		for _, f := range iv.Fingerprint.FilesTouched {
			if churn[idForFile(iv.IntentID, f)] >= staleFileChurnThreshold {
				return RetrievalStatusStale
			}
		}
	}
	return RetrievalStatusCurrent
}

// buildFileChurnIndex returns a map keyed by `<intentID>|<file>`
// whose value is the number of *strictly-later* sealed intents that
// also touched the same file. The strictly-later condition is what
// makes this the stale-detection signal (an intent isn't stale
// because of its own touches).
//
// We index by `<intentID>|<file>` rather than just `<file>` because
// each intent's stale judgement depends on what came AFTER it, not
// on the absolute count of touches.
func buildFileChurnIndex(view *domain.MainlineView) map[string]int {
	out := map[string]int{}
	if view == nil {
		return out
	}
	// Touches by file, with sealed-at timestamps so we can apply
	// the strictly-later filter cheaply.
	type touch struct {
		intentID string
		sealedAt time.Time
	}
	byFile := map[string][]touch{}
	for _, iv := range view.Intents {
		if iv.Fingerprint == nil {
			continue
		}
		var ts time.Time
		if iv.SealedAt != "" {
			if parsed, err := time.Parse(time.RFC3339, iv.SealedAt); err == nil {
				ts = parsed
			}
		}
		for _, f := range iv.Fingerprint.FilesTouched {
			byFile[f] = append(byFile[f], touch{intentID: iv.IntentID, sealedAt: ts})
		}
	}
	for f, list := range byFile {
		for i, t := range list {
			later := 0
			for j, u := range list {
				if i == j {
					continue
				}
				// Strictly-later wins; ties (same sealedAt) do not.
				if u.sealedAt.After(t.sealedAt) {
					later++
				}
			}
			out[idForFile(t.intentID, f)] = later
		}
	}
	return out
}

func idForFile(intentID, file string) string {
	return intentID + "|" + file
}

// includeSupersededLineage expands the result set along explicit
// supersession links. Relevance thresholding should filter unrelated
// history, not hide the replaced half of a decision lineage whose
// replacement already matched the user's task.
func includeSupersededLineage(scored []ContextRelevant, view *domain.MainlineView, churn map[string]int, now time.Time, files []string, query string, queryKeywords []string, branch string, includeBreakdown bool) []ContextRelevant {
	if view == nil || len(scored) == 0 {
		return scored
	}
	byID := map[string]ContextRelevant{}
	for _, r := range scored {
		byID[r.IntentID] = r
	}
	bySuperseder := map[string][]domain.IntentView{}
	for _, iv := range view.Intents {
		if iv.Status == domain.StatusDrafting {
			continue
		}
		superseder := iv.StatusEvidence.SupersededByIntent
		if superseder == "" {
			continue
		}
		bySuperseder[superseder] = append(bySuperseder[superseder], iv)
	}

	for changed := true; changed; {
		changed = false
		for supersederID, superseded := range bySuperseder {
			parent, parentPresent := byID[supersederID]
			if !parentPresent {
				continue
			}
			for _, iv := range superseded {
				if _, exists := byID[iv.IntentID]; exists {
					continue
				}
				score, reasons, breakdown := scoreIntentRelevanceWithRiskLifecycle(iv, files, keywordsFromText(query), branch, view.RiskResolutions)
				if queryKeywords != nil {
					score, reasons, breakdown = scoreIntentRelevanceWithRiskLifecycle(iv, files, queryKeywords, branch, view.RiskResolutions)
				}
				parentScore := parent.Relevance.Score
				if score < parentScore {
					breakdown.Lineage += parentScore - score
					score = parentScore
				}
				if score < 0.01 {
					breakdown.Lineage += 0.01 - score
					score = 0.01
				}
				reasons = append(reasons, "superseded by returned intent "+supersederID)
				retrStatus := classifyRetrievalStatus(iv, churn, now)
				added := packRelevant(iv, score, optionalRelevanceBreakdown(includeBreakdown, breakdown), reasons, retrStatus, view.RiskResolutions, view.FollowupResolutions)
				scored = append(scored, added)
				byID[iv.IntentID] = added
				changed = true
			}
		}
	}
	return scored
}

// enforceSupersessionRanking implements Property 3: any superseder
// present in the result set ranks strictly above the intent it
// supersedes, with the superseded intent immediately after the
// replacement. This is a post-sort hard ordering pass rather than a
// score tweak, so rounding and chained replacements cannot invert
// or separate the lineage.
func enforceSupersessionRanking(scored []ContextRelevant) {
	for pass := 0; pass < len(scored); pass++ {
		moved := false
		byID := map[string]int{}
		for i, r := range scored {
			byID[r.IntentID] = i
		}
		for i := 0; i < len(scored); i++ {
			superseder := scored[i].SupersededBy
			if superseder == "" {
				continue
			}
			j, ok := byID[superseder]
			if !ok || j == i || j == i-1 {
				continue
			}
			moveIntentAfter(scored, i, j)
			moved = true
			break
		}
		if !moved {
			return
		}
	}
}

func moveIntentAfter(scored []ContextRelevant, from, after int) {
	if from == after+1 {
		return
	}
	item := scored[from]
	if from < after {
		copy(scored[from:after], scored[from+1:after+1])
		scored[after] = item
		return
	}
	copy(scored[after+2:from+1], scored[after+1:from])
	scored[after+1] = item
}

// scoreIntentRelevance is the deterministic relevance ranker. Pure
// signal-extraction over fingerprint + summary text; no embeddings.
// It keeps the historical conflict-keyword tokenizer for current and
// files modes; query mode calls scoreIntentRelevanceWithKeywords with
// query-specific terms instead.
//
// Current additive weights:
//
//	file overlap:         0.20 per matching file, capped at 0.40
//	subsystem overlap:    0.10 per path-derived subsystem match
//	                     (the score is clamped to 1.0 immediately
//	                     after this step, matching existing code)
//	title keyword:        0.05 per keyword hit
//	what / why keyword:   0.025 per keyword hit in each field
//	decision keyword:     0.05 once, for the first matching decision
//	recency:              0.10 when <7d old, 0.05 when <30d old
//
// Retrieval adds a same-thread +0.15 boost outside this function for
// --current mode only. Abandoned/superseded/reverted source intents
// keep their signal but receive a final x0.85 multiplier.
func scoreIntentRelevance(iv domain.IntentView, files []string, query, currentBranch string) (float64, []string, ContextRelevanceBreakdown) {
	return scoreIntentRelevanceWithKeywords(iv, files, keywordsFromText(query), currentBranch)
}

func scoreIntentRelevanceWithKeywords(iv domain.IntentView, files []string, keywords []string, currentBranch string) (float64, []string, ContextRelevanceBreakdown) {
	return scoreIntentRelevanceWithRiskLifecycle(iv, files, keywords, currentBranch, nil)
}

func scoreIntentRelevanceWithRiskLifecycle(iv domain.IntentView, files []string, keywords []string, currentBranch string, riskResolutions map[string][]domain.RiskResolution) (float64, []string, ContextRelevanceBreakdown) {
	var score float64
	var reasons []string
	var breakdown ContextRelevanceBreakdown

	if iv.Fingerprint != nil {
		// File overlap. Each matching file scores; capped so a
		// 30-file intent doesn't dominate by sheer surface area.
		fileMatches := countOverlap(iv.Fingerprint.FilesTouched, files)
		if fileMatches > 0 {
			contrib := float64(fileMatches) * 0.20
			if contrib > 0.40 {
				contrib = 0.40
			}
			score += contrib
			breakdown.File += contrib
			if fileMatches == 1 {
				reasons = append(reasons, "touched "+firstOverlap(iv.Fingerprint.FilesTouched, files))
			} else {
				reasons = append(reasons, fmt.Sprintf("touched %d of the same files", fileMatches))
			}
		}

		// Subsystem overlap from request files (path-derived).
		querySubsystems := subsystemsFromFiles(files)
		subMatches := countOverlap(iv.Fingerprint.Subsystems, querySubsystems)
		if subMatches > 0 {
			before := score
			score += 0.10 * float64(subMatches)
			if score > 1.0 {
				score = 1.0
			}
			breakdown.Subsystem += score - before
			reasons = append(reasons, "same subsystem: "+firstOverlap(iv.Fingerprint.Subsystems, querySubsystems))
		}
	}

	// Query/text keyword matching. Callers choose the tokenizer:
	// current/files keep keywordsFromText, while query mode uses the
	// query-specific tokenizer with short-token allowlist, aliases,
	// and CJK fallback terms.
	if len(keywords) > 0 {
		if iv.Summary != nil {
			titleHits := countKeywordHits(keywords, iv.Summary.Title)
			if titleHits > 0 {
				contrib := 0.05 * float64(titleHits)
				score += contrib
				breakdown.Title += contrib
				reasons = append(reasons, "title mentions "+strings.Join(matchedKeywords(keywords, iv.Summary.Title), ", "))
			}
			whatHits := countKeywordHits(keywords, iv.Summary.What)
			if whatHits > 0 {
				contrib := 0.025 * float64(whatHits)
				score += contrib
				breakdown.Summary += contrib
			}
			whyHits := countKeywordHits(keywords, iv.Summary.Why)
			if whyHits > 0 {
				contrib := 0.025 * float64(whyHits)
				score += contrib
				breakdown.Summary += contrib
			}
			for _, d := range iv.Summary.Decisions {
				dText := d.Point + " " + d.Chose + " " + d.Rationale
				if countKeywordHits(keywords, dText) > 0 {
					score += 0.05
					breakdown.Decision += 0.05
					reasons = append(reasons, "decision mentions "+truncateForReason(d.Point, 40))
					break
				}
			}
		}
	}

	// Recency boost — agents usually care about *recent* prior work.
	// Older intents simply receive no boost; they are not penalised.
	if iv.SealedAt != "" {
		if t, err := time.Parse(time.RFC3339, iv.SealedAt); err == nil {
			age := time.Since(t)
			switch {
			case age < 7*24*time.Hour:
				score += 0.10
				breakdown.Recency += 0.10
				reasons = append(reasons, "merged this week")
			case age < 30*24*time.Hour:
				score += 0.05
				breakdown.Recency += 0.05
			}
		}
	}

	// Same thread/branch boost is intentionally NOT applied here.
	// It only fires for --current mode in RetrieveContext,
	// because in --files / --query the user has explicitly named the
	// retrieval target — whether the intent happens to be on the
	// caller's working branch is incidental and would otherwise let
	// "I am on this branch" outrank "I literally asked about this file".
	_ = currentBranch

	// Status-aware adjustments. Abandoned/superseded intents stay
	// in the result set — they tell the agent "this was tried" —
	// but a small penalty so a high-relevance merged intent
	// outranks a same-relevance abandoned one.
	switch iv.Status {
	case domain.StatusAbandoned, domain.StatusSuperseded, domain.StatusReverted:
		before := score
		score *= 0.85
		breakdown.StatusPenalty += before - score
	}

	if len(reasons) == 0 && score > 0 {
		reasons = append(reasons, "weak signal match")
	}
	return score, reasons, breakdown
}

func hasQueryContentSignal(b ContextRelevanceBreakdown) bool {
	return b.Title > 0 ||
		b.Summary > 0 ||
		b.Decision > 0
}

func packRelevant(
	iv domain.IntentView,
	score float64,
	breakdown *ContextRelevanceBreakdown,
	reasons []string,
	retrStatus string,
	riskResolutions map[string][]domain.RiskResolution,
	followupResolutions map[string][]domain.FollowupResolution,
) ContextRelevant {
	r := ContextRelevant{
		IntentID: iv.IntentID,
		Title:    "",
		Status:   retrStatus,
		Relevance: ContextRelevance{
			Score:     round2(score),
			Breakdown: breakdown,
			Reasons:   reasons,
		},
		Followups: map[string]string{
			"show":  "mainline show " + iv.IntentID + " --json",
			"trace": "mainline trace " + iv.IntentID + " --json",
		},
	}
	if iv.Summary != nil {
		r.Title = iv.Summary.Title
		r.Summary = truncateForReason(iv.Summary.What, 240)
		r.Decisions = topDecisions(iv.Summary.Decisions, contextDecisionLimit)
	}
	if iv.StatusEvidence.SupersededByIntent != "" {
		r.SupersededBy = iv.StatusEvidence.SupersededByIntent
	}
	r.Guidance = guidanceFor(retrStatus, r.SupersededBy)
	return r
}

// filterOpenRisks returns only risks that are still open (not resolved
// and not from an expired source intent).
func filterOpenRisks(intentID string, risks []string, resolutions map[string][]domain.RiskResolution, sourceStatus domain.IntentStatus) []string {
	return domain.OpenRiskTexts(intentID, risks, resolutions, sourceStatus)
}

// guidanceFor returns the single-line advisory for a retrieval
// status. Property 6: deterministic mapping. Anti-patterns and
// risks are still surfaced as their own fields; this is the
// orienting reminder.
func guidanceFor(status, supersededBy string) string {
	switch status {
	case RetrievalStatusSuperseded:
		if supersededBy != "" {
			return "superseded by " + supersededBy + " — read that intent and use this only for context"
		}
		return "superseded — read the replacement intent before applying this"
	case RetrievalStatusAbandoned:
		return "this approach was abandoned — understand why before retrying"
	case RetrievalStatusStale:
		return "verify decisions still hold against current code; the surrounding files have moved"
	default:
		return "verify against current code before applying"
	}
}

// contextNotes is the top-level "how to use this retrieval" reminder.
// Intentionally short; the agent reads JSON, not prose. Three lines
// because three invariants matter:
//
//  1. "use intents as historical context" — guard against
//     ignoring the retrieval entirely (agent grep-first habit).
//  2. "verify against current code before editing" — guard
//     against the opposite extreme (agent trusting an intent
//     whose code has been refactored since).
//  3. durable constraints are human-promoted signals, while lifecycle
//     events are warnings.
func contextNotes() []string {
	return []string{
		"Use these intents as historical context, not as a replacement for reading current code.",
		"Verify decisions against the current working tree before editing.",
		"Only human-promoted constraints are hard rules; abandoned/superseded/reverted history is a warning to inspect, not a new constraint.",
	}
}

// currentRelevantFiles synthesises the file list for --current mode.
// Order of preference:
//
//  1. Files changed since fork point (base..HEAD) — exactly what an
//     agent's pending change touches.
//  2. Files in the worktree that are dirty/untracked — for an agent
//     mid-edit on an uncommitted change set.
//  3. Empty list — let scoring fall back to query / branch signals.
func (s *Service) currentRelevantFiles() []string {
	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil
	}
	mainRef := s.syncedMainRef(cfg.Mainline.MainBranch)
	if base := s.Git.ReadRef(mainRef); base != "" {
		head, _ := s.Git.HeadCommit()
		if head != "" && head != base {
			files, _ := s.Git.DiffFilesAgainst(base, head)
			if len(files) > 0 {
				return files
			}
		}
	}
	wt, _ := s.Git.WorktreeStatus()
	if wt != nil {
		return append(append([]string{}, wt.DirtyFiles...), wt.Untracked...)
	}
	return nil
}

// currentRelevantQuery synthesises the query string for --current
// mode. Source priority: active draft goal → (no query). Goal text
// is what the agent claimed they were doing, so it's the highest-
// value seed.
func (s *Service) currentRelevantQuery() string {
	branch, _ := s.Git.CurrentBranch()
	if d, _ := s.Store.FindActiveDraft(branch); d != nil && d.Goal != "" {
		return d.Goal
	}
	return ""
}

// -----------------------------------------------------------
// helpers
// -----------------------------------------------------------

func countOverlap(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := make(map[string]bool, len(a))
	for _, s := range a {
		set[s] = true
	}
	n := 0
	for _, s := range b {
		if set[s] {
			n++
		}
	}
	return n
}

func firstOverlap(a, b []string) string {
	set := make(map[string]bool, len(a))
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		if set[s] {
			return s
		}
	}
	return ""
}

func countKeywordHits(keywords []string, text string) int {
	if len(keywords) == 0 || text == "" {
		return 0
	}
	low := strings.ToLower(text)
	n := 0
	for _, kw := range keywords {
		if strings.Contains(low, kw) {
			n++
		}
	}
	return n
}

func matchedKeywords(keywords []string, text string) []string {
	low := strings.ToLower(text)
	out := []string{}
	for _, kw := range keywords {
		if strings.Contains(low, kw) {
			out = append(out, kw)
		}
	}
	return out
}

func buildContextQueryDebug(raw string, candidateCount int) *ContextQueryDebug {
	return buildContextQueryDebugFromTerms(raw, queryTermsFromText(raw), candidateCount)
}

func buildContextQueryDebugFromTerms(raw string, terms queryTerms, candidateCount int) *ContextQueryDebug {
	effectiveCopy := append(make([]string, 0, len(terms.EffectiveKeywords)), terms.EffectiveKeywords...)
	droppedCopy := append(make([]ContextDroppedTerm, 0, len(terms.DroppedTerms)), terms.DroppedTerms...)
	expandedCopy := make(map[string][]string, len(terms.ExpandedTerms))
	for term, aliases := range terms.ExpandedTerms {
		expandedCopy[term] = append([]string(nil), aliases...)
	}
	return &ContextQueryDebug{
		Raw:               raw,
		EffectiveKeywords: effectiveCopy,
		DroppedTerms:      droppedCopy,
		ExpandedTerms:     expandedCopy,
		CandidateCount:    candidateCount,
	}
}

func topDecisions(in []domain.Decision, n int) []string {
	out := make([]string, 0, n)
	for i, d := range in {
		if i >= n {
			break
		}
		// Render as "<chose> — <rationale>" trimmed; agents care
		// most about WHAT was chosen.
		entry := d.Chose
		if d.Point != "" {
			entry = d.Point + ": " + d.Chose
		}
		entry = truncateForReason(entry, 200)
		out = append(out, entry)
	}
	return out
}

func topItems(in []string, n int) []string {
	if len(in) <= n {
		return in
	}
	return in[:n]
}

func truncateForReason(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

func optionalRelevanceBreakdown(include bool, in ContextRelevanceBreakdown) *ContextRelevanceBreakdown {
	if !include {
		return nil
	}
	out := roundRelevanceBreakdown(in)
	return &out
}

func roundRelevanceBreakdown(in ContextRelevanceBreakdown) ContextRelevanceBreakdown {
	return ContextRelevanceBreakdown{
		File:          round2(in.File),
		Subsystem:     round2(in.Subsystem),
		Title:         round2(in.Title),
		Summary:       round2(in.Summary),
		Decision:      round2(in.Decision),
		Risk:          round2(in.Risk),
		Followup:      round2(in.Followup),
		AntiPattern:   round2(in.AntiPattern),
		Recency:       round2(in.Recency),
		SameThread:    round2(in.SameThread),
		Lineage:       round2(in.Lineage),
		StatusPenalty: round2(in.StatusPenalty),
	}
}

// keep linker happy if filepath ends up unused on some builds.
var _ = filepath.Separator
