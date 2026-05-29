package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/hooks"
)

func TestDefaultSkillInstallCommandIsNonInteractive(t *testing.T) {
	got := defaultSkillInstallCommand("mainline-org/mainline")
	want := []string{
		"npx", "--yes", "skills", "add", "mainline-org/mainline",
		"--skill", "mainline",
		"--agent", "codex", "claude-code", "cursor",
		"--global",
		"--yes",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultSkillInstallCommand() = %#v, want %#v", got, want)
	}
}

func TestInstallDefaultSkillSkipsWhenGlobalSkillExists(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	skillPath := filepath.Join(home, ".agents", "skills", "mainline", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("# Mainline\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := svc.installDefaultSkill()
	if !got.Skipped {
		t.Fatalf("installDefaultSkill should skip when global skill exists: %+v", got)
	}
	if got.Installed {
		t.Fatalf("installDefaultSkill should not report install when it skipped: %+v", got)
	}
	if !strings.Contains(got.Error, skillPath) {
		t.Fatalf("skip reason should mention existing skill path, got %q", got.Error)
	}
}

func TestPrepareIntegrationRepoFilesKeepsFreshHookFilesLocalOnly(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)

	hooksPath := filepath.Join(dir, ".codex", "hooks.json")
	writeFile(t, dir, ".codex/hooks.json", "{}\n")
	integrations := &AgentIntegrationInstallResult{Hooks: []HookInstallResult{{
		Report: hooks.InstallReport{
			Files:        []string{hooksPath},
			CreatedFiles: []string{hooksPath},
		},
	}}}

	stage, localOnly, err := svc.prepareIntegrationRepoFiles(integrations)
	if err != nil {
		t.Fatal(err)
	}
	if len(stage) != 0 {
		t.Fatalf("fresh hook file should not be staged, got %v", stage)
	}
	if !containsIntegrationString(localOnly, ".codex/hooks.json") {
		t.Fatalf("fresh hook file should be local-only, got %v", localOnly)
	}
	excludePath := gitInfoExcludePath(t, svc)
	got, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), ".codex/hooks.json") {
		t.Fatalf("info/exclude missing local hook file:\n%s", got)
	}
	status, err := svc.Git.Run("status", "--short")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(status) != "" {
		t.Fatalf("fresh local hook file should not dirty git status, got:\n%s", status)
	}
}

func TestPrepareIntegrationRepoFilesStagesTrackedHookFiles(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)

	writeFile(t, dir, ".cursor/hooks.json", "{\"version\":1}\n")
	gitCmd(t, dir, "add", ".cursor/hooks.json")
	gitCmd(t, dir, "commit", "-m", "track cursor hooks")
	writeFile(t, dir, ".cursor/hooks.json", "{\"version\":1,\"hooks\":{}}\n")

	hooksPath := filepath.Join(dir, ".cursor", "hooks.json")
	stage, localOnly, err := svc.prepareIntegrationRepoFiles(&AgentIntegrationInstallResult{Hooks: []HookInstallResult{{
		Report: hooks.InstallReport{
			Files:        []string{hooksPath},
			CreatedFiles: []string{hooksPath},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !containsIntegrationString(stage, ".cursor/hooks.json") {
		t.Fatalf("tracked hook file should be staged, got %v", stage)
	}
	if len(localOnly) != 0 {
		t.Fatalf("tracked hook file should not be marked local-only, got %v", localOnly)
	}
}

func TestPrepareIntegrationRepoFilesLeavesExistingUntrackedHooksAlone(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()
	svc := NewServiceFromRoot(dir)

	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	writeFile(t, dir, ".claude/settings.json", "{}\n")
	stage, localOnly, err := svc.prepareIntegrationRepoFiles(&AgentIntegrationInstallResult{Hooks: []HookInstallResult{{
		Report: hooks.InstallReport{Files: []string{settingsPath}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(stage) != 0 || len(localOnly) != 0 {
		t.Fatalf("pre-existing untracked hook file should be left alone, stage=%v localOnly=%v", stage, localOnly)
	}
	status, err := svc.Git.Run("status", "--short")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "?? .claude/") {
		t.Fatalf("pre-existing untracked file should remain visible, got:\n%s", status)
	}
}

func gitInfoExcludePath(t *testing.T, svc *Service) string {
	t.Helper()
	out, err := svc.Git.Run("rev-parse", "--git-path", "info/exclude")
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSpace(out)
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(svc.Git.RepoRoot, path)
}

func containsIntegrationString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
