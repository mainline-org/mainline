package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// -----------------------------------------------------------
// Managed-block model for AGENTS.md
// -----------------------------------------------------------
//
// Mainline owns ONLY a versioned, checksummed block inside the user's
// AGENTS.md (and the IDE pointer stubs). The user owns the file. This
// distinction is the contract that lets us update agent guidance on
// every binary release without ever silently overwriting user-edited
// rules.
//
// Marker format (post this PR):
//
//   <!-- mainline:agents:start version=N checksum=sha256:HEX -->
//   ...rendered template body...
//   <!-- mainline:agents:end -->
//
//   - version=N is the integer that lives in the embedded template's
//     `<!-- mainline-agents-md-version: N -->` line. It moves up only
//     when the template body changes meaningfully.
//   - checksum=sha256:HEX is the sha256 of the rendered body bytes
//     between markers (exclusive). It detects local edits the user
//     made INSIDE the managed block — even when the version still
//     matches what was installed.
//
// Pre-this-PR repos use the older `<!-- mainline:begin -->` / `<!-- mainline:end -->`
// markers without metadata. They are recognised here as "legacy" state
// so `mainline agents update` can migrate in place without losing
// surrounding user content.

const (
	managedMarkerStartPrefix = "<!-- mainline:agents:start"
	managedMarkerStartSuffix = "-->"
	managedMarkerEnd         = "<!-- mainline:agents:end -->"
)

// AgentsBlockState classifies the current state of the managed block
// in AGENTS.md.
type AgentsBlockState string

const (
	// AgentsBlockStateNotInstalled — the file is missing OR present
	// but contains no Mainline marker (legacy or modern). The user
	// must run `mainline agents install` to add the block.
	AgentsBlockStateNotInstalled AgentsBlockState = "not_installed"

	// AgentsBlockStateLegacy — the file contains the pre-v0.4
	// `<!-- mainline:begin -->` markers without version/checksum
	// metadata. `mainline agents update` migrates to the new form.
	AgentsBlockStateLegacy AgentsBlockState = "legacy"

	// AgentsBlockStateInSync — installed block matches the embedded
	// template byte-for-byte (version equal AND checksum equal).
	AgentsBlockStateInSync AgentsBlockState = "in_sync"

	// AgentsBlockStateUpdateAvailable — installed version is older
	// than the embedded template and the body checksum still matches
	// what we installed (user has not edited inside the block). Safe
	// to auto-update.
	AgentsBlockStateUpdateAvailable AgentsBlockState = "update_available"

	// AgentsBlockStateLocallyModified — installed checksum does not
	// match what we would have written for the recorded version.
	// Either the user edited the block, or the file was rewritten by
	// a different tool. Update refuses to overwrite without a
	// disambiguating flag.
	AgentsBlockStateLocallyModified AgentsBlockState = "locally_modified"
)

// StatusAgentsGuidance is the per-status rollup of the managed-block
// state. JSON callers introspect; the CLI's Suggestions block picks a
// call-to-action based on State.
type StatusAgentsGuidance struct {
	Path             string           `json:"path"`
	State            AgentsBlockState `json:"state"`
	InstalledVersion int              `json:"installed_version"`
	CurrentVersion   int              `json:"current_version"`
}

// AgentsManagedBlock is the parsed form of the on-disk managed block.
// Returned by inspectManagedBlock so callers can branch cleanly on
// state without re-parsing the file.
type AgentsManagedBlock struct {
	Path             string
	State            AgentsBlockState
	InstalledVersion int
	InstalledChecksum string
	BodyBytes        string // empty unless State is one of the installed forms
	OuterStart       int    // byte index of the start marker, -1 if absent
	OuterEnd         int    // byte index just after the end marker, -1 if absent
	FileBytes        string // entire file contents (for splice-replace operations)
}

// AgentsCheckResult is the per-file rollup returned by
// `mainline agents check`. One file per managed target (AGENTS.md +
// the four IDE stubs).
type AgentsCheckResult struct {
	Files            []AgentsFileState `json:"files"`
	CurrentVersion   int               `json:"current_version"`
}

// AgentsFileState is one row of AgentsCheckResult.
type AgentsFileState struct {
	Path             string           `json:"path"`
	State            AgentsBlockState `json:"state"`
	InstalledVersion int              `json:"installed_version"`
}

// AgentsInstallResult / AgentsUpdateResult / AgentsDiffResult are the
// returns of the matching service methods. Each carries per-file
// detail so the CLI can render exactly what changed.
type AgentsInstallResult struct {
	Files          []AgentsFileChange `json:"files"`
	CurrentVersion int                `json:"current_version"`
}
type AgentsUpdateResult struct {
	Files          []AgentsFileChange `json:"files"`
	CurrentVersion int                `json:"current_version"`
}
type AgentsDiffResult struct {
	Files          []AgentsFileDiff `json:"files"`
	CurrentVersion int              `json:"current_version"`
}

