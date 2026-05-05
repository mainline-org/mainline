package engine

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mainline-org/mainline/internal/core"
	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/gitops"
	"github.com/mainline-org/mainline/internal/storage"
)

// Service is the main business-logic facade.
type Service struct {
	Git   *gitops.Git
	Store *storage.Store

	// Bus is the optional domain-event sink. When non-nil, the
	// engine emits events at the end of each successful state
	// transition (intent_started, turn_appended, intent_sealed,
	// sync_completed, conflict_detected, check_judged) for the
	// webhook fan-out / hooks Dispatcher to forward.
	//
	// The interface is intentionally tiny (one Emit method) and
	// nil-safe — production code wires a webhook bus, tests leave
	// it nil. We never block on Emit; the bus implementation is
	// expected to enqueue and return.
	Bus EventBus

	// ProgressFunc, when non-nil, is called with a short human-readable
	// phase label at key sync stages (e.g. "fetching", "rebuilding",
	// "pinning"). CLI uses this to drive a spinner; agents ignore it.
	ProgressFunc func(phase string)
}

// EventBus is the engine's view of the domain-event sink. Mirrors
// hooks.EventEmitter but lives here so the engine package does not
// import internal/hooks (one-way: hooks depends on engine via the
// EngineFacade interface, not the other way around).
type EventBus interface {
	Emit(name string, data any)
}

// NewService creates a Service by auto-detecting the repo root from cwd.
func NewService(dir string) (*Service, error) {
	g, err := gitops.New(dir)
	if err != nil {
		return nil, err
	}
	st := storage.New(g.RepoRoot, g)
	return &Service{Git: g, Store: st}, nil
}

// NewServiceFromRoot creates a Service for a known repo root.
func NewServiceFromRoot(root string) *Service {
	g := gitops.NewFromRoot(root)
	return &Service{Git: g, Store: storage.New(root, g)}
}

// SetBus wires the optional domain-event sink. Safe to leave unset
// when the cli has no webhooks configured — the engine treats nil
// as "drop events on the floor".
func (s *Service) SetBus(bus EventBus) { s.Bus = bus }

// emit is the engine-internal nil-safe wrapper. Centralized so adding
// a new domain event is one Service method that ends with `s.emit(...)`,
// and the nil check + panic guard live in exactly one place.
func (s *Service) emit(name string, data any) {
	if s == nil || s.Bus == nil || name == "" {
		return
	}
	defer func() {
		// A misbehaving bus implementation must never crash the
		// CLI. Recover and swallow — the user's command already
		// succeeded by the time we reach emit; we will not turn
		// success into failure because the observability sink
		// blew up.
		_ = recover()
	}()
	s.Bus.Emit(name, data)
}

// progress calls ProgressFunc if set. Used by long-running commands
// (sync, pin) to report phase transitions to the CLI spinner.
func (s *Service) progress(phase string) {
	if s != nil && s.ProgressFunc != nil {
		s.ProgressFunc(phase)
	}
}

// -----------------------------------------------------------
// Init
// -----------------------------------------------------------

type InitResult struct {
	RepoRoot   string `json:"repo_root"`
	ActorID    string `json:"actor_id"`
	ActorName  string `json:"actor_name"`
	MainBranch string `json:"main_branch"`
	Created    bool   `json:"created"`

	// FilesStaged are the repo-relative paths Init successfully
	// `git add`ed before its `mainline: init` commit. Empty when Init
	// re-runs against a repo that already has all of them tracked.
	FilesStaged []string `json:"files_staged,omitempty"`

	// CommitHash is the SHA of the `mainline: init` commit, when
	// Init created one. Empty when there was nothing new to commit
	// (re-runs after the initial install).
	CommitHash string `json:"commit_hash,omitempty"`

	// AgentIntegrations reports the best-effort default skill + hook
	// installation that CLI init requests. Engine callers that need
	// the historical "state only" behavior call Init without options.
	AgentIntegrations *AgentIntegrationInstallResult `json:"agent_integrations,omitempty"`
}

func (s *Service) Init(actorName string) (*InitResult, error) {
	return s.InitWithOptions(actorName, InitOptions{})
}

