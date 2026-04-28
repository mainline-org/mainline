package eval

import (
	"strings"
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
)

// PromptText returns the canonical prompt body for both
// AgentRunPrompt values; an unknown value returns empty. Pin the
// presence of the two known values + the empty fallback so a future
// rename cannot silently produce empty prompts on a real LLM run.
func TestPromptText_KnownPromptsPopulated(t *testing.T) {
	if got := PromptText(AgentPromptCodeFirst); !strings.Contains(strings.ToLower(got), "code-first") {
		t.Errorf("code_first prompt should contain 'code-first': %q", firstN(got, 80))
	}
	if got := PromptText(AgentPromptIntentFirst); !strings.Contains(strings.ToLower(got), "intent-first") {
		t.Errorf("intent_first prompt should contain 'intent-first': %q", firstN(got, 80))
	}
	if got := PromptText("nonsense"); got != "" {
		t.Errorf("unknown prompt should return empty, got %q", firstN(got, 40))
	}
}

// MockRunner returns the registered response and stamps the prompt
// onto the result so callers always see which side of the
// code-first/intent-first split they ran.
func TestMockRunner_ReturnsRegisteredResponseAndStampsPrompt(t *testing.T) {
	mr := &MockRunner{
		Responses: map[string]map[AgentRunPrompt]AgentRunResult{
			"auth-migration": {
				AgentPromptIntentFirst: {Output: "kept the /oauth path", ContextRetrieved: true},
			},
		},
	}
	res, err := mr.Run(AgentRunRequest{
		Fixture: Fixture{Name: "auth-migration"},
		Prompt:  AgentPromptIntentFirst,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Prompt != AgentPromptIntentFirst {
		t.Errorf("Prompt should be stamped on result: got %q", res.Prompt)
	}
	if !res.ContextRetrieved {
		t.Errorf("registered response should propagate ContextRetrieved")
	}
}

// Unregistered (fixture, prompt) returns an empty result with no
// error — lets tests assert "the runner was called against this
// pair" without requiring every test to register every combination.
func TestMockRunner_MissingResponseIsEmptyNotError(t *testing.T) {
	mr := &MockRunner{Responses: map[string]map[AgentRunPrompt]AgentRunResult{}}
	res, err := mr.Run(AgentRunRequest{
		Fixture: Fixture{Name: "anything"}, Prompt: AgentPromptCodeFirst,
	})
	if err != nil {
		t.Fatalf("missing response should not error: %v", err)
	}
	if res.Output != "" || res.ContextRetrieved {
		t.Errorf("missing response should be empty, got %+v", res)
	}
	if res.Prompt != AgentPromptCodeFirst {
		t.Errorf("Prompt should be stamped even on missing response")
	}
}

// ScoreAgentRun: a forbidden item appearing in the output (case-
// insensitive) shows up as a violation.
func TestScoreAgentRun_DetectsForbiddenSubstring(t *testing.T) {
	f := Fixture{
		Name: "fx",
		Forbidden: []string{
			"delete the /oauth session middleware",
			"silently retry the multi-region cluster plan",
		},
	}
	run := AgentRunResult{
		Prompt: AgentPromptCodeFirst,
		Output: "I will Delete The /oauth Session Middleware to clean up.",
	}
	score := ScoreAgentRun(f, run)
	if len(score.ForbiddenViolations) != 1 {
		t.Fatalf("expected 1 violation, got %d (%v)", len(score.ForbiddenViolations), score.ForbiddenViolations)
	}
	if score.ForbiddenViolations[0] != f.Forbidden[0] {
		t.Errorf("wrong forbidden item flagged: %v", score.ForbiddenViolations)
	}
}

// Clean run (output mentions no forbidden item) produces zero
// violations and propagates ContextRetrieved + DurationMillis.
func TestScoreAgentRun_CleanOutputNoViolations(t *testing.T) {
	f := Fixture{Name: "fx", Forbidden: []string{"do bad thing"}}
	run := AgentRunResult{
		Prompt:           AgentPromptIntentFirst,
		Output:           "respected the prior anti-pattern; left /oauth path untouched",
		ContextRetrieved: true,
		DurationMillis:   1234,
	}
	score := ScoreAgentRun(f, run)
	if len(score.ForbiddenViolations) != 0 {
		t.Errorf("clean output should have no violations: %v", score.ForbiddenViolations)
	}
	if !score.ContextRetrieved || score.DurationMillis != 1234 {
		t.Errorf("score must propagate ContextRetrieved + DurationMillis: %+v", score)
	}
}

// RunWithAgent runs every populated fixture against both prompts and
// returns one score per (fixture, prompt) pair. Stub fixtures are
// skipped silently.
func TestRunWithAgent_RunsBothPromptsPerFixtureAndSkipsStubs(t *testing.T) {
	stubs := []Fixture{
		{Name: "fx-a",
			Intents: []SeedIntent{{ID: "int_a", What: "did x", Status: domain.StatusMerged}},
			Task:    "do x",
			Forbidden: []string{"deleted the load-bearing thing"},
		},
		{Name: "stub-no-intents", Description: "[stub]"},
	}
	mr := &MockRunner{
		Responses: map[string]map[AgentRunPrompt]AgentRunResult{
			"fx-a": {
				AgentPromptCodeFirst:   {Output: "I Deleted The Load-Bearing Thing because it looked unused"},
				AgentPromptIntentFirst: {Output: "I respected the constraint", ContextRetrieved: true},
			},
		},
	}
	scores := RunWithAgent(stubs, mr, "")
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores (one fixture × 2 prompts), got %d", len(scores))
	}
	var cf, intf *AgentScore
	for i := range scores {
		switch scores[i].Prompt {
		case AgentPromptCodeFirst:
			cf = &scores[i]
		case AgentPromptIntentFirst:
			intf = &scores[i]
		}
	}
	if cf == nil || intf == nil {
		t.Fatalf("missing one of the prompts in scores: %+v", scores)
	}
	if len(cf.ForbiddenViolations) != 1 {
		t.Errorf("code-first should have flagged the violation: %+v", cf)
	}
	if len(intf.ForbiddenViolations) != 0 {
		t.Errorf("intent-first should be clean: %+v", intf)
	}
	if !intf.ContextRetrieved {
		t.Errorf("intent-first must propagate ContextRetrieved=true")
	}
}

// CommandRunner with no Path errors clearly so a misconfigured CLI
// fails fast instead of silently producing empty scores.
func TestCommandRunner_RequiresPath(t *testing.T) {
	cr := &CommandRunner{}
	_, err := cr.Run(AgentRunRequest{
		Fixture: Fixture{Name: "fx"}, Prompt: AgentPromptCodeFirst,
	})
	if err == nil {
		t.Fatal("CommandRunner without Path should error")
	}
	if !strings.Contains(err.Error(), "Path") {
		t.Errorf("error should mention Path: %v", err)
	}
}

// parseRunnerStdout: when the agent emits a JSON object, parse it as
// AgentRunResult. When the output is plain text, infer
// ContextRetrieved by scanning for "mainline context" substrings.
func TestParseRunnerStdout_JSONShape(t *testing.T) {
	out := `{"output": "kept oauth path", "context_retrieved": true}`
	res, err := parseRunnerStdout(AgentPromptIntentFirst, out, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.ContextRetrieved || res.Output != "kept oauth path" {
		t.Errorf("JSON shape not parsed: %+v", res)
	}
}

func TestParseRunnerStdout_PlainTextInfersContextRetrieved(t *testing.T) {
	out := "I ran mainline context --current --json then made the change"
	res, _ := parseRunnerStdout(AgentPromptIntentFirst, out, "", 0, nil)
	if !res.ContextRetrieved {
		t.Errorf("ContextRetrieved should be inferred from substring 'mainline context'")
	}
	if res.Output != out {
		t.Errorf("plain output should be passed through verbatim")
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
