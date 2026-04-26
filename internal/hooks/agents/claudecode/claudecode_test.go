package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/hooks"
)

func TestInstallMergesClaudeSettings(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{
  "env": {
    "KEEP": "1"
  },
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
  },
  "permissions": {
    "allow": ["Bash(git status:*)"]
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := (Agent{}).Install(dir, hooks.InstallOptions{BinPath: "/tmp/mainline"})
	if err != nil {
		t.Fatal(err)
	}
	if report.HookCount != 6 {
		t.Fatalf("HookCount = %d, want 6", report.HookCount)
	}

	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var top struct {
		Env         map[string]string      `json:"env"`
		Permissions map[string][]string    `json:"permissions"`
		Hooks       map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	if top.Env["KEEP"] != "1" || len(top.Permissions["allow"]) != 1 {
		t.Fatalf("unrelated settings were not preserved: %#v", top)
	}
	if len(top.Hooks["SessionStart"]) != 1 || len(top.Hooks["UserPromptSubmit"]) != 1 {
		t.Fatalf("expected Claude lifecycle hooks: %#v", top.Hooks)
	}
	if got := top.Hooks["PreToolUse"][0].Hooks[0].Command; got != "python3 policy.py" {
		t.Fatalf("user hook was not preserved: %q", got)
	}
	if got := top.Hooks["Stop"][0].Hooks[0].Command; !strings.Contains(got, `mainline" hooks claudecode stop`) {
		t.Fatalf("Stop wrapper = %q", got)
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
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	raw, err := os.ReadFile(settingsPath)
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
	if err := os.WriteFile(settingsPath, append(next, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := (Agent{}).Uninstall(dir); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "hooks claudecode") {
		t.Fatalf("managed hook survived uninstall:\n%s", after)
	}
	if !strings.Contains(string(after), "python3 keep.py") {
		t.Fatalf("user hook was removed:\n%s", after)
	}
}

func TestParseEvent(t *testing.T) {
	ev, err := (Agent{}).ParseEvent(context.Background(), HookUserPromptSubmit, strings.NewReader(`{"session_id":"s1","prompt":"do work"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != hooks.TurnStart || ev.Agent != AgentName || ev.SessionID != "s1" || ev.Prompt != "do work" {
		t.Fatalf("unexpected prompt event: %#v", ev)
	}

	ev, err = (Agent{}).ParseEvent(context.Background(), HookSubagentStop, strings.NewReader(`{"session_id":"s1","agent_type":"explorer","stop_hook_active":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != hooks.SubagentEnd || ev.Reason != "explorer" || ev.Status != "continued" {
		t.Fatalf("unexpected subagent event: %#v", ev)
	}

	ev, err = (Agent{}).ParseEvent(context.Background(), HookPreCompact, strings.NewReader(`{"session_id":"s1","trigger":"manual"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != hooks.Compaction || ev.Reason != "manual" {
		t.Fatalf("unexpected compact event: %#v", ev)
	}
}

func TestRenderHookOutput(t *testing.T) {
	d := hooks.NewDispatcher(fakeEngine{
		status: map[string]any{
			"branch":         "feature/work",
			"actor_id":       "actor_1",
			"turn_count":     0,
			"proposed_count": 1,
			"sync_stale":     false,
		},
		proposals: map[string]any{
			"proposals": []map[string]any{
				{
					"intent_id": "int_one",
					"title":     "First proposal",
					"thread":    "feature/one",
					"status":    "proposed",
				},
			},
		},
	}, nil, hooks.DefaultDispatchSettings())
	out, err := (Agent{}).RenderHookOutput(HookUserPromptSubmit, d, nil, nil)
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
	if !got.Continue || got.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Fatalf("unexpected render output: %s", out)
	}
	if !strings.Contains(got.HookSpecificOutput.AdditionalContext, "Mainline per-prompt context") {
		t.Fatalf("missing prompt context: %s", got.HookSpecificOutput.AdditionalContext)
	}

	out, err = (Agent{}).RenderHookOutput(HookSessionStart, hooks.NewDispatcher(nil, nil, hooks.DefaultDispatchSettings()), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.HookSpecificOutput.HookEventName != "SessionStart" || !strings.Contains(got.HookSpecificOutput.AdditionalContext, "Mainline session-start context") {
		t.Fatalf("unexpected session output: %s", out)
	}
}

type fakeEngine struct {
	status    any
	proposals any
}

func (f fakeEngine) Sync() (any, error)            { return nil, nil }
func (f fakeEngine) Status() (any, error)          { return f.status, nil }
func (f fakeEngine) ListProposals() (any, error)   { return f.proposals, nil }
func (f fakeEngine) BinaryStaleness() (any, error) { return nil, nil }
