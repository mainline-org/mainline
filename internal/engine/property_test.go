//go:build !quick

package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"

	"pgregory.net/rapid"

	"github.com/mainline-org/mainline/internal/core"
	"github.com/mainline-org/mainline/internal/domain"
)

// -----------------------------------------------------------
// Helpers shared by engine-level rapid PBTs.
// -----------------------------------------------------------

// viewSignature collapses a MainlineView into a stable, time-independent
// representation suitable for equality comparison across syncs.
//
// We deliberately strip RebuiltAt and ViewRebuiltAt because both are populated
// from core.Now() on every rebuild and would otherwise mask the actual
// content equality we want to assert.
func viewSignature(v *domain.MainlineView) string {
	if v == nil {
		return "nil"
	}
	type intentSig struct {
		IntentID       string
		Status         domain.IntentStatus
		StatusEvidence domain.StatusEvidence
		ActorID        string
		Thread         string
		GitBranch      string
		Goal           string
		BaseCommit     string
		CodeCommit     string
	}
	intents := make([]intentSig, 0, len(v.Intents))
	for _, iv := range v.Intents {
		intents = append(intents, intentSig{
			IntentID:       iv.IntentID,
			Status:         iv.Status,
			StatusEvidence: iv.StatusEvidence,
			ActorID:        iv.ActorID,
			Thread:         iv.Thread,
			GitBranch:      iv.GitBranch,
			Goal:           iv.Goal,
			BaseCommit:     iv.BaseCommit,
			CodeCommit:     iv.CodeCommit,
		})
	}
	sort.Slice(intents, func(i, j int) bool {
		return intents[i].IntentID < intents[j].IntentID
	})
	body := struct {
		SchemaVersion int
		MainBranch    string
		MainHead      string
		Intents       []intentSig
	}{v.SchemaVersion, v.MainBranch, v.MainHead, intents}
	h, _ := core.CanonicalHash(body)
	return h
}

// seedMergedIntent moved to reconcile_test.go so it is reachable from
// non-PBT (-tags quick) builds too.

// -----------------------------------------------------------
// Sync idempotency
// -----------------------------------------------------------

// Sync must be idempotent: rebuilding the view N times in a row over the same
// underlying git state must produce the same view content (modulo timestamps).
// Otherwise a quiet `mainline sync` could change what downstream commands see.
func TestPropertySyncIdempotent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		nMerged := rapid.IntRange(0, 3).Draw(rt, "nMerged")
		nProposed := rapid.IntRange(0, 2).Draw(rt, "nProposed")
		nResyncs := rapid.IntRange(2, 5).Draw(rt, "nResyncs")

		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}

		for i := 0; i < nMerged; i++ {
			seedMergedIntent(rt, dir, svc,
				fmt.Sprintf("idem-m-%d-%s", i, randomTestString(3)),
				fmt.Sprintf("m_%d.go", i))
		}
		for i := 0; i < nProposed; i++ {
			branch := fmt.Sprintf("feature/idem-p-%d-%s", i, randomTestString(3))
			gitCmd(rt, dir, "checkout", "main")
			gitCmd(rt, dir, "checkout", "-b", branch)
			start, _ := svc.Start("proposed", "")
			fname := fmt.Sprintf("p_%d.go", i)
			writeFile(rt, dir, fname, "package main\n")
			gitCmd(rt, dir, "add", fname)
			gitCmd(rt, dir, "commit", "-m", "p")
			svc.Append("p")
			sr := validSealResult(start.IntentID)
			data, _ := json.Marshal(sr)
			svc.SealSubmit(json.RawMessage(data))
		}
		gitCmd(rt, dir, "checkout", "main")

		if _, err := svc.Sync(); err != nil {
			rt.Fatalf("first sync: %v", err)
		}
		v1, _ := svc.Store.ReadMainlineView()
		sig1 := viewSignature(v1)

		for i := 0; i < nResyncs; i++ {
			if _, err := svc.Sync(); err != nil {
				rt.Fatalf("resync %d: %v", i, err)
			}
			vN, _ := svc.Store.ReadMainlineView()
			if sig := viewSignature(vN); sig != sig1 {
				rt.Fatalf("sync not idempotent at iteration %d:\n first=%s\n  now=%s", i, sig1, sig)
			}
		}
	})
}

// -----------------------------------------------------------
// Reconcile idempotency
// -----------------------------------------------------------

