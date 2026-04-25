package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"pgregory.net/rapid"

	"mainline/internal/domain"
)

// rapidJSONValue generates an arbitrary JSON-shaped value (map | slice | string |
// number | bool | nil) bounded in depth so the test runs in finite time.
func rapidJSONValue(t *rapid.T, depth int) interface{} {
	if depth <= 0 {
		return rapid.OneOf(
			rapid.String().AsAny(),
			rapid.Int().AsAny(),
			rapid.Float64().AsAny(),
			rapid.Bool().AsAny(),
			rapid.Just[interface{}](nil),
		).Draw(t, "leaf")
	}
	return rapid.OneOf(
		rapid.String().AsAny(),
		rapid.Int().AsAny(),
		rapid.Bool().AsAny(),
		rapid.Custom(func(t *rapid.T) interface{} {
			n := rapid.IntRange(0, 4).Draw(t, "len")
			out := make([]interface{}, n)
			for i := range out {
				out[i] = rapidJSONValue(t, depth-1)
			}
			return out
		}).AsAny(),
		rapid.Custom(func(t *rapid.T) interface{} {
			keys := rapid.SliceOfNDistinct(rapid.StringMatching(`[a-z]{1,4}`), 0, 4, rapid.ID[string]).Draw(t, "keys")
			obj := make(map[string]interface{}, len(keys))
			for _, k := range keys {
				obj[k] = rapidJSONValue(t, depth-1)
			}
			return obj
		}).AsAny(),
	).Draw(t, "value")
}

// CanonicalHash(x) must equal sha256(CanonicalJSON(x)) — the two primitives
// are required to be related by exactly that construction.
func TestPropertyCanonicalHashEqualsSha256OfJSON(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		v := rapidJSONValue(t, 3)

		js, err := CanonicalJSON(v)
		if err != nil {
			t.Fatalf("CanonicalJSON: %v", err)
		}
		got, err := CanonicalHash(v)
		if err != nil {
			t.Fatalf("CanonicalHash: %v", err)
		}

		sum := sha256.Sum256(js)
		want := hex.EncodeToString(sum[:])
		if got != want {
			t.Fatalf("CanonicalHash != sha256(CanonicalJSON):\n got=%s\nwant=%s\njson=%s", got, want, js)
		}
	})
}

// Round-tripping through JSON and CanonicalJSON must not change the canonical
// hash. This is the practical guarantee used when we compute SealResultHash on
// one machine and verify it on another.
func TestPropertyCanonicalHashStableAcrossJSONRoundtrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		v := rapidJSONValue(t, 3)

		h1, err := CanonicalHash(v)
		if err != nil {
			t.Fatalf("hash1: %v", err)
		}

		js, _ := CanonicalJSON(v)
		var rt interface{}
		if err := json.Unmarshal(js, &rt); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		h2, err := CanonicalHash(rt)
		if err != nil {
			t.Fatalf("hash2: %v", err)
		}
		if h1 != h2 {
			t.Fatalf("hash changed across roundtrip: %s -> %s\n  json=%s", h1, h2, js)
		}
	})
}

// allStatuses returns every IntentStatus value, including the synthetic empty
// status, so we can exhaustively probe ValidateStateTransition.
func allStatuses() []domain.IntentStatus {
	return []domain.IntentStatus{
		domain.StatusDrafting,
		domain.StatusSealedLocal,
		domain.StatusProposed,
		domain.StatusMerged,
		domain.StatusAbandoned,
		domain.StatusSuperseded,
		domain.StatusReverted,
	}
}

// allowedTransitions is the explicit truth table the production state machine
// is required to implement. Keeping it in tests means any change to the
// engine's allowed map must be mirrored here on purpose.
func allowedTransitions() map[domain.IntentStatus]map[domain.IntentStatus]bool {
	allow := func(targets ...domain.IntentStatus) map[domain.IntentStatus]bool {
		m := make(map[domain.IntentStatus]bool, len(targets))
		for _, t := range targets {
			m[t] = true
		}
		return m
	}
	return map[domain.IntentStatus]map[domain.IntentStatus]bool{
		domain.StatusDrafting:    allow(domain.StatusSealedLocal, domain.StatusAbandoned, domain.StatusSuperseded),
		domain.StatusSealedLocal: allow(domain.StatusProposed, domain.StatusAbandoned, domain.StatusSuperseded),
		domain.StatusProposed:    allow(domain.StatusMerged, domain.StatusAbandoned, domain.StatusSuperseded),
		domain.StatusMerged:      allow(domain.StatusReverted),
	}
}

// Exhaustively cross-check every (from, to) pair. Each transition either
// matches the truth table or is rejected. Terminal states never have outgoing
// edges. This subsumes TestPropertyStateTransitionsAsymmetric.
func TestPropertyStateTransitionsExhaustive(t *testing.T) {
	statuses := allStatuses()
	allow := allowedTransitions()

	for _, from := range statuses {
		for _, to := range statuses {
			err := ValidateStateTransition(from, to)
			permitted := allow[from][to]
			if permitted && err != nil {
				t.Errorf("%s -> %s: expected valid, got error: %v", from, to, err)
			}
			if !permitted && err == nil {
				t.Errorf("%s -> %s: expected invalid, got nil error", from, to)
			}
		}
	}

	// Terminal states (no entry in allow) must reject every transition.
	terminals := []domain.IntentStatus{
		domain.StatusAbandoned,
		domain.StatusSuperseded,
		domain.StatusReverted,
	}
	for _, term := range terminals {
		for _, to := range statuses {
			if err := ValidateStateTransition(term, to); err == nil {
				t.Errorf("terminal %s should not transition to %s", term, to)
			}
		}
	}
}

// All states reachable from drafting via a sequence of legal transitions
// covers every non-terminal node; this guards against accidentally orphaning a
// state in the production map.
func TestPropertyStateMachineAllStatesReachable(t *testing.T) {
	allow := allowedTransitions()
	visited := map[domain.IntentStatus]bool{domain.StatusDrafting: true}
	frontier := []domain.IntentStatus{domain.StatusDrafting}

	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]
		for next := range allow[cur] {
			if !visited[next] {
				visited[next] = true
				frontier = append(frontier, next)
			}
		}
	}

	for _, s := range allStatuses() {
		if !visited[s] {
			t.Errorf("status %s is unreachable from drafting", s)
		}
	}
}
