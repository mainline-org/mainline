package engine

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mainline-org/mainline/internal/core"
	"github.com/mainline-org/mainline/internal/domain"
)

// -----------------------------------------------------------
// Follow-up lifecycle engine
// -----------------------------------------------------------
//
// Follow-ups are explicit future-work items written through
// `mainline followup add`. Older seal-summary follow-up strings remain
// readable for audit/diagnostics, but they do not enter active queues.

var followupIDPattern = regexp.MustCompile(`^int_[0-9a-f]+#\d+$`)
var explicitFollowupIDPattern = regexp.MustCompile(`^followup_[0-9a-f]+$`)

// ParseFollowupID splits a follow-up ID into (intent_id, index). Returns
// an error if the format is invalid.
func ParseFollowupID(followupID string) (intentID string, index int, err error) {
	parts := strings.SplitN(followupID, "#", 2)
	if len(parts) != 2 || !followupIDPattern.MatchString(followupID) {
		return "", 0, fmt.Errorf("invalid follow-up ID %q: expected format int_<hex>#<index>", followupID)
	}
	var idx int
	if _, err := fmt.Sscanf(parts[1], "%d", &idx); err != nil {
		return "", 0, fmt.Errorf("invalid follow-up index in %q: %w", followupID, err)
	}
	return parts[0], idx, nil
}

func validFollowupID(followupID string) bool {
	return followupIDPattern.MatchString(followupID) || explicitFollowupIDPattern.MatchString(followupID)
}

// FollowupID builds a deterministic follow-up ID from intent ID and array index.
func FollowupID(intentID string, index int) string {
	return fmt.Sprintf("%s#%d", intentID, index)
}

// materializeFollowups converts explicit follow-up signals into active
// Followup view-models with status.
func materializeFollowups(view *domain.MainlineView, fileFilter string) []domain.Followup {
	if view == nil {
		return nil
	}

	resolutions := view.FollowupResolutions
	if resolutions == nil {
		resolutions = map[string][]domain.FollowupResolution{}
	}

	var followups []domain.Followup
	for _, f := range view.Followups {
		followup := f
		followup.Status = "open"
		if rr, ok := resolutions[followup.ID]; ok && len(rr) > 0 {
			followup.Status = "resolved"
			followup.ResolvedBy = rr
		}
		if riskExpiredStatuses[statusOfIntent(view, followup.SourceIntent)] {
			followup.Status = "expired"
		}
		if fileFilter != "" && !followupMatchesPath(view, followup, fileFilter) {
			continue
		}
		followups = append(followups, followup)
	}

	sortFollowups(followups)
	return followups
}

func materializeLegacyFollowups(view *domain.MainlineView, fileFilter string) []domain.Followup {
	if view == nil {
		return nil
	}

	resolutions := view.FollowupResolutions
	if resolutions == nil {
		resolutions = map[string][]domain.FollowupResolution{}
	}

	var followups []domain.Followup
	for _, iv := range view.Intents {
		if iv.Summary == nil || len(iv.Summary.Followups) == 0 {
			continue
		}

		if fileFilter != "" && iv.Fingerprint != nil {
			if !touchesPath(iv.Fingerprint.FilesTouched, fileFilter) {
				continue
			}
		}

		for i, text := range iv.Summary.Followups {
			fid := FollowupID(iv.IntentID, i)

			status := "open"
			var resolvedBy []domain.FollowupResolution
			if rr, ok := resolutions[fid]; ok && len(rr) > 0 {
				status = "resolved"
				resolvedBy = rr
			}
			if riskExpiredStatuses[iv.Status] {
				status = "expired"
			}

			followups = append(followups, domain.Followup{
				ID:           fid,
				Text:         text,
				Status:       status,
				SourceIntent: iv.IntentID,
				OpenedAt:     iv.SealedAt,
				Source:       "seal_summary",
				ResolvedBy:   resolvedBy,
			})
		}
	}

	sortFollowups(followups)
	return followups
}

func materializeAllFollowups(view *domain.MainlineView, fileFilter string) []domain.Followup {
	followups := materializeFollowups(view, fileFilter)
	followups = append(followups, materializeLegacyFollowups(view, fileFilter)...)
	return followups
}

func sortFollowups(followups []domain.Followup) {
	sort.Slice(followups, func(i, j int) bool {
		oi, oj := statusOrder(followups[i].Status), statusOrder(followups[j].Status)
		if oi != oj {
			return oi < oj
		}
		return followups[i].OpenedAt > followups[j].OpenedAt
	})
}