func (s *Service) InitWithOptions(actorName string, opts InitOptions) (*InitResult, error) {
	if s.Store.IsInitialized() {
		if err := s.Store.EnsureDirs(); err != nil {
			return nil, fmt.Errorf("create dirs: %w", err)
		}

		cfg, err := s.Store.ReadTeamConfig()
		if err != nil {
			return nil, domain.NewError(domain.ErrNotInitialized, "config not found; run 'mainline init'")
		}

		if _, err := s.Store.ReadIdentity(); err == nil {
			return nil, domain.NewError(domain.ErrAlreadyInitialized,
				".mainline already exists and local identity is configured")
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read identity: %w", err)
		}

		if actorName == "" {
			actorName = "default-agent"
		}
		identity := &domain.Identity{
			ActorID:   core.GenerateActorID(),
			ActorName: actorName,
			CreatedAt: core.Now(),
		}
		if err := s.Store.WriteIdentity(identity); err != nil {
			return nil, fmt.Errorf("write identity: %w", err)
		}

		localCfg := &domain.LocalConfig{
			Actor: domain.ActorSection{
				ID:   identity.ActorID,
				Name: identity.ActorName,
			},
		}
		if err := s.Store.WriteLocalConfig(localCfg); err != nil {
			return nil, fmt.Errorf("write local config: %w", err)
		}

		s.ensureLocalViews(cfg)

		result := &InitResult{
			RepoRoot:   s.Git.RepoRoot,
			ActorID:    identity.ActorID,
			ActorName:  identity.ActorName,
			MainBranch: cfg.Mainline.MainBranch,
			Created:    true,
		}
		if opts.InstallAgentIntegrations {
			result.AgentIntegrations = s.InstallDefaultAgentIntegrations()
		}
		return result, nil
	}

	// Create default team config
	cfg := domain.DefaultTeamConfig()
	cfg.Mainline.MainBranch = s.Git.MainBranch()
	if head := s.Git.ReadRef("refs/heads/" + cfg.Mainline.MainBranch); head != "" {
		cfg.Mainline.Coverage.BaselineCommit = head
	}

	if err := s.Store.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("create dirs: %w", err)
	}

	if err := s.Store.WriteTeamConfig(&cfg); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	// Create identity
	actorID := core.GenerateActorID()
	if actorName == "" {
		actorName = "default-agent"
	}
	identity := &domain.Identity{
		ActorID:   actorID,
		ActorName: actorName,
		CreatedAt: core.Now(),
	}
	if err := s.Store.WriteIdentity(identity); err != nil {
		return nil, fmt.Errorf("write identity: %w", err)
	}

	// Write local config
	localCfg := &domain.LocalConfig{
		Actor: domain.ActorSection{
			ID:   actorID,
			Name: actorName,
		},
	}
	if err := s.Store.WriteLocalConfig(localCfg); err != nil {
		return nil, fmt.Errorf("write local config: %w", err)
	}

	// Gitignore: .ml-cache/ for the local view cache, and
	// .mainline/local.toml since it carries per-actor identity that
	// shouldn't ride into shared history. Without local.toml here,
	// fresh-init repos would have an untracked file and the v0.3
	// snapshot contract would refuse subsequent seals.
	if err := s.Git.EnsureGitignore([]string{".ml-cache/", ".mainline/local.toml"}); err != nil {
		return nil, fmt.Errorf("update .gitignore: %w", err)
	}

	// Configure git notes + actor-log fetch/push so the dedicated
	// mainline refs travel with normal `git push` / `git fetch`.
	s.configureRemoteRefspecs(cfg.Mainline.ActorLogPrefix)
	// Configure git log to show mainline notes by default
	_ = s.Git.ConfigAdd("notes.displayRef", "refs/notes/mainline/*")

	var integrations *AgentIntegrationInstallResult
	if opts.InstallAgentIntegrations {
		cfg.Hooks = domain.DefaultHooksSection()
		integrations = s.InstallDefaultAgentIntegrations()
	}

	// Commit .mainline/config.toml plus repo-local hook config that
	// Init created in one commit so a fresh-init repo lands with a
	// clean worktree. Skill installation is outside the repo and is
	// reported separately.
	if err := s.Store.WriteTeamConfig(&cfg); err != nil {
		return nil, fmt.Errorf("write team config: %w", err)
	}
	addPaths := []string{
		".mainline/config.toml",
		".gitignore",
	}
	addPaths = append(addPaths, integrationRepoPaths(s.Git.RepoRoot, integrations)...)
	addPaths = dedupeStrings(addPaths)
	staged := []string{}
	for _, p := range addPaths {
		// Errors here are non-fatal: file may not exist or path may
		// already be staged. We track which adds succeeded so the
		// CLI can tell users exactly what landed in their repo —
		// the silent commit was a real first-touch surprise.
		if _, err := s.Git.Run("add", p); err == nil {
			staged = append(staged, p)
		}
	}
	// `commit` may fail if there is nothing to commit (re-running init);
	// that's the documented idempotent case, not a bug. When it does
	// commit, capture the SHA so the user sees it on screen.
	commitSHA := ""
	if _, err := s.Git.Run("commit", "-m", "mainline: init"); err == nil {
		if sha, err := s.Git.Run("rev-parse", "HEAD"); err == nil {
			commitSHA = strings.TrimSpace(sha)
		}
	}

	s.ensureLocalViews(&cfg)

	return &InitResult{
		RepoRoot:          s.Git.RepoRoot,
		ActorID:           actorID,
		ActorName:         actorName,
		MainBranch:        cfg.Mainline.MainBranch,
		Created:           true,
		FilesStaged:       staged,
		CommitHash:        commitSHA,
		AgentIntegrations: integrations,
	}, nil
}

// configureRemoteRefspecs ensures the configured remote's fetch/push
// refspecs include both the notes ref (refs/notes/mainline/*) and the
// actor-log namespace. Actor logs live outside refs/heads by default so
// Git hosts do not surface them as recently pushed branches. Each refspec
// is added at most once; a re-run is a no-op. Silently does nothing when
// the configured remote is not present yet.
//
// MVP-readiness fix (PR fix/mvp-install-setup): this used to live
// inline in Init and only fired once. If a user ran `mainline init`
// before adding `git remote add origin ...`, refspecs were never
// configured and cross-actor sync silently degraded. Pulling the
// logic into a helper lets `mainline init --rewire`, `mainline doctor
// --setup --fix`, and Sync's first-time auto-detection all share one
// implementation.
//
// Returns the list of refspec lines that were added this call (for
// reporting in the doctor / rewire UX). Empty slice means everything
// was already in place or there is no origin to configure.
func (s *Service) configureRemoteRefspecs(actorLogPrefix string) []string {
	remote := s.remoteName()
	if !s.Git.HasRemote(remote) {
		return nil
	}
	fetchKey := "remote." + remote + ".fetch"
	pushKey := "remote." + remote + ".push"
	added := []string{}

	notesFetch := "+refs/notes/mainline/*:refs/notes/mainline/*"
	if !strings.Contains(s.Git.ConfigGet(fetchKey), "refs/notes/mainline") {
		_ = s.Git.ConfigAdd(fetchKey, notesFetch)
		added = append(added, "fetch: "+notesFetch)
	}
	notesPush := "refs/notes/mainline/*:refs/notes/mainline/*"
	if !strings.Contains(s.Git.ConfigGet(pushKey), "refs/notes/mainline") {
		_ = s.Git.ConfigAdd(pushKey, notesPush)
		added = append(added, "push: "+notesPush)
	}

	actorFetch := domain.ActorLogFetchRefspec(actorLogPrefix, remote)
	if !strings.Contains(s.Git.ConfigGet(fetchKey), strings.TrimPrefix(actorFetch, "+")) {
		_ = s.Git.ConfigAdd(fetchKey, actorFetch)
		added = append(added, "fetch: "+actorFetch)
	}
	// Keep reading legacy branch-backed actor logs during migration,
	// but stop configuring pushes to refs/heads/_mainline/actor/*.
	legacyFetch := domain.LegacyActorLogFetchRefspec(remote)
	if !strings.Contains(s.Git.ConfigGet(fetchKey), strings.TrimPrefix(legacyFetch, "+")) {
		_ = s.Git.ConfigAdd(fetchKey, legacyFetch)
		added = append(added, "fetch: "+legacyFetch)
	}
	legacyPush := fmt.Sprintf("refs/heads/%s/*:refs/heads/%s/*",
		domain.LegacyActorLogPrefix, domain.LegacyActorLogPrefix)
	if strings.Contains(s.Git.ConfigGet(pushKey), legacyPush) {
		_ = s.Git.ConfigUnsetAll(pushKey, "refs/heads/"+domain.LegacyActorLogPrefix+"/.*")
		added = append(added, "remove push: "+legacyPush)
	}

	actorPush := domain.ActorLogPushRefspec(actorLogPrefix)
	if !strings.Contains(s.Git.ConfigGet(pushKey), actorPush) {
		_ = s.Git.ConfigAdd(pushKey, actorPush)
		added = append(added, "push: "+actorPush)
	}

	return added
}

