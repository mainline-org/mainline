package engine

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/mainline-org/mainline/internal/core"
	"github.com/mainline-org/mainline/internal/domain"
)

// -----------------------------------------------------------
// Check --prepare (Phase 1: deterministic fingerprint overlap)
// -----------------------------------------------------------

func (s *Service) CheckPrepare(intentID string) (*domain.CheckPreparePackage, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}

	// Find the candidate intent
	var candidate *domain.IntentView
	view, _ := s.Store.ReadMainlineView()
	if view != nil {
		for _, iv := range view.Intents {
			if iv.IntentID == intentID {
				candidate = &iv
				break
			}
		}
	}

	// Also check drafts (sealed_local)
	if candidate == nil {
		draft, _ := s.Store.ReadDraft(intentID)
		if draft != nil && draft.Status == domain.StatusSealedLocal {
			// We need the sealed event data - for now build a minimal view
			return nil, domain.NewError(domain.ErrInvalidStatus,
				"intent must be published before running check; use 'mainline publish' first")
		}
		if draft == nil {
			return nil, domain.NewError(domain.ErrNoActiveIntent,
				fmt.Sprintf("intent %s not found", intentID))
		}
	}

	if candidate == nil || candidate.Fingerprint == nil {
		return nil, domain.NewError(domain.ErrCheckFailed,
			"candidate intent has no fingerprint; seal it first")
	}

	// Phase 1: find suspicious pairs
	var tasks []domain.CheckTask
	threshold := cfg.Check.Phase1Threshold
	belowThreshold := 0

	for _, iv := range view.Intents {
		if iv.IntentID == intentID {
			continue
		}
		if iv.Status != domain.StatusMerged && iv.Status != domain.StatusProposed {
			continue
		}
		if iv.Fingerprint == nil {
			continue
		}

		score := FingerprintOverlap(candidate.Fingerprint, iv.Fingerprint)
		if score < threshold {
			belowThreshold++
			continue
		}

		task := domain.CheckTask{
			TaskID:                  fmt.Sprintf("task_%s_%s", intentID, iv.IntentID),
			FingerprintOverlapScore: score,
			Instruction: fmt.Sprintf(
				"Compare candidate intent %s against mainline intent %s. "+
					"Fingerprint overlap score: %.2f. Analyze for semantic conflicts.",
				intentID, iv.IntentID, score),
		}
		task.MainlineIntent.ID = iv.IntentID
		task.MainlineIntent.Title = ""
		if iv.Summary != nil {
			task.MainlineIntent.Title = iv.Summary.Title
		}
		task.MainlineIntent.Status = string(iv.Status)
		task.MainlineIntent.Fingerprint = *iv.Fingerprint
		task.CandidateIntent.ID = intentID
		tasks = append(tasks, task)
	}

	// Sort by overlap score descending
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].FingerprintOverlapScore > tasks[j].FingerprintOverlapScore
	})

	pkg := &domain.CheckPreparePackage{
		Kind:          "mainline.check.prepare",
		SchemaVersion: 1,
		JudgmentTasks: tasks,
		Instruction:   checkInstruction(),
	}
	pkg.CandidateIntent.ID = candidate.IntentID
	if candidate.Summary != nil {
		pkg.CandidateIntent.Title = candidate.Summary.Title
		pkg.CandidateIntent.Summary = *candidate.Summary
	}
	if candidate.Fingerprint != nil {
		pkg.CandidateIntent.Fingerprint = *candidate.Fingerprint
	}
	pkg.Phase1.Lookback = cfg.Check.Lookback
	pkg.Phase1.BelowThreshold = belowThreshold
	pkg.Phase1.SuspiciousPairs = len(tasks)

	return pkg, nil
}

func checkInstruction() string {
	return `For each judgment task, analyze the candidate intent against the mainline intent.
Produce a CheckJudgmentResult JSON with:
1. candidate_intent: the candidate intent ID
2. judgments: array of ConflictJudgment for each task
3. overall: has_conflict, highest_severity, needs_human_review

Return ONLY valid JSON matching the CheckJudgmentResult schema.`
}

// -----------------------------------------------------------
// Check --submit
// -----------------------------------------------------------

