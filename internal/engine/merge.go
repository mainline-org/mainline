package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mainline-org/mainline/internal/core"
	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/gitops"
)

// -----------------------------------------------------------
// Publish
// -----------------------------------------------------------

type PublishResult struct {
	IntentID string `json:"intent_id"`
	Ref      string `json:"ref"`
	Pushed   bool   `json:"pushed"`
}

func (s *Service) Publish(intentID string) (*PublishResult, error) {
	if _, err := s.requireIdentity(); err != nil {
		return nil, err
	}

	if intentID == "" {
		branch, _ := s.Git.CurrentBranch()
		drafts, _ := s.Store.ListDrafts()
		for _, id := range drafts {
			d, _ := s.Store.ReadDraft(id)
			if d != nil && d.GitBranch == branch && d.Status == domain.StatusSealedLocal {
				intentID = id
				break
			}
		}
	}

	if intentID == "" {
		return nil, domain.NewRecoverableError(domain.ErrNoActiveIntent,
			"no sealed intent found to publish",
			"mainline seal --prepare",
		)
	}

	identity, err := s.getIdentity()
	if err != nil {
		return nil, err
	}
	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}

	ref := s.Store.ActorLogRef(identity.ActorID, cfg.Mainline.ActorLogPrefix)

	draft, err := s.Store.ReadDraft(intentID)
	if err != nil || draft == nil {
		return nil, domain.NewError(domain.ErrNoActiveIntent,
			fmt.Sprintf("intent %s not found", intentID))
	}
	if draft.Status != domain.StatusSealedLocal && draft.Status != domain.StatusProposed {
		return nil, domain.NewError(domain.ErrInvalidStatus,
			fmt.Sprintf("intent %s is in status %s, expected sealed_local or proposed", intentID, draft.Status))
	}

	pushed := false
	if s.Git.HasRemote(s.remoteName()) {
		refspec := fmt.Sprintf("%s:%s", ref, ref)
		if err := s.Git.Push(s.remoteName(), refspec); err != nil {
			return nil, domain.NewRecoverableError(domain.ErrPublishFailed,
				fmt.Sprintf("failed to push actor log %s: %v", ref, err),
				"check remote access",
				"retry mainline publish --intent "+intentID,
			)
		}
		pushed = true
	}

	if pushed && draft.Status == domain.StatusSealedLocal {
		draft.Status = domain.StatusProposed
		draft.LastModifiedAt = core.Now()
		_ = s.Store.WriteDraft(draft)
	}

	return &PublishResult{
		IntentID: intentID,
		Ref:      ref,
		Pushed:   pushed,
	}, nil
}

// -----------------------------------------------------------
// Pin  (formerly Reconcile — see Patch 7 in the rc4 spec patch).
// -----------------------------------------------------------
//
// Naming: at the user-facing layer this operation is now called Pin —
// the action is "pin an intent to a main commit" via a git note. The
// on-disk via values written by future calls reflect that
// (pin_auto / pin_explicit). Older notes still on the ref carry the
// historical reconcile_auto / reconcile_manual / reconcile / manual
// values; sync.normaliseVia maps every flavour onto the view-layer
// merged_via=pin bucket, so readers do not need to care which name
// produced the note.

// PinnedCommit records one (intent, commit, strategy) triple produced
// by Pin. Strategy is the rule that won (see CommitNote.MatchStrategy).
type PinnedCommit struct {
	IntentID      string `json:"intent_id"`
	Commit        string `json:"commit"`
	MatchStrategy string `json:"match_strategy"`
}

type PinResult struct {
	Pinned    int            `json:"pinned"`
	IntentIDs []string       `json:"intent_ids"`
	Links     []PinnedCommit `json:"links,omitempty"`
}

