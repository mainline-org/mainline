//go:build !quick

package engine

import (
	"fmt"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/gitops"
)

// ---------------------------------------------------------------------------
// Reference model: a trivially-correct reimplementation of findPinMatchBatched
// that we compare the real code against. Any divergence is a bug in one or the
// other — and the reference is simple enough to eyeball.
// ---------------------------------------------------------------------------

func refFindPinMatch(iv domain.IntentView, entries []gitops.LogEntry, ctx *pinContext) (string, string) {
	type check struct {
		name string
		fn   func(entry gitops.LogEntry) bool
	}
	checks := []check{
		{"tree_hash", func(e gitops.LogEntry) bool {
			if iv.CodeCommit == "" {
				return false
			}
			it := ctx.intentTreeOf[iv.CodeCommit]
			return it != "" && ctx.treeOf[e.Hash] == it
		}},
		{"commit_hash", func(e gitops.LogEntry) bool {
			return iv.CodeCommit != "" && e.Hash == iv.CodeCommit
		}},
		{"merge_parent", func(e gitops.LogEntry) bool {
			if iv.CodeCommit == "" {
				return false
			}
			parents := ctx.commitParents[e.Hash]
			if len(parents) < 2 {
				return false
			}
			for _, p := range parents[1:] {
				if p == iv.CodeCommit {
					return true
				}
			}
			return false
		}},
		{"subject", func(e gitops.LogEntry) bool {
			if iv.CodeCommit == "" {
				return false
			}
			is := ctx.intentSubjects[iv.CodeCommit]
			return is != "" && e.Subject == is
		}},
		{"branch_in_message", func(e gitops.LogEntry) bool {
			if iv.GitBranch == "" {
				return false
			}
			msg := ctx.entryMessages[e.Hash]
			return msg != "" && matchesMergeHeader(msg, iv.GitBranch)
		}},
		{"goal_text", func(e gitops.LogEntry) bool {
			if iv.Goal == "" {
				return false
			}
			msg := ctx.entryMessages[e.Hash]
			return msg != "" && strings.Contains(msg, iv.Goal)
		}},
	}
	for _, c := range checks {
		for _, entry := range entries {
			if c.fn(entry) {
				return entry.Hash, c.name
			}
		}
	}
	return "", ""
}

// ---------------------------------------------------------------------------
// Property: findPinMatchBatched == refFindPinMatch for all generated inputs.
// This subsumes: strategy priority, entry ordering, empty-field gating,
// partial/missing map keys — any behavioural divergence is caught.
// ---------------------------------------------------------------------------

func TestPropertyPinCascadeMatchesReferenceModel(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		iv := drawPinIntentView(rt)
		entries := drawPinLogEntries(rt)
		ctx := drawPinCtx(rt, iv, entries)

		gotCommit, gotStrategy := findPinMatchBatched(iv, entries, ctx)
		wantCommit, wantStrategy := refFindPinMatch(iv, entries, ctx)

		if gotCommit != wantCommit || gotStrategy != wantStrategy {
			rt.Fatalf("divergence from reference:\n  got  = (%q, %q)\n  want = (%q, %q)\n  iv.CodeCommit=%q iv.Goal=%q iv.GitBranch=%q\n  entries=%d ctx.treeOf=%d",
				gotCommit, gotStrategy, wantCommit, wantStrategy,
				iv.CodeCommit, iv.Goal, iv.GitBranch,
				len(entries), len(ctx.treeOf))
		}
	})
}

// ---------------------------------------------------------------------------
// Property: empty CodeCommit disables tree_hash, commit_hash, merge_parent,
// subject. Only branch_in_message and goal_text can match.
// ---------------------------------------------------------------------------

func TestPropertyPinEmptyCodeCommitDisablesCommitStrategies(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		iv := drawPinIntentView(rt)
		iv.CodeCommit = "" // force empty
		entries := drawPinLogEntries(rt)
		ctx := drawPinCtx(rt, iv, entries)

		_, strategy := findPinMatchBatched(iv, entries, ctx)
		if strategy != "" && strategy != "branch_in_message" && strategy != "goal_text" {
			rt.Fatalf("empty CodeCommit matched via %q (should only match branch_in_message or goal_text)", strategy)
		}
	})
}

