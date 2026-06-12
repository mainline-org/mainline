package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mainline-org/mainline/internal/domain"
)

type PullRequestImportOptions struct {
	PRNumber int
	ForkURL  string
	HeadRef  string
	HeadSHA  string
	ActorID  string
}

type PullRequestImportCandidate struct {
	ActorID      string   `json:"actor_id"`
	IntentID     string   `json:"intent_id"`
	SourceRef    string   `json:"source_ref"`
	SourceHead   string   `json:"source_head"`
	GitBranch    string   `json:"git_branch,omitempty"`
	CodeCommit   string   `json:"code_commit,omitempty"`
	CodeTree     string   `json:"code_tree,omitempty"`
	MatchReasons []string `json:"match_reasons,omitempty"`
	Score        int      `json:"score"`
}

type PullRequestImportResult struct {
	Status       string                       `json:"status"`
	PRNumber     int                          `json:"pr_number,omitempty"`
	ForkURL      string                       `json:"fork_url"`
	HeadRef      string                       `json:"head_ref,omitempty"`
	HeadSHA      string                       `json:"head_sha,omitempty"`
	HeadTree     string                       `json:"head_tree,omitempty"`
	CandidateNum int                          `json:"candidate_count"`
	Candidates   []PullRequestImportCandidate `json:"candidates,omitempty"`
	Selected     *PullRequestImportCandidate  `json:"selected,omitempty"`
	Import       *ActorLogImportResult        `json:"import,omitempty"`
	Warnings     []string                     `json:"warnings,omitempty"`
}

func (s *Service) ImportPullRequestIntent(opts PullRequestImportOptions) (*PullRequestImportResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}
	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}

	forkURL := strings.TrimSpace(opts.ForkURL)
	if forkURL == "" {
		return nil, domain.NewError(domain.ErrInvalidInput, "pr-import requires --fork-url")
	}
	headRef := strings.TrimSpace(opts.HeadRef)
	headSHA := strings.TrimSpace(opts.HeadSHA)
	actorID := strings.TrimSpace(opts.ActorID)
	if headRef == "" && headSHA == "" {
		return nil, domain.NewError(domain.ErrInvalidInput, "pr-import requires --head-ref or --head-sha")
	}

	result := &PullRequestImportResult{
		Status:   "no_match",
		PRNumber: opts.PRNumber,
		ForkURL:  forkURL,
		HeadRef:  headRef,
		HeadSHA:  headSHA,
	}

	headTree, warnings := s.preparePullRequestHead(forkURL, headRef, headSHA, opts.PRNumber)
	result.HeadTree = headTree
	result.Warnings = append(result.Warnings, warnings...)

	remoteRefs, err := s.discoverPullRequestActorRefs(forkURL, cfg.Mainline.ActorLogPrefix, actorID)
	if err != nil {
		return nil, err
	}
	if len(remoteRefs) == 0 {
		result.Status = "no_actor_logs"
		return result, nil
	}

	var candidates []PullRequestImportCandidate
	for _, rr := range remoteRefs {
		importRef := "refs/mainline/imports/" + rr.ActorID + "/log"
		refspec := "+" + rr.SourceRef + ":" + importRef
		if err := s.Git.Fetch(forkURL, refspec); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("fetch actor log %s from %s failed: %v", rr.SourceRef, forkURL, err))
			continue
		}
		rawEvents, err := s.Store.ReadActorLogEventsFromRef(importRef)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("read actor log %s failed: %v", importRef, err))
			continue
		}
		for _, raw := range rawEvents {
			var base domain.BaseEvent
			if err := json.Unmarshal(raw, &base); err != nil {
				continue
			}
			if base.EventType != domain.EventIntentSealed || base.ActorID != rr.ActorID {
				continue
			}
			var evt domain.IntentSealedEvent
			if err := json.Unmarshal(raw, &evt); err != nil {
				continue
			}
			score, reasons := scorePullRequestIntentMatch(evt, headRef, headSHA, headTree)
			if score == 0 {
				continue
			}
			candidates = append(candidates, PullRequestImportCandidate{
				ActorID:      rr.ActorID,
				IntentID:     evt.IntentID,
				SourceRef:    rr.SourceRef,
				SourceHead:   rr.SourceHead,
				GitBranch:    evt.GitBranch,
				CodeCommit:   evt.CodeCommit,
				CodeTree:     evt.CodeTree,
				MatchReasons: reasons,
				Score:        score,
			})
		}
	}

	result.Candidates = candidates
	result.CandidateNum = len(candidates)
	if len(candidates) == 0 {
		result.Status = "no_match"
		return result, nil
	}

	top := topPullRequestImportCandidates(candidates)
	if len(top) != 1 {
		result.Status = "ambiguous"
		result.Candidates = top
		result.CandidateNum = len(top)
		return result, nil
	}

	selected := top[0]
	result.Selected = &selected
	importResult, err := s.ImportActorLog(ActorLogImportOptions{
		ActorID:            selected.ActorID,
		Remote:             forkURL,
		ExpectedSourceHead: selected.SourceHead,
	})
	if err != nil {
		return nil, err
	}
	result.Import = importResult
	if importResult.Accepted {
		result.Status = "imported"
	} else {
		result.Status = "already_imported"
	}
	return result, nil
}

