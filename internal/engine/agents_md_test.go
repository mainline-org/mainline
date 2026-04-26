package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// upsertAgentsMD must never destroy user content. These tests pin the
// four-case algorithm (missing / markers / legacy / no-section) and
// the idempotency invariant.

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpsertAgentsMD_CreatesIfMissing(t *testing.T) {
	dir := t.TempDir()
	changed, err := upsertAgentsMD(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed should be true on initial create")
	}
	got := readFileT(t, filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(got, mainlineSectionStart) {
		t.Errorf("missing start marker: %s", got)
	}
	if !strings.Contains(got, mainlineSectionEnd) {
		t.Errorf("missing end marker")
	}
	if !strings.Contains(got, "## Mainline") {
		t.Errorf("missing template body")
	}
}

// The most important property: user content above and below the
// mainline block is preserved byte-for-byte across upsert.
func TestUpsertAgentsMD_PreservesUserContentAroundMarkedSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")

	header := "# Project conventions\n\nAlways prefer Go tests over shell hacks.\n\n"
	staleMainline := wrapMainlineSection("STALE OLD CONTENT")
	footer := "\n\n## Other notes\n\nUnrelated user content here.\n"
	writeFileT(t, path, header+staleMainline+footer)

	if _, err := upsertAgentsMD(dir); err != nil {
		t.Fatal(err)
	}
	got := readFileT(t, path)

	// Both the header and the footer must survive byte-for-byte.
	if !strings.HasPrefix(got, header) {
		t.Errorf("user header lost; got prefix %q", firstN(got, len(header)+10))
	}
	if !strings.HasSuffix(got, footer) {
		t.Errorf("user footer lost; got suffix %q", lastN(got, len(footer)+10))
	}
	// Stale content must be gone.
	if strings.Contains(got, "STALE OLD CONTENT") {
		t.Errorf("stale block was not replaced")
	}
	// New template content must be present.
	if !strings.Contains(got, "<!-- mainline-agents-md-version: 6 -->") {
		t.Errorf("new template body missing")
	}
}

// Pre-v0.3 AGENTS.md format had `## Mainline` heading with the version
// marker but no begin/end markers. upsertAgentsMD must migrate that in
// place without destroying any non-mainline content above or below.
func TestUpsertAgentsMD_MigratesLegacyVersionedSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")

	header := "# My project\n\nWelcome.\n\n"
	legacy := "## Mainline\n\n<!-- mainline-agents-md-version: 1 -->\n\n" +
		"This project uses Mainline (old text).\n\n" +
		"### Some old subheading\n\nold body\n"
	footer := "\n## Code style\n\nuse tabs not spaces\n"
	writeFileT(t, path, header+legacy+footer)

	if _, err := upsertAgentsMD(dir); err != nil {
		t.Fatal(err)
	}
	got := readFileT(t, path)

	// Header survives.
	if !strings.HasPrefix(got, "# My project\n\nWelcome.") {
		t.Errorf("header lost; first line: %q", firstLine(got))
	}
	// Footer survives — `## Code style` is the next H2 after the
	// legacy section, so the migration must stop replacing at that
	// boundary and keep everything from there on.
	if !strings.Contains(got, "## Code style") || !strings.Contains(got, "use tabs not spaces") {
		t.Errorf("footer lost: %s", got)
	}
	// New marker block lives where the legacy section was.
	if !strings.Contains(got, mainlineSectionStart) {
		t.Errorf("new marker block missing")
	}
	// Old version marker is gone (replaced by v4 inside the new block).
	if strings.Contains(got, "mainline-agents-md-version: 1") {
		t.Errorf("old v1 version marker still present")
	}
}

// File exists, has user content, no mainline anywhere → append a new
// marker block at the end.
func TestUpsertAgentsMD_AppendsToFileWithNoMainlineSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	original := "# Project rules\n\nDo X. Don't Y.\n"
	writeFileT(t, path, original)

	if _, err := upsertAgentsMD(dir); err != nil {
		t.Fatal(err)
	}
	got := readFileT(t, path)

	if !strings.HasPrefix(got, original) {
		t.Errorf("user content not preserved as prefix")
	}
	if !strings.Contains(got, mainlineSectionStart) {
		t.Errorf("mainline block not appended")
	}
}

// Idempotency: calling upsertAgentsMD twice in a row must produce the
// same file on the second call (changed=false). This is what lets
// `mainline init --rewire` be safe to run repeatedly.
func TestUpsertAgentsMD_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := upsertAgentsMD(dir); err != nil {
		t.Fatal(err)
	}
	first := readFileT(t, filepath.Join(dir, "AGENTS.md"))

	changed, err := upsertAgentsMD(dir)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("second call should report changed=false on stable input")
	}
	second := readFileT(t, filepath.Join(dir, "AGENTS.md"))
	if first != second {
		t.Errorf("file changed on second call; first=%q second=%q", first, second)
	}
}

// upsertAgentInstructionStubs writes the four IDE pointer files and
// each respects the same upsert algorithm. After one run, all four
// exist; after a second run, none changed.
func TestUpsertAgentInstructionStubs_WritesFourFilesIdempotently(t *testing.T) {
	dir := t.TempDir()

	written, err := upsertAgentInstructionStubs(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CLAUDE.md",
		".cursor/rules/mainline.md",
		".windsurfrules",
		".github/copilot-instructions.md",
	}
	if len(written) != len(want) {
		t.Errorf("first run: written %v want %v", written, want)
	}
	for _, rel := range want {
		full := filepath.Join(dir, rel)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
		got := readFileT(t, full)
		if !strings.Contains(got, mainlineSectionStart) {
			t.Errorf("%s missing marker block", rel)
		}
	}

	// Second run: all idempotent, nothing reported as written.
	written2, err := upsertAgentInstructionStubs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(written2) != 0 {
		t.Errorf("second run should be no-op, got %v", written2)
	}
}

// Stubs preserve user content outside the marker block.
func TestUpsertAgentInstructionStubs_PreservesHandEdits(t *testing.T) {
	dir := t.TempDir()
	cursorPath := filepath.Join(dir, ".cursor/rules/mainline.md")
	writeFileT(t, cursorPath,
		"# Custom cursor rule\n\nuse semicolons everywhere.\n\n"+
			wrapMainlineSection("OLD STUB CONTENT")+
			"\n\n## More custom stuff\n")

	if _, err := upsertAgentInstructionStubs(dir); err != nil {
		t.Fatal(err)
	}
	got := readFileT(t, cursorPath)
	if !strings.Contains(got, "use semicolons everywhere") {
		t.Errorf("user header lost")
	}
	if !strings.Contains(got, "## More custom stuff") {
		t.Errorf("user footer lost")
	}
	if strings.Contains(got, "OLD STUB CONTENT") {
		t.Errorf("stale content not replaced")
	}
	if !strings.Contains(got, "Quick reference") {
		t.Errorf("new stub body missing")
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
