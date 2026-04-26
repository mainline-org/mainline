package engine

import (
	crypto_rand "crypto/rand"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"mainline/internal/domain"
)

// -----------------------------------------------------------
// Full lifecycle integration tests
// -----------------------------------------------------------

// TestFullLifecycleSingleAgent tests the complete intent lifecycle:
// init → start → append → seal → publish → sync → merge → reconcile
func TestFullLifecycleSingleAgent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)

	// Step 1: Init
	initResult, err := svc.Init("agent-alpha")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if initResult.ActorID == "" {
		t.Fatal("init should produce actor ID")
	}

	// Step 2: Create a feature branch and start intent
	gitCmd(t, dir, "checkout", "-b", "feature/auth")
	startResult, err := svc.Start("Implement JWT authentication", "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Logf("Started intent: %s", startResult.IntentID)

	// Step 3: Make changes and record turns
	writeFile(t, dir, "auth.go", `package auth
func Login(user, pass string) (string, error) { return "token", nil }
`)
	gitCmd(t, dir, "add", "auth.go")
	gitCmd(t, dir, "commit", "-m", "add auth module")

	appendResult, err := svc.Append("Implemented login endpoint")
	if err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if appendResult.Index != 0 {
		t.Errorf("first turn index should be 0, got %d", appendResult.Index)
	}

	writeFile(t, dir, "middleware.go", `package auth
func AuthMiddleware() {}
`)
	gitCmd(t, dir, "add", "middleware.go")
	gitCmd(t, dir, "commit", "-m", "add auth middleware")

	appendResult2, err := svc.Append("Added auth middleware")
	if err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if appendResult2.Index != 1 {
		t.Errorf("second turn index should be 1, got %d", appendResult2.Index)
	}

	// Step 4: Seal prepare
	pkg, err := svc.SealPrepare("")
	if err != nil {
		t.Fatalf("seal prepare: %v", err)
	}
	if pkg.Intent.ID != startResult.IntentID {
		t.Error("prepare package should reference the intent")
	}
	if pkg.Kind != "mainline.seal.prepare" {
		t.Errorf("wrong kind: %s", pkg.Kind)
	}

	// Step 5: Seal submit (simulate agent producing SealResult)
	sealResult := domain.SealResult{
		IntentID: startResult.IntentID,
		Summary: domain.IntentSummary{
			Title:    "Implement JWT authentication",
			What:     "Added JWT-based authentication with login endpoint and middleware",
			Why:      "Application needs secure user authentication",
			UserGoal: "Implement JWT authentication",
			Decisions: []domain.Decision{
				{Point: "Token format", Chose: "JWT", Rationale: "Industry standard"},
			},
			Risks:     []string{"Token expiry handling needs attention"},
			Followups: []string{"Add refresh token support"},
		},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:          []string{"auth", "middleware"},
			FilesTouched:        []string{"auth.go", "middleware.go"},
			ArchitecturalClaims: []string{"JWT-based authentication"},
			BehavioralChanges:   []string{"New login flow"},
			Tags:                []string{"security", "feature"},
		},
		Confidence: domain.SealConfidence{Summary: 0.95, Fingerprint: 0.9},
	}
	sealData, _ := json.Marshal(sealResult)
	submitResult, err := svc.SealSubmit(json.RawMessage(sealData))
	if err != nil {
		t.Fatalf("seal submit: %v", err)
	}
	if submitResult.Status != "sealed_local" {
		t.Errorf("expected sealed_local, got %s", submitResult.Status)
	}
	if submitResult.Hash == "" {
		t.Error("canonical hash should be set")
	}

	// Step 6: Publish (no remote, should still succeed)
	pubResult, err := svc.Publish(startResult.IntentID)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if pubResult.Pushed {
		t.Error("should not push without remote")
	}

	// Step 7: Sync (rebuild view from local events)
	syncResult, err := svc.Sync()
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if syncResult.IntentsInView == 0 {
		t.Error("view should have intents after sync")
	}

	// Step 8: Log should show the intent
	logResult, err := svc.Log(10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range logResult.Intents {
		if entry.IntentID == startResult.IntentID {
			found = true
			break
		}
	}
	if !found {
		t.Error("log should contain the intent")
	}

	// Step 9: Context should show it
	ctx, err := svc.Context()
	if err != nil {
		t.Fatal(err)
	}
	if ctx.ActorID == "" {
		t.Error("context should have actor ID")
	}

	// Step 10: Show should work
	showResult, err := svc.Show(startResult.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	if showResult.Intent == nil && showResult.View == nil {
		t.Error("show should return intent or view")
	}

	t.Log("Full lifecycle test passed")
}

// TestMultipleIntentsOnDifferentBranches tests parallel development on branches.
func TestMultipleIntentsOnDifferentBranches(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("multi-agent")

	// Create two branches with intents
	gitCmd(t, dir, "checkout", "-b", "feature/a")
	startA, err := svc.Start("Feature A", "")
	if err != nil {
		t.Fatal(err)
	}
	svc.Append("work on A")

	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "checkout", "-b", "feature/b")
	startB, err := svc.Start("Feature B", "")
	if err != nil {
		t.Fatal(err)
	}
	svc.Append("work on B")

	if startA.IntentID == startB.IntentID {
		t.Error("intent IDs should be unique")
	}

	// Both should be visible in log
	logResult, _ := svc.Log(10)
	if len(logResult.Intents) < 2 {
		t.Errorf("expected at least 2 intents, got %d", len(logResult.Intents))
	}
}

