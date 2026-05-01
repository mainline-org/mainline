//go:build !quick

package engine

import (
	"fmt"
	"sort"
	"testing"

	"pgregory.net/rapid"

	"github.com/mainline-org/mainline/internal/domain"
)

// Property: detectSealedConflicts never includes the candidate itself.
func TestPropertyDetectSealedConflictsExcludesSelf(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		svc.Init("agent")

		candidateID := "int_self"
		fp := drawFingerprint(rt, "candidate")
		view := drawView(rt, candidateID, 5)

		pairs := svc.detectSealedConflicts(candidateID, fp, view, 0.0)
		for _, p := range pairs {
			if p.RemoteIntent == candidateID {
				rt.Fatal("detectSealedConflicts included candidate itself")
			}
		}
	})
}

// Property: detectSealedConflicts only includes intents with status
// merged or proposed. Abandoned/superseded/reverted intents are excluded.
func TestPropertyDetectSealedConflictsStatusFilter(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		svc.Init("agent")

		candidateID := "int_candidate"
		fp := &domain.SemanticFingerprint{
			Subsystems:   []string{"shared"},
			FilesTouched: []string{"shared.go"},
			Tags:         []string{"shared"},
		}

		// Build a view with intents in various statuses sharing the same fingerprint
		statuses := []domain.IntentStatus{
			domain.StatusMerged,
			domain.StatusProposed,
			domain.StatusAbandoned,
			domain.StatusSuperseded,
			domain.StatusReverted,
		}
		view := &domain.MainlineView{Intents: make([]domain.IntentView, len(statuses))}
		for i, s := range statuses {
			view.Intents[i] = domain.IntentView{
				IntentID: fmt.Sprintf("int_%d", i),
				Status:   s,
				Fingerprint: &domain.SemanticFingerprint{
					Subsystems:   []string{"shared"},
					FilesTouched: []string{"shared.go"},
					Tags:         []string{"shared"},
				},
			}
		}

		pairs := svc.detectSealedConflicts(candidateID, fp, view, 0.0)
		for _, p := range pairs {
			for i, s := range statuses {
				if p.RemoteIntent == fmt.Sprintf("int_%d", i) {
					if s != domain.StatusMerged && s != domain.StatusProposed {
						rt.Fatalf("conflict detected against %s intent (status=%s)", p.RemoteIntent, s)
					}
				}
			}
		}

		// Must have found merged and proposed (indices 0 and 1)
		found := map[string]bool{}
		for _, p := range pairs {
			found[p.RemoteIntent] = true
		}
		if !found["int_0"] || !found["int_1"] {
			rt.Fatal("failed to detect conflict with merged/proposed intents")
		}
	})
}

// Property: detectSealedConflicts respects threshold — pairs below threshold
// are excluded, pairs at or above threshold are included.
func TestPropertyDetectSealedConflictsThresholdBoundary(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		svc.Init("agent")

		candidateID := "int_candidate"
		fp := drawFingerprint(rt, "c")

		nIntents := rapid.IntRange(1, 10).Draw(rt, "nIntents")
		view := &domain.MainlineView{Intents: make([]domain.IntentView, nIntents)}
		for i := 0; i < nIntents; i++ {
			view.Intents[i] = domain.IntentView{
				IntentID:    fmt.Sprintf("int_%d", i),
				Status:      domain.StatusProposed,
				Fingerprint: drawFingerprint(rt, fmt.Sprintf("r%d", i)),
			}
		}

		threshold := rapid.Float64Range(0.01, 0.5).Draw(rt, "threshold")
		pairs := svc.detectSealedConflicts(candidateID, fp, view, threshold)

		for _, p := range pairs {
			if p.OverlapScore < threshold {
				rt.Fatalf("pair below threshold included: score=%f < threshold=%f",
					p.OverlapScore, threshold)
			}
		}

		// Verify no eligible intent above threshold was missed
		for _, iv := range view.Intents {
			if iv.IntentID == candidateID || iv.Fingerprint == nil {
				continue
			}
			score := FingerprintOverlap(fp, iv.Fingerprint)
			if score >= threshold {
				found := false
				for _, p := range pairs {
					if p.RemoteIntent == iv.IntentID {
						found = true
						break
					}
				}
				if !found {
					rt.Fatalf("eligible intent %s (score=%f >= threshold=%f) was missed",
						iv.IntentID, score, threshold)
				}
			}
		}
	})
}

