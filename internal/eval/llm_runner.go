package eval

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// LLM runner — Step 5 LLM-runner layer from
// docs_for_ai/mainline-spec-v0.2.md.
//
// What this file ships:
//
//   - The AgentRunner interface every concrete runner satisfies.
//   - MockRunner: scriptable, deterministic; the unit-test driver
//     for the rest of the harness, also useful for "what if the
//     agent did X" rehearsals.
//   - CommandRunner: shells out to an external binary (any CLI that
//     reads a prompt + fixture context on stdin and writes the
//     agent's response on stdout). This is the seam an actual LLM
//     integration plugs into — wrap your favourite LLM CLI and
//     point CommandRunner at it.
//
// What this file does NOT ship:
//
//   - A built-in Anthropic / OpenAI / Bedrock client. Those are
//     their own engineering projects with API keys, rate limits,
//     async control. The CommandRunner seam is the right place for
//     them; the binary on the other side of CommandRunner can
//     speak to whichever provider it wants.
//
// Scoring (RunWithAgent below) compares the agent's output against
// the fixture's Forbidden list using substring match. Imperfect, but
// the baseline that lets us measure code-first vs intent-first
// without a parser-per-language; v2 will add language-specific diff
// inspection if the substring score is too noisy.

// AgentRunPrompt is one of the two prompts the eval harness drives
// agents with. Code-first is the strong baseline (read code thoroughly
// before editing); intent-first is the treatment (read mainline
// context before reading code).
type AgentRunPrompt string

const (
	AgentPromptCodeFirst   AgentRunPrompt = "code_first"
	AgentPromptIntentFirst AgentRunPrompt = "intent_first"
)

//go:embed prompts/code_first.md
var promptCodeFirst string

//go:embed prompts/intent_first.md
var promptIntentFirst string

// PromptText returns the embedded prompt body for a given AgentRunPrompt.
// Treat the bodies as opaque from the harness's point of view — the
// eval contract is "drive the agent with this prompt verbatim".
func PromptText(p AgentRunPrompt) string {
	switch p {
	case AgentPromptCodeFirst:
		return promptCodeFirst
	case AgentPromptIntentFirst:
		return promptIntentFirst
	}
	return ""
}

// AgentRunRequest is the input every runner gets per fixture. Fixture
// + prompt are constant per request; ScratchDir is where the runner
// is allowed to leave artifacts (the harness cleans up between runs).
type AgentRunRequest struct {
	Fixture    Fixture
	Prompt     AgentRunPrompt
	ScratchDir string

	// Timeout is a hard budget for one run. Honoured by CommandRunner;
	// MockRunner ignores it.
	Timeout time.Duration
}

// AgentRunResult is what a runner returns. Output is the agent's full
// response (diff, prose, or JSON — runner-defined); the scorer reads
// it as text. ContextRetrieved tells the harness whether the agent
// actually called `mainline context` during the run — central to
// the intent-first thesis.
type AgentRunResult struct {
	Prompt           AgentRunPrompt `json:"prompt"`
	Output           string         `json:"output"`
	DurationMillis   int64          `json:"duration_ms"`
	ContextRetrieved bool           `json:"context_retrieved"`
	Error            string         `json:"error,omitempty"`
}

// AgentRunner is the seam between the eval harness and any LLM-driving
// runtime. Implementations are responsible for: feeding the prompt to
// the agent, observing whether the agent called `mainline context`,
// and returning the agent's final response.
type AgentRunner interface {
	Run(req AgentRunRequest) (AgentRunResult, error)
}

// MockRunner returns canned responses keyed by (fixture name, prompt).
// Use it in tests AND for "what if the agent did X" rehearsals when
// you don't yet have a real LLM hooked up.
type MockRunner struct {
	// Responses[fixtureName][prompt] = response.
	Responses map[string]map[AgentRunPrompt]AgentRunResult
}

// Run satisfies AgentRunner. Returns the registered response for the
// (fixture, prompt) pair; an empty AgentRunResult with no error if
// nothing is registered (lets tests assert "the runner was called").
func (m *MockRunner) Run(req AgentRunRequest) (AgentRunResult, error) {
	byPrompt, ok := m.Responses[req.Fixture.Name]
	if !ok {
		return AgentRunResult{Prompt: req.Prompt}, nil
	}
	res, ok := byPrompt[req.Prompt]
	if !ok {
		return AgentRunResult{Prompt: req.Prompt}, nil
	}
	res.Prompt = req.Prompt
	return res, nil
}

// CommandRunner shells out to an external binary. The binary is
// invoked once per AgentRunRequest with stdin carrying a JSON
// envelope and stdout expected to carry the agent's output.
//
// Stdin envelope shape:
//
//	{
//	  "fixture": <Fixture>,
//	  "prompt":  "<full prompt text>",
//	  "scratch_dir": "<absolute path>"
//	}
//
// Stdout: any text the agent produced. The substring scorer treats
// it as opaque; if the agent emits structured data
// (`{"context_retrieved": true, ...}`) it is parsed as
// AgentRunResult JSON if the FIRST byte is `{`.
//
// Why this shape: it means the LLM integration is "write a small
// shell wrapper that reads stdin, drives your favourite LLM CLI,
// writes stdout" — no Mainline-side dependency on a specific provider.
type CommandRunner struct {
	// Path is the binary to exec. Required.
	Path string
	// Args are passed verbatim before the stdin envelope.
	Args []string
	// Env optionally extends os.Environ() for the child process.
	Env []string
}

