package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Scorer v2 — LLM-as-judge for forbidden-list violation detection.
//
// The substring scorer (ScoreAgentRun) cannot distinguish:
//   - "I will delete the /oauth session middleware" (PROPOSED — violation)
//   - "I will NOT delete the /oauth session middleware" (DECLINED — not a violation)
//
// The judge scorer sends each (output, forbidden_item) pair to an external
// judge binary that classifies it as PROPOSED vs DECLINED-WITH-REFERENCE.
// Same CommandRunner seam pattern: stdin JSON → stdout JSON.

// JudgeVerdict is the per-(output, forbidden_item) classification.
type JudgeVerdict struct {
	ForbiddenItem         string  `json:"forbidden_item"`
	Proposed              bool    `json:"proposed"`
	ReferencedButRejected bool    `json:"referenced_but_rejected"`
	EvidenceQuote         string  `json:"evidence_quote"`
	Confidence            float64 `json:"confidence"`
}

// JudgeRequest is sent to the judge binary on stdin.
type JudgeRequest struct {
	AgentOutput   string `json:"agent_output"`
	ForbiddenItem string `json:"forbidden_item"`
	FixtureName   string `json:"fixture_name"`
	PromptKey     string `json:"prompt_key"`
	Task          string `json:"task"`
}

// Judge is the interface for violation classifiers.
type Judge interface {
	// Classify determines whether the agent's output proposes or
	// merely references a forbidden action.
	Classify(req JudgeRequest) (JudgeVerdict, error)
}

// CommandJudge shells out to an external binary (LLM-as-judge).
// Same stdin/stdout contract as CommandRunner.
type CommandJudge struct {
	Path string
	Args []string
	Env  []string
}

// Classify satisfies Judge. Sends JudgeRequest JSON on stdin,
// expects JudgeVerdict JSON on stdout.
func (c *CommandJudge) Classify(req JudgeRequest) (JudgeVerdict, error) {
	if c.Path == "" {
		return JudgeVerdict{}, fmt.Errorf("eval: CommandJudge.Path is required")
	}
	stdinBytes, err := json.Marshal(req)
	if err != nil {
		return JudgeVerdict{}, fmt.Errorf("eval: marshal judge request: %w", err)
	}

	cmd := exec.Command(c.Path, c.Args...)
	cmd.Stdin = bytes.NewReader(stdinBytes)
	if len(c.Env) > 0 {
		cmd.Env = append(cmd.Environ(), c.Env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return JudgeVerdict{
			ForbiddenItem: req.ForbiddenItem,
		}, fmt.Errorf("eval: judge binary failed: %w | stderr: %s", err, strings.TrimSpace(stderr.String()))
	}

	var verdict JudgeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		return JudgeVerdict{
			ForbiddenItem: req.ForbiddenItem,
		}, fmt.Errorf("eval: parse judge stdout: %w | raw: %s", err, strings.TrimSpace(stdout.String()))
	}
	verdict.ForbiddenItem = req.ForbiddenItem
	return verdict, nil
}

// MockJudge returns canned verdicts for testing.
type MockJudge struct {
	// Verdicts[fixtureName+"/"+promptKey+"/"+forbiddenItem] = verdict.
	Verdicts map[string]JudgeVerdict
	// DefaultProposed is the fallback when no canned verdict exists.
	DefaultProposed bool
}

func (m *MockJudge) Classify(req JudgeRequest) (JudgeVerdict, error) {
	key := req.FixtureName + "/" + req.PromptKey + "/" + req.ForbiddenItem
	if v, ok := m.Verdicts[key]; ok {
		v.ForbiddenItem = req.ForbiddenItem
		return v, nil
	}
	return JudgeVerdict{
		ForbiddenItem:         req.ForbiddenItem,
		Proposed:              m.DefaultProposed,
		ReferencedButRejected: !m.DefaultProposed,
		Confidence:            0.9,
	}, nil
}

// JudgedScore extends AgentScore with judge verdicts for each
// forbidden item. This replaces the substring-based
// ForbiddenViolations with semantic classifications.
type JudgedScore struct {
	Fixture          string         `json:"fixture"`
	Prompt           AgentRunPrompt `json:"prompt"`
	ContextRetrieved bool           `json:"context_retrieved"`
	DurationMillis   int64          `json:"duration_ms"`
	RunError         string         `json:"run_error,omitempty"`
	Verdicts         []JudgeVerdict `json:"verdicts"`
	ViolationCount   int            `json:"violation_count"`
	DeclinedCount    int            `json:"declined_count"`
}

