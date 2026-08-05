package engine

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/gitops"
	"github.com/mainline-org/mainline/internal/storage"
)

// WorktreeDraft is a local draft discovered in a sibling git worktree.
// It is local diagnostic state, not shared/proposed Mainline state.
type WorktreeDraft struct {
	IntentID           string                     `json:"intent_id"`
	Goal               string                     `json:"goal,omitempty"`
	Status             string                     `json:"status,omitempty"`
	GitBranch          string                     `json:"git_branch,omitempty"`
	Thread             string                     `json:"thread,omitempty"`
	WorktreePath       string                     `json:"worktree_path"`
	DraftPath          string                     `json:"draft_path"`
	TurnCount          int                        `json:"turn_count,omitempty"`
	LastModifiedAt     string                     `json:"last_modified_at,omitempty"`
	Stale              bool                       `json:"stale,omitempty"`
	PartialFingerprint *domain.PartialFingerprint `json:"-"`
}

const siblingDraftStaleAfter = 72 * time.Hour

// SiblingDraftsForCLI exposes local draft visibility for status/hub callers.
func (s *Service) SiblingDraftsForCLI() []WorktreeDraft {
	shared := map[string]bool{}
	if view, _ := s.Store.ReadMainlineView(); view != nil {
		for _, iv := range view.Intents {
			shared[iv.IntentID] = true
		}
	}
	return s.collectSiblingDrafts("", shared)
}

func (s *Service) StoreDraftsDirForCLI() string {
	if s == nil || s.Git == nil {
		return ""
	}
	return filepath.Join(s.Git.RepoRoot, ".ml-cache", "drafts")
}

func (s *Service) findSiblingDrafts(intentID string) []WorktreeDraft {
	return s.collectSiblingDrafts(intentID, nil)
}

func (s *Service) collectSiblingDrafts(intentID string, skipIDs map[string]bool) []WorktreeDraft {
	if s == nil || s.Git == nil {
		return nil
	}
	worktrees, err := s.Git.Worktrees()
	if err != nil || len(worktrees) == 0 {
		return nil
	}
	current := canonicalWorktreePath(s.Git.RepoRoot)
	var out []WorktreeDraft
	for _, wt := range worktrees {
		if wt.Path == "" || wt.Bare {
			continue
		}
		wtPath := filepath.Clean(wt.Path)
		if canonicalWorktreePath(wtPath) == current {
			continue
		}
		st := storage.New(wtPath, nil)
		ids := []string{intentID}
		if intentID == "" {
			draft, err := st.FindActiveDraft(wt.Branch)
			if err != nil || draft == nil {
				continue
			}
			ids = []string{draft.IntentID}
		}
		for _, id := range ids {
			if skipIDs[id] {
				continue
			}
			d, err := st.ReadDraft(id)
			if err != nil || d == nil || !visibleLocalDraftStatus(d.Status) {
				continue
			}
			turns, _ := st.ReadTurns(id)
			draftWithTurns := *d
			draftWithTurns.Turns = turns
			partial := PartialFingerprintFromDraft(&draftWithTurns)
			partial.FilesTouched = siblingWorktreeFiles(gitops.NewFromRoot(wtPath), wt.Head, d.BaseCommit, partial.FilesTouched)
			partial.Subsystems = subsystemsFromFiles(partial.FilesTouched)
			updated := d.LastModifiedAt
			if updated == "" {
				updated = d.CreatedAt
			}
			out = append(out, WorktreeDraft{
				IntentID:           d.IntentID,
				Goal:               d.Goal,
				Status:             string(d.Status),
				GitBranch:          d.GitBranch,
				Thread:             d.Thread,
				WorktreePath:       wtPath,
				DraftPath:          filepath.Join(wtPath, ".ml-cache", "drafts", d.IntentID+".json"),
				TurnCount:          len(turns),
				LastModifiedAt:     updated,
				Stale:              staleSiblingDraft(updated, time.Now()),
				PartialFingerprint: partial,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].WorktreePath != out[j].WorktreePath {
			return out[i].WorktreePath < out[j].WorktreePath
		}
		if out[i].LastModifiedAt != out[j].LastModifiedAt {
			return out[i].LastModifiedAt > out[j].LastModifiedAt
		}
		return out[i].IntentID < out[j].IntentID
	})
	return out
}

func staleSiblingDraft(updated string, now time.Time) bool {
	parsed, err := time.Parse(time.RFC3339, updated)
	return err == nil && now.Sub(parsed) > siblingDraftStaleAfter
}

func siblingWorktreeFiles(git *gitops.Git, head, base string, recorded []string) []string {
	files := append([]string(nil), recorded...)
	if git == nil {
		return compactSortedStrings(files)
	}
	if report, err := git.WorktreeStatus(); err == nil && report != nil {
		files = append(files, report.DirtyFiles...)
		files = append(files, report.Untracked...)
	}
	if base != "" && head != "" && base != head {
		if committed, err := git.DiffFilesAgainst(base, head); err == nil {
			files = append(files, committed...)
		}
	}
	return compactSortedStrings(files)
}

func canonicalWorktreePath(path string) string {
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(resolved)
	}
	return clean
}

func visibleLocalDraftStatus(status domain.IntentStatus) bool {
	return status == domain.StatusDrafting ||
		status == domain.StatusSealedLocal ||
		status == domain.StatusProposed
}
