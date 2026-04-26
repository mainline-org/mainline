package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"mainline/internal/core"
	"mainline/internal/domain"
)

// -----------------------------------------------------------
// Sync
// -----------------------------------------------------------

type SyncResult struct {
	Fetched       bool                  `json:"fetched"`
	ViewRebuilt   bool                  `json:"view_rebuilt"`
	IntentsInView int                   `json:"intents_in_view"`
	ProposedCount int                   `json:"proposed_count"`
	MainHead      string                `json:"main_head"`
	NewSealedSeen int                   `json:"new_sealed_seen,omitempty"`
	NewConflicts  []domain.ConflictPair `json:"new_conflicts,omitempty"`
	// AutoPinned lists the (intent, commit, strategy) triples
	// produced by the v0.2 auto-pin step. Empty when nothing matched
	// or AutoPinAfterSync is disabled.
	AutoPinned []PinnedCommit `json:"auto_pinned,omitempty"`
}

func (s *Service) Sync() (*SyncResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}

	// Snapshot the pre-sync view's intent ids so we can compute the
	// rc5 conflict-detection delta below: `new` = post-sync intents
	// not present in the pre-sync view. Drives both the LastSync
	// delta counter and the auto-check sees-only-new-stuff filter.
	priorIDs := make(map[string]bool)
	if prior, _ := s.Store.ReadMainlineView(); prior != nil {
		for _, iv := range prior.Intents {
			priorIDs[iv.IntentID] = true
		}
	}

	fetched := false
	if s.Git.HasRemote("origin") {
		// One fetch, three refspecs: main branch + every actor log +
		// the notes ref (rc3: notes are the source of truth for merged
		// status). A single `git fetch` shares one ssh handshake with
		// the remote — three separate fetches cost three round-trips
		// (~3s each on github), which dominated sync wall time.
		actorRefspec := fmt.Sprintf("refs/heads/%s/*:refs/remotes/origin/%s/*",
			cfg.Mainline.ActorLogPrefix, cfg.Mainline.ActorLogPrefix)
		s.Git.Fetch("origin",
			cfg.Mainline.MainBranch,
			actorRefspec,
			"refs/notes/mainline/*:refs/notes/mainline/*",
		)
		fetched = true
	}

	// Rebuild view
	view, err := s.rebuildView(cfg)
	if err != nil {
		return nil, fmt.Errorf("rebuild view: %w", err)
	}

	// v0.2: auto-pin runs the strategy cascade in-line so users
	// never need to invoke `mainline pin` separately. We do this
	// BEFORE the phase1 auto-check because pinning flips intents
	// from proposed to merged, and the post-pin view is the truth
	// the rest of sync's outputs (delta, conflicts, proposed_count)
	// should reflect.
	var autoPinned []PinnedCommit
	if cfg.Sync.AutoPinAfterSync {
		if pinResult, err := s.Pin(); err == nil && pinResult != nil && pinResult.Pinned > 0 {
			autoPinned = pinResult.Links
			// Re-rebuild now that new pin notes have landed locally.
			view, _ = s.rebuildView(cfg)
		}
	}

	// Rebuild proposed index
	idx := s.rebuildProposedIndex(view)
	s.Store.WriteProposedIndex(idx)

	// rc5: compute the delta the LastSync record and auto-check both
	// need. New = appeared this sync; we do not warn about pairs we
	// already surfaced on a previous sync.
	deltaIDs := make(map[string]bool)
	newSealedSeen := 0
	for _, iv := range view.Intents {
		if !priorIDs[iv.IntentID] {
			deltaIDs[iv.IntentID] = true
			if iv.Status == domain.StatusProposed {
				newSealedSeen++
			}
		}
	}

	result := &SyncResult{
		Fetched:       fetched,
		ViewRebuilt:   true,
		IntentsInView: len(view.Intents),
		ProposedCount: len(idx.Proposed),
		MainHead:      view.MainHead,
		NewSealedSeen: newSealedSeen,
		AutoPinned:    autoPinned,
	}

	if cfg.Sync.AutoCheckAfterSync {
		// One scoring pass produces the FULL active conflict set;
		// the cached snapshot lets `mainline log` answer "does this
		// intent currently have any phase1 warning?" without
		// re-running the scorer. The CLI surface (NewConflicts) is
		// then the delta — we only print pairs where the remote side
		// is brand-new this sync, so users do not re-see warnings
		// they already acknowledged on a previous sync.
		all := s.detectSyncConflicts(view, cfg.Check.Phase1Threshold, nil)
		s.Store.WritePhase1Warnings(&domain.Phase1WarningsCache{
			SchemaVersion: 1,
			UpdatedAt:     core.Now(),
			Pairs:         all,
		})
		if len(priorIDs) == 0 {
			// First sync: every intent is "new"; warning on every
			// existing pair would be noise.
			result.NewConflicts = nil
		} else {
			for _, p := range all {
				if deltaIDs[p.RemoteIntent] {
					result.NewConflicts = append(result.NewConflicts, p)
				}
			}
		}
	}

	// Persist last-sync record for the freshness-window CLI wrapper
	// and the staleness indicator in `mainline status`.
	identity, _ := s.getIdentity()
	actorID := ""
	if identity != nil {
		actorID = identity.ActorID
	}
	s.Store.WriteLastSync(&domain.LastSync{
		At:            core.Now(),
		ByActor:       actorID,
		MainHead:      view.MainHead,
		NewSealedSeen: newSealedSeen,
	})

	return result, nil
}

