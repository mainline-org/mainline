package engine

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mainline-org/mainline/internal/core"
	"github.com/mainline-org/mainline/internal/domain"
)

type ActorLogImportOptions struct {
	ActorID   string
	Remote    string
	SourceRef string
	ImportRef string
	Force     bool
}

type ActorLogImportResult struct {
	ActorID             string         `json:"actor_id"`
	SourceRemote        string         `json:"source_remote,omitempty"`
	SourceRef           string         `json:"source_ref"`
	ImportRef           string         `json:"import_ref,omitempty"`
	ImportedBranchRefs  []string       `json:"imported_branch_refs,omitempty"`
	ObjectFetchWarnings []string       `json:"object_fetch_warnings,omitempty"`
	SourceHead          string         `json:"source_head"`
	TargetRef           string         `json:"target_ref"`
	PreviousTargetHead  string         `json:"previous_target_head,omitempty"`
	EventCount          int            `json:"event_count"`
	SealedIntentCount   int            `json:"sealed_intent_count"`
	SealedIntentIDs     []string       `json:"sealed_intent_ids,omitempty"`
	Accepted            bool           `json:"accepted"`
	AcceptedBy          string         `json:"accepted_by"`
	AcceptedEventID     string         `json:"accepted_event_id,omitempty"`
	Pushed              bool           `json:"pushed"`
	AutoPinned          []PinnedCommit `json:"auto_pinned,omitempty"`
}