// TestSealRejectInvalidResult tests that invalid SealResult is rejected.
func TestSealRejectInvalidResult(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	start, _ := svc.Start("goal", "")

	// Missing required fields
	invalid := domain.SealResult{
		IntentID: start.IntentID,
		Summary:  domain.IntentSummary{Title: ""}, // missing What, Why
	}
	data, _ := json.Marshal(invalid)
	_, err := svc.SealSubmit(json.RawMessage(data))
	if err == nil {
		t.Error("should reject invalid SealResult")
	}
}

// TestSealAlreadySealedFails tests that sealing an already sealed intent fails.
func TestSealAlreadySealedFails(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	start, _ := svc.Start("goal", "")

	// Seal once
	sr := validSealResult(start.IntentID)
	data, _ := json.Marshal(sr)
	svc.SealSubmit(json.RawMessage(data))

	// Seal again should fail
	_, err := svc.SealSubmit(json.RawMessage(data))
	if err == nil {
		t.Error("double seal should fail")
	}
}

// TestAbandonThenStartNew tests abandoning an intent and starting a new one.
func TestAbandonThenStartNew(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	gitCmd(t, dir, "checkout", "-b", "feature/x")
	start1, _ := svc.Start("first attempt", "")
	svc.Append("some work")

	// Abandon
	err := svc.Abandon(start1.IntentID, "wrong approach")
	if err != nil {
		t.Fatal(err)
	}

	// Start new intent on same branch
	start2, err := svc.Start("second attempt", "")
	if err != nil {
		t.Fatalf("should be able to start after abandon: %v", err)
	}
	if start2.IntentID == start1.IntentID {
		t.Error("new intent should have different ID")
	}
}

// TestCheckPrepareWithOverlap tests conflict detection with overlapping intents.
func TestCheckPrepareWithOverlap(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	// Create and seal first intent
	gitCmd(t, dir, "checkout", "-b", "feature/auth")
	start1, _ := svc.Start("auth feature", "")
	writeFile(t, dir, "auth.go", "package main")
	gitCmd(t, dir, "add", "auth.go")
	gitCmd(t, dir, "commit", "-m", "add auth")
	svc.Append("auth implementation")

	sr1 := domain.SealResult{
		IntentID: start1.IntentID,
		Summary:  domain.IntentSummary{Title: "Auth", What: "auth", Why: "security"},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:   []string{"auth", "middleware"},
			FilesTouched: []string{"auth.go", "middleware.go"},
			Tags:         []string{"security"},
		},
		Confidence: domain.SealConfidence{Summary: 0.9, Fingerprint: 0.9},
	}
	data1, _ := json.Marshal(sr1)
	svc.SealSubmit(json.RawMessage(data1))
	svc.Publish(start1.IntentID)
	svc.Sync()

	// Create second overlapping intent
	gitCmd(t, dir, "checkout", "main")
	gitCmd(t, dir, "checkout", "-b", "feature/auth-v2")
	start2, _ := svc.Start("auth v2", "")
	writeFile(t, dir, "auth_v2.go", "package main")
	gitCmd(t, dir, "add", "auth_v2.go")
	gitCmd(t, dir, "commit", "-m", "auth v2")
	svc.Append("auth v2")

	sr2 := domain.SealResult{
		IntentID: start2.IntentID,
		Summary:  domain.IntentSummary{Title: "Auth v2", What: "auth v2", Why: "upgrade"},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:   []string{"auth", "middleware"}, // overlaps!
			FilesTouched: []string{"auth.go"},             // overlaps!
			Tags:         []string{"security"},
		},
		Confidence: domain.SealConfidence{Summary: 0.9, Fingerprint: 0.9},
	}
	data2, _ := json.Marshal(sr2)
	svc.SealSubmit(json.RawMessage(data2))
	svc.Publish(start2.IntentID)
	svc.Sync()

	// Check should find overlap
	checkPkg, err := svc.CheckPrepare(start2.IntentID)
	if err != nil {
		t.Fatalf("check prepare: %v", err)
	}

	if checkPkg.Phase1.SuspiciousPairs == 0 {
		t.Error("should detect overlapping fingerprints")
	}
}

// TestThreadLifecycle tests full thread lifecycle.
func TestThreadLifecycle(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	// Create thread
	_, err := svc.ThreadNew("epic/auth")
	if err != nil {
		t.Fatal(err)
	}

	// List
	threads, _ := svc.ThreadList()
	if len(threads) != 1 {
		t.Fatalf("expected 1 thread, got %d", len(threads))
	}
	if threads[0].Name != "epic/auth" {
		t.Error("name mismatch")
	}

	// Close
	svc.ThreadClose("epic/auth")
	threads, _ = svc.ThreadList()
	if threads[0].Status != "closed" {
		t.Error("should be closed")
	}
}

