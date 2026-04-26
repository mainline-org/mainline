//go:build !quick

package engine

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/mainline-org/mainline/internal/domain"
)

// scorePartialAgainstFingerprint must always land in [0, 1] regardless
// of input shape — partial fingerprints participate in the same
// downstream threshold comparisons as full ones, and a >1 score would
// trigger spurious always-fires conflicts.
func TestPropertyPartialFingerprintScoreBounded(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		alphabet := []string{
			"engine", "merge", "sync", "auth", "db", "cli", "core",
			"a.go", "b.go", "c.go", "internal/x.go", "test/y.go",
			"refactor", "bugfix", "security",
		}
		drawSlice := func(label string) []string {
			n := rapid.IntRange(0, 5).Draw(rt, label+":n")
			out := make([]string, 0, n)
			for i := 0; i < n; i++ {
				out = append(out, rapid.SampledFrom(alphabet).Draw(rt, label))
			}
			return out
		}
		p := &domain.PartialFingerprint{
			FilesTouched: drawSlice("p.files"),
			Keywords:     drawSlice("p.keywords"),
			Subsystems:   drawSlice("p.subs"),
		}
		fp := &domain.SemanticFingerprint{
			Subsystems:          drawSlice("fp.subs"),
			FilesTouched:        drawSlice("fp.files"),
			ArchitecturalClaims: drawSlice("fp.arch"),
			BehavioralChanges:   drawSlice("fp.beh"),
			Tags:                drawSlice("fp.tags"),
		}
		s := scorePartialAgainstFingerprint(p, fp)
		if s < 0 || s > 1+1e-9 {
			rt.Fatalf("partial overlap out of [0,1]: %f", s)
		}
	})
}

// PartialFingerprintFromDraft must be deterministic for the same draft
// — sync runs against the cached draft state, so non-determinism would
// produce flickering conflict warnings between syncs.
func TestPropertyPartialFingerprintDeterministic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		nTurns := rapid.IntRange(0, 5).Draw(rt, "nTurns")
		fileVocab := []string{
			"a.go", "internal/engine/x.go", "internal/engine/y.go",
			"internal/cli/z.go", "README.md",
		}
		descVocab := []string{
			"refactor sync loop", "add jwt middleware", "extract session",
			"fix tree-hash matching", "tighten phase1 threshold",
		}
		d := &domain.DraftIntent{
			IntentID: "int_t",
			Goal:     rapid.SampledFrom(descVocab).Draw(rt, "goal"),
		}
		for i := 0; i < nTurns; i++ {
			n := rapid.IntRange(0, 3).Draw(rt, "files")
			files := make([]domain.FileChange, 0, n)
			for j := 0; j < n; j++ {
				files = append(files, domain.FileChange{
					Path: rapid.SampledFrom(fileVocab).Draw(rt, "path"),
				})
			}
			d.Turns = append(d.Turns, domain.Turn{
				Description:  rapid.SampledFrom(descVocab).Draw(rt, "desc"),
				FilesChanged: files,
			})
		}
		a := PartialFingerprintFromDraft(d)
		b := PartialFingerprintFromDraft(d)
		if !sliceEq(a.FilesTouched, b.FilesTouched) ||
			!sliceEq(a.Keywords, b.Keywords) ||
			!sliceEq(a.Subsystems, b.Subsystems) {
			rt.Fatalf("non-deterministic partial fingerprint:\n  a=%+v\n  b=%+v", a, b)
		}
	})
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
