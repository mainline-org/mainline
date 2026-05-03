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
	TeamHealth  HubTeamHealth   `json:"team_health"`
	Intents     []HubIntent     `json:"intents"`
	OpenIntents []HubOpenIntent `json:"open_intents,omitempty"`

	// Derived indexes. These are pure functions of Intents; they live
	// on the model so the renderer doesn't have to recompute them and
	// so a future API can return them cheaply.
	FileIndex   []HubFileEntry   `json:"file_index"`
	ActorIndex  []HubActorEntry  `json:"actor_index"`
	RiskIntents []string         `json:"risk_intents"`
	Relations   []HubRelationRow `json:"relations"`

	// CoverageDetail is the per-commit coverage rollup for the
	// /coverage.html page. Populated by the engine→hub bridge in
	// export.go (Service.Gaps + CoverageWindow); empty when no
	// coverage input was provided.
	CoverageDetail HubCoverageDetail `json:"coverage_detail,omitempty"`

	// InheritedHotspots is the per-file roll-up of inherited
	// anti_patterns. Drives the Inherited constraints heatmap on
	// /index.html and the per-file inherited section on
	// /files/<path>.html. Sorted by HighSeverityCount desc then
	// UnacknowledgedRecentTouches desc.
	InheritedHotspots []HubInheritedHotspot `json:"inherited_hotspots,omitempty"`
}

// HubInheritedHotspot mirrors domain.InheritedConstraintHotspot. We
// keep it as its own type so the Hub JSON contract is independent of
// the domain refactors and future fields (e.g. file slug pre-computed
// for the renderer) can land without touching the domain shape.
type HubInheritedHotspot struct {
	FilePath                    string                   `json:"file_path"`
	ConstraintCount             int                      `json:"constraint_count"`
	HighSeverityCount           int                      `json:"high_severity_count"`
	UnacknowledgedRecentTouches int                      `json:"unacknowledged_recent_touches"`
	RecentTouches               int                      `json:"recent_touches"`
	Constraints                 []HubInheritedConstraint `json:"constraints,omitempty"`
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
	// AgeHours is the wall-clock age (sealed→now or last activity→now)
	// in whole hours. 0 when timestamps aren't available; renderer
	// hides the column then. Drives the review-queue aging buckets.
	AgeHours int `json:"age_hours,omitempty"`
	// RiskCount + FileCount let the dashboard show "1 open risk"
	// / "touches 3 files" without a second lookup.
	RiskCount int `json:"risk_count,omitempty"`
	FileCount int `json:"file_count,omitempty"`
	// HighRisk is true when the intent has at least one effective
	// open risk or anti-pattern — used to pin high-risk items above
	// same-age peers in the review queue.
	HighRisk bool `json:"high_risk,omitempty"`
	// ActorID/ActorName carry authorship so the dashboard and digest
	// can render "who proposed this" without a second lookup. Same
	// fields as HubIntent — kept here so HubFocusIntent stays a
	// self-contained DTO.
	ActorID   string `json:"actor_id,omitempty"`
	ActorName string `json:"actor_name,omitempty"`
}

type HubHotFile struct {
	Path        string `json:"path"`
	IntentCount int    `json:"intent_count"`
	// Decision-hotspots metadata (spec §9). RiskIntentCount is how
	// many of the IntentCount intents flagged risks; RecentCount is
	// how many were sealed within the last 7 days. Both are
	// pre-computed in the model layer so the renderer doesn't have
	// to walk the catalog per row.
	RiskIntentCount int `json:"risk_intent_count"`
	RecentCount     int `json:"recent_count"`
}

