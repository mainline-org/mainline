package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mainline-org/mainline/internal/domain"
)

const (
	prDescriptionStartMarker = "<!-- mainline:pr-description:start -->"
	prDescriptionEndMarker   = "<!-- mainline:pr-description:end -->"
	prCommentMarker          = "<!-- mainline:pr-comment:v1 -->"
)

func (s *Service) PRDescription(intentID string) (string, error) {
	if err := s.requireInit(); err != nil {
		return "", err
	}

	view, _ := s.Store.ReadMainlineView()
	if view != nil {
		for _, iv := range view.Intents {
			if iv.IntentID == intentID && iv.Summary != nil {
				return wrapPRDescription(formatPRIntent(iv, 2)), nil
			}
		}
	}

	draft, _ := s.Store.ReadDraft(intentID)
	if draft != nil {
		body := fmt.Sprintf("## Mainline Intent\n\n**Intent:** `%s`\n**Goal:** %s\n",
			draft.IntentID, draft.Goal)
		return wrapPRDescription(body), nil
	}

	return "", domain.NewError(domain.ErrNoActiveIntent, fmt.Sprintf("intent %s not found", intentID))
}

func (s *Service) PRComment(base, head, branch string) (string, error) {
	if err := s.requireInit(); err != nil {
		return "", err
	}

	view, _ := s.Store.ReadMainlineView()
	if view == nil {
		return formatMissingPRComment("Mainline view is not available. Run `mainline sync` and retry."), nil
	}

	matches := s.matchPRIntents(view, base, head, branch)
	if len(matches) == 0 {
		return formatMissingPRComment("No sealed Mainline intent was found for this PR range."), nil
	}

	var sb strings.Builder
	sb.WriteString(prCommentMarker + "\n\n")
	if len(matches) == 1 {
		sb.WriteString(formatPRIntent(matches[0], 2))
		return strings.TrimRight(sb.String(), "\n") + "\n", nil
	}

	sb.WriteString("## Mainline Intents\n\n")
	for i, iv := range matches {
		if i > 0 {
			sb.WriteString("\n---\n\n")
		}
		sb.WriteString(formatPRIntent(iv, 3))
	}
	return strings.TrimRight(sb.String(), "\n") + "\n", nil
}

func (s *Service) matchPRIntents(view *domain.MainlineView, base, head, branch string) []domain.IntentView {
	commits := map[string]bool{}
	if strings.TrimSpace(base) != "" && strings.TrimSpace(head) != "" {
		if out, err := s.Git.Run("rev-list", strings.TrimSpace(base)+".."+strings.TrimSpace(head)); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				commit := strings.TrimSpace(line)
				if commit != "" {
					commits[commit] = true
				}
			}
		}
	}

	var matches []domain.IntentView
	seen := map[string]bool{}
	for _, iv := range view.Intents {
		if iv.Summary == nil || !isPRVisibleIntentStatus(iv.Status) {
			continue
		}
		if iv.CodeCommit != "" && commits[iv.CodeCommit] {
			matches = append(matches, iv)
			seen[iv.IntentID] = true
		}
	}

	if len(matches) == 0 && strings.TrimSpace(branch) != "" {
		for _, iv := range view.Intents {
			if seen[iv.IntentID] || iv.Summary == nil || !isPRVisibleIntentStatus(iv.Status) {
				continue
			}
			if iv.GitBranch == branch {
				matches = append(matches, iv)
				seen[iv.IntentID] = true
			}
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		left, leftOK := parsePRTime(matches[i].SealedAt)
		right, rightOK := parsePRTime(matches[j].SealedAt)
		if leftOK && rightOK {
			return left.Before(right)
		}
		return matches[i].IntentID < matches[j].IntentID
	})
	return matches
}

func isPRVisibleIntentStatus(status domain.IntentStatus) bool {
	switch status {
	case domain.StatusSealedLocal, domain.StatusProposed, domain.StatusMerged:
		return true
	default:
		return false
	}
}

func formatPRIntent(iv domain.IntentView, level int) string {
	summary := iv.Summary
	if summary == nil {
		return ""
	}

	heading := strings.Repeat("#", level)
	subheading := strings.Repeat("#", level+1)
	var sb strings.Builder
	sb.WriteString(heading + " Mainline Intent\n\n")
	sb.WriteString(fmt.Sprintf("**Intent:** `%s`\n", iv.IntentID))
	if iv.Status != "" {
		sb.WriteString(fmt.Sprintf("**Status:** `%s`\n", iv.Status))
	}
	sb.WriteString(fmt.Sprintf("**Title:** %s\n\n", summary.Title))

	sb.WriteString(subheading + " What changed\n\n")
	sb.WriteString(summary.What + "\n\n")

	sb.WriteString(subheading + " Why\n\n")
	sb.WriteString(summary.Why + "\n\n")

	if len(summary.Decisions) > 0 {
		sb.WriteString(subheading + " Decisions\n\n")
		for _, d := range summary.Decisions {
			sb.WriteString(fmt.Sprintf("- **%s:** %s", d.Point, d.Chose))
			if d.Rationale != "" {
				sb.WriteString(fmt.Sprintf(" (%s)", d.Rationale))
			}
			sb.WriteString("\n")
			for _, rej := range d.Rejected {
				sb.WriteString(fmt.Sprintf("  - Rejected: %s\n", rej))
			}
		}
		sb.WriteString("\n")
	}

	if len(summary.Risks) > 0 {
		sb.WriteString(subheading + " Risks\n\n")
		for _, r := range summary.Risks {
			sb.WriteString(fmt.Sprintf("- %s\n", r.Text()))
		}
		sb.WriteString("\n")
	}

	if len(summary.AntiPatterns) > 0 {
		sb.WriteString(subheading + " Anti-patterns\n\n")
		for _, ap := range summary.AntiPatterns {
			sev := ap.Severity
			if sev == "" {
				sev = "unspecified"
			}
			sb.WriteString(fmt.Sprintf("- **[%s]** %s", sev, ap.What))
			if ap.Why != "" {
				sb.WriteString(fmt.Sprintf(" - %s", ap.Why))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if len(summary.Followups) > 0 {
		sb.WriteString(subheading + " Follow-ups\n\n")
		for _, f := range summary.Followups {
			sb.WriteString(fmt.Sprintf("- %s\n", f.Text()))
		}
		sb.WriteString("\n")
	}

	if len(iv.References) > 0 {
		sb.WriteString(subheading + " References\n\n")
		for _, ref := range iv.References {
			label := ref.Label
			if label == "" {
				label = ref.Kind
			}
			if ref.URL != "" {
				sb.WriteString(fmt.Sprintf("- %s: [`%s`](%s)\n", label, ref.Ref, ref.URL))
			} else if ref.Ref != "" {
				sb.WriteString(fmt.Sprintf("- %s: `%s`\n", label, ref.Ref))
			}
		}
		sb.WriteString("\n")
	}

	if iv.Fingerprint != nil && len(iv.Fingerprint.Subsystems) > 0 {
		sb.WriteString(fmt.Sprintf("**Subsystems:** %s\n", strings.Join(iv.Fingerprint.Subsystems, ", ")))
	}

	return strings.TrimRight(sb.String(), "\n") + "\n"
}

func wrapPRDescription(body string) string {
	return prDescriptionStartMarker + "\n" +
		strings.TrimRight(body, "\n") + "\n" +
		prDescriptionEndMarker + "\n"
}

func formatMissingPRComment(message string) string {
	return prCommentMarker + "\n\n" +
		"## Mainline Intent\n\n" +
		message + "\n"
}

func parsePRTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, value)
	return t, err == nil
}
