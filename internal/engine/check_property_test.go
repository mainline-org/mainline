//go:build !quick

package engine

import (
	"testing"

	"pgregory.net/rapid"

	"mainline/internal/domain"
)

// rapidStringSet draws a small set of short tokens. The alphabet is
// intentionally bounded so that random fingerprints share members with
// realistic probability — otherwise jaccard collapses to 0 on every
// draw and the properties never exercise the interesting branches.
func rapidStringSet(t *rapid.T, max int, label string) []string {
	n := rapid.IntRange(0, max).Draw(t, label+":n")
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, rapid.SampledFrom([]string{
			"auth", "db", "api", "ui", "core", "config", "sync", "check",
			"engine", "storage", "cli", "notes", "merge", "test",
		}).Draw(t, label))
	}
	return out
}

func rapidFingerprint(t *rapid.T, label string) *domain.SemanticFingerprint {
	return &domain.SemanticFingerprint{
		Subsystems:          rapidStringSet(t, 6, label+".subs"),
		FilesTouched:        rapidStringSet(t, 8, label+".files"),
		ArchitecturalClaims: rapidStringSet(t, 4, label+".arch"),
		BehavioralChanges:   rapidStringSet(t, 4, label+".beh"),
		Tags:                rapidStringSet(t, 4, label+".tags"),
	}
}

// FingerprintOverlap must be symmetric: who's "candidate" and who's
// "incumbent" must not change the score, otherwise reconcile/check
// flip results based on call order.
func TestPropertyFingerprintOverlapSymmetricRapid(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		a := rapidFingerprint(rt, "a")
		b := rapidFingerprint(rt, "b")
		ab := FingerprintOverlap(a, b)
		ba := FingerprintOverlap(b, a)
		if diff := ab - ba; diff > 1e-9 || diff < -1e-9 {
			rt.Fatalf("overlap not symmetric: ab=%f ba=%f", ab, ba)
		}
	})
}

// Score must lie in [0, 1] for any inputs. A breach here would let
// downstream comparisons against a [0,1] threshold misbehave silently.
func TestPropertyFingerprintOverlapBoundedRapid(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		a := rapidFingerprint(rt, "a")
		b := rapidFingerprint(rt, "b")
		s := FingerprintOverlap(a, b)
		if s < 0 || s > 1+1e-9 {
			rt.Fatalf("overlap out of [0,1]: %f", s)
		}
	})
}

// FingerprintOverlap(a, a) must hit the maximum (sum of weights for
// non-empty dimensions). Given the engine's current weights —
// subsystems .25, files .30, architecture .15, behavioral .15,
// api .10, tags .05 — and at least one non-empty dimension, the
// score is ≥ the smallest weight of any populated dimension.
//
// We verify the weaker but guaranteed property: self-overlap dominates
// any other overlap with the same a.
func TestPropertyFingerprintOverlapSelfDominates(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		a := rapidFingerprint(rt, "a")
		b := rapidFingerprint(rt, "b")
		self := FingerprintOverlap(a, a)
		other := FingerprintOverlap(a, b)
		if self < other-1e-9 {
			rt.Fatalf("self-overlap %f < cross-overlap %f", self, other)
		}
	})
}

