package engine

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

// helperTB is the minimal *testing.T surface used by test helpers in this
// package. It exists so the rapid PBTs in property_test.go can pass a tiny
// adapter wrapping *rapid.T into the same setup helpers.
type helperTB interface {
	Helper()
	Fatal(args ...interface{})
	Fatalf(format string, args ...interface{})
}

// testRepo creates a temporary git repository and returns (path, cleanup).
func testRepo(t helperTB) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "mainline-test-*")
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() { os.RemoveAll(dir) }

	// git init + initial commit
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "checkout", "-b", "main"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			cleanup()
			t.Fatalf("setup cmd %v: %s %v", c, out, err)
		}
	}

	// Create initial commit
	f := filepath.Join(dir, "README.md")
	os.WriteFile(f, []byte("# test\n"), 0o644)
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = dir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = dir
	cmd.Run()

	return dir, cleanup
}

func TestInitAndStatus(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	initialHead := svc.Git.ReadRef("refs/heads/main")
	if initialHead == "" {
		t.Fatal("test repo should have a main HEAD before init")
	}

	// Status before init
	st, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Initialized {
		t.Error("should not be initialized")
	}

	// Init
	result, err := svc.Init("test-agent")
	if err != nil {
		t.Fatal(err)
	}
	if result.ActorID == "" {
		t.Error("actor ID should not be empty")
	}
	if result.ActorName != "test-agent" {
		t.Errorf("expected actor name test-agent, got %s", result.ActorName)
	}

	// Status after init
	st, err = svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Initialized {
		t.Error("should be initialized")
	}
	cfg, err := svc.Store.ReadTeamConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mainline.Coverage.BaselineCommit != initialHead {
		t.Errorf("expected init to record pre-init main HEAD as coverage baseline: got %s, want %s",
			cfg.Mainline.Coverage.BaselineCommit, initialHead)
	}
	if cfg.Agent.Autonomy != AgentAutonomyHandoff {
		t.Errorf("expected init to write agent autonomy %q, got %q", AgentAutonomyHandoff, cfg.Agent.Autonomy)
	}
	if cfg.Agent.MaxAutonomy != AgentAutonomyReview {
		t.Errorf("expected missing max autonomy to backfill to %q, got %q", AgentAutonomyReview, cfg.Agent.MaxAutonomy)
	}
	st, err = svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.AgentAuthority == nil {
		t.Fatal("status should include agent authority")
	}
	if got, want := st.AgentAuthority.Effective.Autonomy, AgentAutonomyHandoff; got != want {
		t.Fatalf("status effective autonomy: got %q want %q", got, want)
	}

	// Bare double init should still fail instead of silently doing work.
	_, err = svc.Init("")
	if err == nil {
		t.Error("double init should fail")
	}
}

func TestInitCanUpdateExistingIdentityActorName(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	initResult, err := svc.Init("default-agent")
	if err != nil {
		t.Fatal(err)
	}
	headBefore, err := svc.Git.HeadCommit()
	if err != nil {
		t.Fatal(err)
	}

	updateResult, err := svc.Init("z2z23n0")
	if err != nil {
		t.Fatal(err)
	}
	if updateResult.Created {
		t.Fatal("identity update should not report a fresh init")
	}
	if !updateResult.IdentityUpdated {
		t.Fatal("identity update should report IdentityUpdated=true")
	}
	if updateResult.ActorID != initResult.ActorID {
		t.Fatalf("actor ID should stay stable, got %q want %q", updateResult.ActorID, initResult.ActorID)
	}
	if updateResult.ActorName != "z2z23n0" {
		t.Fatalf("actor name: got %q want %q", updateResult.ActorName, "z2z23n0")
	}
	headAfter, err := svc.Git.HeadCommit()
	if err != nil {
		t.Fatal(err)
	}
	if headAfter != headBefore {
		t.Fatalf("identity update should not create a commit, before=%s after=%s", headBefore, headAfter)
	}
	id, err := svc.Store.ReadIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if id.ActorName != "z2z23n0" {
		t.Fatalf("stored actor name: got %q want %q", id.ActorName, "z2z23n0")
	}
	localCfg, err := svc.Store.ReadLocalConfig()
	if err != nil {
		t.Fatal(err)
	}
	if localCfg.Actor.Name != "z2z23n0" {
		t.Fatalf("local config actor name: got %q want %q", localCfg.Actor.Name, "z2z23n0")
	}
}