// pinStrategies is the priority-ordered list of rules tried by Pin.
// tree_hash is first because squash merge preserves the tree exactly,
// and it is what GitHub's web UI does by default — the case Pin must
// handle to be useful in practice.
//
// `merge_parent` checks whether the intent's code_commit is the
// second parent of a merge commit on main. This catches --no-ff
// merges where GitHub creates a merge commit and the branch tip
// becomes the second parent.
//
// `subject` sits between merge_parent and branch_in_message to handle
// the "rebase before merge" case: a feature branch with multiple
// commits gets rebased on origin/main right before merge, every
// commit's tree AND hash change, but the subject line stays —
// `git rebase` does not edit messages by default.
//
// `branch_in_message` parses the canonical GitHub merge commit header
// "Merge pull request #N from owner/branch" and compares the branch
// name against the intent's GitBranch. This catches merge commits
// even when tree_hash fails due to conflict resolution.
//
// `goal_text` is the broadest match: intent goal as substring of
// any main commit message. Least precise, tried last among git-based
// strategies.
//
// After the git-native cascade, ghPRPostPass runs as a separate step
// for any remaining unmatched proposed intents, using `gh pr list` to
// resolve branch→mergeCommit via the GitHub API.
var pinStrategies = []string{
	"tree_hash",
	"commit_hash",
	"merge_parent",
	"subject",
	"branch_in_message",
	"goal_text",
}

