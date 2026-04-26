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

	// Ensure .ml-cache in .gitignore
	if err := s.Git.EnsureGitignore([]string{".ml-cache/"}); err != nil {
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
	s.Git.ConfigAdd("notes.displayRef", "refs/notes/mainline/*")

	// Commit .mainline/ config + tracked files
	if err := s.Git.WriteAndCommitFile(".mainline/config.toml", mustReadFile(s.Store, cfg), "mainline: init"); err != nil {
		// Non-fatal: maybe there are no changes or index is locked
	}

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
		s.Git.ConfigAdd(fetchKey, notesFetch)
		added = append(added, "fetch: "+notesFetch)
	}
	notesPush := "refs/notes/mainline/*:refs/notes/mainline/*"
	if !strings.Contains(s.Git.ConfigGet(pushKey), "refs/notes/mainline") {
		s.Git.ConfigAdd(pushKey, notesPush)
		added = append(added, "push: "+notesPush)
	}

	actorFetch := fmt.Sprintf("+refs/heads/%s/*:refs/remotes/%s/%s/*",
		actorLogPrefix, remote, actorLogPrefix)
	if !strings.Contains(s.Git.ConfigGet(fetchKey), "refs/heads/"+actorLogPrefix) {
		s.Git.ConfigAdd(fetchKey, actorFetch)
		added = append(added, "fetch: "+actorFetch)
	}
	actorPush := fmt.Sprintf("refs/heads/%s/*:refs/heads/%s/*",
		actorLogPrefix, actorLogPrefix)
	if !strings.Contains(s.Git.ConfigGet(pushKey), "refs/heads/"+actorLogPrefix) {
		s.Git.ConfigAdd(pushKey, actorPush)
		added = append(added, "push: "+actorPush)
	}

	return added
}

// RewireResult is returned by Service.Rewire / `mainline init --rewire`.
type RewireResult struct {
	HadRemote      bool     `json:"had_remote"`
	RefspecsAdded  []string `json:"refspecs_added"`
	NotesDisplayed bool     `json:"notes_displayed"`
	AGENTSWritten  bool     `json:"agents_written"`
	PRTplWritten   bool     `json:"pr_template_written"`
	GitignoreFixed bool     `json:"gitignore_fixed"`
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
	s.Git.ConfigAdd("notes.displayRef", "refs/notes/mainline/*")
	r.NotesDisplayed = true

	// Re-apply .gitignore, AGENTS.md, PR template — but unlike init,
	// force-rewrite the templates to pick up newer content. We do this
	// by deleting then re-creating; safer than os.Stat-check.
	if err := s.Git.EnsureGitignore([]string{".ml-cache/"}); err == nil {
		r.GitignoreFixed = true
	}
	agentsPath := filepath.Join(s.Git.RepoRoot, "AGENTS.md")
	if _, err := os.Stat(agentsPath); err == nil {
		os.Remove(agentsPath)
	}
	s.writeAgentsMD()
	r.AGENTSWritten = true

	prtPath := filepath.Join(s.Git.RepoRoot, ".github", "PULL_REQUEST_TEMPLATE.md")
	if _, err := os.Stat(prtPath); err == nil {
		os.Remove(prtPath)
	}
	s.writePRTemplate()
	r.PRTplWritten = true

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
		s.Store.WriteMainlineView(view)
	}

	if idx, _ := s.Store.ReadProposedIndex(); idx == nil {
		idx = &domain.ProposedIndex{
			SchemaVersion: 1,
			RebuiltAt:     core.Now(),
		}
		s.Store.WriteProposedIndex(idx)
	}
}