func (s *Service) ImportActorLog(opts ActorLogImportOptions) (*ActorLogImportResult, error) {
	identity, err := s.requireIdentity()
	if err != nil {
		return nil, err
	}
	if err := s.requireInit(); err != nil {
		return nil, err
	}
	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}

	actorID := strings.TrimSpace(opts.ActorID)
	if actorID == "" {
		return nil, domain.NewError(domain.ErrInvalidInput, "actor import requires --actor")
	}

	targetRef := domain.ActorLogRef(actorID, cfg.Mainline.ActorLogPrefix)
	sourceRef := strings.TrimSpace(opts.SourceRef)
	importRef := strings.TrimSpace(opts.ImportRef)
	remote := strings.TrimSpace(opts.Remote)
	resultSourceRef := sourceRef

	if remote != "" {
		if sourceRef == "" {
			sourceRef = targetRef
		}
		if importRef == "" {
			importRef = "refs/mainline/imports/" + actorID + "/log"
		}
		refspec := "+" + sourceRef + ":" + importRef
		if err := s.Git.Fetch(remote, refspec); err != nil {
			return nil, domain.NewRecoverableError(domain.ErrSyncFailed,
				fmt.Sprintf("fetch actor log %s from %s failed: %v", sourceRef, remote, err),
				"check the fork remote or URL",
				"retry mainline actor import --actor "+actorID+" --remote "+remote)
		}
		resultSourceRef = sourceRef
		sourceRef = importRef
	} else if sourceRef == "" {
		return nil, domain.NewError(domain.ErrInvalidInput, "actor import requires --source-ref or --remote")
	}

	sourceHead := s.Git.ReadRef(sourceRef)
	if sourceHead == "" {
		return nil, domain.NewError(domain.ErrInvalidInput,
			fmt.Sprintf("source actor-log ref %q was not found", sourceRef))
	}

	rawEvents, err := s.Store.ReadActorLogEventsFromRef(sourceRef)
	if err != nil {
		return nil, fmt.Errorf("read source actor log: %w", err)
	}
	validation, err := validateImportedActorEvents(actorID, rawEvents)
	if err != nil {
		return nil, err
	}
	sealedIDs := validation.SealedIntentIDs

	var importedBranchRefs []string
	var objectFetchWarnings []string
	if remote != "" && len(validation.Branches) > 0 {
		importedBranchRefs, objectFetchWarnings = s.fetchImportedBranches(remote, actorID, validation.Branches)
	}

	previousTargetHead := s.Git.ReadRef(targetRef)
	if previousTargetHead != "" && previousTargetHead != sourceHead && !opts.Force {
		ok, err := s.gitIsAncestor(previousTargetHead, sourceHead)
		if err != nil {
			return nil, fmt.Errorf("check actor-log ancestry: %w", err)
		}
		if !ok {
			return nil, domain.NewRecoverableError(domain.ErrConflictDetected,
				fmt.Sprintf("target actor log %s already has a divergent head", targetRef),
				"inspect both refs before accepting",
				"rerun with --force only if the source ref is the intended replacement")
		}
	}

	accepted := previousTargetHead != sourceHead
	eventID := ""
	var autoPinned []PinnedCommit
	if accepted {
		if err := s.Git.UpdateRef(targetRef, sourceHead); err != nil {
			return nil, fmt.Errorf("update target actor log ref: %w", err)
		}

		eventID = core.GenerateEventID()
		acceptEvent := domain.ActorLogAcceptedEvent{
			BaseEvent: domain.BaseEvent{
				EventID:       eventID,
				SchemaVersion: 1,
				EventType:     domain.EventActorLogAccepted,
				ActorID:       identity.ActorID,
				ActorName:     identity.ActorName,
				Timestamp:     core.Now(),
			},
			AcceptedActorID:     actorID,
			SourceRemote:        remote,
			SourceRef:           resultSourceRef,
			SourceHead:          sourceHead,
			TargetRef:           targetRef,
			PreviousTargetHead:  previousTargetHead,
			EventCount:          len(rawEvents),
			SealedIntentIDs:     sealedIDs,
			ImportedBranchRefs:  importedBranchRefs,
			ObjectFetchWarnings: objectFetchWarnings,
			Verified:            true,
			AuthorSealed:        true,
		}
		if err := s.Store.AppendActorLogEvent(identity.ActorID, cfg.Mainline.ActorLogPrefix, acceptEvent); err != nil {
			if previousTargetHead != "" {
				_ = s.Git.UpdateRef(targetRef, previousTargetHead)
			} else {
				_, _ = s.Git.Run("update-ref", "-d", targetRef)
			}
			return nil, fmt.Errorf("write actor-log accept event: %w", err)
		}
	}

	view, err := s.rebuildView(cfg)
	if err != nil {
		return nil, fmt.Errorf("rebuild view after actor import: %w", err)
	}
	if cfg.Sync.AutoPinAfterSync {
		if pinResult, err := s.Pin(); err == nil && pinResult != nil && pinResult.Pinned > 0 {
			autoPinned = pinResult.Links
			view, _ = s.rebuildView(cfg)
		}
	}
	idx := s.rebuildProposedIndex(view)
	_ = s.Store.WriteProposedIndex(idx)

	pushed := false
	if s.Git.HasRemote(s.remoteName()) {
		pushRefspecs := []string{targetRef + ":" + targetRef}
		acceptActorRef := domain.ActorLogRef(identity.ActorID, cfg.Mainline.ActorLogPrefix)
		if s.Git.ReadRef(acceptActorRef) != "" {
			pushRefspecs = append(pushRefspecs, acceptActorRef+":"+acceptActorRef)
		}
		for _, ref := range importedBranchRefs {
			pushRefspecs = append(pushRefspecs, ref+":"+ref)
		}
		if len(autoPinned) > 0 {
			pushRefspecs = append(pushRefspecs, "refs/notes/mainline/intents")
		}
		if err := s.Git.Push(s.remoteName(), pushRefspecs...); err != nil {
			return nil, domain.NewRecoverableError(domain.ErrPublishFailed,
				fmt.Sprintf("accepted actor log locally but failed to push upstream refs: %v", err),
				"check remote access",
				"retry mainline actor import with the same arguments")
		}
		pushed = true
	}

	return &ActorLogImportResult{
		ActorID:             actorID,
		SourceRemote:        remote,
		SourceRef:           resultSourceRef,
		ImportRef:           importRef,
		ImportedBranchRefs:  importedBranchRefs,
		ObjectFetchWarnings: objectFetchWarnings,
		SourceHead:          sourceHead,
		TargetRef:           targetRef,
		PreviousTargetHead:  previousTargetHead,
		EventCount:          len(rawEvents),
		SealedIntentCount:   len(sealedIDs),
		SealedIntentIDs:     sealedIDs,
		Accepted:            accepted,
		AcceptedBy:          identity.ActorID,
		AcceptedEventID:     eventID,
		Pushed:              pushed,
		AutoPinned:          autoPinned,
	}, nil
}

type importedActorValidation struct {
	SealedIntentIDs []string
	Branches        []string
}

