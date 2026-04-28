// Package hub builds a read-only static site over the local synced
// intent view. The CLI entry point is `mainline hub export <dir>`.
//
// Hub v1 is a deliberately throwaway prototype that validates one
// question: does a centralised, browsable view of intent history
// pull its weight for human readers? If yes, Hub v2 is a hosted
// service that ingests intent logs across repos and serves an API.
//
// To make that pivot cheap, this package is split:
//
//   - model.go   the Hub view-model — survives into Hub v2 as the
//     JSON DTO the API would return.
//   - export.go  derivation of the model from engine read APIs +
//     static-site directory layout. Throwaway.
//   - render.go  Go-template HTML rendering. Throwaway.
//
// Internal vocabulary stays close to the domain types (IntentView /
// IntentSummary / SemanticFingerprint) so the export step is mostly
// flattening, not transformation.
package hub

import "github.com/mainline-org/mainline/internal/domain"

// HubModel is the full materialised view the renderer consumes.
// Built once per `mainline hub export` invocation; intentionally
// JSON-serialisable so we can drop a copy at hub/data/intents.json
// for inspection / future ingestion.
type HubModel struct {
	GeneratedAt string          `json:"generated_at"`
	MainBranch  string          `json:"main_branch"`
	MainHead    string          `json:"main_head"`
	Dashboard   HubDashboard    `json:"dashboard"`
	Intents     []HubIntent     `json:"intents"`
	OpenIntents []HubOpenIntent `json:"open_intents,omitempty"`

	// Derived indexes. These are pure functions of Intents; they live
	// on the model so the renderer doesn't have to recompute them and
	// so a future API can return them cheaply.
	FileIndex   []HubFileEntry   `json:"file_index"`
	ActorIndex  []HubActorEntry  `json:"actor_index"`
	RiskIntents []string         `json:"risk_intents"`
	Relations   []HubRelationRow `json:"relations"`
}

// HubDashboard is the human-first landing view: small rollups and
// prioritized links that answer "what should I inspect now?" without
// forcing readers through raw tables.
type HubDashboard struct {
	TotalIntents    int              `json:"total_intents"`
	OpenIntents     int              `json:"open_intents"`
	ProposedIntents int              `json:"proposed_intents"`
	MergedIntents   int              `json:"merged_intents"`
	RiskIntents     int              `json:"risk_intents"`
	FileCount       int              `json:"file_count"`
	ActorCount      int              `json:"actor_count"`
	StatusCounts    []HubStatusCount `json:"status_counts,omitempty"`
	Focus           []HubFocusIntent `json:"focus,omitempty"`
	HotFiles        []HubHotFile     `json:"hot_files,omitempty"`
}

type HubStatusCount struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

type HubFocusIntent struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type HubHotFile struct {
	Path        string `json:"path"`
	IntentCount int    `json:"intent_count"`
}

// HubIntent is the per-intent record. Fields map 1:1 onto IntentView
// + IntentSummary + SemanticFingerprint where they exist; we copy
// values rather than embedding the domain types so a renamed domain
// field does not silently mutate the Hub contract.
type HubIntent struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Goal      string `json:"goal"`
	Thread    string `json:"thread"`
	GitBranch string `json:"git_branch,omitempty"`

	ActorID   string `json:"actor_id"`
	ActorName string `json:"actor_name,omitempty"`

	SealedAt         string `json:"sealed_at,omitempty"`
	MergedMainCommit string `json:"merged_main_commit,omitempty"`
	BaseCommit       string `json:"base_commit,omitempty"`
	CodeCommit       string `json:"code_commit,omitempty"`

	What      string           `json:"what,omitempty"`
	Why       string           `json:"why,omitempty"`
	UserGoal  string           `json:"user_goal,omitempty"`
	Decisions []HubDecision    `json:"decisions,omitempty"`
	Rejected  []HubAlternative `json:"rejected,omitempty"`
	Risks     []string         `json:"risks,omitempty"`
	Followups []string         `json:"followups,omitempty"`

	Subsystems          []string `json:"subsystems,omitempty"`
	FilesTouched        []string `json:"files_touched,omitempty"`
	ArchitecturalClaims []string `json:"architectural_claims,omitempty"`
	BehavioralChanges   []string `json:"behavioral_changes,omitempty"`
	Tags                []string `json:"tags,omitempty"`

	SupersededByIntent string `json:"superseded_by_intent,omitempty"`
}