// RewireResult is returned by Service.Rewire / `mainline init --rewire`.
type RewireResult struct {
	HadRemote       bool     `json:"had_remote"`
	RefspecsAdded   []string `json:"refspecs_added"`
	NotesDisplayed  bool     `json:"notes_displayed"`
	AGENTSWritten   bool     `json:"agents_written"`
	IDEStubsWritten []string `json:"ide_stubs_written,omitempty"`
	PRTplWritten    bool     `json:"pr_template_written"`
	GitignoreFixed  bool     `json:"gitignore_fixed"`
}

// Rewire re-applies the parts of `mainline init` that depend on the
// remote being present and that init normally only does once: refspec
// configuration and .gitignore. Identity, team config, agent guidance,
// and committed .mainline/ files are NOT touched — Rewire is safe to
// run repeatedly on an already-initialised repo.
//
// Use cases:
//   - User ran `mainline init` then later `git remote add origin ...`
//     — refspecs were never written; Rewire fixes that.
//   - .gitignore drift after older init runs — Rewire restores the
//     Mainline cache ignore entry without touching repo guidance.
func (s *Service) Rewire() (*RewireResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}
	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}
	r := &RewireResult{
		HadRemote: s.Git.HasRemote(s.remoteName()),
	}
	r.RefspecsAdded = s.configureRemoteRefspecs(cfg.Mainline.ActorLogPrefix)

	// Always re-apply notes.displayRef (idempotent ConfigAdd dedupes).
	_ = s.Git.ConfigAdd("notes.displayRef", "refs/notes/mainline/*")
	r.NotesDisplayed = true

	// Re-apply .gitignore.
	if err := s.Git.EnsureGitignore([]string{".ml-cache/"}); err == nil {
		r.GitignoreFixed = true
	}

	return r, nil
}

func (s *Service) ensureLocalViews(cfg *domain.TeamConfig) {
	if view, _ := s.Store.ReadMainlineView(); view == nil {
		view = &domain.MainlineView{
			SchemaVersion: 1,
			RebuiltAt:     core.Now(),
			MainBranch:    cfg.Mainline.MainBranch,
		}
		head, _ := s.Git.HeadCommit()
		view.MainHead = head
		// Best-effort: this is the cold-start scaffold. If writing
		// fails, the next sync rebuilds anyway.
		_ = s.Store.WriteMainlineView(view)
	}

	if idx, _ := s.Store.ReadProposedIndex(); idx == nil {
		idx = &domain.ProposedIndex{
			SchemaVersion: 1,
			RebuiltAt:     core.Now(),
		}
		_ = s.Store.WriteProposedIndex(idx)
	}
}

// -----------------------------------------------------------
// Status
// -----------------------------------------------------------

type StatusResult struct {
	Initialized        bool                `json:"initialized"`
	IdentityConfigured bool                `json:"identity_configured"`
	Branch             string              `json:"branch,omitempty"`
	ActorID            string              `json:"actor_id,omitempty"`
	ActiveIntent       *domain.DraftIntent `json:"active_intent,omitempty"`
	TurnCount          int                 `json:"turn_count"`
	ProposedCount      int                 `json:"proposed_count"`
	LocalHead          string              `json:"local_head,omitempty"`
	MainHead           string              `json:"main_head,omitempty"`
	// rc5: sync staleness surface. LastSync is the persisted record
	// of the most recent successful Sync; nil means never synced in
	// this clone. SyncStaleSeconds and SyncStale are convenience
	// fields so the CLI does not need to do the math.
	LastSync         *domain.LastSync `json:"last_sync,omitempty"`
	SyncStaleSeconds int64            `json:"sync_stale_seconds,omitempty"`
	SyncStale        bool             `json:"sync_stale"`

	// v0.3 coverage summary over the last CoverageWindowSize commits
	// on main. Surfaced by default in `mainline status`. Detail view
	// is the separate `mainline gaps` command.
	Coverage *StatusCoverageSummary `json:"coverage,omitempty"`

	// AgentsGuidance reports the state of the Mainline-managed
	// block inside AGENTS.md. After a binary upgrade the user has
	// a stale block until they run `mainline agents update`;
	// surfacing the state here on every status is the primary
	// upgrade-discoverability mechanism. The block uses checksum-
	// based change detection so user edits are never silently
	// overwritten — mainline owns the block, the user owns the
	// rest of the file.
	AgentsGuidance *StatusAgentsGuidance `json:"agents_guidance,omitempty"`

	// rc7+: status as the daily entry point.
	//
	// UnsealedDrafts surfaces work the user (or the agent that did
	// it) might have forgotten — drafts in drafting or sealed_local
	// state on ANY branch, not just the current one. A common
	// pre-this-version failure was: agent starts work on
	// feature/A, gets pulled to feature/B, comes back two days
	// later — `mainline status` on feature/B never mentioned the
	// orphan draft on feature/A.
	UnsealedDrafts []StatusUnsealedDraft `json:"unsealed_drafts,omitempty"`

	// RecentSealed lists the last few intents that landed on main
	// — informational, helps a user re-enter "what just happened"
	// without running a separate `mainline log`. Capped to keep the
	// status output short.
	RecentSealed []StatusRecentIntent `json:"recent_sealed,omitempty"`

	ProposalHealth *StatusProposalHealth `json:"proposal_health,omitempty"`

	// Suggestions are actionable next-step CLI commands derived
	// from the rest of StatusResult. The CLI prints them as a
	// "Suggestions:" block under the main rollup.
	Suggestions []string `json:"suggestions,omitempty"`

	// ActionableItems is the ranked inbox behind `mainline status
	// --actionable`. It groups the highest-value follow-up commands
	// across status, gaps, proposal doctor, risks, and followups so
	// callers do not need to jump across commands just to decide what
	// to inspect first.
	ActionableItems []StatusActionItem `json:"actionable_items,omitempty"`
}

