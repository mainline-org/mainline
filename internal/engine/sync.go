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
	Fetched        bool   `json:"fetched"`
	ViewRebuilt    bool   `json:"view_rebuilt"`
	IntentsInView  int    `json:"intents_in_view"`
	ProposedCount  int    `json:"proposed_count"`
	MainHead       string `json:"main_head"`
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
		refspec := fmt.Sprintf("refs/%s/*:refs/%s/*", cfg.Mainline.ActorLogPrefix, cfg.Mainline.ActorLogPrefix)
		s.Git.Fetch("origin", refspec)
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
				iv.StatusEvidence.MergedConfidence = "acknowledged"
			}
		}
	}

	// Also scan main branch trailers for merge evidence
	s.scanMainTrailers(cfg, intentMap)

	for _, iv := range intentMap {
		view.Intents = append(view.Intents, *iv)
	}

	if err := s.Store.WriteMainlineView(view); err != nil {
		return nil, err
	}

	return view, nil
}

func (s *Service) collectAllEvents(prefix string) ([]json.RawMessage, error) {
	// List all actor refs
	identity, _ := s.getIdentity()
	if identity == nil {
		return nil, nil
	}

	// Read events from local actor's log
	events, _ := s.Store.ReadActorLogEvents(identity.ActorID, prefix)
	return events, nil
}

func (s *Service) scanMainTrailers(cfg *domain.TeamConfig, intentMap map[string]*domain.IntentView) {
	// Scan recent main branch commits for Mainline-Intent trailers
	entries, err := s.Git.LogOneline(cfg.Mainline.MainBranch, cfg.Check.Lookback)
	if err != nil {
		return
	}

	for _, entry := range entries {
		trailers, err := s.Git.CommitTrailers(entry.Hash)
		if err != nil {
			continue
		}
		intentID, ok := trailers["Mainline-Intent"]
		if !ok {
			continue
		}
		intentID = strings.TrimSpace(intentID)
		if iv, exists := intentMap[intentID]; exists {
			iv.Status = domain.StatusMerged
			iv.StatusEvidence.MergedMainCommit = entry.Hash
			iv.StatusEvidence.MergedConfidence = "confirmed"
		} else {
			// Create a minimal view from trailer
			intentMap[intentID] = &domain.IntentView{
				IntentID:      intentID,
				SchemaVersion: 1,
				Status:        domain.StatusMerged,
				ViewRebuiltAt: core.Now(),
				StatusEvidence: domain.StatusEvidence{
					MergedMainCommit: entry.Hash,
					MergedConfidence: "confirmed",
				},
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
