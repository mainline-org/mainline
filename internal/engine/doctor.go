package engine

import (
	"fmt"
	"time"

	"mainline/internal/domain"
)

type DoctorOptions struct {
	Fix        bool
	StaleAfter time.Duration
}

type DoctorResult struct {
	CheckedDrafts int                  `json:"checked_drafts"`
	OrphanDrafts  []DoctorDraftFinding `json:"orphan_drafts,omitempty"`
	StaleDrafts   []DoctorDraftFinding `json:"stale_drafts,omitempty"`
	DeletedDrafts []string             `json:"deleted_drafts,omitempty"`
}

type DoctorDraftFinding struct {
	IntentID       string              `json:"intent_id"`
	Status         domain.IntentStatus `json:"status"`
	Thread         string              `json:"thread,omitempty"`
	GitBranch      string              `json:"git_branch,omitempty"`
	Goal           string              `json:"goal,omitempty"`
	CreatedAt      string              `json:"created_at,omitempty"`
	LastModifiedAt string              `json:"last_modified_at,omitempty"`
	Reason         string              `json:"reason"`
}

func (s *Service) Doctor(opts DoctorOptions) (*DoctorResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}
	if opts.StaleAfter <= 0 {
		opts.StaleAfter = 24 * time.Hour
	}

	currentBranch, _ := s.Git.CurrentBranch()
	now := time.Now()
	result := &DoctorResult{}

	ids, err := s.Store.ListDrafts()
	if err != nil {
		return nil, fmt.Errorf("list drafts: %w", err)
	}
	result.CheckedDrafts = len(ids)

	for _, id := range ids {
		draft, err := s.Store.ReadDraft(id)
		if err != nil || draft == nil {
			continue
		}
		if draft.Status != domain.StatusDrafting {
			continue
		}

		finding := doctorFindingFromDraft(draft)
		branchMissing := draft.GitBranch != "" && !s.Git.BranchExists(draft.GitBranch)
		if branchMissing && draft.GitBranch != currentBranch {
			finding.Reason = "drafting intent points at a missing local branch"
			result.OrphanDrafts = append(result.OrphanDrafts, finding)
			continue
		}

		if staleDraft(draft, now, opts.StaleAfter) {
			finding.Reason = fmt.Sprintf("drafting intent has not changed for at least %s", opts.StaleAfter)
			result.StaleDrafts = append(result.StaleDrafts, finding)
		}
	}

	if opts.Fix {
		for _, finding := range result.OrphanDrafts {
			if err := s.Store.DeleteDraft(finding.IntentID); err != nil {
				return nil, fmt.Errorf("delete draft %s: %w", finding.IntentID, err)
			}
			result.DeletedDrafts = append(result.DeletedDrafts, finding.IntentID)
		}
	}

	return result, nil
}

func doctorFindingFromDraft(d *domain.DraftIntent) DoctorDraftFinding {
	return DoctorDraftFinding{
		IntentID:       d.IntentID,
		Status:         d.Status,
		Thread:         d.Thread,
		GitBranch:      d.GitBranch,
		Goal:           d.Goal,
		CreatedAt:      d.CreatedAt,
		LastModifiedAt: d.LastModifiedAt,
	}
}

func staleDraft(d *domain.DraftIntent, now time.Time, staleAfter time.Duration) bool {
	t, err := time.Parse(time.RFC3339, d.LastModifiedAt)
	if err != nil {
		t, err = time.Parse(time.RFC3339, d.CreatedAt)
		if err != nil {
			return false
		}
	}
	return now.Sub(t) >= staleAfter
}
