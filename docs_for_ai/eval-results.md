# Mainline Eval Results

**Date:** 2026-04-29 (initial baseline)
**Catalog:** 8/8 populated (auth-migration, abandoned-approach, superseded-decision, stale-intent, billing-boundary, risk-aware-tests, docs-only-intent, refactor-cross-file)
**Run:** `mainline eval run` — precondition scorer only; LLM-runner round pending external runner wire-up.

This document captures the **first complete run** of the eval harness
against the populated catalog. The findings here are the honest input
to Context Reliability v2.

---

## What the eval is and is not

**The eval is** the substrate for testing the product thesis:
*intent-first agents make fewer mistakes than code-first agents.*
It has two layers:

1. **Precondition scorer** (`mainline eval run`) — deterministic.
   Asks: *given the fixture's task description, does retrieval surface
   the constraining intents?* If not, no agent — code-first or
   intent-first — can possibly act on the constraint, so the comparison
   is moot until retrieval is fixed.

2. **LLM runner** (`mainline eval agent --runner <path>`) — pending.
   Drives both the code-first and intent-first prompts against an
   external runner binary, scores forbidden-list violations, and
   reports the comparison. Requires the user to wire a runner script
   that talks to their LLM of choice (Anthropic / OpenAI / Bedrock /
   local).

This document records layer 1 results. Layer 2 results land here
after the first runner round.

---

## Layer 1 baseline: precondition scorer

| # | Fixture | Status | Finding |
|---|---|---|---|
| 1 | `auth-migration` | ✓ pass | both intents + anti_patterns surface |
| 2 | `abandoned-approach` | ✓ pass | abandoned intent + anti_pattern surface with status=abandoned |
| 3 | `superseded-decision` | ✗ FAIL | superseder retrieved, **superseded intent dropped** |
| 4 | `stale-intent` | ✓ pass | wall-clock-stale classifier fires correctly |
| 5 | `billing-boundary` | ✓ pass | both boundary anti_patterns surface for the auth task |
| 6 | `risk-aware-tests` | ✓ pass | test-discipline anti_pattern surfaces |
| 7 | `docs-only-intent` | ✗ FAIL | terminology intent **does not surface** for an AGENTS.md docs task |
| 8 | `refactor-cross-file` | ✓ pass | signature-preservation anti_pattern surfaces |

**Score: 6/8 pass.** Two failures are **honest signals**, not test
authoring bugs. They point at concrete retrieval gaps the next round
of context-reliability work needs to fix.

---

## Failure analysis

### F1. `superseded-decision`: superseded intent dropped below relevance threshold

**Task:** "add a new column to the export endpoint"
**Expected:** both `int_new_parquet_export` (current decision) AND
`int_old_csv_export` (superseded, with `status=superseded`) appear in
retrieval.
**Actual:** only `int_new_parquet_export` appears.

**Root cause** (deterministic chain, Property 3 + relevance threshold
interaction):

1. `int_new_parquet_export` matches the task by file overlap (touches
   `src/export/csv.go` and `src/export/parquet.go`) and earns a
   relevance score around the threshold.
2. `int_old_csv_export` would match by file overlap too (it touches
   `src/export/csv.go`), but Property 3's score-pinning rule sets its
   final score to `parquet.score - 0.01` to enforce
   "superseder ranks strictly above superseded".
3. The relevance threshold filter (`score < 0.05`) then drops the
   pinned score — either because parquet.score was already close to
   the threshold or because the rounding at `round2` produces 0.04.

**Why this matters:** an agent reading retrieval gets only the new
decision, never sees that there *was* an old plan. This violates the
spec's promise that "agents see the full decision lineage" — exactly
the case Property 3 was added to handle in PR #64.

**v2 candidate fix:** when a superseder is in the result set, its
superseded should be added unconditionally (status="superseded"),
bypassing the relevance threshold. The threshold should filter
*independently-discovered* intents, not links to already-included
intents.

