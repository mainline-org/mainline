# Mainline context --query retrieval improvement plan

> **Status:** Draft implementation plan. Not a committed spec.
> **Date:** 2026-05-02
> **Scope:** `mainline context --query` only. Keep retrieval deterministic,
> offline, and explainable; do not introduce embedding search in this plan.

---

## Purpose

`mainline context --query` is the concept-entry retrieval path for agents:
when a task names a design area, bug class, or constraint but not a file, the
agent uses query mode to find prior intents before reading code.

Local Codex history shows this is not the hottest path, but it is a real one:
agents used it for hooks, webhook fan-out, stale draft lifecycle status,
supersession ranking, actor-name display, push guidance, and preflight design
review. These are high-value moments because the relevant signal is historical
intent, not just current source.

The goal is therefore to make query mode more reliable before adding heavier
retrieval machinery.

---

## Current shape

Query mode today is deterministic:

1. Extract keywords from the user query.
2. Use the SQLite reverse index when available to fetch candidate intents whose
   title / goal / summary / decisions / risks / anti_patterns contain those
   keywords.
3. Score candidates with `scoreIntentRelevance`.
4. Filter below `contextRelevanceThreshold`.
5. Sort, enforce supersession ordering, and return the top N.

This is intentionally not embedding or vector search. The semantic signal comes
from structured intent summaries, decisions, risks, anti_patterns, and
fingerprints; the retrieval algorithm itself is keyword-based.

---

## Problems to fix

### P1. Query behavior is hard to audit

The output does not show what the query became after tokenisation. For example,
`ack constraint inherited anti_patterns` silently drops `ack` today because it is
shorter than four characters.

### P2. Short domain terms are fragile

The current token rules can drop useful terms such as `ack`, `JWT`, `API`,
`CLI`, `UI`, `D1`, `KV`, and `S3`.

### P3. Chinese queries are under-supported

The current keyword extraction is mostly ASCII-oriented, while this repo has
many Chinese-language intents and seal summaries.

### P4. Recency can pollute query results

Recent weak-signal intents are useful as fallback candidates, but recency should
not by itself make an unrelated intent look relevant for a query.

### P5. Score is too opaque for tuning

The output has `score` and `reasons`, but not a per-signal breakdown. That makes
weight drift and false positives harder to diagnose.

### P6. Comments and implementation can drift

The weight budget comments in `context_retrieval.go` should match actual scoring
behavior, or future tuning work will optimize against the wrong mental model.

---

## Non-goals

- Do not add default embedding / vector search.
- Do not record every query into Mainline's git-native intent log.
- Do not make scores comparable across separate queries.
- Do not rewrite `context --files` or `context --current` as part of this plan.
- Do not change conflict scoring semantics by accidentally changing the shared
  `keywordsFromText` behavior.

---

## Implementation plan

### Step 1 — Add retrieval eval coverage

Add deterministic regression coverage before changing ranking behavior.

Initial golden queries:

- `ack constraint inherited anti_patterns`
- `context score relevance retrieval`
- `supersession ranking property chain`
- `JWT auth`
- `不要重新引入 subsystem 继承`
- `确认 inherited constraints`
- `zzzz-no-real-topic`

Expected checks:

- known relevant intents appear in top 5;
- expected anti_patterns / inherited constraints are surfaced when applicable;
- nonsense queries do not return a misleading recency-only top 5;
- result ordering remains deterministic.

### Step 2 — Add `query_debug`

Extend query-mode JSON with an audit block. Keep it compact and deterministic.

Example:

```json
{
  "query_debug": {
    "raw": "ack constraint inherited anti_patterns",
    "effective_keywords": ["anti_patterns", "constraint", "inherited"],
    "dropped_terms": [{"term": "ack", "reason": "too_short"}],
    "expanded_terms": {},
    "candidate_count": 12
  }
}
```

This step should not change retrieval results. It only makes the current behavior
visible. `expanded_terms` is deliberately an empty object in PR 1; aliases such
as `ack -> acknowledge / acknowledged / acknowledgement` belong to PR 2.

### Step 3 — Introduce query-specific tokenisation

Do not directly broaden `keywordsFromText`, because it is also used by conflict
scoring. Add a query-specific path instead, for example:

```go
queryTermsFromText(text string) QueryTerms
```

It should support:

- a short-token allowlist for domain acronyms and infrastructure names;
- alias expansion for common workflow abbreviations such as
  `ack -> acknowledge / acknowledged / acknowledgement`;
- CJK fallback tokens for Chinese query text.

The output should feed both candidate selection and score explanation.

### Step 4 — Make recency a boost, not evidence

In query mode, require at least one content signal before an intent can pass the
relevance threshold. Recency can improve ordering among content-matching intents,
but should not be enough to enter the result set on its own.

If a future product need wants recent weak-signal hints, surface them under an
explicit `weak_recent_candidates` section rather than mixing them into the main
top-N.

### Step 5 — Add score breakdown

Extend `relevance` with a per-signal breakdown while preserving the existing
`score` and `reasons` fields.

Example:

```json
{
  "relevance": {
    "score": 0.45,
    "breakdown": {
      "title": 0.10,
      "decision": 0.05,
      "risk": 0.00,
      "followup": 0.00,
      "anti_pattern": 0.15,
      "recency": 0.10
    },
    "reasons": ["title mentions context", "anti_pattern mentions constraint"]
  }
}
```

The text renderer can keep showing only score + reasons unless the JSON output
needs deeper debugging.

### Step 6 — Align scorer comments and tests

After the behavior is covered, make the comments in `context_retrieval.go` match
the implementation exactly. If the intended caps differ from the code, change
the code only with tests that show the ranking effect.

---

## Suggested PR split

This split is intentional. Do not combine the two unless the reviewer explicitly
asks for a single larger change. PR 1 builds the measuring surface; PR 2 changes
retrieval behavior using that surface.

### PR 1: Observability and eval

- add golden query regression tests;
- add `query_debug`;
- add score breakdown;
- align comments with existing behavior.

This should be low risk because ranking behavior can remain unchanged.

PR 1 should not:

- change tokenisation semantics beyond what is required to expose debug output;
- add CJK tokenisation, aliases, or acronym allowlists;
- change recency filtering;
- claim retrieval quality is fixed.

### PR 2: Recall quality

- add query-specific tokenizer;
- support short-token allowlist and aliases;
- add CJK fallback;
- prevent recency-only entries from polluting top results.

This intentionally changes retrieval results and should be gated by the evals
from PR 1.

PR 2 should start only after PR 1 has landed or after the branch already contains
equivalent eval/debug coverage. If PR 2 changes top-N results, the changed
ordering must be explained through `query_debug`, `relevance.breakdown`, and
the golden query tests.

---

## Acceptance checks

### PR 1 acceptance

Run at least:

```bash
go test ./internal/engine -run 'TestContextRetrieval_(QueryGoldenRegressionCoverage|QueryDebugJSONShape|RelevanceBreakdownAddsUpForAdditiveSignals|RelevanceBreakdownExplainsLineageBoost|RelevanceBreakdownExplainsStatusPenalty)$|^TestPropertyScoreBreakdownTracksRawScore$' -count=1
```

Then smoke the live CLI:

```bash
mainline context --query "ack constraint inherited anti_patterns" --json
mainline context --query "JWT" --json
mainline context --query "不要重新引入 subsystem 继承" --json
mainline context --query "zzzz-no-real-topic" --json
```

Expected for PR 1:

- `query_debug` is present for query mode and shows `raw`,
  `effective_keywords`, `dropped_terms`, empty `expanded_terms`, and
  `candidate_count`;
- `ack` / `JWT`-style short tokens are visible as dropped, not silently hidden;
- Chinese text remains unsupported by retrieval, but unsupported non-ASCII terms
  are visible in `dropped_terms`;
- returned intents keep `score` and `reasons`, with additive
  `relevance.breakdown` explaining the rounded score;
- ranking/tokenisation/recency behavior is unchanged.

### Full-plan acceptance after PR 2

Run at least:

```bash
mainline context --query "ack constraint inherited anti_patterns" --json
mainline context --query "JWT" --json
mainline context --query "不要重新引入 subsystem 继承" --json
mainline context --query "zzzz-no-real-topic" --json
```

Expected result:

- the first three queries surface relevant history or explain why no matching
  history exists;
- `query_debug` shows effective, dropped, and expanded terms;
- the nonsense query does not return a misleading recency-only top 5;
- returned `score` values remain deterministic and are explained by `reasons`
  plus `breakdown`;
- no embedding dependency or network call is introduced.
