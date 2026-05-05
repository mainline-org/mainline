package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

// configureRemoteRefspecs is the canonical place where notes / actor-log
// fetch+push refspecs land in `git config`. The pre-MVP bug: it ran
// only once inside Init, and silently did nothing if origin was missing
// at init time. These tests pin the post-fix invariants.

func TestConfigureRemoteRefspecsNoopWithoutOrigin(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	added := svc.configureRemoteRefspecs(domain.DefaultActorLogPrefix)
	if added != nil {
		t.Errorf("no origin → expected nil added, got %v", added)
	}
}

func TestConfigureRemoteRefspecsAddsSetupRefspecsThenIsIdempotent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	if _, err := svc.Git.Run("remote", "add", "origin", "git@example.com:fake/fake.git"); err != nil {
		t.Fatalf("git remote add: %v", err)
	}

	added := svc.configureRemoteRefspecs(domain.DefaultActorLogPrefix)
	if len(added) != 5 {
		t.Fatalf("first call should add 5 refspec entries (notes fetch+push, actor fetch+push, legacy fetch), got %d: %v",
			len(added), added)
	}

	// Second call — must add nothing.
	again := svc.configureRemoteRefspecs(domain.DefaultActorLogPrefix)
	if len(again) != 0 {
		t.Errorf("second call should be no-op, got %d additions: %v", len(again), again)
	}

	// Sanity: actual git config now contains both ref namespaces.
	fetch := svc.Git.ConfigGet("remote.origin.fetch")
	push := svc.Git.ConfigGet("remote.origin.push")
	for _, want := range []string{"refs/notes/mainline", "refs/mainline/actors"} {
		if !strings.Contains(fetch, want) {
			t.Errorf("remote.origin.fetch missing %q: %s", want, fetch)
		}
		if !strings.Contains(push, want) {
			t.Errorf("remote.origin.push missing %q: %s", want, push)
		}
	}
	if !strings.Contains(fetch, "refs/heads/_mainline/actor") {
		t.Errorf("remote.origin.fetch should keep legacy actor logs readable: %s", fetch)
	}
	if strings.Contains(push, "refs/heads/_mainline/actor") {
		t.Errorf("remote.origin.push should not push legacy branch actor logs: %s", push)
	}
}

func TestConfigureRemoteRefspecsRemovesLegacyActorPush(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	if _, err := svc.Git.Run("remote", "add", "origin", "git@example.com:fake/fake.git"); err != nil {
		t.Fatalf("git remote add: %v", err)
	}
	legacyPush := "refs/heads/" + domain.LegacyActorLogPrefix + "/*:refs/heads/" + domain.LegacyActorLogPrefix + "/*"
	if err := svc.Git.ConfigAdd("remote.origin.push", legacyPush); err != nil {
		t.Fatalf("seed legacy push refspec: %v", err)
	}

	added := svc.configureRemoteRefspecs(domain.DefaultActorLogPrefix)
	push := svc.Git.ConfigGet("remote.origin.push")
	if strings.Contains(push, legacyPush) {
		t.Fatalf("legacy actor branch push refspec should be removed, got %q", push)
	}
	if !strings.Contains(push, domain.ActorLogPushRefspec(domain.DefaultActorLogPrefix)) {
		t.Fatalf("hidden actor push refspec missing after rewrite, got %q", push)
	}
	if !strings.Contains(strings.Join(added, "\n"), "remove push: "+legacyPush) {
		t.Fatalf("rewrite should report legacy push removal, got %v", added)
	}
}

