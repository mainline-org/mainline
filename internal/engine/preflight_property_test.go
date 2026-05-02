//go:build !quick

package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"pgregory.net/rapid"

	"github.com/mainline-org/mainline/internal/domain"
)

func TestPropertyPreflightAggregationDeterministic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		in := drawPreflightInput(rt)

		first := buildPreflightResult(in)
		firstJSON, _ := json.Marshal(first)
		for i := 0; i < 5; i++ {
			got := buildPreflightResult(in)
			gotJSON, _ := json.Marshal(got)
			if string(gotJSON) != string(firstJSON) {
				rt.Fatalf("preflight result flickered:\nfirst=%s\ngot=%s", firstJSON, gotJSON)
			}
		}
	})
}

func TestPropertyPreflightProposedOverlapSoundness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		current := drawPathSet(rt, "current", 5)
		proposed := drawPreflightIntents(rt, "proposed", domain.StatusProposed, 8)
		res := buildPreflightResult(preflightInput{
			status:       &StatusResult{Initialized: true, IdentityConfigured: true, LocalHead: "head", MainHead: "head"},
			currentFiles: current,
			proposed:     proposed,
		})

		want := map[string]bool{}
		for _, iv := range proposed {
			if preflightFilesOverlap(current, iv.Fingerprint.FilesTouched) {
				want[iv.IntentID] = true
			}
		}
		got := map[string]bool{}
		for _, o := range res.Overlaps {
			if o.Kind == PreflightOverlapProposed {
				got[o.IntentID] = true
			}
		}
		if !sameBoolMap(want, got) {
			rt.Fatalf("proposed overlap mismatch current=%v want=%v got=%v overlaps=%+v", current, want, got, res.Overlaps)
		}
	})
}

func TestPropertyPreflightStatusFilterExcludesTerminalNonCurrentStates(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		current := drawPathSet(rt, "current", 5)
		if len(current) == 0 {
			current = []string{"shared.go"}
		}
		statuses := []domain.IntentStatus{
			domain.StatusAbandoned,
			domain.StatusSuperseded,
			domain.StatusReverted,
		}
		var proposed []domain.IntentView
		var view []domain.IntentView
		for i, status := range statuses {
			proposed = append(proposed, preflightIntent(fmt.Sprintf("p_%d", i), status, current, ""))
			view = append(view, preflightIntent(fmt.Sprintf("m_%d", i), status, current, "new-main"))
		}
		res := buildPreflightResult(preflightInput{
			status:          &StatusResult{Initialized: true, IdentityConfigured: true, LocalHead: "local", MainHead: "new-main"},
			currentFiles:    current,
			proposed:        proposed,
			view:            &domain.MainlineView{Intents: view},
			upstreamCommits: map[string]bool{"new-main": true},
		})
		if len(res.Overlaps) != 0 {
			rt.Fatalf("abandoned/superseded/reverted must never overlap, got %+v", res.Overlaps)
		}
	})
}

func TestPropertyPreflightMergedWindowOnlyUsesLocalToSyncedMain(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		current := drawPathSet(rt, "current", 4)
		if len(current) == 0 {
			current = []string{"shared.go"}
		}
		window := drawCommitSet(rt, "window", 5)
		var intents []domain.IntentView
		want := map[string]bool{}
		n := rapid.IntRange(1, preflightOverlapLimit).Draw(rt, "n")
		for i := 0; i < n; i++ {
			inWindow := rapid.Bool().Draw(rt, fmt.Sprintf("in-window-%d", i))
			commit := fmt.Sprintf("old-%d", i)
			if inWindow && len(window) > 0 {
				commit = window[i%len(window)]
			}
			id := fmt.Sprintf("merged_%d", i)
			intents = append(intents, preflightIntent(id, domain.StatusMerged, current, commit))
			if inWindow && len(window) > 0 {
				want[id] = true
			}
		}
		res := buildPreflightResult(preflightInput{
			status:          &StatusResult{Initialized: true, IdentityConfigured: true, LocalHead: "local", MainHead: "new-main"},
			currentFiles:    current,
			view:            &domain.MainlineView{Intents: intents},
			upstreamCommits: boolSet(window),
		})
		got := map[string]bool{}
		for _, o := range res.Overlaps {
			if o.Kind == PreflightOverlapUpstreamMerged {
				got[o.IntentID] = true
			}
		}
		if !sameBoolMap(want, got) {
			rt.Fatalf("merged window mismatch window=%v want=%v got=%v overlaps=%+v", window, want, got, res.Overlaps)
		}
	})
}

