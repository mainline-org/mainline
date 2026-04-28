package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/engine"
	"github.com/mainline-org/mainline/internal/hooks"

	// Blank-import each agent package so its init() registers with
	// the hooks registry. New agents are added here, alongside the
	// cursor entry; the rest of the cli is agent-agnostic.
	_ "github.com/mainline-org/mainline/internal/hooks/agents/claudecode"
	_ "github.com/mainline-org/mainline/internal/hooks/agents/codex"
	_ "github.com/mainline-org/mainline/internal/hooks/agents/cursor"
)

var (
	hooksInstallAgent    string
	hooksInstallAll      bool
	hooksInstallForce    bool
	hooksInstallLocalDev bool
	hooksInstallBin      string
	hooksUninstallAgent  string
	hooksUninstallAll    bool
)

// hooksCmd is the root of the `mainline hooks ...` subtree. The
// subcommands fall into two groups:
//
//   - User-facing: install, uninstall, status, list-agents, enable,
//     disable. These manage the on-disk integration files in
//     .cursor/, .claude/, etc.
//   - Agent-facing: `mainline hooks <agent> <event>` dispatch entry
//     points wired by Cobra dynamically from the registry. Every
//     registered agent gets one subcommand per HookNames() entry;
//     the cli never hardcodes a per-agent switch.
var hooksCmd = &cobra.Command{
	Use:   "hooks",
	Short: "Install and dispatch agent lifecycle hooks",
	Long: `Manage agent hook integrations (Cursor, Claude Code, Codex, ...) and
serve as the dispatch entry point invoked by those agents.

Common flows:
  mainline hooks install --agent cursor   # write .cursor/hooks.json
  mainline hooks status                   # which agents are wired
  mainline hooks disable                  # soft kill-switch
  mainline hooks uninstall --all          # remove every integration

The agent itself calls
  mainline hooks <agent> <event>
on each lifecycle event, with the agent-native JSON payload on
stdin. End users do not invoke that form directly.`,
}

// -----------------------------------------------------------
// install
// -----------------------------------------------------------

var hooksInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install hooks for one or all supported agents",
	RunE: func(cmd *cobra.Command, args []string) error {
		targets, err := selectAgents(hooksInstallAgent, hooksInstallAll)
		if err != nil {
			return err
		}
		root, err := os.Getwd()
		if err != nil {
			return err
		}

		// Resolve --bin to an absolute path so the wrapper does not
		// depend on whatever CWD cursor invokes the hook from.
		bin := hooksInstallBin
		if bin != "" {
			abs, err := filepath.Abs(bin)
			if err != nil {
				return fmt.Errorf("--bin: %w", err)
			}
			bin = abs
		}
		results := make([]agentInstallResult, 0, len(targets))
		for _, a := range targets {
			rep, err := a.Install(root, hooks.InstallOptions{
				Force:    hooksInstallForce,
				LocalDev: hooksInstallLocalDev,
				BinPath:  bin,
			})
			results = append(results, agentInstallResult{
				Agent:  a.Name(),
				Report: rep,
				Err:    errString(err),
			})
		}
		// Soft on/off setting: when [hooks] is missing or has all
		// fields false, the dispatcher would no-op. Install
		// implicitly turns the section on by setting Enabled=true
		// in config.toml — the user's explicit choice to install
		// is the same choice as enable.
		if err := ensureHooksEnabled(); err != nil {
			// Persisting the toggle is nice-to-have; the on-disk
			// hook entries already work. Print but don't fail.
			fmt.Fprintf(os.Stderr, "warn: could not enable [hooks] in config.toml: %v\n", err)
		}

		if jsonOutput {
			outputJSON(map[string]any{"agents": results})
			return nil
		}
		for _, r := range results {
			if r.Err != "" {
				fmt.Printf("✗ %s: %s\n", r.Agent, r.Err)
				continue
			}
			tag := "installed"
			if r.Report.AlreadyInstalled {
				tag = "already up to date"
			}
			fmt.Printf("✓ %s: %s (%d hook entries)\n", r.Agent, tag, r.Report.HookCount)
			for _, f := range r.Report.Files {
				rel, _ := filepath.Rel(root, f)
				if rel == "" {
					rel = f
				}
				fmt.Printf("    %s\n", rel)
			}
			if r.Report.Scope != "" {
				fmt.Printf("    scope: %s\n", r.Report.Scope)
			}
		}
		fmt.Println()
		fmt.Println("Install scope: repo-local. Mainline writes this repository's agent config files")
		fmt.Println("and does not inspect or modify global Cursor, Claude Code, or Codex settings.")
		fmt.Println("Existing agent sessions may need a new session or app restart before they see")
		fmt.Println("new repo-local hook config.")
		fmt.Println()
		fmt.Println("Hooks are enabled. At sessionStart the dispatcher will run `mainline sync`")
		fmt.Println("and inject a status snapshot as agent context. All other workflow steps")
		fmt.Println("(start, append, seal --prepare/--submit, check) remain agent-driven per AGENTS.md.")
		fmt.Println("Run `mainline hooks disable` to pause without uninstalling.")
		return nil
	},
}

