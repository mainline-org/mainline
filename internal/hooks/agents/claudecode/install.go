package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mainline-org/mainline/internal/hooks"
)

const (
	claudeDirName      = ".claude"
	settingsFileName   = "settings.json"
	managedCommandMark = "hooks claudecode "
)

func (Agent) Install(repoRoot string, opts hooks.InstallOptions) (hooks.InstallReport, error) {
	opts = hooks.ResolveInstallOptions(repoRoot, opts)
	report := hooks.InstallReport{Scope: "repo-local", RestartRequired: true, CommandMode: hooks.InstallCommandMode(opts)}
	settingsPath := filepath.Join(repoRoot, claudeDirName, settingsFileName)

	rawTop, rawHooks, fileExisted, err := loadSettingsJSON(settingsPath)
	if err != nil {
		return report, err
	}

	changed := false
	hookCount := 0
	for cliName, nativeKey := range nativeHookKey {
		groups := decodeGroups(rawHooks[nativeKey])
		filtered := make([]hookGroup, 0, len(groups))
		removed := 0
		managedSame := false
		desiredCommand := wrapperCommand(opts, cliName)
		for _, g := range groups {
			for _, h := range g.Hooks {
				if isManagedHook(h) && h.Command == desiredCommand {
					managedSame = true
				}
			}
			g.Hooks = filterManagedHooks(g.Hooks, &removed)
			if len(g.Hooks) > 0 {
				filtered = append(filtered, g)
			}
		}
		filtered = append(filtered, hookGroup{
			Hooks: []commandHook{{
				Type:    "command",
				Command: desiredCommand,
			}},
		})
		hookCount++
		if !fileExisted || removed != 1 || !managedSame {
			changed = true
		}
		encoded, err := encodeGroups(filtered)
		if err != nil {
			return report, fmt.Errorf("encode %s hooks: %w", nativeKey, err)
		}
		rawHooks[nativeKey] = encoded
	}

	rawTop["hooks"] = mustMarshalSorted(rawHooks)
	out := mustMarshalSorted(rawTop)
	prev, _ := os.ReadFile(settingsPath)
	if !changed && string(prev) == string(append(out, '\n')) {
		report.AlreadyInstalled = true
		report.HookCount = hookCount
		report.Files = []string{settingsPath}
		return report, nil
	}

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return report, fmt.Errorf("create .claude dir: %w", err)
	}
	if err := os.WriteFile(settingsPath, append(out, '\n'), 0o644); err != nil {
		return report, fmt.Errorf("write %s: %w", settingsPath, err)
	}
	report.Files = []string{settingsPath}
	report.HookCount = hookCount
	return report, nil
}

func (Agent) Uninstall(repoRoot string) error {
	settingsPath := filepath.Join(repoRoot, claudeDirName, settingsFileName)
	rawTop, rawHooks, fileExisted, err := loadSettingsJSON(settingsPath)
	if err != nil {
		return err
	}
	if !fileExisted {
		return nil
	}

	for _, nativeKey := range nativeHookKey {
		groups := decodeGroups(rawHooks[nativeKey])
		filteredGroups := make([]hookGroup, 0, len(groups))
		for _, g := range groups {
			removed := 0
			g.Hooks = filterManagedHooks(g.Hooks, &removed)
			if len(g.Hooks) > 0 {
				filteredGroups = append(filteredGroups, g)
			}
		}
		if len(filteredGroups) == 0 {
			delete(rawHooks, nativeKey)
		} else {
			encoded, err := encodeGroups(filteredGroups)
			if err != nil {
				return fmt.Errorf("encode %s hooks: %w", nativeKey, err)
			}
			rawHooks[nativeKey] = encoded
		}
	}

	if len(rawHooks) == 0 {
		delete(rawTop, "hooks")
	} else {
		rawTop["hooks"] = mustMarshalSorted(rawHooks)
	}
	if len(rawTop) == 0 {
		os.Remove(settingsPath)
		os.Remove(filepath.Dir(settingsPath))
		return nil
	}

	out := mustMarshalSorted(rawTop)
	if err := os.WriteFile(settingsPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", settingsPath, err)
	}
	return nil
}

func (Agent) IsInstalled(repoRoot string) (bool, error) {
	st, err := (Agent{}).InstallationStatus(repoRoot)
	return st.Installed, err
}

