package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/hooks"
)

func TestInstallationStatusDetectsMissingHookAndRepair(t *testing.T) {
	dir := t.TempDir()
	if _, err := (Agent{}).Install(dir, hooks.InstallOptions{BinPath: "/tmp/mainline"}); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(dir, ".cursor", "hooks.json")
	raw, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var top struct {
		Version int                    `json:"version"`
		Hooks   map[string][]hookEntry `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	delete(top.Hooks, "stop")
	next, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooksPath, append(next, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := (Agent{}).InstallationStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Installed || !st.NeedsRepair || !strings.Contains(strings.Join(st.RepairReasons, "\n"), "stop") {
		t.Fatalf("expected missing stop hook to need repair: %#v", st)
	}

	if _, err := (Agent{}).Install(dir, hooks.InstallOptions{BinPath: "/tmp/mainline"}); err != nil {
		t.Fatal(err)
	}
	st, err = (Agent{}).InstallationStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.NeedsRepair || st.HookCount != st.ExpectedHookCount {
		t.Fatalf("install should repair missing hook: %#v", st)
	}
}

func TestLocalDevWrapperFailsSoft(t *testing.T) {
	got := wrapperCommand(hooks.InstallOptions{LocalDev: true}, HookStop)
	if !strings.Contains(got, "|| exit 0") {
		t.Fatalf("local-dev wrapper should fail soft: %q", got)
	}
}
