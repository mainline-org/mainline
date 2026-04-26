package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/hooks"
)

func TestInstallMergesHooksAndEnablesFeature(t *testing.T) {
	dir := t.TempDir()
	codexDir := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(codexDir, "hooks.json")
	if err := os.WriteFile(hooksPath, []byte(`{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "python3 policy.py"
          }
        ]
      }
    ]
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexDir, "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"gpt-5.5\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := (Agent{}).Install(dir, hooks.InstallOptions{BinPath: "/tmp/mainline"})
	if err != nil {
		t.Fatal(err)
	}
	if report.HookCount != 3 {
		t.Fatalf("HookCount = %d, want 3", report.HookCount)
	}

	raw, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var top struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	if len(top.Hooks["SessionStart"]) != 1 || top.Hooks["SessionStart"][0].Matcher != "startup|resume" {
		t.Fatalf("SessionStart hook not installed as expected: %#v", top.Hooks["SessionStart"])
	}
	if got := top.Hooks["PreToolUse"][0].Hooks[0].Command; got != "python3 policy.py" {
		t.Fatalf("user hook was not preserved: %q", got)
	}
	if got := top.Hooks["Stop"][0].Hooks[0].Command; !strings.Contains(got, `mainline" hooks codex stop`) {
		t.Fatalf("Stop wrapper = %q", got)
	}

	cfg, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "[features]\ncodex_hooks = true") {
		t.Fatalf("config.toml did not enable codex_hooks:\n%s", cfg)
	}

	again, err := (Agent{}).Install(dir, hooks.InstallOptions{BinPath: "/tmp/mainline"})
	if err != nil {
		t.Fatal(err)
	}
	if !again.AlreadyInstalled {
		t.Fatalf("second install should be idempotent: %#v", again)
	}
}

func TestUninstallRemovesOnlyManagedHooks(t *testing.T) {
	dir := t.TempDir()
	if _, err := (Agent{}).Install(dir, hooks.InstallOptions{BinPath: "/tmp/mainline"}); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(dir, ".codex", "hooks.json")
	raw, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var top struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	top.Hooks["Stop"][0].Hooks = append(top.Hooks["Stop"][0].Hooks, commandHook{
		Type:    "command",
		Command: "python3 keep.py",
	})
	next, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooksPath, append(next, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := (Agent{}).Uninstall(dir); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "mainline hooks codex") {
		t.Fatalf("managed hook survived uninstall:\n%s", after)
	}
	if !strings.Contains(string(after), "python3 keep.py") {
		t.Fatalf("user hook was removed:\n%s", after)
	}
}

func TestParseEvent(t *testing.T) {
	ev, err := (Agent{}).ParseEvent(context.Background(), HookUserPromptSubmit, strings.NewReader(`{"session_id":"s1","turn_id":"t1","prompt":"do work"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != hooks.TurnStart || ev.Agent != AgentName || ev.SessionID != "s1" || ev.Prompt != "do work" {
		t.Fatalf("unexpected prompt event: %#v", ev)
	}

	msg := "done"
	ev, err = (Agent{}).ParseEvent(context.Background(), HookStop, strings.NewReader(`{"session_id":"s1","last_assistant_message":"`+msg+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != hooks.TurnEnd || ev.Summary != msg || ev.Status != "completed" {
		t.Fatalf("unexpected stop event: %#v", ev)
	}
}

func TestRenderSessionStartOutput(t *testing.T) {
	d := hooks.NewDispatcher(nil, nil, hooks.DefaultDispatchSettings())
	out, err := (Agent{}).RenderHookOutput(HookSessionStart, d, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Continue           bool `json:"continue"`
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Continue || got.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("unexpected render output: %s", out)
	}
	if !strings.Contains(got.HookSpecificOutput.AdditionalContext, "Mainline session-start context") {
		t.Fatalf("missing mainline context: %s", got.HookSpecificOutput.AdditionalContext)
	}
}