// Pin scans every proposed intent in the materialised view and tries
// to associate it with a main-branch commit using a cascade of rules
// (tree_hash → commit_hash → goal_text). The first matching commit wins.
//
// Pin is not restricted to intents owned by the calling actor: notes
// live on shared main commits and any teammate may attach one. The
// note's added_by field still records who performed the pin so the
// audit trail is preserved.
func (s *Service) Pin() (*PinResult, error) {
	identity, err := s.requireIdentity()
	if err != nil {
		return nil, err
	}
	cfg, _ := s.getTeamConfig()

	view, _ := s.Store.ReadMainlineView()
	if view == nil {
		return &PinResult{}, nil
	}

	mainRef := s.syncedMainRef(cfg.Mainline.MainBranch)
	entries, _ := s.Git.LogOneline(mainRef, cfg.Check.Lookback)

	hashSeen := map[string]bool{}
	hashes := make([]string, 0, len(entries))
	appendHash := func(hash string) {
		if hash == "" || hashSeen[hash] {
			return
		}
		hashSeen[hash] = true
		hashes = append(hashes, hash)
	}
	for _, e := range entries {
		appendHash(e.Hash)
	}
	for _, iv := range view.Intents {
		if iv.Status != domain.StatusProposed && iv.Status != domain.StatusMerged {
			continue
		}
		for _, target := range iv.BackfillCommits {
			appendHash(s.resolvePinCommit(target))
		}
	}

	// Collect all unique CodeCommit values from proposed/merged intents.
	var codeCommits []string
	codeCommitSeen := map[string]bool{}
	for _, iv := range view.Intents {
		if iv.Status != domain.StatusProposed && iv.Status != domain.StatusMerged {
			continue
		}
		if iv.CodeCommit != "" && !codeCommitSeen[iv.CodeCommit] {
			codeCommits = append(codeCommits, iv.CodeCommit)
			codeCommitSeen[iv.CodeCommit] = true
		}
	}

	// Run all 6 batch git calls concurrently — each is an independent
	// subprocess with no shared state.
	var (
		treeOf         map[string]string
		intentTreeOf   map[string]string
		intentSubjects map[string]string
		entryMessages  map[string]string
		noteCache      map[string]string
		commitParents  map[string][]string
		wg             sync.WaitGroup
	)
	wg.Add(6)
	go func() { defer wg.Done(); treeOf, _ = s.Git.CommitTreeHashes(hashes) }()
	go func() { defer wg.Done(); intentTreeOf, _ = s.Git.CommitTreeHashes(codeCommits) }()
	go func() { defer wg.Done(); intentSubjects, _ = s.Git.CommitSubjects(codeCommits) }()
	go func() { defer wg.Done(); entryMessages, _ = s.Git.FullCommitMessages(hashes) }()
	go func() { defer wg.Done(); noteCache, _ = s.Git.NotesForCommits(hashes) }()
	go func() { defer wg.Done(); commitParents, _ = s.Git.CommitParents(hashes) }()
	wg.Wait()

	if treeOf == nil {
		treeOf = map[string]string{}
	}
	if intentTreeOf == nil {
		intentTreeOf = map[string]string{}
	}
	if intentSubjects == nil {
		intentSubjects = map[string]string{}
	}
	if entryMessages == nil {
		entryMessages = map[string]string{}
	}
	if noteCache == nil {
		noteCache = map[string]string{}
	}
	if commitParents == nil {
		commitParents = map[string][]string{}
	}

	pinCtx := &pinContext{
		treeOf:         treeOf,
		intentTreeOf:   intentTreeOf,
		intentSubjects: intentSubjects,
		entryMessages:  entryMessages,
		noteCache:      noteCache,
		commitParents:  commitParents,
	}

	result := &PinResult{}
	for _, iv := range view.Intents {
		if iv.Status != domain.StatusProposed && iv.Status != domain.StatusMerged {
			continue
		}

		if len(iv.BackfillCommits) > 0 {
			pinnedAny := false
			for _, target := range iv.BackfillCommits {
				resolved := s.resolvePinCommit(target)
				if alreadyHasIntentCached(pinCtx.noteCache, resolved, iv.IntentID) {
					continue
				}
				hash, _ := core.CanonicalHash(iv)
				note := domain.CommitNote{
					SchemaVersion: 1,
					Kind:          "mainline.commit_note",
					Intents: []domain.IntentReference{
						{IntentID: iv.IntentID, SealResultHash: "sha256:" + hash},
					},
					AddedAt:       core.Now(),
					AddedBy:       identity.ActorID,
					Via:           "pin_backfill",
					MatchStrategy: "backfill_explicit",
					ReconciledAt:  core.Now(),
					ReconciledBy:  identity.ActorID,
				}
				if err := upsertCommitNote(s.Git, resolved, note); err != nil {
					continue
				}
				if b, err := json.Marshal(note); err == nil {
					pinCtx.noteCache[resolved] = string(b)
				}
				pinnedAny = true
				result.Links = append(result.Links, PinnedCommit{
					IntentID:      iv.IntentID,
					Commit:        resolved,
					MatchStrategy: "backfill_explicit",
				})
			}
			if pinnedAny {
				result.IntentIDs = append(result.IntentIDs, iv.IntentID)
			}
			continue
		}

		var primary, strategy string
		switch iv.Status {
		case domain.StatusProposed:
			primary, strategy = findPinMatchBatched(iv, entries, pinCtx)
			if primary == "" {
				continue
			}
		case domain.StatusMerged:
			primary = iv.StatusEvidence.MergedMainCommit
			if primary == "" || treeOf[primary] == "" {
				continue
			}
			strategy = "tree_hash"
		default:
			continue
		}

		targets := sameTreePinTargets(primary, entries, pinCtx)

		hash, _ := core.CanonicalHash(iv)
		pinnedAny := false
		for _, target := range targets {
			if alreadyHasIntentCached(pinCtx.noteCache, target, iv.IntentID) {
				continue
			}
			note := domain.CommitNote{
				SchemaVersion: 1,
				Kind:          "mainline.commit_note",
				Intents: []domain.IntentReference{
					{IntentID: iv.IntentID, SealResultHash: "sha256:" + hash},
				},
				AddedAt:       core.Now(),
				AddedBy:       identity.ActorID,
				Via:           "pin_auto",
				MatchStrategy: strategy,
				ReconciledAt:  core.Now(),
				ReconciledBy:  identity.ActorID,
			}
			if err := upsertCommitNote(s.Git, target, note); err != nil {
				continue
			}
			pinnedAny = true
			result.Links = append(result.Links, PinnedCommit{
				IntentID:      iv.IntentID,
				Commit:        target,
				MatchStrategy: strategy,
			})
		}
		if pinnedAny {
			result.IntentIDs = append(result.IntentIDs, iv.IntentID)
		}
	}

	// ── GitHub API post-pass ──
	// For proposed intents that no git-native strategy could pin,
	// try the GitHub API via `gh` to resolve branch→mergeCommit.
	pinnedSet := make(map[string]bool, len(result.IntentIDs))
	for _, id := range result.IntentIDs {
		pinnedSet[id] = true
	}
	var unpinned []domain.IntentView
	for _, iv := range view.Intents {
		if iv.Status != domain.StatusProposed {
			continue
		}
		if pinnedSet[iv.IntentID] || len(iv.BackfillCommits) > 0 {
			continue
		}
		if iv.GitBranch == "" {
			continue
		}
		unpinned = append(unpinned, iv)
	}
	if len(unpinned) > 0 {
		entrySet := make(map[string]bool, len(entries))
		entryByHash := make(map[string]gitops.LogEntry, len(entries))
		for _, e := range entries {
			entrySet[e.Hash] = true
			entryByHash[e.Hash] = e
		}
		branchToMerge := ghMergedPRBranches(s.Git.RepoRoot)
		for _, iv := range unpinned {
			mergeCommit, ok := branchToMerge[iv.GitBranch]
			if !ok {
				continue
			}
			if !entrySet[mergeCommit] {
				continue
			}
			if !prMergeCandidateAllowed(iv, entryByHash[mergeCommit]) {
				continue
			}
			if alreadyHasIntentCached(pinCtx.noteCache, mergeCommit, iv.IntentID) {
				continue
			}
			hash, _ := core.CanonicalHash(iv)
			note := domain.CommitNote{
				SchemaVersion: 1,
				Kind:          "mainline.commit_note",
				Intents: []domain.IntentReference{
					{IntentID: iv.IntentID, SealResultHash: "sha256:" + hash},
				},
				AddedAt:       core.Now(),
				AddedBy:       identity.ActorID,
				Via:           "pin_auto",
				MatchStrategy: "gh_pr_merge",
				ReconciledAt:  core.Now(),
				ReconciledBy:  identity.ActorID,
			}
			if err := upsertCommitNote(s.Git, mergeCommit, note); err != nil {
				continue
			}
			result.Links = append(result.Links, PinnedCommit{
				IntentID:      iv.IntentID,
				Commit:        mergeCommit,
				MatchStrategy: "gh_pr_merge",
			})
			result.IntentIDs = append(result.IntentIDs, iv.IntentID)
		}
	}

	result.Pinned = len(result.IntentIDs)

	if result.Pinned > 0 && s.Git.HasRemote(s.remoteName()) {
		_ = s.Git.Push(s.remoteName(), "refs/notes/mainline/intents")
	}

	return result, nil
}

