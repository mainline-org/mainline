package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"mainline/internal/domain"
	"mainline/internal/gitops"
)

// Store manages .ml-cache and .mainline file I/O.
type Store struct {
	RepoRoot string
	Git      *gitops.Git
}

// New creates a Store for the given repo root.
func New(repoRoot string, g *gitops.Git) *Store {
	return &Store{RepoRoot: repoRoot, Git: g}
}

// -----------------------------------------------------------
// Paths
// -----------------------------------------------------------

func (s *Store) mainlineDir() string { return filepath.Join(s.RepoRoot, ".mainline") }
func (s *Store) cacheDir() string    { return filepath.Join(s.RepoRoot, ".ml-cache") }
func (s *Store) draftsDir() string   { return filepath.Join(s.cacheDir(), "drafts") }
func (s *Store) viewsDir() string    { return filepath.Join(s.cacheDir(), "views") }
func (s *Store) sessionsDir() string { return filepath.Join(s.cacheDir(), "sessions") }

func (s *Store) teamConfigPath() string  { return filepath.Join(s.mainlineDir(), "config.toml") }
func (s *Store) localConfigPath() string { return filepath.Join(s.mainlineDir(), "local.toml") }
func (s *Store) identityPath() string    { return filepath.Join(s.cacheDir(), "identity.json") }

// -----------------------------------------------------------
// Init dirs
// -----------------------------------------------------------

