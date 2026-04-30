// Package cursor wires Cursor IDE / Cursor agent CLI into mainline's
// hooks subsystem. The package owns:
//
//   - the .cursor/hooks.json file: install / uninstall / detect
//   - mapping Cursor's native hook payloads (sessionStart, stop, etc)
//     onto mainline's normalized hooks.Event taxonomy
//
// Hook installation merges into any existing .cursor/hooks.json
// instead of replacing it, so users with their own cursor hooks (a
// pre-tool-use scanner, a session-end notifier, ...) keep them.
package cursor

import (
	"encoding/json"

	"github.com/mainline-org/mainline/internal/hooks"
)

// AgentName is the canonical id used on the CLI:
// `mainline hooks install --agent cursor` and
// `mainline hooks cursor <event>`. Lowercase and hyphen-free for
// stability across cursor-cli vs cursor-ide etc.
const AgentName = "cursor"

// DisplayName is the human label used in `mainline hooks status`.
const DisplayName = "Cursor"

// Native cursor hook event names (the keys cursor expects under
// "hooks" in .cursor/hooks.json). Exposed as constants so install /
// uninstall / parse all reference the same string and can't drift.
//
// Subset rationale (v1):
//   - sessionStart, sessionEnd: lifecycle bookends; always installed
//   - beforeSubmitPrompt: gives us the user prompt for auto-start
//   - stop: turn-end signal for auto-append
//   - subagentStop: structured summary + modified_files for
//     subagent-driven turns
//
// Not installed in v1 (no automation mapping yet):
//   - preCompact: would be useful for a future "flush state before
//     compaction" hook; not load-bearing today
//   - subagentStart, preToolUse, postToolUse: informational
const (
	HookSessionStart       = "session-start"
	HookSessionEnd         = "session-end"
	HookBeforeSubmitPrompt = "before-submit-prompt"
	HookStop               = "stop"
	HookSubagentStop       = "subagent-stop"
)

// nativeHookKey maps our hyphenated hook id (used on the CLI:
// `mainline hooks cursor session-start`) to the camelCase key cursor
// expects in hooks.json. Centralized so install + parse + uninstall
// share one mapping.
var nativeHookKey = map[string]string{
	HookSessionStart:       "sessionStart",
	HookSessionEnd:         "sessionEnd",
	HookBeforeSubmitPrompt: "beforeSubmitPrompt",
	HookStop:               "stop",
	HookSubagentStop:       "subagentStop",
}

// Agent is the cursor implementation of hooks.Agent. The struct is
// stateless — every method takes a repoRoot or stdin reader. One
// shared instance per process is fine; the package init() registers
// exactly that.
type Agent struct{}

// Name implements hooks.Agent.
func (Agent) Name() string { return AgentName }

// DisplayName implements hooks.Agent.
func (Agent) DisplayName() string { return DisplayName }

// HookNames implements hooks.Agent. Returns the hyphenated CLI ids
// (the cli builds `mainline hooks cursor <name>` subcommands from
// these), not the camelCase native cursor keys.
func (Agent) HookNames() []string {
	return []string{
		HookSessionStart,
		HookSessionEnd,
		HookBeforeSubmitPrompt,
		HookStop,
		HookSubagentStop,
	}
}

// RenderHookOutput implements hooks.HookOutputRenderer. It produces
// cursor-protocol JSON for the events where cursor accepts host
// output:
//
//   - session-start: cursor lets a hook return
//     {"continue": true, "additional_context": "<markdown>"}
//     and injects the markdown into the agent's system context. This
//     is the ONLY cursor hook that can push state into the agent; we
//     use it to deliver a status snapshot + sync summary + scenario
//     hint, so the agent does not need to remember to call `mainline
//     status` before reasoning.
//
// Other hooks (before-submit-prompt, stop, subagent-stop, session-end)
// either don't accept output or have no use for one in the
// "context provider" model (the agent drives all semantic decisions
// per the Mainline skill or team guidance), so we return (nil, nil)
// and the CLI writes nothing.
func (Agent) RenderHookOutput(hookName string, d *hooks.Dispatcher, _ *hooks.Event, _ any) ([]byte, error) {
	if hookName != HookSessionStart || d == nil {
		return nil, nil
	}
	md := d.RenderSessionStartContext(d.LastSync(), d.LastStatus())
	if md == "" {
		return nil, nil
	}
	return json.Marshal(map[string]any{
		"continue":           true,
		"additional_context": md,
	})
}

func init() {
	// Self-register so the cli's `mainline hooks ...` subcommand
	// tree picks the agent up at startup. There is no central
	// switch per agent — adding cursor amounts to importing this
	// package, which the cli does in its hooks_cmd.go via blank
	// import.
	hooks.Register(Agent{})
}
