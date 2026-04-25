package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"mainline/internal/core"
	"mainline/internal/domain"
)

// -----------------------------------------------------------
// Log
// -----------------------------------------------------------

type LogResult struct {
	Intents []LogIntentEntry `json:"intents"`
}

type LogIntentEntry struct {
	IntentID   string              `json:"intent_id"`
	Status     domain.IntentStatus `json:"status"`
	Title      string              `json:"title,omitempty"`
	Goal       string              `json:"goal,omitempty"`
	Thread     string              `json:"thread,omitempty"`
	SealedAt   string              `json:"sealed_at,omitempty"`
	ActivityAt string              `json:"activity_at,omitempty"`
	Author     string              `json:"author,omitempty"`
	ActorID    string              `json:"actor_id,omitempty"`
	ActorName  string              `json:"actor_name,omitempty"`
	// Check is a one-character marker for the intent's most recent
	// phase2 check verdict, suitable for inline log rendering:
	//   ""  no check yet
	//   "ok" no_conflict, no human review needed
	//   "!"  has_conflict
	//   "?"  needs_human_review
	Check string `json:"check,omitempty"`
}

func (s *Service) Log(limit int, statusFilter ...string) (*LogResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	rawStatusFilter := ""
	if len(statusFilter) > 0 {
		rawStatusFilter = statusFilter[0]
	}
	filter, err := normalizeLogStatusFilter(rawStatusFilter)
	if err != nil {
		return nil, err
	}

	cfg, _ := s.getTeamConfig()
	if limit <= 0 && cfg != nil {
		limit = cfg.Log.DefaultLimit
	}
	if limit <= 0 {
		limit = 20
	}

	result := &LogResult{}
	seenIntentIDs := make(map[string]bool)

	// Collect from mainline view
	view, _ := s.Store.ReadMainlineView()
	if view != nil {
		for _, iv := range view.Intents {
			seenIntentIDs[iv.IntentID] = true
			entry := LogIntentEntry{
				IntentID:   iv.IntentID,
				Status:     iv.Status,
				Goal:       iv.Goal,
				Thread:     iv.Thread,
				SealedAt:   iv.SealedAt,
				ActivityAt: s.intentViewActivityAt(iv),
				Author:     authorName(iv.ActorName, iv.ActorID),
				ActorID:    iv.ActorID,
				ActorName:  iv.ActorName,
				Check:      checkMarker(iv.LastCheck),
			}
			if iv.Summary != nil {
				entry.Title = iv.Summary.Title
			}
			if logStatusMatches(entry.Status, filter) {
				result.Intents = append(result.Intents, entry)
			}
		}
	}

	// Collect from local drafts
	identity, _ := s.getIdentity()
	draftAuthor := s.actorDisplayName(identity)
	drafts, _ := s.Store.ListDrafts()
	for _, id := range drafts {
		d, _ := s.Store.ReadDraft(id)
		if d == nil {
			continue
		}
		if !seenIntentIDs[id] {
			entry := LogIntentEntry{
				IntentID:   d.IntentID,
				Status:     d.Status,
				Goal:       d.Goal,
				Thread:     d.Thread,
				ActivityAt: draftActivityAt(d),
				Author:     draftAuthor,
			}
			if identity != nil {
				entry.ActorID = identity.ActorID
				entry.ActorName = draftAuthor
			}
			if logStatusMatches(entry.Status, filter) {
				result.Intents = append(result.Intents, entry)
			}
		}
	}

	sort.SliceStable(result.Intents, func(i, j int) bool {
		left := result.Intents[i]
		right := result.Intents[j]
		leftTime, leftOK := parseLogActivityTime(left.ActivityAt)
		rightTime, rightOK := parseLogActivityTime(right.ActivityAt)
		if leftOK != rightOK {
			return leftOK
		}
		if leftOK && !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		if left.ActivityAt != right.ActivityAt {
			return left.ActivityAt > right.ActivityAt
		}
		return left.IntentID < right.IntentID
	})

	if len(result.Intents) > limit {
		result.Intents = result.Intents[:limit]
	}

	return result, nil
}

func authorName(actorName, actorID string) string {
	if actorName != "" {
		return actorName
	}
	return actorID
}

func normalizeLogStatusFilter(status string) (domain.IntentStatus, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" || status == "all" {
		return "", nil
	}
	switch domain.IntentStatus(status) {
	case domain.StatusDrafting,
		domain.StatusSealedLocal,
		domain.StatusProposed,
		domain.StatusMerged,
		domain.StatusAbandoned,
		domain.StatusSuperseded,
		domain.StatusReverted:
		return domain.IntentStatus(status), nil
	default:
		return "", domain.NewError(domain.ErrInvalidInput,
			fmt.Sprintf("invalid status %q", status))
	}
}

func logStatusMatches(status domain.IntentStatus, filter domain.IntentStatus) bool {
	return filter == "" || status == filter
}

// checkMarker turns a CheckSummary into a one-token marker for inline
// log rendering. The empty string means no check has been recorded.
func checkMarker(lc *domain.CheckSummary) string {
	if lc == nil {
		return ""
	}
	if lc.NeedsHumanReview {
		return "?"
	}
	if lc.HasConflict {
		return "!"
	}
	return "ok"
}

func (s *Service) intentViewActivityAt(iv domain.IntentView) string {
	switch iv.Status {
	case domain.StatusMerged:
		if iv.StatusEvidence.MergedMainCommit != "" {
			if date, err := s.Git.CommitDate(iv.StatusEvidence.MergedMainCommit); err == nil {
				return date
			}
		}
	case domain.StatusReverted:
		if iv.StatusEvidence.RevertedMainCommit != "" {
			if date, err := s.Git.CommitDate(iv.StatusEvidence.RevertedMainCommit); err == nil {
				return date
			}
		}
	}
	if iv.SealedAt != "" {
		return iv.SealedAt
	}
	return iv.ViewRebuiltAt
}

