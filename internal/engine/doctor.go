package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mainline-org/mainline/internal/domain"
)

const DefaultStaleProposedAfter = 72 * time.Hour

type DoctorOptions struct {
	Fix                bool
	StaleAfter         time.Duration
	Proposals          bool
	StaleProposedAfter time.Duration
	Notes              bool
	NotesCommitMapPath string
	NotesInfer         bool
	// Setup, when true, runs install / wiring sanity checks
	// (remote refspec configuration, identity file present and
	// readable) instead of the drafts orphan scan. Combined with Fix=true, missing refspec
	// configuration is rewired in place — Setup never touches the
	// identity file because that's a per-actor decision.
	Setup bool
}

type DoctorResult struct {
	CheckedDrafts int                            `json:"checked_drafts,omitempty"`
	OrphanDrafts  []DoctorDraftFinding           `json:"orphan_drafts,omitempty"`
	StaleDrafts   []DoctorDraftFinding           `json:"stale_drafts,omitempty"`
	DeletedDrafts []string                       `json:"deleted_drafts,omitempty"`
	Setup         *DoctorSetupReport             `json:"setup,omitempty"`
	Proposals     *DoctorProposalReport          `json:"proposals,omitempty"`
	Historical    *DoctorHistoricalSignalsReport `json:"historical_signals,omitempty"`
	Notes         *DoctorNotesReport             `json:"notes,omitempty"`
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
	// locally_modified.
	AgentsBlockState      string   `json:"agents_block_state,omitempty"`
	AgentsBlockVersion    int      `json:"agents_block_version,omitempty"`
	AgentsTemplateVersion int      `json:"agents_template_version,omitempty"`
	GitignoreOK           bool     `json:"gitignore_ok"`
	SSHMultiplexOK        bool     `json:"ssh_multiplex_ok"`
	Fixed                 []string `json:"fixed,omitempty"`       // refspecs added by --fix
	Issues                []string `json:"issues,omitempty"`      // blocking problems
	Suggestions           []string `json:"suggestions,omitempty"` // non-blocking perf tips
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

type DoctorProposalReport struct {
	CheckedProposals int                     `json:"checked_proposals"`
	StaleAfter       string                  `json:"stale_after"`
	Findings         []DoctorProposalFinding `json:"findings,omitempty"`
}

type DoctorProposalFinding struct {
	IntentID           string   `json:"intent_id"`
	Title              string   `json:"title,omitempty"`
	Goal               string   `json:"goal,omitempty"`
	Thread             string   `json:"thread,omitempty"`
	GitBranch          string   `json:"git_branch,omitempty"`
	ActorName          string   `json:"actor_name,omitempty"`
	SealedAt           string   `json:"sealed_at,omitempty"`
	AgeHours           int      `json:"age_hours,omitempty"`
	FindingCodes       []string `json:"finding_codes,omitempty"`
	Reasons            []string `json:"reasons,omitempty"`
	ReplacementHints   []string `json:"replacement_hints,omitempty"`
	RecommendedAction  string   `json:"recommended_action,omitempty"`
	RecommendedCommand string   `json:"recommended_command,omitempty"`
}

type DoctorHistoricalSignalsReport struct {
	SealSummaryRisks        int `json:"seal_summary_risks,omitempty"`
	SealSummaryFollowups    int `json:"seal_summary_followups,omitempty"`
	SealSummaryAntiPatterns int `json:"seal_summary_anti_patterns,omitempty"`
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
	if opts.Proposals {
		if opts.StaleProposedAfter <= 0 {
			opts.StaleProposedAfter = DefaultStaleProposedAfter
		}
		return s.doctorProposals(opts.StaleProposedAfter)
	}
	if opts.Notes {
		return s.doctorNotes(opts.NotesCommitMapPath, opts.NotesInfer)
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

	if view, _ := s.Store.ReadMainlineView(); view != nil {
		result.Historical = doctorHistoricalSignals(view)
	}

	return result, nil
}

func doctorHistoricalSignals(view *domain.MainlineView) *DoctorHistoricalSignalsReport {
	if view == nil {
		return nil
	}
	report := &DoctorHistoricalSignalsReport{
		SealSummaryRisks:     len(materializeLegacyRisks(view, "")),
		SealSummaryFollowups: len(materializeLegacyFollowups(view, "")),
	}
	for _, iv := range view.Intents {
		if iv.Summary == nil {
			continue
		}
		report.SealSummaryAntiPatterns += len(iv.Summary.AntiPatterns)
	}
	if report.SealSummaryRisks == 0 &&
		report.SealSummaryFollowups == 0 &&
		report.SealSummaryAntiPatterns == 0 {
		return nil
	}
	return report
}

func (s *Service) doctorProposals(staleAfter time.Duration) (*DoctorResult, error) {
	view, _ := s.Store.ReadMainlineView()
	report := &DoctorProposalReport{StaleAfter: staleAfter.String()}
	if view == nil {
		return &DoctorResult{Proposals: report}, nil
	}

	now := time.Now().UTC()
	pinMatches := s.proposalPinMatches(view)
	mergedByFile := mergedIntentsByFile(view)

	for _, iv := range view.Intents {
		if iv.Status != domain.StatusProposed {
			continue
		}
		report.CheckedProposals++

		finding := proposalFindingBase(iv, now)
		isStale := finding.AgeHours >= int(staleAfter.Hours())
		if isStale {
			finding.add("stale_proposed", fmt.Sprintf("proposed for %s, past %s cleanup threshold",
				formatDoctorHours(finding.AgeHours), staleAfter))
			if iv.CodeCommit != "" && !s.commitExists(iv.CodeCommit) {
				finding.add("orphan_code_commit", "code commit is not reachable in this clone")
			}
			if iv.GitBranch != "" && !s.Git.BranchExists(iv.GitBranch) {
				finding.add("missing_local_branch", "git branch recorded on the intent is missing locally")
			}
		}
		if pin, ok := pinMatches[iv.IntentID]; ok {
			finding.add("pin_candidate", fmt.Sprintf("matches main commit %s via %s",
				shortDoctorHash(pin.Commit), pin.MatchStrategy))
			finding.RecommendedAction = "pin"
			finding.RecommendedCommand = fmt.Sprintf("mainline pin %s %s", iv.IntentID, pin.Commit)
		}

		if isStale {
			for _, repl := range replacementHints(iv, mergedByFile, now) {
				finding.add("later_merged_overlap", "later merged intent touches the same files")
				finding.ReplacementHints = append(finding.ReplacementHints, repl)
			}
		}

		if len(finding.FindingCodes) == 0 {
			continue
		}
		if finding.RecommendedAction == "" {
			finding.RecommendedAction = "review_then_abandon"
			reason := "proposal doctor: " + strings.Join(finding.Reasons, "; ")
			finding.RecommendedCommand = fmt.Sprintf("mainline abandon %s --reason %s",
				iv.IntentID, strconv.Quote(reason))
		}
		report.Findings = append(report.Findings, finding)
	}

	sort.SliceStable(report.Findings, func(i, j int) bool {
		if report.Findings[i].AgeHours != report.Findings[j].AgeHours {
			return report.Findings[i].AgeHours > report.Findings[j].AgeHours
		}
		return report.Findings[i].IntentID < report.Findings[j].IntentID
	})
	return &DoctorResult{Proposals: report}, nil
}

func proposalFindingBase(iv domain.IntentView, now time.Time) DoctorProposalFinding {
	title := iv.Goal
	if iv.Summary != nil && iv.Summary.Title != "" {
		title = iv.Summary.Title
	}
	ageHours := 0
	if t, err := time.Parse(time.RFC3339, iv.SealedAt); err == nil {
		ageHours = int(now.Sub(t).Hours())
	}
	return DoctorProposalFinding{
		IntentID:  iv.IntentID,
		Title:     title,
		Goal:      iv.Goal,
		Thread:    iv.Thread,
		GitBranch: iv.GitBranch,
		ActorName: iv.ActorName,
		SealedAt:  iv.SealedAt,
		AgeHours:  ageHours,
	}
}

func (f *DoctorProposalFinding) add(code, reason string) {
	for _, existing := range f.FindingCodes {
		if existing == code {
			return
		}
	}
	f.FindingCodes = append(f.FindingCodes, code)
	f.Reasons = append(f.Reasons, reason)
}

func (s *Service) commitExists(commit string) bool {
	_, err := s.Git.Run("rev-parse", "--verify", commit+"^{commit}")
	return err == nil
}

func (s *Service) proposalPinMatches(view *domain.MainlineView) map[string]PinnedCommit {
	cfg, err := s.getTeamConfig()
	if err != nil || cfg == nil {
		return nil
	}
	mainRef := s.syncedMainRef(cfg.Mainline.MainBranch)
	entries, err := s.Git.LogOneline(mainRef, cfg.Check.Lookback)
	if err != nil || len(entries) == 0 {
		return nil
	}
	hashes := make([]string, 0, len(entries))
	for _, e := range entries {
		hashes = append(hashes, e.Hash)
	}
	var codeCommits []string
	seen := map[string]bool{}
	for _, iv := range view.Intents {
		if iv.Status != domain.StatusProposed || iv.CodeCommit == "" || seen[iv.CodeCommit] {
			continue
		}
		codeCommits = append(codeCommits, iv.CodeCommit)
		seen[iv.CodeCommit] = true
	}
	treeOf, _ := s.Git.CommitTreeHashes(hashes)
	intentTreeOf, _ := s.Git.CommitTreeHashes(codeCommits)
	intentSubjects, _ := s.Git.CommitSubjects(codeCommits)
	entryMessages, _ := s.Git.FullCommitMessages(hashes)
	commitParents, _ := s.Git.CommitParents(hashes)
	ctx := &pinContext{
		treeOf:         nonNilStringMap(treeOf),
		intentTreeOf:   nonNilStringMap(intentTreeOf),
		intentSubjects: nonNilStringMap(intentSubjects),
		entryMessages:  nonNilStringMap(entryMessages),
		noteCache:      map[string]string{},
		commitParents:  nonNilStringSliceMap(commitParents),
	}
	out := map[string]PinnedCommit{}
	for _, iv := range view.Intents {
		if iv.Status != domain.StatusProposed {
			continue
		}
		commit, strategy := findPinMatchBatched(iv, entries, ctx)
		if commit == "" {
			continue
		}
		out[iv.IntentID] = PinnedCommit{IntentID: iv.IntentID, Commit: commit, MatchStrategy: strategy}
	}
	return out
}

func nonNilStringMap(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	return in
}

func nonNilStringSliceMap(in map[string][]string) map[string][]string {
	if in == nil {
		return map[string][]string{}
	}
	return in
}

func mergedIntentsByFile(view *domain.MainlineView) map[string][]domain.IntentView {
	out := map[string][]domain.IntentView{}
	for _, iv := range view.Intents {
		if iv.Status != domain.StatusMerged || iv.Fingerprint == nil {
			continue
		}
		for _, f := range iv.Fingerprint.FilesTouched {
			out[f] = append(out[f], iv)
		}
	}
	return out
}

func replacementHints(iv domain.IntentView, byFile map[string][]domain.IntentView, now time.Time) []string {
	if iv.Fingerprint == nil {
		return nil
	}
	sealedAt, err := time.Parse(time.RFC3339, iv.SealedAt)
	if err != nil {
		sealedAt = now
	}
	type candidate struct {
		id      string
		title   string
		overlap int
	}
	candidates := map[string]*candidate{}
	for _, f := range iv.Fingerprint.FilesTouched {
		for _, merged := range byFile[f] {
			mt, err := time.Parse(time.RFC3339, merged.SealedAt)
			if err != nil || !mt.After(sealedAt) {
				continue
			}
			c := candidates[merged.IntentID]
			if c == nil {
				title := merged.Goal
				if merged.Summary != nil && merged.Summary.Title != "" {
					title = merged.Summary.Title
				}
				c = &candidate{id: merged.IntentID, title: title}
				candidates[merged.IntentID] = c
			}
			c.overlap++
		}
	}
	list := make([]candidate, 0, len(candidates))
	for _, c := range candidates {
		if c.overlap < 2 && doctorTextOverlapCount(iv.Goal, c.title) < 2 {
			continue
		}
		list = append(list, *c)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].overlap != list[j].overlap {
			return list[i].overlap > list[j].overlap
		}
		return list[i].id < list[j].id
	})
	if len(list) > 3 {
		list = list[:3]
	}
	out := make([]string, 0, len(list))
	for _, c := range list {
		out = append(out, fmt.Sprintf("%s (%s; %d shared file(s))", c.id, c.title, c.overlap))
	}
	return out
}