// Auto-pin (the v0.2 successor to the rc4 reconcile concept) is
// allowed to mutate state on the first sync after a note goes missing
// — it writes a catch-up note for any merged-but-undeclared intent
// — but every subsequent sync with no new commits must be a no-op.
// Otherwise we risk piling up duplicate notes every time
// `mainline sync` runs.
//
// To exercise the catch-up path, we deliberately wipe the merge note
// that Service.Merge writes, demoting the intents from "merged" back
// to "proposed" in the rebuilt view; the next sync's auto-pin must
// re-attach a note on the first call and stay quiet thereafter.
//
// Pre-v0.2 this test called svc.Pin() explicitly, but Pin now runs
// automatically inside Sync (via cfg.Sync.AutoPinAfterSync). Asserting
// against the explicit Pin() return value would always read 0 because
// Sync already pinned everything before Pin's loop runs. Read
// SyncResult.AutoPinned instead — that IS what users observe.
func TestPropertyReconcileIdempotent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		nMerged := rapid.IntRange(1, 3).Draw(rt, "nMerged")

		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}

		mergeCommits := make([]string, 0, nMerged)
		for i := 0; i < nMerged; i++ {
			_, mc := seedMergedIntent(rt, dir, svc,
				fmt.Sprintf("rec-%d-%s", i, randomTestString(3)),
				fmt.Sprintf("r_%d.go", i))
			mergeCommits = append(mergeCommits, mc)
		}
		gitCmd(rt, dir, "checkout", "main")

		// Strip the merge notes so auto-pin actually has work to do.
		for _, mc := range mergeCommits {
			if _, err := svc.Git.Run("notes", "--ref=mainline/intents", "remove", mc); err != nil {
				rt.Fatalf("remove note %s: %v", mc, err)
			}
		}

		// First sync: auto-pin must re-attach all nMerged notes.
		first, err := svc.Sync()
		if err != nil {
			rt.Fatalf("first sync: %v", err)
		}
		if len(first.AutoPinned) != nMerged {
			rt.Errorf("first sync auto-pin expected %d, got %d (links=%v)",
				nMerged, len(first.AutoPinned), first.AutoPinned)
		}

		// Second sync: nothing more to pin.
		second, err := svc.Sync()
		if err != nil {
			rt.Fatalf("second sync: %v", err)
		}
		if len(second.AutoPinned) != 0 {
			rt.Errorf("second sync must be no-op, got %d auto-pinned (links=%v)",
				len(second.AutoPinned), second.AutoPinned)
		}

		// Third sync: still 0.
		third, _ := svc.Sync()
		if len(third.AutoPinned) != 0 {
			rt.Errorf("third sync must also be no-op, got %d", len(third.AutoPinned))
		}
	})
}

// -----------------------------------------------------------
// Replay consistency
// -----------------------------------------------------------

// Replay consistency: deleting the materialized view file and re-running Sync
// must reconstruct an equivalent view. The view is a cache; the actor log +
// git notes are the source of truth, and that invariant should hold for any
// state the engine can produce.
func TestPropertyReplayConsistency(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		nMerged := rapid.IntRange(0, 3).Draw(rt, "nMerged")
		nProposed := rapid.IntRange(0, 2).Draw(rt, "nProposed")

		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}

		for i := 0; i < nMerged; i++ {
			seedMergedIntent(rt, dir, svc,
				fmt.Sprintf("rep-m-%d-%s", i, randomTestString(3)),
				fmt.Sprintf("rm_%d.go", i))
		}
		for i := 0; i < nProposed; i++ {
			branch := fmt.Sprintf("feature/rep-p-%d-%s", i, randomTestString(3))
			gitCmd(rt, dir, "checkout", "main")
			gitCmd(rt, dir, "checkout", "-b", branch)
			start, _ := svc.Start("proposed", "")
			fname := fmt.Sprintf("rp_%d.go", i)
			writeFile(rt, dir, fname, "package main\n")
			gitCmd(rt, dir, "add", fname)
			gitCmd(rt, dir, "commit", "-m", "p")
			svc.Append("p")
			sr := validSealResult(start.IntentID)
			data, _ := json.Marshal(sr)
			svc.SealSubmit(json.RawMessage(data))
		}
		gitCmd(rt, dir, "checkout", "main")

		if _, err := svc.Sync(); err != nil {
			rt.Fatalf("first sync: %v", err)
		}
		original, _ := svc.Store.ReadMainlineView()
		sigOriginal := viewSignature(original)

		// Drop the cached view; the actor log + notes must be enough to
		// reconstruct the same content.
		empty := &domain.MainlineView{
			SchemaVersion: 1,
			MainBranch:    "main",
		}
		if err := svc.Store.WriteMainlineView(empty); err != nil {
			rt.Fatalf("blank view: %v", err)
		}
		if _, err := svc.Sync(); err != nil {
			rt.Fatalf("replay sync: %v", err)
		}
		replayed, _ := svc.Store.ReadMainlineView()
		if sig := viewSignature(replayed); sig != sigOriginal {
			rt.Fatalf("replay produced different view:\n original=%s\n replayed=%s", sigOriginal, sig)
		}
	})
}