// ---------------------------------------------------------------------------
// Property: strategy priority beats entry position.
// If tree_hash matches entry[2] and commit_hash matches entry[0],
// tree_hash still wins because it is a higher-priority strategy.
// ---------------------------------------------------------------------------

func TestPropertyPinStrategyPriorityBeatsEntryPosition(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Construct a scenario: multiple strategies match different entries
		nEntries := rapid.IntRange(2, 5).Draw(rt, "nEntries")
		entries := make([]gitops.LogEntry, nEntries)
		for i := range entries {
			entries[i] = gitops.LogEntry{
				Hash:    fmt.Sprintf("main_%d", i),
				Subject: fmt.Sprintf("subject_%d", i),
			}
		}

		codeCommit := "code_abc"
		iv := domain.IntentView{
			IntentID:   "int_test",
			CodeCommit: codeCommit,
			Goal:       fmt.Sprintf("subject_%d", 0), // matches entry[0] via goal_text
			GitBranch:  "feature/test",
			Status:     domain.StatusProposed,
		}

		// Pick which strategy should win (tree_hash or commit_hash)
		winIdx := rapid.IntRange(1, nEntries-1).Draw(rt, "winIdx")
		winStrategy := rapid.SampledFrom([]string{"tree_hash", "commit_hash"}).Draw(rt, "winStrategy")

		ctx := &pinContext{
			treeOf:         make(map[string]string),
			intentTreeOf:   make(map[string]string),
			intentSubjects: make(map[string]string),
			entryMessages:  make(map[string]string),
			commitParents:  make(map[string][]string),
		}

		// Set up goal_text to match entry[0] (lower priority strategy, earlier entry)
		ctx.entryMessages[entries[0].Hash] = entries[0].Subject

		// Set up winning strategy to match a LATER entry
		switch winStrategy {
		case "tree_hash":
			ctx.intentTreeOf[codeCommit] = "shared_tree"
			ctx.treeOf[entries[winIdx].Hash] = "shared_tree"
		case "commit_hash":
			entries[winIdx].Hash = codeCommit
		}

		gotCommit, gotStrategy := findPinMatchBatched(iv, entries, ctx)

		// The higher-priority strategy must win even though goal_text's
		// matching entry appears earlier in the list
		if gotStrategy != winStrategy {
			rt.Fatalf("expected strategy %q to win, got %q (commit=%q)", winStrategy, gotStrategy, gotCommit)
		}
	})
}

// ---------------------------------------------------------------------------
// Property: within the same strategy, the first matching entry wins.
// ---------------------------------------------------------------------------

func TestPropertyPinFirstEntryWinsWithinStrategy(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		nEntries := rapid.IntRange(2, 6).Draw(rt, "nEntries")
		entries := make([]gitops.LogEntry, nEntries)
		for i := range entries {
			entries[i] = gitops.LogEntry{
				Hash:    fmt.Sprintf("main_%d", i),
				Subject: "shared subject",
			}
		}

		codeCommit := "code_xyz"
		iv := domain.IntentView{
			IntentID:   "int_test",
			CodeCommit: codeCommit,
			Status:     domain.StatusProposed,
		}

		// All entries share the same tree → multiple tree_hash matches
		ctx := &pinContext{
			treeOf:         make(map[string]string),
			intentTreeOf:   map[string]string{codeCommit: "shared_tree"},
			intentSubjects: make(map[string]string),
			entryMessages:  make(map[string]string),
			commitParents:  make(map[string][]string),
		}
		for _, e := range entries {
			ctx.treeOf[e.Hash] = "shared_tree"
		}

		gotCommit, gotStrategy := findPinMatchBatched(iv, entries, ctx)
		if gotStrategy != "tree_hash" {
			rt.Fatalf("expected tree_hash, got %q", gotStrategy)
		}
		if gotCommit != entries[0].Hash {
			rt.Fatalf("expected first entry %q, got %q", entries[0].Hash, gotCommit)
		}
	})
}

