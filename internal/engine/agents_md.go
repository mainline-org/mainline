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

// Mainline section markers. The marker pair makes the section
// addressable so upsertAgentsMD can replace just the mainline block,
// preserving any non-mainline content the user has added before or
// after it. Using HTML comments keeps the markers invisible in
// rendered Markdown.
const (
	mainlineSectionStart = "<!-- mainline:begin -->"
	mainlineSectionEnd   = "<!-- mainline:end -->"
)

var (
	// Matches the modern marker-wrapped block (case 1).
	reMainlineBlock = regexp.MustCompile(
		`(?s)<!-- mainline:begin -->.*?<!-- mainline:end -->`)

	// Matches a legacy `## Mainline` heading + version marker.
	// rc1..v0.2 wrote the section without begin/end markers; we
	// detect those and migrate them in place.
	reLegacyHeader = regexp.MustCompile(
		`(?m)^## Mainline\s*\n+\s*<!-- mainline-agents-md-version: \d+ -->`)
)

// upsertAgentsMD writes or updates the mainline section of AGENTS.md
// at the repo root. Three cases:
//
//  1. File missing — create it containing only the wrapped template.
//  2. File exists with mainline:begin / mainline:end markers — replace
//     the bytes between (and including) them. Surrounding user content
//     is left exactly as-is.
//  3. File exists with the legacy ## Mainline heading + version marker
//     (rc1..v0.2 format) — find the section by walking from the
//     heading to the next H2 (or EOF), replace it with the new wrapped
//     block. This migrates pre-v0.3 files.
//  4. File exists, no markers, no legacy heading — append the wrapped
//     block at the end with one blank-line separator.
//
// Surrounding non-mainline content is never modified. The function is
// idempotent: calling it twice in a row produces no diff on the second
// call.
func upsertAgentsMD(repoRoot string) (changed bool, err error) {
	return upsertSectionFile(
		filepath.Join(repoRoot, "AGENTS.md"),
		strings.TrimRight(agentsMDTemplate, "\n"),
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

// upsertSectionFile is the file-IO layer shared by AGENTS.md and the
// IDE stubs. It applies the four-case upsert algorithm documented on
// upsertAgentsMD. body is the raw section content (no markers); the
// function wraps it with mainlineSectionStart/End on write.
func upsertSectionFile(path, body string) (bool, error) {
	wrapped := wrapMainlineSection(body)
	desired := wrapped + "\n"

	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return true, os.WriteFile(path, []byte(desired), 0o644)
	}
	if err != nil {
		return false, err
	}

	text := string(existing)

	// Case: modern markers present — replace just the wrapped block.
	if reMainlineBlock.MatchString(text) {
		updated := reMainlineBlock.ReplaceAllString(text, wrapped)
		if updated == text {
			return false, nil
		}
		return true, os.WriteFile(path, []byte(updated), 0o644)
	}

	// Case: legacy `## Mainline` + version marker present — find the
	// section bounds and splice in the wrapped block.
	if loc := reLegacyHeader.FindStringIndex(text); loc != nil {
		updated := replaceLegacySection(text, loc[0], wrapped)
		if updated == text {
			return false, nil
		}
		return true, os.WriteFile(path, []byte(updated), 0o644)
	}

	// Case: no mainline content present — append at end with one
	// blank-line separator.
	sep := "\n\n"
	switch {
	case text == "":
		sep = ""
	case strings.HasSuffix(text, "\n\n"):
		sep = ""
	case strings.HasSuffix(text, "\n"):
		sep = "\n"
	}
	return true, os.WriteFile(path, []byte(text+sep+desired), 0o644)
}

func wrapMainlineSection(body string) string {
	return mainlineSectionStart + "\n" + body + "\n" + mainlineSectionEnd
}

// replaceLegacySection finds the end of the pre-marker `## Mainline`
// section starting at `start` (the index of `## Mainline`) and
// returns the input with that whole region replaced by `wrapped`. The
// section ends at the next `^## ` heading, or EOF.
func replaceLegacySection(text string, start int, wrapped string) string {
	rest := text[start:]
	// Skip the heading line itself, then find the next H2.
	lines := strings.SplitAfter(rest, "\n")
	end := len(rest)
	for i := 1; i < len(lines); i++ {
		// `## ` exactly (not `### `).
		if strings.HasPrefix(lines[i], "## ") {
			// Compute the absolute end-byte position.
			off := 0
			for j := 0; j < i; j++ {
				off += len(lines[j])
			}
			end = off
			break
		}
	}
	tail := text[start+end:]
	// Trim a trailing newline from the section we are removing so we
	// do not double-up blank lines around the new wrapped block.
	prefix := strings.TrimRight(text[:start], " \n")
	if prefix != "" {
		prefix += "\n\n"
	}
	suffix := strings.TrimLeft(tail, "\n")
	if suffix != "" {
		suffix = "\n\n" + suffix
	}
	return prefix + wrapped + suffix
}
