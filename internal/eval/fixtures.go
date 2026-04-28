package eval

import "github.com/mainline-org/mainline/internal/domain"

// Fixtures returns the canonical eval set. v1 ships a representative
// subset of the 8 scenarios listed in spec §9 Step 5; the remaining
// fixtures are stubs (Description set, Intents empty) so the catalog
// is visible from the start and can be filled in incrementally.
//
// Each populated fixture is a *precondition* test: it verifies that
// retrieval surfaces the constraint at all. The agent-side
// validation (does an intent-first agent actually act on the
// constraint?) is the next layer's job and lives in the LLM runner.
func Fixtures() []Fixture {
	return []Fixture{
		authMigration(),
		abandonedApproach(),
		supersededDecision(),
		staleIntent(),
		billingBoundary(),

		// Stubs — populate as fixture authors ship ground truth.
		{Name: "risk-aware-tests",
			Description: "[stub] must run specific regression tests when touching risk-tagged code"},
		{Name: "docs-only-intent",
			Description: "[stub] empty diff still gets a decision record"},
		{Name: "refactor-cross-file",
			Description: "[stub] refactor preserves behaviour across files"},
	}
}

// authMigration is the canonical Step-5 example: an OAuth flow that
// keeps a session middleware around for the /oauth callback while
// the rest of the app moved to JWT. The agent task is to "clean up
// unused auth middleware". Retrieval must surface the
// "do not delete legacy session middleware on /oauth" anti-pattern,
// because that is the only thing standing between the cleanup task
// and a production outage.
func authMigration() Fixture {
	return Fixture{
		Name:        "auth-migration",
		Description: "Auth migration: cleanup must preserve /oauth session path",
		Intents: []SeedIntent{
			{
				ID:    "int_auth_migration",
				Title: "Migrate access auth to JWT",
				Goal:  "migrate access auth from session-based to JWT",
				What:  "Replace session middleware with JWT validation on /api routes.",
				Why:   "Sessions don't scale across regions; JWT is stateless.",
				Decisions: []domain.Decision{
					{Point: "auth shape", Chose: "JWT for /api", Rationale: "stateless"},
				},
				Risks: []string{"old mobile clients still send session cookies"},
				AntiPatterns: []domain.AntiPattern{
					{
						What:     "Removing legacy session middleware on /oauth path",
						Why:      "OAuth callback handler still requires session state during the redirect",
						Severity: "high",
					},
				},
				Files:      []string{"src/auth/middleware.go", "src/auth/jwt.go"},
				Subsystems: []string{"auth"},
				Status:     domain.StatusMerged,
				AgeDays:    14,
			},
			{
				ID:    "int_oauth_session_kept",
				Title: "Keep session middleware for /oauth callback",
				Goal:  "explicitly retain session middleware on the /oauth route",
				What:  "Bypass JWT-only routing for /oauth so session cookie still flows.",
				Why:   "OAuth provider's redirect carries state in a session cookie; switching to JWT mid-handshake breaks the callback.",
				Decisions: []domain.Decision{
					{Point: "oauth route", Chose: "session middleware retained", Rationale: "OAuth callback contract"},
				},
				AntiPatterns: []domain.AntiPattern{
					{
						What:     "Removing the /oauth session middleware in any 'cleanup' or 'refactor' pass",
						Why:      "OAuth callback handler still requires session state",
						Severity: "high",
					},
				},
				Files:      []string{"src/auth/middleware.go", "src/auth/oauth.go"},
				Subsystems: []string{"auth"},
				Status:     domain.StatusMerged,
				AgeDays:    7,
			},
		},
		Task: "clean up unused auth middleware",
		Expected: []ExpectedItem{
			{
				IntentID:         "int_oauth_session_kept",
				AntiPatternMatch: "/oauth session middleware",
				Note:             "the session-keep intent must surface its own anti_pattern",
			},
			{
				IntentID:         "int_auth_migration",
				AntiPatternMatch: "legacy session middleware on /oauth",
				Note:             "the migration intent itself flagged /oauth as a no-touch zone",
			},
		},
		Forbidden: []string{
			"delete the /oauth session middleware",
			"remove session cookie handling from auth/middleware.go without acknowledging the OAuth constraint",
		},
	}
}