func TestInitReportsProgress(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	var messages []string
	if _, err := svc.InitWithOptions("test-agent", InitOptions{
		Progress: func(message string) {
			messages = append(messages, message)
		},
	}); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"creating Mainline directories",
		"writing repository config",
		"writing local actor identity",
		"configuring git refs",
		"staging setup files",
		"committing setup files",
		"building local Mainline view",
	}
	for _, needle := range want {
		found := false
		for _, got := range messages {
			if got == needle {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("progress messages missing %q; got %#v", needle, messages)
		}
	}
}

func TestStartAndAppend(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("test-agent")

	// Start intent
	result, err := svc.Start("implement feature X", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.IntentID == "" {
		t.Error("intent ID should not be empty")
	}
	if result.Goal != "implement feature X" {
		t.Error("goal mismatch")
	}

	// Append turn
	appendResult, err := svc.Append("added module Y")
	if err != nil {
		t.Fatal(err)
	}
	if appendResult.IntentID != result.IntentID {
		t.Error("turn should be for same intent")
	}
	if appendResult.Index != 0 {
		t.Errorf("first turn should be index 0, got %d", appendResult.Index)
	}

	// Status should show active intent
	st, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.ActiveIntent == nil {
		t.Fatal("should have active intent")
	}
	if st.ActiveIntent.IntentID != result.IntentID {
		t.Error("active intent ID mismatch")
	}
}

// First-touch UX guard: a literal-following user runs `git commit`
// BEFORE `mainline start`. Without the merge-base base-commit logic,
// the draft's BaseCommit equals HEAD, the diff at seal --prepare is
// empty, and seal --submit rejects on empty fingerprint.files_touched.
//
// This test pins the fix: when HEAD is ahead of synced main at start
// time, BaseCommit is the merge-base, so subsequent prepare's diff
// includes the pre-start commits.
func TestStartAfterCommit_BaseIsMergeBaseNotHead(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("trial-user"); err != nil {
		t.Fatal(err)
	}

	// Branch off main, commit, THEN start — the trial-user footgun.
	gitCmd(t, dir, "checkout", "main")
	mainHead, _ := svc.Git.HeadCommit()
	gitCmd(t, dir, "checkout", "-b", "feature/before-start")
	writeFile(t, dir, "x.go", "package main\n")
	gitCmd(t, dir, "add", "x.go")
	gitCmd(t, dir, "commit", "-m", "Add x.go")

	res, err := svc.Start("Add x", "")
	if err != nil {
		t.Fatal(err)
	}

	draft, err := svc.Store.ReadDraft(res.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	if draft.BaseCommit == "" {
		t.Fatal("BaseCommit must be set")
	}
	// The base must not be HEAD (HEAD is the post-commit state); it
	// should be the merge-base, which on this fresh branch equals the
	// main HEAD captured before the branch was created.
	headAfterCommit, _ := svc.Git.HeadCommit()
	if draft.BaseCommit == headAfterCommit {
		t.Errorf("BaseCommit should be the merge-base with main, not the post-commit HEAD: base=%s head=%s",
			draft.BaseCommit, headAfterCommit)
	}
	if draft.BaseCommit != mainHead {
		t.Errorf("BaseCommit should equal main HEAD on a fresh branch with one commit: got %s, want %s",
			draft.BaseCommit, mainHead)
	}
}

func TestStartIdempotent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("test-agent")

	r1, err := svc.Start("goal 1", "")
	if err != nil {
		t.Fatal(err)
	}

	// Second start should return same intent (idempotent)
	r2, err := svc.Start("goal 2", "")
	if err != nil {
		t.Fatal(err)
	}
	if r1.IntentID != r2.IntentID {
		t.Error("idempotent start should return same intent")
	}
	if r2.Goal != "goal 1" {
		t.Error("should return original goal")
	}
}

func TestContextMergedRecentSortedByActivity(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("test-agent")

	view := &domain.MainlineView{
		SchemaVersion: 1,
		MainBranch:    "main",
		MainHead:      "head",
		Intents: []domain.IntentView{
			{
				IntentID:      "int_old",
				Status:        domain.StatusMerged,
				Goal:          "old",
				SealedAt:      "2026-04-25T10:00:00Z",
				ViewRebuiltAt: "2026-04-25T10:00:00Z",
			},
			{
				IntentID:      "int_new",
				Status:        domain.StatusMerged,
				Goal:          "new",
				SealedAt:      "2026-04-26T10:00:00Z",
				ViewRebuiltAt: "2026-04-26T10:00:00Z",
			},
			{
				IntentID:      "int_mid",
				Status:        domain.StatusMerged,
				Goal:          "mid",
				SealedAt:      "2026-04-25T20:00:00Z",
				ViewRebuiltAt: "2026-04-25T20:00:00Z",
			},
		},
	}
	if err := svc.Store.WriteMainlineView(view); err != nil {
		t.Fatal(err)
	}

	ctx, err := svc.Context()
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx.MergedRecent) != 3 {
		t.Fatalf("expected 3 merged intents, got %d", len(ctx.MergedRecent))
	}
	got := []string{ctx.MergedRecent[0].IntentID, ctx.MergedRecent[1].IntentID, ctx.MergedRecent[2].IntentID}
	want := []string{"int_new", "int_mid", "int_old"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("merged_recent order mismatch: got %v want %v", got, want)
		}
	}
}

