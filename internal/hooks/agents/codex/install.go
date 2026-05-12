package codex

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
	codexDirName   = ".codex"
	hooksFileName  = "hooks.json"
	configFileName = "config.toml"
)

func (Agent) Install(repoRoot string, opts hooks.InstallOptions) (hooks.InstallReport, error) {
	opts = hooks.ResolveInstallOptions(repoRoot, opts)
	report := hooks.InstallReport{Scope: "repo-local", RestartRequired: true, CommandMode: hooks.InstallCommandMode(opts)}
	hooksPath := filepath.Join(repoRoot, codexDirName, hooksFileName)
	configPath := filepath.Join(repoRoot, codexDirName, configFileName)

	rawTop, rawHooks, fileExisted, err := loadHooksJSON(hooksPath)
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
			Matcher: matcherFor(nativeKey),
			Hooks: []commandHook{{
				Type:          "command",
				Command:       desiredCommand,
				StatusMessage: statusMessageFor(nativeKey),
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

	files := []string{hooksPath}
	configChanged, err := ensureCodexHooksFeature(configPath)
	if err != nil {
		return report, err
	}
	if configChanged {
		files = append(files, configPath)
	}

	if !changed && !configChanged {
		report.AlreadyInstalled = true
		report.HookCount = hookCount
		report.Files = files
		return report, nil
	}

	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		return report, fmt.Errorf("create .codex dir: %w", err)
	}
	if err := os.WriteFile(hooksPath, append(out, '\n'), 0o644); err != nil {
		return report, fmt.Errorf("write %s: %w", hooksPath, err)
	}
	report.Files = files
	report.HookCount = hookCount
	return report, nil
}

func (Agent) Uninstall(repoRoot string) error {
	hooksPath := filepath.Join(repoRoot, codexDirName, hooksFileName)
	rawTop, rawHooks, fileExisted, err := loadHooksJSON(hooksPath)
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
		os.Remove(hooksPath)
		os.Remove(filepath.Dir(hooksPath))
		return nil
	}

	out := mustMarshalSorted(rawTop)
	if err := os.WriteFile(hooksPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", hooksPath, err)
	}
	return nil
}

func (Agent) IsInstalled(repoRoot string) (bool, error) {
	st, err := (Agent{}).InstallationStatus(repoRoot)
	return st.Installed, err
}

func (Agent) InstallationStatus(repoRoot string) (hooks.InstallationStatus, error) {
	hooksPath := filepath.Join(repoRoot, codexDirName, hooksFileName)
	configPath := filepath.Join(repoRoot, codexDirName, configFileName)
	st := hooks.InstallationStatus{
		Scope:             "repo-local",
		Files:             []string{hooksPath, configPath},
		ExpectedHookCount: len(nativeHookKey),
	}
	_, rawHooks, fileExisted, err := loadHooksJSON(hooksPath)
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
		if countManagedCodex(rawHooks[nativeKey]) == 0 {
			st.RepairReasons = append(st.RepairReasons, fmt.Sprintf("missing mainline hook under %s", nativeKey))
		}
	}
	featureEnabled, legacyFeaturePresent := codexHooksFeatureState(configPath)
	if !featureEnabled {
		st.RepairReasons = append(st.RepairReasons, "hooks feature is disabled or missing")
	}
	if legacyFeaturePresent {
		st.RepairReasons = append(st.RepairReasons, "legacy codex_hooks feature flag is present")
	}
	if reason := hooks.RuntimeRepairReason(st.CommandMode); reason != "" {
		st.RepairReasons = append(st.RepairReasons, reason)
	}
	st.Installed = st.HookCount > 0
	st.RestartRequired = st.Installed
	if !st.Installed {
		st.RepairReasons = nil
	}
	st.NeedsRepair = len(st.RepairReasons) > 0
	return st, nil
}

type hookGroup struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []commandHook `json:"hooks"`
}

type commandHook struct {
	Type          string `json:"type"`
	Command       string `json:"command"`
	StatusMessage string `json:"statusMessage,omitempty"`
	Timeout       int    `json:"timeout,omitempty"`
}

func loadHooksJSON(path string) (map[string]json.RawMessage, map[string]json.RawMessage, bool, error) {
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
		return fmt.Sprintf(`sh -c 'test -x %q && exec %q hooks codex %s || exit 0'`,
			opts.BinPath, opts.BinPath, hookID)
	case opts.LocalDev:
		return fmt.Sprintf(`sh -c 'cd "$(git rev-parse --show-toplevel)" && export GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/mainline-go-build}" && exec go run . hooks codex %s || exit 0'`, hookID)
	default:
		return fmt.Sprintf(`sh -c 'command -v mainline >/dev/null 2>&1 && exec mainline hooks codex %s || exit 0'`, hookID)
	}
}