// ---------------------------------------------------------------------------
// Property: matchesMergeHeader only matches the exact branch name from a
// canonical GitHub merge header on the FIRST line.
// ---------------------------------------------------------------------------

func TestPropertyMatchesMergeHeaderExactness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a branch name (may contain slashes, hyphens, dots)
		branchChars := "abcdefghijklmnopqrstuvwxyz0123456789-_/."
		branchLen := rapid.IntRange(1, 30).Draw(rt, "branchLen")
		var b strings.Builder
		for i := 0; i < branchLen; i++ {
			idx := rapid.IntRange(0, len(branchChars)-1).Draw(rt, fmt.Sprintf("bc%d", i))
			b.WriteByte(branchChars[idx])
		}
		branch := b.String()

		prNumber := rapid.IntRange(1, 99999).Draw(rt, "prNumber")

		// Valid header must match
		validMsg := fmt.Sprintf("Merge pull request #%d from owner/%s", prNumber, branch)
		if !matchesMergeHeader(validMsg, branch) {
			rt.Fatalf("valid header not matched: %q branch=%q", validMsg, branch)
		}

		// Valid header on second line must NOT match
		secondLine := fmt.Sprintf("some other first line\nMerge pull request #%d from owner/%s", prNumber, branch)
		if matchesMergeHeader(secondLine, branch) {
			rt.Fatalf("header on second line should not match: %q", secondLine)
		}

		// Prefix of branch should NOT match
		if len(branch) > 1 {
			prefix := branch[:len(branch)-1]
			if matchesMergeHeader(validMsg, prefix) && prefix != branch {
				rt.Fatalf("prefix %q should not match header for branch %q", prefix, branch)
			}
		}

		// Suffix extension should NOT match
		extended := branch + "-extra"
		extMsg := fmt.Sprintf("Merge pull request #%d from owner/%s", prNumber, extended)
		if matchesMergeHeader(extMsg, branch) {
			rt.Fatalf("extended branch %q should not match for branch %q", extended, branch)
		}
	})
}

// ---------------------------------------------------------------------------
// Property: partial/missing map keys gracefully skip strategies.
// If intentTreeOf is missing the code_commit key, tree_hash is skipped
// but later strategies (commit_hash etc.) still work.
// ---------------------------------------------------------------------------

func TestPropertyPinPartialContextGracefulDegradation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		codeCommit := "code_commit_abc"
		iv := domain.IntentView{
			IntentID:   "int_test",
			CodeCommit: codeCommit,
			Status:     domain.StatusProposed,
		}

		entries := []gitops.LogEntry{{Hash: codeCommit, Subject: "test commit"}}

		// Deliberately leave intentTreeOf empty → tree_hash should skip
		ctx := &pinContext{
			treeOf:         map[string]string{codeCommit: "some_tree"},
			intentTreeOf:   map[string]string{}, // missing!
			intentSubjects: make(map[string]string),
			entryMessages:  make(map[string]string),
			commitParents:  make(map[string][]string),
		}

		gotCommit, gotStrategy := findPinMatchBatched(iv, entries, ctx)
		// tree_hash should be skipped, commit_hash should catch it
		if gotStrategy != "commit_hash" {
			rt.Fatalf("expected commit_hash fallback, got %q (commit=%q)", gotStrategy, gotCommit)
		}
		if gotCommit != codeCommit {
			rt.Fatalf("expected commit %q, got %q", codeCommit, gotCommit)
		}
	})
}

// ---------------------------------------------------------------------------
// Generators
// ---------------------------------------------------------------------------

func drawPinIntentView(rt *rapid.T) domain.IntentView {
	// Bias toward empty strings to test strategy gating
	maybeEmpty := func(label string) string {
		if rapid.IntRange(0, 4).Draw(rt, label+":empty") == 0 {
			return ""
		}
		alpha := []string{
			"code_a", "code_b", "code_c", "code_d",
			"main_0", "main_1", "main_2",
		}
		return rapid.SampledFrom(alpha).Draw(rt, label)
	}

	goalAlpha := []string{
		"", "fix auth", "refactor sync", "a", "ab",
		"Merge pull request", "fix: login bug",
	}

	branchAlpha := []string{
		"", "feature/test", "fix/auth", "main",
		"feature/deep/nested/branch", "release-1.0",
	}

	return domain.IntentView{
		IntentID:   "int_gen",
		CodeCommit: maybeEmpty("cc"),
		Goal:       rapid.SampledFrom(goalAlpha).Draw(rt, "goal"),
		GitBranch:  rapid.SampledFrom(branchAlpha).Draw(rt, "branch"),
		Status:     domain.StatusProposed,
	}
}

