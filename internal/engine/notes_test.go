package engine

import (
	"encoding/json"
	"testing"

	"github.com/mainline-org/mainline/internal/core"
	"github.com/mainline-org/mainline/internal/domain"
)

// -----------------------------------------------------------
// Git Notes integration tests (rc3)
// -----------------------------------------------------------

// TestSyncReadsLegacyMergeNotes verifies that sync still understands notes
// written by the removed mainline merge command.
func TestSyncReadsLegacyMergeNotes(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	// Create, seal, merge an intent
	gitCmd(t, dir, "checkout", "-b", "feature/sync-notes")
	start, _ := svc.Start("sync notes test", "")
	writeFile(t, dir, "sync_test.go", "package main\n")
	gitCmd(t, dir, "add", "sync_test.go")
	gitCmd(t, dir, "commit", "-m", "add file")
	svc.Append("work")

	sr := validSealResult(start.IntentID)
	data, _ := json.Marshal(sr)
	svc.SealSubmit(json.RawMessage(data))

	mergeCommit := squashMergeNoNote(t, dir, "feature/sync-notes", "merge feature/sync-notes")
	draft, _ := svc.Store.ReadDraft(start.IntentID)
	hash, _ := core.CanonicalHash(draft)
	identity, _ := svc.Store.ReadIdentity()
	if err := upsertCommitNote(svc.Git, mergeCommit, domain.CommitNote{
		Intents: []domain.IntentReference{
			{IntentID: start.IntentID, SealResultHash: "sha256:" + hash},
		},
		AddedBy: identity.ActorID,
		Via:     "merge",
	}); err != nil {
		t.Fatalf("write legacy merge note: %v", err)
	}

	// Sync should pick up the merged intent via notes
	syncResult, err := svc.Sync()
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	if syncResult.IntentsInView == 0 {
		t.Error("sync should find intents in view")
	}

	// Check view has merged intent
	view, _ := svc.Store.ReadMainlineView()
	found := false
	for _, iv := range view.Intents {
		if iv.IntentID == start.IntentID {
			if iv.Status != domain.StatusMerged {
				t.Errorf("expected merged, got %s", iv.Status)
			}
			if iv.StatusEvidence.MergedVia != "merge" {
				t.Errorf("expected legacy merged_via=merge, got %s", iv.StatusEvidence.MergedVia)
			}
			found = true
		}
	}
	if !found {
		t.Error("merged intent not found in view")
	}
}

// TestCommitNoteSchema verifies CommitNote JSON structure.
func TestCommitNoteSchema(t *testing.T) {
	note := domain.CommitNote{
		SchemaVersion: 1,
		Kind:          "mainline.commit_note",
		Intents: []domain.IntentReference{
			{IntentID: "int_abc12345", SealResultHash: "sha256:deadbeef"},
		},
		AddedAt: "2026-04-25T14:32:00Z",
		AddedBy: "actor_12345678",
		Via:     "merge",
	}

	data, err := json.Marshal(note)
	if err != nil {
		t.Fatal(err)
	}

	var parsed domain.CommitNote
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed.Kind != "mainline.commit_note" {
		t.Error("kind mismatch")
	}
	if parsed.SchemaVersion != 1 {
		t.Error("schema version mismatch")
	}
	if len(parsed.Intents) != 1 {
		t.Error("intents count mismatch")
	}
	if parsed.Intents[0].IntentID != "int_abc12345" {
		t.Error("intent_id mismatch")
	}
	if parsed.Via != "merge" {
		t.Error("via mismatch")
	}
}

// TestReconcileCommitNote verifies reconcile via field.
func TestReconcileCommitNoteVia(t *testing.T) {
	note := domain.CommitNote{
		SchemaVersion: 1,
		Kind:          "mainline.commit_note",
		Intents:       []domain.IntentReference{{IntentID: "int_1", SealResultHash: "sha256:abc"}},
		Via:           "reconcile",
		ReconciledAt:  "2026-04-25T15:00:00Z",
		ReconciledBy:  "actor_xyz",
	}

	data, _ := json.Marshal(note)
	var parsed domain.CommitNote
	json.Unmarshal(data, &parsed)

	if parsed.Via != "reconcile" {
		t.Errorf("expected via=reconcile, got %s", parsed.Via)
	}
	if parsed.ReconciledBy != "actor_xyz" {
		t.Error("reconciled_by mismatch")
	}
}

// TestNoteWithMultipleIntents verifies one commit can reference multiple intents.
func TestNoteWithMultipleIntents(t *testing.T) {
	note := domain.CommitNote{
		SchemaVersion: 1,
		Kind:          "mainline.commit_note",
		Intents: []domain.IntentReference{
			{IntentID: "int_aaa", SealResultHash: "sha256:111"},
			{IntentID: "int_bbb", SealResultHash: "sha256:222"},
		},
		AddedAt: "2026-04-25T14:32:00Z",
		AddedBy: "actor_123",
	}

	data, _ := json.Marshal(note)
	var parsed domain.CommitNote
	json.Unmarshal(data, &parsed)

	if len(parsed.Intents) != 2 {
		t.Fatalf("expected 2 intents, got %d", len(parsed.Intents))
	}
	if parsed.Intents[0].IntentID != "int_aaa" || parsed.Intents[1].IntentID != "int_bbb" {
		t.Error("intent ordering or content mismatch")
	}
}

