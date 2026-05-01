package engine

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
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

// riskIDPattern validates risk ID format: "int_<hex>#<digit>"
var riskIDPattern = regexp.MustCompile(`^int_[0-9a-f]+#\d+$`)

// ParseRiskID splits a risk ID into (intent_id, index). Returns an
// error if the format is invalid.
func ParseRiskID(riskID string) (intentID string, index int, err error) {
	parts := strings.SplitN(riskID, "#", 2)
	if len(parts) != 2 || !riskIDPattern.MatchString(riskID) {
		return "", 0, fmt.Errorf("invalid risk ID %q: expected format int_<hex>#<index>", riskID)
	}
	var idx int
	if _, err := fmt.Sscanf(parts[1], "%d", &idx); err != nil {
		return "", 0, fmt.Errorf("invalid risk index in %q: %w", riskID, err)
	}
	return parts[0], idx, nil
}

// RiskID builds a deterministic risk ID from intent ID and array index.
func RiskID(intentID string, index int) string {
	return fmt.Sprintf("%s#%d", intentID, index)
}

// riskExpiredStatuses are intent statuses where all risks are expired
// (the work was replaced, abandoned, or reverted — risks are moot).
var riskExpiredStatuses = map[domain.IntentStatus]bool{
	domain.StatusSuperseded: true,
	domain.StatusAbandoned:  true,
	domain.StatusReverted:   true,
}

// materializeRisks converts the view into Risk view-models with status.
// fileFilter, when non-empty, restricts to risks from intents that
// touched a file matching the prefix.
func materializeRisks(view *domain.MainlineView, fileFilter string) []domain.Risk {
	if view == nil {
		return nil
	}

	resolutions := view.RiskResolutions
	if resolutions == nil {
		resolutions = map[string][]domain.RiskResolution{}
	}

	var risks []domain.Risk
	for _, iv := range view.Intents {
		if iv.Summary == nil || len(iv.Summary.Risks) == 0 {
			continue
		}

		// File filter: skip intents that don't touch the requested path.
		if fileFilter != "" && iv.Fingerprint != nil {
			if !touchesPath(iv.Fingerprint.FilesTouched, fileFilter) {
				continue
			}
		}

		for i, text := range iv.Summary.Risks {
			rid := RiskID(iv.IntentID, i)

			status := "open"
			var resolvedBy []domain.RiskResolution

			// Check resolution events
			if rr, ok := resolutions[rid]; ok && len(rr) > 0 {
				status = "resolved"
				resolvedBy = rr
			}

			// Check source intent lifecycle → expired
			if riskExpiredStatuses[iv.Status] {
				status = "expired"
			}

			risks = append(risks, domain.Risk{
				ID:           rid,
				Text:         text,
				Status:       status,
				SourceIntent: iv.IntentID,
				OpenedAt:     iv.SealedAt,
				ResolvedBy:   resolvedBy,
			})
		}
	}

	// Sort: open first, then resolved, then expired; within each group by recency.
	sort.Slice(risks, func(i, j int) bool {
		oi, oj := statusOrder(risks[i].Status), statusOrder(risks[j].Status)
		if oi != oj {
			return oi < oj
		}
		return risks[i].OpenedAt > risks[j].OpenedAt // newer first
	})

	return risks
}

// materializeOpenRisks is the seal-prepare variant: only open risks
// on files in the given list.
func materializeOpenRisks(view *domain.MainlineView, files []string) []domain.Risk {
	all := materializeRisks(view, "")
	var open []domain.Risk
	for _, r := range all {
		if r.Status != "open" {
			continue
		}
		// Check if any of the intent's files overlap with the current files.
		for _, iv := range view.Intents {
			if iv.IntentID != r.SourceIntent || iv.Fingerprint == nil {
				continue
			}
			if filesOverlap(iv.Fingerprint.FilesTouched, files) {
				open = append(open, r)
				break
			}
		}
	}
	return open
}

func filesOverlap(a, b []string) bool {
	set := make(map[string]bool, len(a)*2)
	for _, f := range a {
		set[f] = true
		// Only add the immediate parent directory, not all ancestors.
		if d := filepath.Dir(f); d != "." && d != "/" {
			set[d+"/"] = true
		}
	}
	for _, f := range b {
		if set[f] {
			return true
		}
		if d := filepath.Dir(f); d != "." && d != "/" {
			if set[d+"/"] {
				return true
			}
		}
	}
	return false
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
	switch s {
	case "open":
		return 0
	case "resolved":
		return 1
	case "expired":
		return 2
	default:
		return 3
	}
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
