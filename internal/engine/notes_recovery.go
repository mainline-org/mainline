package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/gitops"
)

type DoctorNotesReport struct {
	MainRef                       string              `json:"main_ref"`
	MainHead                      string              `json:"main_head,omitempty"`
	NotesRef                      string              `json:"notes_ref,omitempty"`
	NotesTotal                    int                 `json:"notes_total"`
	ReachableNotes                int                 `json:"reachable_notes"`
	UnreachableNotes              int                 `json:"unreachable_notes"`
	UnreachableMainlineNotes      int                 `json:"unreachable_mainline_notes"`
	InvalidMainlineNotes          int                 `json:"invalid_mainline_notes,omitempty"`
	ProposedCount                 int                 `json:"proposed_count,omitempty"`
	SuspiciousProposedCount       int                 `json:"suspicious_proposed_count,omitempty"`
	LikelyHistoryRewrite          bool                `json:"likely_history_rewrite"`
	RecommendedCommand            string              `json:"recommended_command,omitempty"`
	MigrationPlan                 *NotesMigrationPlan `json:"migration_plan,omitempty"`
	NotesRewriteRecoveryAvailable bool                `json:"notes_rewrite_recovery_available,omitempty"`
}

type NotesMigrationOptions struct {
	CommitMapPath string `json:"commit_map_path,omitempty"`
	Infer         bool   `json:"infer,omitempty"`
	Write         bool   `json:"write,omitempty"`
	Push          bool   `json:"push,omitempty"`
}

type NotesMigrationResult struct {
	Plan              *NotesMigrationPlan `json:"plan"`
	Wrote             bool                `json:"wrote,omitempty"`
	Pushed            bool                `json:"pushed,omitempty"`
	OldLocalNotesRef  string              `json:"old_local_notes_ref,omitempty"`
	NewLocalNotesRef  string              `json:"new_local_notes_ref,omitempty"`
	OldRemoteNotesRef string              `json:"old_remote_notes_ref,omitempty"`
	RemoteName        string              `json:"remote_name,omitempty"`
}

type NotesMigrationPlan struct {
	MainRef        string                  `json:"main_ref"`
	MainHead       string                  `json:"main_head,omitempty"`
	NotesRef       string                  `json:"notes_ref,omitempty"`
	Mode           string                  `json:"mode"`
	SafeMigrations []NotesMigration        `json:"safe_migrations,omitempty"`
	ReviewRequired []NotesMigrationReview  `json:"review_required,omitempty"`
	Unresolved     []NotesMigrationProblem `json:"unresolved,omitempty"`
}

type NotesMigration struct {
	OldCommit  string   `json:"old_commit"`
	NewCommit  string   `json:"new_commit"`
	Strategy   string   `json:"strategy"`
	Confidence string   `json:"confidence"`
	IntentIDs  []string `json:"intent_ids,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

type NotesMigrationReview struct {
	OldCommit  string   `json:"old_commit"`
	Candidates []string `json:"candidates,omitempty"`
	Strategy   string   `json:"strategy"`
	IntentIDs  []string `json:"intent_ids,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

type NotesMigrationProblem struct {
	OldCommit string   `json:"old_commit"`
	IntentIDs []string `json:"intent_ids,omitempty"`
	Reason    string   `json:"reason"`
}

func (s *Service) doctorNotes(commitMapPath string, infer bool) (*DoctorResult, error) {
	report, err := s.buildDoctorNotesReport(commitMapPath, infer)
	if err != nil {
		return nil, err
	}
	return &DoctorResult{Notes: report}, nil
}

func (s *Service) buildDoctorNotesReport(commitMapPath string, infer bool) (*DoctorNotesReport, error) {
	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}
	mainRef := s.syncedMainRef(cfg.Mainline.MainBranch)
	mainHead := s.Git.ReadRef(mainRef)
	notesRef := s.Git.ReadRef(gitops.NotesRef)

	entries, err := s.Git.NotesListEntries()
	if err != nil {
		return nil, err
	}
	reachable, _ := s.Git.RevListSet(mainRef)

	report := &DoctorNotesReport{
		MainRef:  mainRef,
		MainHead: mainHead,
		NotesRef: notesRef,
	}
	report.NotesTotal = len(entries)

	var unreachable []gitops.NoteEntry
	for _, entry := range entries {
		if reachable[entry.CommitHash] {
			report.ReachableNotes++
			continue
		}
		report.UnreachableNotes++
		unreachable = append(unreachable, entry)
	}

	// Doctor is the full live diagnosis surface. Parse unreachable note
	// blobs through one cat-file --batch process instead of forking
	// `git notes show` once per stale entry.
	batch, batchErr := s.Git.OpenCatFileBatch()
	if batchErr == nil {
		defer batch.Close()
	}
	for _, entry := range unreachable {
		raw, err := s.readNoteBlob(entry, batch)
		if err != nil {
			continue
		}
		note, ok := parseMainlineCommitNote(raw)
		if !ok {
			if strings.TrimSpace(raw) != "" {
				report.InvalidMainlineNotes++
			}
			continue
		}
		if len(note.Intents) > 0 || len(note.Reverts) > 0 {
			report.UnreachableMainlineNotes++
		}
	}

	if idx, _ := s.Store.ReadProposedIndex(); idx != nil {
		report.ProposedCount = len(idx.Proposed)
	}
	if view, _ := s.Store.ReadMainlineView(); view != nil {
		if health := collectStatusProposalHealth(view, DefaultStaleProposedAfter); health != nil {
			report.SuspiciousProposedCount = health.SuspiciousCount
		}
	}

	report.LikelyHistoryRewrite = likelyNotesHistoryRewrite(
		report.NotesTotal,
		report.ReachableNotes,
		report.UnreachableMainlineNotes,
		report.ProposedCount,
	)
	if report.LikelyHistoryRewrite {
		report.RecommendedCommand = "mainline migrate notes --infer --dry-run"
		report.NotesRewriteRecoveryAvailable = true
	}

	if commitMapPath != "" || infer {
		plan, err := s.buildNotesMigrationPlan(NotesMigrationOptions{
			CommitMapPath: commitMapPath,
			Infer:         infer,
		})
		if err != nil {
			return nil, err
		}
		report.MigrationPlan = plan
	}

	return report, nil
}

