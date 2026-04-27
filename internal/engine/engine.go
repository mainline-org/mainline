package engine

import (
	"fmt"
	"os"
	"path/filepath"
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

// -----------------------------------------------------------
// Init
// -----------------------------------------------------------

type InitResult struct {
	RepoRoot   string `json:"repo_root"`
	ActorID    string `json:"actor_id"`
	ActorName  string `json:"actor_name"`
	MainBranch string `json:"main_branch"`
	Created    bool   `json:"created"`
}

func (s *Service) Init(actorName string) (*InitResult, error) {
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

		return &InitResult{
			RepoRoot:   s.Git.RepoRoot,
			ActorID:    identity.ActorID,
			ActorName:  identity.ActorName,
			MainBranch: cfg.Mainline.MainBranch,
			Created:    true,
		}, nil
	}

	// Create default team config
	cfg := domain.DefaultTeamConfig()
	cfg.Mainline.MainBranch = s.Git.MainBranch()

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

	// Write AGENTS.md if it doesn't exist
	s.writeAgentsMD()

	// Write PR template (no trailers, rc3)
	s.writePRTemplate()

	// Configure git notes + actor-log fetch/push so the dedicated
	// mainline refs travel with normal `git push` / `git fetch`.
	s.configureRemoteRefspecs(cfg.Mainline.ActorLogPrefix)
	// Configure git log to show mainline notes by default
	_ = s.Git.ConfigAdd("notes.displayRef", "refs/notes/mainline/*")

	// Commit .mainline/config.toml plus everything else Init created
	// (.gitignore, AGENTS.md, PR template) in one commit so a fresh-init
	// repo lands with a clean worktree. Without this, the v0.3
	// snapshot contract would refuse the very first seal because Init's
	// own files would show as untracked.
	if err := s.Store.WriteTeamConfig(&cfg); err != nil {
		return nil, fmt.Errorf("write team config: %w", err)
	}
	addPaths := []string{
		".mainline/config.toml",
		".gitignore",
		"AGENTS.md",
		"CLAUDE.md",
		".cursor/rules/mainline.md",
		".windsurfrules",
		".github/PULL_REQUEST_TEMPLATE.md",
		".github/copilot-instructions.md",
	}
	for _, p := range addPaths {
		// Errors here are non-fatal: file may not exist or path may
		// already be staged.
		_, _ = s.Git.Run("add", p)
	}
	// `commit` may fail if there is nothing to commit (re-running init);
	// that's the documented idempotent case, not a bug.
	_, _ = s.Git.Run("commit", "-m", "mainline: init")

	s.ensureLocalViews(&cfg)

	return &InitResult{
		RepoRoot:   s.Git.RepoRoot,
		ActorID:    actorID,
		ActorName:  actorName,
		MainBranch: cfg.Mainline.MainBranch,
		Created:    true,
	}, nil
}

// configureRemoteRefspecs ensures origin's fetch/push refspecs include
// both the notes ref (refs/notes/mainline/*) and the actor-log namespace
// (refs/heads/<prefix>/*). Each refspec is added at most once — a re-run
// is a no-op. Silently does nothing when origin is not configured yet.
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

	actorFetch := fmt.Sprintf("+refs/heads/%s/*:refs/remotes/%s/%s/*",
		actorLogPrefix, remote, actorLogPrefix)
	if !strings.Contains(s.Git.ConfigGet(fetchKey), "refs/heads/"+actorLogPrefix) {
		_ = s.Git.ConfigAdd(fetchKey, actorFetch)
		added = append(added, "fetch: "+actorFetch)
	}
	actorPush := fmt.Sprintf("refs/heads/%s/*:refs/heads/%s/*",
		actorLogPrefix, actorLogPrefix)
	if !strings.Contains(s.Git.ConfigGet(pushKey), "refs/heads/"+actorLogPrefix) {
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
// configuration, AGENTS.md, PR template, .gitignore. Identity, team
// config, and committed .mainline/ files are NOT touched — Rewire is
// safe to run repeatedly on an already-initialised repo.
//
// Use cases:
//   - User ran `mainline init` then later `git remote add origin ...`
//     — refspecs were never written; Rewire fixes that.
//   - Older AGENTS.md / PR template that init's stat-check skipped on
//     the second call — Rewire force-rewrites them to the current
//     template version.
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

	// AGENTS.md + IDE stubs use the section-aware upsert: only the
	// `<!-- mainline:begin -->`..`<!-- mainline:end -->` block is
	// touched, surrounding user content is preserved. Pre-v0.3
	// AGENTS.md files (no markers, just `## Mainline` heading) get
	// migrated in place to the marker-wrapped form.
	if changed, err := upsertAgentsMD(s.Git.RepoRoot); err == nil {
		r.AGENTSWritten = changed
	}
	if stubs, err := upsertAgentInstructionStubs(s.Git.RepoRoot); err == nil {
		r.IDEStubsWritten = stubs
	}

	// PR template: as before, recreated only if missing — no upsert
	// machinery needed because PR templates are typically not
	// hand-edited and the template is short.
	prtPath := filepath.Join(s.Git.RepoRoot, ".github", "PULL_REQUEST_TEMPLATE.md")
	if _, err := os.Stat(prtPath); err != nil {
		s.writePRTemplate()
		r.PRTplWritten = true
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

// writeAgentsMD is the legacy thin wrapper retained for the Init code
// path. Real logic moved to upsertAgentsMD (section-aware, preserves
// user content, handles legacy section migration). See agents_md.go.
func (s *Service) writeAgentsMD() {
	// Failures are surfaced via doctor — Init does not abort just
	// because the AGENTS.md template could not be written.
	_, _ = upsertAgentsMD(s.Git.RepoRoot)
	_, _ = upsertAgentInstructionStubs(s.Git.RepoRoot)
}

func (s *Service) writePRTemplate() {
	path := filepath.Join(s.Git.RepoRoot, ".github", "PULL_REQUEST_TEMPLATE.md")
	if _, err := os.Stat(path); err == nil {
		return
	}
	// Best-effort: PR template is a convenience for humans, not
	// load-bearing for mainline correctness. Errors here fall through.
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	content := `## Summary

<!-- Describe what this PR does -->

## Mainline Intent

<!--
This section is auto-filled by mainline pr-description.
It is for human reviewers; Mainline does not parse it.
-->

## Tested

<!-- How was this tested? -->
`
	_ = os.WriteFile(path, []byte(content), 0o644)
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

func (s *Service) Status() (*StatusResult, error) {
	result := &StatusResult{
		Initialized: s.Store.IsInitialized(),
	}
	if !result.Initialized {
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
	if view, _ := s.Store.ReadMainlineView(); view != nil {
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

	return result, nil
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
			"mainline init")
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
