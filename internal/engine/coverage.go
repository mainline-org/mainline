package engine

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/gitops"
)

// -----------------------------------------------------------
// Coverage model (v0.3)
// -----------------------------------------------------------
//
// Every commit reachable from main is in exactly one of three states:
//
//   covered    notes ref points at a sealed (non-abandoned) intent
//   skipped    Mainline-Skip: trailer, matched [mainline.skip] pattern,
//              or pre-Mainline baseline history
//   uncovered  neither
//
// State is computed directly from git facts:
//   - refs/notes/mainline/intents (already-shipped rc3+ infrastructure)
//   - commit messages (subject + trailer block)
//   - team-config [mainline.skip] patterns and [mainline.coverage] baseline
//
// No mainline-private derived schema.

type CoverageState string

const (
	CoverageCovered   CoverageState = "covered"
	CoverageSkipped   CoverageState = "skipped"
	CoverageUncovered CoverageState = "uncovered"
)

const preMainlineBaselineReason = "pre-Mainline baseline"

// MainlineSkipTrailer is the canonical trailer key. Empty reason after
// the colon is rejected — see SkipReasonFromMessage.
const MainlineSkipTrailer = "Mainline-Skip"

// CommitCoverage is the per-commit classification result returned from
// CoverageWindow.
type CommitCoverage struct {
	Commit      string        `json:"commit"`
	Subject     string        `json:"subject"`
	Author      string        `json:"author"`
	CommittedAt string        `json:"committed_at"`
	State       CoverageState `json:"state"`

	// IntentIDs is populated when State == Covered. May contain multiple
	// ids when multiple intents share a commit (squash-merge of a
	// multi-intent feature). Filtered to non-abandoned intents.
	IntentIDs []string `json:"intent_ids,omitempty"`

	// SkipReason is populated when State == Skipped. Either a
	// human-supplied reason from the trailer, "matched config
	// pattern: <pattern>", or "pre-Mainline baseline: <sha>".
	SkipReason string `json:"skip_reason,omitempty"`
}

// CoverageWindow walks the last n commits on main (newest first) and
// classifies each as covered/skipped/uncovered. One pass uses cat-file
// --batch (already shipped) for note bodies + commit objects.
//
// view supplies the live mapping from intent id → status; it is
// required so we can drop notes that point at abandoned intents
// (treated as uncovered per the spec).
func (s *Service) CoverageWindow(n int, view *domain.MainlineView, cfg *domain.TeamConfig) ([]CommitCoverage, error) {
	if n <= 0 {
		n = 30
	}
	mainRef := s.syncedMainRef(cfg.Mainline.MainBranch)
	if s.Git.ReadRef(mainRef) == "" {
		return nil, nil
	}

	// One log invocation gets commit hash + subject + author + ISO date
	// for the last n commits on main, newest first. Limited window keeps
	// status output snappy on long-history repos.
	entries, err := s.Git.LogWindow(mainRef, n)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}

	// Build the intent-id → live-status lookup once. Notes that point
	// at intents in this set with a non-abandoned status count as
	// covered.
	liveIntents := make(map[string]bool, len(view.Intents))
	for _, iv := range view.Intents {
		if iv.Status == domain.StatusAbandoned {
			continue
		}
		liveIntents[iv.IntentID] = true
	}

	// Index notes-list by commit so per-commit lookups are O(1) without
	// shelling out to `git notes show` in a loop.
	noteByCommit := make(map[string]string)
	notes, _ := s.Git.NotesListEntries()
	if len(notes) > 0 {
		batch, err := s.Git.OpenCatFileBatch()
		if err == nil {
			defer batch.Close()
			for _, ne := range notes {
				body, err := batch.Read(ne.NoteBlob)
				if err != nil || body == nil {
					continue
				}
				noteByCommit[ne.CommitHash] = string(body)
			}
		}
	}

	skipPatterns := compileSkipPatterns(cfg)
	baselineCommit := strings.TrimSpace(cfg.Mainline.Coverage.BaselineCommit)
	var baselineAncestors map[string]bool
	if baselineCommit != "" {
		if set, err := s.Git.RevListSet(baselineCommit); err == nil {
			baselineAncestors = set
		}
	}

	out := make([]CommitCoverage, 0, len(entries))
	for _, e := range entries {
		cov := CommitCoverage{
			Commit:      e.Hash,
			Subject:     e.Subject,
			Author:      e.Author,
			CommittedAt: e.Date,
			State:       CoverageUncovered,
		}

		// Pri 1: covered (notes-ref hit pointing at a live intent).
		// Sealed intent claim is a stronger fact than any skip pattern,
		// so this priority order is deliberate.
		if rawNote, ok := noteByCommit[e.Hash]; ok {
			var note domain.CommitNote
			if err := json.Unmarshal([]byte(rawNote), &note); err == nil {
				if note.Kind == "mainline.commit_note" {
					ids := liveIntentIDsFromNote(&note, liveIntents)
					if len(ids) > 0 {
						cov.State = CoverageCovered
						cov.IntentIDs = ids
						out = append(out, cov)
						continue
					}
				}
			}
		}

		// Pri 2: pre-Mainline baseline. Existing project history
		// before `mainline init` is not a gap in Mainline usage. A
		// later explicit note still wins because covered is checked
		// first.
		if baselineAncestors[e.Hash] {
			cov.State = CoverageSkipped
			cov.SkipReason = preMainlineBaselineReason + ": " + short8(baselineCommit)
			out = append(out, cov)
			continue
		}

		// Pri 3: skipped (trailer or pattern). LogWindow returns subject
		// only; the trailer lives in the body, so fetch the full message
		// just for commits we have not already classified as covered.
		full, _ := s.Git.FullCommitMessage(e.Hash)
		if reason := SkipReasonFromMessage(full); reason != "" {
			cov.State = CoverageSkipped
			cov.SkipReason = reason
		} else if pattern := matchSkipPattern(e.Subject, skipPatterns); pattern != "" {
			cov.State = CoverageSkipped
			cov.SkipReason = "matched config pattern: " + pattern
		}

		out = append(out, cov)
	}
	return out, nil
}