// EnsureDirs creates all required directories.
func (s *Store) EnsureDirs() error {
	dirs := []string{
		s.mainlineDir(),
		s.cacheDir(),
		s.draftsDir(),
		s.viewsDir(),
		s.sessionsDir(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

// IsInitialized checks if .mainline/config.toml exists.
func (s *Store) IsInitialized() bool {
	_, err := os.Stat(s.teamConfigPath())
	return err == nil
}

// -----------------------------------------------------------
// Team config
// -----------------------------------------------------------

func (s *Store) ReadTeamConfig() (*domain.TeamConfig, error) {
	var cfg domain.TeamConfig
	data, err := os.ReadFile(s.teamConfigPath())
	if err != nil {
		return nil, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config.toml: %w", err)
	}
	return &cfg, nil
}

func (s *Store) WriteTeamConfig(cfg *domain.TeamConfig) error {
	if err := os.MkdirAll(s.mainlineDir(), 0o755); err != nil {
		return err
	}
	f, err := os.Create(s.teamConfigPath())
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// -----------------------------------------------------------
// Local config
// -----------------------------------------------------------

func (s *Store) ReadLocalConfig() (*domain.LocalConfig, error) {
	var cfg domain.LocalConfig
	data, err := os.ReadFile(s.localConfigPath())
	if err != nil {
		return nil, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse local.toml: %w", err)
	}
	return &cfg, nil
}

func (s *Store) WriteLocalConfig(cfg *domain.LocalConfig) error {
	if err := os.MkdirAll(s.mainlineDir(), 0o755); err != nil {
		return err
	}
	f, err := os.Create(s.localConfigPath())
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// -----------------------------------------------------------
// Identity
// -----------------------------------------------------------

func (s *Store) ReadIdentity() (*domain.Identity, error) {
	data, err := os.ReadFile(s.identityPath())
	if err != nil {
		return nil, err
	}
	var id domain.Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return nil, err
	}
	return &id, nil
}

func (s *Store) WriteIdentity(id *domain.Identity) error {
	if err := os.MkdirAll(s.cacheDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.identityPath(), data, 0o644)
}

// -----------------------------------------------------------
// Draft intents
// -----------------------------------------------------------

func (s *Store) draftPath(intentID string) string {
	return filepath.Join(s.draftsDir(), intentID+".json")
}

func (s *Store) turnsPath(intentID string) string {
	return filepath.Join(s.draftsDir(), intentID+".turns.jsonl")
}

func (s *Store) ReadDraft(intentID string) (*domain.DraftIntent, error) {
	data, err := os.ReadFile(s.draftPath(intentID))
	if err != nil {
		return nil, err
	}
	var draft domain.DraftIntent
	if err := json.Unmarshal(data, &draft); err != nil {
		return nil, err
	}
	return &draft, nil
}

func (s *Store) WriteDraft(draft *domain.DraftIntent) error {
	if err := os.MkdirAll(s.draftsDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(draft, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.draftPath(draft.IntentID), data, 0o644)
}

func (s *Store) DeleteDraft(intentID string) error {
	os.Remove(s.draftPath(intentID))
	os.Remove(s.turnsPath(intentID))
	return nil
}

// ListDrafts returns all draft intent IDs.
func (s *Store) ListDrafts() ([]string, error) {
	entries, err := os.ReadDir(s.draftsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".json") && !strings.Contains(name, ".turns.") {
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
	}
	return ids, nil
}

// FindActiveDraft returns the first draft in "drafting" status for the given branch, or nil.
func (s *Store) FindActiveDraft(branch string) (*domain.DraftIntent, error) {
	ids, err := s.ListDrafts()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		d, err := s.ReadDraft(id)
		if err != nil {
			continue
		}
		if d.Status == domain.StatusDrafting && d.GitBranch == branch {
			return d, nil
		}
	}
	return nil, nil
}

// AppendTurn appends a turn to the JSONL log.
func (s *Store) AppendTurn(turn *domain.Turn) error {
	if err := os.MkdirAll(s.draftsDir(), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(turn)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.turnsPath(turn.IntentID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	f.Write(data)
	f.WriteString("\n")
	return nil
}

// ReadTurns reads all turns for an intent from the JSONL log.
func (s *Store) ReadTurns(intentID string) ([]domain.Turn, error) {
	data, err := os.ReadFile(s.turnsPath(intentID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var turns []domain.Turn
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var t domain.Turn
		if err := json.Unmarshal([]byte(line), &t); err != nil {
			continue
		}
		turns = append(turns, t)
	}
	return turns, nil
}

// -----------------------------------------------------------
// Actor log (via git plumbing)
// -----------------------------------------------------------

// ActorLogRef returns the git ref for an actor's log.
func (s *Store) ActorLogRef(actorID, prefix string) string {
	return fmt.Sprintf("refs/heads/%s/%s", prefix, actorID)
}

// AppendActorLogEvent appends an event to the actor's log via git plumbing.
func (s *Store) AppendActorLogEvent(actorID, prefix string, event interface{}) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	// Hash the event as a blob
	blobHash, err := s.Git.HashObject(data)
	if err != nil {
		return fmt.Errorf("hash event blob: %w", err)
	}

	ref := s.ActorLogRef(actorID, prefix)
	parentCommit := s.Git.ReadRef(ref)

	// Create tree with the single blob
	treeHash, err := s.Git.MakeTree("event.json", blobHash)
	if err != nil {
		return fmt.Errorf("make tree: %w", err)
	}

	// Create commit
	commitHash, err := s.Git.CommitTree(treeHash, parentCommit, "actor-log-event")
	if err != nil {
		return fmt.Errorf("commit tree: %w", err)
	}

	// Update ref
	if err := s.Git.UpdateRef(ref, commitHash); err != nil {
		return fmt.Errorf("update ref: %w", err)
	}

	return nil
}

// ReadActorLogEvents reads all events from an actor's log.
func (s *Store) ReadActorLogEvents(actorID, prefix string) ([]json.RawMessage, error) {
	ref := s.ActorLogRef(actorID, prefix)
	return s.ReadActorLogEventsFromRef(ref)
}

// ReadActorLogEventsFromRef reads all events from a concrete actor log ref.
func (s *Store) ReadActorLogEventsFromRef(ref string) ([]json.RawMessage, error) {
	head := s.Git.ReadRef(ref)
	if head == "" {
		return nil, nil
	}

	var events []json.RawMessage
	commit := head
	for commit != "" {
		// Get tree from commit
		treeOut, err := s.Git.Run("log", "-1", "--format=%T", commit)
		if err != nil {
			break
		}
		treeHash := strings.TrimSpace(treeOut)

		// List tree entries to find event.json blob
		entries, err := s.Git.ListTree(treeHash)
		if err != nil {
			break
		}
		for _, e := range entries {
			if e.Name == "event.json" {
				blob, err := s.Git.CatBlob(e.Hash)
				if err == nil {
					events = append(events, json.RawMessage(blob))
				}
			}
		}

		// Walk to parent
		parentOut, err := s.Git.Run("log", "-1", "--format=%P", commit)
		if err != nil {
			break
		}
		commit = strings.TrimSpace(parentOut)
	}

	// Reverse to chronological order
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events, nil
}

// -----------------------------------------------------------
// Views
// -----------------------------------------------------------

func (s *Store) mainlineViewPath() string  { return filepath.Join(s.viewsDir(), "mainline.json") }
func (s *Store) proposedIndexPath() string { return filepath.Join(s.viewsDir(), "proposed-index.json") }
func (s *Store) lastSyncPath() string      { return filepath.Join(s.viewsDir(), "last-sync.json") }

// ReadLastSync returns the persisted record of the most recent
// successful sync, or nil if none exists. Errors other than "not
// found" propagate.
func (s *Store) ReadLastSync() (*domain.LastSync, error) {
	data, err := os.ReadFile(s.lastSyncPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ls domain.LastSync
	if err := json.Unmarshal(data, &ls); err != nil {
		return nil, err
	}
	return &ls, nil
}

func (s *Store) WriteLastSync(ls *domain.LastSync) error {
	if err := os.MkdirAll(s.viewsDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ls, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.lastSyncPath(), data, 0o644)
}

func (s *Store) ReadMainlineView() (*domain.MainlineView, error) {
	data, err := os.ReadFile(s.mainlineViewPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &domain.MainlineView{SchemaVersion: 1}, nil
		}
		return nil, err
	}
	var v domain.MainlineView
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func (s *Store) WriteMainlineView(v *domain.MainlineView) error {
	if err := os.MkdirAll(s.viewsDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.mainlineViewPath(), data, 0o644)
}

func (s *Store) ReadProposedIndex() (*domain.ProposedIndex, error) {
	data, err := os.ReadFile(s.proposedIndexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &domain.ProposedIndex{SchemaVersion: 1}, nil
		}
		return nil, err
	}
	var idx domain.ProposedIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func (s *Store) WriteProposedIndex(idx *domain.ProposedIndex) error {
	if err := os.MkdirAll(s.viewsDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.proposedIndexPath(), data, 0o644)
}

// -----------------------------------------------------------
// Threads
// -----------------------------------------------------------

func (s *Store) threadsDir() string { return filepath.Join(s.cacheDir(), "threads") }

// threadFileName sanitizes thread name for use as filename (replace / with _).
func threadFileName(name string) string {
	return strings.ReplaceAll(name, "/", "_") + ".json"
}

func (s *Store) ReadThread(name string) (*domain.Thread, error) {
	data, err := os.ReadFile(filepath.Join(s.threadsDir(), threadFileName(name)))
	if err != nil {
		return nil, err
	}
	var t domain.Thread
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) WriteThread(t *domain.Thread) error {
	if err := os.MkdirAll(s.threadsDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.threadsDir(), threadFileName(t.Name)), data, 0o644)
}

func (s *Store) ListThreads() ([]domain.Thread, error) {
	entries, err := os.ReadDir(s.threadsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var threads []domain.Thread
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.threadsDir(), e.Name()))
		if err != nil {
			continue
		}
		var t domain.Thread
		if err := json.Unmarshal(data, &t); err != nil {
			continue
		}
		threads = append(threads, t)
	}
	sort.Slice(threads, func(i, j int) bool {
		return threads[i].CreatedAt > threads[j].CreatedAt
	})
	return threads, nil
}
