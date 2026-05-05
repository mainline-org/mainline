package engine

import (
	"fmt"
	"strings"

	"github.com/mainline-org/mainline/internal/core"
	"github.com/mainline-org/mainline/internal/domain"
)

type AddConstraintInput struct {
	IntentID   string
	Files      []string
	What       string
	Why        string
	Severity   string
	Source     string
	SourceNote string
}

type AddRiskInput struct {
	IntentID  string
	Files     []string
	Statement domain.RiskStatement
	Source    string
}

type AddFollowupInput struct {
	IntentID  string
	Files     []string
	Statement domain.FollowupStatement
	Source    string
}

func (s *Service) AddConstraint(in AddConstraintInput) (*domain.Constraint, error) {
	identity, cfg, err := s.signalWriteContext()
	if err != nil {
		return nil, err
	}
	in.What = strings.TrimSpace(in.What)
	in.Why = strings.TrimSpace(in.Why)
	if in.What == "" {
		return nil, domain.NewRecoverableError(domain.ErrInvalidInput,
			"constraint text is required",
			`run: mainline guard add --file <path> "<constraint>" --why "<reason>"`,
		)
	}
	if in.Why == "" {
		return nil, domain.NewRecoverableError(domain.ErrInvalidInput,
			"constraint reason is required",
			"constraints must explain why future agents must stop before violating them",
		)
	}
	if in.Source == "" {
		in.Source = domain.SignalSourceCommand
	}
	files := cleanSignalFiles(in.Files)
	if len(files) == 0 && strings.TrimSpace(in.IntentID) == "" {
		return nil, domain.NewRecoverableError(domain.ErrInvalidInput,
			"constraint needs --file or --intent",
			"constraints are future behavior rules; scope them to files or a source intent",
		)
	}

	eventID := core.GenerateEventID()
	id := "guard_" + strings.TrimPrefix(eventID, "evt_")
	event := domain.ConstraintAddedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       eventID,
			SchemaVersion: 1,
			EventType:     domain.EventConstraintAdded,
			ActorID:       identity.ActorID,
			ActorName:     s.actorDisplayName(identity),
			Timestamp:     core.Now(),
		},
		ConstraintID: id,
		IntentID:     strings.TrimSpace(in.IntentID),
		Files:        files,
		What:         in.What,
		Why:          in.Why,
		Severity:     normalizedSeverity(in.Severity),
		Source:       in.Source,
		SourceNote:   strings.TrimSpace(in.SourceNote),
	}
	if err := s.appendSignalEvent(cfg, event); err != nil {
		return nil, err
	}
	return &domain.Constraint{
		ID:           id,
		What:         event.What,
		Why:          event.Why,
		Severity:     event.Severity,
		Files:        files,
		SourceIntent: event.IntentID,
		OpenedAt:     event.Timestamp,
		OpenedBy:     identity.ActorID,
		Source:       event.Source,
		SourceNote:   event.SourceNote,
	}, nil
}

func (s *Service) AddRisk(in AddRiskInput) (*domain.Risk, error) {
	identity, cfg, err := s.signalWriteContext()
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateRiskStatement(in.Statement); err != nil {
		return nil, domain.NewRecoverableError(domain.ErrInvalidInput,
			err.Error(),
			`use: mainline risks add --intent <id> "<failure mode>" --trigger "<when>" --mitigation "<how to reduce it>"`,
			"if this is only reviewer context, keep it in review_notes instead of creating a risk",
		)
	}
	intentID, err := s.resolveSignalIntent(in.IntentID)
	if err != nil {
		return nil, err
	}
	if in.Source == "" {
		in.Source = domain.SignalSourceCommand
	}
	files := cleanSignalFiles(in.Files)
	eventID := core.GenerateEventID()
	id := "risk_" + strings.TrimPrefix(eventID, "evt_")
	stmt := in.Statement
	event := domain.RiskAddedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       eventID,
			SchemaVersion: 1,
			EventType:     domain.EventRiskAdded,
			ActorID:       identity.ActorID,
			ActorName:     s.actorDisplayName(identity),
			Timestamp:     core.Now(),
		},
		RiskID:    id,
		IntentID:  intentID,
		Files:     files,
		Statement: stmt,
		Source:    in.Source,
	}
	if err := s.appendSignalEvent(cfg, event); err != nil {
		return nil, err
	}
	return &domain.Risk{
		ID:           id,
		Text:         stmt.Text(),
		Statement:    &stmt,
		Status:       "open",
		SourceIntent: intentID,
		Files:        files,
		OpenedBy:     identity.ActorID,
		OpenedAt:     event.Timestamp,
		Source:       event.Source,
	}, nil
}