// HubTeamHealth is the founder-facing summary the spec calls for.
// Survives into Hub v2 as the JSON DTO behind a /team-health API
// endpoint; same omitempty discipline as the other Hub types so an
// older reader parses unchanged.
//
// The headline state is HealthLevel — one of "healthy" | "attention"
// | "critical". Spec §4.3: data unavailability never reads as
// healthy; HealthLevel falls back to "" with HealthSummary
// explaining why.
type HubTeamHealth struct {
	HealthLevel   string `json:"health_level"`
	HealthSummary string `json:"health_summary"`

	// Counts duplicated from the existing Dashboard so the
	// team-health JSON is self-contained for downstream consumers.
	TotalIntents     int `json:"total_intents"`
	OpenIntents      int `json:"open_intents"`
	ProposedIntents  int `json:"proposed_intents"`
	RiskIntentCount  int `json:"risk_intent_count"`
	FilesWithHistory int `json:"files_with_history"`

	// Aging snapshot for review-queue / open-work freshness.
	ProposedOlderThan12h int `json:"proposed_older_than_12h"`
	ProposedOlderThan24h int `json:"proposed_older_than_24h"`
	ProposedOlderThan48h int `json:"proposed_older_than_48h"`
	ProposedOlderThan72h int `json:"proposed_older_than_72h"`
	OldestProposedHours  int `json:"oldest_proposed_hours"`
	OpenOlderThan24h     int `json:"open_older_than_24h"`
	OpenOlderThan72h     int `json:"open_older_than_72h"`

	// Coverage subsection (spec §6). Available=false on repos where
	// gaps data hasn't been computed; renderer must show partial-
	// data wording rather than pretending healthy.
	Coverage HubCoverageSummary `json:"coverage"`

	// Risk radar (spec §8). MissingMitigation is left nil-able
	// because the heuristic is fragile; renderer hides the line
	// when it's not available rather than printing "0".
	Risk HubRiskRadar `json:"risk"`

	// Weekly digest (spec §10). 7-day rolling window.
	Digest HubWeeklyDigest `json:"digest"`

	// ActorActivity is per-actor distribution of in-flight + recent
	// sealed work. NOT a leaderboard — sorted alphabetically and
	// scoped to "what's currently open" + "what merged in the digest
	// window". Spec §11.
	ActorActivity []HubActorActivity `json:"actor_activity,omitempty"`

	// Lifecycle is the status mix + supersession/abandonment ratios
	// across the sealed catalog. Spec §12. Verdict empty when sample
	// size is too small to judge.
	Lifecycle HubLifecycleHealth `json:"lifecycle"`
}

// HubActorActivity rolls up per-actor work distribution for the
// dashboard's actor section. Counts only — never rankings, never
// productivity metrics. ActorID is unique; ActorName is the
// human-friendly form for display.
type HubActorActivity struct {
	ActorID          string `json:"actor_id"`
	ActorName        string `json:"actor_name,omitempty"`
	OpenProposed     int    `json:"open_proposed"`
	SealedThisWindow int    `json:"sealed_this_window"`
}

// HubLifecycleHealth captures the status distribution of all sealed
// intents plus supersession + abandonment ratios. Verdict is one of
// "healthy" | "attention" | "critical" | "" (when sample is too
// small to judge — under 5 sealed intents).
type HubLifecycleHealth struct {
	Total            int     `json:"total"`
	Proposed         int     `json:"proposed"`
	Merged           int     `json:"merged"`
	Abandoned        int     `json:"abandoned"`
	Superseded       int     `json:"superseded"`
	Reverted         int     `json:"reverted"`
	SupersessionRate float64 `json:"supersession_rate"`
	AbandonmentRate  float64 `json:"abandonment_rate"`
	Verdict          string  `json:"verdict,omitempty"`
}

// HubCoverageSummary encodes the "intent coverage" subsection. When
// Available is false, render-side must show partial-data wording
// instead of zero counts.
type HubCoverageSummary struct {
	Available                bool    `json:"available"`
	CoveredCommits           int     `json:"covered_commits"`
	UncoveredCommits         int     `json:"uncovered_commits"`
	CoverageRatio            float64 `json:"coverage_ratio"`
	HighRiskUncoveredCommits int     `json:"high_risk_uncovered_commits"`
}