func (s *Service) rebuildView(cfg *domain.TeamConfig) (*domain.MainlineView, error) {
	mainRef := s.syncedMainRef(cfg.Mainline.MainBranch)
	head := s.Git.ReadRef(mainRef)
	if head == "" {
		head, _ = s.Git.HeadCommit()
	}

	view := &domain.MainlineView{
		SchemaVersion: 1,
		RebuiltAt:     core.Now(),
		MainBranch:    cfg.Mainline.MainBranch,
		MainHead:      head,
	}

	// Collect events from all actor logs
	events, err := s.collectAllEvents(cfg.Mainline.ActorLogPrefix)
	if err != nil {
		return nil, err
	}

	// Build intent views from events
	intentMap := make(map[string]*domain.IntentView)

	for _, raw := range events {
		var base domain.BaseEvent
		if err := json.Unmarshal(raw, &base); err != nil {
			continue
		}

		switch base.EventType {
		case domain.EventIntentSealed:
			var evt domain.IntentSealedEvent
			if err := json.Unmarshal(raw, &evt); err != nil {
				continue
			}
			iv := &domain.IntentView{
				IntentID:      evt.IntentID,
				SchemaVersion: 1,
				Status:        domain.StatusProposed,
				ActorID:       evt.ActorID,
				ActorName:     evt.ActorName,
				Thread:        evt.Thread,
				GitBranch:     evt.GitBranch,
				Goal:          evt.Goal,
				SealedAt:      evt.SealedAt,
				BaseCommit:    evt.BaseCommit,
				CodeCommit:    evt.CodeCommit,
				Summary:       &evt.Summary,
				Fingerprint:   &evt.Fingerprint,
				ViewRebuiltAt: core.Now(),
				StatusEvidence: domain.StatusEvidence{
					SealedEventID: evt.EventID,
				},
				Publication: "published",
			}
			intentMap[evt.IntentID] = iv

		case domain.EventIntentAbandoned:
			var evt domain.IntentAbandonedEvent
			if err := json.Unmarshal(raw, &evt); err != nil {
				continue
			}
			if iv, ok := intentMap[evt.IntentID]; ok {
				iv.Status = domain.StatusAbandoned
				iv.StatusEvidence.AbandonedEventID = evt.EventID
			}

		case domain.EventIntentSuperseded:
			var evt domain.IntentSupersededEvent
			if err := json.Unmarshal(raw, &evt); err != nil {
				continue
			}
			if iv, ok := intentMap[evt.IntentID]; ok {
				iv.Status = domain.StatusSuperseded
				iv.StatusEvidence.SupersededByIntent = evt.SupersededBy
			}

		case domain.EventIntentMergeAcknowledged:
			var evt domain.IntentMergeAcknowledgedEvent
			if err := json.Unmarshal(raw, &evt); err != nil {
				continue
			}
			if iv, ok := intentMap[evt.IntentID]; ok {
				iv.Status = domain.StatusMerged
				iv.StatusEvidence.MergedMainCommit = evt.MergeCommit
				iv.StatusEvidence.MergedVia = "pin"
			}

		case domain.EventCheckJudgment:
			var evt domain.CheckJudgmentEvent
			if err := json.Unmarshal(raw, &evt); err != nil {
				continue
			}
			iv, ok := intentMap[evt.CandidateIntent]
			if !ok {
				// Candidate not yet in the view (e.g. cross-repo, or
				// the sealed event hasn't been collected yet). Drop —
				// next sync will pick it up once the seal lands.
				continue
			}
			// last-write-wins: events stream in chronological order so
			// the final iteration is always the most recent judgment.
			iv.LastCheck = &domain.CheckSummary{
				EventID:          evt.EventID,
				AtTime:           evt.Timestamp,
				ByActor:          evt.ActorID,
				JudgmentCount:    len(evt.Judgments),
				HasConflict:      evt.Overall.HasConflict,
				HighestSeverity:  evt.Overall.HighestSeverity,
				NeedsHumanReview: evt.Overall.NeedsHumanReview,
				AgainstIntents:   extractAgainstIntents(evt.Judgments, evt.CandidateIntent),
			}
		}
	}

	// Scan main branch notes for merge evidence (rc3: notes replace trailers)
	s.scanMainNotes(cfg, mainRef, intentMap)

	for _, iv := range intentMap {
		view.Intents = append(view.Intents, *iv)
	}

	if err := s.Store.WriteMainlineView(view); err != nil {
		return nil, err
	}

	return view, nil
}