func drawPinLogEntries(rt *rapid.T) []gitops.LogEntry {
	n := rapid.IntRange(0, 5).Draw(rt, "nEntries")
	hashAlpha := []string{
		"main_0", "main_1", "main_2", "main_3", "main_4",
		"code_a", "code_b", "code_c", "code_d",
	}
	subjectAlpha := []string{
		"fix auth", "refactor sync", "initial commit",
		"fix: login bug", "chore: format",
	}
	entries := make([]gitops.LogEntry, n)
	for i := 0; i < n; i++ {
		entries[i] = gitops.LogEntry{
			Hash:    rapid.SampledFrom(hashAlpha).Draw(rt, fmt.Sprintf("eh%d", i)),
			Subject: rapid.SampledFrom(subjectAlpha).Draw(rt, fmt.Sprintf("es%d", i)),
		}
	}
	return entries
}

func drawPinCtx(rt *rapid.T, iv domain.IntentView, entries []gitops.LogEntry) *pinContext {
	ctx := &pinContext{
		treeOf:         make(map[string]string),
		intentTreeOf:   make(map[string]string),
		intentSubjects: make(map[string]string),
		entryMessages:  make(map[string]string),
		noteCache:      make(map[string]string),
		commitParents:  make(map[string][]string),
	}

	treeAlpha := []string{"tree_x", "tree_y", "tree_z", "tree_shared"}

	// Populate maps with random coverage — sometimes present, sometimes missing
	for _, e := range entries {
		if rapid.IntRange(0, 3).Draw(rt, "treeOf:"+e.Hash) > 0 {
			ctx.treeOf[e.Hash] = rapid.SampledFrom(treeAlpha).Draw(rt, "tv:"+e.Hash)
		}
		if rapid.IntRange(0, 2).Draw(rt, "msg:"+e.Hash) > 0 {
			msgAlpha := []string{
				"fix auth", "refactor sync", "a", "ab",
				fmt.Sprintf("Merge pull request #42 from owner/%s", iv.GitBranch),
				fmt.Sprintf("Merge pull request #99 from owner/other-branch\n\n%s", iv.Goal),
				iv.Goal,
				"unrelated message",
			}
			ctx.entryMessages[e.Hash] = rapid.SampledFrom(msgAlpha).Draw(rt, "mv:"+e.Hash)
		}
		// Parents: 0-3 parents
		nParents := rapid.IntRange(0, 3).Draw(rt, "np:"+e.Hash)
		if nParents > 0 {
			parents := make([]string, nParents)
			parentAlpha := []string{"parent_0", "parent_1", "code_a", "code_b", "code_c", "code_d", iv.CodeCommit}
			for j := 0; j < nParents; j++ {
				parents[j] = rapid.SampledFrom(parentAlpha).Draw(rt, fmt.Sprintf("p:%s:%d", e.Hash, j))
			}
			ctx.commitParents[e.Hash] = parents
		}
	}

	// Intent tree / subject (sometimes missing)
	if iv.CodeCommit != "" {
		if rapid.IntRange(0, 3).Draw(rt, "intentTree") > 0 {
			ctx.intentTreeOf[iv.CodeCommit] = rapid.SampledFrom(treeAlpha).Draw(rt, "itv")
		}
		if rapid.IntRange(0, 3).Draw(rt, "intentSubj") > 0 {
			subjectAlpha := []string{
				"fix auth", "refactor sync", "initial commit",
				"fix: login bug", "chore: format",
			}
			ctx.intentSubjects[iv.CodeCommit] = rapid.SampledFrom(subjectAlpha).Draw(rt, "isv")
		}
	}

	return ctx
}
