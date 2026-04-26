package hooks

import (
	"fmt"
	"sort"
	"sync"
)

// Agent is the per-coding-agent integration contract. An Agent owns:
//
//   - the file format on disk that the agent reads at startup
//     (e.g. .cursor/hooks.json) — Install / Uninstall / IsInstalled
//   - the per-agent payload schema delivered on stdin to each hook
//     command — embedded EventParser
//   - the canonical name and display label used in CLI output
//
// Agent does NOT model what the hook should DO when it fires. That is
// the Dispatcher's responsibility — the same Dispatcher serves every
// agent. Adding a new agent therefore means: implement Agent + its
// install logic + its ParseEvent mapping; reuse the Dispatcher
// unchanged.
//
// Implementations live under internal/hooks/agents/<name>/ and call
// hooks.Register from their package init() so the cli wires up the
// `mainline hooks <name> <hook>` subtree dynamically — no central
// switch statement to update per agent.
type Agent interface {
	EventParser

	// Name is the lowercase, hyphen-separated canonical id used on
	// the CLI (`mainline hooks install --agent cursor`). Stable
	// across releases; renames need a deprecation alias.
	Name() string

	// DisplayName is the human label shown in `mainline hooks status`
	// and similar surfaces. Free-form proper noun (e.g. "Cursor",
	// "Claude Code").
	DisplayName() string

	// Install writes / merges the agent's on-disk hook config
	// pointing at `mainline hooks <name> <hook>`. Must be idempotent
	// AND must preserve any unrelated keys the user has in the
	// config — `--force` controls whether existing mainline-managed
	// entries are rewritten in place. Returns the list of host
	// config files that were created or modified for the
	// `mainline hooks status` surface.
	Install(repoRoot string, opts InstallOptions) (InstallReport, error)

	// Uninstall removes only mainline-managed entries from the
	// agent config, leaving user-installed hooks alone. If the
	// resulting config is empty / equivalent to default, the file
	// itself may be removed — implementations document their
	// behaviour. Idempotent: uninstalling an already-uninstalled
	// agent is not an error.
	Uninstall(repoRoot string) error

	// IsInstalled reports whether the agent's config currently
	// contains any mainline-managed entries. Used by `mainline hooks
	// status` to enumerate active integrations and by Install to
	// detect already-installed state.
	IsInstalled(repoRoot string) (bool, error)

	// HookNames returns the list of native hook event names this
	// Agent installs (e.g. "session-start", "before-submit-prompt").
	// The CLI builds `mainline hooks <agent> <hook>` cobra subcommands
	// from this list; ParseEvent must accept every value here.
	HookNames() []string
}

// InstallOptions controls the host-side install behaviour. New fields
// are additive; pass an empty value to use defaults.
type InstallOptions struct {
	// Force replaces existing mainline-managed entries in place. Use
	// after a release that changes the wrapper command (e.g. switching
	// from `mainline` to a different binary path). User-installed
	// hooks remain untouched regardless.
	Force bool

	// LocalDev points the wrapper command at `go run .` instead of
	// the installed `mainline` binary so contributors developing the
	// hooks subsystem itself get their changes picked up without a
	// reinstall. Off by default.
	LocalDev bool
}

// InstallReport tells the CLI what to print after a successful install.
// Empty Files is allowed (no-op idempotent install reports the
// already-installed paths).
type InstallReport struct {
	// Files are absolute paths the install touched. Used in CLI
	// output ("wrote .cursor/hooks.json") and in `mainline hooks
	// status` so the user can find the files manually.
	Files []string `json:"files,omitempty"`

	// HookCount is the number of hook event entries the agent now
	// has wired to mainline (after merge). One agent can install
	// several entries (e.g. cursor installs 5).
	HookCount int `json:"hook_count"`

	// AlreadyInstalled is true when no changes were needed because
	// the existing config already pointed at mainline. Distinct
	// from "Files: nil" because Force=true on an already-installed
	// agent still rewrites and returns the file list.
	AlreadyInstalled bool `json:"already_installed,omitempty"`
}

// -----------------------------------------------------------
// Registry
// -----------------------------------------------------------
//
// Agents register themselves at process init via Register. The CLI
// then enumerates registered agents to build subcommand trees, list
// supported agents, and route `mainline hooks <agent> <hook>` calls.
//
// Registry is intentionally a tiny global. Mainline's hooks subsystem
// is process-scoped (one CLI invocation per hook fire), and the
// Dispatcher / Service themselves are passed in explicitly — the
// global only owns the agent list, not any state with lifecycle.

var (
	registryMu sync.RWMutex
	registry   = map[string]Agent{}
)

// Register adds an Agent to the global registry. Called from each
// agent package's init(). Re-registering the same name panics —
// that almost certainly means two packages claiming the same
// canonical id, which we want loud.
func Register(a Agent) {
	if a == nil {
		panic("hooks.Register: nil agent")
	}
	name := a.Name()
	if name == "" {
		panic("hooks.Register: agent with empty Name()")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("hooks.Register: %s already registered", name))
	}
	registry[name] = a
}

// Get returns the Agent registered under name, or (nil, false). The
// CLI uses this when dispatching `mainline hooks <name> <hook>`.
func Get(name string) (Agent, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	a, ok := registry[name]
	return a, ok
}

// List returns all registered agents in alphabetical order by Name.
// Stable ordering keeps `mainline hooks list-agents` output
// deterministic across builds.
func List() []Agent {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Agent, 0, len(registry))
	for _, a := range registry {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out
}

// Names returns the canonical ids of every registered agent. Same
// ordering guarantee as List.
func Names() []string {
	agents := List()
	out := make([]string, len(agents))
	for i, a := range agents {
		out[i] = a.Name()
	}
	return out
}
