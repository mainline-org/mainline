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
//   read decisions / risks / fingerprint of those intents
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
//   - hard hook blocking — agent guidance, not enforcement
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
type ContextRetrievalResult struct {
	Query           ContextQueryEcho   `json:"query"`
	RelevantIntents []ContextRelevant  `json:"relevant_intents"`
	Guidance        []string           `json:"guidance"`
}

// ContextQueryEcho echoes back what mode/inputs were used so an agent
// can audit "is this the retrieval I asked for?" without re-running.
type ContextQueryEcho struct {
	Mode  string   `json:"mode"`
	Files []string `json:"files,omitempty"`
	Text  string   `json:"text,omitempty"`
}

// ContextRelevant is one ranked intent in the retrieval result.
// Decisions / Risks are truncated; Followups are command suggestions
// the agent can copy-paste to drill into the full record.
type ContextRelevant struct {
	IntentID  string            `json:"intent_id"`
	Title     string            `json:"title"`
	Status    string            `json:"status"`
	Relevance ContextRelevance  `json:"relevance"`
	Summary   string            `json:"summary"`
	Decisions []string          `json:"decisions,omitempty"`
	Risks     []string          `json:"risks,omitempty"`
	Followups map[string]string `json:"followups,omitempty"`

	// Status-conditional surface:
	AbandonedReason string `json:"abandoned_reason,omitempty"`
	SupersededBy    string `json:"superseded_by,omitempty"`
}

// ContextRelevance captures the score and human-readable reasons.
// Scores are not normalised across calls — they're meant to be
// compared within a single result, not across different queries.
type ContextRelevance struct {
	Score   float64  `json:"score"`
	Reasons []string `json:"reasons"`
}

const (
	// ContextRetrievalDefaultLimit caps how many intents the
	// retrieval returns by default. Five is "enough to remind the
	// agent of relevant history" without producing a context dump
	// that drowns out the actual coding task.
	ContextRetrievalDefaultLimit = 5

	// contextDecisionLimit / contextRiskLimit cap per-intent
	// surface so a 20-decision intent doesn't fill the agent's
	// window. The agent can `mainline show <id>` for the rest.
	contextDecisionLimit = 3
	contextRiskLimit     = 3

	// contextRelevanceThreshold filters out intents below this
	// score. Tuned against the dogfood repo: 0.05 keeps anything
	// with at least one weak signal (subsystem match or recency
	// boost) and drops noise.
	contextRelevanceThreshold = 0.05
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
			"--query \"<text>\" to retrieve intents whose decisions / risks / summary match these keywords",
		)
	}

	view, _ := s.Store.ReadMainlineView()
	if view == nil {
		return &ContextRetrievalResult{
			Query:    ContextQueryEcho{Mode: req.Mode, Files: files, Text: query},
			Guidance: contextGuidance(),
		}, nil
	}

	// Score every non-drafting intent; rank; truncate to Limit.
	scored := make([]ContextRelevant, 0, len(view.Intents))
	branch, _ := s.Git.CurrentBranch()
	for _, iv := range view.Intents {
		if iv.Status == domain.StatusDrafting {
			continue
		}
		score, reasons := scoreIntentRelevance(iv, files, query, branch)
		// Mode-specific boost: in --current the user is implicitly
		// asking about their working branch, so an intent on the
		// same thread is materially more relevant. In --files /
		// --query the user has named the target explicitly; thread
		// match would let "I'm on this branch" outrank explicit
		// matches.
		if req.Mode == "current" && branch != "" && iv.Thread == branch {
			score += 0.15
			reasons = append(reasons, "same thread")
		}
		if score < contextRelevanceThreshold {
			continue
		}
		scored = append(scored, packRelevant(iv, score, reasons))
	}
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Relevance.Score > scored[j].Relevance.Score
	})
	if len(scored) > req.Limit {
		scored = scored[:req.Limit]
	}

	return &ContextRetrievalResult{
		Query:           ContextQueryEcho{Mode: req.Mode, Files: files, Text: query},
		RelevantIntents: scored,
		Guidance:        contextGuidance(),
	}, nil
}

