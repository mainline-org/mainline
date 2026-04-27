package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	full := filepath.Join(dir, rel)
	if d := filepath.Dir(full); d != "." {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	return full
}

func TestInspectManagedBlock_NotInstalledOnMissingFile(t *testing.T) {
	dir := t.TempDir()
	blk := inspectManagedBlock(filepath.Join(dir, "AGENTS.md"), "body", 7)
	if blk.State != AgentsBlockStateNotInstalled {
		t.Fatalf("missing file should be not_installed, got %s", blk.State)
	}
}

func TestInspectManagedBlock_NotInstalledOnPlainFile(t *testing.T) {
	dir := t.TempDir()
	path := writeTempFile(t, dir, "AGENTS.md",
		"# My team's AGENTS.md\n\nWe like documentation.\n")
	blk := inspectManagedBlock(path, "body", 7)
	if blk.State != AgentsBlockStateNotInstalled {
		t.Fatalf("file with no markers should be not_installed, got %s", blk.State)
	}
}

func TestInspectManagedBlock_LegacyOldMarkers(t *testing.T) {
	dir := t.TempDir()
	path := writeTempFile(t, dir, "AGENTS.md",
		"# Mine\n\n<!-- mainline:begin -->\n## Mainline (old)\n<!-- mainline:end -->\n")
	blk := inspectManagedBlock(path, "body", 7)
	if blk.State != AgentsBlockStateLegacy {
		t.Fatalf("old markers should be legacy, got %s", blk.State)
	}
}

func TestInspectManagedBlock_InSyncWhenWrittenWithCurrentVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	body := "Hello body\nMore body\n"
	if err := writeManagedBlock(path, body, 7); err != nil {
		t.Fatalf("write: %v", err)
	}
	blk := inspectManagedBlock(path, body, 7)
	if blk.State != AgentsBlockStateInSync {
		t.Fatalf("freshly written block should be in_sync, got %s (body=%q)", blk.State, blk.BodyBytes)
	}
}

func TestInspectManagedBlock_UpdateAvailableWhenVersionBumped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	oldBody := "Old body content"
	if err := writeManagedBlock(path, oldBody, 6); err != nil {
		t.Fatalf("write old: %v", err)
	}
	blk := inspectManagedBlock(path, "New body content", 7)
	if blk.State != AgentsBlockStateUpdateAvailable {
		t.Fatalf("v6 file vs v7 binary should be update_available, got %s", blk.State)
	}
	if blk.InstalledVersion != 6 {
		t.Errorf("InstalledVersion should be 6, got %d", blk.InstalledVersion)
	}
}

func TestInspectManagedBlock_LocallyModifiedWhenBodyEdited(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := writeManagedBlock(path, "Original body", 7); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Tamper with the body inside the markers.
	raw, _ := os.ReadFile(path)
	tampered := strings.Replace(string(raw), "Original body", "User edited the block", 1)
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("tamper write: %v", err)
	}
	blk := inspectManagedBlock(path, "Original body", 7)
	if blk.State != AgentsBlockStateLocallyModified {
		t.Fatalf("user-edited body should be locally_modified, got %s (body=%q)",
			blk.State, blk.BodyBytes)
	}
}

func TestWriteManagedBlock_PreservesUserContentAround(t *testing.T) {
	// The contract: anything outside the markers stays byte-for-byte.
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	header := "# My project\n\n## My own rules\n\n- Do this thing\n- Don't do that\n\n"
	footer := "\n## Code style\n\nuse spaces not tabs\n"
	original := header + "<!-- placeholder for mainline -->\n" + footer
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	if err := writeManagedBlock(path, "Mainline says hi", 7); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), header) {
		t.Errorf("user header lost; got: %q", string(got))
	}
	// Footer survival: original ends with a newline-prefixed section,
	// must still appear after the new block.
	if !strings.Contains(string(got), "## Code style") {
		t.Errorf("user footer lost; got: %q", string(got))
	}
	if !strings.Contains(string(got), "Mainline says hi") {
		t.Errorf("new body missing; got: %q", string(got))
	}
}

func TestAgentsCheck_TopLevelStateForFreshRepo(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	if _, err := svc.Init("agent"); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Init writes AGENTS.md via the legacy upsert (old markers); the
	// new check command must classify it as Legacy and propose
	// migration via update.
	res, err := svc.AgentsCheck()
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.CurrentVersion <= 0 {
		t.Fatalf("CurrentVersion should be > 0, got %d", res.CurrentVersion)
	}
	var agentsState AgentsBlockState
	for _, f := range res.Files {
		if f.Path == "AGENTS.md" {
			agentsState = f.State
		}
	}
	if agentsState != AgentsBlockStateLegacy && agentsState != AgentsBlockStateInSync {
		t.Fatalf("post-init AGENTS.md should be legacy or in_sync, got %s", agentsState)
	}
}

