package webhook

import (
	"crypto/rand"
	"encoding/hex"
)

// generateEventID returns a queue-unique id used in envelope filenames
// and the X-Mainline-Event-Id header. We use 16 random hex chars
// rather than the engine's intent/turn id helpers because webhook
// events are NOT actor-log-scoped — they're a transient queue entry
// and should not collide with the domain-id namespace.
//
// Format: ev_<16 lowercase hex>. Collision probability for a
// per-repo queue is negligible.
func generateEventID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read is documented to never fail on supported
		// platforms; if it does, an empty id is harmless — the
		// queue layer will write a file like "ev_.json" and the
		// caller's failure path is the same.
		return "ev_"
	}
	return "ev_" + hex.EncodeToString(b[:])
}