// StatusUnsealedDraft is the per-draft summary surfaced under
// "Unsealed intents" in `mainline status`.
type StatusUnsealedDraft struct {
	IntentID   string `json:"intent_id"`
	Goal       string `json:"goal"`
	GitBranch  string `json:"git_branch"`
	Status     string `json:"status"` // drafting | sealed_local
	TurnCount  int    `json:"turn_count"`
	AgeSeconds int64  `json:"age_seconds"`
}

type StatusProposalHealth struct {
	StaleAfterHours int `json:"stale_after_hours"`
	SuspiciousCount int `json:"suspicious_count"`
	OldestAgeHours  int `json:"oldest_age_hours,omitempty"`
}

// StatusRecentIntent is the per-intent summary in the "Recent sealed
// intents" block. Just enough to answer "what landed recently"
// without sending the user to `mainline log`.
type StatusRecentIntent struct {
	IntentID         string `json:"intent_id"`
	Title            string `json:"title"`
	Status           string `json:"status"`
	ActorName        string `json:"actor_name,omitempty"`
	WhenSeconds      int64  `json:"when_seconds_ago"`
	MergedMainCommit string `json:"merged_main_commit,omitempty"`
}

// StatusCoverageSummary is the compact coverage rollup carried in
// StatusResult. Counts plus the actionable list (uncovered commits)
// inline; covered/skipped detail goes to `mainline gaps`.
type StatusCoverageSummary struct {
	WindowSize     int              `json:"window_size"`
	CoveredCount   int              `json:"covered_count"`
	SkippedCount   int              `json:"skipped_count"`
	UncoveredCount int              `json:"uncovered_count"`
	Uncovered      []CommitCoverage `json:"uncovered,omitempty"`
}

// StatusActionItem is one row in the `status --actionable` inbox.
// It deliberately stores a single command per item: the command opens
// the detailed surface (gaps, doctor, risks, followups, etc.) where
// the user can inspect and decide.
type StatusActionItem struct {
	Kind               string `json:"kind"`
	Title              string `json:"title"`
	Why                string `json:"why"`
	Risk               string `json:"risk"`
	RecommendedCommand string `json:"recommended_command"`
}

func (s *Service) Status() (*StatusResult, error) {
	result := &StatusResult{
		Initialized: s.Store.IsInitialized(),
	}
	if !result.Initialized {
		result.ActionableItems = buildStatusActionItems(result)
		return result, nil
	}

	branch, _ := s.Git.CurrentBranch()
	result.Branch = branch

	id, err := s.Store.ReadIdentity()
	if err == nil && id != nil && strings.TrimSpace(id.ActorID) != "" {
		result.ActorID = id.ActorID
		result.IdentityConfigured = true
	}

	draft, _ := s.Store.FindActiveDraft(branch)
	if draft != nil {
		result.ActiveIntent = draft
		turns, _ := s.Store.ReadTurns(draft.IntentID)
		result.TurnCount = len(turns)
	}

	idx, _ := s.Store.ReadProposedIndex()
	if idx != nil {
		result.ProposedCount = len(idx.Proposed)
	}

	head, _ := s.Git.HeadCommit()
	result.LocalHead = head

	if ls, _ := s.Store.ReadLastSync(); ls != nil {
		result.LastSync = ls
		result.MainHead = ls.MainHead
		cfg, _ := s.getTeamConfig()
		threshold := int64(86400)
		if cfg != nil && cfg.Sync.StaleThresholdSeconds > 0 {
			threshold = cfg.Sync.StaleThresholdSeconds
		}
		if t, err := time.Parse(time.RFC3339, ls.At); err == nil {
			elapsed := int64(time.Since(t).Seconds())
			result.SyncStaleSeconds = elapsed
			result.SyncStale = elapsed > threshold
		}
	} else {
		if view, _ := s.Store.ReadMainlineView(); view != nil {
			result.MainHead = view.MainHead
		}
		// Never synced — treat as stale so the CLI can prompt.
		result.SyncStale = true
	}

	// v0.3 coverage summary. Computed from the existing view + git
	// facts (notes ref, commit messages); cheap thanks to cat-file
	// --batch (already shipped). Errors are non-fatal — coverage is
	// nice-to-have, not load-bearing for status.
	view, _ := s.Store.ReadMainlineView()
	if view != nil {
		cfg, _ := s.getTeamConfig()
		if cfg != nil {
			window := CoverageWindowSize
			cov, err := s.CoverageWindow(window, view, cfg)
			if err == nil && len(cov) > 0 {
				summary := &StatusCoverageSummary{WindowSize: window}
				for _, c := range cov {
					switch c.State {
					case CoverageCovered:
						summary.CoveredCount++
					case CoverageSkipped:
						summary.SkippedCount++
					case CoverageUncovered:
						summary.UncoveredCount++
						summary.Uncovered = append(summary.Uncovered, c)
					}
				}
				result.Coverage = summary
			}
		}
	}

	// AGENTS.md managed-block state. Cheap (one ReadFile + regex +
	// sha256). Populates the StatusAgentsGuidance summary so JSON
	// callers can introspect; the Suggestions block below picks
	// the right call-to-action based on State.
	if g := s.AgentsGuidanceState(); g != nil {
		result.AgentsGuidance = g
	}

	// rc7+ daily-entry-point blocks. Each is a derived rollup over
	// data already in the view + drafts dir; status performs no new
	// git work beyond what the prior CoverageWindow call did.
	result.UnsealedDrafts = s.collectUnsealedDrafts(branch, view)
	result.RecentSealed = collectRecentSealed(view, statusRecentSealedLimit)
	result.ProposalHealth = collectStatusProposalHealth(view, DefaultStaleProposedAfter)
	result.Suggestions = buildStatusSuggestions(result)
	result.ActionableItems = buildStatusActionItems(result)

	return result, nil
}