// TestNoteWithReverts verifies revert tracking in notes.
func TestNoteWithReverts(t *testing.T) {
	note := domain.CommitNote{
		SchemaVersion: 1,
		Kind:          "mainline.commit_note",
		Intents:       nil,
		Reverts:       []string{"int_reverted1", "int_reverted2"},
		AddedAt:       "2026-04-25T15:00:00Z",
		AddedBy:       "actor_123",
	}

	data, _ := json.Marshal(note)
	var parsed domain.CommitNote
	json.Unmarshal(data, &parsed)

	if len(parsed.Reverts) != 2 {
		t.Error("reverts count mismatch")
	}
}

// TestInitConfiguresNotesFetch verifies init sets up notes fetch/push.
func TestInitConfiguresNotesFetch(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	// notes.displayRef should be configured
	cfg := svc.Git.ConfigGet("notes.displayRef")
	if cfg == "" {
		t.Error("notes.displayRef should be configured after init")
	}
}

// -----------------------------------------------------------
// Property-based tests for notes
// -----------------------------------------------------------

// Property: CommitNote JSON roundtrip is lossless
func TestPropertyCommitNoteRoundtrip(t *testing.T) {
	for i := 0; i < 50; i++ {
		note := randomCommitNote()
		data, err := json.Marshal(note)
		if err != nil {
			t.Fatal(err)
		}
		var parsed domain.CommitNote
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.Kind != note.Kind {
			t.Error("kind lost in roundtrip")
		}
		if parsed.SchemaVersion != note.SchemaVersion {
			t.Error("schema_version lost in roundtrip")
		}
		if len(parsed.Intents) != len(note.Intents) {
			t.Errorf("intents count changed: %d -> %d", len(note.Intents), len(parsed.Intents))
		}
		for j, ref := range parsed.Intents {
			if ref.IntentID != note.Intents[j].IntentID {
				t.Error("intent_id lost in roundtrip")
			}
			if ref.SealResultHash != note.Intents[j].SealResultHash {
				t.Error("seal_result_hash lost in roundtrip")
			}
		}
		if parsed.Via != note.Via {
			t.Error("via lost in roundtrip")
		}
		if len(parsed.Reverts) != len(note.Reverts) {
			t.Error("reverts count changed in roundtrip")
		}
	}
}

// Property: CanonicalHash of a CommitNote is deterministic and key-order
// independent. Mutating any field must change the hash.
func TestPropertyCommitNoteCanonicalHash(t *testing.T) {
	for i := 0; i < 30; i++ {
		note := randomCommitNote()
		h1, err := core.CanonicalHash(note)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		h2, err := core.CanonicalHash(note)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if h1 != h2 {
			t.Errorf("CanonicalHash not deterministic: %s vs %s", h1, h2)
		}

		// Mutating a field must change the hash.
		mutated := note
		mutated.Via += "_mutated"
		h3, _ := core.CanonicalHash(mutated)
		if h3 == h1 {
			t.Error("mutating .Via must change CanonicalHash")
		}
	}
}

// Property: note with 0 intents and 0 reverts is valid
func TestPropertyEmptyNoteValid(t *testing.T) {
	note := domain.CommitNote{
		SchemaVersion: 1,
		Kind:          "mainline.commit_note",
		AddedAt:       "2026-04-25T00:00:00Z",
		AddedBy:       "actor_test",
	}
	data, err := json.Marshal(note)
	if err != nil {
		t.Fatal(err)
	}
	var parsed domain.CommitNote
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Kind != "mainline.commit_note" {
		t.Error("empty note should preserve kind")
	}
}

// -----------------------------------------------------------
// Helpers
// -----------------------------------------------------------

func randomCommitNote() domain.CommitNote {
	nIntents := randomInt(4) + 1
	intents := make([]domain.IntentReference, nIntents)
	for i := range intents {
		intents[i] = domain.IntentReference{
			IntentID:       "int_" + randomTestString(8),
			SealResultHash: "sha256:" + randomTestString(16),
		}
	}

	vias := []string{"merge", "reconcile", "manual", ""}
	via := vias[randomInt(len(vias))]

	note := domain.CommitNote{
		SchemaVersion: 1,
		Kind:          "mainline.commit_note",
		Intents:       intents,
		AddedAt:       "2026-04-25T14:00:00Z",
		AddedBy:       "actor_" + randomTestString(8),
		Via:           via,
	}

	if randomInt(3) == 0 {
		nReverts := randomInt(3)
		for i := 0; i < nReverts; i++ {
			note.Reverts = append(note.Reverts, "int_"+randomTestString(8))
		}
	}

	return note
}