func likelyNotesHistoryRewrite(notesTotal, reachableNotes, unreachableMainlineNotes, proposedCount int) bool {
	unreachableDominates := notesTotal > 0 && unreachableMainlineNotes*2 >= notesTotal
	return unreachableMainlineNotes > 0 &&
		(proposedCount >= 20 ||
			reachableNotes == 0 ||
			(unreachableMainlineNotes >= 10 && unreachableDominates))
}

func (s *Service) readNoteBlob(entry gitops.NoteEntry, batch *gitops.CatFileBatch) (string, error) {
	if entry.NoteBlob == "" || batch == nil {
		raw, err := s.Git.NotesShow(entry.CommitHash)
		return raw, err
	}
	data, err := batch.Read(entry.NoteBlob)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (s *Service) MigrateNotes(opts NotesMigrationOptions) (*NotesMigrationResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}
	if opts.Push && !opts.Write {
		return nil, fmt.Errorf("--push requires --write")
	}

	oldLocal := s.Git.ReadRef(gitops.NotesRef)
	var oldRemote string
	remote := s.remoteName()
	if opts.Push {
		oldRemote = s.remoteNotesRef(remote)
		if oldRemote == "" {
			return nil, fmt.Errorf("remote %s has no %s ref to lease against", remote, gitops.NotesRef)
		}
		if oldLocal == "" || oldLocal != oldRemote {
			return nil, fmt.Errorf("local %s is not up to date with %s; fetch notes before --push", gitops.NotesRef, remote)
		}
	}

	plan, err := s.buildNotesMigrationPlan(opts)
	if err != nil {
		return nil, err
	}
	result := &NotesMigrationResult{
		Plan:              plan,
		OldLocalNotesRef:  oldLocal,
		OldRemoteNotesRef: oldRemote,
		RemoteName:        remote,
	}

	if !opts.Write || len(plan.SafeMigrations) == 0 {
		result.NewLocalNotesRef = s.Git.ReadRef(gitops.NotesRef)
		return result, nil
	}

	for _, migration := range plan.SafeMigrations {
		raw, _ := s.Git.NotesShow(migration.OldCommit)
		note, ok := parseMainlineCommitNote(raw)
		if !ok {
			continue
		}
		if err := upsertCommitNote(s.Git, migration.NewCommit, note); err != nil {
			return nil, fmt.Errorf("write note %s -> %s: %w", migration.OldCommit, migration.NewCommit, err)
		}
		_ = s.Git.NotesRemove(migration.OldCommit)
	}
	result.Wrote = true
	result.NewLocalNotesRef = s.Git.ReadRef(gitops.NotesRef)

	if cfg, err := s.getTeamConfig(); err == nil && cfg != nil {
		_ = s.refreshLocalViewIndexes(cfg)
	}

	if opts.Push {
		lease := fmt.Sprintf("--force-with-lease=%s:%s", gitops.NotesRef, oldRemote)
		refspec := fmt.Sprintf("%s:%s", gitops.NotesRef, gitops.NotesRef)
		if err := s.Git.Push(remote, lease, refspec); err != nil {
			return nil, fmt.Errorf("push migrated notes: %w", err)
		}
		result.Pushed = true
	}

	return result, nil
}