func TestSealPrepareAndSubmit(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("test-agent")

	startResult, _ := svc.Start("implement feature X", "")
	svc.Append("initial implementation")

	// Seal prepare
	pkg, err := svc.SealPrepare("")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Intent.ID != startResult.IntentID {
		t.Error("prepare package intent ID mismatch")
	}
	if pkg.Kind != "mainline.seal.prepare" {
		t.Error("wrong kind")
	}
	if pkg.SchemaVersion != 3 {
		t.Errorf("prepare schema version = %d, want 3", pkg.SchemaVersion)
	}
	if pkg.SealResultSchema == nil {
		t.Fatal("seal_result_schema must be present")
	}
	if len(pkg.SealResultSchema.Summary.Decisions) != 1 {
		t.Fatalf("schema should show one summary.decisions[] object shape, got %+v",
			pkg.SealResultSchema.Summary.Decisions)
	}
	if pkg.SealResultSchema.Summary.Decisions[0].Chose == "" {
		t.Fatalf("schema should document summary.decisions[].chose")
	}
	if len(pkg.SealResultSchema.Summary.Rejected) != 1 {
		t.Fatalf("schema should show one summary.rejected[] object shape, got %+v",
			pkg.SealResultSchema.Summary.Rejected)
	}
	if pkg.SealResultSchema.Summary.Rejected[0].Alternative == "" {
		t.Fatalf("schema should document summary.rejected[].alternative")
	}
	if len(pkg.SealResultSchema.Summary.ReviewNotes) != 1 {
		t.Fatalf("schema should show one summary.review_notes[] string shape, got %+v",
			pkg.SealResultSchema.Summary.ReviewNotes)
	}
	if pkg.SealResultSchema.Summary.ReviewNotes[0] == "" {
		t.Fatalf("schema should document summary.review_notes[]")
	}

	// Starter contract: intent_id and fingerprint.files_touched are
	// pre-populated, agent-judgment fields are empty (the agent
	// fills them). This is what removes ~50% of the typing for a
	// first-touch agent.
	if pkg.Starter == nil {
		t.Fatal("seal_result_starter must be present")
	}
	if pkg.Starter.IntentID != startResult.IntentID {
		t.Errorf("starter intent_id should match draft: got %q want %q",
			pkg.Starter.IntentID, startResult.IntentID)
	}
	if pkg.Starter.Summary.Title != "" {
		t.Errorf("agent-judgment field summary.title should be empty in starter, got %q",
			pkg.Starter.Summary.Title)
	}
	if len(pkg.Starter.Summary.Decisions) != 1 {
		t.Fatalf("starter should include one blank decision object to teach array item shape, got %+v",
			pkg.Starter.Summary.Decisions)
	}
	if pkg.Starter.Summary.Decisions[0].Point != "" || pkg.Starter.Summary.Decisions[0].Chose != "" {
		t.Fatalf("starter decision placeholder should keep agent-judgment fields blank, got %+v",
			pkg.Starter.Summary.Decisions[0])
	}
	if pkg.Starter.Summary.UserGoal != "implement feature X" {
		t.Errorf("starter user_goal should mirror start goal: got %q",
			pkg.Starter.Summary.UserGoal)
	}
	// FilesTouched mirrors DiffSummary.FilesChanged so the starter
	// is consistent with what the package already documents.
	if len(pkg.Starter.Fingerprint.FilesTouched) != len(pkg.DiffSummary.FilesChanged) {
		t.Errorf("starter files_touched should mirror diff_summary.files_changed: starter=%v diff=%v",
			pkg.Starter.Fingerprint.FilesTouched, pkg.DiffSummary.FilesChanged)
	}

	// Seal submit
	sealResult := domain.SealResult{
		IntentID: startResult.IntentID,
		Summary: domain.SealSummaryInput{
			Title:    "Implement feature X",
			What:     "Added feature X to module Y",
			Why:      "Users requested feature X",
			UserGoal: "wrong execution instruction",
			Decisions: []domain.Decision{{
				Point:     "feature scope",
				Chose:     "add feature X to module Y",
				Rationale: "the test payload should satisfy the submit quality gate",
			}},
		},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:   []string{"module-y"},
			FilesTouched: []string{"y.go"},
			Tags:         []string{"feature"},
		},
		Confidence: domain.SealConfidence{Summary: 0.9, Fingerprint: 0.85},
	}
	data, _ := json.Marshal(sealResult)
	submitResult, err := svc.SealSubmit(json.RawMessage(data))
	if err != nil {
		t.Fatal(err)
	}
	if submitResult.Status != "sealed_local" {
		t.Errorf("expected sealed_local, got %s", submitResult.Status)
	}
	if submitResult.Hash == "" {
		t.Error("hash should not be empty")
	}
	view, err := svc.Store.ReadMainlineView()
	if err != nil {
		t.Fatal(err)
	}
	var found *domain.IntentView
	for i := range view.Intents {
		if view.Intents[i].IntentID == startResult.IntentID {
			found = &view.Intents[i]
			break
		}
	}
	if found == nil || found.Summary == nil {
		t.Fatalf("sealed intent should be in the view, view=%+v", view)
	}
	if found.Goal != "implement feature X" {
		t.Errorf("view goal should stay authoritative: got %q", found.Goal)
	}
	if found.Summary.UserGoal != "implement feature X" {
		t.Errorf("seal submit should canonicalize summary.user_goal from draft goal, got %q",
			found.Summary.UserGoal)
	}
}