// HubCoverageDetail is the per-commit list rendered on the standalone
// /coverage.html page. Populated alongside HubCoverageSummary by the
// engine→hub bridge in export.go. Order is newest-first to match the
// rest of the Hub UI.
type HubCoverageDetail struct {
	WindowSize int                 `json:"window_size"`
	Commits    []HubCoverageCommit `json:"commits,omitempty"`
}

type HubCoverageCommit struct {
	Commit      string `json:"commit"`
	Subject     string `json:"subject"`
	Author      string `json:"author"`
	CommittedAt string `json:"committed_at"`
	State       string `json:"state"`
	HighRisk    bool   `json:"high_risk,omitempty"`
	SkipReason  string `json:"skip_reason,omitempty"`
}

// HubRiskRadar surfaces actionable constraint and risk signal —
// proposed intents that carry constraints or soft risks, files with
// concentrated constraint history. NOT a count of all intents with
// risks (that already lives on the dashboard); these are the
// actionable subsets needing review.
type HubRiskRadar struct {
	RiskBearingIntents      int              `json:"risk_bearing_intents"`
	RiskBearingProposed     int              `json:"risk_bearing_proposed"`
	RecentRiskBearing       int              `json:"recent_risk_bearing"`
	RisksMissingMitigation  *int             `json:"risks_missing_mitigation,omitempty"`
	RiskHeavyFiles          []HubHotFile     `json:"risk_heavy_files,omitempty"`
	RiskBearingProposedRows []HubFocusIntent `json:"risk_bearing_proposed_rows,omitempty"`
}

// HubWeeklyDigest is the 7-day rolling rollup. ImportantDecisions /
// RisksToWatch / AbandonedApproaches are pre-truncated to a small N
// so the dashboard section stays scannable; the full lists live on
// their dedicated pages.
type HubWeeklyDigest struct {
	WindowDays            int              `json:"window_days"`
	SealedThisWindow      int              `json:"sealed_this_window"`
	ProposedThisWindow    int              `json:"proposed_this_window"`
	AbandonedThisWindow   int              `json:"abandoned_this_window"`
	SupersededThisWindow  int              `json:"superseded_this_window"`
	RiskBearingThisWindow int              `json:"risk_bearing_this_window"`
	HotFilesThisWindow    []HubHotFile     `json:"hot_files_this_window,omitempty"`
	ImportantDecisions    []HubFocusIntent `json:"important_decisions,omitempty"`
	RisksToWatch          []HubFocusIntent `json:"risks_to_watch,omitempty"`
	AbandonedApproaches   []HubFocusIntent `json:"abandoned_approaches,omitempty"`
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

	What         string               `json:"what,omitempty"`
	Why          string               `json:"why,omitempty"`
	UserGoal     string               `json:"user_goal,omitempty"`
	Decisions    []HubDecision        `json:"decisions,omitempty"`
	Rejected     []HubAlternative     `json:"rejected,omitempty"`
	Risks        []string             `json:"risks,omitempty"`
	OpenRisks    []domain.Risk        `json:"open_risks,omitempty"`
	AntiPatterns []domain.AntiPattern `json:"anti_patterns,omitempty"`
	Followups    []string             `json:"followups,omitempty"`

	Subsystems          []string `json:"subsystems,omitempty"`
	FilesTouched        []string `json:"files_touched,omitempty"`
	ArchitecturalClaims []string `json:"architectural_claims,omitempty"`
	BehavioralChanges   []string `json:"behavioral_changes,omitempty"`
	Tags                []string `json:"tags,omitempty"`

	SupersededByIntent string `json:"superseded_by_intent,omitempty"`

	// Turns is the append-by-append timeline of the intent.
	// Populated from the local store when available; nil for remote
	// intents whose turns were never synced locally.
	Turns []HubTurn `json:"turns,omitempty"`

	// References to external materials (sessions, issues, etc.)
	References []domain.Reference `json:"references,omitempty"`

	// LastCheck mirrors IntentView.LastCheck so the graph view can
	// surface phase-2 conflict edges. Nil means no agent has run
	// `mainline check --submit` against this intent yet.
	LastCheck *HubCheckSummary `json:"last_check,omitempty"`

	// InheritedConstraints surfaces anti_patterns from prior intents
	// whose touched files / subsystems overlap with this intent.
	// Each entry carries an Acknowledgement (decision /
	// rejected_alternative / anti_pattern / risk / "" for none) so
	// reviewers see "did the agent acknowledge the prior constraint"
	// without re-walking the seal text.
	InheritedConstraints []HubInheritedConstraint `json:"inherited_constraints,omitempty"`
}

