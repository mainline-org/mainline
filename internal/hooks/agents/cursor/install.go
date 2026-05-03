package cursor

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

// hooksFileName is what cursor expects under .cursor/.
const hooksFileName = "hooks.json"

// managedMarker is the sentinel substring inside the hook command
// string that tells uninstall/Install "this entry was written by
// mainline; it is safe to rewrite or remove". User-installed hooks
// will not contain this substring (unless the user is deliberately
// pretending to be mainline, in which case they get what they asked
// for). Centralized so the same string is recognized by every helper.
const managedMarker = "hooks cursor "

// Install implements hooks.Agent. Writes / merges .cursor/hooks.json
// so cursor invokes `mainline hooks cursor <event>` at the relevant
// lifecycle points. Existing user-installed hook entries are
// preserved verbatim — we only touch entries whose command contains
// our managedMarker.
//
// Behaviour:
//   - File missing: create with version=1, our 5 hook entries.
//   - File present: parse as map[string]RawMessage to keep unknown
//     top-level fields intact; for each event we manage, decode
//     just that array, drop existing managed entries (when Force
//     or when re-running install with a changed wrapper), append
//     our entry, re-encode.
//   - Round-trip preserves any unknown hook event keys (e.g. the
//     user's preToolUse) and any unknown top-level fields.
func (Agent) Install(repoRoot string, opts hooks.InstallOptions) (hooks.InstallReport, error) {
	opts = hooks.ResolveInstallOptions(repoRoot, opts)
	report := hooks.InstallReport{Scope: "repo-local", RestartRequired: true, CommandMode: hooks.InstallCommandMode(opts)}
	hooksPath := filepath.Join(repoRoot, ".cursor", hooksFileName)

	rawTop, rawHooks, fileExisted, err := loadHooksJSON(hooksPath)
	if err != nil {
		return report, err
	}

	managedCmds := allManagedPrefixes()

	changed := false
	hookCount := 0
	for cliName, nativeKey := range nativeHookKey {
		entries := decodeEntries(rawHooks[nativeKey])
		// Drop any prior mainline-managed entries. We don't compare
		// against the exact current command — if a release changed
		// the wrapper (e.g. switched local-dev path), Install with
		// or without --force should converge on the new command.
		// User-installed entries are left alone by isManaged().
		filtered := make([]hookEntry, 0, len(entries))
		removed := 0
		for _, e := range entries {
			if isManagedEntry(e, managedCmds) {
				removed++
				continue
			}
			filtered = append(filtered, e)
		}
		// Append our wrapper. If --force or we removed an old one,
		// this is a rewrite; otherwise it's a fresh install. Either
		// way the resulting state is the same.
		filtered = append(filtered, hookEntry{
			Command: wrapperCommand(opts, cliName),
		})
		hookCount++
		if removed != 1 || !fileExisted {
			changed = true
		}
		// Even when removed==1 and we appended the same cmd, JSON
		// encoding may differ from disk; just write and let the
		// outer "did the file content change?" check decide.
		encoded, err := encodeEntries(filtered)
		if err != nil {
			return report, fmt.Errorf("encode %s entries: %w", nativeKey, err)
		}
		rawHooks[nativeKey] = encoded
	}

	// Marshal the full file. Use a stable key order so re-running
	// install on an unchanged repo produces a byte-identical file
	// (otherwise the `mainline init` snapshot contract would flag
	// ".cursor/hooks.json modified" every time).
	rawTop["version"] = json.RawMessage(`1`)
	rawTop["hooks"] = mustMarshalSorted(rawHooks)
	out := mustMarshalSorted(rawTop)

	prev, _ := os.ReadFile(hooksPath)
	if !changed && string(prev) == string(out) {
		report.AlreadyInstalled = true
		report.HookCount = hookCount
		report.Files = []string{hooksPath}
		return report, nil
	}

	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		return report, fmt.Errorf("create .cursor dir: %w", err)
	}
	if err := os.WriteFile(hooksPath, append(out, '\n'), 0o644); err != nil {
		return report, fmt.Errorf("write %s: %w", hooksPath, err)
	}
	report.Files = []string{hooksPath}
	report.HookCount = hookCount
	return report, nil
}

