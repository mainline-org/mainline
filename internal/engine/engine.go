package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mainline/internal/core"
	"mainline/internal/domain"
	"mainline/internal/gitops"
	"mainline/internal/storage"
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

	// Configure git notes fetch/push (rc3: notes are source of truth)
	if s.Git.HasRemote("origin") {
		notesFetch := "+refs/notes/mainline/*:refs/notes/mainline/*"
		if !strings.Contains(s.Git.ConfigGet("remote.origin.fetch"), "refs/notes/mainline") {
			s.Git.ConfigAdd("remote.origin.fetch", notesFetch)
		}
		notesPush := "refs/notes/mainline/*:refs/notes/mainline/*"
		if !strings.Contains(s.Git.ConfigGet("remote.origin.push"), "refs/notes/mainline") {
			s.Git.ConfigAdd("remote.origin.push", notesPush)
		}
	}
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
		cfg.Mainline.SchemaVersion, cfg.Mainline.MainBranch, cfg.Mainline.ActorLogPrefix, cfg.Mainline.RequireSealBefore,
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

<!-- mainline-agents-md-version: 1 -->

This project uses Mainline to record the intent behind AI-assisted code changes.
You are expected to use Mainline while modifying code.

### Before changing code

    mainline status --json
    mainline start "<short description of the user's goal>" --json
    mainline context --json

### While working

    mainline append "<specific description of what changed>" --json

### When the task is complete

    mainline seal --prepare --json
    mainline seal --submit - --json

### Do not run unless explicitly asked by the user

    mainline merge
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

	return result, nil
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