// pinContext holds pre-batched data for the pin sweep so findPinMatch
// and alreadyHasIntent don't fork per-intent subprocess calls.
type pinContext struct {
	treeOf         map[string]string   // main commit → tree hash
	intentTreeOf   map[string]string   // code_commit → tree hash
	intentSubjects map[string]string   // code_commit → subject line
	entryMessages  map[string]string   // main commit → full message
	noteCache      map[string]string   // main commit → note JSON (empty if no note)
	commitParents  map[string][]string // main commit → parent hashes (merge commits have 2+)
}

// findPinMatchBatched is the batched version of findPinMatch that uses
// pre-fetched data instead of forking per-intent git subprocesses.
func findPinMatchBatched(iv domain.IntentView, entries []gitops.LogEntry, ctx *pinContext) (string, string) {
	for _, strategy := range pinStrategies {
		switch strategy {
		case "tree_hash":
			if iv.CodeCommit == "" {
				continue
			}
			intentTree := ctx.intentTreeOf[iv.CodeCommit]
			if intentTree == "" {
				continue
			}
			for _, entry := range entries {
				if ctx.treeOf[entry.Hash] == intentTree {
					return entry.Hash, strategy
				}
			}
		case "commit_hash":
			if iv.CodeCommit == "" {
				continue
			}
			for _, entry := range entries {
				if entry.Hash == iv.CodeCommit {
					return entry.Hash, strategy
				}
			}
		case "merge_parent":
			if iv.CodeCommit == "" {
				continue
			}
			for _, entry := range entries {
				parents := ctx.commitParents[entry.Hash]
				if len(parents) < 2 {
					continue
				}
				for _, p := range parents[1:] {
					if p == iv.CodeCommit {
						return entry.Hash, strategy
					}
				}
			}
		case "subject":
			if iv.CodeCommit == "" {
				continue
			}
			intentSubject := ctx.intentSubjects[iv.CodeCommit]
			if intentSubject == "" {
				continue
			}
			for _, entry := range entries {
				if entry.Subject == intentSubject {
					return entry.Hash, strategy
				}
			}
		case "branch_in_message":
			branch := iv.GitBranch
			if branch == "" {
				continue
			}
			for _, entry := range entries {
				msg := ctx.entryMessages[entry.Hash]
				if msg != "" && matchesMergeHeader(msg, branch) && prMergeCandidateAllowed(iv, entry) {
					return entry.Hash, strategy
				}
			}
		case "goal_text":
			if iv.Goal == "" {
				continue
			}
			for _, entry := range entries {
				msg := ctx.entryMessages[entry.Hash]
				if msg != "" && strings.Contains(msg, iv.Goal) {
					return entry.Hash, strategy
				}
			}
		}
	}
	return "", ""
}

