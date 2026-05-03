package domain

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// -----------------------------------------------------------
// Risk lifecycle view helpers
// -----------------------------------------------------------
//
// Risks are stored immutably as []string on IntentSummary. These helpers
// derive the effective lifecycle view used by CLI, context, seal prepare,
// and Hub so consumers don't accidentally treat raw historical prose as
// current state.

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

// RiskSourceExpired reports whether a source intent lifecycle makes all
// of its risks moot.
func RiskSourceExpired(status IntentStatus) bool {
	return status == StatusSuperseded ||
		status == StatusAbandoned ||
		status == StatusReverted
}

// MaterializeRisks converts a MainlineView into Risk view-models with
// lifecycle status. fileFilter, when non-empty, restricts to risks from
// intents that touched a file matching the prefix.
func MaterializeRisks(view *MainlineView, fileFilter string) []Risk {
	if view == nil {
		return nil
	}

	resolutions := view.RiskResolutions
	if resolutions == nil {
		resolutions = map[string][]RiskResolution{}
	}

	var risks []Risk
	for _, iv := range view.Intents {
		if iv.Summary == nil || len(iv.Summary.Risks) == 0 {
			continue
		}

		if fileFilter != "" && iv.Fingerprint != nil {
			if !riskTouchesPath(iv.Fingerprint.FilesTouched, fileFilter) {
				continue
			}
		}

		for i, text := range iv.Summary.Risks {
			rid := RiskID(iv.IntentID, i)

			status := "open"
			var resolvedBy []RiskResolution

			if rr, ok := resolutions[rid]; ok && len(rr) > 0 {
				status = "resolved"
				resolvedBy = rr
			}

			if RiskSourceExpired(iv.Status) {
				status = "expired"
			}

			risks = append(risks, Risk{
				ID:           rid,
				Text:         text,
				Status:       status,
				SourceIntent: iv.IntentID,
				OpenedAt:     iv.SealedAt,
				ResolvedBy:   resolvedBy,
			})
		}
	}

	sort.Slice(risks, func(i, j int) bool {
		oi, oj := RiskStatusOrder(risks[i].Status), RiskStatusOrder(risks[j].Status)
		if oi != oj {
			return oi < oj
		}
		return risks[i].OpenedAt > risks[j].OpenedAt
	})

	return risks
}

// MaterializeOpenRisks returns only open risks whose source intent
// touched files overlapping with the supplied file list.
func MaterializeOpenRisks(view *MainlineView, files []string) []Risk {
	if view == nil {
		return nil
	}
	all := MaterializeRisks(view, "")
	var open []Risk
	for _, r := range all {
		if r.Status != "open" {
			continue
		}
		for _, iv := range view.Intents {
			if iv.IntentID != r.SourceIntent || iv.Fingerprint == nil {
				continue
			}
			if RiskFilesOverlap(iv.Fingerprint.FilesTouched, files) {
				open = append(open, r)
				break
			}
		}
	}
	return open
}

// OpenRiskTexts filters a raw summary.risks slice down to the effective
// open texts for one source intent.
func OpenRiskTexts(intentID string, risks []string, resolutions map[string][]RiskResolution, sourceStatus IntentStatus) []string {
	if RiskSourceExpired(sourceStatus) {
		return nil
	}
	if len(resolutions) == 0 {
		return risks
	}
	var open []string
	for i, text := range risks {
		rid := RiskID(intentID, i)
		if rr, ok := resolutions[rid]; ok && len(rr) > 0 {
			continue
		}
		open = append(open, text)
	}
	return open
}

// RiskFilesOverlap reports exact-file or same-immediate-directory overlap.
func RiskFilesOverlap(a, b []string) bool {
	set := make(map[string]bool, len(a)*2)
	for _, f := range a {
		set[f] = true
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

// RiskStatusOrder sorts open before resolved before expired.
func RiskStatusOrder(s string) int {
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

func riskTouchesPath(files []string, prefix string) bool {
	for _, f := range files {
		if strings.HasPrefix(f, prefix) {
			return true
		}
	}
	return false
}