func collectStatusProposalHealth(view *domain.MainlineView, staleAfter time.Duration) *StatusProposalHealth {
	if view == nil {
		return nil
	}
	now := time.Now().UTC()
	out := &StatusProposalHealth{StaleAfterHours: int(staleAfter.Hours())}
	for _, iv := range view.Intents {
		if iv.Status != domain.StatusProposed || iv.SealedAt == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, iv.SealedAt)
		if err != nil {
			continue
		}
		ageHours := int(now.Sub(t).Hours())
		if ageHours > out.OldestAgeHours {
			out.OldestAgeHours = ageHours
		}
		if time.Duration(ageHours)*time.Hour >= staleAfter {
			out.SuspiciousCount++
		}
	}
	if out.SuspiciousCount == 0 {
		return nil
	}
	return out
}

// statusRecentSealedLimit caps the "Recent sealed intents" block.
// Three is the median count that fits inline without forcing a
// scroll on a typical terminal; users wanting more run `mainline log`.
const statusRecentSealedLimit = 3

// collectUnsealedDrafts walks the drafts directory and returns every
// draft in drafting or sealed_local state across all branches. The
// current-branch active draft (already in result.ActiveIntent) is
// excluded so it's not double-printed.
//
// Cross-referenced against the view: a draft file may say
// "sealed_local" while the view (from sync's auto-pin) already
// reports the intent as merged. Trusting only the draft file would
// surface "Unsealed intents: <id>" and a "resume" suggestion for an
// intent the team already considers landed — exactly the kind of
// stale-suggestion that destroys trust in `mainline status`.
func (s *Service) collectUnsealedDrafts(currentBranch string, view *domain.MainlineView) []StatusUnsealedDraft {
	ids, _ := s.Store.ListDrafts()
	if len(ids) == 0 {
		return nil
	}
	// Index the view's authoritative status per intent id.
	viewStatus := make(map[string]domain.IntentStatus)
	if view != nil {
		for _, iv := range view.Intents {
			viewStatus[iv.IntentID] = iv.Status
		}
	}
	now := time.Now().UTC()
	var out []StatusUnsealedDraft
	for _, id := range ids {
		d, err := s.Store.ReadDraft(id)
		if err != nil || d == nil {
			continue
		}
		if d.Status != domain.StatusDrafting && d.Status != domain.StatusSealedLocal {
			continue
		}
		// View-overrides-draft: if sync has progressed this intent
		// past sealed_local, the draft file is stale. Treat the
		// view's status as truth.
		if vs, ok := viewStatus[id]; ok {
			if isTerminalIntentStatus(vs) {
				continue
			}
		}
		// Skip the active draft on the current branch — it's
		// already shown via ActiveIntent.
		if d.Status == domain.StatusDrafting && d.GitBranch == currentBranch {
			continue
		}
		turns, _ := s.Store.ReadTurns(id)
		var ageSec int64
		if t, err := time.Parse(time.RFC3339, d.CreatedAt); err == nil {
			ageSec = int64(now.Sub(t).Seconds())
		}
		out = append(out, StatusUnsealedDraft{
			IntentID:   id,
			Goal:       d.Goal,
			GitBranch:  d.GitBranch,
			Status:     string(d.Status),
			TurnCount:  len(turns),
			AgeSeconds: ageSec,
		})
	}
	// Newest first — recency dominates relevance for "did I forget
	// something" recall. Same-second ties broken stably by intent id.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].AgeSeconds < out[j].AgeSeconds
	})
	return out
}