// Adding a token shared between the two fingerprints in any single
// dimension must not decrease the overall score. Catches regressions
// where a refactor of jaccard or the weighting accidentally inverts
// a sign on one of the dimensions.
func TestPropertyFingerprintOverlapMonotonicAddingShared(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		a := rapidFingerprint(rt, "a")
		b := rapidFingerprint(rt, "b")
		base := FingerprintOverlap(a, b)

		dim := rapid.SampledFrom([]string{"sub", "file", "arch", "beh", "tag"}).Draw(rt, "dim")
		shared := "monotonic_added_token_" + rapid.StringMatching(`[a-z]{3}`).Draw(rt, "tok")
		switch dim {
		case "sub":
			a.Subsystems = append(a.Subsystems, shared)
			b.Subsystems = append(b.Subsystems, shared)
		case "file":
			a.FilesTouched = append(a.FilesTouched, shared)
			b.FilesTouched = append(b.FilesTouched, shared)
		case "arch":
			a.ArchitecturalClaims = append(a.ArchitecturalClaims, shared)
			b.ArchitecturalClaims = append(b.ArchitecturalClaims, shared)
		case "beh":
			a.BehavioralChanges = append(a.BehavioralChanges, shared)
			b.BehavioralChanges = append(b.BehavioralChanges, shared)
		case "tag":
			a.Tags = append(a.Tags, shared)
			b.Tags = append(b.Tags, shared)
		}
		after := FingerprintOverlap(a, b)
		if after < base-1e-9 {
			rt.Fatalf("adding shared token to %s decreased overlap: %f -> %f", dim, base, after)
		}
	})
}

// Either operand being an empty fingerprint (all dimensions zero-length)
// must produce a 0 score regardless of the other side. This is the
// "pre-seal candidate" guard — half-built fingerprints should not
// silently look like they overlap with anything.
func TestPropertyFingerprintOverlapEmptyIsZero(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		other := rapidFingerprint(rt, "other")
		empty := &domain.SemanticFingerprint{}
		ab := FingerprintOverlap(empty, other)
		ba := FingerprintOverlap(other, empty)
		if ab != 0 || ba != 0 {
			rt.Fatalf("empty overlap should be 0, got ab=%f ba=%f", ab, ba)
		}
	})
}

// CheckPrepare's accounting must not lose intents: every eligible
// (merged|proposed, fingerprinted, not the candidate) intent ends up
// either in the suspicious bucket or the below-threshold counter.
// Drift here would mean phase1 "below_threshold" reads as "we evaluated
// N safely" when really we silently dropped some.
func TestPropertyCheckPrepareCountsEveryEligibleIntent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		nIntents := rapid.IntRange(2, 6).Draw(rt, "nIntents")
		threshold := rapid.Float64Range(0.0, 1.0).Draw(rt, "threshold")

		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}

		cfg, _ := svc.Store.ReadTeamConfig()
		cfg.Check.Phase1Threshold = threshold
		svc.Store.WriteTeamConfig(cfg)

		// Seed the view directly with N fingerprinted intents bypassing
		// the seal flow: no merge / push needed and runs in milliseconds.
		view := &domain.MainlineView{
			SchemaVersion: 1,
			MainBranch:    "main",
		}
		for i := 0; i < nIntents; i++ {
			fp := rapidFingerprint(rt, "fp")
			id := "int_" + rapid.StringMatching(`[a-f0-9]{8}`).Draw(rt, "id")
			view.Intents = append(view.Intents, domain.IntentView{
				IntentID:    id,
				Status:      domain.StatusProposed,
				ActorID:     "actor",
				Goal:        "g",
				Fingerprint: fp,
			})
		}
		svc.Store.WriteMainlineView(view)

		candidate := view.Intents[0]
		pkg, err := svc.CheckPrepare(candidate.IntentID)
		if err != nil {
			rt.Fatalf("CheckPrepare: %v", err)
		}

		// Every other intent (nIntents - 1 of them, candidate itself
		// excluded) is eligible because we forced status=proposed and
		// gave each a fingerprint above. So the buckets must add up
		// to nIntents-1.
		eligible := nIntents - 1
		if pkg.Phase1.SuspiciousPairs+pkg.Phase1.BelowThreshold != eligible {
			rt.Fatalf("counts mismatch: suspicious=%d below=%d eligible=%d (threshold=%f)",
				pkg.Phase1.SuspiciousPairs, pkg.Phase1.BelowThreshold,
				eligible, threshold)
		}
		if len(pkg.JudgmentTasks) != pkg.Phase1.SuspiciousPairs {
			rt.Fatalf("len(JudgmentTasks)=%d != SuspiciousPairs=%d",
				len(pkg.JudgmentTasks), pkg.Phase1.SuspiciousPairs)
		}
	})
}