func doctorTextOverlapCount(a, b string) int {
	seen := map[string]bool{}
	for _, tok := range strings.Fields(strings.ToLower(a)) {
		tok = strings.Trim(tok, ".,:;()[]{}<>\"'`")
		if len(tok) >= 4 {
			seen[tok] = true
		}
	}
	count := 0
	for _, tok := range strings.Fields(strings.ToLower(b)) {
		tok = strings.Trim(tok, ".,:;()[]{}<>\"'`")
		if len(tok) >= 4 && seen[tok] {
			count++
		}
	}
	return count
}

func formatDoctorHours(hours int) string {
	if hours < 24 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dd", hours/24)
}

func shortDoctorHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
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

	// AGENTS.md is optional repo policy. Report its state for visibility,
	// but do not fail setup when it is absent.
	rep.AgentsMDOK = fileExists(filepath.Join(s.Git.RepoRoot, "AGENTS.md"))
	if g := s.AgentsGuidanceState(); g != nil {
		rep.AgentsBlockState = string(g.State)
		rep.AgentsBlockVersion = g.InstalledVersion
		rep.AgentsTemplateVersion = g.CurrentVersion
		switch g.State {
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
	rep.GitignoreOK = gitignoreContains(s.Git.RepoRoot, ".ml-cache/")
	if !rep.GitignoreOK {
		rep.Issues = append(rep.Issues, "'.ml-cache/' missing from .gitignore — run 'mainline init --rewire'")
	}

	// SSH ControlMaster check — non-blocking performance suggestion.
	// Only relevant when remote uses SSH (git@... or ssh://...).
	if rep.HasRemote {
		remoteURL := s.Git.ConfigGet("remote." + remote + ".url")
		if isSSHRemote(remoteURL) {
			rep.SSHMultiplexOK = sshControlMasterConfigured()
			if !rep.SSHMultiplexOK {
				rep.Suggestions = append(rep.Suggestions,
					"SSH ControlMaster not detected — enable it to cut sync latency from ~3s to ~1s on repeat runs. "+
						"Add to ~/.ssh/config:\n"+
						"  Host github.com\n"+
						"    ControlMaster auto\n"+
						"    ControlPath ~/.ssh/sockets/%r@%h-%p\n"+
						"    ControlPersist 600")
			}
		} else {
			rep.SSHMultiplexOK = true // N/A for HTTPS
		}
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
	refspecIssue := "remote refspecs incomplete — run 'mainline init --rewire' (or 'mainline doctor --setup --fix')"
	if rep.HasRemote {
		fetch := s.Git.ConfigGet(fetchKey)
		push := s.Git.ConfigGet(pushKey)
		rep.NotesFetchOK = strings.Contains(fetch, "refs/notes/mainline")
		rep.NotesPushOK = strings.Contains(push, "refs/notes/mainline")
		rep.ActorFetchOK = actorFetchRefspecsOK(fetch, cfg.Mainline.ActorLogPrefix, remote)
		rep.ActorPushOK = strings.Contains(push, domain.ActorLogPushRefspec(cfg.Mainline.ActorLogPrefix))
		if !rep.NotesFetchOK || !rep.NotesPushOK || !rep.ActorFetchOK || !rep.ActorPushOK {
			rep.Issues = append(rep.Issues, refspecIssue)
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
		rep.ActorFetchOK = actorFetchRefspecsOK(fetch, cfg.Mainline.ActorLogPrefix, remote)
		rep.ActorPushOK = strings.Contains(push, domain.ActorLogPushRefspec(cfg.Mainline.ActorLogPrefix))
		if rep.NotesFetchOK && rep.NotesPushOK && rep.ActorFetchOK && rep.ActorPushOK {
			rep.Issues = removeIssue(rep.Issues, refspecIssue)
		}
	}

	return &DoctorResult{Setup: rep}, nil
}

func actorFetchRefspecsOK(fetchConfig, actorLogPrefix, remote string) bool {
	for _, refspec := range []string{
		domain.ActorLogFetchRefspec(actorLogPrefix, remote),
		domain.BranchBackedActorLogFetchRefspec(actorLogPrefix, remote),
		domain.LegacyActorLogFetchRefspec(remote),
	} {
		if !strings.Contains(fetchConfig, strings.TrimPrefix(refspec, "+")) {
			return false
		}
	}
	return true
}

func removeIssue(issues []string, target string) []string {
	filtered := issues[:0]
	for _, issue := range issues {
		if issue != target {
			filtered = append(filtered, issue)
		}
	}
	return filtered
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

// isSSHRemote returns true if the URL looks like an SSH remote
// (git@host:... or ssh://...).
func isSSHRemote(url string) bool {
	return strings.HasPrefix(url, "git@") || strings.HasPrefix(url, "ssh://")
}

// sshControlMasterConfigured checks whether the host extracted from
// the remote URL has ControlMaster configured in ~/.ssh/config.
// This is a best-effort heuristic — it reads the SSH config file and
// looks for ControlMaster in Host blocks matching the remote host.
func sshControlMasterConfigured() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		return false // no SSH config → not configured
	}
	content := strings.ToLower(string(data))
	// Simple heuristic: if ControlMaster appears anywhere in the SSH
	// config, we assume it's configured (could be Host * or specific).
	return strings.Contains(content, "controlmaster")
}