func TestPropertyPreflightOutputCompactAndStable(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		current := []string{"shared.go"}
		var proposed []domain.IntentView
		for i := 0; i < preflightOverlapLimit*3; i++ {
			id := fmt.Sprintf("int_%02d", i%7) // deliberate duplicates
			proposed = append(proposed, preflightIntent(id, domain.StatusProposed, current, ""))
		}

		res := buildPreflightResult(preflightInput{
			status:       &StatusResult{Initialized: true, IdentityConfigured: true, LocalHead: "head", MainHead: "head"},
			currentFiles: current,
			proposed:     proposed,
		})
		if len(res.Overlaps) > preflightOverlapLimit {
			rt.Fatalf("overlap limit exceeded: %d > %d", len(res.Overlaps), preflightOverlapLimit)
		}
		seen := map[string]bool{}
		var keys []string
		for _, o := range res.Overlaps {
			key := o.Kind + ":" + o.IntentID
			if seen[key] {
				rt.Fatalf("duplicate overlap %s in %+v", key, res.Overlaps)
			}
			seen[key] = true
			keys = append(keys, key)
		}
		if !sort.StringsAreSorted(keys) {
			rt.Fatalf("expected stable sorted overlap keys, got %v", keys)
		}
	})
}

func drawPreflightInput(rt *rapid.T) preflightInput {
	current := drawPathSet(rt, "current", 6)
	proposed := drawPreflightIntents(rt, "proposed", domain.StatusProposed, 8)
	merged := drawPreflightIntents(rt, "merged", domain.StatusMerged, 8)
	window := drawCommitSet(rt, "window", 4)
	for i := range merged {
		if rapid.Bool().Draw(rt, fmt.Sprintf("merged-in-window-%d", i)) && len(window) > 0 {
			merged[i].StatusEvidence.MergedMainCommit = window[i%len(window)]
		} else {
			merged[i].StatusEvidence.MergedMainCommit = fmt.Sprintf("old-%d", i)
		}
	}
	levelStatus := &StatusResult{
		Initialized:        true,
		IdentityConfigured: true,
		LocalHead:          "local",
		MainHead:           "main",
		SyncStale:          rapid.Bool().Draw(rt, "sync-stale"),
	}
	return preflightInput{
		status:          levelStatus,
		currentFiles:    current,
		proposed:        proposed,
		view:            &domain.MainlineView{Intents: merged},
		upstreamCommits: boolSet(window),
	}
}

func drawPreflightIntents(rt *rapid.T, label string, status domain.IntentStatus, max int) []domain.IntentView {
	n := rapid.IntRange(0, max).Draw(rt, label+".n")
	out := make([]domain.IntentView, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s_%02d", label, i)
		out = append(out, preflightIntent(id, status, drawPathSet(rt, fmt.Sprintf("%s.files.%d", label, i), 5), ""))
	}
	return out
}

func drawPathSet(rt *rapid.T, label string, max int) []string {
	n := rapid.IntRange(0, max).Draw(rt, label+".n")
	pool := []string{
		"shared.go", "internal/engine/a.go", "internal/cli/a.go",
		"docs/readme.md", "skills/mainline/SKILL.md", "go.mod",
	}
	seen := map[string]bool{}
	var out []string
	for i := 0; i < n; i++ {
		p := rapid.SampledFrom(pool).Draw(rt, fmt.Sprintf("%s.%d", label, i))
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func drawCommitSet(rt *rapid.T, label string, max int) []string {
	n := rapid.IntRange(0, max).Draw(rt, label+".n")
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("%s-commit-%d", label, i))
	}
	return out
}

func boolSet(items []string) map[string]bool {
	out := map[string]bool{}
	for _, item := range items {
		out[item] = true
	}
	return out
}

func sameBoolMap(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if b[k] != av {
			return false
		}
	}
	return true
}