type CheckSubmitResult struct {
	CandidateIntent  string `json:"candidate_intent"`
	HasConflict      bool   `json:"has_conflict"`
	HighestSeverity  string `json:"highest_severity"`
	NeedsHumanReview bool   `json:"needs_human_review"`
	JudgmentCount    int    `json:"judgment_count"`
	EventID          string `json:"event_id"`
}

func (s *Service) CheckSubmit(input json.RawMessage) (*CheckSubmitResult, error) {
	if err := s.requireInit(); err != nil {
		return nil, err
	}

	var cr domain.CheckJudgmentResult
	if err := json.Unmarshal(input, &cr); err != nil {
		return nil, domain.NewError(domain.ErrInvalidInput,
			fmt.Sprintf("invalid CheckJudgmentResult JSON: %v", err))
	}

	if err := core.ValidateCheckJudgmentResult(&cr); err != nil {
		return nil, domain.NewError(domain.ErrCheckFailed, err.Error())
	}

	// Write check judgment event to actor log
	identity, err := s.getIdentity()
	if err != nil {
		return nil, err
	}
	cfg, err := s.getTeamConfig()
	if err != nil {
		return nil, err
	}

	eventID := core.GenerateEventID()
	event := domain.CheckJudgmentEvent{
		BaseEvent: domain.BaseEvent{
			EventID:       eventID,
			SchemaVersion: 1,
			EventType:     domain.EventCheckJudgment,
			ActorID:       identity.ActorID,
			ActorName:     s.actorDisplayName(identity),
			Timestamp:     core.Now(),
		},
		CandidateIntent: cr.CandidateIntent,
		Judgments:       cr.Judgments,
		Overall:         cr.Overall,
	}

	if err := s.Store.AppendActorLogEvent(identity.ActorID, cfg.Mainline.ActorLogPrefix, event); err != nil {
		return nil, fmt.Errorf("write check event: %w", err)
	}

	res := &CheckSubmitResult{
		CandidateIntent:  cr.CandidateIntent,
		HasConflict:      cr.Overall.HasConflict,
		HighestSeverity:  cr.Overall.HighestSeverity,
		NeedsHumanReview: cr.Overall.NeedsHumanReview,
		JudgmentCount:    len(cr.Judgments),
		EventID:          eventID,
	}
	s.emit("check_judged", res)
	return res, nil
}

// -----------------------------------------------------------
// Fingerprint Overlap (Phase 1 Scoring)
// -----------------------------------------------------------

// FingerprintOverlap computes the overlap score between two fingerprints.
// Uses a weighted Jaccard-like similarity across multiple dimensions.
func FingerprintOverlap(a, b *domain.SemanticFingerprint) float64 {
	if a == nil || b == nil {
		return 0
	}

	weights := map[string]float64{
		"subsystems":   0.25,
		"files":        0.30,
		"architecture": 0.15,
		"behavioral":   0.15,
		"api":          0.10,
		"tags":         0.05,
	}

	score := 0.0
	score += weights["subsystems"] * jaccard(a.Subsystems, b.Subsystems)
	score += weights["files"] * jaccard(a.FilesTouched, b.FilesTouched)
	score += weights["architecture"] * jaccard(a.ArchitecturalClaims, b.ArchitecturalClaims)
	score += weights["behavioral"] * jaccard(a.BehavioralChanges, b.BehavioralChanges)
	score += weights["tags"] * jaccard(a.Tags, b.Tags)

	// API changes: compare by signature
	var apiA, apiB []string
	for _, c := range a.APIChanges {
		apiA = append(apiA, c.Signature)
	}
	for _, c := range b.APIChanges {
		apiB = append(apiB, c.Signature)
	}
	score += weights["api"] * jaccard(apiA, apiB)

	return score
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	setA := make(map[string]bool)
	for _, s := range a {
		setA[s] = true
	}
	setB := make(map[string]bool)
	for _, s := range b {
		setB[s] = true
	}

	intersection := 0
	for k := range setA {
		if setB[k] {
			intersection++
		}
	}

	union := len(setA)
	for k := range setB {
		if !setA[k] {
			union++
		}
	}

	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