func sameTreePinTargets(primary string, entries []gitops.LogEntry, ctx *pinContext) []string {
	targets := []string{primary}
	primaryTree := ctx.treeOf[primary]
	if primaryTree == "" {
		return targets
	}
	for _, e := range entries {
		if e.Hash == primary || ctx.treeOf[e.Hash] != primaryTree {
			continue
		}
		if sameTreeDirectNeighbor(primary, e.Hash, ctx.commitParents) {
			targets = append(targets, e.Hash)
		}
	}
	return targets
}

func sameTreeDirectNeighbor(a, b string, parents map[string][]string) bool {
	for _, parent := range parents[a] {
		if parent == b {
			return true
		}
	}
	for _, parent := range parents[b] {
		if parent == a {
			return true
		}
	}
	return false
}

func prMergeCandidateAllowed(iv domain.IntentView, entry gitops.LogEntry) bool {
	if iv.SealedAt == "" || entry.Date == "" {
		return true
	}
	sealedAt, err := time.Parse(time.RFC3339, iv.SealedAt)
	if err != nil {
		return true
	}
	mergedAt, err := time.Parse(time.RFC3339, entry.Date)
	if err != nil {
		return true
	}
	return !mergedAt.Before(sealedAt)
}

// alreadyHasIntentCached checks the note cache instead of forking git.
func alreadyHasIntentCached(noteCache map[string]string, commit, intentID string) bool {
	noteContent := noteCache[commit]
	if noteContent == "" {
		return false
	}
	var existing domain.CommitNote
	if err := json.Unmarshal([]byte(noteContent), &existing); err != nil {
		return false
	}
	for _, ref := range existing.Intents {
		if ref.IntentID == intentID {
			return true
		}
	}
	return false
}

func (s *Service) resolvePinCommit(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	// BackfillCommits persisted before the normalization fix may contain
	// abbreviated hashes. Resolve them before any full-hash keyed cache lookup.
	if len(target) < 40 {
		if full, err := s.Git.Run("rev-parse", "--verify", target+"^{commit}"); err == nil {
			if resolved := strings.TrimSpace(full); resolved != "" {
				return resolved
			}
		}
	}
	return target
}

// mergeHeaderRE matches the canonical GitHub merge commit first line:
// "Merge pull request #NNN from <owner>/<branch>"
var mergeHeaderRE = regexp.MustCompile(`^Merge pull request #\d+ from [^/]+/(.+)$`)

// matchesMergeHeader returns true if the commit message's first line
// is a GitHub merge header whose branch name matches exactly.
func matchesMergeHeader(fullMessage, branch string) bool {
	firstLine := fullMessage
	if idx := strings.IndexByte(fullMessage, '\n'); idx >= 0 {
		firstLine = fullMessage[:idx]
	}
	m := mergeHeaderRE.FindStringSubmatch(strings.TrimSpace(firstLine))
	return len(m) >= 2 && m[1] == branch
}