// agentInstallResult is the per-agent row in the install JSON / human
// output. Err is a string (not error) so it serializes cleanly.
type agentInstallResult struct {
	Agent  string              `json:"agent"`
	Report hooks.InstallReport `json:"report"`
	Err    string              `json:"error,omitempty"`
}

// -----------------------------------------------------------
// uninstall
// -----------------------------------------------------------

var hooksUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove mainline-managed hook entries from one or all agents",
	RunE: func(cmd *cobra.Command, args []string) error {
		targets, err := selectAgents(hooksUninstallAgent, hooksUninstallAll)
		if err != nil {
			return err
		}
		root, err := os.Getwd()
		if err != nil {
			return err
		}
		type row struct {
			Agent string `json:"agent"`
			Err   string `json:"error,omitempty"`
		}
		out := make([]row, 0, len(targets))
		for _, a := range targets {
			err := a.Uninstall(root)
			out = append(out, row{Agent: a.Name(), Err: errString(err)})
		}
		if jsonOutput {
			outputJSON(map[string]any{"agents": out})
			return nil
		}
		for _, r := range out {
			if r.Err != "" {
				fmt.Printf("✗ %s: %s\n", r.Agent, r.Err)
			} else {
				fmt.Printf("✓ %s: removed mainline-managed entries\n", r.Agent)
			}
		}
		return nil
	},
}

// -----------------------------------------------------------
// status / list-agents
// -----------------------------------------------------------

var hooksStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show installed hook integrations and dispatcher toggles",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := os.Getwd()
		if err != nil {
			return err
		}
		type row struct {
			Agent             string                   `json:"agent"`
			DisplayName       string                   `json:"display_name"`
			Status            hooks.InstallationStatus `json:"status"`
			DispatcherEnabled bool                     `json:"dispatcher_enabled"`
			Effective         bool                     `json:"effective"`
			Err               string                   `json:"error,omitempty"`
		}

		// Dispatcher toggles from config.toml so the user sees not
		// just "is the on-disk config wired" but "are the auto-flow
		// actions actually firing". Common confusion pre-this:
		// install + disable looks identical to "not installed" from
		// the user's perspective; status disambiguates.
		var section *domain.HooksSection
		if svc, _ := getService(); svc != nil {
			if cfg, _ := svc.Store.ReadTeamConfig(); cfg != nil {
				h := cfg.Hooks
				section = &h
			}
		}
		dispatcherEnabled := section != nil && section.Enabled

		var out []row
		for _, a := range hooks.List() {
			status, err := installationStatusFor(a, root)
			effective := status.Installed && !status.NeedsRepair && dispatcherEnabled
			out = append(out, row{
				Agent:             a.Name(),
				DisplayName:       a.DisplayName(),
				Status:            status,
				DispatcherEnabled: dispatcherEnabled,
				Effective:         effective,
				Err:               errString(err),
			})
		}

		if jsonOutput {
			outputJSON(map[string]any{
				"agents":   out,
				"settings": section,
			})
			return nil
		}
		fmt.Println("Agent integrations (repo-local):")
		if len(out) == 0 {
			fmt.Println("  (no agents registered)")
		}
		for _, r := range out {
			state := "not installed"
			switch {
			case r.Err != "":
				state = "error"
			case r.Effective:
				state = "installed, effective"
			case r.Status.Installed && r.Status.NeedsRepair:
				state = "installed, needs repair"
			case r.Status.Installed && !r.DispatcherEnabled:
				state = "installed, dispatcher disabled"
			case r.Status.Installed:
				state = "installed, not effective"
			}
			fmt.Printf("  %-12s %-12s %s", r.Agent, r.DisplayName, state)
			if r.Status.ExpectedHookCount > 0 {
				fmt.Printf("  hooks=%d/%d", r.Status.HookCount, r.Status.ExpectedHookCount)
			}
			if r.Status.Scope != "" {
				fmt.Printf("  scope=%s", r.Status.Scope)
			}
			fmt.Println()
			if r.Err != "" {
				fmt.Printf("    error: %s\n", r.Err)
			}
			for _, f := range r.Status.Files {
				rel, _ := filepath.Rel(root, f)
				if rel == "" {
					rel = f
				}
				fmt.Printf("    file: %s\n", rel)
			}
			if len(r.Status.RepairReasons) > 0 {
				fmt.Printf("    repair: %s\n", strings.Join(r.Status.RepairReasons, "; "))
			}
		}
		fmt.Println()
		fmt.Println("Scope: repo-local only; global agent settings are not inspected or modified.")
		fmt.Println("Effectiveness requires both a complete agent config and [hooks].enabled=true.")
		fmt.Println()
		if section == nil {
			fmt.Println("Dispatcher: (mainline not initialized in this repo)")
			return nil
		}
		fmt.Println("Dispatcher (.mainline/config.toml [hooks]):")
		fmt.Printf("  enabled                       = %v\n", section.Enabled)
		fmt.Printf("  auto_sync_on_session_start    = %v\n", section.AutoSyncOnSessionStart)
		return nil
	},
}

var hooksListAgentsCmd = &cobra.Command{
	Use:   "list-agents",
	Short: "List supported agents and their hook events",
	RunE: func(cmd *cobra.Command, args []string) error {
		type row struct {
			Name        string   `json:"name"`
			DisplayName string   `json:"display_name"`
			Hooks       []string `json:"hooks"`
		}
		var out []row
		for _, a := range hooks.List() {
			out = append(out, row{
				Name:        a.Name(),
				DisplayName: a.DisplayName(),
				Hooks:       a.HookNames(),
			})
		}
		if jsonOutput {
			outputJSON(map[string]any{"agents": out})
			return nil
		}
		if len(out) == 0 {
			fmt.Println("(no agents registered)")
			return nil
		}
		for _, r := range out {
			fmt.Printf("%s (%s)\n", r.DisplayName, r.Name)
			for _, h := range r.Hooks {
				fmt.Printf("  - %s\n", h)
			}
		}
		return nil
	},
}

// -----------------------------------------------------------
// enable / disable
// -----------------------------------------------------------

var hooksEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Soft-enable the dispatcher without re-installing",
	RunE:  func(*cobra.Command, []string) error { return setHooksEnabled(true) },
}

var hooksDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Soft-disable the dispatcher (on-disk hooks still fire but no-op)",
	RunE:  func(*cobra.Command, []string) error { return setHooksEnabled(false) },
}

// -----------------------------------------------------------
// `mainline hooks <agent> <event>` dispatch
// -----------------------------------------------------------
//
// Each registered agent gets a parent subcommand under `hooks`, and
// each of its HookNames() entries gets a leaf RunE under that. The
// leaf reads the agent-native JSON payload from stdin, hands it to
// the agent's ParseEvent, and forwards the normalized hooks.Event
// to the Dispatcher.

func registerAgentDispatchCommands() {
	for _, a := range hooks.List() {
		agent := a // capture
		parent := &cobra.Command{
			Use:    agent.Name(),
			Short:  fmt.Sprintf("Dispatch a %s lifecycle hook (called by the agent)", agent.DisplayName()),
			Hidden: true,
		}
		for _, hookName := range agent.HookNames() {
			hn := hookName
			parent.AddCommand(&cobra.Command{
				Use:   hn,
				Short: fmt.Sprintf("%s: %s", agent.DisplayName(), hn),
				RunE: func(*cobra.Command, []string) error {
					return runAgentHook(agent, hn)
				},
			})
		}
		hooksCmd.AddCommand(parent)
	}
}