// Init followed by a later `git remote add origin` followed by
// `mainline init --rewire` should leave the same fully-wired state as
// `mainline init` would have if origin existed at init time.
func TestRewireFillsRefspecsAfterLateOrigin(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	// At this point: no origin, no refspecs.
	if got := svc.Git.ConfigGet("remote.origin.fetch"); got != "" {
		t.Fatalf("pre-condition: no remote.origin.fetch, got %q", got)
	}

	// User adds origin AFTER init.
	if _, err := svc.Git.Run("remote", "add", "origin", "git@example.com:fake/fake.git"); err != nil {
		t.Fatalf("git remote add: %v", err)
	}

	r, err := svc.Rewire()
	if err != nil {
		t.Fatalf("Rewire: %v", err)
	}
	if !r.HadRemote {
		t.Error("Rewire should report HadRemote=true after origin was added")
	}
	if len(r.RefspecsAdded) != 5 {
		t.Errorf("Rewire should have added 5 refspec entries, got %d", len(r.RefspecsAdded))
	}
}

// Rewire is safe to run on a healthy repo: nothing is broken,
// HadRemote=true, RefspecsAdded is empty.
func TestRewireIsSafeOnHealthyRepo(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Git.Run("remote", "add", "origin", "git@example.com:fake/fake.git"); err != nil {
		t.Fatalf("git remote add: %v", err)
	}
	svc.Init("agent")

	r, err := svc.Rewire()
	if err != nil {
		t.Fatalf("Rewire: %v", err)
	}
	if !r.HadRemote {
		t.Error("HadRemote should be true")
	}
	if len(r.RefspecsAdded) != 0 {
		t.Errorf("healthy repo Rewire should add 0 refspecs, got %d: %v",
			len(r.RefspecsAdded), r.RefspecsAdded)
	}
}

func TestRewirePreservesCustomPRTemplate(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	path := filepath.Join(dir, ".github", "PULL_REQUEST_TEMPLATE.md")
	custom := "## Summary\n\n## Review Checklist\n\n- Product owner approved\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir PR template dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatalf("write custom PR template: %v", err)
	}

	r, err := svc.Rewire()
	if err != nil {
		t.Fatalf("Rewire: %v", err)
	}
	if r.PRTplWritten {
		t.Fatal("Rewire should not touch PR templates")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read PR template: %v", err)
	}
	if string(data) != custom {
		t.Fatalf("custom PR template was modified:\n%s", string(data))
	}
}

// doctor --setup populates a structured report and sets the per-check
// booleans correctly for a freshly-initialised repo with origin
// configured at init time.
func TestDoctorSetupReportClean(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Git.Run("remote", "add", "origin", "git@example.com:fake/fake.git"); err != nil {
		t.Fatalf("git remote add: %v", err)
	}
	svc.Init("agent")

	res, err := svc.Doctor(DoctorOptions{Setup: true})
	if err != nil {
		t.Fatalf("Doctor --setup: %v", err)
	}
	if res.Setup == nil {
		t.Fatal("Setup report should not be nil")
	}
	r := res.Setup
	for label, ok := range map[string]bool{
		"HasRemote":         r.HasRemote,
		"NotesFetchOK":      r.NotesFetchOK,
		"NotesPushOK":       r.NotesPushOK,
		"ActorFetchOK":      r.ActorFetchOK,
		"ActorPushOK":       r.ActorPushOK,
		"NotesDisplayRefOK": r.NotesDisplayRefOK,
		"IdentityOK":        r.IdentityOK,
		"GitignoreOK":       r.GitignoreOK,
	} {
		if !ok {
			t.Errorf("expected %s=true on a healthy repo", label)
		}
	}
	if len(r.Issues) != 0 {
		t.Errorf("expected no issues on a healthy repo, got %v", r.Issues)
	}
}

func TestDoctorSetupIgnoresLegacyPRTemplate(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Git.Run("remote", "add", "origin", "git@example.com:fake/fake.git"); err != nil {
		t.Fatalf("git remote add: %v", err)
	}
	svc.Init("agent")

	path := filepath.Join(dir, ".github", "PULL_REQUEST_TEMPLATE.md")
	legacy := "Mainline-Intent: <intent-id>\nMainline-Seal: sha256:<hash>\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir PR template dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy PR template: %v", err)
	}

	res, err := svc.Doctor(DoctorOptions{Setup: true})
	if err != nil {
		t.Fatalf("Doctor --setup: %v", err)
	}
	if strings.Contains(strings.Join(res.Setup.Issues, "\n"), "deprecated Mainline trailers") {
		t.Fatalf("doctor should not report PR template issues, got %v", res.Setup.Issues)
	}
}

