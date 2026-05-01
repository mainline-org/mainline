package engine

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mainline-org/mainline/internal/hooks"

	_ "github.com/mainline-org/mainline/internal/hooks/agents/claudecode"
	_ "github.com/mainline-org/mainline/internal/hooks/agents/codex"
	_ "github.com/mainline-org/mainline/internal/hooks/agents/cursor"
)

type InitOptions struct {
	InstallAgentIntegrations bool
}

type AgentIntegrationInstallResult struct {
	Skill SkillInstallResult  `json:"skill"`
	Hooks []HookInstallResult `json:"hooks,omitempty"`
}

type SkillInstallResult struct {
	Requested bool     `json:"requested"`
	Command   []string `json:"command,omitempty"`
	Installed bool     `json:"installed"`
	Skipped   bool     `json:"skipped,omitempty"`
	Error     string   `json:"error,omitempty"`
	Output    string   `json:"output,omitempty"`
}

type HookInstallResult struct {
	Agent       string              `json:"agent"`
	DisplayName string              `json:"display_name"`
	Report      hooks.InstallReport `json:"report"`
	Error       string              `json:"error,omitempty"`
}

func (s *Service) InstallDefaultAgentIntegrations() *AgentIntegrationInstallResult {
	return &AgentIntegrationInstallResult{
		Skill: s.installDefaultSkill(),
		Hooks: s.installDefaultHooks(),
	}
}

func (s *Service) installDefaultSkill() SkillInstallResult {
	res := SkillInstallResult{
		Requested: true,
		Command:   []string{"npx", "--yes", "skills", "add", "mainline"},
	}
	npx, err := exec.LookPath("npx")
	if err != nil {
		res.Skipped = true
		res.Error = "npx not found on PATH"
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, npx, "--yes", "skills", "add", "mainline")
	cmd.Dir = s.Git.RepoRoot
	out, err := cmd.CombinedOutput()
	res.Output = trimCommandOutput(string(out))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			res.Error = "npx skills add mainline timed out"
		} else {
			res.Error = err.Error()
		}
		return res
	}
	res.Installed = true
	return res
}

func trimCommandOutput(out string) string {
	out = strings.TrimSpace(out)
	const max = 4000
	if len(out) <= max {
		return out
	}
	return out[:max] + "\n...<truncated>"
}

func (s *Service) installDefaultHooks() []HookInstallResult {
	agents := hooks.List()
	out := make([]HookInstallResult, 0, len(agents))
	for _, a := range agents {
		rep, err := a.Install(s.Git.RepoRoot, hooks.InstallOptions{})
		row := HookInstallResult{
			Agent:       a.Name(),
			DisplayName: a.DisplayName(),
			Report:      rep,
		}
		if err != nil {
			row.Error = err.Error()
		}
		out = append(out, row)
	}
	return out
}

func integrationRepoPaths(repoRoot string, integrations *AgentIntegrationInstallResult) []string {
	if integrations == nil {
		return nil
	}
	var out []string
	for _, h := range integrations.Hooks {
		if h.Error != "" {
			continue
		}
		for _, p := range h.Report.Files {
			if rel, err := filepath.Rel(repoRoot, p); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				out = append(out, rel)
			}
		}
	}
	return out
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