// AgentsFileChange records one file's transition in install/update.
type AgentsFileChange struct {
	Path     string           `json:"path"`
	From     AgentsBlockState `json:"from"`
	To       AgentsBlockState `json:"to"`
	Action   string           `json:"action"` // "installed" | "updated" | "migrated" | "skipped" | "refused"
	Reason   string           `json:"reason,omitempty"`
}

// AgentsFileDiff carries the old/new bodies for a single file.
type AgentsFileDiff struct {
	Path  string `json:"path"`
	State AgentsBlockState `json:"state"`
	Old   string           `json:"old"`
	New   string           `json:"new"`
}

// AgentsUpdateOptions controls the conflict-resolution policy when
// the managed block has been locally modified. Default (zero value)
// is the safest path: refuse to overwrite.
type AgentsUpdateOptions struct {
	// Theirs: when true, replace the locally-modified block with the
	// embedded template (`--theirs` in the CLI). User edits inside
	// the managed block are lost. Use deliberately.
	Theirs bool
}

// agentsManagedTargets is the canonical list of files this command
// family manages. Same as the legacy upsertAgentInstructionStubs
// targets plus AGENTS.md itself.
//
// Each gets its own managed block; the block content differs (full
// guidance for AGENTS.md, short pointer stub for the IDE files) but
// the install/check/update/diff flow is identical.
type agentsTarget struct {
	relPath  string
	body     string // embedded template body (no markers)
}

func (s *Service) agentsManagedTargets() []agentsTarget {
	full := strings.TrimRight(agentsMDTemplate, "\n")
	stub := strings.TrimRight(agentsStubTemplate, "\n")
	return []agentsTarget{
		{"AGENTS.md", full},
		{"CLAUDE.md", stub},
		{".cursor/rules/mainline.md", stub},
		{".windsurfrules", stub},
		{".github/copilot-instructions.md", stub},
	}
}

// -----------------------------------------------------------
// Service entry points
// -----------------------------------------------------------

// AgentsCheck reports the state of every managed target without
// touching the filesystem.
func (s *Service) AgentsCheck() (*AgentsCheckResult, error) {
	current := EmbeddedAgentsMDVersion()
	out := &AgentsCheckResult{CurrentVersion: current}
	for _, t := range s.agentsManagedTargets() {
		blk := inspectManagedBlock(filepath.Join(s.Git.RepoRoot, t.relPath), t.body, current)
		out.Files = append(out.Files, AgentsFileState{
			Path:             t.relPath,
			State:            blk.State,
			InstalledVersion: blk.InstalledVersion,
		})
	}
	return out, nil
}

// AgentsInstall is for first-time install: writes the managed block
// to every target. If a target already has a block, the entry is
// "skipped" with a hint to run `mainline agents update`.
func (s *Service) AgentsInstall() (*AgentsInstallResult, error) {
	current := EmbeddedAgentsMDVersion()
	out := &AgentsInstallResult{CurrentVersion: current}
	for _, t := range s.agentsManagedTargets() {
		fullPath := filepath.Join(s.Git.RepoRoot, t.relPath)
		blk := inspectManagedBlock(fullPath, t.body, current)
		if blk.State == AgentsBlockStateNotInstalled {
			if err := writeManagedBlock(fullPath, t.body, current); err != nil {
				return nil, fmt.Errorf("install %s: %w", t.relPath, err)
			}
			out.Files = append(out.Files, AgentsFileChange{
				Path: t.relPath, From: AgentsBlockStateNotInstalled,
				To: AgentsBlockStateInSync, Action: "installed",
			})
			continue
		}
		out.Files = append(out.Files, AgentsFileChange{
			Path: t.relPath, From: blk.State, To: blk.State, Action: "skipped",
			Reason: "already installed; use `mainline agents update` to refresh",
		})
	}
	return out, nil
}