func expectedNativeHookKeys() map[string]bool {
	out := make(map[string]bool, len(nativeHookKey))
	for _, nativeKey := range nativeHookKey {
		out[nativeKey] = true
	}
	return out
}

func countManagedCodex(raw json.RawMessage) int {
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

func codexHooksFeatureState(path string) (enabled bool, legacyPresent bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	text := string(data)
	return hasHooksFeatureEnabled(text), hasLegacyCodexHooksFeature(text)
}

func allManagedPrefixes() []string {
	return []string{
		`hooks codex `,
		`go run . hooks codex `,
	}
}

func matcherFor(nativeKey string) string {
	if nativeKey == "SessionStart" {
		return "startup|resume"
	}
	return ""
}

func statusMessageFor(nativeKey string) string {
	if nativeKey == "SessionStart" {
		return "Loading Mainline context"
	}
	return ""
}

func ensureCodexHooksFeature(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("read %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return false, fmt.Errorf("create .codex dir: %w", err)
		}
		return true, os.WriteFile(path, []byte("[features]\nhooks = true\n"), 0o644)
	}
	text := string(data)
	next := setHooksFeatureEnabled(text)
	if next == text {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

func hasHooksFeatureEnabled(text string) bool {
	found := false
	walkFeatures(text, func(key, value string) bool {
		if key == "hooks" {
			found = value == "true"
			return false
		}
		return true
	})
	return found
}

func hasLegacyCodexHooksFeature(text string) bool {
	found := false
	walkFeatures(text, func(key, _ string) bool {
		if key == "codex_hooks" {
			found = true
			return false
		}
		return true
	})
	return found
}

func walkFeatures(text string, visit func(key, value string) bool) {
	inFeatures := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inFeatures = trimmed == "[features]"
			continue
		}
		if !inFeatures || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, ok := parseFeatureAssignment(trimmed)
		if !ok {
			continue
		}
		if !visit(key, value) {
			return
		}
	}
}

func setHooksFeatureEnabled(text string) string {
	lines := strings.Split(text, "\n")
	inFeatures := false
	featuresSeen := false
	hooksSeen := false
	changed := false
	out := make([]string, 0, len(lines)+2)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inFeatures {
				if !hooksSeen {
					out = insertBeforeTrailingBlank(out, "hooks = true")
					changed = true
				}
				if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
					out = append(out, "")
				}
			}
			inFeatures = trimmed == "[features]"
			if inFeatures {
				featuresSeen = true
				hooksSeen = false
			}
			out = append(out, line)
			continue
		}
		if inFeatures {
			key, value, ok := parseFeatureAssignment(trimmed)
			if ok && key == "codex_hooks" {
				changed = true
				continue
			}
			if ok && key == "hooks" {
				hooksSeen = true
				if value != "true" {
					out = append(out, "hooks = true")
					changed = true
					continue
				}
			}
		}
		out = append(out, line)
	}
	if inFeatures {
		if !hooksSeen {
			out = insertBeforeTrailingBlank(out, "hooks = true")
			changed = true
		}
	}
	if featuresSeen {
		if !changed {
			return text
		}
		return ensureTrailingNewline(strings.Join(out, "\n"))
	}
	prefix := ""
	if strings.TrimSpace(text) == "" {
		return "[features]\nhooks = true\n"
	}
	if !strings.HasSuffix(text, "\n") {
		prefix = "\n"
	}
	return ensureTrailingNewline(text + prefix + "\n[features]\nhooks = true")
}

func parseFeatureAssignment(trimmed string) (key string, value string, ok bool) {
	parts := strings.SplitN(trimmed, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key = strings.TrimSpace(parts[0])
	value = strings.TrimSpace(parts[1])
	if beforeComment, _, found := strings.Cut(value, "#"); found {
		value = strings.TrimSpace(beforeComment)
	}
	return key, value, key != ""
}

func insertBeforeTrailingBlank(lines []string, value string) []string {
	i := len(lines)
	for i > 0 && strings.TrimSpace(lines[i-1]) == "" {
		i--
	}
	lines = append(lines, "")
	copy(lines[i+1:], lines[i:])
	lines[i] = value
	return lines
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
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