// -----------------------------------------------------------
// Revert note shape
// -----------------------------------------------------------

// When a manual commit on main carries a commit_note with a non-empty
// `reverts` field, a subsequent Sync must mark the listed intents as reverted
// and record the reverting commit hash. This guarantees the revert pathway in
// scanMainNotes round-trips through real notes — not just synthetic structs.
func TestPropertyRevertNoteShape(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		nReverted := rapid.IntRange(1, 3).Draw(rt, "nReverted")

		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}

		ids := make([]string, 0, nReverted)
		for i := 0; i < nReverted; i++ {
			id, _ := seedMergedIntent(rt, dir, svc,
				fmt.Sprintf("rv-%d-%s", i, randomTestString(3)),
				fmt.Sprintf("rv_%d.go", i))
			ids = append(ids, id)
		}
		gitCmd(rt, dir, "checkout", "main")
		svc.Sync()

		// Make a revert commit on main.
		writeFile(rt, dir, "revert.go", "package main\n// revert\n")
		gitCmd(rt, dir, "add", "revert.go")
		gitCmd(rt, dir, "commit", "-m", "revert previous intents")
		revertCommit, _ := svc.Git.HeadCommit()

		identity, _ := svc.Store.ReadIdentity()
		note := domain.CommitNote{
			SchemaVersion: 1,
			Kind:          "mainline.commit_note",
			Reverts:       ids,
			AddedAt:       core.Now(),
			AddedBy:       identity.ActorID,
			Via:           "manual",
		}
		noteJSON, _ := json.Marshal(note)
		if err := svc.Git.NotesAdd(revertCommit, string(noteJSON)); err != nil {
			rt.Fatalf("write revert note: %v", err)
		}

		if _, err := svc.Sync(); err != nil {
			rt.Fatalf("sync after revert: %v", err)
		}
		view, _ := svc.Store.ReadMainlineView()
		seen := make(map[string]domain.IntentView, len(view.Intents))
		for _, iv := range view.Intents {
			seen[iv.IntentID] = iv
		}
		for _, id := range ids {
			iv, ok := seen[id]
			if !ok {
				rt.Errorf("reverted intent %s missing from view", id)
				continue
			}
			if iv.Status != domain.StatusReverted {
				rt.Errorf("intent %s expected reverted, got %s", id, iv.Status)
			}
			if iv.StatusEvidence.RevertedMainCommit != revertCommit {
				rt.Errorf("intent %s revert commit %s != %s",
					id, iv.StatusEvidence.RevertedMainCommit, revertCommit)
			}
		}
	})
}

// -----------------------------------------------------------
// ID-generator concurrency
// -----------------------------------------------------------

// GenerateIntentID, GenerateActorID and GenerateTurnID feed crypto/rand and
// must remain collision-free under heavy concurrent calls. A race on the
// generator would silently fold two different intents into one mainline view
// row.
func TestPropertyIDGeneratorsConcurrentlyUnique(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		nGoroutines := rapid.IntRange(2, 8).Draw(rt, "nGoroutines")
		perGoroutine := rapid.IntRange(20, 100).Draw(rt, "perGoroutine")

		var wg sync.WaitGroup
		intents := make([][]string, nGoroutines)
		actors := make([][]string, nGoroutines)
		turns := make([][]string, nGoroutines)
		for g := 0; g < nGoroutines; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				is := make([]string, perGoroutine)
				as := make([]string, perGoroutine)
				ts := make([]string, perGoroutine)
				for i := 0; i < perGoroutine; i++ {
					is[i] = core.GenerateIntentID()
					as[i] = core.GenerateActorID()
					ts[i] = core.GenerateTurnID()
				}
				intents[g] = is
				actors[g] = as
				turns[g] = ts
			}(g)
		}
		wg.Wait()

		assertUnique := func(label string, sets [][]string) {
			seen := make(map[string]struct{}, nGoroutines*perGoroutine)
			for _, s := range sets {
				for _, id := range s {
					if _, dup := seen[id]; dup {
						rt.Fatalf("%s collision: %s", label, id)
					}
					seen[id] = struct{}{}
				}
			}
		}
		assertUnique("intent_id", intents)
		assertUnique("actor_id", actors)
		assertUnique("turn_id", turns)
	})
}

// -----------------------------------------------------------
// Note schema forward compatibility
// -----------------------------------------------------------