// AgentsUpdate refreshes the managed block to the current version. If
// the block is locally modified, returns Action="refused" unless
// opts.Theirs is true.
func (s *Service) AgentsUpdate(opts AgentsUpdateOptions) (*AgentsUpdateResult, error) {
	current := EmbeddedAgentsMDVersion()
	out := &AgentsUpdateResult{CurrentVersion: current}
	for _, t := range s.agentsManagedTargets() {
		fullPath := filepath.Join(s.Git.RepoRoot, t.relPath)
		blk := inspectManagedBlock(fullPath, t.body, current)
		switch blk.State {
		case AgentsBlockStateNotInstalled:
			out.Files = append(out.Files, AgentsFileChange{
				Path: t.relPath, From: blk.State, To: blk.State, Action: "skipped",
				Reason: "not installed; run `mainline agents install`",
			})
		case AgentsBlockStateInSync:
			out.Files = append(out.Files, AgentsFileChange{
				Path: t.relPath, From: blk.State, To: blk.State, Action: "skipped",
				Reason: "already in sync",
			})
		case AgentsBlockStateUpdateAvailable, AgentsBlockStateLegacy:
			if err := writeManagedBlock(fullPath, t.body, current); err != nil {
				return nil, fmt.Errorf("update %s: %w", t.relPath, err)
			}
			action := "updated"
			if blk.State == AgentsBlockStateLegacy {
				action = "migrated"
			}
			out.Files = append(out.Files, AgentsFileChange{
				Path: t.relPath, From: blk.State, To: AgentsBlockStateInSync,
				Action: action,
			})
		case AgentsBlockStateLocallyModified:
			if !opts.Theirs {
				out.Files = append(out.Files, AgentsFileChange{
					Path: t.relPath, From: blk.State, To: blk.State, Action: "refused",
					Reason: "agent guidance has local edits; pass --theirs to overwrite",
				})
				continue
			}
			if err := writeManagedBlock(fullPath, t.body, current); err != nil {
				return nil, fmt.Errorf("update %s: %w", t.relPath, err)
			}
			out.Files = append(out.Files, AgentsFileChange{
				Path: t.relPath, From: blk.State, To: AgentsBlockStateInSync,
				Action: "updated", Reason: "--theirs: local edits discarded",
			})
		}
	}
	return out, nil
}

// AgentsDiff returns the old/new body for every target whose block
// would change under update. Targets in InSync are omitted.
func (s *Service) AgentsDiff() (*AgentsDiffResult, error) {
	current := EmbeddedAgentsMDVersion()
	out := &AgentsDiffResult{CurrentVersion: current}
	for _, t := range s.agentsManagedTargets() {
		fullPath := filepath.Join(s.Git.RepoRoot, t.relPath)
		blk := inspectManagedBlock(fullPath, t.body, current)
		if blk.State == AgentsBlockStateInSync {
			continue
		}
		out.Files = append(out.Files, AgentsFileDiff{
			Path:  t.relPath,
			State: blk.State,
			Old:   blk.BodyBytes,
			New:   t.body,
		})
	}
	return out, nil
}

// AgentsGuidanceState is the status-time rollup helper. Reports the
// AGENTS.md state only — the four IDE stubs derive from the same
// template and would just duplicate the signal in the daily-entry
// view.
func (s *Service) AgentsGuidanceState() *StatusAgentsGuidance {
	current := EmbeddedAgentsMDVersion()
	target := s.agentsManagedTargets()[0]
	blk := inspectManagedBlock(filepath.Join(s.Git.RepoRoot, target.relPath), target.body, current)
	return &StatusAgentsGuidance{
		Path:             target.relPath,
		State:            blk.State,
		InstalledVersion: blk.InstalledVersion,
		CurrentVersion:   current,
	}
}

// -----------------------------------------------------------
// Inspection + write primitives
// -----------------------------------------------------------

// reModernMarkerStart parses the new marker form. Captures version
// (group 1) and checksum (group 2). Tolerates extra whitespace.
var reModernMarkerStart = regexp.MustCompile(
	`<!-- mainline:agents:start version=(\d+) checksum=sha256:([0-9a-f]{64}) -->`)

// reLegacyMarkerStart matches the pre-v0.4 marker pair. Used to
// detect Legacy state for migration.
var reLegacyMarkerStart = regexp.MustCompile(`<!-- mainline:begin -->`)