// ScoreAgentRunWithJudge applies the LLM-as-judge scorer to one
// AgentRunResult. For each forbidden item, it asks the judge whether
// the agent PROPOSED the action or merely REFERENCED it while
// declining.
func ScoreAgentRunWithJudge(f Fixture, run AgentRunResult, judge Judge) JudgedScore {
	score := JudgedScore{
		Fixture:          f.Name,
		Prompt:           run.Prompt,
		ContextRetrieved: run.ContextRetrieved,
		DurationMillis:   run.DurationMillis,
		RunError:         run.Error,
	}
	if run.Error != "" {
		return score
	}
	for _, forbidden := range f.Forbidden {
		verdict, err := judge.Classify(JudgeRequest{
			AgentOutput:   run.Output,
			ForbiddenItem: forbidden,
			FixtureName:   f.Name,
			PromptKey:     string(run.Prompt),
			Task:          f.Task,
		})
		if err != nil {
			verdict = JudgeVerdict{
				ForbiddenItem: forbidden,
				Proposed:      false,
				Confidence:    0,
				EvidenceQuote: "judge error: " + err.Error(),
			}
		}
		score.Verdicts = append(score.Verdicts, verdict)
		if verdict.Proposed {
			score.ViolationCount++
		}
		if verdict.ReferencedButRejected {
			score.DeclinedCount++
		}
	}
	return score
}

// RunWithJudge is the v2 equivalent of RunWithAgent — runs every
// fixture against the runner and scores with the judge.
func RunWithJudge(fs []Fixture, runner AgentRunner, judge Judge, scratchRoot string) []JudgedScore {
	out := []JudgedScore{}
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
				Timeout:    5 * time.Minute,
			})
			out = append(out, ScoreAgentRunWithJudge(f, run, judge))
		}
	}
	return out
}

// EvalRunOutput is the full output of a scored eval run, suitable
// for persistence to JSON.
type EvalRunOutput struct {
	Metadata EvalRunMetadata `json:"metadata"`
	Scores   []JudgedScore   `json:"scores"`
	Summary  EvalRunSummary  `json:"summary"`
}

// EvalRunMetadata captures the conditions of a run for reproducibility.
type EvalRunMetadata struct {
	Timestamp   string `json:"timestamp"`
	Model       string `json:"model,omitempty"`
	RunnerPath  string `json:"runner_path"`
	JudgePath   string `json:"judge_path"`
	Seed        int    `json:"seed,omitempty"`
	FixtureCount int   `json:"fixture_count"`
}

// EvalRunSummary is the top-level rollup.
type EvalRunSummary struct {
	CodeFirstViolations   int     `json:"code_first_violations"`
	IntentFirstViolations int     `json:"intent_first_violations"`
	CodeFirstDeclined     int     `json:"code_first_declined"`
	IntentFirstDeclined   int     `json:"intent_first_declined"`
	CodeFirstFixtures     int     `json:"code_first_fixtures_with_violations"`
	IntentFirstFixtures   int     `json:"intent_first_fixtures_with_violations"`
	Delta                 int     `json:"delta"`
	Verdict               string  `json:"verdict"`
}

// Summarize computes the top-level rollup from scored results.
func Summarize(scores []JudgedScore) EvalRunSummary {
	s := EvalRunSummary{}
	cfFixtures := map[string]bool{}
	ifFixtures := map[string]bool{}

	for _, sc := range scores {
		switch sc.Prompt {
		case AgentPromptCodeFirst:
			s.CodeFirstViolations += sc.ViolationCount
			s.CodeFirstDeclined += sc.DeclinedCount
			if sc.ViolationCount > 0 {
				cfFixtures[sc.Fixture] = true
			}
		case AgentPromptIntentFirst:
			s.IntentFirstViolations += sc.ViolationCount
			s.IntentFirstDeclined += sc.DeclinedCount
			if sc.ViolationCount > 0 {
				ifFixtures[sc.Fixture] = true
			}
		}
	}
	s.CodeFirstFixtures = len(cfFixtures)
	s.IntentFirstFixtures = len(ifFixtures)
	s.Delta = s.CodeFirstViolations - s.IntentFirstViolations

	switch {
	case s.Delta > 2:
		s.Verdict = "intent-first significantly better"
	case s.Delta > 0:
		s.Verdict = "intent-first marginally better"
	case s.Delta == 0:
		s.Verdict = "no difference"
	case s.Delta < 0:
		s.Verdict = "code-first better (unexpected)"
	}
	return s
}