func (s *Service) collectAllEvents(prefix string) ([]json.RawMessage, error) {
	refPrefixes := []string{
		fmt.Sprintf("refs/heads/%s", prefix),
		fmt.Sprintf("refs/remotes/origin/%s", prefix),
	}

	seenRefs := make(map[string]bool)
	seenEvents := make(map[string]bool)
	var events []json.RawMessage

	for _, refPrefix := range refPrefixes {
		refs, err := s.Git.ListRefs(refPrefix)
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			if seenRefs[ref] {
				continue
			}
			seenRefs[ref] = true

			refEvents, err := s.Store.ReadActorLogEventsFromRef(ref)
			if err != nil {
				return nil, err
			}
			for _, event := range refEvents {
				key := string(event)
				if seenEvents[key] {
					continue
				}
				seenEvents[key] = true
				events = append(events, event)
			}
		}
	}

	return events, nil
}

func (s *Service) syncedMainRef(mainBranch string) string {
	remoteRef := "refs/remotes/origin/" + mainBranch
	if s.Git.ReadRef(remoteRef) != "" {
		return remoteRef
	}
	return mainBranch
}

func (s *Service) scanMainNotes(cfg *domain.TeamConfig, mainRef string, intentMap map[string]*domain.IntentView) {
	// rc3: scan main branch commits for git notes (source of truth for merged)
	entries, err := s.Git.LogOneline(mainRef, cfg.Check.Lookback)
	if err != nil {
		return
	}

	// LogOneline returns newest-first; replay chronologically so a later
	// revert commit can correctly overwrite the earlier merge state for the
	// same intent.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	for _, entry := range entries {
		noteContent, err := s.Git.NotesShow(entry.Hash)
		if err != nil || noteContent == "" {
			continue
		}

		var note domain.CommitNote
		if err := json.Unmarshal([]byte(noteContent), &note); err != nil {
			continue
		}
		if note.Kind != "mainline.commit_note" {
			continue
		}

		via := normaliseVia(note.Via)

		for _, ref := range note.Intents {
			if iv, exists := intentMap[ref.IntentID]; exists {
				iv.Status = domain.StatusMerged
				iv.StatusEvidence.MergedMainCommit = entry.Hash
				iv.StatusEvidence.MergedVia = via
			} else {
				intentMap[ref.IntentID] = &domain.IntentView{
					IntentID:      ref.IntentID,
					SchemaVersion: 1,
					Status:        domain.StatusMerged,
					ViewRebuiltAt: core.Now(),
					StatusEvidence: domain.StatusEvidence{
						MergedMainCommit: entry.Hash,
						MergedVia:        via,
					},
				}
			}
		}

		// Handle reverts
		for _, revertedID := range note.Reverts {
			if iv, exists := intentMap[revertedID]; exists {
				iv.Status = domain.StatusReverted
				iv.StatusEvidence.RevertedMainCommit = entry.Hash
			}
		}
	}
}

// extractAgainstIntents pulls the unique mainline intent ids out of a
// judgment list. Each ConflictJudgment carries a TaskID of the form
// "task_<candidate>_<mainline>" and (optionally) Evidence with explicit
// MainlineIntent fields. We try the evidence first because it is
// self-describing; we fall back to parsing the task id when evidence is
// empty (some judgments legitimately omit evidence on no_conflict).
func extractAgainstIntents(judgments []domain.ConflictJudgment, candidate string) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(id string) {
		if id == "" || id == candidate || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, j := range judgments {
		evidenceFound := false
		for _, ev := range j.Evidence {
			if ev.MainlineIntent != "" {
				add(ev.MainlineIntent)
				evidenceFound = true
			}
		}
		if !evidenceFound && j.TaskID != "" {
			// Format: task_<candidate>_<mainline> where each id is
			// "int_<8 hex>". Find the last "int_" in the suffix.
			if idx := strings.LastIndex(j.TaskID, "_int_"); idx >= 0 {
				add("int_" + j.TaskID[idx+len("_int_"):])
			}
		}
	}
	return out
}

// normaliseVia collapses the on-disk via spelling into the two values
// the view layer cares about: "merge" or "pin". The operation is
// called Pin from rc4 Patch 7 onwards; on-disk values written by
// future code use "pin_auto" / "pin_explicit". Everything older
// (rc3-era "reconcile" / "manual" / rc4-pre-Patch7 "reconcile_auto" /
// "reconcile_manual" / "link_auto" / "link_explicit") still maps to
// "pin" so existing notes keep rendering correctly.
func normaliseVia(raw string) string {
	switch raw {
	case "", "merge":
		return "merge"
	case "pin_auto", "pin_explicit",
		"link_auto", "link_explicit",
		"reconcile", "reconcile_auto", "reconcile_manual", "manual":
		return "pin"
	default:
		return raw
	}
}

func (s *Service) rebuildProposedIndex(view *domain.MainlineView) *domain.ProposedIndex {
	idx := &domain.ProposedIndex{
		SchemaVersion: 1,
		RebuiltAt:     core.Now(),
	}
	for _, iv := range view.Intents {
		if iv.Status == domain.StatusProposed {
			proposed := iv
			idx.Proposed = append(idx.Proposed, proposed)
		}
	}
	return idx
}