### F2. `docs-only-intent`: docs-only intent doesn't surface for a docs task

**Task:** "write a new section in AGENTS.md describing the seal workflow"
**Expected:** `int_terminology_guide` (records the "agent guidance" vs
"managed block" rule) surfaces with its anti_pattern, so the agent
doesn't reintroduce the deprecated term.
**Actual:** the intent doesn't appear in retrieval.

**Root cause** (retrieval scoring scope):

1. The intent's load-bearing text is in its **anti_patterns**:
   *"Reintroducing 'managed block' or 'Mainline template' in CLI
   output, help text, README, or AGENTS.md"*. That string contains the
   task keyword `AGENTS.md`.
2. PR #86's SQLite-backed `--query` path joins on `intent_decisions`
   and `intent_risks`. **It does not join on `intent_anti_patterns`.**
3. The in-memory keyword scorer in `scoreIntentRelevance` also walks
   only `Decisions` and `Risks`, not `AntiPatterns`.
4. So the intent's most-relevant text is invisible to the scorer.

**Why this matters:** anti_patterns are the load-bearing safety
surface. A reader reading the intent record relies on them. A scorer
that *doesn't* search them is a glaring asymmetry — the agent's
working memory contains the constraint as a target string, but
retrieval can't find it.

**v2 candidate fix:**
1. Add an `intent_anti_patterns.what` join to the SQLite query path.
2. Add `AntiPatterns[*].What` to the in-memory keyword scorer.
3. Consider scoring anti_pattern keyword matches *higher* than
   decision/risk matches — they're the highest-severity surface.

---

## What the LLM-runner round will measure

When the runner is wired (any binary that reads the JSON envelope on
stdin, writes the agent's response on stdout — see `mainline eval
agent --help`), the harness will run each populated fixture twice
per fixture (code-first prompt, intent-first prompt) and score:

- **forbidden-list violations** (substring match against the agent's
  output)
- **`context_retrieved`** (did the agent actually call `mainline
  context` during the run — load-bearing for the intent-first thesis)
- **per-prompt latency**

The expected signal — if the thesis holds — is that intent-first
runs flag forbidden violations less often than code-first runs *on
fixtures whose precondition layer passes*. For F1 and F2 above the
comparison is moot until the v2 fixes land.

A reference runner wrapper template (anthropic-runner.sh /
openai-runner.sh) is **not** shipped here because it requires the
user's API credentials; users wire their own. The
`mainline eval agent --help` output documents the stdin/stdout
contract.

---

## Recommended next steps (per-the-spec sequencing)

1. **Context Reliability v2 — F2 fix first.** Add anti_patterns to
   the keyword search (SQLite + in-memory). This is the higher-
   leverage of the two — anti_patterns are the load-bearing safety
   surface, and the gap is across every fixture, not just
   docs-only-intent.

2. **Context Reliability v2 — F1 fix.** Pin superseded intents into
   the result set whenever their superseder is in. Smaller change;
   easier to land alongside the F2 work.

3. **Re-run `mainline eval run`** — expect 8/8 pass.

4. **Wire an LLM runner.** Write a small bash/python wrapper that
   reads the JSON envelope on stdin, calls Anthropic / OpenAI /
   local-LLM, writes the response on stdout. Run
   `mainline eval agent --runner <path>` to compare code-first vs
   intent-first.

5. **Update this document** with the LLM-runner results, including
   per-prompt latency, forbidden-violation counts, and any
   third-finding-class issues the agent runs surface.

---

## Appendix: layer-1 reproducibility

Run with the binary built from this commit:

```
mainline eval run         # all 8
mainline eval run F1      # one fixture by name
```

Output is deterministic: the fixtures are pure data, BuildView
synthesises the view in-memory, and the precondition scorer is a
pure function. The same fixtures + same binary always produce the
same pass/fail set — no flake, no environment dependence.