// abandonedApproach: a previous attempt to solve the same problem
// was abandoned. A new attempt must NOT silently repeat it.
// Retrieval must surface the abandoned intent with status=abandoned
// so the agent can read why it was abandoned before retrying.
func abandonedApproach() Fixture {
	return Fixture{
		Name:        "abandoned-approach",
		Description: "Abandoned-approach: don't silently repeat a failed migration plan",
		Intents: []SeedIntent{
			{
				ID:    "int_failed_redis_session",
				Title: "Replace cookie sessions with Redis-backed sessions",
				Goal:  "move session state out of cookies into Redis",
				What:  "Stand up a Redis cluster, write session middleware to read/write keys.",
				Why:   "Cookie sessions don't scale beyond a single region.",
				Risks: []string{"Redis cluster ops burden", "p95 latency hit on every request"},
				AntiPatterns: []domain.AntiPattern{
					{
						What:     "Reintroducing Redis-backed sessions without the multi-region replication design",
						Why:      "We hit unacceptable replication-lag failures during the original rollout",
						Severity: "high",
					},
				},
				Files:      []string{"src/session/redis.go", "src/session/store.go"},
				Subsystems: []string{"session"},
				Status:     domain.StatusAbandoned,
				AgeDays:    60,
			},
		},
		Task: "scale session storage out of single-region cookies",
		Expected: []ExpectedItem{
			{
				IntentID:         "int_failed_redis_session",
				MinStatus:        "abandoned",
				AntiPatternMatch: "redis-backed sessions",
				Note:             "abandoned intent must surface with status=abandoned and its anti_pattern intact",
			},
		},
		Forbidden: []string{
			"propose Redis-backed sessions without acknowledging the prior abandonment",
			"silently retry the multi-region cluster plan",
		},
	}
}

// supersededDecision: an old decision was replaced by a newer one.
// Retrieval must surface the new one ABOVE the old one (Property 3),
// and the old one's status must be `superseded`.
func supersededDecision() Fixture {
	return Fixture{
		Name:        "superseded-decision",
		Description: "Superseded-decision: agent must use the replacement, not the old plan",
		Intents: []SeedIntent{
			{
				ID:    "int_old_csv_export",
				Title: "Build CSV export endpoint",
				Goal:  "expose /export.csv for analyst consumption",
				What:  "Server-rendered CSV from /export.csv with a 60s rate limit.",
				Why:   "Analysts asked for CSV; rate limit because it's expensive.",
				Decisions: []domain.Decision{
					{Point: "format", Chose: "CSV", Rationale: "easiest for analyst tooling"},
				},
				Files:        []string{"src/export/csv.go"},
				Subsystems:   []string{"export"},
				Status:       domain.StatusSuperseded,
				SupersededBy: "int_new_parquet_export",
				AgeDays:      45,
			},
			{
				ID:    "int_new_parquet_export",
				Title: "Replace CSV export with Parquet",
				Goal:  "move analyst export from CSV to Parquet for column store ingest",
				What:  "Parquet export under /export.parquet; deprecate /export.csv with 90-day window.",
				Why:   "Analyst tools moved to Snowflake which prefers Parquet; CSV path is deprecated.",
				Decisions: []domain.Decision{
					{Point: "format", Chose: "Parquet", Rationale: "Snowflake-native, smaller, typed"},
				},
				AntiPatterns: []domain.AntiPattern{
					{
						What:     "Adding new fields to the deprecated CSV export endpoint",
						Why:      "It is going away; new fields belong on Parquet only",
						Severity: "medium",
					},
				},
				Files:      []string{"src/export/parquet.go", "src/export/csv.go"},
				Subsystems: []string{"export"},
				Status:     domain.StatusMerged,
				AgeDays:    14,
			},
		},
		Task: "add a new column to the export endpoint",
		Expected: []ExpectedItem{
			{
				IntentID: "int_new_parquet_export",
				Note:     "the current effective decision (Parquet) must appear",
			},
			{
				IntentID:  "int_old_csv_export",
				MinStatus: "superseded",
				Note:      "the old CSV decision must appear with status=superseded",
			},
		},
		Forbidden: []string{
			"add the new column to the CSV endpoint",
			"treat the CSV plan as the current source of truth",
		},
	}
}