// Run satisfies AgentRunner. Encodes the request as JSON on stdin,
// reads stdout as either an AgentRunResult JSON or a raw text
// response.
func (c *CommandRunner) Run(req AgentRunRequest) (AgentRunResult, error) {
	if c.Path == "" {
		return AgentRunResult{Prompt: req.Prompt}, fmt.Errorf("eval: CommandRunner.Path is required")
	}
	envelope := struct {
		Fixture    Fixture        `json:"fixture"`
		Prompt     string         `json:"prompt"`
		PromptKey  AgentRunPrompt `json:"prompt_key"`
		ScratchDir string         `json:"scratch_dir"`
	}{
		Fixture:    req.Fixture,
		Prompt:     PromptText(req.Prompt),
		PromptKey:  req.Prompt,
		ScratchDir: req.ScratchDir,
	}
	stdinBytes, err := json.Marshal(envelope)
	if err != nil {
		return AgentRunResult{Prompt: req.Prompt}, fmt.Errorf("eval: marshal envelope: %w", err)
	}

	cmd := exec.Command(c.Path, c.Args...)
	cmd.Stdin = bytes.NewReader(stdinBytes)
	cmd.Env = append(os.Environ(), c.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if req.Timeout > 0 {
		// Timer guard: cmd.Start + monitoring goroutine. Kept simple —
		// tests use MockRunner; CommandRunner's hot path is one fork
		// per fixture per prompt, modest scale.
		done := make(chan error, 1)
		start := time.Now()
		if err := cmd.Start(); err != nil {
			return AgentRunResult{Prompt: req.Prompt, Error: err.Error()}, err
		}
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			return parseRunnerStdout(req.Prompt, stdout.String(), stderr.String(),
				time.Since(start), err)
		case <-time.After(req.Timeout):
			_ = cmd.Process.Kill()
			<-done
			return AgentRunResult{
				Prompt:         req.Prompt,
				DurationMillis: req.Timeout.Milliseconds(),
				Error:          fmt.Sprintf("timeout after %s", req.Timeout),
			}, fmt.Errorf("timeout")
		}
	}

	start := time.Now()
	err = cmd.Run()
	return parseRunnerStdout(req.Prompt, stdout.String(), stderr.String(), time.Since(start), err)
}

// parseRunnerStdout interprets the runner's stdout. If the first
// non-whitespace byte is `{`, parse as AgentRunResult JSON; otherwise
// treat the whole output as the Output string and infer
// ContextRetrieved by scanning for `mainline context` substrings.
func parseRunnerStdout(prompt AgentRunPrompt, stdout, stderr string, elapsed time.Duration, runErr error) (AgentRunResult, error) {
	res := AgentRunResult{
		Prompt:         prompt,
		DurationMillis: elapsed.Milliseconds(),
	}
	trimmed := strings.TrimSpace(stdout)
	if strings.HasPrefix(trimmed, "{") {
		var parsed AgentRunResult
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			parsed.Prompt = prompt
			if parsed.DurationMillis == 0 {
				parsed.DurationMillis = elapsed.Milliseconds()
			}
			if runErr != nil {
				parsed.Error = runErr.Error()
			}
			return parsed, runErr
		}
	}
	res.Output = stdout
	res.ContextRetrieved = strings.Contains(stdout, "mainline context")
	if runErr != nil {
		res.Error = runErr.Error()
		if stderr != "" {
			res.Error += " | stderr: " + strings.TrimSpace(stderr)
		}
	}
	return res, runErr
}

// AgentScore is the per-(fixture, prompt) outcome that lets the
// eval-results doc compare code-first to intent-first.
type AgentScore struct {
	Fixture          string         `json:"fixture"`
	Prompt           AgentRunPrompt `json:"prompt"`
	ContextRetrieved bool           `json:"context_retrieved"`
	ForbiddenViolations []string    `json:"forbidden_violations,omitempty"`
	DurationMillis   int64          `json:"duration_ms"`
	RunError         string         `json:"run_error,omitempty"`
}

// ScoreAgentRun applies the substring scorer to one AgentRunResult.
// A "forbidden violation" is a Forbidden-list entry whose text
// (case-insensitive) appears in the agent's Output. This is
// imperfect — a perfectly-honest agent who says "I considered
// removing the /oauth middleware but didn't because of the prior
// intent" would trip the substring match. The Note field on each
// score lets reviewers triage.
func ScoreAgentRun(f Fixture, run AgentRunResult) AgentScore {
	score := AgentScore{
		Fixture:          f.Name,
		Prompt:           run.Prompt,
		ContextRetrieved: run.ContextRetrieved,
		DurationMillis:   run.DurationMillis,
		RunError:         run.Error,
	}
	low := strings.ToLower(run.Output)
	for _, forbidden := range f.Forbidden {
		if strings.Contains(low, strings.ToLower(forbidden)) {
			score.ForbiddenViolations = append(score.ForbiddenViolations, forbidden)
		}
	}
	return score
}

// RunWithAgent runs every populated fixture against the given runner,
// once per AgentRunPrompt, and returns the per-(fixture, prompt) score
// table. Empty-Intents fixtures (stubs in the catalog) are skipped.
//
// This is the highest-level entry point in the LLM-runner layer — a
// CLI driver wires the runner and prints/exports the result.
func RunWithAgent(fs []Fixture, runner AgentRunner, scratchRoot string) []AgentScore {
	out := []AgentScore{}
	for _, f := range fs {
		if len(f.Intents) == 0 {
			continue
		}
		for _, prompt := range []AgentRunPrompt{AgentPromptCodeFirst, AgentPromptIntentFirst} {
			scratch := scratchRoot
			if scratch != "" {
				scratch = scratch + "/" + f.Name + "-" + string(prompt)
			}
			run, _ := runner.Run(AgentRunRequest{
				Fixture:    f,
				Prompt:     prompt,
				ScratchDir: scratch,
			})
			out = append(out, ScoreAgentRun(f, run))
		}
	}
	return out
}
