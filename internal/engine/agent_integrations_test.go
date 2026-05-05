package engine

import (
	"reflect"
	"testing"
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
