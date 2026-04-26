package agent

import "github.com/mainline-org/mainline/internal/domain"

// Adapter is the interface for agent-side operations.
// v0.1 is a stub – detection and auto-invocation are not implemented.
type Adapter interface {
	// Detect checks if a compatible agent is available.
	Detect() (bool, string)

	// SealIntent asks the agent to produce a SealResult for the given prepare package.
	SealIntent(pkg *domain.SealPreparePackage) (*domain.SealResult, error)

	// CheckConflicts asks the agent to produce a CheckJudgmentResult.
	CheckConflicts(pkg *domain.CheckPreparePackage) (*domain.CheckJudgmentResult, error)
}

// StubAdapter is the v0.1 no-op adapter.
type StubAdapter struct{}

func NewStub() *StubAdapter { return &StubAdapter{} }

func (s *StubAdapter) Detect() (bool, string) {
	return false, "no agent adapter configured (v0.1 stub)"
}

func (s *StubAdapter) SealIntent(pkg *domain.SealPreparePackage) (*domain.SealResult, error) {
	return nil, &domain.MainlineError{
		Code:        domain.ErrInvalidInput,
		Message:     "agent adapter not available; use --prepare to get the JSON package, then submit the SealResult via --submit",
		Recoverable: true,
		SuggestedActions: []string{
			"mainline seal --prepare | agent-cli seal",
			"mainline seal --submit < seal-result.json",
		},
	}
}

func (s *StubAdapter) CheckConflicts(pkg *domain.CheckPreparePackage) (*domain.CheckJudgmentResult, error) {
	return nil, &domain.MainlineError{
		Code:        domain.ErrInvalidInput,
		Message:     "agent adapter not available; use --prepare to get the JSON package, then submit via --submit",
		Recoverable: true,
		SuggestedActions: []string{
			"mainline check --prepare | agent-cli check",
			"mainline check --submit < check-result.json",
		},
	}
}
