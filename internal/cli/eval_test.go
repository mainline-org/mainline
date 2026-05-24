package cli

import (
	"testing"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/eval"
)

func TestEvalFixtureRetrieverDoesNotSynthesizeUnsurfacedRiskOrFollowupSignals(t *testing.T) {
	f := eval.Fixture{
		Name: "risk-followup-not-surfaced",
		Intents: []eval.SeedIntent{{
			ID:    "int_auth_warning",
			Title: "Record auth cleanup warning",
			What:  "Auth middleware cleanup needs care.",
			ExplicitRisks: []eval.SeedRisk{{
				Text: "Removing the /oauth path can break callback sessions",
				Files: []string{
					"src/auth/middleware.go",
				},
			}},
			ExplicitFollowups: []eval.SeedFollowup{{
				Text: "Audit the /oauth callback after the middleware cleanup",
				Files: []string{
					"src/auth/middleware.go",
				},
			}},
			Files: []string{
				"src/auth/middleware.go",
			},
			Status:  domain.StatusMerged,
			AgeDays: 3,
		}},
		Task:      "clean up auth middleware",
		TaskFiles: []string{"src/auth/middleware.go"},
		Expected: []eval.ExpectedItem{
			{
				IntentID: "int_auth_warning",
				Signal:   eval.ExpectedSignal{Kind: eval.SignalRisk, Match: "/oauth path"},
			},
			{
				IntentID: "int_auth_warning",
				Signal:   eval.ExpectedSignal{Kind: eval.SignalFollowup, Match: "/oauth callback"},
			},
		},
	}

	retriever, err := newFixtureRetriever(f)
	if err != nil {
		t.Fatalf("newFixtureRetriever: %v", err)
	}
	res, err := eval.RunFixture(f, retriever, 10)
	if err != nil {
		t.Fatalf("RunFixture: %v", err)
	}
	if res.Pass {
		t.Fatalf("fixture should fail because RetrieveContext does not surface risk/follow-up signals to the agent: %+v", res)
	}
	for _, item := range res.Items {
		if item.Pass {
			t.Fatalf("expected item %s to fail, got %+v", item.IntentID, res.Items)
		}
	}
}
