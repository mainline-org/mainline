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
// Risks are explicit review warnings written through `mainline risks add`.
// Older seal-summary risk strings remain readable for audit/diagnostics,
// but they do not enter active queues.
// Before v0.4 they were write-once: no IDs, no resolution, no
// expiry. This file adds:
//
//   - Deterministic legacy risk IDs: "{intent_id}#{array_index}"
//   - Explicit risk IDs: "risk_<hex>"
//   - Materialised Risk view-model with status (open/resolved/expired)
//   - Resolution via SealResult.ResolvesRisks (atomic with seal)
//   - Manual resolution via `mainline risks resolve`
//   - Expiry when source intent is superseded/abandoned/reverted
//
// Legacy risk IDs are stable because sealed intents are immutable —
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

// materializeRisks converts explicit risk signals into active Risk
// view-models with status.
func materializeRisks(view *domain.MainlineView, fileFilter string) []domain.Risk {
	return domain.MaterializeRisks(view, fileFilter)
}

func materializeLegacyRisks(view *domain.MainlineView, fileFilter string) []domain.Risk {
	return domain.MaterializeLegacyRisks(view, fileFilter)
}

func materializeAllRisks(view *domain.MainlineView, fileFilter string) []domain.Risk {
	risks := materializeRisks(view, fileFilter)
	risks = append(risks, materializeLegacyRisks(view, fileFilter)...)
	return risks
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
	if risks == nil {
		risks = []domain.Risk{}
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

	view, _ := s.Store.ReadMainlineView()
	if view == nil {
		return fmt.Errorf("no mainline view available; run 'mainline sync' first")
	}

	var found bool
	if domain.ValidRiskID(riskID) {
		for _, r := range materializeAllRisks(view, "") {
			if r.ID == riskID {
				found = true
				break
			}
		}
	}
	if !found {
		if intentID, index, err := ParseRiskID(riskID); err == nil {
			for _, iv := range view.Intents {
				if iv.IntentID == intentID && iv.Summary != nil && index < len(iv.Summary.Risks) {
					found = true
					break
				}
			}
		}
	}
	if !found {
		return domain.NewRecoverableError(
			domain.ErrInvalidInput,
			fmt.Sprintf("risk %q not found", riskID),
			"run `mainline risks --all` to see available risks",
		)
	}

	for _, r := range materializeAllRisks(view, "") {
		if r.ID != riskID {
			continue
		}
		if r.Status != "open" {
			return domain.NewRecoverableError(
				domain.ErrInvalidInput,
				fmt.Sprintf("risk %q is already %s", riskID, r.Status),
				"run `mainline risks --all` to see resolved or expired explicit risks",
			)
		}
		break
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
