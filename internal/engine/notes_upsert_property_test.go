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

// Property: after upserting any sequence of single-intent notes onto the
// same commit, the resulting note's intent set equals the union of
// IntentIDs ever upserted. The dogfood failure (intent-A overwrites
// intent-B by accident) reduces to "the union was smaller than the
// inputs"; this property would have caught it before release.
func TestPropertyUpsertNeverDropsIntents(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		nWrites := rapid.IntRange(1, 12).Draw(rt, "nWrites")
		// Bound the IntentID alphabet so we get realistic duplicate rates;
		// otherwise dedupe paths are barely exercised.
		alphabet := []string{"int_a", "int_b", "int_c", "int_d", "int_e", "int_f"}

		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}
		commit, _ := svc.Git.HeadCommit()

		expected := make(map[string]bool)
		for i := 0; i < nWrites; i++ {
			id := alphabet[rapid.IntRange(0, len(alphabet)-1).Draw(rt, fmt.Sprintf("id-%d", i))]
			expected[id] = true
			err := upsertCommitNote(svc.Git, commit, domain.CommitNote{
				Intents: []domain.IntentReference{{IntentID: id, SealResultHash: "sha256:" + id}},
				AddedAt: fmt.Sprintf("t%d", i),
				AddedBy: "actor",
				Via:     "merge",
			})
			if err != nil {
				rt.Fatalf("upsert %d: %v", i, err)
			}
		}

		raw, _ := svc.Git.NotesShow(commit)
		var got domain.CommitNote
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			rt.Fatalf("parse final note: %v", err)
		}

		gotSet := make(map[string]bool, len(got.Intents))
		for _, ref := range got.Intents {
			if gotSet[ref.IntentID] {
				rt.Errorf("duplicate intent %q in final note", ref.IntentID)
			}
			gotSet[ref.IntentID] = true
		}

		if len(gotSet) != len(expected) {
			rt.Fatalf("intent-set size %d != expected %d (got=%v want=%v)",
				len(gotSet), len(expected), sortedKeys(gotSet), sortedKeys(expected))
		}
		for id := range expected {
			if !gotSet[id] {
				rt.Errorf("intent %q dropped from final note", id)
			}
		}
	})
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
