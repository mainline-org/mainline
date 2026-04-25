package engine

import (
	"encoding/json"
	"fmt"

	"mainline/internal/core"
	"mainline/internal/domain"
)

// -----------------------------------------------------------
// Sync
// -----------------------------------------------------------

type SyncResult struct {
	Fetched       bool   `json:"fetched"`
	ViewRebuilt   bool   `json:"view_rebuilt"`
	IntentsInView int    `json:"intents_in_view"`
	ProposedCount int    `json:"proposed_count"`
	MainHead      string `json:"main_head"`
}

func (s *Service) Sync() (*SyncResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}

	fetched := false
	if s.Git.HasRemote("origin") {
		// Fetch main branch
		s.Git.Fetch("origin", cfg.Mainline.MainBranch)
		// Fetch actor log refs
		refspec := fmt.Sprintf("refs/heads/%s/*:refs/remotes/origin/%s/*",
			cfg.Mainline.ActorLogPrefix, cfg.Mainline.ActorLogPrefix)
		s.Git.Fetch("origin", refspec)
		// Fetch notes (rc3: notes are source of truth for merged status)
		s.Git.Fetch("origin", "refs/notes/mainline/*:refs/notes/mainline/*")
		fetched = true
	}

	// Rebuild view
	view, err := s.rebuildView(cfg)
	if err != nil {
		return nil, fmt.Errorf("rebuild view: %w", err)
	}

	// Rebuild proposed index
	idx := s.rebuildProposedIndex(view)
	s.Store.WriteProposedIndex(idx)

	return &SyncResult{
		Fetched:       fetched,
		ViewRebuilt:   true,
		IntentsInView: len(view.Intents),
		ProposedCount: len(idx.Proposed),
		MainHead:      view.MainHead,
	}, nil
}

func (s *Service) rebuildView(cfg *domain.TeamConfig) (*domain.MainlineView, error) {
	head, _ := s.Git.HeadCommit()

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
				iv.StatusEvidence.MergedVia = "reconcile"
			}
		}
	}

	// Scan main branch notes for merge evidence (rc3: notes replace trailers)
	s.scanMainNotes(cfg, intentMap)

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

func (s *Service) scanMainNotes(cfg *domain.TeamConfig, intentMap map[string]*domain.IntentView) {
	// rc3: scan main branch commits for git notes (source of truth for merged)
	entries, err := s.Git.LogOneline(cfg.Mainline.MainBranch, cfg.Check.Lookback)
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

		via := note.Via
		if via == "" {
			via = "merge"
		}

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
