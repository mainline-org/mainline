package engine

import (
	"fmt"
	"strings"

	"github.com/mainline-org/mainline/internal/core"
	"github.com/mainline-org/mainline/internal/domain"
)

// -----------------------------------------------------------
// Risk lifecycle engine
// -----------------------------------------------------------
//
// Risks are soft warnings written as []string on IntentSummary.
// Before v0.4 they were write-once: no IDs, no resolution, no
// expiry. This file adds:
//
//   - Deterministic risk IDs: "{intent_id}#{array_index}"
//   - Materialised Risk view-model with status (open/resolved/expired)
//   - Resolution via SealResult.ResolvesRisks (atomic with seal)
//   - Manual resolution via `mainline risks resolve`
//   - Expiry when source intent is superseded/abandoned/reverted
//
// Risk IDs are stable because sealed intents are immutable —
// the risk array never changes after seal, so the index is fixed.

// ParseRiskID splits a risk ID into (intent_id, index). Returns an
// error if the format is invalid.
func ParseRiskID(riskID string) (intentID string, index int, err error) {
	return domain.ParseRiskID(riskID)
}

// RiskID builds a deterministic risk ID from intent ID and array index.
func RiskID(intentID string, index int) string {
	return domain.RiskID(intentID, index)
}

var riskExpiredStatuses = map[domain.IntentStatus]bool{
	domain.StatusSuperseded: true,
	domain.StatusAbandoned:  true,
	domain.StatusReverted:   true,
}

// materializeRisks converts the view into Risk view-models with status.
// fileFilter, when non-empty, restricts to risks from intents that
// touched a file matching the prefix.
func materializeRisks(view *domain.MainlineView, fileFilter string) []domain.Risk {
	return domain.MaterializeRisks(view, fileFilter)
}

// materializeOpenRisks is the seal-prepare variant: only open risks
// on files in the given list.
func materializeOpenRisks(view *domain.MainlineView, files []string) []domain.Risk {
	return domain.MaterializeOpenRisks(view, files)
}

func filesOverlap(a, b []string) bool {
	return domain.RiskFilesOverlap(a, b)
}

func touchesPath(files []string, prefix string) bool {
	for _, f := range files {
		if strings.HasPrefix(f, prefix) {
			return true
		}
	}
	return false
}

func statusOrder(s string) int {
	return domain.RiskStatusOrder(s)
}

// ListRisks is the Service entry point for `mainline risks`.
func (s *Service) ListRisks(fileFilter string, includeAll bool) ([]domain.Risk, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	view, err := s.Store.ReadMainlineView()
	if err != nil || view == nil {
		return nil, fmt.Errorf("no mainline view available; run 'mainline sync' first")
	}

	risks := materializeRisks(view, fileFilter)
	if !includeAll {
		var open []domain.Risk
		for _, r := range risks {
			if r.Status == "open" {
				open = append(open, r)
			}
		}
		risks = open
	}

	return risks, nil
}

// ResolveRisk manually resolves a risk via CLI. Writes a
// RiskResolvedEvent to the actor log and pushes.
func (s *Service) ResolveRisk(riskID string, byIntent string, rationale string) error {
	identity, err := s.requireIdentity()
	if err != nil {
		return err
	}
	cfg, err := s.getTeamConfig()
	if err != nil {
		return err
	}

	// Validate risk ID format
	intentID, index, err := ParseRiskID(riskID)
	if err != nil {
		return err
	}

	// Validate the source intent exists and has that risk index
	view, _ := s.Store.ReadMainlineView()
	if view == nil {
		return fmt.Errorf("no mainline view available; run 'mainline sync' first")
	}

	var found bool
	for _, iv := range view.Intents {
		if iv.IntentID == intentID && iv.Summary != nil && index < len(iv.Summary.Risks) {
			found = true
			break
		}
	}
	if !found {
		return domain.NewRecoverableError(
			domain.ErrInvalidInput,
			fmt.Sprintf("risk %q not found: intent %s has no risk at index %d", riskID, intentID, index),
			"run `mainline risks` to see available risks",
		)
	}

	// Check it's not already resolved
	if rr, ok := view.RiskResolutions[riskID]; ok && len(rr) > 0 {
		return domain.NewRecoverableError(
			domain.ErrInvalidInput,
			fmt.Sprintf("risk %q is already resolved", riskID),
			"run `mainline risks --all` to see resolved risks",
		)
	}

	event := domain.RiskResolvedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       core.GenerateEventID(),
			SchemaVersion: 1,
			EventType:     domain.EventRiskResolved,
			ActorID:       identity.ActorID,
			ActorName:     s.actorDisplayName(identity),
			Timestamp:     core.Now(),
		},
		RiskID:           riskID,
		ResolvedByIntent: byIntent,
		Rationale:        rationale,
	}

	if err := s.Store.AppendActorLogEvent(identity.ActorID, cfg.Mainline.ActorLogPrefix, event); err != nil {
		return fmt.Errorf("write risk_resolved event: %w", err)
	}

	// Try to push
	if s.Git.HasRemote(s.remoteName()) {
		ref := s.Store.ActorLogRef(identity.ActorID, cfg.Mainline.ActorLogPrefix)
		refspec := fmt.Sprintf("%s:%s", ref, ref)
		_ = s.Git.Push(s.remoteName(), refspec)
	}

	return nil
}
