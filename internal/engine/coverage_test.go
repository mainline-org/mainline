package engine

import (
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestCoverageWindow_CoveredViaSealedIntent(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Seed a merged intent — Merge writes the commit_note onto the
	// merge commit, which is exactly what coverage classifies as covered.
	intentID, _ := seedMergedIntent(t, dir, svc, "covered-test", "covered.go")
	gitCmd(t, dir, "checkout", "main")
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	view, _ := svc.Store.ReadMainlineView()
	cfg, _ := svc.Store.ReadTeamConfig()
	cov, err := svc.CoverageWindow(20, view, cfg)
	if err != nil {
		t.Fatalf("CoverageWindow: %v", err)
	}

	var foundCovered bool
	for _, c := range cov {
		if c.State == CoverageCovered {
			for _, id := range c.IntentIDs {
				if id == intentID {
					foundCovered = true
				}
			}
		}
	}
	if !foundCovered {
		t.Fatalf("expected intent %s to mark its merge commit covered; coverage=%+v", intentID, cov)
	}
}

func TestCoverageWindow_SkippedViaTrailer(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	gitCmd(t, dir, "checkout", "main")
	writeFile(t, dir, "skip_me.go", "package main\n")
	gitCmd(t, dir, "add", "skip_me.go")
	gitCmd(t, dir, "commit", "-m", "chore: bump version to 0.3.1\n\nMainline-Skip: routine version bump")

	view, _ := svc.Store.ReadMainlineView()
	cfg, _ := svc.Store.ReadTeamConfig()
	if view == nil {
		view = &domain.MainlineView{}
	}
	cov, err := svc.CoverageWindow(5, view, cfg)
	if err != nil {
		t.Fatalf("CoverageWindow: %v", err)
	}

	if len(cov) == 0 {
		t.Fatalf("expected at least one commit, got 0")
	}
	head := cov[0]
	if head.State != CoverageSkipped {
		t.Fatalf("expected head to be skipped, got %s (subject=%q)", head.State, head.Subject)
	}
	if head.SkipReason != "routine version bump" {
		t.Fatalf("expected reason 'routine version bump', got %q", head.SkipReason)
	}
}

func TestCoverageWindow_SkippedViaConfigPattern(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	gitCmd(t, dir, "checkout", "main")
	writeFile(t, dir, "format.go", "package main\n")
	gitCmd(t, dir, "add", "format.go")
	gitCmd(t, dir, "commit", "-m", "chore: format codebase")

	cfg, _ := svc.Store.ReadTeamConfig()
	cfg.Mainline.Skip.Patterns = []string{"^chore: format"}
	svc.Store.WriteTeamConfig(cfg)

	view, _ := svc.Store.ReadMainlineView()
	if view == nil {
		view = &domain.MainlineView{}
	}
	cov, err := svc.CoverageWindow(5, view, cfg)
	if err != nil {
		t.Fatalf("CoverageWindow: %v", err)
	}

	head := cov[0]
	if head.State != CoverageSkipped {
		t.Fatalf("expected head to be skipped via pattern, got %s", head.State)
	}
	if head.SkipReason != "matched config pattern: ^chore: format" {
		t.Fatalf("unexpected reason: %q", head.SkipReason)
	}
}

func TestCoverageWindow_UncoveredFromManualCommit(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	gitCmd(t, dir, "checkout", "main")
	writeFile(t, dir, "manual.go", "package main\n")
	gitCmd(t, dir, "add", "manual.go")
	gitCmd(t, dir, "commit", "-m", "manual edit, no intent")

	view, _ := svc.Store.ReadMainlineView()
	cfg, _ := svc.Store.ReadTeamConfig()
	if view == nil {
		view = &domain.MainlineView{}
	}
	// Empty out default skip patterns so we isolate the uncovered case.
	cfg.Mainline.Skip.Patterns = nil
	cov, err := svc.CoverageWindow(5, view, cfg)
	if err != nil {
		t.Fatalf("CoverageWindow: %v", err)
	}

	head := cov[0]
	if head.State != CoverageUncovered {
		t.Fatalf("expected head to be uncovered, got %s", head.State)
	}
}

func TestCoverageWindow_EmptyTrailerReasonRejected(t *testing.T) {
	// The Mainline-Skip trailer with an empty/whitespace reason MUST NOT
	// classify as skipped — empty reasons are a thoughtless rubber stamp.
	got := SkipReasonFromMessage("subject\n\nMainline-Skip:")
	if got != "" {
		t.Fatalf("expected empty reason to be rejected, got %q", got)
	}
	got = SkipReasonFromMessage("subject\n\nMainline-Skip:    ")
	if got != "" {
		t.Fatalf("expected whitespace-only reason to be rejected, got %q", got)
	}
	got = SkipReasonFromMessage("subject\n\nMainline-Skip: real reason")
	if got != "real reason" {
		t.Fatalf("expected 'real reason', got %q", got)
	}
}

func TestCoverageWindow_CoveredOverridesSkipPattern(t *testing.T) {
	// Priority test: when a commit BOTH has a sealed-intent note AND
	// matches a skip pattern, covered wins. Sealed claim is a stronger
	// fact than a config-driven skip.
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}
	intentID, _ := seedMergedIntent(t, dir, svc, "priority-test", "p.go")
	gitCmd(t, dir, "checkout", "main")
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	cfg, _ := svc.Store.ReadTeamConfig()
	// Add a wide pattern that would also match — covered must win.
	cfg.Mainline.Skip.Patterns = []string{".*"}
	svc.Store.WriteTeamConfig(cfg)
	cfg, _ = svc.Store.ReadTeamConfig()

	view, _ := svc.Store.ReadMainlineView()
	cov, _ := svc.CoverageWindow(20, view, cfg)
	for _, c := range cov {
		for _, id := range c.IntentIDs {
			if id == intentID {
				if c.State != CoverageCovered {
					t.Fatalf("expected covered to win over wildcard skip pattern, got %s", c.State)
				}
				return
			}
		}
	}
	t.Fatalf("did not find merge commit for intent %s in coverage window", intentID)
}
