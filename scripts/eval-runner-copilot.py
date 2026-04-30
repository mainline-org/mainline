#!/usr/bin/env python3
"""eval-runner-copilot.py — eval runner that replays pre-computed agent responses.

This runner embeds the outputs from a Claude Opus 4.6 simulation run
(2026-04-30). It allows running `mainline eval agent --runner` without
an external API key, validating the full harness pipeline.

For live LLM runs, use eval-runner-anthropic.sh instead.

Usage:
    mainline eval agent --runner ./scripts/eval-runner-copilot.py
"""

import json
import sys
import time

# Pre-computed responses from Claude Opus 4.6 sub-agent simulation (2026-04-30)
RESPONSES = {
    "auth-migration": {
        "code_first": "I would grep for all references to the session middleware across the codebase. If it's truly only used on one route, I'd check that route — if the route is /oauth/callback, I would NOT remove the middleware because OAuth callback flows often require session state to pass the authorization code exchange. I'd leave it in place and add a comment explaining why it's still needed there. If the single usage is on a non-OAuth route that already has JWT auth wired up, I'd remove the middleware registration from that route and delete the dead code.",
        "intent_first": "I would remove all unused auth middleware EXCEPT the session middleware on the /oauth path. Two HIGH anti-patterns explicitly forbid removing it: the OAuth callback handler still requires session state during the redirect. My cleanup would remove deprecated session middleware from all non-OAuth routes, leave the /oauth session middleware completely untouched, add a code comment referencing int_oauth_session_kept, and verify no other routes reference the removed middleware. I called mainline context first to retrieve these constraints.",
    },
    "abandoned-approach": {
        "code_first": "I would inspect src/session/redis.go and src/session/store.go to understand how far the prior implementation got. I'd check if the code compiles, has tests, or is wired into anything. If it's incomplete/broken, I would not blindly reuse it — I'd evaluate whether the interface in store.go is sound, then either complete the Redis implementation or rewrite it. I'd wire it into the app's session creation path, replacing the cookie store, and add integration tests against a Redis instance.",
        "intent_first": "I would NOT propose Redis-backed sessions. The HIGH anti-pattern explicitly forbids reintroducing Redis sessions without the multi-region replication design — the prior attempt was abandoned due to unacceptable replication-lag failures. Instead, I would research alternative approaches (signed JWTs with short TTL, edge-compatible token stores, or distributed session store with built-in multi-region replication like DynamoDB Global Tables or CockroachDB). I called mainline context and it surfaced the abandoned intent with its anti-pattern, so I know NOT to retry the same path.",
    },
    "superseded-decision": {
        "code_first": "I would add the new column to both csv.go and parquet.go since both export formats exist and should stay in parity. I'd inspect what data source backs the exports, add the column to the query/struct, then update the CSV writer and Parquet schema. I'd verify the rate limiter in csv.go still applies correctly and run existing tests for both endpoints.",
        "intent_first": "I would add the new column to the Parquet export endpoint only. The MEDIUM anti-pattern forbids adding new fields to the deprecated CSV export — it is being replaced by Parquet (int_new_parquet_export supersedes int_old_csv_export). My changes: add the new column definition to the Parquet schema/writer, update Parquet export tests, NOT touch the CSV export endpoint at all. I called mainline context and it clearly showed CSV is superseded.",
    },
    "stale-intent": {
        "code_first": "I would not just bump the number. First, I'd inspect limiter.go to understand the token bucket parameters (rate, burst size, per-IP vs global). Since the index grew 3x, the backend can likely handle more — but I'd check if there are downstream dependencies (DB connections, memory). I'd propose increasing to 200-300rps with a proportional burst, update the comment to note the index size at time of tuning, and ensure the limiter test covers the new value. I'd flag this as needing load-test validation before production.",
        "intent_first": "The intent is STALE with explicit guidance to verify before applying. I would first inspect the current rate-limiter implementation to see what value is actually deployed. Check current index size and load metrics to assess whether 100rps is still appropriate. NOT blindly apply the old 100rps decision — the risk explicitly warns it was tuned to a smaller index. Make the tuning change only after verifying current conditions. I called mainline context which flagged this as stale, prompting verification.",
    },
    "billing-boundary": {
        "code_first": "I would use src/billing/service.go's GrantTrialExtension method rather than calling db.UpdateBillingState directly from src/auth/. The billing service owns that domain logic — calling the DB directly from auth would bypass any validation, event emission, or state-machine checks in the billing service. My change: in the auth/upgrade handler, import and call billing.GrantTrialExtension(ctx, userID), handling its error. I would not touch the billing table directly.",
        "intent_first": "I would implement the trial extension by calling billing.GrantTrialExtension(userID, days) — the approved API per int_trial_extension_via_billing. I would NOT directly write billing state from the auth package (HIGH anti-pattern — caused BILL-211, BILL-403). Would NOT import billing internal packages into auth. My implementation: in the upgrade button handler, call billing.GrantTrialExtension via the public interface. I called mainline context which surfaced the boundary constraint and the correct API to use.",
    },
    "risk-aware-tests": {
        "code_first": "I would make the one-line change in src/search/limiter.go (100 to 200). Then I would run the tests — both test/limiter_burst_test.go and the regression scrapers in internal/regression/scrapers/. Even if they're slow, they exist for a reason — a rate limiter change is exactly what burst/scraper regression tests are designed to catch. If tests need threshold updates to match the new limit, I'd update them deliberately. I would not skip them just because they're slow.",
        "intent_first": "I would change the token-bucket configuration to 200rps, and critically: run test/limiter_burst_test.go — the HIGH anti-pattern explicitly forbids skipping it. Run the full scraper-regression test suite — sealing without it is forbidden. Only proceed to commit if both pass at 200rps. If they fail, report back that the change is unsafe. I called mainline context which surfaced the test-discipline anti-patterns.",
    },
    "docs-only-intent": {
        "code_first": "I would open AGENTS.md, identify the marker-bounded regions, and add the new seal workflow section inside the appropriate markers. I'd describe: seal --prepare (snapshots worktree), filling the SealResult JSON, seal --submit (validates + syncs), and the worktree-clean contract. I'd follow the existing doc style and update any version/checksum markers if the file uses them.",
        "intent_first": "I would write the new seal workflow section using the term 'agent guidance' for user-facing references to the marker-bounded region. I would NOT use 'managed block' or 'Mainline template' — the MEDIUM anti-pattern forbids reintroducing those terms in user-facing copy like AGENTS.md. Internal Go code keeps 'managed block' as a precise engineering term, but docs use 'agent guidance'. I called mainline context which surfaced the terminology standardisation intent.",
    },
    "refactor-cross-file": {
        "code_first": "I would add backtick-string tokenization in the lexer (new token type TOKEN_BACKTICK_STRING), extend the lexer NextToken to recognize backtick as string delimiter. Add AST node support for backtick string literals. Ensure reader passes backticks through unmodified. Add tests for Parse with backtick strings. Verify the public API Parse(input) to AST error doesn't change signature — this is purely additive.",
        "intent_first": "I would implement backtick strings as a purely additive feature. NOT change the Parse(input) (*AST, error) signature (HIGH anti-pattern — breaks every caller). NOT touch existing lexer whitespace/comment logic (MEDIUM anti-pattern — untested edge cases). My approach: new TOKEN_BACKTICK_STRING in lexer, new AST node type, new parser branch for backtick literals — without altering existing parse paths. I called mainline context which surfaced the refactor constraints.",
    },
}


def main():
    envelope = json.load(sys.stdin)
    fixture_name = envelope["fixture"]["Name"]
    prompt_key = envelope["prompt_key"]

    start = time.time()

    if fixture_name in RESPONSES and prompt_key in RESPONSES[fixture_name]:
        output = RESPONSES[fixture_name][prompt_key]
    else:
        output = f"Unknown fixture/prompt: {fixture_name}/{prompt_key}"

    elapsed_ms = int((time.time() - start) * 1000)

    # Detect context_retrieved by checking for "mainline context" in output
    context_retrieved = "mainline context" in output

    result = {
        "prompt": prompt_key,
        "output": output,
        "duration_ms": elapsed_ms,
        "context_retrieved": context_retrieved,
    }
    json.dump(result, sys.stdout)


if __name__ == "__main__":
    main()
