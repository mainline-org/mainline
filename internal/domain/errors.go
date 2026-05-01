package domain

import "fmt"

// ErrorCode enumerates machine-readable error codes for agent consumption.
type ErrorCode string

const (
	ErrNotInitialized     ErrorCode = "NOT_INITIALIZED"
	ErrAlreadyInitialized ErrorCode = "ALREADY_INITIALIZED"
	ErrNotInGitRepo       ErrorCode = "NOT_IN_GIT_REPO"
	ErrNoActiveIntent     ErrorCode = "NO_ACTIVE_INTENT"
	ErrActiveIntentExists ErrorCode = "ACTIVE_INTENT_EXISTS"
	ErrInvalidStatus      ErrorCode = "INVALID_STATUS"
	ErrSealFailed         ErrorCode = "SEAL_FAILED"
	ErrCheckFailed        ErrorCode = "CHECK_FAILED"
	ErrMergeFailed        ErrorCode = "MERGE_FAILED"
	ErrPublishFailed      ErrorCode = "PUBLISH_FAILED"
	ErrSyncFailed         ErrorCode = "SYNC_FAILED"
	ErrInvalidInput       ErrorCode = "INVALID_INPUT"
	ErrGitError           ErrorCode = "GIT_ERROR"
	ErrIOError            ErrorCode = "IO_ERROR"
	ErrConflictDetected   ErrorCode = "CONFLICT_DETECTED"
)

// MainlineError is the standard error returned to callers (agents or humans).
type MainlineError struct {
	Code             ErrorCode `json:"code"`
	Message          string    `json:"message"`
	Recoverable      bool      `json:"recoverable"`
	SuggestedActions []string  `json:"suggested_actions,omitempty"`
}

func (e *MainlineError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func NewError(code ErrorCode, msg string) *MainlineError {
	return &MainlineError{Code: code, Message: msg}
}

func NewRecoverableError(code ErrorCode, msg string, actions ...string) *MainlineError {
	return &MainlineError{
		Code:             code,
		Message:          msg,
		Recoverable:      true,
		SuggestedActions: actions,
	}
}

// JSONErrorResponse is the top-level JSON error envelope.
type JSONErrorResponse struct {
	OK    bool           `json:"ok"`
	Error *MainlineError `json:"error"`
}

// JSONResponse is the top-level JSON success envelope.
type JSONResponse struct {
	OK   bool        `json:"ok"`
	Data interface{} `json:"data"`
}

// MainlineView is the materialized state of the whole mainline.
type MainlineView struct {
	SchemaVersion int          `json:"schema_version"`
	RebuiltAt     string       `json:"rebuilt_at"`
	MainBranch    string       `json:"main_branch"`
	MainHead      string       `json:"main_head"`
	Intents       []IntentView `json:"intents"`

	// RiskResolutions maps risk IDs ("int_xxx#N") to their resolution
	// records. Populated during view rebuild from IntentSealedEvent.
	// ResolvesRisks + RiskResolvedEvent. Missing key = open risk.
	RiskResolutions map[string][]RiskResolution `json:"risk_resolutions,omitempty"`
}

// ProposedIndex is the fast-lookup index for proposed (not yet merged) intents.
type ProposedIndex struct {
	SchemaVersion int          `json:"schema_version"`
	RebuiltAt     string       `json:"rebuilt_at"`
	Proposed      []IntentView `json:"proposed"`
}
