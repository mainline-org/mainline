package engine

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestContextRetrieval_QueryGoldenRegressionCoverage(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	view := queryGoldenRegressionView(time.Now().Add(-45 * 24 * time.Hour).UTC().Format(time.RFC3339))
	if err := svc.Store.WriteMainlineView(view); err != nil {
		t.Fatalf("write view: %v", err)
	}

	cases := []struct {
		name      string
		query     string
		wantIDs   []string
		wantEmpty bool
		check     func(t *testing.T, res *ContextRetrievalResult)
	}{
		{
			name:    "ack constraint inherited anti_patterns",
			query:   "ack constraint inherited anti_patterns",
			wantIDs: []string{"int_ack_constraints"},
			check: func(t *testing.T, res *ContextRetrievalResult) {
				assertEffectiveKeyword(t, res, "ack")
				assertEffectiveKeyword(t, res, "anti_patterns")
				assertNotDroppedTerm(t, res, "ack")
				assertExpandedTerms(t, res, "ack", []string{"acknowledge", "acknowledged", "acknowledgement", "acknowledgment"})
				ri := mustFindRelevant(t, res, "int_ack_constraints")
				if len(ri.AntiPatterns) == 0 {
					t.Fatalf("expected anti_patterns surfaced for ack constraint query")
				}
			},
		},
		{
			name:    "context score relevance retrieval",
			query:   "context score relevance retrieval",
			wantIDs: []string{"int_context_scoring"},
			check: func(t *testing.T, res *ContextRetrievalResult) {
				ri := mustFindRelevant(t, res, "int_context_scoring")
				b := ri.Relevance.Breakdown
				if b == nil {
					t.Fatalf("expected query-mode breakdown")
				}
				if b.Title == 0 || b.Summary == 0 || b.Decision == 0 || b.Risk == 0 || b.AntiPattern == 0 {
					t.Fatalf("expected multi-signal breakdown, got %+v", b)
				}
				if ri.Relevance.Score == 0 || len(ri.Relevance.Reasons) == 0 {
					t.Fatalf("score/reasons compatibility fields must remain populated: %+v", ri.Relevance)
				}
			},
		},
		{
			name:    "supersession ranking property chain",
			query:   "supersession ranking property chain",
			wantIDs: []string{"int_supersession_new", "int_supersession_old"},
			check: func(t *testing.T, res *ContextRetrievalResult) {
				newIdx := indexOfRelevant(res, "int_supersession_new")
				oldIdx := indexOfRelevant(res, "int_supersession_old")
				if newIdx < 0 || oldIdx < 0 {
					t.Fatalf("expected both supersession chain endpoints, got %+v", res.RelevantIntents)
				}
				if newIdx >= oldIdx {
					t.Fatalf("superseder must rank above superseded; newIdx=%d oldIdx=%d", newIdx, oldIdx)
				}
			},
		},
		{
			name:    "JWT auth",
			query:   "JWT auth",
			wantIDs: []string{"int_jwt_auth"},
			check: func(t *testing.T, res *ContextRetrievalResult) {
				assertEffectiveKeyword(t, res, "jwt")
				assertEffectiveKeyword(t, res, "auth")
				assertNotDroppedTerm(t, res, "jwt")
			},
		},
		{
			name:    "不要重新引入 subsystem 继承",
			query:   "不要重新引入 subsystem 继承",
			wantIDs: []string{"int_subsystem_inheritance"},
			check: func(t *testing.T, res *ContextRetrievalResult) {
				assertEffectiveKeyword(t, res, "subsystem")
				assertEffectiveKeyword(t, res, "不要重新引入")
				assertEffectiveKeyword(t, res, "继承")
				assertNotDroppedTerm(t, res, "不要重新引入")
				assertNotDroppedTerm(t, res, "继承")
				ri := mustFindRelevant(t, res, "int_subsystem_inheritance")
				if ri.Relevance.Breakdown == nil || ri.Relevance.Breakdown.AntiPattern == 0 {
					t.Fatalf("expected CJK anti_pattern signal in breakdown, got %+v", ri.Relevance.Breakdown)
				}
			},
		},
		{
			name:    "不要重新引入继承约束",
			query:   "不要重新引入继承约束",
			wantIDs: []string{"int_subsystem_inheritance"},
			check: func(t *testing.T, res *ContextRetrievalResult) {
				assertEffectiveKeyword(t, res, "重新")
				assertEffectiveKeyword(t, res, "引入")
				assertEffectiveKeyword(t, res, "继承")
				assertEffectiveKeyword(t, res, "约束")
				ri := mustFindRelevant(t, res, "int_subsystem_inheritance")
				if ri.Relevance.Breakdown == nil || ri.Relevance.Breakdown.AntiPattern == 0 {
					t.Fatalf("expected unspaced CJK query to score via anti_pattern, got %+v", ri.Relevance.Breakdown)
				}
			},
		},
		{
			name:    "确认 inherited constraints",
			query:   "确认 inherited constraints",
			wantIDs: []string{"int_ack_constraints"},
			check: func(t *testing.T, res *ContextRetrievalResult) {
				assertEffectiveKeyword(t, res, "inherited")
				assertEffectiveKeyword(t, res, "constraints")
			},
		},
		{
			name:      "zzzz-no-real-topic",
			query:     "zzzz-no-real-topic",
			wantEmpty: true,
			check: func(t *testing.T, res *ContextRetrievalResult) {
				assertEffectiveKeyword(t, res, "zzzz-no-real-topic")
				if res.QueryDebug.CandidateCount != len(view.Intents) {
					t.Fatalf("expected current no-hit fallback candidate count to expose full-view scan; got %d want %d",
						res.QueryDebug.CandidateCount, len(view.Intents))
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := svc.RetrieveContext(ContextRetrievalRequest{
				Mode:  "query",
				Query: tc.query,
				Limit: 5,
			})
			if err != nil {
				t.Fatalf("retrieve: %v", err)
			}
			if res.QueryDebug == nil {
				t.Fatalf("query_debug must be present for query mode")
			}
			if res.QueryDebug.Raw != tc.query {
				t.Fatalf("query_debug.raw mismatch: got %q want %q", res.QueryDebug.Raw, tc.query)
			}
			if res.QueryDebug.ExpandedTerms == nil {
				t.Fatalf("expanded_terms map should be present, got nil")
			}
			if tc.wantEmpty {
				if len(res.RelevantIntents) != 0 {
					t.Fatalf("expected no relevant intents, got %+v", res.RelevantIntents)
				}
			} else {
				for _, wantID := range tc.wantIDs {
					if idx := indexOfRelevant(res, wantID); idx < 0 || idx >= 5 {
						t.Fatalf("expected %s in top 5, idx=%d result=%+v", wantID, idx, res.RelevantIntents)
					}
				}
			}
			if tc.check != nil {
				tc.check(t, res)
			}
		})
	}
}

func TestContextRetrieval_QueryCJKOnlyFallback(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := svc.Store.WriteMainlineView(queryGoldenRegressionView(time.Now().Add(-45 * 24 * time.Hour).UTC().Format(time.RFC3339))); err != nil {
		t.Fatalf("write view: %v", err)
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "query",
		Query: "不要重新引入 继承",
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	assertEffectiveKeyword(t, res, "不要重新引入")
	assertEffectiveKeyword(t, res, "继承")
	ri := mustFindRelevant(t, res, "int_subsystem_inheritance")
	if ri.Relevance.Breakdown == nil || ri.Relevance.Breakdown.AntiPattern == 0 {
		t.Fatalf("expected CJK-only query to score via anti_pattern, got %+v", ri.Relevance.Breakdown)
	}
}

func TestContextRetrieval_QueryDropsRecencyOnlyMatches(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := svc.Store.WriteMainlineView(queryGoldenRegressionView(time.Now().UTC().Format(time.RFC3339))); err != nil {
		t.Fatalf("write view: %v", err)
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "query",
		Query: "zzzz-no-real-topic",
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.RelevantIntents) != 0 {
		t.Fatalf("query mode should drop recency-only matches, got %+v", res.RelevantIntents)
	}
}

func TestContextRetrieval_QueryDebugJSONShape(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := svc.Store.WriteMainlineView(queryGoldenRegressionView(time.Now().Add(-45 * 24 * time.Hour).UTC().Format(time.RFC3339))); err != nil {
		t.Fatalf("write view: %v", err)
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "query",
		Query: "ack constraint inherited anti_patterns",
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	qd, ok := raw["query_debug"].(map[string]any)
	if !ok {
		t.Fatalf("expected top-level query_debug object in JSON: %s", data)
	}
	for _, key := range []string{"raw", "effective_keywords", "dropped_terms", "expanded_terms", "candidate_count"} {
		if _, ok := qd[key]; !ok {
			t.Fatalf("query_debug missing %s in JSON: %s", key, data)
		}
	}
	intents, ok := raw["relevant_intents"].([]any)
	if !ok || len(intents) == 0 {
		t.Fatalf("expected relevant_intents in JSON: %s", data)
	}
	first, ok := intents[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected first relevant intent JSON: %T", intents[0])
	}
	relevance, ok := first["relevance"].(map[string]any)
	if !ok {
		t.Fatalf("expected relevance object in JSON: %s", data)
	}
	for _, key := range []string{"score", "reasons", "breakdown"} {
		if _, ok := relevance[key]; !ok {
			t.Fatalf("relevance missing %s in JSON: %s", key, data)
		}
	}
}

func TestContextRetrieval_BreakdownOmittedOutsideQueryMode(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := svc.Store.WriteMainlineView(queryGoldenRegressionView(time.Now().Add(-45 * 24 * time.Hour).UTC().Format(time.RFC3339))); err != nil {
		t.Fatalf("write view: %v", err)
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "files",
		Files: []string{"internal/engine/context_retrieval.go"},
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if res.QueryDebug != nil {
		t.Fatalf("query_debug should only be present for query mode")
	}
	if len(res.RelevantIntents) == 0 {
		t.Fatalf("expected file-mode results")
	}
	if res.RelevantIntents[0].Relevance.Breakdown != nil {
		t.Fatalf("breakdown should be omitted outside query mode, got %+v", res.RelevantIntents[0].Relevance.Breakdown)
	}

	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["query_debug"]; ok {
		t.Fatalf("query_debug should be omitted from files-mode JSON: %s", data)
	}
	intents := raw["relevant_intents"].([]any)
	first := intents[0].(map[string]any)
	relevance := first["relevance"].(map[string]any)
	if _, ok := relevance["breakdown"]; ok {
		t.Fatalf("breakdown should be omitted from files-mode JSON: %s", data)
	}
}

func TestContextRetrieval_RelevanceBreakdownAddsUpForAdditiveSignals(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := svc.Store.WriteMainlineView(queryGoldenRegressionView(time.Now().Add(-45 * 24 * time.Hour).UTC().Format(time.RFC3339))); err != nil {
		t.Fatalf("write view: %v", err)
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "query",
		Query: "context score relevance retrieval",
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	ri := mustFindRelevant(t, res, "int_context_scoring")
	assertBreakdownExplainsScore(t, ri.Relevance)
}

func TestContextRetrieval_RelevanceBreakdownExplainsLineageBoost(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	sealedAt := time.Now().Add(-45 * 24 * time.Hour).UTC().Format(time.RFC3339)
	view := &domain.MainlineView{
		SchemaVersion: 1,
		MainBranch:    "main",
		RebuiltAt:     sealedAt,
		Intents: []domain.IntentView{
			queryGoldenIntent("int_lineage_new", sealedAt,
				"Parquet schema replacement",
				"Current parquet schema decision.",
				"Schema retrieval should return the current decision first.",
				[]domain.Decision{
					{Point: "parquet schema", Chose: "Use the replacement schema.", Rationale: "It is the current decision."},
				},
				nil,
				nil,
				nil,
				[]string{"internal/storage/parquet.go"},
				[]string{"storage"},
			),
			queryGoldenIntent("int_lineage_old", sealedAt,
				"Old binary format",
				"Earlier binary format was replaced.",
				"Kept only as lineage context.",
				nil,
				nil,
				nil,
				&domain.StatusEvidence{SupersededByIntent: "int_lineage_new"},
				[]string{"internal/storage/old_binary.go"},
				[]string{"storage"},
			),
		},
	}
	if err := svc.Store.WriteMainlineView(view); err != nil {
		t.Fatalf("write view: %v", err)
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "query",
		Query: "parquet schema",
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	old := mustFindRelevant(t, res, "int_lineage_old")
	if old.Relevance.Breakdown == nil {
		t.Fatalf("expected query-mode breakdown")
	}
	if old.Relevance.Breakdown.Lineage == 0 {
		t.Fatalf("expected lineage boost for superseded intent included by returned superseder, got %+v", old.Relevance.Breakdown)
	}
	assertBreakdownExplainsScore(t, old.Relevance)
}

func TestContextRetrieval_RelevanceBreakdownExplainsStatusPenalty(t *testing.T) {
	sealedAt := time.Now().Add(-45 * 24 * time.Hour).UTC().Format(time.RFC3339)
	iv := queryGoldenIntent("int_superseded_auth", sealedAt,
		"Auth hardening",
		"Auth validation changed.",
		"Auth flow needs deterministic checks.",
		[]domain.Decision{
			{Point: "auth validation", Chose: "Validate auth claims.", Rationale: "Requests need deterministic checks."},
		},
		nil,
		nil,
		&domain.StatusEvidence{SupersededByIntent: "int_current_auth"},
		[]string{"internal/auth/jwt.go"},
		[]string{"auth"},
	)
	score, reasons, breakdown := scoreIntentRelevance(iv, nil, "auth validation", "")
	if len(reasons) == 0 {
		t.Fatalf("expected relevance reasons")
	}
	relevance := ContextRelevance{
		Score:     round2(score),
		Breakdown: optionalRelevanceBreakdown(true, breakdown),
		Reasons:   reasons,
	}
	if relevance.Breakdown == nil || relevance.Breakdown.StatusPenalty == 0 {
		t.Fatalf("expected status penalty for superseded intent, got %+v", relevance.Breakdown)
	}
	assertBreakdownExplainsScore(t, relevance)
}

func queryGoldenRegressionView(sealedAt string) *domain.MainlineView {
	return &domain.MainlineView{
		SchemaVersion: 1,
		MainBranch:    "main",
		RebuiltAt:     sealedAt,
		Intents: []domain.IntentView{
			queryGoldenIntent("int_ack_constraints", sealedAt,
				"Ack inherited constraints and anti_patterns",
				"Context retrieval must surface inherited constraints and anti_patterns before editing.",
				"Agents need a visible acknowledgement path for inherited constraints.",
				[]domain.Decision{
					{Point: "ack constraint flow", Chose: "Carry acknowledged_constraints in the seal summary.", Rationale: "Inherited constraints need explicit confirmation."},
				},
				[]string{"Dropping inherited constraints would hide prior safety rules."},
				[]domain.AntiPattern{
					{What: "Skipping inherited constraint acknowledgement in anti_patterns areas", Why: "Future agents must preserve high-severity constraints.", Severity: "high"},
				},
				nil,
				[]string{"internal/engine/context_retrieval.go"},
				[]string{"engine"},
			),
			queryGoldenIntent("int_context_scoring", sealedAt,
				"Context score relevance retrieval observability",
				"Add query_debug and relevance breakdown to context query retrieval JSON.",
				"Agents need score debug for context retrieval tuning.",
				[]domain.Decision{
					{Point: "context score breakdown", Chose: "Expose relevance breakdown without changing retrieval ranking.", Rationale: "PR1 measures existing behavior."},
				},
				[]string{"Changing the score formula would alter retrieval ordering."},
				[]domain.AntiPattern{
					{What: "Treating relevance breakdown as a retrieval quality fix", Why: "PR1 only exposes measurement; recall changes belong in PR2.", Severity: "medium"},
				},
				nil,
				[]string{"internal/engine/context_retrieval.go"},
				[]string{"engine"},
			),
			queryGoldenIntent("int_supersession_new", sealedAt,
				"Supersession ranking property chain",
				"Replacement property chain keeps superseder above superseded history.",
				"Supersession ranking must preserve the current decision before older context.",
				[]domain.Decision{
					{Point: "property chain", Chose: "Enforce supersession ranking after score sort.", Rationale: "Lineage order is a hard property."},
				},
				nil,
				nil,
				nil,
				[]string{"internal/engine/context_retrieval.go"},
				[]string{"engine"},
			),
			queryGoldenIntent("int_supersession_old", sealedAt,
				"Old supersession ranking property chain",
				"Earlier property chain ranked old superseded history too high.",
				"Kept only as lineage context.",
				nil,
				nil,
				nil,
				&domain.StatusEvidence{SupersededByIntent: "int_supersession_new"},
				[]string{"internal/engine/context_retrieval.go"},
				[]string{"engine"},
			),
			queryGoldenIntent("int_jwt_auth", sealedAt,
				"JWT auth hardening",
				"Implement JWT auth validation for request middleware.",
				"Auth flow needs deterministic token checks.",
				[]domain.Decision{
					{Point: "auth token validation", Chose: "Validate JWT claims before request handling."},
				},
				nil,
				nil,
				nil,
				[]string{"internal/auth/jwt.go"},
				[]string{"auth"},
			),
			queryGoldenIntent("int_subsystem_inheritance", sealedAt,
				"Do not reintroduce subsystem inheritance",
				"不要重新引入 subsystem 继承; subsystem inheritance was removed from retrieval.",
				"Subsystem inheritance made ranking harder to audit.",
				[]domain.Decision{
					{Point: "subsystem inheritance", Chose: "Keep subsystem matching path-derived only."},
				},
				nil,
				[]domain.AntiPattern{
					{What: "不要重新引入 subsystem 继承", Why: "It would expand PR1 beyond observability.", Severity: "high"},
				},
				nil,
				[]string{"internal/engine/conflict.go"},
				[]string{"engine"},
			),
		},
	}
}

func queryGoldenIntent(id, sealedAt, title, what, why string, decisions []domain.Decision, risks []string, antiPatterns []domain.AntiPattern, evidence *domain.StatusEvidence, files []string, subsystems []string) domain.IntentView {
	status := domain.StatusMerged
	statusEvidence := domain.StatusEvidence{}
	if evidence != nil {
		status = domain.StatusSuperseded
		statusEvidence = *evidence
	}
	return domain.IntentView{
		IntentID:       id,
		Status:         status,
		ActorID:        "agent",
		Thread:         "query-golden",
		GitBranch:      "query-golden",
		Goal:           title,
		SealedAt:       sealedAt,
		ViewRebuiltAt:  sealedAt,
		StatusEvidence: statusEvidence,
		Summary: &domain.IntentSummary{
			Title:        title,
			What:         what,
			Why:          why,
			Decisions:    decisions,
			Risks:        risks,
			AntiPatterns: antiPatterns,
		},
		Fingerprint: &domain.SemanticFingerprint{
			FilesTouched: files,
			Subsystems:   subsystems,
		},
	}
}

func assertNotDroppedTerm(t *testing.T, res *ContextRetrievalResult, term string) {
	t.Helper()
	if res.QueryDebug == nil {
		t.Fatalf("query_debug missing")
	}
	for _, d := range res.QueryDebug.DroppedTerms {
		if d.Term == term {
			t.Fatalf("term %q should not be dropped, got %+v", term, res.QueryDebug.DroppedTerms)
		}
	}
}

func assertEffectiveKeyword(t *testing.T, res *ContextRetrievalResult, keyword string) {
	t.Helper()
	if res.QueryDebug == nil {
		t.Fatalf("query_debug missing")
	}
	for _, kw := range res.QueryDebug.EffectiveKeywords {
		if kw == keyword {
			return
		}
	}
	t.Fatalf("expected effective keyword %q, got %+v", keyword, res.QueryDebug.EffectiveKeywords)
}

func assertExpandedTerms(t *testing.T, res *ContextRetrievalResult, term string, want []string) {
	t.Helper()
	if res.QueryDebug == nil {
		t.Fatalf("query_debug missing")
	}
	got, ok := res.QueryDebug.ExpandedTerms[term]
	if !ok {
		t.Fatalf("expected expanded terms for %q, got %+v", term, res.QueryDebug.ExpandedTerms)
	}
	if len(got) != len(want) {
		t.Fatalf("expanded terms for %q length mismatch: got %+v want %+v", term, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expanded terms for %q mismatch: got %+v want %+v", term, got, want)
		}
	}
}

func mustFindRelevant(t *testing.T, res *ContextRetrievalResult, intentID string) ContextRelevant {
	t.Helper()
	for _, ri := range res.RelevantIntents {
		if ri.IntentID == intentID {
			return ri
		}
	}
	t.Fatalf("expected intent %s in result, got %+v", intentID, res.RelevantIntents)
	return ContextRelevant{}
}

func indexOfRelevant(res *ContextRetrievalResult, intentID string) int {
	for i, ri := range res.RelevantIntents {
		if ri.IntentID == intentID {
			return i
		}
	}
	return -1
}

func assertBreakdownExplainsScore(t *testing.T, rel ContextRelevance) {
	t.Helper()
	if rel.Breakdown == nil {
		t.Fatalf("expected relevance breakdown")
	}
	b := rel.Breakdown
	sum := b.File + b.Subsystem + b.Title + b.Summary + b.Decision +
		b.Risk + b.Followup + b.AntiPattern + b.Recency + b.SameThread + b.Lineage -
		b.StatusPenalty
	if math.Abs(round2(sum)-rel.Score) > 0.001 {
		t.Fatalf("breakdown should explain rounded score; sum=%.2f score=%.2f breakdown=%+v",
			round2(sum), rel.Score, b)
	}
}
