package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mainline-org/mainline/internal/domain"
)

type DoctorOptions struct {
	Fix        bool
	StaleAfter time.Duration
	// Setup, when true, runs install / wiring sanity checks
	// (remote refspec configuration, identity file present and
	// readable, AGENTS.md present in repo root) instead of the
	// drafts orphan scan. Combined with Fix=true, missing refspec
	// configuration is rewired in place — Setup never touches the
	// identity file because that's a per-actor decision.
	Setup bool
}

type DoctorResult struct {
	CheckedDrafts int                  `json:"checked_drafts,omitempty"`
	OrphanDrafts  []DoctorDraftFinding `json:"orphan_drafts,omitempty"`
	StaleDrafts   []DoctorDraftFinding `json:"stale_drafts,omitempty"`
	DeletedDrafts []string             `json:"deleted_drafts,omitempty"`
	Setup         *DoctorSetupReport   `json:"setup,omitempty"`
}

// DoctorSetupReport summarises every install / wiring check the doctor
// runs in --setup mode. Each field is a small struct so the consumer
// (CLI text output, JSON, or future TUI) can render the *fix me* state
// without grepping a free-form message string.
//
// RemoteName is whichever git remote mainline talks to (defaults to
// "origin"; teams that push to a non-origin remote configure it via
// [mainline] remote in .mainline/config.toml). HasRemote checks
// whether that named remote actually exists in `git remote`.
type DoctorSetupReport struct {
	RemoteName        string `json:"remote_name"`
	HasRemote         bool   `json:"has_remote"`
	NotesFetchOK      bool   `json:"notes_fetch_ok"`
	NotesPushOK       bool   `json:"notes_push_ok"`
	ActorFetchOK      bool   `json:"actor_fetch_ok"`
	ActorPushOK       bool   `json:"actor_push_ok"`
	NotesDisplayRefOK bool   `json:"notes_display_ref_ok"`
	IdentityOK        bool   `json:"identity_ok"`
	IdentityActorID   string `json:"identity_actor_id,omitempty"`
	AgentsMDOK        bool   `json:"agents_md_ok"`
	// AgentsBlockState reports the state of the Mainline-managed
	// block inside AGENTS.md (independent of file presence).
	// Values: not_installed | legacy | in_sync | update_available |
	// locally_modified. Use `mainline agents check` for the full
	// per-file report including the IDE stubs.
	AgentsBlockState      string   `json:"agents_block_state,omitempty"`
	AgentsBlockVersion    int      `json:"agents_block_version,omitempty"`
	AgentsTemplateVersion int      `json:"agents_template_version,omitempty"`
	PRTemplateOK          bool     `json:"pr_template_ok"`
	GitignoreOK           bool     `json:"gitignore_ok"`
	Fixed                 []string `json:"fixed,omitempty"` // refspecs added by --fix
	Issues                []string `json:"issues,omitempty"`
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

	if opts.Setup {
		return s.doctorSetup(opts.Fix)
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

// doctorSetup runs the install / wiring sanity checks. Always
// inspects every dimension and populates DoctorSetupReport.Issues
// with one human-readable line per problem; the bool fields support
// programmatic JSON consumers. When fix is true and origin exists,
// missing refspec configuration is rewired in place via Service.Rewire.
func (s *Service) doctorSetup(fix bool) (*DoctorResult, error) {
	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}
	remote := s.remoteName()
	rep := &DoctorSetupReport{
		RemoteName: remote,
		HasRemote:  s.Git.HasRemote(remote),
	}

	// Identity check
	if id, err := s.Store.ReadIdentity(); err == nil && id != nil && id.ActorID != "" {
		rep.IdentityOK = true
		rep.IdentityActorID = id.ActorID
	} else {
		rep.Issues = append(rep.Issues,
			"identity file missing — run 'mainline init --actor-name <name>'")
	}

	// AGENTS.md / PR template / .gitignore presence + managed-block
	// state. The managed block is the Mainline-owned section; user
	// content above/below the markers is preserved across updates.
	rep.AgentsMDOK = fileExists(filepath.Join(s.Git.RepoRoot, "AGENTS.md"))
	if g := s.AgentsGuidanceState(); g != nil {
		rep.AgentsBlockState = string(g.State)
		rep.AgentsBlockVersion = g.InstalledVersion
		rep.AgentsTemplateVersion = g.CurrentVersion
		switch g.State {
		case AgentsBlockStateNotInstalled:
			rep.Issues = append(rep.Issues,
				"AGENTS.md missing or has no Mainline agent guidance — run 'mainline agents install'")
		case AgentsBlockStateLegacy:
			rep.Issues = append(rep.Issues,
				"AGENTS.md has legacy agent guidance (pre-v0.4 format) — run 'mainline agents update' to migrate")
		case AgentsBlockStateUpdateAvailable:
			rep.Issues = append(rep.Issues, fmt.Sprintf(
				"AGENTS.md agent guidance is v%d, this binary's template is v%d — run 'mainline agents diff' then 'agents update'",
				g.InstalledVersion, g.CurrentVersion))
		case AgentsBlockStateLocallyModified:
			rep.Issues = append(rep.Issues,
				"AGENTS.md agent guidance has local edits — run 'mainline agents check' to review")
		}
	}
	prTemplateExists, prTemplateLegacy := prTemplateState(s.Git.RepoRoot)
	rep.PRTemplateOK = prTemplateExists && !prTemplateLegacy
	if !prTemplateExists {
		rep.Issues = append(rep.Issues, ".github/PULL_REQUEST_TEMPLATE.md missing — run 'mainline init --rewire'")
	} else if prTemplateLegacy {
		rep.Issues = append(rep.Issues, ".github/PULL_REQUEST_TEMPLATE.md uses deprecated Mainline trailers — run 'mainline init --rewire'")
	}
	rep.GitignoreOK = gitignoreContains(s.Git.RepoRoot, ".ml-cache/")
	if !rep.GitignoreOK {
		rep.Issues = append(rep.Issues, "'.ml-cache/' missing from .gitignore — run 'mainline init --rewire'")
	}

	// notes.displayRef config — informative, not load-bearing
	rep.NotesDisplayRefOK = strings.Contains(s.Git.ConfigGet("notes.displayRef"), "refs/notes/mainline")
	if !rep.NotesDisplayRefOK {
		rep.Issues = append(rep.Issues,
			"notes.displayRef not pointing at mainline — 'git log' will not show notes inline")
	}

	// Refspec checks (only meaningful when the configured remote exists)
	fetchKey := "remote." + remote + ".fetch"
	pushKey := "remote." + remote + ".push"
	if rep.HasRemote {
		fetch := s.Git.ConfigGet(fetchKey)
		push := s.Git.ConfigGet(pushKey)
		rep.NotesFetchOK = strings.Contains(fetch, "refs/notes/mainline")
		rep.NotesPushOK = strings.Contains(push, "refs/notes/mainline")
		rep.ActorFetchOK = strings.Contains(fetch, "refs/heads/"+cfg.Mainline.ActorLogPrefix)
		rep.ActorPushOK = strings.Contains(push, "refs/heads/"+cfg.Mainline.ActorLogPrefix)
		if !rep.NotesFetchOK || !rep.NotesPushOK || !rep.ActorFetchOK || !rep.ActorPushOK {
			rep.Issues = append(rep.Issues,
				"remote refspecs incomplete — run 'mainline init --rewire' (or 'mainline doctor --setup --fix')")
		}
	} else {
		rep.Issues = append(rep.Issues, fmt.Sprintf(
			"no '%s' remote configured — cross-actor sync requires one. "+
				"Either `git remote add %s <url>`, or set [mainline] remote = \"<name>\" "+
				"in .mainline/config.toml then re-run with --fix",
			remote, remote))
	}

	if fix && rep.HasRemote {
		added := s.configureRemoteRefspecs(cfg.Mainline.ActorLogPrefix)
		rep.Fixed = added
		// Re-evaluate the refspec booleans after the fix attempt so
		// the JSON consumer sees the post-fix state.
		fetch := s.Git.ConfigGet(fetchKey)
		push := s.Git.ConfigGet(pushKey)
		rep.NotesFetchOK = strings.Contains(fetch, "refs/notes/mainline")
		rep.NotesPushOK = strings.Contains(push, "refs/notes/mainline")
		rep.ActorFetchOK = strings.Contains(fetch, "refs/heads/"+cfg.Mainline.ActorLogPrefix)
		rep.ActorPushOK = strings.Contains(push, "refs/heads/"+cfg.Mainline.ActorLogPrefix)
	}

	return &DoctorResult{Setup: rep}, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func gitignoreContains(repoRoot, pattern string) bool {
	data, err := os.ReadFile(filepath.Join(repoRoot, ".gitignore"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), pattern)
}