// HubInheritedConstraint mirrors domain.InheritedConstraint plus the
// pre-computed acknowledgement form the Hub renderer + reviewer-facing
// surfaces use as a badge. Acknowledgement is "" when not acknowledged
// in any of the supported forms.
type HubInheritedConstraint struct {
	SourceIntent    string   `json:"source_intent"`
	What            string   `json:"what"`
	Why             string   `json:"why"`
	Severity        string   `json:"severity,omitempty"`
	MatchedBy       []string `json:"matched_by"`
	Acknowledgement string   `json:"acknowledgement,omitempty"` // decision | rejected_alternative | anti_pattern | risk | ""
}

// HubCheckSummary is the per-intent rollup of the latest phase-2
// judgment. Mirrors domain.CheckSummary, intentionally narrow — the
// Hub only needs the conflict signal + the IDs of the intents the
// check ruled against, not the full Evidence blob.
type HubCheckSummary struct {
	HasConflict      bool     `json:"has_conflict"`
	HighestSeverity  string   `json:"highest_severity,omitempty"`
	NeedsHumanReview bool     `json:"needs_human_review,omitempty"`
	AgainstIntents   []string `json:"against_intents,omitempty"`
	AtTime           string   `json:"at,omitempty"`
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

// HubTurn is a single turn in the intent's timeline. Mirrors the
// essential fields from domain.Turn for the Hub renderer.
type HubTurn struct {
	Index       int    `json:"index"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at,omitempty"`
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

// HubRelationRow is the text-adjacency representation of intent-to-
// intent links. Three kinds:
//
//   - supersedes / superseded_by — bidirectional, written from
//     IntentView.StatusEvidence.SupersededByIntent.
//   - conflicts_with — bidirectional, written from
//     IntentView.LastCheck.AgainstIntents (phase-2 check judgments).
//   - shares_file — bidirectional implicit edge, emitted when two
//     intents touched the same file. Note carries the file count so
//     the renderer can rank by overlap weight.
type HubRelationRow struct {
	From string `json:"from"`
	Kind string `json:"kind"` // "supersedes" | "superseded_by" | "conflicts_with" | "shares_file"
	To   string `json:"to"`
	Note string `json:"note,omitempty"`
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

// HubIntentFromView is the exported alias used by callers outside
// this package (e.g. the `mainline digest` CLI) that need to flatten
// view rows into the Hub shape. Internal callers still use the lower-
// case form.
func HubIntentFromView(v *domain.IntentView) HubIntent {
	return hubIntentFromView(v)
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
		out.AntiPatterns = append([]domain.AntiPattern(nil), s.AntiPatterns...)
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
	if c := v.LastCheck; c != nil {
		out.LastCheck = &HubCheckSummary{
			HasConflict:      c.HasConflict,
			HighestSeverity:  c.HighestSeverity,
			NeedsHumanReview: c.NeedsHumanReview,
			AgainstIntents:   append([]string(nil), c.AgainstIntents...),
			AtTime:           c.AtTime,
		}
	}
	if f := v.Fingerprint; f != nil {
		out.Subsystems = append([]string(nil), f.Subsystems...)
		out.FilesTouched = append([]string(nil), f.FilesTouched...)
		out.ArchitecturalClaims = append([]string(nil), f.ArchitecturalClaims...)
		out.BehavioralChanges = append([]string(nil), f.BehavioralChanges...)
		out.Tags = append([]string(nil), f.Tags...)
	}
	if len(v.References) > 0 {
		out.References = append([]domain.Reference(nil), v.References...)
	}
	if out.Title == "" {
		out.Title = v.Goal
	}
	return out
}