// inspectManagedBlock reads the file at path, classifies the managed
// block state against the embedded template body, and returns the
// rich state for downstream rendering.
//
// Critical: a missing file is NotInstalled, NOT an error. The user
// may genuinely not have AGENTS.md yet.
func inspectManagedBlock(path, embeddedBody string, currentVersion int) AgentsManagedBlock {
	blk := AgentsManagedBlock{
		Path:       path,
		State:      AgentsBlockStateNotInstalled,
		OuterStart: -1, OuterEnd: -1,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return blk
	}
	text := string(data)
	blk.FileBytes = text

	// Modern marker form takes precedence. If we see a modern start
	// AND end, parse the metadata and compute installed body.
	if loc := reModernMarkerStart.FindStringSubmatchIndex(text); loc != nil {
		// loc layout: [start, end, v_start, v_end, c_start, c_end]
		matches := reModernMarkerStart.FindStringSubmatch(text)
		bodyStart := loc[1] // first byte AFTER start marker
		// Skip leading newline that always follows the marker on write.
		if bodyStart < len(text) && text[bodyStart] == '\n' {
			bodyStart++
		}
		endIdx := strings.Index(text[bodyStart:], managedMarkerEnd)
		if endIdx < 0 {
			// Start without end — treat as Legacy/corrupted; safer
			// to migrate-on-update than to silently leave it.
			blk.State = AgentsBlockStateLegacy
			return blk
		}
		bodyEnd := bodyStart + endIdx
		// Trim a single trailing newline before the end marker
		// (matches what writeManagedBlock emits).
		if bodyEnd > bodyStart && text[bodyEnd-1] == '\n' {
			bodyEnd--
		}
		body := text[bodyStart:bodyEnd]
		blk.BodyBytes = body
		blk.InstalledVersion = atoiOrZero(matches[1])
		blk.InstalledChecksum = matches[2]
		blk.OuterStart = loc[0]
		blk.OuterEnd = strings.Index(text[bodyEnd:], managedMarkerEnd) + bodyEnd + len(managedMarkerEnd)

		// Compare body to expected:
		actual := bodyChecksum(body)
		expected := bodyChecksum(embeddedBody)
		switch {
		case blk.InstalledVersion == currentVersion && actual == expected:
			blk.State = AgentsBlockStateInSync
		case actual == blk.InstalledChecksum && blk.InstalledVersion < currentVersion:
			// Body matches the checksum we recorded at install ⇒
			// user has NOT edited the block; we can safely upgrade.
			blk.State = AgentsBlockStateUpdateAvailable
		case actual != blk.InstalledChecksum:
			blk.State = AgentsBlockStateLocallyModified
		case blk.InstalledVersion == currentVersion && actual != expected:
			// Same version, body differs from embedded — local edit.
			blk.State = AgentsBlockStateLocallyModified
		default:
			blk.State = AgentsBlockStateUpdateAvailable
		}
		return blk
	}

	// Legacy marker form: pre-v0.4 wrote `<!-- mainline:begin -->`.
	if reLegacyMarkerStart.MatchString(text) {
		blk.State = AgentsBlockStateLegacy
		return blk
	}

	// File exists but no marker at all → not installed.
	return blk
}

// writeManagedBlock writes (or replaces) the managed block in the
// file at path. The new marker line embeds version + body checksum
// so a future inspect can detect local edits via checksum mismatch.
//
// For files without any existing block, the block is appended with
// one blank-line separator (or used as the entire file content if
// the file is missing).
//
// For files with a legacy block, the legacy span is replaced. For
// files with a modern block, the existing block is replaced.
func writeManagedBlock(path, body string, version int) error {
	wrapped := wrapManagedBlock(body, version)
	desired := wrapped + "\n"

	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if dir := filepath.Dir(path); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		return os.WriteFile(path, []byte(desired), 0o644)
	}
	if err != nil {
		return err
	}

	text := string(existing)

	// Modern block present → splice replace.
	if loc := reModernMarkerStart.FindStringIndex(text); loc != nil {
		endIdx := strings.Index(text[loc[1]:], managedMarkerEnd)
		if endIdx >= 0 {
			outerEnd := loc[1] + endIdx + len(managedMarkerEnd)
			updated := text[:loc[0]] + wrapped + text[outerEnd:]
			return os.WriteFile(path, []byte(updated), 0o644)
		}
	}

	// Legacy block present → splice replace from begin to end.
	if startIdx := strings.Index(text, "<!-- mainline:begin -->"); startIdx >= 0 {
		endTag := "<!-- mainline:end -->"
		if endIdx := strings.Index(text[startIdx:], endTag); endIdx >= 0 {
			outerEnd := startIdx + endIdx + len(endTag)
			updated := text[:startIdx] + wrapped + text[outerEnd:]
			return os.WriteFile(path, []byte(updated), 0o644)
		}
	}

	// No block → append. Normalise separators so we never produce
	// triple-blank-lines around the block.
	sep := "\n\n"
	switch {
	case text == "":
		sep = ""
	case strings.HasSuffix(text, "\n\n"):
		sep = ""
	case strings.HasSuffix(text, "\n"):
		sep = "\n"
	}
	return os.WriteFile(path, []byte(text+sep+desired), 0o644)
}

// wrapManagedBlock renders the marker pair around body, embedding
// version + body checksum. The newline immediately after the start
// marker is part of the contract — inspectManagedBlock skips it on
// read.
func wrapManagedBlock(body string, version int) string {
	sum := bodyChecksum(body)
	start := fmt.Sprintf("<!-- mainline:agents:start version=%d checksum=sha256:%s -->",
		version, sum)
	return start + "\n" + body + "\n" + managedMarkerEnd
}

// bodyChecksum is sha256(body) hex-encoded. The whole-string sum
// catches user edits including whitespace tweaks; the agent's
// behaviour is sensitive to small text changes so we don't normalise.
func bodyChecksum(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

func atoiOrZero(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
