package engine

import (
	"encoding/json"

	"mainline/internal/domain"
	"mainline/internal/gitops"
)

// upsertCommitNote attaches `addition` to commit. If the commit already
// carries a parsable mainline.commit_note, addition's intents and
// reverts are merged into the existing note (deduplicated by IntentID
// and string respectively) instead of replacing them.
//
// Note-level metadata (added_at, added_by, via, match_strategy,
// reconciled_*) is taken from `addition` so the audit trail reflects
// the latest writer. Per-intent attribution lives in `added_by` plus
// the intent reference itself.
//
// Why this exists: NotesAdd uses `git notes add -f` which overwrites
// any existing note on the commit. Without an upsert, two intents
// landing on the same main commit (a real case during dogfooding when
// the second intent's seal-time HEAD coincided with the first's merge
// commit) silently kicked each other out of the notes ref.
//
// If the commit's existing note is not a mainline.commit_note (different
// kind, or unparseable JSON), we treat it as if there were no note and
// write `addition` outright. We do not preserve foreign notes — the
// notes ref is dedicated to mainline (refs/notes/mainline/intents),
// so finding a non-mainline payload there is itself a bug we shouldn't
// silently round-trip.
func upsertCommitNote(git *gitops.Git, commit string, addition domain.CommitNote) error {
	merged := addition

	if raw, _ := git.NotesShow(commit); raw != "" {
		var existing domain.CommitNote
		if err := json.Unmarshal([]byte(raw), &existing); err == nil &&
			existing.Kind == "mainline.commit_note" {
			merged.Intents = mergeIntentRefs(existing.Intents, addition.Intents)
			merged.Reverts = mergeStringSets(existing.Reverts, addition.Reverts)
		}
	}

	merged.SchemaVersion = 1
	merged.Kind = "mainline.commit_note"

	data, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	return git.NotesAdd(commit, string(data))
}

// mergeIntentRefs concatenates two intent-reference slices, keeping the
// first occurrence of each IntentID. Existing entries take precedence so
// a stale seal_result_hash is never silently overwritten by a fresh
// reconcile note for the same intent — that case should be flagged by
// the alreadyHasIntent guard upstream, not papered over here.
func mergeIntentRefs(existing, additions []domain.IntentReference) []domain.IntentReference {
	seen := make(map[string]bool, len(existing)+len(additions))
	out := make([]domain.IntentReference, 0, len(existing)+len(additions))
	for _, ref := range existing {
		if seen[ref.IntentID] {
			continue
		}
		seen[ref.IntentID] = true
		out = append(out, ref)
	}
	for _, ref := range additions {
		if seen[ref.IntentID] {
			continue
		}
		seen[ref.IntentID] = true
		out = append(out, ref)
	}
	return out
}

// mergeStringSets returns the deduplicated union of a and b, preserving
// the order in which each value was first seen.
func mergeStringSets(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, s := range b {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