func TestAgentsUpdate_MigratesLegacyToModernMarkers(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	// Fresh init produces legacy markers (begin/end without
	// version=N). Update must migrate to the modern marker form
	// while keeping any user content above/below intact.
	preCheck, _ := svc.AgentsCheck()
	hadLegacy := false
	for _, f := range preCheck.Files {
		if f.Path == "AGENTS.md" && f.State == AgentsBlockStateLegacy {
			hadLegacy = true
		}
	}
	if !hadLegacy {
		t.Skipf("init did not produce legacy state; nothing to migrate")
	}

	// Add user content above the block to guard preservation.
	agentsPath := filepath.Join(dir, "AGENTS.md")
	raw, _ := os.ReadFile(agentsPath)
	withHeader := "# My team\n\nWe ship things.\n\n" + string(raw)
	os.WriteFile(agentsPath, []byte(withHeader), 0o644)

	res, err := svc.AgentsUpdate(AgentsUpdateOptions{})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	migratedAGENTS := false
	for _, f := range res.Files {
		if f.Path == "AGENTS.md" && f.Action == "migrated" {
			migratedAGENTS = true
		}
	}
	if !migratedAGENTS {
		t.Fatalf("AGENTS.md should have been migrated, got files=%+v", res.Files)
	}

	post, _ := os.ReadFile(agentsPath)
	if !strings.Contains(string(post), "# My team") {
		t.Errorf("user header lost during migration: %s", string(post))
	}
	if !strings.Contains(string(post), "<!-- mainline:agents:start version=") {
		t.Errorf("modern marker missing post-migration: %s", string(post))
	}
}

func TestAgentsUpdate_RefusesToOverwriteLocalEdits(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	// Migrate legacy → modern, then tamper with the body.
	svc.AgentsUpdate(AgentsUpdateOptions{})
	agentsPath := filepath.Join(dir, "AGENTS.md")
	raw, _ := os.ReadFile(agentsPath)
	tampered := strings.Replace(string(raw), "Mainline", "MainlineHACKED", 1)
	if tampered == string(raw) {
		t.Skipf("template did not contain 'Mainline' to tamper with — body shape changed")
	}
	os.WriteFile(agentsPath, []byte(tampered), 0o644)

	res, err := svc.AgentsUpdate(AgentsUpdateOptions{})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	refused := false
	for _, f := range res.Files {
		if f.Path == "AGENTS.md" && f.Action == "refused" {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("update should refuse to overwrite locally-modified AGENTS.md, got %+v", res.Files)
	}

	// File is unchanged (still tampered).
	got, _ := os.ReadFile(agentsPath)
	if !strings.Contains(string(got), "MainlineHACKED") {
		t.Fatalf("refused update must NOT have rewritten the file")
	}
}

func TestAgentsUpdate_TheirsOverwritesLocalEdits(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	svc.AgentsUpdate(AgentsUpdateOptions{})
	agentsPath := filepath.Join(dir, "AGENTS.md")
	raw, _ := os.ReadFile(agentsPath)
	tampered := strings.Replace(string(raw), "Mainline", "MainlineHACKED", 1)
	if tampered == string(raw) {
		t.Skipf("template did not contain 'Mainline' to tamper with")
	}
	os.WriteFile(agentsPath, []byte(tampered), 0o644)

	res, err := svc.AgentsUpdate(AgentsUpdateOptions{Theirs: true})
	if err != nil {
		t.Fatalf("update --theirs: %v", err)
	}
	updated := false
	for _, f := range res.Files {
		if f.Path == "AGENTS.md" && f.Action == "updated" {
			updated = true
		}
	}
	if !updated {
		t.Fatalf("--theirs should have updated AGENTS.md, got %+v", res.Files)
	}
	got, _ := os.ReadFile(agentsPath)
	if strings.Contains(string(got), "MainlineHACKED") {
		t.Fatalf("--theirs should have overwritten the tampered body")
	}
}

func TestAgentsDiff_EmptyWhenInSync(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")
	svc.AgentsUpdate(AgentsUpdateOptions{}) // migrate to modern + in_sync

	res, err := svc.AgentsDiff()
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(res.Files) != 0 {
		t.Fatalf("post-update diff should be empty (everything in sync), got %+v", res.Files)
	}
}

func TestAgentsGuidanceState_FlowsToStatus(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)
	svc.Init("agent")

	res, err := svc.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if res.AgentsGuidance == nil {
		t.Fatalf("Status must populate AgentsGuidance")
	}
	if res.AgentsGuidance.CurrentVersion <= 0 {
		t.Fatalf("CurrentVersion should be set, got %d", res.AgentsGuidance.CurrentVersion)
	}
	// Pre-update state is Legacy (init writes old markers); after
	// agents update, status should report in_sync.
	svc.AgentsUpdate(AgentsUpdateOptions{})
	res2, _ := svc.Status()
	if res2.AgentsGuidance.State != AgentsBlockStateInSync {
		t.Fatalf("after agents update, status guidance should be in_sync, got %s",
			res2.AgentsGuidance.State)
	}
}
