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

	st, err := (Agent{}).InstallationStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Installed || st.NeedsRepair || st.HookCount != st.ExpectedHookCount {
		t.Fatalf("unexpected healthy install status: %#v", st)
	}
}

func TestInstallationStatusDetectsDisabledFeatureAndRepair(t *testing.T) {
	dir := t.TempDir()
	if _, err := (Agent{}).Install(dir, hooks.InstallOptions{BinPath: "/tmp/mainline"}); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, ".codex", "config.toml")
	if err := os.WriteFile(configPath, []byte("[features]\ncodex_hooks = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := (Agent{}).InstallationStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Installed || !st.NeedsRepair || !strings.Contains(strings.Join(st.RepairReasons, "\n"), "codex_hooks") {
		t.Fatalf("expected disabled codex_hooks to need repair: %#v", st)
	}

	if _, err := (Agent{}).Install(dir, hooks.InstallOptions{BinPath: "/tmp/mainline"}); err != nil {
		t.Fatal(err)
	}
	st, err = (Agent{}).InstallationStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.NeedsRepair {
		t.Fatalf("install should repair disabled codex_hooks: %#v", st)
	}
}

func TestLocalDevWrapperFailsSoft(t *testing.T) {
	got := wrapperCommand(hooks.InstallOptions{LocalDev: true}, HookStop)
	if !strings.Contains(got, "|| exit 0") {
		t.Fatalf("local-dev wrapper should fail soft: %q", got)
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

func TestRenderUserPromptSubmitOutput(t *testing.T) {
	eng := fakeEngine{
		status: map[string]any{
			"branch":         "feature/work",
			"actor_id":       "actor_1",
			"turn_count":     2,
			"proposed_count": 2,
			"sync_stale":     false,
			"active_intent": map[string]any{
				"intent_id": "int_active",
				"thread":    "feature/work",
				"goal":      "Do active work",
			},
			"coverage": map[string]any{
				"uncovered_count": 1,
			},
		},
		proposals: map[string]any{
			"proposals": []map[string]any{
				{
					"intent_id": "int_one",
					"title":     "First proposal",
					"thread":    "feature/one",
					"goal":      "This long goal should not be rendered in the per-prompt summary",
					"status":    "proposed",
				},
			},
		},
	}
	d := hooks.NewDispatcher(eng, nil, hooks.DefaultDispatchSettings())
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
	ctx := got.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, "Mainline per-prompt context") || !strings.Contains(ctx, "int_active") || !strings.Contains(ctx, "int_one") {
		t.Fatalf("missing prompt context details: %s", ctx)
	}
	if strings.Contains(ctx, "This long goal should not be rendered") {
		t.Fatalf("proposal goal leaked into lightweight prompt context: %s", ctx)
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
