package engine

import (
	"sort"
	"strings"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/gitops"
)

const (
	PreflightLevelOK    = "ok"
	PreflightLevelWarn  = "warn"
	PreflightLevelBlock = "block"

	PreflightFindingNotInitialized         = "not_initialized"
	PreflightFindingIdentityMissing        = "identity_missing"
	PreflightFindingSyncStale              = "sync_stale"
	PreflightFindingNotesRewriteDrift      = "notes_rewrite_drift"
	PreflightFindingBranchDrift            = "branch_drift"
	PreflightFindingActiveBaseBehind       = "active_intent_base_behind"
	PreflightFindingDirtyWithoutCommitDiff = "dirty_without_commit_diff"

	PreflightOverlapProposed       = "proposed_overlap"
	PreflightOverlapUpstreamMerged = "upstream_merged_overlap"
	// PreflightOverlapGoalText fires when the active draft's goal text
	// shares enough keywords with another proposed intent's title or
	// goal. Catches duplicate-work-in-flight before any code is written
	// (when file-overlap detection has nothing to compare against).
	PreflightOverlapGoalText = "goal_text_overlap"
)

const preflightOverlapLimit = 8

// preflightGoalOverlapMinKeywords is the minimum effective-keyword
// count required before goal-text overlap is even attempted. One- or
// two-word goals are too noisy to score reliably (a single common
// word like "fix" or "improve" would match too many intents).
const preflightGoalOverlapMinKeywords = 2

// preflightGoalOverlapMinHitRate is the fraction of the active draft's
// goal keywords that must appear in a proposed intent's title or goal
// before it surfaces as a duplicate-work warning. Tuned conservatively:
// 0.5 means at least half of the goal's significant words have to land
// somewhere on the candidate, otherwise the noise floor swamps signal.
const preflightGoalOverlapMinHitRate = 0.5

type PreflightResult struct {
	Level           string             `json:"level"`
	OKToContinue    bool               `json:"ok_to_continue"`
	Facts           PreflightFacts     `json:"facts"`
	Findings        []PreflightFinding `json:"findings,omitempty"`
	Overlaps        []PreflightOverlap `json:"overlaps,omitempty"`
	RecommendedNext []string           `json:"recommended_next,omitempty"`
}

type PreflightFacts struct {
	Branch          string   `json:"branch,omitempty"`
	ActiveIntentID  string   `json:"active_intent_id,omitempty"`
	ActiveBase      string   `json:"active_base,omitempty"`
	LocalHead       string   `json:"local_head,omitempty"`
	MainHead        string   `json:"main_head,omitempty"`
	SyncStale       bool     `json:"sync_stale,omitempty"`
	WorktreeStatus  string   `json:"worktree_status,omitempty"`
	DirtyFiles      []string `json:"dirty_files,omitempty"`
	UntrackedFiles  []string `json:"untracked_files,omitempty"`
	CurrentFiles    []string `json:"current_files,omitempty"`
	CommitDiffFiles []string `json:"commit_diff_files,omitempty"`
	ProposedCount   int      `json:"proposed_count,omitempty"`

	NotesRewriteDrift        bool `json:"notes_rewrite_drift,omitempty"`
	UnreachableMainlineNotes int  `json:"unreachable_mainline_notes,omitempty"`
}

type PreflightFinding struct {
	Code    string   `json:"code"`
	Level   string   `json:"level"`
	Message string   `json:"message"`
	Files   []string `json:"files,omitempty"`
}

type PreflightOverlap struct {
	Kind         string   `json:"kind"`
	Level        string   `json:"level"`
	IntentID     string   `json:"intent_id"`
	Title        string   `json:"title,omitempty"`
	Status       string   `json:"status"`
	MatchedFiles []string `json:"matched_files,omitempty"`
	// MatchedKeywords carries the goal-text words that landed on a
	// goal_text_overlap candidate. Empty for file-overlap kinds —
	// matched_files carries the evidence there.
	MatchedKeywords  []string `json:"matched_keywords,omitempty"`
	Score            int      `json:"score"`
	MergedMainCommit string   `json:"merged_main_commit,omitempty"`
	// AuthorName surfaces the proposed intent's author so the warning
	// reads "z2z23n0 already proposed this" without a second lookup.
	// Only filled for goal_text_overlap and proposed_overlap kinds —
	// upstream_merged_overlap intents are already on main and the
	// committer is shown in git log.
	AuthorName string `json:"author_name,omitempty"`
	AuthorID   string `json:"author_id,omitempty"`
}

type preflightInput struct {
	status          *StatusResult
	currentFiles    []string
	commitDiffFiles []string
	worktree        *gitops.WorktreeStatusReport
	proposed        []domain.IntentView
	view            *domain.MainlineView
	upstreamCommits map[string]bool
}