// collectRecentSealed picks the last N merged intents from the view
// by sealed_at descending. Cross-actor included (the user wants to
// see "what landed recently" regardless of who did it).
func collectRecentSealed(view *domain.MainlineView, limit int) []StatusRecentIntent {
	if view == nil || limit <= 0 {
		return nil
	}
	now := time.Now().UTC()
	type candidate struct {
		summary    StatusRecentIntent
		sortKeySec int64
	}
	var pool []candidate
	for _, iv := range view.Intents {
		if iv.Status != domain.StatusMerged {
			continue
		}
		title := iv.Goal
		if iv.Summary != nil && iv.Summary.Title != "" {
			title = iv.Summary.Title
		}
		var ago int64 = -1
		if iv.SealedAt != "" {
			if t, err := time.Parse(time.RFC3339, iv.SealedAt); err == nil {
				ago = int64(now.Sub(t).Seconds())
			}
		}
		pool = append(pool, candidate{
			summary: StatusRecentIntent{
				IntentID:         iv.IntentID,
				Title:            title,
				Status:           string(iv.Status),
				ActorName:        iv.ActorName,
				WhenSeconds:      ago,
				MergedMainCommit: iv.StatusEvidence.MergedMainCommit,
			},
			sortKeySec: ago,
		})
	}
	// Sort by recency: smallest WhenSeconds (most recent) first;
	// unknown timestamps (-1) sort last via the explicit guard.
	sort.SliceStable(pool, func(i, j int) bool {
		ai, aj := pool[i].sortKeySec, pool[j].sortKeySec
		if ai < 0 {
			return false
		}
		if aj < 0 {
			return true
		}
		return ai < aj
	})
	if len(pool) > limit {
		pool = pool[:limit]
	}
	out := make([]StatusRecentIntent, len(pool))
	for i, c := range pool {
		out[i] = c.summary
	}
	return out
}

// buildStatusSuggestions derives a short list of next-step commands
// from the assembled status. The goal is "obvious next thing" — not
// an exhaustive cookbook. Order matches the natural daily flow:
// active intent first, then unsealed work elsewhere, then setup
// repair, then a fresh start prompt.
func buildStatusSuggestions(r *StatusResult) []string {
	if !r.Initialized {
		return []string{"mainline init --actor-name \"<your name>\""}
	}
	if !r.IdentityConfigured {
		return []string{"mainline init --actor-name \"<your name>\""}
	}
	var out []string
	switch {
	case r.ActiveIntent != nil && r.ActiveIntent.Status == domain.StatusDrafting:
		// Mid-flight intent on the current branch. Lead with
		// context --current so the agent reads relevant historical
		// intents BEFORE editing — the rc8 intent-first rule.
		out = append(out,
			"mainline context --current --json   # relevant prior intents BEFORE editing",
			fmt.Sprintf("mainline append \"<what changed>\"   # record progress on %s", r.ActiveIntent.IntentID),
			"mainline seal --prepare > seal.json   # then fill seal.json and submit")
	case r.ActiveIntent != nil && r.ActiveIntent.Status == domain.StatusSealedLocal:
		// Sealed but not yet pushed.
		out = append(out,
			fmt.Sprintf("mainline publish --intent %s   # push the actor log to the team", r.ActiveIntent.IntentID))
	case len(r.UnsealedDrafts) > 0:
		// Work elsewhere worth resuming.
		d := r.UnsealedDrafts[0]
		out = append(out,
			fmt.Sprintf("git checkout %s && mainline status   # resume %s", d.GitBranch, d.IntentID))
	default:
		// Clean state — prompt for a new intent.
		out = append(out,
			"mainline start \"<the user's goal>\"   # claim a new intent",
			"mainline context --current --json   # relevant prior intents (before editing)")
	}
	// Sync staleness is a separate axis — append regardless.
	if r.SyncStale {
		out = append(out, "mainline sync   # team view is stale")
	}
	if r.Coverage != nil && r.Coverage.UncoveredCount > 0 {
		out = append(out, "mainline gaps   # uncovered commits with rescue options")
	}
	if r.ProposalHealth != nil && r.ProposalHealth.SuspiciousCount > 0 {
		out = append(out, "mainline doctor --proposals   # inspect proposed intents older than 72h")
	}
	if g := r.AgentsGuidance; g != nil {
		switch g.State {
		case AgentsBlockStateUpdateAvailable:
			out = append(out, "mainline agents diff   # see what changed; then `mainline agents update`")
		case AgentsBlockStateLocallyModified:
			out = append(out, "mainline agents check   # agent guidance has local edits; review before update")
		case AgentsBlockStateLegacy:
			out = append(out, "mainline agents update   # migrate agent guidance to the versioned marker format")
		}
	}
	return out
}

const statusActionableLimit = 5