func validateImportedActorEvents(actorID string, rawEvents []json.RawMessage) (*importedActorValidation, error) {
	if len(rawEvents) == 0 {
		return nil, domain.NewError(domain.ErrInvalidInput,
			fmt.Sprintf("source actor log for %s contains no events", actorID))
	}

	seenSealed := map[string]bool{}
	seenBranches := map[string]bool{}
	var sealedIDs []string
	var branches []string
	for i, raw := range rawEvents {
		var base domain.BaseEvent
		if err := json.Unmarshal(raw, &base); err != nil {
			return nil, domain.NewError(domain.ErrInvalidInput,
				fmt.Sprintf("source actor log event %d is not valid JSON: %v", i, err))
		}
		if base.EventID == "" || base.EventType == "" {
			return nil, domain.NewError(domain.ErrInvalidInput,
				fmt.Sprintf("source actor log event %d is missing event_id or event_type", i))
		}
		if !knownImportedActorEventType(base.EventType) {
			return nil, domain.NewError(domain.ErrInvalidInput,
				fmt.Sprintf("source actor log event %s has unsupported event_type %q",
					base.EventID, base.EventType))
		}
		if base.ActorID != actorID {
			return nil, domain.NewError(domain.ErrInvalidInput,
				fmt.Sprintf("source actor log event %s belongs to actor %s, expected %s",
					base.EventID, base.ActorID, actorID))
		}
		if base.EventType == domain.EventIntentSealed {
			var evt domain.IntentSealedEvent
			if err := json.Unmarshal(raw, &evt); err != nil {
				return nil, domain.NewError(domain.ErrInvalidInput,
					fmt.Sprintf("sealed event %s is invalid: %v", base.EventID, err))
			}
			if evt.IntentID == "" {
				return nil, domain.NewError(domain.ErrInvalidInput,
					fmt.Sprintf("sealed event %s is missing intent_id", base.EventID))
			}
			if !seenSealed[evt.IntentID] {
				seenSealed[evt.IntentID] = true
				sealedIDs = append(sealedIDs, evt.IntentID)
			}
			branch := strings.TrimSpace(evt.GitBranch)
			if branch != "" && !seenBranches[branch] {
				seenBranches[branch] = true
				branches = append(branches, branch)
			}
		}
	}
	return &importedActorValidation{SealedIntentIDs: sealedIDs, Branches: branches}, nil
}

func knownImportedActorEventType(eventType domain.EventType) bool {
	switch eventType {
	case domain.EventIntentSealed,
		domain.EventIntentSuperseded,
		domain.EventIntentAbandoned,
		domain.EventIntentMergeAcknowledged,
		domain.EventCheckJudgment,
		domain.EventConstraintAdded,
		domain.EventRiskAdded,
		domain.EventRiskResolved,
		domain.EventFollowupAdded,
		domain.EventFollowupResolved:
		return true
	default:
		return false
	}
}

func (s *Service) gitIsAncestor(ancestor, descendant string) (bool, error) {
	if ancestor == "" || descendant == "" {
		return false, nil
	}
	if _, err := s.Git.Run("merge-base", "--is-ancestor", ancestor, descendant); err == nil {
		return true, nil
	} else if strings.Contains(err.Error(), "exit status 1") {
		return false, nil
	} else {
		return false, err
	}
}

func (s *Service) fetchImportedBranches(remote, actorID string, branches []string) ([]string, []string) {
	imported := make([]string, 0, len(branches))
	var errs []string
	for _, branch := range branches {
		branch = strings.TrimSpace(branch)
		if branch == "" {
			continue
		}
		target := s.importedBranchRef(actorID, branch)
		refspec := "+refs/heads/" + branch + ":" + target
		if err := s.Git.Fetch(remote, refspec); err != nil {
			errs = append(errs, fmt.Sprintf("fetch branch %s from %s failed: %v", branch, remote, err))
			continue
		}
		imported = append(imported, target)
	}
	return imported, errs
}

func (s *Service) importedBranchRef(actorID, branch string) string {
	base := "refs/mainline/imports/" + actorID + "/branches/"
	candidate := base + sanitizeImportedBranchRef(branch)
	if _, err := s.Git.Run("check-ref-format", candidate); err == nil {
		return candidate
	}
	sum := sha256.Sum256([]byte(branch))
	return fmt.Sprintf("%sbranch-%x", base, sum[:6])
}

func sanitizeImportedBranchRef(branch string) string {
	branch = strings.TrimSpace(branch)
	var b strings.Builder
	lastSlash := false
	for _, r := range branch {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_':
			b.WriteRune(r)
			lastSlash = false
		case r == '/':
			if !lastSlash {
				b.WriteByte('/')
				lastSlash = true
			}
		default:
			b.WriteByte('-')
			lastSlash = false
		}
	}
	out := strings.Trim(b.String(), "/.")
	if out == "" {
		return "branch"
	}
	return out
}