func (s *Service) Preflight() (*PreflightResult, error) {
	status, err := s.Status()
	if err != nil {
		return nil, err
	}

	idx, _ := s.Store.ReadProposedIndex()
	view, _ := s.Store.ReadMainlineView()
	wt, _ := s.Git.WorktreeStatus()
	upstreamCommits := s.preflightUpstreamCommitSet(status)
	commitDiffFiles := s.preflightCommitDiffFiles(status)

	var proposed []domain.IntentView
	if idx != nil {
		proposed = idx.Proposed
	}

	return buildPreflightResult(preflightInput{
		status:          status,
		currentFiles:    preflightCurrentFiles(commitDiffFiles, wt),
		commitDiffFiles: commitDiffFiles,
		worktree:        wt,
		proposed:        proposed,
		view:            view,
		upstreamCommits: upstreamCommits,
	}), nil
}

func (s *Service) preflightCommitDiffFiles(status *StatusResult) []string {
	if status == nil {
		return nil
	}
	base := ""
	if status.MainHead != "" && status.LocalHead != "" && status.MainHead != status.LocalHead {
		base, _ = s.Git.MergeBase(status.MainHead, status.LocalHead)
	}
	if base == "" || status.LocalHead == "" || base == status.LocalHead {
		return nil
	}
	files, err := s.Git.DiffFilesAgainst(base, status.LocalHead)
	if err != nil {
		return nil
	}
	return compactSortedStrings(files)
}

func (s *Service) preflightUpstreamCommitSet(status *StatusResult) map[string]bool {
	if status == nil || status.LocalHead == "" || status.MainHead == "" || status.LocalHead == status.MainHead {
		return nil
	}
	commits, err := s.Git.RevList(status.LocalHead + ".." + status.MainHead)
	if err != nil {
		return nil
	}
	out := make(map[string]bool, len(commits))
	for _, c := range commits {
		out[c] = true
	}
	return out
}

func preflightCurrentFiles(commitDiffFiles []string, wt *gitops.WorktreeStatusReport) []string {
	var out []string
	out = append(out, commitDiffFiles...)
	if wt != nil {
		out = append(out, wt.DirtyFiles...)
		out = append(out, wt.Untracked...)
	}
	return compactSortedStrings(out)
}

