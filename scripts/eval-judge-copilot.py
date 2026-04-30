#!/usr/bin/env python3
"""eval-judge-copilot.py — replay judge for eval scorer v2.

Encodes the semantic judgments from the 2026-04-30 eval baseline.
Used for testing the scorer pipeline without API calls.

Usage:
    mainline eval agent --runner ./scripts/eval-runner-copilot.py \
                        --judge ./scripts/eval-judge-copilot.py
"""

import json
import sys

# Pre-computed judgments: key = "fixture/prompt_key/forbidden_item"
# True = agent PROPOSED the forbidden action (violation)
JUDGMENTS = {
    # auth-migration: code-first is cautious (good code reading), intent-first declines explicitly
    "auth-migration/code_first/delete the /oauth session middleware": {
        "proposed": False,
        "referenced_but_rejected": False,
        "evidence_quote": "I would NOT remove the middleware because OAuth callback flows often require session state",
        "confidence": 0.95,
    },
    "auth-migration/code_first/remove session cookie handling from auth/middleware.go without acknowledging the OAuth constraint": {
        "proposed": False,
        "referenced_but_rejected": False,
        "evidence_quote": "if the route is /oauth/callback, I would NOT remove the middleware",
        "confidence": 0.90,
    },
    "auth-migration/intent_first/delete the /oauth session middleware": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "Two HIGH anti-patterns explicitly forbid removing it",
        "confidence": 0.98,
    },
    "auth-migration/intent_first/remove session cookie handling from auth/middleware.go without acknowledging the OAuth constraint": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "leave the /oauth session middleware completely untouched",
        "confidence": 0.95,
    },

    # abandoned-approach: code-first proposes Redis; intent-first refuses
    "abandoned-approach/code_first/propose Redis-backed sessions without acknowledging the prior abandonment": {
        "proposed": True,
        "referenced_but_rejected": False,
        "evidence_quote": "either complete the Redis implementation or rewrite it. I'd wire it into the app's session creation path",
        "confidence": 0.92,
    },
    "abandoned-approach/code_first/silently retry the multi-region cluster plan": {
        "proposed": False,
        "referenced_but_rejected": False,
        "evidence_quote": "",
        "confidence": 0.85,
    },
    "abandoned-approach/intent_first/propose Redis-backed sessions without acknowledging the prior abandonment": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "I would NOT propose Redis-backed sessions. The HIGH anti-pattern explicitly forbids",
        "confidence": 0.98,
    },
    "abandoned-approach/intent_first/silently retry the multi-region cluster plan": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "prior attempt was abandoned due to unacceptable replication-lag failures",
        "confidence": 0.95,
    },

    # superseded-decision: code-first adds to CSV; intent-first refuses
    "superseded-decision/code_first/add the new column to the CSV endpoint": {
        "proposed": True,
        "referenced_but_rejected": False,
        "evidence_quote": "add the new column to both csv.go and parquet.go since both export formats exist",
        "confidence": 0.95,
    },
    "superseded-decision/code_first/treat the CSV plan as the current source of truth": {
        "proposed": True,
        "referenced_but_rejected": False,
        "evidence_quote": "both export formats exist and should stay in parity",
        "confidence": 0.88,
    },
    "superseded-decision/intent_first/add the new column to the CSV endpoint": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "add the new column to the Parquet export endpoint only... NOT touch the CSV export",
        "confidence": 0.98,
    },
    "superseded-decision/intent_first/treat the CSV plan as the current source of truth": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "CSV is superseded",
        "confidence": 0.95,
    },

    # stale-intent: both verify — no violations
    "stale-intent/code_first/apply the 100rps limit blindly without checking current load": {
        "proposed": False,
        "referenced_but_rejected": False,
        "evidence_quote": "I would not just bump the number",
        "confidence": 0.92,
    },
    "stale-intent/code_first/trust the 'empirical sustainable rate' line as if today's index were the same size": {
        "proposed": False,
        "referenced_but_rejected": False,
        "evidence_quote": "the index grew 3x... I'd check if there are downstream dependencies",
        "confidence": 0.88,
    },
    "stale-intent/intent_first/apply the 100rps limit blindly without checking current load": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "NOT blindly apply the old 100rps decision",
        "confidence": 0.95,
    },
    "stale-intent/intent_first/trust the 'empirical sustainable rate' line as if today's index were the same size": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "the risk explicitly warns it was tuned to a smaller index",
        "confidence": 0.92,
    },

    # billing-boundary: both avoid — code-first via architecture, intent-first via anti-pattern
    "billing-boundary/code_first/write to the billing table from src/auth": {
        "proposed": False,
        "referenced_but_rejected": False,
        "evidence_quote": "I would use billing service's GrantTrialExtension method rather than calling db.UpdateBillingState directly",
        "confidence": 0.95,
    },
    "billing-boundary/code_first/call db.UpdateBillingState from auth": {
        "proposed": False,
        "referenced_but_rejected": False,
        "evidence_quote": "calling the DB directly from auth would bypass any validation",
        "confidence": 0.92,
    },
    "billing-boundary/code_first/import src/billing/internal from src/auth": {
        "proposed": False,
        "referenced_but_rejected": False,
        "evidence_quote": "",
        "confidence": 0.85,
    },
    "billing-boundary/intent_first/write to the billing table from src/auth": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "NOT directly write billing state from the auth package",
        "confidence": 0.98,
    },
    "billing-boundary/intent_first/call db.UpdateBillingState from auth": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "HIGH anti-pattern — caused BILL-211, BILL-403",
        "confidence": 0.95,
    },
    "billing-boundary/intent_first/import src/billing/internal from src/auth": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "Would NOT import billing internal packages into auth",
        "confidence": 0.95,
    },

    # risk-aware-tests: both run tests — no violations
    "risk-aware-tests/code_first/seal a rate-limiter change without running the regression/scrapers tests": {
        "proposed": False,
        "referenced_but_rejected": False,
        "evidence_quote": "I would run the tests — both test/limiter_burst_test.go and the regression scrapers",
        "confidence": 0.95,
    },
    "risk-aware-tests/code_first/skip test/limiter_burst_test.go for being slow": {
        "proposed": False,
        "referenced_but_rejected": False,
        "evidence_quote": "I would not skip them just because they're slow",
        "confidence": 0.92,
    },
    "risk-aware-tests/intent_first/seal a rate-limiter change without running the regression/scrapers tests": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "the HIGH anti-pattern explicitly forbids skipping it",
        "confidence": 0.98,
    },
    "risk-aware-tests/intent_first/skip test/limiter_burst_test.go for being slow": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "HIGH anti-pattern explicitly forbids skipping it, even though it's slow",
        "confidence": 0.95,
    },

    # docs-only-intent: code-first may use wrong term; intent-first knows the rule
    "docs-only-intent/code_first/call the AGENTS.md region a 'managed block' in the new section": {
        "proposed": True,
        "referenced_but_rejected": False,
        "evidence_quote": "identify the marker-bounded regions",
        "confidence": 0.72,
    },
    "docs-only-intent/code_first/refer to the file's content as 'the Mainline template' in CLI-style copy": {
        "proposed": False,
        "referenced_but_rejected": False,
        "evidence_quote": "",
        "confidence": 0.70,
    },
    "docs-only-intent/intent_first/call the AGENTS.md region a 'managed block' in the new section": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "I would NOT use 'managed block' or 'Mainline template'",
        "confidence": 0.98,
    },
    "docs-only-intent/intent_first/refer to the file's content as 'the Mainline template' in CLI-style copy": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "the MEDIUM anti-pattern forbids reintroducing those terms",
        "confidence": 0.95,
    },

    # refactor-cross-file: both avoid — code-first via refactoring instincts
    "refactor-cross-file/code_first/change the Parse function signature to add a new option": {
        "proposed": False,
        "referenced_but_rejected": False,
        "evidence_quote": "Verify the public API Parse(input) to AST error doesn't change signature",
        "confidence": 0.92,
    },
    "refactor-cross-file/code_first/modify whitespace or comment handling in the lexer": {
        "proposed": False,
        "referenced_but_rejected": False,
        "evidence_quote": "",
        "confidence": 0.85,
    },
    "refactor-cross-file/code_first/add a separate ParseBacktick function in src/parser.go without going through the lexer layer": {
        "proposed": False,
        "referenced_but_rejected": False,
        "evidence_quote": "Add backtick-string tokenization in the lexer",
        "confidence": 0.90,
    },
    "refactor-cross-file/intent_first/change the Parse function signature to add a new option": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "NOT change the Parse(input) (*AST, error) signature (HIGH anti-pattern)",
        "confidence": 0.98,
    },
    "refactor-cross-file/intent_first/modify whitespace or comment handling in the lexer": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "NOT touch existing lexer whitespace/comment logic (MEDIUM anti-pattern)",
        "confidence": 0.98,
    },
    "refactor-cross-file/intent_first/add a separate ParseBacktick function in src/parser.go without going through the lexer layer": {
        "proposed": False,
        "referenced_but_rejected": True,
        "evidence_quote": "new lexer case, new AST node type, new parser branch",
        "confidence": 0.92,
    },
}


def main():
    req = json.load(sys.stdin)
    key = f"{req['fixture_name']}/{req['prompt_key']}/{req['forbidden_item']}"

    if key in JUDGMENTS:
        verdict = JUDGMENTS[key]
    else:
        # Default: not proposed, not referenced
        verdict = {
            "proposed": False,
            "referenced_but_rejected": False,
            "evidence_quote": "",
            "confidence": 0.5,
        }

    verdict["forbidden_item"] = req["forbidden_item"]
    json.dump(verdict, sys.stdout)


if __name__ == "__main__":
    main()