// Uninstall implements hooks.Agent. Removes only mainline-managed
// entries. If after removal the file has no hook arrays left, deletes
// the file (and parent dir if empty) so a clean uninstall leaves the
// repo as if cursor support was never touched.
func (Agent) Uninstall(repoRoot string) error {
	hooksPath := filepath.Join(repoRoot, ".cursor", hooksFileName)
	rawTop, rawHooks, fileExisted, err := loadHooksJSON(hooksPath)
	if err != nil {
		return err
	}
	if !fileExisted {
		return nil
	}

	for _, nativeKey := range nativeHookKey {
		entries := decodeEntries(rawHooks[nativeKey])
		filtered := entries[:0]
		for _, e := range entries {
			if isManagedEntry(e, allManagedPrefixes()) {
				continue
			}
			filtered = append(filtered, e)
		}
		if len(filtered) == 0 {
			delete(rawHooks, nativeKey)
		} else {
			encoded, err := encodeEntries(filtered)
			if err != nil {
				return fmt.Errorf("encode %s: %w", nativeKey, err)
			}
			rawHooks[nativeKey] = encoded
		}
	}

	// If hooks map is empty and the only top-level key was version,
	// the file no longer carries information — remove it. Otherwise
	// keep it so unrelated user content survives.
	if len(rawHooks) == 0 {
		delete(rawTop, "hooks")
	} else {
		rawTop["hooks"] = mustMarshalSorted(rawHooks)
	}
	if onlyHasKey(rawTop, "version") || len(rawTop) == 0 {
		os.Remove(hooksPath)
		// Try to remove the parent dir; ignore error (it may have
		// other contents like rules/).
		os.Remove(filepath.Dir(hooksPath))
		return nil
	}

	out := mustMarshalSorted(rawTop)
	if err := os.WriteFile(hooksPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", hooksPath, err)
	}
	return nil
}

// IsInstalled implements hooks.Agent. Returns true iff the on-disk
// .cursor/hooks.json contains at least one hook entry whose command
// is mainline-managed. We check the file even when it does exist
// because users can mix-and-match: cursor file may exist for unrelated
// hooks, with no mainline integration installed.
func (Agent) IsInstalled(repoRoot string) (bool, error) {
	st, err := (Agent{}).InstallationStatus(repoRoot)
	return st.Installed, err
}