// Property: detectSealedConflicts output is sorted by overlap descending.
func TestPropertyDetectSealedConflictsSortedDescending(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		svc.Init("agent")

		candidateID := "int_candidate"
		fp := drawFingerprint(rt, "c")
		view := drawView(rt, candidateID, 8)

		pairs := svc.detectSealedConflicts(candidateID, fp, view, 0.0)
		if !sort.SliceIsSorted(pairs, func(i, j int) bool {
			return pairs[i].OverlapScore > pairs[j].OverlapScore
		}) {
			rt.Fatal("pairs not sorted by overlap descending")
		}
	})
}

// Property: detectSealedConflicts with nil fingerprint or nil view returns nil.
func TestPropertyDetectSealedConflictsNilSafety(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		svc.Init("agent")

		fp := drawFingerprint(rt, "fp")
		view := drawView(rt, "int_x", 3)

		if pairs := svc.detectSealedConflicts("x", nil, view, 0.0); pairs != nil {
			rt.Fatal("nil fingerprint should return nil")
		}
		if pairs := svc.detectSealedConflicts("x", fp, nil, 0.0); pairs != nil {
			rt.Fatal("nil view should return nil")
		}
	})
}

// Property: detectSealedConflicts skips view intents with nil fingerprints.
func TestPropertyDetectSealedConflictsSkipsNilFingerprints(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		svc.Init("agent")

		candidateID := "int_candidate"
		fp := &domain.SemanticFingerprint{
			Subsystems:   []string{"shared"},
			FilesTouched: []string{"shared.go"},
		}

		view := &domain.MainlineView{
			Intents: []domain.IntentView{
				{IntentID: "int_nil_fp", Status: domain.StatusProposed, Fingerprint: nil},
				{IntentID: "int_has_fp", Status: domain.StatusProposed, Fingerprint: &domain.SemanticFingerprint{
					Subsystems: []string{"shared"}, FilesTouched: []string{"shared.go"},
				}},
			},
		}

		pairs := svc.detectSealedConflicts(candidateID, fp, view, 0.0)
		for _, p := range pairs {
			if p.RemoteIntent == "int_nil_fp" {
				rt.Fatal("nil-fingerprint intent should be skipped")
			}
		}
	})
}

// --- helpers ---

func drawFingerprint(rt *rapid.T, prefix string) *domain.SemanticFingerprint {
	subsAlpha := []string{"engine", "cli", "auth", "sync", "merge", "core", "docs"}
	fileAlpha := []string{"a.go", "b.go", "c.go", "x.go", "y.go", "internal/z.go", "README.md"}
	tagAlpha := []string{"refactor", "bugfix", "security", "perf", "breaking", "docs"}

	drawSlice := func(alpha []string, label string) []string {
		n := rapid.IntRange(1, 4).Draw(rt, prefix+"."+label+":n")
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, rapid.SampledFrom(alpha).Draw(rt, prefix+"."+label))
		}
		return out
	}

	return &domain.SemanticFingerprint{
		Subsystems:   drawSlice(subsAlpha, "subs"),
		FilesTouched: drawSlice(fileAlpha, "files"),
		Tags:         drawSlice(tagAlpha, "tags"),
	}
}

func drawView(rt *rapid.T, excludeID string, maxIntents int) *domain.MainlineView {
	n := rapid.IntRange(1, maxIntents).Draw(rt, "viewSize")
	eligibleStatuses := []domain.IntentStatus{
		domain.StatusMerged, domain.StatusProposed,
		domain.StatusAbandoned, domain.StatusSuperseded,
	}
	view := &domain.MainlineView{Intents: make([]domain.IntentView, n)}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("int_view_%d", i)
		if id == excludeID {
			id = id + "_alt"
		}
		view.Intents[i] = domain.IntentView{
			IntentID:    id,
			Status:      rapid.SampledFrom(eligibleStatuses).Draw(rt, fmt.Sprintf("status_%d", i)),
			Fingerprint: drawFingerprint(rt, fmt.Sprintf("view_%d", i)),
		}
	}
	return view
}