type pullRequestActorRef struct {
	ActorID    string
	SourceRef  string
	SourceHead string
}

func (s *Service) discoverPullRequestActorRefs(forkURL, prefix, actorID string) ([]pullRequestActorRef, error) {
	if actorID != "" {
		sourceRef := domain.ActorLogRef(actorID, prefix)
		refs, err := s.Git.LsRemote(forkURL, sourceRef)
		if err != nil {
			return nil, domain.NewRecoverableError(domain.ErrSyncFailed,
				fmt.Sprintf("list actor log %s from %s failed: %v", sourceRef, forkURL, err),
				"check the fork URL",
				"ask the contributor to run mainline publish --remote <fork>")
		}
		for _, ref := range refs {
			if ref.Ref == sourceRef {
				return []pullRequestActorRef{{ActorID: actorID, SourceRef: sourceRef, SourceHead: ref.Hash}}, nil
			}
		}
		return nil, nil
	}

	pattern := strings.TrimRight(prefix, "/") + "/*/log"
	refs, err := s.Git.LsRemote(forkURL, pattern)
	if err != nil {
		return nil, domain.NewRecoverableError(domain.ErrSyncFailed,
			fmt.Sprintf("list actor logs from %s failed: %v", forkURL, err),
			"check the fork URL",
			"ask the contributor to run mainline publish --remote <fork>")
	}
	out := make([]pullRequestActorRef, 0, len(refs))
	for _, ref := range refs {
		id := actorIDFromRef(ref.Ref, prefix)
		if id == "" {
			continue
		}
		out = append(out, pullRequestActorRef{ActorID: id, SourceRef: ref.Ref, SourceHead: ref.Hash})
	}
	return out, nil
}

func actorIDFromRef(ref, prefix string) string {
	prefix = strings.TrimRight(prefix, "/") + "/"
	if !strings.HasPrefix(ref, prefix) || !strings.HasSuffix(ref, "/log") {
		return ""
	}
	id := strings.TrimSuffix(strings.TrimPrefix(ref, prefix), "/log")
	if id == "" || strings.Contains(id, "/") {
		return ""
	}
	return id
}

func (s *Service) preparePullRequestHead(forkURL, headRef, headSHA string, prNumber int) (string, []string) {
	var warnings []string
	if headRef != "" {
		importRef := pullRequestHeadImportRef(headRef, prNumber)
		if err := s.Git.Fetch(forkURL, "+refs/heads/"+headRef+":"+importRef); err != nil {
			warnings = append(warnings, fmt.Sprintf("fetch PR head branch %s from %s failed: %v", headRef, forkURL, err))
		} else if headSHA == "" {
			headSHA = s.Git.ReadRef(importRef)
		}
	}
	if headSHA == "" {
		return "", warnings
	}
	tree, err := s.Git.CommitTreeHash(headSHA)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("read PR head tree for %s failed: %v", headSHA, err))
		return "", warnings
	}
	return tree, warnings
}

func pullRequestHeadImportRef(headRef string, prNumber int) string {
	base := "refs/mainline/imports/pr-heads/"
	if prNumber > 0 {
		return fmt.Sprintf("%spr-%d", base, prNumber)
	}
	return base + sanitizeImportedBranchRef(headRef)
}

func scorePullRequestIntentMatch(evt domain.IntentSealedEvent, headRef, headSHA, headTree string) (int, []string) {
	var score int
	var reasons []string
	codeEvidenceAvailable := headSHA != "" || headTree != ""
	codeMatched := false
	if headSHA != "" && evt.CodeCommit == headSHA {
		score += 100
		codeMatched = true
		reasons = append(reasons, "code_commit")
	}
	if headTree != "" && evt.CodeTree == headTree {
		score += 60
		codeMatched = true
		reasons = append(reasons, "code_tree")
	}
	if codeEvidenceAvailable && !codeMatched {
		return 0, nil
	}
	if headRef != "" && evt.GitBranch == headRef {
		score += 20
		reasons = append(reasons, "git_branch")
	}
	return score, reasons
}

func topPullRequestImportCandidates(candidates []PullRequestImportCandidate) []PullRequestImportCandidate {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].ActorID != candidates[j].ActorID {
			return candidates[i].ActorID < candidates[j].ActorID
		}
		return candidates[i].IntentID < candidates[j].IntentID
	})
	maxScore := candidates[0].Score
	var top []PullRequestImportCandidate
	for _, c := range candidates {
		if c.Score != maxScore {
			break
		}
		top = append(top, c)
	}
	return top
}