// A CommitNote produced by a future schema_version (with extra unknown
// fields) must still be readable by today's parser; otherwise a single
// ahead-of-its-time write on main would freeze every reader's `mainline sync`.
func TestPropertyNoteSchemaForwardCompatible(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		schemaVersion := rapid.IntRange(1, 99).Draw(rt, "schemaVersion")
		nIntents := rapid.IntRange(0, 4).Draw(rt, "nIntents")
		nReverts := rapid.IntRange(0, 3).Draw(rt, "nReverts")
		hasFutureFields := rapid.Bool().Draw(rt, "hasFutureFields")

		// Build a note manually so we can inject unknown fields a future
		// schema might add.
		intents := make([]map[string]interface{}, nIntents)
		for i := range intents {
			intents[i] = map[string]interface{}{
				"intent_id":        "int_" + randomTestString(8),
				"seal_result_hash": "sha256:" + randomTestString(16),
			}
		}
		reverts := make([]string, nReverts)
		for i := range reverts {
			reverts[i] = "int_" + randomTestString(8)
		}
		raw := map[string]interface{}{
			"schema_version": schemaVersion,
			"kind":           "mainline.commit_note",
			"intents":        intents,
			"reverts":        reverts,
			"added_at":       "2026-04-25T00:00:00Z",
			"added_by":       "actor_" + randomTestString(8),
			"via":            "merge",
		}
		if hasFutureFields {
			raw["future_field"] = "ignore me"
			raw["another_unknown"] = map[string]interface{}{"nested": 42}
		}

		data, err := json.Marshal(raw)
		if err != nil {
			rt.Fatalf("marshal: %v", err)
		}

		var parsed domain.CommitNote
		if err := json.Unmarshal(data, &parsed); err != nil {
			rt.Fatalf("unknown-field note must still parse: %v", err)
		}
		if parsed.Kind != "mainline.commit_note" {
			rt.Errorf("kind lost: %q", parsed.Kind)
		}
		if parsed.SchemaVersion != schemaVersion {
			rt.Errorf("schema_version lost: %d != %d", parsed.SchemaVersion, schemaVersion)
		}
		if len(parsed.Intents) != nIntents {
			rt.Errorf("intents length: got %d want %d", len(parsed.Intents), nIntents)
		}
		if len(parsed.Reverts) != nReverts {
			rt.Errorf("reverts length: got %d want %d", len(parsed.Reverts), nReverts)
		}
	})
}

// -----------------------------------------------------------
// Thread events append-only
// -----------------------------------------------------------

// Append must be append-only with respect to the turn log: every Append adds
// exactly one turn at the next index, and the prefix of previously-recorded
// turns is left byte-for-byte unchanged. Anything else means the engine is
// silently rewriting history.
func TestPropertyThreadEventsAppendOnly(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		nAppends := rapid.IntRange(2, 8).Draw(rt, "nAppends")

		dir, cleanup := testRepo(rt)
		defer cleanup()
		svc := NewServiceFromRoot(dir)
		if _, err := svc.Init("agent"); err != nil {
			rt.Fatalf("init: %v", err)
		}
		gitCmd(rt, dir, "checkout", "-b", "feature/append-only")
		start, err := svc.Start("append-only goal", "")
		if err != nil {
			rt.Fatalf("start: %v", err)
		}

		var prevSnapshot []domain.Turn
		for i := 0; i < nAppends; i++ {
			fname := fmt.Sprintf("a_%d.go", i)
			writeFile(rt, dir, fname, "package main\n")
			gitCmd(rt, dir, "add", fname)
			gitCmd(rt, dir, "commit", "-m", "step")

			res, err := svc.Append(fmt.Sprintf("turn %d", i))
			if err != nil {
				rt.Fatalf("append %d: %v", i, err)
			}
			if res.Index != i {
				rt.Errorf("expected index %d, got %d", i, res.Index)
			}

			turns, err := svc.Store.ReadTurns(start.IntentID)
			if err != nil {
				rt.Fatalf("read turns %d: %v", i, err)
			}
			if len(turns) != i+1 {
				rt.Fatalf("expected %d turns, got %d", i+1, len(turns))
			}
			// The previously recorded prefix must be byte-identical.
			for j, prev := range prevSnapshot {
				now, _ := json.Marshal(turns[j])
				before, _ := json.Marshal(prev)
				if string(now) != string(before) {
					rt.Fatalf("turn %d mutated at iter %d:\n before=%s\n  now=%s",
						j, i, before, now)
				}
			}
			prevSnapshot = append([]domain.Turn(nil), turns...)
		}
	})
}
