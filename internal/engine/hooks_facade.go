package engine

// HookFacade returns a Service-backed HookFacade for the hooks
// dispatcher. The hooks package defines its own narrow interface
// (hooks.EngineFacade); the facade here adapts our wider API down to
// that interface.
//
// Only Sync and Status survive: hooks must not call anything that
// requires semantic judgment (start/append/seal-prepare/seal-submit/
// check) — those are agent-only operations described in AGENTS.md.
// Trimming the surface at the boundary makes the constraint
// statically checkable: any new "automation" added to the dispatcher
// has to widen this interface, which is the conversation we want to
// have explicitly.
func (s *Service) HookFacade() HookFacade { return hookFacade{s: s} }

// HookFacade is the narrow surface the hooks Dispatcher uses. Type
// matches hooks.EngineFacade structurally so the cli wires them up
// with one assignment.
type HookFacade interface {
	Sync() (any, error)
	Status() (any, error)
}

type hookFacade struct{ s *Service }

func (h hookFacade) Sync() (any, error) {
	return h.s.Sync()
}

func (h hookFacade) Status() (any, error) {
	return h.s.Status()
}