// --setup with --fix wires up missing refspecs in place. Verifies the
// support flow described to friends: "if doctor flags refspecs, just
// add --fix and re-run".
func TestDoctorSetupFixWiresRefspecs(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	if _, err := svc.Git.Run("remote", "add", "origin", "git@example.com:fake/fake.git"); err != nil {
		t.Fatalf("git remote add: %v", err)
	}

	// Before --fix: refspecs missing.
	res, _ := svc.Doctor(DoctorOptions{Setup: true})
	if res.Setup.NotesFetchOK || res.Setup.ActorFetchOK {
		t.Fatal("pre-condition: refspecs should be missing before --fix")
	}
	if len(res.Setup.Issues) == 0 {
		t.Fatal("pre-condition: should have issues before --fix")
	}

	res, err := svc.Doctor(DoctorOptions{Setup: true, Fix: true})
	if err != nil {
		t.Fatalf("Doctor --setup --fix: %v", err)
	}
	if !res.Setup.NotesFetchOK || !res.Setup.ActorFetchOK ||
		!res.Setup.NotesPushOK || !res.Setup.ActorPushOK {
		t.Errorf("after --fix all refspec OK booleans should be true: %+v", res.Setup)
	}
	if len(res.Setup.Fixed) != 5 {
		t.Errorf("--fix should report 5 refspec entries added, got %d: %v",
			len(res.Setup.Fixed), res.Setup.Fixed)
	}
	if strings.Contains(strings.Join(res.Setup.Issues, "\n"), "remote refspecs incomplete") {
		t.Fatalf("--fix should clear stale refspec issue after rewiring, got %v", res.Setup.Issues)
	}
}

// configureRemoteRefspecs respects the team config's remote name —
// fork-based / non-default-remote workflows must not be hardcoded to
// "origin". This pins the post-MVP fix that removed the literal
// "origin" assumption from every engine call site.
func TestConfigureRemoteRefspecsHonoursNonOriginRemote(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	// Override the team config's Remote name then add a remote with
	// that name (NOT "origin"). configureRemoteRefspecs must wire
	// the named remote, not "origin".
	cfg, _ := svc.Store.ReadTeamConfig()
	cfg.Mainline.Remote = "upstream"
	svc.Store.WriteTeamConfig(cfg)

	if _, err := svc.Git.Run("remote", "add", "upstream", "git@example.com:fake/fake.git"); err != nil {
		t.Fatalf("git remote add upstream: %v", err)
	}

	added := svc.configureRemoteRefspecs(domain.DefaultActorLogPrefix)
	if len(added) != 5 {
		t.Fatalf("expected 5 refspec entries added on the upstream remote, got %d: %v",
			len(added), added)
	}

	// origin (the default name) must be untouched — no refspec
	// configured for it.
	if got := svc.Git.ConfigGet("remote.origin.fetch"); got != "" {
		t.Errorf("origin should remain untouched, got fetch=%q", got)
	}
	// upstream must carry the mainline refspecs.
	upstreamFetch := svc.Git.ConfigGet("remote.upstream.fetch")
	if !strings.Contains(upstreamFetch, "refs/notes/mainline") ||
		!strings.Contains(upstreamFetch, "refs/mainline/actors") {
		t.Errorf("upstream.fetch should carry both mainline refspecs, got %q", upstreamFetch)
	}
}