// ghMergedPRBranches returns a map of headRefName→mergeCommitOID for
// recently merged PRs, using the `gh` CLI. Returns nil (not an error)
// when `gh` is unavailable, unauthenticated, or times out — the caller
// treats this as a graceful skip.
func ghMergedPRBranches(repoRoot string) map[string]string {
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ghPath, "pr", "list",
		"--state", "merged",
		"--limit", "100",
		"--json", "headRefName,mergeCommit",
	)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1")

	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var prs []struct {
		HeadRefName string `json:"headRefName"`
		MergeCommit struct {
			OID string `json:"oid"`
		} `json:"mergeCommit"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil
	}

	result := make(map[string]string, len(prs))
	for _, pr := range prs {
		if pr.HeadRefName != "" && pr.MergeCommit.OID != "" {
			result[pr.HeadRefName] = pr.MergeCommit.OID
		}
	}
	return result
}

// PinExplicit writes a pin_explicit note pinning intentID to
// commitHash without consulting the heuristic cascade. It is the
// escape hatch when the automatic match cannot reach the right commit
// (e.g. a rebase scrambled the tree, or the agent never recorded
// code_commit).
//
// The intent must currently be in the proposed state and the commit
// must exist; the note's added_by records the calling actor regardless
// of who owns the intent.
func (s *Service) PinExplicit(intentID, commitHash string) (*PinnedCommit, error) {
	identity, err := s.requireIdentity()
	if err != nil {
		return nil, err
	}
	if intentID == "" || commitHash == "" {
		return nil, domain.NewError(domain.ErrInvalidInput,
			"pin explicit requires both intent and commit")
	}

	resolved, err := s.Git.Run("rev-parse", "--verify", commitHash+"^{commit}")
	if err != nil {
		return nil, domain.NewError(domain.ErrInvalidInput,
			fmt.Sprintf("commit %q not found: %v", commitHash, err))
	}
	resolved = strings.TrimSpace(resolved)

	view, _ := s.Store.ReadMainlineView()
	var iv *domain.IntentView
	if view != nil {
		for i := range view.Intents {
			if view.Intents[i].IntentID == intentID {
				iv = &view.Intents[i]
				break
			}
		}
	}
	if iv == nil {
		return nil, domain.NewError(domain.ErrNoActiveIntent,
			fmt.Sprintf("intent %s not found in view; run mainline sync first", intentID))
	}
	if iv.Status != domain.StatusProposed {
		return nil, domain.NewError(domain.ErrInvalidStatus,
			fmt.Sprintf("intent %s is in status %s; only proposed intents can be pinned",
				intentID, iv.Status))
	}

	if alreadyHasIntent(s.Git, resolved, intentID) {
		return &PinnedCommit{
			IntentID:      intentID,
			Commit:        resolved,
			MatchStrategy: "manual",
		}, nil
	}

	hash, _ := core.CanonicalHash(iv)
	note := domain.CommitNote{
		SchemaVersion: 1,
		Kind:          "mainline.commit_note",
		Intents: []domain.IntentReference{
			{IntentID: intentID, SealResultHash: "sha256:" + hash},
		},
		AddedAt:       core.Now(),
		AddedBy:       identity.ActorID,
		Via:           "pin_explicit",
		MatchStrategy: "manual",
		ReconciledAt:  core.Now(),
		ReconciledBy:  identity.ActorID,
	}
	if err := upsertCommitNote(s.Git, resolved, note); err != nil {
		return nil, fmt.Errorf("write note: %w", err)
	}
	if s.Git.HasRemote(s.remoteName()) {
		_ = s.Git.Push(s.remoteName(), "refs/notes/mainline/intents")
	}

	return &PinnedCommit{
		IntentID:      intentID,
		Commit:        resolved,
		MatchStrategy: "manual",
	}, nil
}

// alreadyHasIntent returns true if the existing note on commit already
// references intentID — Reconcile must not double-stamp the same intent on
// the same commit (otherwise NotesAdd's `-f` would overwrite an unrelated
// intent that happened to share the commit).
func alreadyHasIntent(git *gitops.Git, commit, intentID string) bool {
	noteContent, _ := git.NotesShow(commit)
	if noteContent == "" {
		return false
	}
	var existing domain.CommitNote
	if err := json.Unmarshal([]byte(noteContent), &existing); err != nil {
		return false
	}
	for _, ref := range existing.Intents {
		if ref.IntentID == intentID {
			return true
		}
	}
	return false
}