func (Agent) InstallationStatus(repoRoot string) (hooks.InstallationStatus, error) {
	hooksPath := filepath.Join(repoRoot, ".cursor", hooksFileName)
	st := hooks.InstallationStatus{
		Scope:             "repo-local",
		Files:             []string{hooksPath},
		ExpectedHookCount: len(nativeHookKey),
	}
	_, rawHooks, fileExisted, err := loadHooksJSON(hooksPath)
	if err != nil {
		return st, err
	}
	if !fileExisted {
		return st, nil
	}
	prefixes := allManagedPrefixes()
	expected := expectedNativeHookKeys()
	for nativeKey, raw := range rawHooks {
		managedForKey := 0
		for _, e := range decodeEntries(raw) {
			if isManagedEntry(e, prefixes) {
				managedForKey++
				st.CommandMode = hooks.MergeCommandMode(st.CommandMode, hooks.WrapperCommandMode(e.Command))
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
		if countManagedCursor(rawHooks[nativeKey], prefixes) == 0 {
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

// -----------------------------------------------------------
// Private helpers
// -----------------------------------------------------------

// hookEntry is one element of a cursor hook array. Cursor allows an
// optional `matcher` for tool-use hooks; we don't set it (we don't
// install preToolUse/postToolUse), but we round-trip it so user
// entries with matchers don't get clobbered.
type hookEntry struct {
	Command string `json:"command"`
	Matcher string `json:"matcher,omitempty"`
}

// loadHooksJSON returns (top-level map, hooks-section map, fileExisted, err).
// On a missing file we return ((empty), (empty), false, nil) so callers can
// treat "no file" as "fresh install".
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

func decodeEntries(raw json.RawMessage) []hookEntry {
	if len(raw) == 0 {
		return nil
	}
	var entries []hookEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		// Unknown shape — return empty so we don't drop them on
		// uninstall. Round-trip preservation is not critical here
		// because we only call decodeEntries on event keys we
		// manage; keys we don't manage are kept as raw bytes by
		// the parent map.
		return nil
	}
	return entries
}

func encodeEntries(entries []hookEntry) (json.RawMessage, error) {
	if len(entries) == 0 {
		return json.RawMessage("[]"), nil
	}
	return json.Marshal(entries)
}

// wrapperCommand returns the full command string for one cursor hook
// entry, with the hook id (e.g. "session-start") baked in.
//
// Three shapes, in priority order:
//
//   - BinPath set: hard-code the absolute binary path. Used when the
//     user has built a local mainline (e.g. `go build -o ./mainline .`)
//     and wants hooks to invoke it without putting `mainline` on PATH.
//     Tests this exact path on every fire — fastest, most reliable for
//     dogfooding. Still fail-soft: if the binary disappears, the test
//     short-circuits and `|| exit 0` keeps cursor happy.
//
//   - LocalDev set: `go run .` from the worktree root. Picks up source
//     changes without a rebuild step but pays a 1-3s compile cost on
//     every hook fire — fine for slow events (sessionStart) but
//     painful on beforeSubmitPrompt where the user waits for the
//     prompt to dispatch.
//
//   - Default: `command -v mainline` on PATH. Production form for
//     users who `go install` mainline. Fail-soft via `|| exit 0` so
//     a system without mainline doesn't break cursor.
func wrapperCommand(opts hooks.InstallOptions, hookID string) string {
	switch {
	case opts.BinPath != "":
		return fmt.Sprintf(`sh -c 'test -x %q && exec %q hooks cursor %s || exit 0'`,
			opts.BinPath, opts.BinPath, hookID)
	case opts.LocalDev:
		return fmt.Sprintf(`sh -c 'cd "$(git rev-parse --show-toplevel)" && export GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/mainline-go-build}" && exec go run . hooks cursor %s || exit 0'`, hookID)
	default:
		return fmt.Sprintf(`sh -c 'command -v mainline >/dev/null 2>&1 && exec mainline hooks cursor %s || exit 0'`, hookID)
	}
}

func expectedNativeHookKeys() map[string]bool {
	out := make(map[string]bool, len(nativeHookKey))
	for _, nativeKey := range nativeHookKey {
		out[nativeKey] = true
	}
	return out
}

func countManagedCursor(raw json.RawMessage, prefixes []string) int {
	count := 0
	for _, e := range decodeEntries(raw) {
		if isManagedEntry(e, prefixes) {
			count++
		}
	}
	return count
}

// allManagedPrefixes is the union of every wrapper-command substring
// mainline has ever written. Install removes prior mainline-managed
// entries before appending the new one; Uninstall removes ALL
// mainline-managed entries; IsInstalled detects ANY.
//
// Two shapes recognized:
//   - `hooks cursor ` covers the PATH form and the absolute-path
//     --bin form, where the quoted binary path can interrupt the
//     literal `mainline hooks cursor` substring.
//   - `go run . hooks cursor ` covers --local-dev.
func allManagedPrefixes() []string {
	return []string{
		managedMarker,
		`go run . hooks cursor `,
	}
}

func isManagedEntry(e hookEntry, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.Contains(e.Command, p) {
			return true
		}
	}
	return false
}

// mustMarshalSorted marshals m with stable (alphabetical) key order
// so install on an unchanged repo produces byte-identical output.
// Without sorting, Go's map iteration randomization would dirty the
// file every other run, breaking the v0.3 snapshot contract.
//
// Output is pretty-printed (2-space indent) via json.Indent over the
// already-sorted compact form so re-formatting does not undo the key
// ordering — encoding/json's MarshalIndent on a map would re-randomize.
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
		// json.Indent only fails on invalid input; if it does, the
		// caller's downstream WriteFile will surface the bug.
		return []byte(compact.String())
	}
	return out.Bytes()
}

// onlyHasKey reports whether m's only key is name. Used by uninstall
// to decide between rewriting the file and removing it entirely.
func onlyHasKey(m map[string]json.RawMessage, name string) bool {
	if len(m) != 1 {
		return false
	}
	_, ok := m[name]
	return ok
}