// TestPRDescription tests PR description generation (rc3: no trailer).
func TestPRDescription(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	start, _ := svc.Start("add feature", "")

	// rc3: pr-trailer removed, only pr-description exists
	// PR description
	desc, err := svc.PRDescription(start.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	if desc == "" {
		t.Error("description should not be empty")
	}
}

// TestCanonicalHashDeterministic tests hash consistency across calls.
func TestCanonicalHashDeterministicIntegration(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	start, _ := svc.Start("goal", "")

	h1, err := svc.CanonicalHashIntent(start.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := svc.CanonicalHashIntent(start.IntentID)
	if h1 != h2 {
		t.Error("hash should be deterministic")
	}
}

// TestStatusBeforeAndAfterInit tests status command lifecycle.
func TestStatusBeforeAndAfterInit(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)

	// Before init
	st, _ := svc.Status()
	if st.Initialized {
		t.Error("should not be initialized")
	}

	// After init
	svc.Init("agent")
	st, _ = svc.Status()
	if !st.Initialized {
		t.Error("should be initialized")
	}
	if st.Branch == "" {
		t.Error("branch should not be empty")
	}
}

// TestListProposalsEmpty tests list-proposals when empty.
func TestListProposalsEmpty(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	result, err := svc.ListProposals()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Proposals) != 0 {
		t.Error("should be empty initially")
	}
}

// TestReconcileEmpty tests reconcile when nothing to reconcile.
func TestReconcileEmpty(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	result, err := svc.Pin()
	if err != nil {
		t.Fatal(err)
	}
	if result.Pinned != 0 {
		t.Error("should reconcile 0")
	}
}

// -----------------------------------------------------------
// Property-based tests for engine
// -----------------------------------------------------------

// Property: starting N intents on N branches produces N unique IDs
func TestPropertyUniqueIntentIDs(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		branch := "feature/" + randomTestString(6)
		gitCmd(t, dir, "checkout", "main")
		gitCmd(t, dir, "checkout", "-b", branch)
		result, err := svc.Start("goal "+randomTestString(4), "")
		if err != nil {
			t.Fatal(err)
		}
		if seen[result.IntentID] {
			t.Fatalf("duplicate intent ID: %s", result.IntentID)
		}
		seen[result.IntentID] = true
	}
}

// Property: every append increments turn index sequentially
func TestPropertyTurnIndexSequential(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	svc.Start("goal", "")

	for i := 0; i < 10; i++ {
		result, err := svc.Append("turn " + randomTestString(3))
		if err != nil {
			t.Fatal(err)
		}
		if result.Index != i {
			t.Errorf("expected index %d, got %d", i, result.Index)
		}
	}
}

// Sealed-local intents may still be abandoned per the state machine
// (sealed_local → abandoned is a valid transition).
func TestSealedLocalCanBeAbandoned(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	start, _ := svc.Start("goal", "")

	sr := validSealResult(start.IntentID)
	data, _ := json.Marshal(sr)
	svc.SealSubmit(json.RawMessage(data))

	if err := svc.Abandon(start.IntentID, "reason"); err != nil {
		t.Errorf("sealed_local → abandoned should be valid: %v", err)
	}
}

// Merged intents must not be abandoned: the only legal transition out of
// merged is → reverted.
func TestPropertyCannotAbandonMerged(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	gitCmd(t, dir, "checkout", "-b", "feature/no-abandon-merged")
	start, _ := svc.Start("merged then abandon", "")
	writeFile(t, dir, "x.go", "package main\n")
	gitCmd(t, dir, "add", "x.go")
	gitCmd(t, dir, "commit", "-m", "x")
	svc.Append("work")

	sr := validSealResult(start.IntentID)
	data, _ := json.Marshal(sr)
	if _, err := svc.SealSubmit(json.RawMessage(data)); err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := svc.Merge(start.IntentID); err != nil {
		t.Fatalf("merge: %v", err)
	}

	if err := svc.Abandon(start.IntentID, "should fail"); err == nil {
		t.Error("merged → abandoned must be rejected")
	}
}

// -----------------------------------------------------------
// Helpers
// -----------------------------------------------------------

func gitCmd(t helperTB, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s %v", args, out, err)
	}
}

func writeFile(t helperTB, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func validSealResult(intentID string) domain.SealResult {
	return domain.SealResult{
		IntentID: intentID,
		Summary: domain.IntentSummary{
			Title: "Test Title",
			What:  "Test what",
			Why:   "Test why",
		},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:   []string{"test"},
			FilesTouched: []string{"test.go"},
		},
		Confidence: domain.SealConfidence{Summary: 0.9, Fingerprint: 0.9},
	}
}

func randomTestString(n int) string {
	b := make([]byte, n)
	crypto_rand.Read(b)
	for i := range b {
		b[i] = 'a' + b[i]%26
	}
	return string(b)
}