// runAgentHook is the dispatch hot path: read stdin -> parse -> route.
//
//   - Errors here are LOGGED but NOT propagated as exit-1: a hook that
//     exits non-zero would interrupt the user's agent session in some
//     hosts (cursor surfaces it as a "hook failed" toast). The few
//     cases where a non-zero exit IS appropriate (mainline not
//     initialized in this repo) are pre-checked.
//   - Service init failure is non-fatal — `mainline hooks ...` should
//     work in any directory the agent runs in, including non-repo
//     ones, where mainline simply has nothing useful to do.
func runAgentHook(agent hooks.Agent, hookName string) error {
	ev, err := agent.ParseEvent(context.Background(), hookName, os.Stdin)
	if err != nil {
		warnHook("parse %s/%s: %v", agent.Name(), hookName, err)
		return nil
	}

	svc, err := getService()
	if err != nil {
		// Repo not initialized — the hook fires from cursor's
		// global config but this directory is not a mainline
		// project. Quiet success, no engine work.
		return nil
	}
	// Surface webhook events as "hook"-source so subscribers can
	// distinguish lifecycle pushes from engine state changes.
	attachWebhookBus(svc, "hook")

	cfg, _ := svc.Store.ReadTeamConfig()
	settings := hooks.DefaultDispatchSettings()
	if cfg != nil {
		settings = hooks.DispatchSettings{
			Enabled:                cfg.Hooks.Enabled,
			AutoSyncOnSessionStart: cfg.Hooks.AutoSyncOnSessionStart,
		}
	}

	bus := svc.Bus // may be nil
	emitter := hookEmitter{bus: bus}
	dispatcher := hooks.NewDispatcher(svc.HookFacade(), emitter, settings)
	dispatcher.Notify = stderrNotifier{}
	if os.Getenv("MAINLINE_HOOKS_DEBUG") != "" {
		dispatcher.Log = stderrLogger{}
	}

	if err := dispatcher.Dispatch(context.Background(), ev); err != nil {
		warnHook("dispatch %s/%s: %v", agent.Name(), hookName, err)
	}

	// If the agent implements HookOutputRenderer, give it a chance
	// to write agent-protocol stdout (e.g. cursor's
	// {"continue":true,"additional_context":"..."}). The renderer
	// reads cached state from dispatcher (LastSync / LastStatus) so
	// no second sync round-trip happens. Errors here are warnings,
	// not fatal — a hook that crashes its own session would be
	// worse than one that silently skips its context injection.
	if r, ok := agent.(hooks.HookOutputRenderer); ok {
		out, err := r.RenderHookOutput(hookName, dispatcher, ev, nil)
		if err != nil {
			warnHook("render %s/%s: %v", agent.Name(), hookName, err)
		} else if len(out) > 0 {
			if _, werr := os.Stdout.Write(out); werr != nil {
				warnHook("stdout %s/%s: %v", agent.Name(), hookName, werr)
			}
		}
	}
	return nil
}

// hookEmitter bridges the engine.EventBus surface into hooks.EventEmitter.
// The hooks package emits a fully-formed DomainEvent (it has agent /
// session_id context); the bus we stored on Service is engine-shaped
// (Emit(name, data)). We translate by promoting Domain context onto
// the envelope.
type hookEmitter struct {
	bus engine.EventBus
}

func (h hookEmitter) Emit(ev hooks.DomainEvent) {
	if h.bus == nil {
		return
	}
	// Round-trip the DomainEvent as the data payload so the
	// envelope gets agent + session_id + occurred_at fields out of
	// the box. The bus marshals data to RawMessage, which preserves
	// the structure for HTTP subscribers.
	h.bus.Emit(ev.Name, ev)
}

type stderrNotifier struct{}

func (stderrNotifier) Notify(line string) {
	if !jsonOutput {
		fmt.Fprintln(os.Stderr, line)
	}
}

type stderrLogger struct{}