func buildPreflightResult(in preflightInput) *PreflightResult {
	res := &PreflightResult{Level: PreflightLevelOK, OKToContinue: true}
	if in.status != nil {
		res.Facts.Branch = in.status.Branch
		res.Facts.LocalHead = in.status.LocalHead
		res.Facts.MainHead = in.status.MainHead
		res.Facts.SyncStale = in.status.SyncStale
		res.Facts.ProposedCount = in.status.ProposedCount
		if in.status.NotesHealth != nil && in.status.NotesHealth.LikelyHistoryRewrite {
			res.Facts.NotesRewriteDrift = true
			res.Facts.UnreachableMainlineNotes = in.status.NotesHealth.UnreachableMainlineNotes
		}
		if in.status.ActiveIntent != nil {
			res.Facts.ActiveIntentID = in.status.ActiveIntent.IntentID
			res.Facts.ActiveBase = in.status.ActiveIntent.BaseCommit
		}
	}
	if in.worktree != nil {
		res.Facts.WorktreeStatus = in.worktree.Status
		res.Facts.DirtyFiles = compactSortedStrings(in.worktree.DirtyFiles)
		res.Facts.UntrackedFiles = compactSortedStrings(in.worktree.Untracked)
	}
	res.Facts.CurrentFiles = compactSortedStrings(in.currentFiles)
	res.Facts.CommitDiffFiles = compactSortedStrings(in.commitDiffFiles)

	addFinding := func(code, level, message string, files []string) {
		res.Findings = append(res.Findings, PreflightFinding{
			Code:    code,
			Level:   level,
			Message: message,
			Files:   compactSortedStrings(files),
		})
	}

	if in.status == nil || !in.status.Initialized {
		addFinding(PreflightFindingNotInitialized, PreflightLevelBlock,
			"Mainline is not initialized in this repository.", nil)
	} else if !in.status.IdentityConfigured {
		addFinding(PreflightFindingIdentityMissing, PreflightLevelBlock,
			"This clone has no Mainline actor identity.", nil)
	}
	if in.status != nil && in.status.SyncStale {
		addFinding(PreflightFindingSyncStale, PreflightLevelWarn,
			"Team view is stale; refresh before trusting collaboration state.", nil)
	}
	if in.status != nil && in.status.NotesHealth != nil && in.status.NotesHealth.LikelyHistoryRewrite {
		addFinding(PreflightFindingNotesRewriteDrift, PreflightLevelWarn,
			"Mainline notes look detached from main after a history rewrite or force-push; inspect before trusting proposal or coverage state.", nil)
	}
	if in.status != nil && len(in.upstreamCommits) > 0 {
		addFinding(PreflightFindingBranchDrift, PreflightLevelWarn,
			"Synced main contains commits not in local HEAD; review or rebase if they affect this work.", nil)
	}
	if in.status != nil && in.status.ActiveIntent != nil &&
		in.status.ActiveIntent.BaseCommit != "" &&
		in.status.MainHead != "" &&
		in.status.ActiveIntent.BaseCommit != in.status.MainHead &&
		len(in.upstreamCommits) > 0 {
		addFinding(PreflightFindingActiveBaseBehind, PreflightLevelBlock,
			"Active draft was started before the synced main head.", nil)
	}

	currentFiles := compactSortedStrings(in.currentFiles)
	if len(currentFiles) > 0 {
		for _, iv := range in.proposed {
			if iv.Status != domain.StatusProposed || iv.Fingerprint == nil {
				continue
			}
			if preflightFilesOverlap(currentFiles, iv.Fingerprint.FilesTouched) {
				res.Overlaps = append(res.Overlaps, preflightOverlapFromIntent(
					PreflightOverlapProposed, PreflightLevelBlock, iv, currentFiles,
				))
			}
		}
	}

	// Goal-text overlap: even when the worktree is clean (no commits
	// yet, no dirty files), we want to catch "another agent already
	// claimed this work" as soon as `mainline start` runs. File-overlap
	// has nothing to bite on at that point, so we fall back to keyword
	// match against proposed intents' title + goal text. Level=warn
	// (not block) — same goal text can legitimately mean a related-but-
	// different intent, so the agent gets a heads-up, not a refusal.
	if in.status != nil && in.status.ActiveIntent != nil {
		goalOverlaps := goalTextOverlaps(in.status.ActiveIntent, in.proposed)
		res.Overlaps = append(res.Overlaps, goalOverlaps...)
	}

	if len(currentFiles) > 0 {
		if in.view != nil && len(in.upstreamCommits) > 0 {
			for _, iv := range in.view.Intents {
				if iv.Status != domain.StatusMerged || iv.Fingerprint == nil {
					continue
				}
				commit := iv.StatusEvidence.MergedMainCommit
				if commit == "" || !in.upstreamCommits[commit] {
					continue
				}
				if preflightFilesOverlap(currentFiles, iv.Fingerprint.FilesTouched) {
					res.Overlaps = append(res.Overlaps, preflightOverlapFromIntent(
						PreflightOverlapUpstreamMerged, PreflightLevelBlock, iv, currentFiles,
					))
				}
			}
		}
	}

	if in.worktree != nil && in.worktree.Status != "clean" && len(in.commitDiffFiles) == 0 {
		files := append(append([]string{}, in.worktree.DirtyFiles...), in.worktree.Untracked...)
		addFinding(PreflightFindingDirtyWithoutCommitDiff, PreflightLevelWarn,
			"Worktree has dirty/untracked files but no committed diff; seal evidence and fingerprint will be weak until committed.", files)
	}

	res.Overlaps = compactPreflightOverlaps(res.Overlaps)
	res.RecommendedNext = preflightRecommendations(res)
	res.Level = aggregatePreflightLevel(res.Findings, res.Overlaps)
	res.OKToContinue = res.Level != PreflightLevelBlock
	return res
}

func preflightOverlapFromIntent(kind, level string, iv domain.IntentView, currentFiles []string) PreflightOverlap {
	title := iv.Goal
	if iv.Summary != nil && iv.Summary.Title != "" {
		title = iv.Summary.Title
	}
	matched := matchedOverlapFiles(currentFiles, iv.Fingerprint.FilesTouched)
	o := PreflightOverlap{
		Kind:             kind,
		Level:            level,
		IntentID:         iv.IntentID,
		Title:            title,
		Status:           string(iv.Status),
		MatchedFiles:     matched,
		Score:            len(matched),
		MergedMainCommit: iv.StatusEvidence.MergedMainCommit,
	}
	// Author surfaces only on proposed_overlap (in-flight work);
	// upstream_merged_overlap is already on main and git blame answers
	// the question better.
	if kind == PreflightOverlapProposed {
		o.AuthorID = iv.ActorID
		o.AuthorName = iv.ActorName
	}
	return o
}

func compactPreflightOverlaps(in []PreflightOverlap) []PreflightOverlap {
	if len(in) == 0 {
		return nil
	}
	sort.SliceStable(in, func(i, j int) bool {
		li, lj := preflightLevelRank(in[i].Level), preflightLevelRank(in[j].Level)
		if li != lj {
			return li > lj
		}
		if in[i].Score != in[j].Score {
			return in[i].Score > in[j].Score
		}
		ki, kj := in[i].Kind+":"+in[i].IntentID, in[j].Kind+":"+in[j].IntentID
		return ki < kj
	})
	seen := map[string]bool{}
	out := make([]PreflightOverlap, 0, len(in))
	for _, o := range in {
		key := o.Kind + ":" + o.IntentID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, o)
		if len(out) >= preflightOverlapLimit {
			break
		}
	}
	return out
}