// buildStatusActionItems derives the compact "what needs attention"
// inbox from the same read models that power the existing status,
// gaps, and doctor commands. It must stay read-only:
// every recommendation points at a command that lets the user inspect
// and decide rather than auto-applying a lifecycle event.
func buildStatusActionItems(r *StatusResult) []StatusActionItem {
	if r == nil {
		return nil
	}
	if !r.Initialized {
		return []StatusActionItem{{
			Kind:               "setup",
			Title:              "Mainline is not initialized in this repo",
			Why:                "Mainline cannot record intent history until repository state exists.",
			Risk:               "Work can land without durable intent context for future agents.",
			RecommendedCommand: "mainline init --actor-name \"<your name>\"",
		}}
	}
	if !r.IdentityConfigured {
		return []StatusActionItem{{
			Kind:               "setup",
			Title:              "This clone has no Mainline actor identity",
			Why:                "Writes need a stable actor identity before intents, turns, or resolutions can be recorded.",
			Risk:               "Local work cannot be safely attributed or published to the team log.",
			RecommendedCommand: "mainline init --actor-name \"<your name>\"",
		}}
	}

	items := make([]StatusActionItem, 0, statusActionableLimit)
	add := func(item StatusActionItem) {
		if len(items) < statusActionableLimit {
			items = append(items, item)
		}
	}

	if r.ActiveIntent != nil {
		switch r.ActiveIntent.Status {
		case domain.StatusDrafting:
			add(StatusActionItem{
				Kind:               "active_intent",
				Title:              fmt.Sprintf("Continue active intent %s", r.ActiveIntent.IntentID),
				Why:                "This branch already has a drafting intent, so new edits should stay attached to it.",
				Risk:               "Skipping context or append can leave the next agent without the latest why.",
				RecommendedCommand: "mainline context --current --json",
			})
		case domain.StatusSealedLocal:
			add(StatusActionItem{
				Kind:               "active_intent",
				Title:              fmt.Sprintf("Publish sealed intent %s", r.ActiveIntent.IntentID),
				Why:                "The intent is sealed locally but not yet visible to the team.",
				Risk:               "Reviewers and other agents may miss this work's intent until the actor log is published.",
				RecommendedCommand: fmt.Sprintf("mainline publish --intent %s", r.ActiveIntent.IntentID),
			})
		}
	}

	if r.Coverage != nil && r.Coverage.UncoveredCount > 0 {
		add(StatusActionItem{
			Kind:               "coverage",
			Title:              fmt.Sprintf("%d uncovered commit(s) on main", r.Coverage.UncoveredCount),
			Why:                "Main has commits that are not covered by a sealed intent or an explicit skip.",
			Risk:               "Future agents cannot recover why those commits happened or whether they were routine.",
			RecommendedCommand: "mainline gaps",
		})
	}

	if r.ProposalHealth != nil && r.ProposalHealth.SuspiciousCount > 0 {
		add(StatusActionItem{
			Kind:               "proposal",
			Title:              fmt.Sprintf("%d proposed intent(s) older than %dh", r.ProposalHealth.SuspiciousCount, r.ProposalHealth.StaleAfterHours),
			Why:                "Proposed intents past the cleanup threshold need a human review decision.",
			Risk:               "Stale proposed work can clog review, duplicate later work, or hide an intent that should be pinned.",
			RecommendedCommand: "mainline doctor --proposals",
		})
	}

	if len(r.UnsealedDrafts) > 0 {
		d := oldestUnsealedDraft(r.UnsealedDrafts)
		add(StatusActionItem{
			Kind:               "draft",
			Title:              fmt.Sprintf("%d unsealed intent(s) on other branches", len(r.UnsealedDrafts)),
			Why:                fmt.Sprintf("%s is %s on %s and %s old.", d.IntentID, d.Status, d.GitBranch, formatStatusActionAge(d.AgeSeconds)),
			Risk:               "Forgotten local work can keep polluting status or be duplicated by a later intent.",
			RecommendedCommand: fmt.Sprintf("git checkout %s && mainline status", d.GitBranch),
		})
	}

	if g := r.AgentsGuidance; g != nil {
		switch g.State {
		case AgentsBlockStateUpdateAvailable:
			add(StatusActionItem{
				Kind:               "agent_guidance",
				Title:              "Agent guidance update is available",
				Why:                "AGENTS.md carries an older Mainline policy block than this binary knows how to generate.",
				Risk:               "Agents may follow stale workflow instructions until the diff is reviewed and applied.",
				RecommendedCommand: "mainline agents diff",
			})
		case AgentsBlockStateLocallyModified:
			add(StatusActionItem{
				Kind:               "agent_guidance",
				Title:              "Agent guidance has local edits",
				Why:                "Mainline detected edits inside the managed guidance block.",
				Risk:               "Updating blindly could overwrite team policy edits.",
				RecommendedCommand: "mainline agents check",
			})
		case AgentsBlockStateLegacy:
			add(StatusActionItem{
				Kind:               "agent_guidance",
				Title:              "Agent guidance uses the legacy format",
				Why:                "The repo has pre-v0.4 guidance that should be migrated before future updates.",
				Risk:               "Legacy guidance can drift from the current skill workflow.",
				RecommendedCommand: "mainline agents update",
			})
		}
	}

	if r.SyncStale {
		add(StatusActionItem{
			Kind:               "sync",
			Title:              "Team view is stale",
			Why:                "The local materialized view is older than the configured freshness threshold or has never synced.",
			Risk:               "Status can miss recently published intents, pins, or conflict signals.",
			RecommendedCommand: "mainline sync",
		})
	}

	return items
}

func oldestUnsealedDraft(in []StatusUnsealedDraft) StatusUnsealedDraft {
	if len(in) == 0 {
		return StatusUnsealedDraft{}
	}
	oldest := in[0]
	for _, d := range in[1:] {
		if d.AgeSeconds > oldest.AgeSeconds {
			oldest = d
		}
	}
	return oldest
}

func formatStatusActionAge(seconds int64) string {
	switch {
	case seconds <= 0:
		return "0s"
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh", seconds/3600)
	default:
		return fmt.Sprintf("%dd", seconds/86400)
	}
}

// CoverageWindowSize controls how many recent commits on main `mainline
// status` and `mainline gaps` examine for coverage classification.
// 30 keeps status output snappy and the human's mental window
// reasonable (last day or two on an active repo).
const CoverageWindowSize = 30

// GapsResult is what `mainline gaps` returns. Lists every commit in
// the coverage window with its classification + per-commit suggestion
// list (only populated for uncovered commits — covered/skipped need
// no action).
type GapsResult struct {
	WindowSize int              `json:"window_size"`
	MainHead   string           `json:"main_head,omitempty"`
	Uncovered  []GapsEntry      `json:"uncovered,omitempty"`
	Skipped    []CommitCoverage `json:"skipped,omitempty"`
	Covered    int              `json:"covered_count"`
}

// GapsEntry is the per-uncovered-commit detail block.
type GapsEntry struct {
	Commit      string           `json:"commit"`
	Subject     string           `json:"subject"`
	Author      string           `json:"author"`
	CommittedAt string           `json:"committed_at"`
	Suggestions []GapsSuggestion `json:"suggestions"`
}

// GapsSuggestion is a single rescue path. Ordered by reversibility
// (cheapest first) — see spec §9.
type GapsSuggestion struct {
	Action     string `json:"action"`     // "reset" | "backfill" | "skip"
	Applicable string `json:"applicable"` // human-readable applicability
	Command    string `json:"command"`    // ready-to-paste command
}

