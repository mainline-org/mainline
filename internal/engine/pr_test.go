package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestPRDescriptionIncludesMarkersAndOwnAntiPatterns(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	view := &domain.MainlineView{
		SchemaVersion: 1,
		Intents: []domain.IntentView{
			{
				IntentID: "int_prior",
				Status:   domain.StatusMerged,
				Summary: &domain.IntentSummary{
					Title: "Prior constraint",
					What:  "Recorded historical context.",
					Why:   "Future agents need the constraint in context retrieval.",
					AntiPatterns: []domain.AntiPattern{
						{What: "Do not render inherited constraints in PR output", Why: "Reviewer-facing PR output should not be flooded by historical context", Severity: "high"},
					},
				},
				Fingerprint: &domain.SemanticFingerprint{
					FilesTouched: []string{"internal/engine/pr.go"},
					Subsystems:   []string{"engine"},
				},
			},
			{
				IntentID: "int_prdesc",
				Status:   domain.StatusProposed,
				Summary: &domain.IntentSummary{
					Title: "Render PR intent",
					What:  "Added PR intent rendering.",
					Why:   "Reviewers need intent before diff.",
					AntiPatterns: []domain.AntiPattern{
						{What: "Do not require PR trailers", Why: "Metadata lives in actor refs and git notes", Severity: "high"},
					},
				},
				Fingerprint: &domain.SemanticFingerprint{
					FilesTouched: []string{"internal/engine/pr.go"},
					Subsystems:   []string{"engine"},
				},
			},
		},
	}
	if err := svc.Store.WriteMainlineView(view); err != nil {
		t.Fatalf("write view: %v", err)
	}

	desc, err := svc.PRDescription("int_prdesc")
	if err != nil {
		t.Fatalf("PRDescription: %v", err)
	}
	for _, want := range []string{
		prDescriptionStartMarker,
		prDescriptionEndMarker,
		"### Anti-patterns",
		"Do not require PR trailers",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q:\n%s", want, desc)
		}
	}
	for _, unwanted := range []string{
		"Inherited constraints considered",
		"Do not render inherited constraints in PR output",
		"Reviewer-facing PR output should not be flooded by historical context",
	} {
		if strings.Contains(desc, unwanted) {
			t.Fatalf("description unexpectedly included inherited constraint %q:\n%s", unwanted, desc)
		}
	}
}

func TestPRCommentMatchesIntentByCommitRange(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	base, err := svc.Git.HeadCommit()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	gitCmd(t, dir, "checkout", "-b", "feature/pr-comment")
	start, err := svc.Start("Generate PR intent comment", "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	writeFile(t, dir, "pr_comment.go", "package main\n")
	gitCmd(t, dir, "add", "pr_comment.go")
	gitCmd(t, dir, "commit", "-m", "feat: add pr comment")
	head, err := svc.Git.HeadCommit()
	if err != nil {
		t.Fatalf("head after commit: %v", err)
	}

	if _, err := svc.SealPrepare(""); err != nil {
		t.Fatalf("seal prepare: %v", err)
	}
	seal := validSealResult(start.IntentID)
	seal.Summary.Title = "Generate PR intent comment"
	seal.Summary.What = "Rendered Mainline intent data as a PR comment."
	seal.Summary.Why = "PR review should surface sealed intent even when the PR body lacks it."
	seal.Fingerprint.FilesTouched = []string{"pr_comment.go"}
	data, _ := json.Marshal(seal)
	if _, err := svc.SealSubmit(json.RawMessage(data)); err != nil {
		t.Fatalf("seal submit: %v", err)
	}
	if _, err := svc.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	comment, err := svc.PRComment(base, head, "feature/pr-comment")
	if err != nil {
		t.Fatalf("PRComment: %v", err)
	}
	for _, want := range []string{
		prCommentMarker,
		start.IntentID,
		"Generate PR intent comment",
		"Rendered Mainline intent data as a PR comment.",
	} {
		if !strings.Contains(comment, want) {
			t.Fatalf("comment missing %q:\n%s", want, comment)
		}
	}
}