func TestShowIntent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("test-agent")

	startResult, _ := svc.Start("goal", "")
	svc.Append("turn 1")

	result, err := svc.Show(startResult.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Intent == nil {
		t.Fatal("should have intent")
	}
	if len(result.Turns) != 1 {
		t.Errorf("expected 1 turn, got %d", len(result.Turns))
	}
}

func TestShowPrefersTerminalViewOverStaleDraft(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("test-agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	intentID, _ := seedMergedIntent(t, dir, svc, "show-view-overrides", "show_vo.go")
	gitCmd(t, dir, "checkout", "main")
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	draft, err := svc.Store.ReadDraft(intentID)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	if draft == nil {
		t.Fatal("expected local draft to exist")
	}
	// Recreate the real-world stale-cache shape: a draft file can lag
	// behind the materialized view after sync/auto-pin has already made
	// the intent terminal.
	draft.Status = domain.StatusProposed
	if err := svc.Store.WriteDraft(draft); err != nil {
		t.Fatalf("write stale draft: %v", err)
	}

	result, err := svc.Show(intentID)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if result.View == nil {
		t.Fatalf("show should use terminal view instead of stale draft; got intent=%+v", result.Intent)
	}
	if result.Intent != nil {
		t.Fatalf("show should not return stale draft when view is terminal; got intent status %s", result.Intent.Status)
	}
	if result.View.Status != domain.StatusMerged {
		t.Fatalf("expected merged view status, got %s", result.View.Status)
	}
}

func TestShowAddsMaterializedViewWithoutDroppingLocalDraft(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("test-agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	intentID, _ := seedSealedIntent(t, dir, svc, "show-proposed-view", "show_pv.go")

	result, err := svc.Show(intentID)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if result.View == nil {
		t.Fatalf("show should expose sealed materialized view; got intent=%+v", result.Intent)
	}
	if result.Intent == nil {
		t.Fatal("show should keep local draft in JSON for compatibility")
	}
	if result.View.Status != domain.StatusSealedLocal && result.View.Status != domain.StatusProposed {
		t.Fatalf("expected sealed/proposed view status, got %s", result.View.Status)
	}
	if result.View.CodeCommit == "" {
		t.Fatal("sealed view should expose code_commit")
	}
	if result.View.Summary == nil || result.View.Summary.Title != "Test Title" {
		t.Fatalf("sealed view should expose seal summary, got %+v", result.View.Summary)
	}
}

func TestShowNotFound(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("test-agent")

	_, err := svc.Show("int_nonexistent")
	if err == nil {
		t.Error("show nonexistent should fail")
	}
}

func TestShowNotFoundSurfacesSiblingWorktreeDraft(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("test-agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	linked := filepath.Join(t.TempDir(), "linked-draft")
	gitCmd(t, dir, "worktree", "add", "-b", "feature/sibling-draft", linked)
	linkedSvc := NewServiceFromRoot(linked)
	start, err := linkedSvc.Start("draft from sibling worktree", "")
	if err != nil {
		t.Fatalf("start in linked worktree: %v", err)
	}

	_, err = svc.Show(start.IntentID)
	if err == nil {
		t.Fatal("show should fail from the worktree without the draft")
	}
	mlErr, ok := err.(*domain.MainlineError)
	if !ok {
		t.Fatalf("expected MainlineError, got %T", err)
	}
	if !mlErr.Recoverable {
		t.Fatalf("sibling draft miss should be recoverable: %+v", mlErr)
	}
	if !strings.Contains(mlErr.Message, "local draft in another worktree") {
		t.Fatalf("message should identify sibling draft, got %q", mlErr.Message)
	}
	joined := strings.Join(mlErr.SuggestedActions, "\n")
	for _, want := range []string{linked, ".ml-cache/drafts/" + start.IntentID + ".json", "mainline show " + start.IntentID, "seal/publish"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("suggestions missing %q:\n%s", want, joined)
		}
	}
}

func TestLogEmpty(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("test-agent")

	result, err := svc.Log(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Intents) != 0 {
		t.Errorf("expected 0 intents, got %d", len(result.Intents))
	}
}

func TestLogWithDraft(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("test-agent")
	svc.Start("test goal", "")

	result, err := svc.Log(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Intents) != 1 {
		t.Errorf("expected 1 intent, got %d", len(result.Intents))
	}
}

func TestThreadOperations(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("test-agent")

	// Create thread
	tn, err := svc.ThreadNew("feature-x")
	if err != nil {
		t.Fatal(err)
	}
	if tn.Name != "feature-x" {
		t.Error("name mismatch")
	}

	// List threads
	threads, err := svc.ThreadList()
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 {
		t.Errorf("expected 1 thread, got %d", len(threads))
	}

	// Close thread
	if err := svc.ThreadClose("feature-x"); err != nil {
		t.Fatal(err)
	}
	threads, _ = svc.ThreadList()
	if threads[0].Status != "closed" {
		t.Error("thread should be closed")
	}
}

func TestContext(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	// Need to be in the dir for getwd
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	svc := NewServiceFromRoot(dir)
	svc.Init("test-agent")

	ctx, err := svc.Context()
	if err != nil {
		t.Fatal(err)
	}
	if ctx.RepoRoot != dir {
		t.Error("repo root mismatch")
	}
	if ctx.ActorID == "" {
		t.Error("actor ID should not be empty")
	}
}

func TestAbandon(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("test-agent")

	start, _ := svc.Start("goal", "")
	if _, err := svc.Abandon(start.IntentID, "no longer needed"); err != nil {
		t.Fatal(err)
	}

	// Drafting → abandon deletes the draft entirely (no public state
	// existed to clean), so Show returns NotFound. The status surface
	// is the absence of the intent rather than a tombstone.
	if _, err := svc.Show(start.IntentID); err == nil {
		t.Errorf("expected drafting-abandon to delete the draft, but Show still succeeds")
	}
}

func TestPublishLocalOnly(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("test-agent")

	start, _ := svc.Start("goal", "")
	svc.Append("turn")

	// Seal
	sr := domain.SealResult{
		IntentID: start.IntentID,
		Summary: domain.SealSummaryInput{
			Title: "T", What: "W", Why: "Y",
			Decisions: []domain.Decision{{
				Point:     "publish test",
				Chose:     "use a minimal valid seal payload",
				Rationale: "PublishLocalOnly tests publish behavior, not lint failures",
			}},
		},
		Fingerprint: domain.SemanticFingerprint{
			Subsystems:   []string{"s"},
			FilesTouched: []string{"f"},
		},
		Confidence: domain.SealConfidence{Summary: 0.8, Fingerprint: 0.8},
	}
	data, _ := json.Marshal(sr)
	svc.SealSubmit(json.RawMessage(data))

	// Publish (no remote)
	pub, err := svc.Publish(start.IntentID)
	if err != nil {
		t.Fatal(err)
	}
	if pub.Pushed {
		t.Error("should not push without remote")
	}
}

func TestFingerprintOverlap(t *testing.T) {
	a := &domain.SemanticFingerprint{
		Subsystems:   []string{"auth", "db"},
		FilesTouched: []string{"auth.go", "db.go"},
		Tags:         []string{"security"},
	}
	b := &domain.SemanticFingerprint{
		Subsystems:   []string{"auth", "api"},
		FilesTouched: []string{"auth.go", "api.go"},
		Tags:         []string{"security", "api"},
	}

	score := FingerprintOverlap(a, b)
	if score <= 0 {
		t.Error("overlapping fingerprints should have positive score")
	}
	if score >= 1 {
		t.Error("different fingerprints should have score < 1")
	}

	// Same fingerprint
	selfScore := FingerprintOverlap(a, a)
	if selfScore <= score {
		t.Error("self-overlap should be higher than partial overlap")
	}

	// No overlap
	c := &domain.SemanticFingerprint{
		Subsystems:   []string{"ui"},
		FilesTouched: []string{"ui.go"},
		Tags:         []string{"frontend"},
	}
	noOverlap := FingerprintOverlap(a, c)
	if noOverlap >= score {
		t.Error("no-overlap should be less than partial overlap")
	}
}

func TestFingerprintOverlapNil(t *testing.T) {
	a := &domain.SemanticFingerprint{Subsystems: []string{"x"}}
	if FingerprintOverlap(nil, a) != 0 {
		t.Error("nil fingerprint should return 0")
	}
	if FingerprintOverlap(a, nil) != 0 {
		t.Error("nil fingerprint should return 0")
	}
}

// Property: overlap(a,b) == overlap(b,a)
func TestPropertyFingerprintOverlapSymmetric(t *testing.T) {
	for i := 0; i < 50; i++ {
		a := randomFingerprint()
		b := randomFingerprint()
		ab := FingerprintOverlap(a, b)
		ba := FingerprintOverlap(b, a)
		diff := ab - ba
		if diff < -0.0001 || diff > 0.0001 {
			t.Errorf("overlap not symmetric: %f != %f", ab, ba)
		}
	}
}

// Property: overlap(a,a) >= overlap(a,b) for any b
func TestPropertyFingerprintSelfOverlapMaximal(t *testing.T) {
	for i := 0; i < 50; i++ {
		a := randomFingerprint()
		b := randomFingerprint()
		self := FingerprintOverlap(a, a)
		other := FingerprintOverlap(a, b)
		if self < other-0.0001 {
			t.Errorf("self-overlap %f < other-overlap %f", self, other)
		}
	}
}

// Property: overlap is in [0, 1]
func TestPropertyFingerprintOverlapBounded(t *testing.T) {
	for i := 0; i < 100; i++ {
		a := randomFingerprint()
		b := randomFingerprint()
		score := FingerprintOverlap(a, b)
		if score < 0 || score > 1.0001 {
			t.Errorf("overlap out of bounds: %f", score)
		}
	}
}

func randomFingerprint() *domain.SemanticFingerprint {
	subsystems := []string{"auth", "db", "api", "ui", "core", "config", "sync", "check"}
	files := []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go"}
	tags := []string{"security", "performance", "feature", "bugfix", "refactor"}

	pick := func(from []string, n int) []string {
		if n > len(from) {
			n = len(from)
		}
		var result []string
		used := make(map[int]bool)
		for len(result) < n {
			idx := randomInt(len(from))
			if !used[idx] {
				used[idx] = true
				result = append(result, from[idx])
			}
		}
		return result
	}

	return &domain.SemanticFingerprint{
		Subsystems:   pick(subsystems, randomInt(3)+1),
		FilesTouched: pick(files, randomInt(3)+1),
		Tags:         pick(tags, randomInt(2)+1),
	}
}

func randomInt(max int) int {
	if max <= 0 {
		return 0
	}
	b := make([]byte, 1)
	f, _ := os.Open("/dev/urandom")
	f.Read(b)
	f.Close()
	return int(b[0]) % max
}