type HubDecision struct {
	Point     string   `json:"point"`
	Chose     string   `json:"chose"`
	Rationale string   `json:"rationale,omitempty"`
	Rejected  []string `json:"rejected,omitempty"`
}

type HubAlternative struct {
	Alternative string `json:"alternative"`
	Reason      string `json:"reason,omitempty"`
}

// HubFileEntry is the reverse index: which intents touched which
// file. Sorted Path ascending; IntentIDs sorted by intent activity
// (most recent first) at export time.
type HubFileEntry struct {
	Path      string   `json:"path"`
	IntentIDs []string `json:"intent_ids"`
}

// HubActorEntry is the reverse index: which intents this actor
// authored. Sorted ActorID ascending; IntentIDs newest-first.
type HubActorEntry struct {
	ActorID   string   `json:"actor_id"`
	ActorName string   `json:"actor_name,omitempty"`
	IntentIDs []string `json:"intent_ids"`
}

// HubRelationRow is the simple text-adjacency representation of
// intent-to-intent links. Hub v1 only carries supersedes /
// superseded-by — those are the only links the domain currently
// records explicitly. "relates" is a future-Hub concern.
type HubRelationRow struct {
	From string `json:"from"`
	Kind string `json:"kind"` // "supersedes" | "superseded_by"
	To   string `json:"to"`
}

// HubOpenIntent is local in-flight work that is not yet represented
// as a sealed intent in the mainline view.
type HubOpenIntent struct {
	ID             string `json:"id"`
	Goal           string `json:"goal"`
	Status         string `json:"status"`
	Thread         string `json:"thread"`
	GitBranch      string `json:"git_branch,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	LastModifiedAt string `json:"last_modified_at,omitempty"`
	TurnCount      int    `json:"turn_count"`
}

// hubIntentFromView flattens an IntentView (+ embedded summary +
// fingerprint) into a HubIntent. Pure, no I/O — easy to unit-test.
func hubIntentFromView(v *domain.IntentView) HubIntent {
	out := HubIntent{
		ID:                 v.IntentID,
		Status:             string(v.Status),
		Goal:               v.Goal,
		Thread:             v.Thread,
		GitBranch:          v.GitBranch,
		ActorID:            v.ActorID,
		ActorName:          v.ActorName,
		SealedAt:           v.SealedAt,
		MergedMainCommit:   v.StatusEvidence.MergedMainCommit,
		BaseCommit:         v.BaseCommit,
		CodeCommit:         v.CodeCommit,
		SupersededByIntent: v.StatusEvidence.SupersededByIntent,
	}
	if s := v.Summary; s != nil {
		out.Title = s.Title
		out.What = s.What
		out.Why = s.Why
		out.UserGoal = s.UserGoal
		out.Risks = append([]string(nil), s.Risks...)
		out.Followups = append([]string(nil), s.Followups...)
		for _, d := range s.Decisions {
			out.Decisions = append(out.Decisions, HubDecision{
				Point:     d.Point,
				Chose:     d.Chose,
				Rationale: d.Rationale,
				Rejected:  append([]string(nil), d.Rejected...),
			})
		}
		for _, a := range s.Rejected {
			out.Rejected = append(out.Rejected, HubAlternative{
				Alternative: a.Alternative,
				Reason:      a.Reason,
			})
		}
	}
	if f := v.Fingerprint; f != nil {
		out.Subsystems = append([]string(nil), f.Subsystems...)
		out.FilesTouched = append([]string(nil), f.FilesTouched...)
		out.ArchitecturalClaims = append([]string(nil), f.ArchitecturalClaims...)
		out.BehavioralChanges = append([]string(nil), f.BehavioralChanges...)
		out.Tags = append([]string(nil), f.Tags...)
	}
	if out.Title == "" {
		out.Title = v.Goal
	}
	return out
}
