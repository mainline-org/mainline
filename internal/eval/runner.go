package eval

// This file is the bridge between the harness substrate (eval.go +
// fixtures.go, both pure data) and the engine layer that actually
// runs retrieval. Bridges live here, not in eval.go, so the
// substrate stays import-free of engine — that keeps the eval
// package cheap to import from tests and lets future LLM-runner
// code depend on eval without pulling in the whole engine.
//
// The Run* functions take an injected retriever instead of an
// engine.Service so this file does not import engine. Callers in
// internal/cli/eval.go (or future LLM runners) wire the engine in.

// Retriever is the interface eval needs from a retrieval backend.
// The engine satisfies it, but the indirection keeps eval testable
// with a fake.
type Retriever interface {
	// RetrieveByQuery runs context retrieval in query mode and
	// returns the per-intent shape ScoreFixture needs. Implementers
	// must apply the same logic mainline context --query would.
	RetrieveByQuery(query string, limit int) ([]Retrieved, error)
}

// RunFixture executes one fixture against the retriever and scores
// it. The retriever is expected to have been set up against a view
// produced by BuildView(fixture); the harness CLI handles that wire-
// up.
func RunFixture(f Fixture, r Retriever, limit int) (ScoreResult, error) {
	got, err := r.RetrieveByQuery(f.Task, limit)
	if err != nil {
		return ScoreResult{Fixture: f.Name, Description: f.Description, Pass: false}, err
	}
	return ScoreFixture(f, got), nil
}

// RunAll runs every fixture in fs against the retriever. Returns
// the per-fixture score and a top-level pass flag (all fixtures
// pass).
func RunAll(fs []Fixture, r Retriever, limit int) (Summary, error) {
	out := Summary{}
	for _, f := range fs {
		// Skip stub fixtures (no Intents seeded yet) so the harness
		// doesn't false-fail on placeholders. Stubs still appear in
		// the summary as "skipped".
		if len(f.Intents) == 0 {
			out.Results = append(out.Results, ScoreResult{
				Fixture:     f.Name,
				Description: f.Description,
				Pass:        false,
			})
			out.Skipped++
			continue
		}
		res, err := RunFixture(f, r, limit)
		if err != nil {
			return out, err
		}
		out.Results = append(out.Results, res)
		if res.Pass {
			out.Passed++
		} else {
			out.Failed++
		}
	}
	out.AllPassed = out.Failed == 0 && out.Skipped == 0
	return out, nil
}

// Summary is the harness-wide rollup the CLI prints.
type Summary struct {
	Results   []ScoreResult `json:"results"`
	Passed    int           `json:"passed"`
	Failed    int           `json:"failed"`
	Skipped   int           `json:"skipped"`
	AllPassed bool          `json:"all_passed"`
}