func (s *Service) buildNotesMigrationPlan(opts NotesMigrationOptions) (*NotesMigrationPlan, error) {
	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}
	mainRef := s.syncedMainRef(cfg.Mainline.MainBranch)
	mainHead := s.Git.ReadRef(mainRef)
	notesRef := s.Git.ReadRef(gitops.NotesRef)
	entries, err := s.Git.NotesListEntries()
	if err != nil {
		return nil, err
	}
	reachable, _ := s.Git.RevListSet(mainRef)
	commitMap, err := readCommitMap(opts.CommitMapPath)
	if err != nil {
		return nil, err
	}

	plan := &NotesMigrationPlan{
		MainRef:  mainRef,
		MainHead: mainHead,
		NotesRef: notesRef,
		Mode:     notesMigrationMode(opts),
	}

	var treeIndex map[string][]string
	var patchIndex map[string][]string
	ensureTreeIndex := func() map[string][]string {
		if treeIndex != nil {
			return treeIndex
		}
		treeIndex = map[string][]string{}
		trees, _ := s.Git.CommitTreeHashesForRef(mainRef)
		for commit, tree := range trees {
			if tree != "" && reachable[commit] {
				treeIndex[tree] = append(treeIndex[tree], commit)
			}
		}
		for tree := range treeIndex {
			sort.Strings(treeIndex[tree])
		}
		return treeIndex
	}
	ensurePatchIndex := func() map[string][]string {
		if patchIndex != nil {
			return patchIndex
		}
		patchIndex = map[string][]string{}
		commits, _ := s.Git.RevList(mainRef)
		for _, commit := range commits {
			pid, _ := s.Git.PatchID(commit)
			if pid == "" {
				continue
			}
			patchIndex[pid] = append(patchIndex[pid], commit)
		}
		for pid := range patchIndex {
			sort.Strings(patchIndex[pid])
		}
		return patchIndex
	}

	for _, entry := range entries {
		if reachable[entry.CommitHash] {
			continue
		}
		raw, _ := s.Git.NotesShow(entry.CommitHash)
		note, ok := parseMainlineCommitNote(raw)
		if !ok {
			continue
		}
		intentIDs := noteIntentIDs(note)
		if len(intentIDs) == 0 && len(note.Reverts) == 0 {
			continue
		}

		if mapped := commitMap[entry.CommitHash]; mapped != "" {
			switch {
			case mapped == entry.CommitHash:
				plan.Unresolved = append(plan.Unresolved, NotesMigrationProblem{
					OldCommit: entry.CommitHash,
					IntentIDs: intentIDs,
					Reason:    "commit map keeps the note on an unreachable commit",
				})
			case !s.commitExists(mapped):
				plan.Unresolved = append(plan.Unresolved, NotesMigrationProblem{
					OldCommit: entry.CommitHash,
					IntentIDs: intentIDs,
					Reason:    "mapped commit is not present in this clone",
				})
			case !reachable[mapped]:
				plan.Unresolved = append(plan.Unresolved, NotesMigrationProblem{
					OldCommit: entry.CommitHash,
					IntentIDs: intentIDs,
					Reason:    "mapped commit is not reachable from current main",
				})
			default:
				plan.SafeMigrations = append(plan.SafeMigrations, NotesMigration{
					OldCommit:  entry.CommitHash,
					NewCommit:  mapped,
					Strategy:   "commit_map",
					Confidence: "exact",
					IntentIDs:  intentIDs,
					Reason:     "commit-map maps old note key to current main",
				})
			}
			continue
		}

		if !opts.Infer {
			plan.Unresolved = append(plan.Unresolved, NotesMigrationProblem{
				OldCommit: entry.CommitHash,
				IntentIDs: intentIDs,
				Reason:    "no commit-map entry and inference disabled",
			})
			continue
		}
		if !s.commitExists(entry.CommitHash) {
			plan.Unresolved = append(plan.Unresolved, NotesMigrationProblem{
				OldCommit: entry.CommitHash,
				IntentIDs: intentIDs,
				Reason:    "old note commit object is missing",
			})
			continue
		}

		oldTree, _ := s.Git.CommitTreeHash(entry.CommitHash)
		if oldTree != "" {
			candidates := ensureTreeIndex()[oldTree]
			candidates = withoutCommit(candidates, entry.CommitHash)
			switch len(candidates) {
			case 1:
				plan.SafeMigrations = append(plan.SafeMigrations, NotesMigration{
					OldCommit:  entry.CommitHash,
					NewCommit:  candidates[0],
					Strategy:   "tree_hash_unique",
					Confidence: "high",
					IntentIDs:  intentIDs,
					Reason:     "old note commit tree uniquely matches current main",
				})
				continue
			default:
				if len(candidates) > 1 {
					plan.ReviewRequired = append(plan.ReviewRequired, NotesMigrationReview{
						OldCommit:  entry.CommitHash,
						Candidates: candidates,
						Strategy:   "tree_hash",
						IntentIDs:  intentIDs,
						Reason:     "old note commit tree matches multiple current main commits",
					})
					continue
				}
			}
		}

		oldPatch, _ := s.Git.PatchID(entry.CommitHash)
		if oldPatch != "" {
			candidates := ensurePatchIndex()[oldPatch]
			candidates = withoutCommit(candidates, entry.CommitHash)
			switch len(candidates) {
			case 1:
				plan.SafeMigrations = append(plan.SafeMigrations, NotesMigration{
					OldCommit:  entry.CommitHash,
					NewCommit:  candidates[0],
					Strategy:   "patch_id_unique",
					Confidence: "high",
					IntentIDs:  intentIDs,
					Reason:     "old note commit patch-id uniquely matches current main",
				})
				continue
			default:
				if len(candidates) > 1 {
					plan.ReviewRequired = append(plan.ReviewRequired, NotesMigrationReview{
						OldCommit:  entry.CommitHash,
						Candidates: candidates,
						Strategy:   "patch_id",
						IntentIDs:  intentIDs,
						Reason:     "old note commit patch-id matches multiple current main commits",
					})
					continue
				}
			}
		}

		plan.Unresolved = append(plan.Unresolved, NotesMigrationProblem{
			OldCommit: entry.CommitHash,
			IntentIDs: intentIDs,
			Reason:    "no safe commit-map, tree-hash, or patch-id match found",
		})
	}

	sortNotesMigrationPlan(plan)
	return plan, nil
}