// SkipReasonFromMessage returns the value of the Mainline-Skip trailer
// in a commit message, or empty string if absent. Empty / whitespace-
// only reasons are rejected to prevent the trailer becoming a
// thoughtless rubber stamp.
func SkipReasonFromMessage(fullMessage string) string {
	trailers := gitops.ParseTrailers(extractTrailerBlock(fullMessage))
	reason, ok := trailers[MainlineSkipTrailer]
	if !ok {
		return ""
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ""
	}
	return reason
}

// extractTrailerBlock returns the last paragraph of a commit message —
// the location git-interpret-trailers parses. A multi-line message
// where Mainline-Skip: appears mid-body would not be a valid trailer
// per git's own rules; we mirror that semantic.
func extractTrailerBlock(msg string) string {
	msg = strings.TrimRight(msg, "\n")
	if msg == "" {
		return ""
	}
	idx := strings.LastIndex(msg, "\n\n")
	if idx < 0 {
		return msg
	}
	return msg[idx+2:]
}

// SkipPattern is a compiled regex paired with its source string for
// reporting which pattern matched.
type SkipPattern struct {
	Source string
	Regex  *regexp.Regexp
}

func compileSkipPatterns(cfg *domain.TeamConfig) []SkipPattern {
	if cfg == nil {
		return nil
	}
	out := make([]SkipPattern, 0, len(cfg.Mainline.Skip.Patterns))
	for _, p := range cfg.Mainline.Skip.Patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			// Bad patterns are skipped silently rather than crashing the
			// whole coverage scan; doctor surfaces them separately.
			continue
		}
		out = append(out, SkipPattern{Source: p, Regex: re})
	}
	return out
}

func matchSkipPattern(subject string, patterns []SkipPattern) string {
	for _, p := range patterns {
		if p.Regex.MatchString(subject) {
			return p.Source
		}
	}
	return ""
}

// liveIntentIDsFromNote filters note.Intents down to ids that exist in
// the view and are not abandoned. Used by CoverageWindow to decide
// whether a note is load-bearing for "covered".
func liveIntentIDsFromNote(note *domain.CommitNote, liveIntents map[string]bool) []string {
	if note == nil {
		return nil
	}
	out := make([]string, 0, len(note.Intents))
	for _, ref := range note.Intents {
		if liveIntents[ref.IntentID] {
			out = append(out, ref.IntentID)
		}
	}
	return out
}