// scoreIntentRelevance is the deterministic relevance ranker. Pure
// signal-extraction over fingerprint + summary text; no embeddings.
//
// Rough budget (max ~1.0 from any single intent, but most clamp far
// below):
//
//   file overlap:        up to 0.40   ← strongest signal
//   subsystem overlap:   up to 0.20
//   risk keyword match:  up to 0.20   ← deliberately above decisions
//                                       since a risk-match is more
//                                       constraining for the agent
//   decision kw match:   up to 0.15
//   title kw match:      up to 0.10
//   what / summary kw:   up to 0.05
//   recency:             up to 0.10
//   same thread/branch:  up to 0.15
func scoreIntentRelevance(iv domain.IntentView, files []string, query, currentBranch string) (float64, []string) {
	var score float64
	var reasons []string

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
			score += 0.10 * float64(subMatches)
			if score > 1.0 {
				score = 1.0
			}
			reasons = append(reasons, "same subsystem: "+firstOverlap(iv.Fingerprint.Subsystems, querySubsystems))
		}
	}

	// Keyword extraction from the query string. Reuses the conflict
	// detection's keywordsFromText — same stopword list, same
	// tokenisation, so retrieval and conflict scoring agree on what
	// counts as a meaningful word.
	if query != "" {
		keywords := keywordsFromText(query)
		if iv.Summary != nil {
			titleHits := countKeywordHits(keywords, iv.Summary.Title)
			if titleHits > 0 {
				score += 0.05 * float64(titleHits)
				reasons = append(reasons, "title mentions "+strings.Join(matchedKeywords(keywords, iv.Summary.Title), ", "))
			}
			whatHits := countKeywordHits(keywords, iv.Summary.What)
			if whatHits > 0 {
				score += 0.025 * float64(whatHits)
			}
			for _, d := range iv.Summary.Decisions {
				dText := d.Point + " " + d.Chose + " " + d.Rationale
				if countKeywordHits(keywords, dText) > 0 {
					score += 0.05
					reasons = append(reasons, "decision mentions "+truncateForReason(d.Point, 40))
					break
				}
			}
			for _, r := range iv.Summary.Risks {
				if countKeywordHits(keywords, r) > 0 {
					score += 0.10
					reasons = append(reasons, "risk mentions "+truncateForReason(r, 40))
					break
				}
			}
		}
	}

	// Recency boost — agents usually care about *recent* prior work.
	// Older intents get a small penalty so a months-old merge
	// doesn't outrank a same-week change of similar relevance.
	if iv.SealedAt != "" {
		if t, err := time.Parse(time.RFC3339, iv.SealedAt); err == nil {
			age := time.Since(t)
			switch {
			case age < 7*24*time.Hour:
				score += 0.10
				reasons = append(reasons, "merged this week")
			case age < 30*24*time.Hour:
				score += 0.05
			}
		}
	}

	// Same thread/branch boost is intentionally NOT applied here.
	// It only fires for --current mode (see scoreIntentRelevanceForCurrent),
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
		score *= 0.85
	}

	if len(reasons) == 0 && score > 0 {
		reasons = append(reasons, "weak signal match")
	}
	return score, reasons
}

func packRelevant(iv domain.IntentView, score float64, reasons []string) ContextRelevant {
	r := ContextRelevant{
		IntentID: iv.IntentID,
		Title:    "",
		Status:   string(iv.Status),
		Relevance: ContextRelevance{
			Score:   round2(score),
			Reasons: reasons,
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
		r.Risks = topItems(iv.Summary.Risks, contextRiskLimit)
	}
	if iv.Status == domain.StatusSuperseded {
		r.SupersededBy = iv.StatusEvidence.SupersededByIntent
	}
	// Abandoned reason: not in StatusEvidence today (only the event
	// carries it). When/if surfaced later, populate r.AbandonedReason
	// from there. For now leave empty — show/trace expose it.
	return r
}

// contextGuidance is the rc7+ "verify against current code" reminder.
// Intentionally short; the agent reads JSON, not prose. Two lines
// because both invariants matter:
//
//   1. "use intents as historical context" — guard against
//      ignoring the retrieval entirely (agent grep-first habit).
//   2. "verify against current code before editing" — guard
//      against the opposite extreme (agent trusting an intent
//      whose code has been refactored since).
func contextGuidance() []string {
	return []string{
		"Use these intents as historical context, not as a replacement for reading current code.",
		"Verify decisions against the current working tree before editing.",
	}
}

// currentRelevantFiles synthesises the file list for --current mode.
// Order of preference:
//
//   1. Files changed since fork point (base..HEAD) — exactly what an
//      agent's pending change touches.
//   2. Files in the worktree that are dirty/untracked — for an agent
//      mid-edit on an uncommitted change set.
//   3. Empty list — let scoring fall back to query / branch signals.
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

// keep linker happy if filepath ends up unused on some builds.
var _ = filepath.Separator
