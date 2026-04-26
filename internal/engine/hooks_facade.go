package engine

// HookFacade returns a Service-backed EngineFacade for the hooks
// dispatcher. The hooks package defines its own narrow interface so
// it does not depend on every Service method; the facade here adapts
// our wider API down to that interface.
//
// We do NOT just `_ HooksFacade = (*Service)(nil)` and let Service
// itself satisfy the interface, because:
//
//   - hooks.EngineFacade.Sync returns `any` (the hooks package does
//     not import engine), but Service.Sync returns *SyncResult. Same
//     mismatch on Status / Start / Append / SealPrepare. A wrapper
//     does the small interface adaptation in one place.
//   - ActiveDraftIntentID is hook-flow-only — it has no caller in the
//     engine package itself and would just clutter the public surface.
func (s *Service) HookFacade() HookFacade { return hookFacade{s: s} }

// HookFacade is the narrow surface the hooks Dispatcher uses. Type
// matches hooks.EngineFacade structurally so the cli wires them up
// with one assignment.
type HookFacade interface {
	Sync() (any, error)
	Status() (any, error)
	Start(goal, thread string) (any, error)
	Append(description string) (any, error)
	SealPrepare(intentID string) (any, error)
	ActiveDraftIntentID() (string, error)
}

type hookFacade struct{ s *Service }

func (h hookFacade) Sync() (any, error) {
	return h.s.Sync()
}

func (h hookFacade) Status() (any, error) {
	return h.s.Status()
}

func (h hookFacade) Start(goal, thread string) (any, error) {
	return h.s.Start(goal, thread)
}

func (h hookFacade) Append(description string) (any, error) {
	return h.s.Append(description)
}

func (h hookFacade) SealPrepare(intentID string) (any, error) {
	return h.s.SealPrepare(intentID)
}

// ActiveDraftIntentID returns the active draft's intent id on the
// current branch, or "". Backed by Store.FindActiveDraft so we share
// one truth with the rest of the engine — adding a new entry point
// would risk drift if the "what counts as active" rules ever changed.
func (h hookFacade) ActiveDraftIntentID() (string, error) {
	branch, err := h.s.Git.CurrentBranch()
	if err != nil {
		return "", err
	}
	d, err := h.s.Store.FindActiveDraft(branch)
	if err != nil {
		return "", err
	}
	if d == nil {
		return "", nil
	}
	return d.IntentID, nil
}