// Gaps returns the coverage window plus rescue suggestions. The
// suggestions are static per commit (we cannot know if it is pushed
// without round-tripping the remote), so we list all three options
// ordered by reversibility and let the user pick.
func (s *Service) Gaps() (*GapsResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}
	view, _ := s.Store.ReadMainlineView()
	cfg, _ := s.getTeamConfig()
	if view == nil || cfg == nil {
		return &GapsResult{WindowSize: CoverageWindowSize}, nil
	}
	cov, err := s.CoverageWindow(CoverageWindowSize, view, cfg)
	if err != nil {
		return nil, err
	}
	out := &GapsResult{
		WindowSize: CoverageWindowSize,
		MainHead:   view.MainHead,
	}
	for _, c := range cov {
		switch c.State {
		case CoverageCovered:
			out.Covered++
		case CoverageSkipped:
			out.Skipped = append(out.Skipped, c)
		case CoverageUncovered:
			out.Uncovered = append(out.Uncovered, GapsEntry{
				Commit:      c.Commit,
				Subject:     c.Subject,
				Author:      c.Author,
				CommittedAt: c.CommittedAt,
				Suggestions: rescueSuggestions(c.Commit),
			})
		}
	}
	return out, nil
}

// rescueSuggestions builds the three-option rescue list per spec §9.
// Order is reversibility-first: reset (zero info loss), backfill
// (works post-push), skip (last-resort if commit is routine).
func rescueSuggestions(commit string) []GapsSuggestion {
	return []GapsSuggestion{
		{
			Action:     "reset",
			Applicable: "if the commit is not yet pushed",
			Command:    "git reset --soft HEAD^   # then `mainline start ...` for the proper flow",
		},
		{
			Action:     "backfill",
			Applicable: "if the commit is already pushed",
			Command:    fmt.Sprintf("mainline start --commits %s \"<your why>\"", short8(commit)),
		},
		{
			Action:     "skip",
			Applicable: "if the commit is genuinely routine (chore/format/version bump)",
			Command:    "git commit --amend  # add `Mainline-Skip: <reason>` trailer  (or add a [mainline.skip] pattern)",
		},
	}
}

func short8(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// GetTeamConfigForCLI is a thin re-export of the package-private
// getTeamConfig so the cli package can read freshness window settings
// from the same source the rest of the engine does.
func (s *Service) GetTeamConfigForCLI() (*domain.TeamConfig, error) {
	return s.getTeamConfig()
}

// remoteName returns the git remote mainline pushes / fetches its
// notes and actor logs to. Reads cfg.Mainline.Remote (defaults to
// "origin"). Pre-MVP this was hardcoded to "origin" everywhere —
// fork-based or non-default-remote workflows broke silently.
func (s *Service) remoteName() string {
	cfg, err := s.getTeamConfig()
	if err != nil || cfg == nil || cfg.Mainline.Remote == "" {
		return "origin"
	}
	return cfg.Mainline.Remote
}

// RemoteName is the exported variant for the CLI layer to print
// "Fetched from <remote>" messages without re-loading the config.
func (s *Service) RemoteName() string { return s.remoteName() }

// GetLastSyncForCLI returns the last-sync record (or nil if none),
// re-exported for the auto-before-command wrapper.
func (s *Service) GetLastSyncForCLI() (*domain.LastSync, error) {
	return s.Store.ReadLastSync()
}

func (s *Service) requireInit() error {
	if !s.Store.IsInitialized() {
		return domain.NewRecoverableError(domain.ErrNotInitialized,
			"mainline not initialized. Run 'mainline init' first.",
			"mainline init --actor-name \"<your name>\"",
			"or, if init was run but state is unclear: mainline doctor --setup")
	}
	return nil
}

func (s *Service) getIdentity() (*domain.Identity, error) {
	id, err := s.Store.ReadIdentity()
	if err != nil {
		return nil, domain.NewError(domain.ErrNotInitialized, "identity not found; run 'mainline init'")
	}
	return id, nil
}

// requireIdentity is the gate every write path must pass before mutating
// state. A fresh `git clone` of a mainline-enabled repo has the team
// `.mainline/config.toml` (committed) but NOT the local
// `.ml-cache/identity.json` (per-actor, gitignored). Without this gate
// the engine would happily create drafts with empty actor_id, write
// actor-log events with no author, and (worst case) mutate a draft to
// sealed_local before discovering the identity is missing — leaving
// orphan state nothing can recover. requireIdentity rejects all of
// that early with a recoverable error pointing the user at
// `mainline init --actor-name`.
func (s *Service) requireIdentity() (*domain.Identity, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}
	id, err := s.Store.ReadIdentity()
	if err != nil || id == nil || strings.TrimSpace(id.ActorID) == "" {
		return nil, domain.NewRecoverableError(
			domain.ErrNotInitialized,
			"this clone has no Mainline actor identity",
			"mainline init --actor-name <your name>",
			"or export $MAINLINE_ACTOR_NAME and run `mainline init` again",
			"`mainline doctor --setup` will report the same diagnosis if you want a wider check",
		)
	}
	return id, nil
}

// IdentityConfigured reports whether the local clone has a usable
// actor identity. Read surfaces (status/context) use this to surface
// the missing-identity state explicitly rather than silently emitting
// actor_id="" when displaying.
func (s *Service) IdentityConfigured() bool {
	id, err := s.Store.ReadIdentity()
	if err != nil || id == nil {
		return false
	}
	return strings.TrimSpace(id.ActorID) != ""
}

func (s *Service) actorDisplayName(identity *domain.Identity) string {
	name := strings.TrimSpace(s.Git.ConfigGet("user.name"))
	if name != "" {
		return name
	}
	if identity != nil {
		if identity.ActorName != "" {
			return identity.ActorName
		}
		return identity.ActorID
	}
	return ""
}

func (s *Service) getTeamConfig() (*domain.TeamConfig, error) {
	cfg, err := s.Store.ReadTeamConfig()
	if err != nil {
		return nil, domain.NewError(domain.ErrNotInitialized, "config not found; run 'mainline init'")
	}
	return cfg, nil
}
