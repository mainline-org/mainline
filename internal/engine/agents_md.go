package engine

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Canonical templates. Embedded so engine binary is self-sufficient
// and the template is the single source of truth — no drift between
// generated AGENTS.md and a docs/AGENTS.md sample file.

//go:embed templates/agents-md.md
var agentsMDTemplate string

//go:embed templates/agents-stub.md
var agentsStubTemplate string

func agentsLightTemplate() string {
	return fmt.Sprintf(`## Mainline

<!-- mainline-agents-md-version: %d -->

**Stop AI coding agents from repeating old engineering mistakes.**

This repository uses Mainline, a Git-native memory layer that tells coding agents why the code is the way it is before they edit it. Agents must use the Mainline skill workflow for non-trivial engineering work and read agent autonomy stop lines from preflight/status. Autonomy is advisory; hard gates and current user instructions take priority. Review autonomy may push a non-main branch and stops at PR; it never authorizes pushing main, merge, or release. Seal-time conflicts are phase-1 overlap warnings: agents classify overlap warnings before escalating and do not paste raw JSON by default. mainline publish publishes intent metadata, not product releases. mainline agents update refreshes this repo guidance; update global skills separately with npx --yes skills update mainline --global --yes or the matching skills add.
`, EmbeddedAgentsMDVersion())
}

// Pre-v0.4 markers (`<!-- mainline:begin -->` / `<!-- mainline:end -->`)
// and the very-old rc1..v0.2 `## Mainline` heading form are both
// recognised by inspectManagedBlock for migration purposes. The new
// versioned-marker form is in agents_managed.go; this file is kept
// for the helpers below that init / rewire still call into.

var (
	// reAgentsMDVersion captures the integer N from
	// `<!-- mainline-agents-md-version: N -->`. Used by version
	// helpers below to compare embedded vs on-disk to detect a
	// stale AGENTS.md after a binary upgrade.
	reAgentsMDVersion = regexp.MustCompile(
		`<!-- mainline-agents-md-version: (\d+) -->`)
)

// EmbeddedAgentsMDVersion returns the version integer baked into the
// binary's full workflow template. The lightweight AGENTS.md policy
// pointer uses the same version so stale installed policy can be
// detected after upgrades.
//
// Discipline: any meaningful body change to templates/agents-md.md
// must bump the version marker. The embedded version is the contract
// that lets staleness detection notice when an on-disk AGENTS.md
// diverges after a binary upgrade.
func EmbeddedAgentsMDVersion() int {
	return parseAgentsMDVersion(agentsMDTemplate)
}

// LocalAgentsMDVersion returns the version integer found in the
// repo-root AGENTS.md. Returns:
//
//	-1, false    file does not exist
//	 0, false    file exists but no version marker
//	 N, true     file exists and carries `version: N`
//
// Callers compare against EmbeddedAgentsMDVersion() to decide
// "outdated".
func LocalAgentsMDVersion(repoRoot string) (int, bool) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "AGENTS.md"))
	if err != nil {
		return -1, false
	}
	v := parseAgentsMDVersion(string(data))
	return v, v >= 0
}

// AgentsMDOutdated reports whether the local AGENTS.md is older than
// the embedded template. A missing file or a present-but-versionless
// file both count as outdated for callers that are inspecting the
// optional repo-policy surface.
func AgentsMDOutdated(repoRoot string) bool {
	local, present := LocalAgentsMDVersion(repoRoot)
	if !present {
		return true
	}
	return local < EmbeddedAgentsMDVersion()
}

// parseAgentsMDVersion returns the captured integer from the version
// marker, or -1 when the marker is absent. Helper kept private; its
// only callers are the two exported wrappers above.
func parseAgentsMDVersion(text string) int {
	m := reAgentsMDVersion.FindStringSubmatch(text)
	if len(m) != 2 {
		return -1
	}
	var n int
	for _, r := range m[1] {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// upsertAgentsMD writes or updates the Mainline-managed block in
// AGENTS.md at the repo root. Wraps writeManagedBlock with the
// embedded template body and current version. Idempotent — repeated
// calls leave the file byte-equal once it is in sync.
//
// User content above and below the markers is preserved byte-for-byte.
// Legacy formats (pre-v0.4 begin/end markers, very-old `## Mainline`
// heading) are migrated to the modern versioned+checksum marker form
// the first time this runs.
func upsertAgentsMD(repoRoot string) (changed bool, err error) {
	return upsertSectionFile(
		filepath.Join(repoRoot, "AGENTS.md"),
		strings.TrimRight(agentsLightTemplate(), "\n"),
	)
}

// upsertAgentInstructionStubs writes the small "see AGENTS.md"
// pointer files that non-Claude-Code IDEs read. Each is upserted with
// the same section markers so manual edits outside the marker block
// survive. Missing parent directories are created.
//
// Files written:
//   - CLAUDE.md (Claude Code primary; reads AGENTS.md too but a
//     separate file removes ambiguity for users who configured Claude
//     Code without AGENTS.md awareness)
//   - .cursor/rules/mainline.md
//   - .windsurfrules
//   - .github/copilot-instructions.md
//
// All four take the same short stub content. Hand-edited content
// outside the marker block is preserved on every re-run.
func upsertAgentInstructionStubs(repoRoot string) (written []string, err error) {
	stub := strings.TrimRight(agentsStubTemplate, "\n")
	targets := []string{
		"CLAUDE.md",
		".cursor/rules/mainline.md",
		".windsurfrules",
		".github/copilot-instructions.md",
	}
	for _, rel := range targets {
		full := filepath.Join(repoRoot, rel)
		if dir := filepath.Dir(full); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return written, fmt.Errorf("mkdir %s: %w", dir, err)
			}
		}
		changed, err := upsertSectionFile(full, stub)
		if err != nil {
			return written, fmt.Errorf("upsert %s: %w", rel, err)
		}
		if changed {
			written = append(written, rel)
		}
	}
	return written, nil
}

// upsertSectionFile is the file-IO layer shared by the managed
// AGENTS.md policy block and legacy stub helpers. It delegates to
// writeManagedBlock so installs produce the modern versioned-marker
// form (with sha256 checksum) from day one. The legacy-section
// migration paths handled here in
// rc1..rc7 — `<!-- mainline:begin -->` markers and the very-old
// `## Mainline` heading — are preserved by writeManagedBlock's
// inspect-and-splice logic.
//
// Pre-condition expected by callers: body has no markers; this
// function wraps it.
func upsertSectionFile(path, body string) (bool, error) {
	version := EmbeddedAgentsMDVersion()
	if version <= 0 {
		// Embedded template missing the version marker — should not
		// happen in shipped binaries; fall back to v1 to keep init
		// from blocking. Doctor will surface the anomaly separately.
		version = 1
	}
	prevHash, prevExists := readBodyChecksum(path)
	if err := writeManagedBlock(path, body, version); err != nil {
		return false, err
	}
	newHash := bodyChecksum(body)
	changed := !prevExists || prevHash != newHash
	return changed, nil
}

// readBodyChecksum returns the sha256 of the managed-block body
// inside path's modern marker, or "" if no modern marker is present.
// Used by upsertSectionFile to compute its `changed` return value
// without re-reading the file twice.
func readBodyChecksum(path string) (string, bool) {
	blk := inspectManagedBlock(path, "", 0)
	if blk.State == AgentsBlockStateNotInstalled || blk.State == AgentsBlockStateLegacy {
		return "", false
	}
	return bodyChecksum(blk.BodyBytes), true
}