func (stderrLogger) Debugf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[hooks debug] "+format+"\n", args...)
}
func (stderrLogger) Infof(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[hooks info] "+format+"\n", args...)
}
func (stderrLogger) Warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[hooks warn] "+format+"\n", args...)
}

func warnHook(format string, args ...any) {
	if os.Getenv("MAINLINE_HOOKS_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "mainline hooks: "+format+"\n", args...)
	}
}

func installationStatusFor(a hooks.Agent, root string) (hooks.InstallationStatus, error) {
	if reporter, ok := a.(hooks.InstallationStatusReporter); ok {
		return reporter.InstallationStatus(root)
	}
	installed, err := a.IsInstalled(root)
	return hooks.InstallationStatus{
		Installed: installed,
		Scope:     "repo-local",
	}, err
}

// -----------------------------------------------------------
// helpers
// -----------------------------------------------------------

// selectAgents resolves the --agent / --all flags into the set of
// Agent implementations to operate on. Returns a friendly error if
// nothing matches so the user can re-run with a valid name.
func selectAgents(name string, all bool) ([]hooks.Agent, error) {
	if all {
		agents := hooks.List()
		if len(agents) == 0 {
			return nil, fmt.Errorf("no agents registered")
		}
		return agents, nil
	}
	if name == "" {
		// Default remains cursor for compatibility with the first
		// shipped hook flow; new agents are selected explicitly or
		// via --all.
		name = "cursor"
	}
	a, ok := hooks.Get(name)
	if !ok {
		known := hooks.Names()
		sort.Strings(known)
		return nil, fmt.Errorf("unknown agent %q (known: %v)", name, known)
	}
	return []hooks.Agent{a}, nil
}

func ensureHooksEnabled() error { return setHooksEnabled(true) }

func setHooksEnabled(enabled bool) error {
	svc, err := getService()
	if err != nil {
		return err
	}
	cfg, err := svc.Store.ReadTeamConfig()
	if err != nil {
		return err
	}
	// First-time write: section is zero values, populate full
	// defaults THEN flip enabled. Without this, setHooksEnabled(true)
	// on a never-installed repo would leave AutoSyncOnSessionStart
	// false, defeating the only mechanical auto-flow toggle.
	if !cfg.Hooks.Enabled && !cfg.Hooks.AutoSyncOnSessionStart {
		cfg.Hooks = domain.DefaultHooksSection()
	}
	cfg.Hooks.Enabled = enabled
	if err := svc.Store.WriteTeamConfig(cfg); err != nil {
		return err
	}
	if jsonOutput {
		// Echo the resulting [hooks] section so the caller does
		// not need a follow-up `mainline hooks status --json` to
		// confirm the toggle took effect.
		outputJSON(map[string]any{
			"enabled":  enabled,
			"settings": cfg.Hooks,
		})
		return nil
	}
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	fmt.Printf("Hooks dispatcher %s.\n", state)
	return nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func init() {
	hooksInstallCmd.Flags().StringVar(&hooksInstallAgent, "agent", "", "agent to install (defaults to cursor)")
	hooksInstallCmd.Flags().BoolVar(&hooksInstallAll, "all", false, "install for every supported agent")
	hooksInstallCmd.Flags().BoolVar(&hooksInstallForce, "force", false, "rewrite mainline-managed entries even if unchanged")
	hooksInstallCmd.Flags().BoolVar(&hooksInstallLocalDev, "local-dev", false, "wrap with `go run .` instead of installed mainline")
	hooksInstallCmd.Flags().StringVar(&hooksInstallBin, "bin", "", "absolute (or relative) path to a prebuilt mainline binary; wrapper will exec it directly")

	hooksUninstallCmd.Flags().StringVar(&hooksUninstallAgent, "agent", "", "agent to uninstall (defaults to cursor)")
	hooksUninstallCmd.Flags().BoolVar(&hooksUninstallAll, "all", false, "uninstall every supported agent")

	hooksCmd.AddCommand(hooksInstallCmd, hooksUninstallCmd, hooksStatusCmd,
		hooksListAgentsCmd, hooksEnableCmd, hooksDisableCmd)

	// Register `mainline hooks <agent> <event>` subtree dynamically
	// from the registry. Adding an agent is one blank import above —
	// no dispatch glue here.
	registerAgentDispatchCommands()
}