func draftActivityAt(d *domain.DraftIntent) string {
	if d.LastModifiedAt != "" {
		return d.LastModifiedAt
	}
	return d.CreatedAt
}

func parseLogActivityTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// -----------------------------------------------------------
// Context
// -----------------------------------------------------------

type ContextResult struct {
	RepoRoot        string          `json:"repo_root"`
	Branch          string          `json:"branch"`
	MainBranch      string          `json:"main_branch"`
	ActorID         string          `json:"actor_id"`
	ActiveIntent    *ContextIntent  `json:"active_intent,omitempty"`
	ProposedIntents []ContextIntent `json:"proposed_intents"`
	MergedRecent    []ContextIntent `json:"merged_recent"`
}

type ContextIntent struct {
	IntentID string `json:"intent_id"`
	Title    string `json:"title,omitempty"`
	Goal     string `json:"goal,omitempty"`
	Status   string `json:"status"`
	Thread   string `json:"thread,omitempty"`
}

func (s *Service) Context() (*ContextResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	branch, _ := s.Git.CurrentBranch()
	cfg, _ := s.getTeamConfig()
	identity, _ := s.getIdentity()

	result := &ContextResult{
		RepoRoot:   s.Git.RepoRoot,
		Branch:     branch,
		MainBranch: cfg.Mainline.MainBranch,
	}
	if identity != nil {
		result.ActorID = identity.ActorID
	}

	// Active draft
	draft, _ := s.Store.FindActiveDraft(branch)
	if draft != nil {
		result.ActiveIntent = &ContextIntent{
			IntentID: draft.IntentID,
			Goal:     draft.Goal,
			Status:   string(draft.Status),
			Thread:   draft.Thread,
		}
	}

	// Proposed & merged from view
	view, _ := s.Store.ReadMainlineView()
	if view != nil {
		for _, iv := range view.Intents {
			ci := ContextIntent{
				IntentID: iv.IntentID,
				Status:   string(iv.Status),
				Thread:   iv.Thread,
				Goal:     iv.Goal,
			}
			if iv.Summary != nil {
				ci.Title = iv.Summary.Title
			}
			switch iv.Status {
			case domain.StatusProposed:
				result.ProposedIntents = append(result.ProposedIntents, ci)
			case domain.StatusMerged:
				result.MergedRecent = append(result.MergedRecent, ci)
			}
		}
	}

	return result, nil
}

// -----------------------------------------------------------
// ListProposals
// -----------------------------------------------------------

type ListProposalsResult struct {
	Proposals []ContextIntent `json:"proposals"`
}

func (s *Service) ListProposals() (*ListProposalsResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	idx, _ := s.Store.ReadProposedIndex()
	result := &ListProposalsResult{}
	if idx != nil {
		for _, iv := range idx.Proposed {
			ci := ContextIntent{
				IntentID: iv.IntentID,
				Status:   string(iv.Status),
				Thread:   iv.Thread,
				Goal:     iv.Goal,
			}
			if iv.Summary != nil {
				ci.Title = iv.Summary.Title
			}
			result.Proposals = append(result.Proposals, ci)
		}
	}
	return result, nil
}

// -----------------------------------------------------------
// Thread operations
// -----------------------------------------------------------

type ThreadNewResult struct {
	Name      string `json:"name"`
	GitBranch string `json:"git_branch"`
}

func (s *Service) ThreadNew(name string) (*ThreadNewResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	cfg, _ := s.getTeamConfig()
	base, _ := s.Git.HeadCommit()

	gitBranch := name
	if !s.Git.BranchExists(gitBranch) {
		// Create branch from main
		mainHead := s.Git.ReadRef("refs/heads/" + cfg.Mainline.MainBranch)
		if mainHead == "" {
			mainHead = base
		}
		s.Git.CreateBranch(gitBranch, mainHead)
	}

	thread := &domain.Thread{
		Name:       name,
		GitBranch:  gitBranch,
		BaseCommit: base,
		Status:     "active",
		CreatedAt:  core.Now(),
	}
	if err := s.Store.WriteThread(thread); err != nil {
		return nil, err
	}

	return &ThreadNewResult{Name: name, GitBranch: gitBranch}, nil
}

func (s *Service) ThreadList() ([]domain.Thread, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}
	return s.Store.ListThreads()
}

func (s *Service) ThreadClose(name string) error {
	if err := s.requireInit(); err != nil {
		return err
	}
	t, err := s.Store.ReadThread(name)
	if err != nil {
		return domain.NewError(domain.ErrInvalidInput, fmt.Sprintf("thread %s not found", name))
	}
	t.Status = "closed"
	t.ClosedAt = core.Now()
	return s.Store.WriteThread(t)
}

// -----------------------------------------------------------
// Canonical Hash
// -----------------------------------------------------------

func (s *Service) CanonicalHashIntent(intentID string) (string, error) {
	if err := s.requireInit(); err != nil {
		return "", err
	}

	// Try view first
	view, _ := s.Store.ReadMainlineView()
	if view != nil {
		for _, iv := range view.Intents {
			if iv.IntentID == intentID {
				return core.CanonicalHash(iv)
			}
		}
	}

	// Try draft
	draft, _ := s.Store.ReadDraft(intentID)
	if draft != nil {
		return core.CanonicalHash(draft)
	}

	return "", domain.NewError(domain.ErrInvalidInput, fmt.Sprintf("intent %s not found", intentID))
}
