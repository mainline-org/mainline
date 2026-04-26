package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	added := svc.configureRemoteRefspecs("_mainline/actor")
	if added != nil {
		t.Errorf("no origin → expected nil added, got %v", added)
	}
}

func TestConfigureRemoteRefspecsAddsAllFourThenIsIdempotent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	if _, err := svc.Git.Run("remote", "add", "origin", "git@example.com:fake/fake.git"); err != nil {
		t.Fatalf("git remote add: %v", err)
	}

	added := svc.configureRemoteRefspecs("_mainline/actor")
	if len(added) != 4 {
		t.Fatalf("first call should add 4 refspecs (notes fetch+push, actor fetch+push), got %d: %v",
			len(added), added)
	}

	// Second call — must add nothing.
	again := svc.configureRemoteRefspecs("_mainline/actor")
	if len(again) != 0 {
		t.Errorf("second call should be no-op, got %d additions: %v", len(again), again)
	}

	// Sanity: actual git config now contains both ref namespaces.
	fetch := svc.Git.ConfigGet("remote.origin.fetch")
	push := svc.Git.ConfigGet("remote.origin.push")
	for _, want := range []string{"refs/notes/mainline", "refs/heads/_mainline/actor"} {
		if !strings.Contains(fetch, want) {
			t.Errorf("remote.origin.fetch missing %q: %s", want, fetch)
		}
		if !strings.Contains(push, want) {
			t.Errorf("remote.origin.push missing %q: %s", want, push)
		}
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
	if len(r.RefspecsAdded) != 4 {
		t.Errorf("Rewire should have added 4 refspecs, got %d", len(r.RefspecsAdded))
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
		"AgentsMDOK":        r.AgentsMDOK,
		"PRTemplateOK":      r.PRTemplateOK,
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
		t.Errorf("after --fix all four refspec OK booleans should be true: %+v", res.Setup)
	}
	if len(res.Setup.Fixed) != 4 {
		t.Errorf("--fix should report 4 refspecs added, got %d: %v",
			len(res.Setup.Fixed), res.Setup.Fixed)
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

	added := svc.configureRemoteRefspecs("_mainline/actor")
	if len(added) != 4 {
		t.Fatalf("expected 4 refspecs added on the upstream remote, got %d: %v",
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
		!strings.Contains(upstreamFetch, "refs/heads/_mainline/actor") {
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

// doctor --setup honestly reports a missing AGENTS.md by deleting the
// init-written file and re-running.
func TestDoctorSetupFlagsMissingAgentsMD(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	if err := os.Remove(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatalf("remove AGENTS.md: %v", err)
	}
	res, _ := svc.Doctor(DoctorOptions{Setup: true})
	if res.Setup.AgentsMDOK {
		t.Error("AgentsMDOK should be false after deletion")
	}
	found := false
	for _, msg := range res.Setup.Issues {
		if strings.Contains(msg, "AGENTS.md missing") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected AGENTS.md missing issue, got %v", res.Setup.Issues)
	}
}