// materializeOpenFollowups is the seal-prepare variant: only open
// follow-ups on files in the given list.
func materializeOpenFollowups(view *domain.MainlineView, files []string) []domain.Followup {
	all := materializeFollowups(view, "")
	var open []domain.Followup
	for _, f := range all {
		if f.Status != "open" {
			continue
		}
		if len(f.Files) > 0 && filesOverlap(f.Files, files) {
			open = append(open, f)
			continue
		}
		for _, iv := range view.Intents {
			if iv.IntentID != f.SourceIntent || iv.Fingerprint == nil {
				continue
			}
			if filesOverlap(iv.Fingerprint.FilesTouched, files) {
				open = append(open, f)
				break
			}
		}
	}
	return open
}

// ListFollowups is the Service entry point for `mainline followups`.
func (s *Service) ListFollowups(fileFilter string, includeAll bool) ([]domain.Followup, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	view, err := s.Store.ReadMainlineView()
	if err != nil || view == nil {
		return nil, fmt.Errorf("no mainline view available; run 'mainline sync' first")
	}

	followups := materializeFollowups(view, fileFilter)
	if !includeAll {
		var open []domain.Followup
		for _, f := range followups {
			if f.Status == "open" {
				open = append(open, f)
			}
		}
		followups = open
	}
	if followups == nil {
		followups = []domain.Followup{}
	}

	return followups, nil
}

// ResolveFollowup manually resolves a follow-up via CLI. Writes a
// FollowupResolvedEvent to the actor log and pushes.
func (s *Service) ResolveFollowup(followupID string, byIntent string, rationale string) error {
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
	if validFollowupID(followupID) {
		for _, f := range materializeAllFollowups(view, "") {
			if f.ID == followupID {
				found = true
				break
			}
		}
	}
	if !found {
		if intentID, index, err := ParseFollowupID(followupID); err == nil {
			for _, iv := range view.Intents {
				if iv.IntentID == intentID && iv.Summary != nil && index < len(iv.Summary.Followups) {
					found = true
					break
				}
			}
		}
	}
	if !found {
		return domain.NewRecoverableError(
			domain.ErrInvalidInput,
			fmt.Sprintf("follow-up %q not found", followupID),
			"run `mainline followups --all` to see available follow-ups",
		)
	}
	for _, f := range materializeAllFollowups(view, "") {
		if f.ID != followupID {
			continue
		}
		if f.Status != "open" {
			return domain.NewRecoverableError(
				domain.ErrInvalidInput,
				fmt.Sprintf("follow-up %q is already %s", followupID, f.Status),
				"run `mainline followups --all` to see resolved or expired explicit follow-ups",
			)
		}
		break
	}

	if rr, ok := view.FollowupResolutions[followupID]; ok && len(rr) > 0 {
		return domain.NewRecoverableError(
			domain.ErrInvalidInput,
			fmt.Sprintf("follow-up %q is already resolved", followupID),
			"run `mainline followups --all` to see resolved follow-ups",
		)
	}

	event := domain.FollowupResolvedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       core.GenerateEventID(),
			SchemaVersion: 1,
			EventType:     domain.EventFollowupResolved,
			ActorID:       identity.ActorID,
			ActorName:     s.actorDisplayName(identity),
			Timestamp:     core.Now(),
		},
		FollowupID:       followupID,
		ResolvedByIntent: byIntent,
		Rationale:        rationale,
	}

	if err := s.Store.AppendActorLogEvent(identity.ActorID, cfg.Mainline.ActorLogPrefix, event); err != nil {
		return fmt.Errorf("write followup_resolved event: %w", err)
	}

	if s.Git.HasRemote(s.remoteName()) {
		ref := s.Store.ActorLogRef(identity.ActorID, cfg.Mainline.ActorLogPrefix)
		refspec := fmt.Sprintf("%s:%s", ref, ref)
		_ = s.Git.Push(s.remoteName(), refspec)
	}

	return nil
}

// filterOpenFollowups returns only follow-ups that are still open (not
// resolved and not from an expired source intent).
func filterOpenFollowups(intentID string, followups []string, resolutions map[string][]domain.FollowupResolution, sourceStatus domain.IntentStatus) []string {
	if riskExpiredStatuses[sourceStatus] {
		return nil
	}
	if len(resolutions) == 0 {
		return followups
	}
	var open []string
	for i, text := range followups {
		fid := FollowupID(intentID, i)
		if rr, ok := resolutions[fid]; ok && len(rr) > 0 {
			continue
		}
		open = append(open, text)
	}
	return open
}

func followupMatchesPath(view *domain.MainlineView, f domain.Followup, prefix string) bool {
	if touchesPath(f.Files, prefix) {
		return true
	}
	for _, iv := range view.Intents {
		if iv.IntentID == f.SourceIntent && iv.Fingerprint != nil {
			return touchesPath(iv.Fingerprint.FilesTouched, prefix)
		}
	}
	return false
}

func statusOfIntent(view *domain.MainlineView, intentID string) domain.IntentStatus {
	if view == nil || intentID == "" {
		return ""
	}
	for _, iv := range view.Intents {
		if iv.IntentID == intentID {
			return iv.Status
		}
	}
	return ""
}