func (s *Service) remoteNotesRef(remote string) string {
	out, err := s.Git.Run("ls-remote", remote, gitops.NotesRef)
	if err != nil {
		return ""
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func notesMigrationMode(opts NotesMigrationOptions) string {
	var modes []string
	if opts.CommitMapPath != "" {
		modes = append(modes, "commit_map")
	}
	if opts.Infer {
		modes = append(modes, "infer")
	}
	if len(modes) == 0 {
		return "diagnose"
	}
	return strings.Join(modes, "+")
}

func readCommitMap(path string) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(path) == "" {
		return out, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		out[fields[0]] = fields[1]
	}
	return out, scanner.Err()
}

func parseMainlineCommitNote(raw string) (domain.CommitNote, bool) {
	var note domain.CommitNote
	if strings.TrimSpace(raw) == "" {
		return note, false
	}
	if err := json.Unmarshal([]byte(raw), &note); err != nil {
		return note, false
	}
	if note.Kind != "mainline.commit_note" {
		return note, false
	}
	return note, true
}

func noteIntentIDs(note domain.CommitNote) []string {
	seen := map[string]bool{}
	var ids []string
	for _, ref := range note.Intents {
		if ref.IntentID == "" || seen[ref.IntentID] {
			continue
		}
		seen[ref.IntentID] = true
		ids = append(ids, ref.IntentID)
	}
	sort.Strings(ids)
	return ids
}

func withoutCommit(commits []string, skip string) []string {
	out := make([]string, 0, len(commits))
	for _, commit := range commits {
		if commit == "" || commit == skip {
			continue
		}
		out = append(out, commit)
	}
	return out
}

func sortNotesMigrationPlan(plan *NotesMigrationPlan) {
	sort.Slice(plan.SafeMigrations, func(i, j int) bool {
		return plan.SafeMigrations[i].OldCommit < plan.SafeMigrations[j].OldCommit
	})
	sort.Slice(plan.ReviewRequired, func(i, j int) bool {
		return plan.ReviewRequired[i].OldCommit < plan.ReviewRequired[j].OldCommit
	})
	sort.Slice(plan.Unresolved, func(i, j int) bool {
		return plan.Unresolved[i].OldCommit < plan.Unresolved[j].OldCommit
	})
}