func (Agent) InstallationStatus(repoRoot string) (hooks.InstallationStatus, error) {
	settingsPath := filepath.Join(repoRoot, claudeDirName, settingsFileName)
	st := hooks.InstallationStatus{
		Scope:             "repo-local",
		Files:             []string{settingsPath},
		ExpectedHookCount: len(nativeHookKey),
	}
	_, rawHooks, fileExisted, err := loadSettingsJSON(settingsPath)
	if err != nil {
		return st, err
	}
	if !fileExisted {
		return st, nil
	}
	expected := expectedNativeHookKeys()
	for nativeKey, raw := range rawHooks {
		managedForKey := 0
		for _, g := range decodeGroups(raw) {
			for _, h := range g.Hooks {
				if isManagedHook(h) {
					managedForKey++
					st.CommandMode = hooks.MergeCommandMode(st.CommandMode, hooks.WrapperCommandMode(h.Command))
				}
			}
		}
		if managedForKey == 0 {
			continue
		}
		st.HookCount += managedForKey
		if !expected[nativeKey] {
			st.RepairReasons = append(st.RepairReasons, fmt.Sprintf("unexpected mainline hook under %s", nativeKey))
		} else if managedForKey > 1 {
			st.RepairReasons = append(st.RepairReasons, fmt.Sprintf("duplicate mainline hooks under %s", nativeKey))
		}
	}
	for _, nativeKey := range nativeHookKey {
		if countManagedClaude(rawHooks[nativeKey]) == 0 {
			st.RepairReasons = append(st.RepairReasons, fmt.Sprintf("missing mainline hook under %s", nativeKey))
		}
	}
	st.Installed = st.HookCount > 0
	st.RestartRequired = st.Installed
	if !st.Installed {
		st.RepairReasons = nil
	}
	if reason := hooks.RuntimeRepairReason(st.CommandMode); reason != "" {
		st.RepairReasons = append(st.RepairReasons, reason)
	}
	st.NeedsRepair = len(st.RepairReasons) > 0
	return st, nil
}

type hookGroup struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []commandHook `json:"hooks"`
}

type commandHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

func loadSettingsJSON(path string) (map[string]json.RawMessage, map[string]json.RawMessage, bool, error) {
	rawTop := map[string]json.RawMessage{}
	rawHooks := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return rawTop, rawHooks, false, nil
		}
		return nil, nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return rawTop, rawHooks, true, nil
	}
	if err := json.Unmarshal(data, &rawTop); err != nil {
		return nil, nil, true, fmt.Errorf("parse %s: %w", path, err)
	}
	if h, ok := rawTop["hooks"]; ok && len(h) > 0 {
		if err := json.Unmarshal(h, &rawHooks); err != nil {
			return nil, nil, true, fmt.Errorf("parse hooks section in %s: %w", path, err)
		}
	}
	return rawTop, rawHooks, true, nil
}

func decodeGroups(raw json.RawMessage) []hookGroup {
	if len(raw) == 0 {
		return nil
	}
	var groups []hookGroup
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil
	}
	return groups
}

func encodeGroups(groups []hookGroup) (json.RawMessage, error) {
	if len(groups) == 0 {
		return json.RawMessage("[]"), nil
	}
	return json.Marshal(groups)
}

func filterManagedHooks(in []commandHook, removed *int) []commandHook {
	out := make([]commandHook, 0, len(in))
	for _, h := range in {
		if isManagedHook(h) {
			(*removed)++
			continue
		}
		out = append(out, h)
	}
	return out
}

func isManagedHook(h commandHook) bool {
	for _, p := range allManagedPrefixes() {
		if strings.Contains(h.Command, p) {
			return true
		}
	}
	return false
}

func wrapperCommand(opts hooks.InstallOptions, hookID string) string {
	switch {
	case opts.BinPath != "":
		return fmt.Sprintf(`sh -c 'test -x %q && exec %q hooks claudecode %s || exit 0'`,
			opts.BinPath, opts.BinPath, hookID)
	case opts.LocalDev:
		return fmt.Sprintf(`sh -c 'cd "$(git rev-parse --show-toplevel)" && export GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/mainline-go-build}" && exec go run . hooks claudecode %s || exit 0'`, hookID)
	default:
		return fmt.Sprintf(`sh -c 'command -v mainline >/dev/null 2>&1 && exec mainline hooks claudecode %s || exit 0'`, hookID)
	}
}

func expectedNativeHookKeys() map[string]bool {
	out := make(map[string]bool, len(nativeHookKey))
	for _, nativeKey := range nativeHookKey {
		out[nativeKey] = true
	}
	return out
}

func countManagedClaude(raw json.RawMessage) int {
	count := 0
	for _, g := range decodeGroups(raw) {
		for _, h := range g.Hooks {
			if isManagedHook(h) {
				count++
			}
		}
	}
	return count
}

func allManagedPrefixes() []string {
	return []string{
		managedCommandMark,
		`go run . hooks claudecode `,
	}
}

func mustMarshalSorted(m map[string]json.RawMessage) []byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var compact strings.Builder
	compact.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			compact.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		compact.Write(kb)
		compact.WriteByte(':')
		compact.Write(m[k])
	}
	compact.WriteByte('}')

	var out bytes.Buffer
	if err := json.Indent(&out, []byte(compact.String()), "", "  "); err != nil {
		return []byte(compact.String())
	}
	return out.Bytes()
}