// doctor --setup reports the configured remote name in its report so
// the CLI render and JSON consumers do not have to guess.
func TestDoctorSetupReportsRemoteName(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	cfg, _ := svc.Store.ReadTeamConfig()
	cfg.Mainline.Remote = "upstream"
	svc.Store.WriteTeamConfig(cfg)

	res, err := svc.Doctor(DoctorOptions{Setup: true})
	if err != nil {
		t.Fatalf("Doctor --setup: %v", err)
	}
	if res.Setup.RemoteName != "upstream" {
		t.Errorf("RemoteName: got %q want %q", res.Setup.RemoteName, "upstream")
	}
	if res.Setup.HasRemote {
		t.Error("HasRemote should be false — we never added an 'upstream' remote here")
	}
	// The Issues message must mention the configured remote name so
	// the user sees the right `git remote add ...` instruction.
	found := false
	for _, msg := range res.Setup.Issues {
		if strings.Contains(msg, "'upstream'") || strings.Contains(msg, "upstream") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Issues should reference the configured remote name 'upstream', got %v", res.Setup.Issues)
	}
}

func TestSyncHonoursNonOriginRemoteTrackingRefs(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	initRes, err := svc.Init("agent")
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	intentID, _ := seedSealedIntent(t, dir, svc, "upstream-only", "upstream.go")
	cfg, _ := svc.Store.ReadTeamConfig()
	localActorRef := svc.Store.ActorLogRef(initRes.ActorID, cfg.Mainline.ActorLogPrefix)

	remoteDir, err := os.MkdirTemp("", "mainline-remote-*")
	if err != nil {
		t.Fatalf("remote temp: %v", err)
	}
	defer os.RemoveAll(remoteDir)
	gitCmd(t, remoteDir, "init", "--bare")
	gitCmd(t, dir, "remote", "add", "upstream", remoteDir)

	cfg.Mainline.Remote = "upstream"
	cfg.Sync.AutoPinAfterSync = false
	if err := svc.Store.WriteTeamConfig(cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}

	gitCmd(t, dir, "checkout", "main")
	remoteMain, err := gitRunIn(t, dir, "rev-parse", "main")
	if err != nil {
		t.Fatalf("rev-parse remote main: %v", err)
	}
	remoteMain = strings.TrimSpace(remoteMain)
	gitCmd(t, dir, "push", "upstream", "main")
	gitCmd(t, dir, "push", "upstream", localActorRef+":"+localActorRef)
	gitCmd(t, dir, "update-ref", "-d", localActorRef)

	writeFile(t, dir, "local-only.txt", "not on upstream\n")
	gitCmd(t, dir, "add", "local-only.txt")
	gitCmd(t, dir, "commit", "-m", "local only")
	localMain, err := gitRunIn(t, dir, "rev-parse", "main")
	if err != nil {
		t.Fatalf("rev-parse local main: %v", err)
	}
	if strings.TrimSpace(localMain) == remoteMain {
		t.Fatal("test setup failed: local main should differ from upstream/main")
	}

	res, err := svc.Sync()
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.MainHead != remoteMain {
		t.Fatalf("sync should use upstream/main as main head, got %s want %s", res.MainHead, remoteMain)
	}

	view, _ := svc.Store.ReadMainlineView()
	for _, iv := range view.Intents {
		if iv.IntentID == intentID {
			return
		}
	}
	t.Fatalf("intent %s from upstream actor log missing from view", intentID)
}

// AGENTS.md is optional repo-level policy; doctor --setup reports its
// presence but does not raise an issue when it is absent.
func TestDoctorSetupTreatsAgentsMDAsOptional(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	res, _ := svc.Doctor(DoctorOptions{Setup: true})
	if res.Setup.AgentsMDOK {
		t.Error("AgentsMDOK should be false when repo policy was not installed")
	}
	if strings.Contains(strings.Join(res.Setup.Issues, "\n"), "AGENTS.md missing") {
		t.Errorf("AGENTS.md absence should not be an issue, got %v", res.Setup.Issues)
	}
}