func aggregatePreflightLevel(findings []PreflightFinding, overlaps []PreflightOverlap) string {
	level := PreflightLevelOK
	for _, f := range findings {
		level = maxPreflightLevel(level, f.Level)
	}
	for _, o := range overlaps {
		level = maxPreflightLevel(level, o.Level)
	}
	return level
}

func maxPreflightLevel(a, b string) string {
	if preflightLevelRank(b) > preflightLevelRank(a) {
		return b
	}
	return a
}

func preflightLevelRank(level string) int {
	switch level {
	case PreflightLevelBlock:
		return 2
	case PreflightLevelWarn:
		return 1
	default:
		return 0
	}
}

func preflightRecommendations(res *PreflightResult) []string {
	if res == nil {
		return nil
	}
	var out []string
	add := func(s string) {
		if s != "" {
			out = append(out, s)
		}
	}
	for _, f := range res.Findings {
		switch f.Code {
		case PreflightFindingNotInitialized:
			add("mainline init --actor-name \"<your name>\"")
		case PreflightFindingIdentityMissing:
			add("mainline init --actor-name \"<your name>\"")
		case PreflightFindingSyncStale:
			add("mainline sync")
		case PreflightFindingNotesRewriteDrift:
			add("mainline doctor --notes --json")
		case PreflightFindingBranchDrift, PreflightFindingActiveBaseBehind:
			add("review synced main changes; rebase or merge if they affect this work")
		case PreflightFindingDirtyWithoutCommitDiff:
			add("commit the intended code diff before seal --prepare, or keep this as a dirty-work warning")
		}
	}
	for _, o := range res.Overlaps {
		add("mainline show " + o.IntentID + " --json")
	}
	if len(res.Overlaps) > 0 {
		add("if overlap is real, run mainline check or ask for human judgment before continuing")
	}
	return dedupeStrings(out)
}

func compactSortedStrings(in []string) []string {
	out := dedupeStrings(in)
	sort.Strings(out)
	return out
}

func matchedOverlapFiles(a, b []string) []string {
	set := map[string]bool{}
	for _, s := range b {
		set[s] = true
	}
	var out []string
	for _, s := range a {
		if set[s] {
			out = append(out, s)
		}
	}
	return compactSortedStrings(out)
}

func preflightFilesOverlap(a, b []string) bool {
	return len(matchedOverlapFiles(a, b)) > 0
}

// goalTextOverlaps returns proposed-overlap warnings derived purely
// from the active draft's goal text. Reuses keywordsFromText (same
// tokenisation as conflict + context-retrieval) so the three signals
// agree on what "the same words" means. Self-exclusion is by IntentID
// — re-running preflight on your own draft never warns you about
// yourself. The hit rate threshold is on the active draft's keyword
// count (not the candidate's), so a 4-keyword goal needs at least
// 2 of those 4 to land somewhere on a candidate.
func goalTextOverlaps(active *domain.DraftIntent, proposed []domain.IntentView) []PreflightOverlap {
	if active == nil {
		return nil
	}
	goal := strings.TrimSpace(active.Goal)
	if goal == "" {
		return nil
	}
	keywords := keywordsFromText(goal)
	if len(keywords) < preflightGoalOverlapMinKeywords {
		return nil
	}
	required := int(float64(len(keywords))*preflightGoalOverlapMinHitRate + 0.5)
	if required < 1 {
		required = 1
	}

	var out []PreflightOverlap
	for _, iv := range proposed {
		if iv.Status != domain.StatusProposed {
			continue
		}
		if iv.IntentID == active.IntentID {
			continue
		}
		title := iv.Goal
		if iv.Summary != nil && iv.Summary.Title != "" {
			title = iv.Summary.Title
		}
		hay := strings.ToLower(strings.TrimSpace(iv.Goal + " " + title))
		if hay == "" {
			continue
		}
		hits := countKeywordHits(keywords, hay)
		if hits < required {
			continue
		}
		matched := matchedKeywords(keywords, hay)
		out = append(out, PreflightOverlap{
			Kind:            PreflightOverlapGoalText,
			Level:           PreflightLevelWarn,
			IntentID:        iv.IntentID,
			Title:           title,
			Status:          string(iv.Status),
			MatchedKeywords: matched,
			Score:           hits,
			AuthorID:        iv.ActorID,
			AuthorName:      iv.ActorName,
		})
	}
	return out
}