// billingBoundary: the auth subsystem must not write billing state.
// Two intents establish the cross-subsystem rule. The agent task is
// "make the trial-extension flow work" — the lazy implementation
// reaches across the boundary; the right implementation calls the
// billing service. Retrieval must surface the boundary anti-pattern
// so the agent sees the constraint before drafting the change.
func billingBoundary() Fixture {
	return Fixture{
		Name:        "billing-boundary",
		Description: "Cross-subsystem boundary: auth must not write billing state directly",
		Intents: []SeedIntent{
			{
				ID:    "int_billing_boundary",
				Title: "Auth must not write billing state directly",
				Goal:  "establish the auth ↔ billing boundary",
				What:  "Auth code reads billing state via the billing service interface; never writes billing tables directly. All write paths go through billing.UpdateSubscription / billing.GrantTrialExtension.",
				Why:   "Billing is the system-of-record for subscription state. Direct writes from auth bypass billing's invariants (proration, audit log, tax compliance) and have caused two prior incidents.",
				Decisions: []domain.Decision{
					{Point: "boundary direction", Chose: "auth → billing service interface only", Rationale: "billing owns its invariants"},
				},
				AntiPatterns: []domain.AntiPattern{
					{
						What:     "Calling db.UpdateBillingState / writing to the billing table from anything in src/auth/",
						Why:      "Bypasses billing's audit log + proration logic; has caused two prod incidents (BILL-211, BILL-403). Always go through billing.<Action>() RPC.",
						Severity: "high",
					},
					{
						What:     "Importing src/billing/internal from src/auth/",
						Why:      "Internal billing types should not leak into auth. Use the public billing service interface only.",
						Severity: "high",
					},
				},
				Files:      []string{"src/auth/middleware.go", "src/auth/session.go", "src/billing/service.go"},
				Subsystems: []string{"auth", "billing"},
				Status:     domain.StatusMerged,
				AgeDays:    21,
			},
			{
				ID:    "int_trial_extension_via_billing",
				Title: "Trial extensions go through billing.GrantTrialExtension",
				Goal:  "thread trial-extension flow through the billing service",
				What:  "Auth side calls billing.GrantTrialExtension(userID, days); billing performs the database write, audit log entry, and proration.",
				Why:   "Trial extensions are a billing event. Auth knows when (user clicks 'extend'); billing knows how (write + audit + tax).",
				Decisions: []domain.Decision{
					{Point: "extension API shape", Chose: "billing.GrantTrialExtension(userID, days) error", Rationale: "single billing-side entry, audit-logged"},
				},
				Files:      []string{"src/billing/service.go", "src/billing/trial.go", "src/auth/middleware.go"},
				Subsystems: []string{"billing", "auth"},
				Status:     domain.StatusMerged,
				AgeDays:    7,
			},
		},
		Task: "make the trial-extension flow work for the new free-tier upgrade button",
		Expected: []ExpectedItem{
			{
				IntentID:         "int_billing_boundary",
				AntiPatternMatch: "from anything in src/auth/",
				Note:             "the boundary intent's anti_pattern must reach the agent before they draft the change",
			},
			{
				IntentID: "int_trial_extension_via_billing",
				Note:     "the chosen API for the exact task must surface so the agent uses it",
			},
		},
		Forbidden: []string{
			"write to the billing table from src/auth",
			"call db.UpdateBillingState from auth",
			"import src/billing/internal from src/auth",
		},
	}
}

// staleIntent: an intent that's old enough to be classified stale.
// Retrieval must mark it as stale (not abandoned, not current) so
// the agent's verify-against-current-code reflex fires.
func staleIntent() Fixture {
	return Fixture{
		Name:        "stale-intent",
		Description: "Stale-intent: old decision still in scope, but agent must verify before acting",
		Intents: []SeedIntent{
			{
				ID:    "int_stale_rate_limiter",
				Title: "Rate-limit the /search endpoint at 100rps",
				Goal:  "add a per-IP rate limiter to /search",
				What:  "Token bucket at 100rps per source IP.",
				Why:   "/search was the entry point for a scraper outage in the original incident.",
				Decisions: []domain.Decision{
					{Point: "limit shape", Chose: "100rps token bucket", Rationale: "100 was the empirical sustainable rate at the time"},
				},
				Risks: []string{
					"100rps was tuned to a smaller index; if the index has grown, this may be too aggressive",
				},
				Files:      []string{"src/search/limiter.go"},
				Subsystems: []string{"search"},
				Status:     domain.StatusMerged,
				AgeDays:    120, // > staleAge (90 days)
			},
		},
		Task: "tune the search rate limiter",
		Expected: []ExpectedItem{
			{
				IntentID:  "int_stale_rate_limiter",
				MinStatus: "stale",
				Note:      "old wall-clock age must classify the intent as stale, prompting verification",
			},
		},
		Forbidden: []string{
			"apply the 100rps limit blindly without checking current load",
			"trust the 'empirical sustainable rate' line as if today's index were the same size",
		},
	}
}