// Threshold = 0 must classify every eligible intent as suspicious;
// threshold = 1 must classify none. These are the load-bearing edges
// of the predicate that decides whether phase2 fires.
func TestPropertyCheckPrepareThresholdEdges(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		nIntents := rapid.IntRange(2, 5).Draw(rt, "nIntents")

		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}

		view := &domain.MainlineView{SchemaVersion: 1, MainBranch: "main"}
		for i := 0; i < nIntents; i++ {
			fp := rapidFingerprint(rt, "fp")
			view.Intents = append(view.Intents, domain.IntentView{
				IntentID:    "int_" + rapid.StringMatching(`[a-f0-9]{8}`).Draw(rt, "id"),
				Status:      domain.StatusProposed,
				ActorID:     "actor",
				Goal:        "g",
				Fingerprint: fp,
			})
		}
		svc.Store.WriteMainlineView(view)
		candidate := view.Intents[0]

		cfg, _ := svc.Store.ReadTeamConfig()
		eligible := nIntents - 1

		// threshold = 0: every eligible pair surfaces as a judgment task.
		cfg.Check.Phase1Threshold = 0.0
		svc.Store.WriteTeamConfig(cfg)
		pkg, _ := svc.CheckPrepare(candidate.IntentID)
		if pkg.Phase1.SuspiciousPairs != eligible {
			rt.Errorf("threshold 0: suspicious=%d, want %d", pkg.Phase1.SuspiciousPairs, eligible)
		}
		if pkg.Phase1.BelowThreshold != 0 {
			rt.Errorf("threshold 0: below=%d, want 0", pkg.Phase1.BelowThreshold)
		}

		// threshold > 1: no pair can ever exceed it; everything is below.
		cfg.Check.Phase1Threshold = 1.5
		svc.Store.WriteTeamConfig(cfg)
		pkg, _ = svc.CheckPrepare(candidate.IntentID)
		if pkg.Phase1.SuspiciousPairs != 0 {
			rt.Errorf("threshold 1.5: suspicious=%d, want 0", pkg.Phase1.SuspiciousPairs)
		}
		if pkg.Phase1.BelowThreshold != eligible {
			rt.Errorf("threshold 1.5: below=%d, want %d",
				pkg.Phase1.BelowThreshold, eligible)
		}
	})
}

// CheckPrepare must sort judgment tasks newest-overlap-first. A
// regression here demotes the most-likely conflicts under cheap noise
// in the agent's downstream prompt window.
func TestPropertyCheckPrepareTasksSortedByOverlap(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		nIntents := rapid.IntRange(3, 8).Draw(rt, "nIntents")

		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}

		view := &domain.MainlineView{SchemaVersion: 1, MainBranch: "main"}
		for i := 0; i < nIntents; i++ {
			fp := rapidFingerprint(rt, "fp")
			view.Intents = append(view.Intents, domain.IntentView{
				IntentID:    "int_" + rapid.StringMatching(`[a-f0-9]{8}`).Draw(rt, "id"),
				Status:      domain.StatusProposed,
				ActorID:     "actor",
				Goal:        "g",
				Fingerprint: fp,
			})
		}
		svc.Store.WriteMainlineView(view)

		cfg, _ := svc.Store.ReadTeamConfig()
		cfg.Check.Phase1Threshold = 0.0
		svc.Store.WriteTeamConfig(cfg)

		pkg, err := svc.CheckPrepare(view.Intents[0].IntentID)
		if err != nil {
			rt.Fatalf("CheckPrepare: %v", err)
		}
		for i := 1; i < len(pkg.JudgmentTasks); i++ {
			if pkg.JudgmentTasks[i-1].FingerprintOverlapScore <
				pkg.JudgmentTasks[i].FingerprintOverlapScore {
				rt.Fatalf("tasks not sorted desc by overlap: %v",
					pkg.JudgmentTasks)
			}
		}
	})
}