func (s *Service) AddFollowup(in AddFollowupInput) (*domain.Followup, error) {
	identity, cfg, err := s.signalWriteContext()
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateFollowupStatement(in.Statement); err != nil {
		return nil, domain.NewRecoverableError(domain.ErrInvalidInput,
			err.Error(),
			`use: mainline followups add --intent <id> "<task>" --source explicit_defer --source-note "<who deferred it>"`,
			"agent-created maybe-later ideas belong in review_notes or nowhere",
		)
	}
	intentID, err := s.resolveSignalIntent(in.IntentID)
	if err != nil {
		return nil, err
	}
	if in.Source == "" {
		in.Source = domain.SignalSourceCommand
	}
	files := cleanSignalFiles(in.Files)
	eventID := core.GenerateEventID()
	id := "followup_" + strings.TrimPrefix(eventID, "evt_")
	stmt := in.Statement
	event := domain.FollowupAddedEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       eventID,
			SchemaVersion: 1,
			EventType:     domain.EventFollowupAdded,
			ActorID:       identity.ActorID,
			ActorName:     s.actorDisplayName(identity),
			Timestamp:     core.Now(),
		},
		FollowupID: id,
		IntentID:   intentID,
		Files:      files,
		Statement:  stmt,
		Source:     in.Source,
	}
	if err := s.appendSignalEvent(cfg, event); err != nil {
		return nil, err
	}
	return &domain.Followup{
		ID:           id,
		Text:         stmt.Text(),
		Statement:    &stmt,
		Status:       "open",
		SourceIntent: intentID,
		Files:        files,
		OpenedBy:     identity.ActorID,
		OpenedAt:     event.Timestamp,
		Source:       event.Source,
	}, nil
}

func (s *Service) signalWriteContext() (*domain.Identity, *domain.TeamConfig, error) {
	identity, err := s.requireIdentity()
	if err != nil {
		return nil, nil, err
	}
	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, nil, err
	}
	return identity, cfg, nil
}

func (s *Service) appendSignalEvent(cfg *domain.TeamConfig, event interface{}) error {
	identity, err := s.requireIdentity()
	if err != nil {
		return err
	}
	if err := s.Store.AppendActorLogEvent(identity.ActorID, cfg.Mainline.ActorLogPrefix, event); err != nil {
		return fmt.Errorf("write signal event: %w", err)
	}
	if s.Git.HasRemote(s.remoteName()) {
		ref := s.Store.ActorLogRef(identity.ActorID, cfg.Mainline.ActorLogPrefix)
		refspec := fmt.Sprintf("%s:%s", ref, ref)
		_ = s.Git.Push(s.remoteName(), refspec)
	}
	if _, err := s.rebuildView(cfg); err != nil {
		return fmt.Errorf("rebuild view after signal write: %w", err)
	}
	return nil
}

func (s *Service) resolveSignalIntent(intentID string) (string, error) {
	intentID = strings.TrimSpace(intentID)
	if intentID != "" {
		return intentID, nil
	}
	branch, _ := s.Git.CurrentBranch()
	draft, _ := s.Store.FindActiveDraft(branch)
	if draft == nil {
		return "", domain.NewRecoverableError(domain.ErrNoActiveIntent,
			"no --intent provided and no active draft on this branch",
			"pass --intent int_xxx or start an intent first",
		)
	}
	return draft.IntentID, nil
}

func cleanSignalFiles(files []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

func normalizedSeverity(sev string) string {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "high", "medium", "low":
		return strings.ToLower(strings.TrimSpace(sev))
	default:
		return "high"
	}
}
