package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

// sealedIntentTouching is a focused setup helper: produces a sealed
// intent whose fingerprint records the given files. Returns the
// sealed intent id; the test caller then queries trace / context
// against it.
func sealedIntentTouching(t *testing.T, dir string, svc *Service, branchSuffix string, files []string, summaryTitle string) string {
	t.Helper()
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "checkout", "-b", "feature/"+branchSuffix)
	start, err := svc.Start("ctx test "+branchSuffix, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, f := range files {
		writeFile(t, dir, f, "// "+f+"\n")
	}
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "ctx test "+branchSuffix)
	if _, err := svc.Append("did the work"); err != nil {
		t.Fatalf("append: %v", err)
	}
	sr := validSealResult(start.IntentID)
	sr.Summary.Title = summaryTitle
	sr.Fingerprint.FilesTouched = files
	sr.Fingerprint.Subsystems = subsystemsFromFiles(files)
	data, _ := json.Marshal(sr)
	if _, err := svc.SealSubmit(json.RawMessage(data)); err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	return start.IntentID
}

func TestContextRetrieval_FilesModeMatchesByPath(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	want := sealedIntentTouching(t, dir, svc, "auth-jwt",
		[]string{"src/auth/middleware.go", "src/auth/jwt.go"},
		"Move auth from session to JWT")
	_ = sealedIntentTouching(t, dir, svc, "billing",
		[]string{"src/billing/charges.go"},
		"Refactor billing fees")

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "files",
		Files: []string{"src/auth/middleware.go"},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.RelevantIntents) == 0 {
		t.Fatalf("expected at least one relevant intent for src/auth/middleware.go")
	}
	if res.RelevantIntents[0].IntentID != want {
		t.Fatalf("expected file-match intent %s ranked first, got %s",
			want, res.RelevantIntents[0].IntentID)
	}
	// Look for "touched" reason — file overlap is the strongest
	// signal and should always show up in reasons when it fires.
	found := false
	for _, r := range res.RelevantIntents[0].Relevance.Reasons {
		if strings.Contains(r, "touched") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'touched' reason in: %v", res.RelevantIntents[0].Relevance.Reasons)
	}
}

func TestContextRetrieval_QueryModeMatchesKeywords(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	want := sealedIntentTouching(t, dir, svc, "kw-test",
		[]string{"src/auth/middleware.go"},
		"Refactor authentication to JWT")
	_ = sealedIntentTouching(t, dir, svc, "kw-other",
		[]string{"src/billing/charges.go"},
		"Update billing")

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "query",
		Query: "JWT authentication refresh tokens",
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.RelevantIntents) == 0 {
		t.Fatalf("expected query match for 'JWT authentication'")
	}
	if res.RelevantIntents[0].IntentID != want {
		t.Errorf("expected keyword-match intent %s ranked first, got %s",
			want, res.RelevantIntents[0].IntentID)
	}
}

func TestContextRetrieval_OutputCompactWithLimits(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	for i := 0; i < 8; i++ {
		sealedIntentTouching(t, dir, svc,
			"fan-"+randomTestString(3),
			[]string{"src/shared/util.go"},
			"shared util change")
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "files",
		Files: []string{"src/shared/util.go"},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	// Default limit caps at ContextRetrievalDefaultLimit.
	if len(res.RelevantIntents) > ContextRetrievalDefaultLimit {
		t.Fatalf("default limit %d exceeded: got %d intents",
			ContextRetrievalDefaultLimit, len(res.RelevantIntents))
	}
	// Each entry exposes follow-up commands so the agent can drill
	// into the full record without re-deriving the command shape.
	for _, ri := range res.RelevantIntents {
		if ri.Followups["show"] == "" || ri.Followups["trace"] == "" {
			t.Errorf("intent %s missing show/trace followups", ri.IntentID)
		}
	}
}

func TestContextRetrieval_GuidanceAlwaysPresent(t *testing.T) {
	// Guidance is the rc7 honest-signal: "use these as historical
	// context, not as a replacement for reading current code".
	// Must always appear — even on empty results — so the agent
	// internalises the contract.
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "query",
		Query: "this string matches absolutely nothing useful zzzqqq",
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.Guidance) < 2 {
		t.Fatalf("expected >=2 guidance lines (use-and-verify), got %v", res.Guidance)
	}
	gtext := strings.Join(res.Guidance, " ")
	if !strings.Contains(gtext, "Verify") && !strings.Contains(gtext, "verify") {
		t.Errorf("guidance must remind the agent to verify against current code: %v", res.Guidance)
	}
}

func TestContextRetrieval_AbandonedIntentVisibleButPenalised(t *testing.T) {
	// Abandoned intents are valuable signal: "this approach was
	// tried; don't repeat it". They should appear in retrieval when
	// the file/keyword overlap is strong enough — but rank below
	// merged intents of comparable score.
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	mergedID := sealedIntentTouching(t, dir, svc, "merged-path",
		[]string{"src/shared/x.go"}, "merged approach")
	abandonedID := sealedIntentTouching(t, dir, svc, "abandoned-path",
		[]string{"src/shared/x.go"}, "abandoned approach")
	if _, err := svc.Abandon(abandonedID, "approach didn't work"); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "files",
		Files: []string{"src/shared/x.go"},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	var mergedScore, abandonedScore float64
	abandonedSeen := false
	for _, ri := range res.RelevantIntents {
		switch ri.IntentID {
		case mergedID:
			mergedScore = ri.Relevance.Score
		case abandonedID:
			abandonedScore = ri.Relevance.Score
			abandonedSeen = true
			if ri.Status != string(domain.StatusAbandoned) {
				t.Errorf("expected abandoned status in retrieval output, got %s", ri.Status)
			}
		}
	}
	if !abandonedSeen {
		t.Fatalf("abandoned intent should still appear in retrieval (signal: 'this was tried')")
	}
	if abandonedScore >= mergedScore {
		t.Errorf("abandoned intent should rank below same-relevance merged: merged=%.2f abandoned=%.2f",
			mergedScore, abandonedScore)
	}
}

func TestContextRetrieval_DraftingIntentsExcluded(t *testing.T) {
	// Drafting intents have no sealed fingerprint — they are
	// in-progress and shouldn't be surfaced as historical context.
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	gitCmd(t, dir, "checkout", "-b", "feature/in-progress")
	draftID := mustStart(t, svc, "still drafting")

	res, err := svc.RetrieveContext(ContextRetrievalRequest{
		Mode:  "query",
		Query: "still drafting",
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	for _, ri := range res.RelevantIntents {
		if ri.IntentID == draftID {
			t.Fatalf("drafting intent %s should not appear in retrieval", draftID)
		}
	}
}

func TestContextRetrieval_BadModeRejected(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	_, err := svc.RetrieveContext(ContextRetrievalRequest{Mode: "nonsense"})
	if err == nil {
		t.Fatalf("expected mode-validation error, got nil")
	}
	if !strings.Contains(err.Error(), "current") || !strings.Contains(err.Error(), "files") || !strings.Contains(err.Error(), "query") {
		t.Errorf("error should suggest the three supported modes, got: %v", err)
	}
}

func mustStart(t *testing.T, svc *Service, goal string) string {
	t.Helper()
	r, err := svc.Start(goal, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return r.IntentID
}
