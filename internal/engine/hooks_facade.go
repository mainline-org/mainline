package engine

// HookFacade returns a Service-backed HookFacade for the hooks
// dispatcher. The hooks package defines its own narrow interface
// (hooks.EngineFacade); the facade here adapts our wider API down to
// that interface.
//
// Only mechanical, deterministic methods survive: hooks must not call
// anything that requires semantic judgment (start/append/seal-prepare/
// seal-submit/check) — those are agent-only operations described in
// AGENTS.md. Trimming the surface at the boundary makes the constraint
// statically checkable: any new "automation" added to the dispatcher
// has to widen this interface, which is the conversation we want to
// have explicitly.
//
// Sync, Status, ListProposals, and BinaryStaleness are all read-only
// and produce state snapshots — none of them can mint an intent,
// append a turn, or write a sealed event.
func (s *Service) HookFacade() HookFacade { return hookFacade{s: s} }

// HookFacade is the narrow surface the hooks Dispatcher uses. Type
// matches hooks.EngineFacade structurally so the cli wires them up
// with one assignment.
type HookFacade interface {
	Sync() (any, error)
	Status() (any, error)
	ListProposals() (any, error)
	// BinaryStaleness returns a snapshot of how old the running
	// mainline binary is relative to main HEAD. Pure mechanical (file
	// mtime + git commit date), no semantic judgement. Used by the
	// dispatcher to flag "you forgot to `go build` after pull" in the
	// session_start additional_context — the symptom that motivated
	// adding the check is hooks running pre-fix code after the fix
	// has merged to main.
	BinaryStaleness() (any, error)
}

type hookFacade struct{ s *Service }

func (h hookFacade) Sync() (any, error) {
	return h.s.Sync()
}

func (h hookFacade) Status() (any, error) {
	return h.s.Status()
}

func (h hookFacade) ListProposals() (any, error) {
	return h.s.ListProposals()
}

func (h hookFacade) BinaryStaleness() (any, error) {
	return h.s.BinaryStaleness()
}