func mustReadFile(st *storage.Store, cfg domain.TeamConfig) string {
	// Serialize config to TOML string for commit
	return fmt.Sprintf(`[mainline]
schema_version = %d
main_branch = "%s"
actor_log_prefix = "%s"
require_seal_before = "%s"
remote = "%s"

[sync]
auto_sync = %v
interval = "%s"

[check]
auto_check = %v
lookback = %d
phase1_threshold = %v
require_before_merge = %v

[publish]
auto_publish = %v

[merge]
strategy = "%s"

[log]
default_limit = %d
`,
		cfg.Mainline.SchemaVersion, cfg.Mainline.MainBranch, cfg.Mainline.ActorLogPrefix, cfg.Mainline.RequireSealBefore, cfg.Mainline.Remote,
		cfg.Sync.AutoSync, cfg.Sync.Interval,
		cfg.Check.AutoCheck, cfg.Check.Lookback, cfg.Check.Phase1Threshold, cfg.Check.RequireBeforeMerge,
		cfg.Publish.AutoPublish,
		cfg.Merge.Strategy,
		cfg.Log.DefaultLimit,
	)
}

func (s *Service) writeAgentsMD() {
	path := filepath.Join(s.Git.RepoRoot, "AGENTS.md")
	if _, err := os.Stat(path); err == nil {
		return // already exists
	}
	content := `## Mainline

<!-- mainline-agents-md-version: 3 -->

This project uses Mainline to record the intent behind AI-assisted code changes.

### Before changing code

    mainline status --json

If there is no active intent, start one:

    mainline start "<short description of the user's goal>" --json

For unfamiliar subsystems, query history (auto-syncs with the team):

    mainline context <keyword> --json

### While working

After each meaningful logical change, record a turn:

    mainline append "<specific description of what changed>" --json

### When the task is complete

1. Make sure all code changes are committed:

       git add <files> && git commit -m "<message>"

2. Prepare a seal package:

       mainline seal --prepare --json

3. Generate JSON matching the returned schema. Include rich tags in
   the fingerprint (primary subsystem, synonyms, parent concepts,
   related technologies):

       "tags": ["auth", "authentication", "security", "jwt", "session"]

4. Submit it:

       mainline seal --submit --json < seal.json

   Mainline syncs with the team and runs phase1 conflict checks
   automatically inside --submit. If the JSON response includes a
   "conflicts" array, surface those conflicts to the user clearly
   before continuing.

### Semantic conflict checks

When asked to check semantic conflicts (auto-syncs first):

    mainline check --prepare --intent <id> --json

Generate a CheckJudgmentResult JSON matching the schema, then submit:

    mainline check --submit --json < judgment.json

### Do not run unless explicitly asked

    mainline merge
    mainline pin <intent> <commit>
    mainline revert
`
	os.WriteFile(path, []byte(content), 0o644)
}

func (s *Service) writePRTemplate() {
	path := filepath.Join(s.Git.RepoRoot, ".github", "PULL_REQUEST_TEMPLATE.md")
	if _, err := os.Stat(path); err == nil {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
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
	os.WriteFile(path, []byte(content), 0o644)
}

// -----------------------------------------------------------
// Status
// -----------------------------------------------------------

type StatusResult struct {
	Initialized   bool                `json:"initialized"`
	Branch        string              `json:"branch,omitempty"`
	ActorID       string              `json:"actor_id,omitempty"`
	ActiveIntent  *domain.DraftIntent `json:"active_intent,omitempty"`
	TurnCount     int                 `json:"turn_count"`
	ProposedCount int                 `json:"proposed_count"`
	MainHead      string              `json:"main_head,omitempty"`
	// rc5: sync staleness surface. LastSync is the persisted record
	// of the most recent successful Sync; nil means never synced in
	// this clone. SyncStaleSeconds and SyncStale are convenience
	// fields so the CLI does not need to do the math.
	LastSync         *domain.LastSync `json:"last_sync,omitempty"`
	SyncStaleSeconds int64            `json:"sync_stale_seconds,omitempty"`
	SyncStale        bool             `json:"sync_stale"`
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
	if err == nil {
		result.ActorID = id.ActorID
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
	result.MainHead = head

	if ls, _ := s.Store.ReadLastSync(); ls != nil {
		result.LastSync = ls
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
		// Never synced — treat as stale so the CLI can prompt.
		result.SyncStale = true
	}

	return result, nil
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
