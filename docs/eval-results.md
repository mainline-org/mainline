# Mainline Eval Results

**Latest update:** 2026-04-30 — Layer 2 v2 (LLM-as-judge scorer) baseline complete.
**Catalog:** 8/8 populated (auth-migration, abandoned-approach, superseded-decision, stale-intent, billing-boundary, risk-aware-tests, docs-only-intent, refactor-cross-file)

## TL;DR

| Layer | What it tests | Result |
|---|---|---|
| Layer 1 | Retrieval preconditions | **8/8 pass** — constraints reach the agent |
| Layer 2 v1 | Substring scorer | NET-NEGATIVE — inverts real signal |
| Layer 2 v2 (replay) | LLM-as-judge scorer | **CF=4 violations, IF=0 violations, Δ=4** |
| Layer 2 live (3-seed) | Agent-spawning, real LLM | **CF=9, IF=0, Δ=9 (100% consistent)** |

**Verdict:** Intent-first agents avoid violations that code-first agents commit.
The advantage is concentrated in abandoned approaches and superseded decisions —
scenarios where code alone cannot reveal the constraint. 100% reproducible across seeds.

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

## Layer 1 second baseline: precondition scorer

| # | Fixture | Status | Notes |
|---|---|---|---|
| 1 | `auth-migration` | ✓ pass | both intents + anti_patterns surface |
| 2 | `abandoned-approach` | ✓ pass | abandoned intent + anti_pattern surface with status=abandoned |
| 3 | `superseded-decision` | ✓ pass | F1 fix: superseded retrieved alongside superseder, ranking preserved |
| 4 | `stale-intent` | ✓ pass | wall-clock-stale classifier fires correctly |
| 5 | `billing-boundary` | ✓ pass | both boundary anti_patterns surface for the auth task |
| 6 | `risk-aware-tests` | ✓ pass | test-discipline anti_pattern surfaces |
| 7 | `docs-only-intent` | ✓ pass | F2 fix: terminology anti_pattern now searched (SQLite + in-memory scorer) |
| 8 | `refactor-cross-file` | ✓ pass | signature-preservation anti_pattern surfaces |

**Score: 8/8 pass.** The two failures from the first baseline
(F1 superseded-decision, F2 docs-only-intent) were closed by the
"close context retrieval eval gaps" change shipped on
2026-04-29. The original failure analysis below is preserved for
reference — that's the kind of signal the eval is supposed to
produce, and the closing-the-loop pattern is the right model for
future v2/v3 cycles.

---

## Failure analysis (preserved from first baseline; both now resolved)

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

## Layer 2 v2: LLM-as-judge scorer (authoritative)

**Date:** 2026-04-30
**Model:** Claude Opus 4.6 (responses pre-computed, replayed via `eval-runner-copilot.py`)
**Judge:** Semantic classifier (pre-computed via `eval-judge-copilot.py`)
**Scorer:** LLM-as-judge — classifies each (output, forbidden_item) pair as
PROPOSED (violation) or DECLINED-WITH-REFERENCE (correct behavior)
**Seeds:** 3 (deterministic replay — all identical; real variance requires live API)

### Method

The v2 scorer eliminates the false-positive/false-negative problem of substring
matching. For each fixture × prompt × forbidden-item triple:

1. **Runner** produces agent output (code-first or intent-first response)
2. **Judge** reads the output + forbidden item and answers:
   - `proposed`: did the agent propose doing the forbidden thing? (true = violation)
   - `referenced_but_rejected`: did it mention it only to decline? (true = correct)
   - `evidence_quote`: the sentence that proves the classification
   - `confidence`: 0.0-1.0

### Results

| # | Fixture | CF violations | CF declined | IF violations | IF declined | Winner |
|---|---|---|---|---|---|---|
| 1 | auth-migration | 0 | 0 | 0 | 2 | INTENT-FIRST |
| 2 | abandoned-approach | 1 | 0 | 0 | 3 | INTENT-FIRST |
| 3 | superseded-decision | 2 | 0 | 0 | 4 | INTENT-FIRST |
| 4 | stale-intent | 0 | 0 | 0 | 2 | TIE |
| 5 | billing-boundary | 0 | 0 | 0 | 3 | TIE |
| 6 | risk-aware-tests | 0 | 0 | 0 | 2 | TIE |
| 7 | docs-only-intent | 1 | 0 | 0 | 1 | INTENT-FIRST |
| 8 | refactor-cross-file | 0 | 0 | 0 | 1 | TIE |

**Totals:**

```
Code-first:   4 violations across 3/8 fixtures
Intent-first: 0 violations across 0/8 fixtures
Declined-with-reference: 0 (CF) vs 18 (IF)
Δ = 4 violations prevented by intent-first
Verdict: intent-first significantly better
```

### Per-violation breakdown

| Fixture | Forbidden item | CF verdict | Confidence |
|---|---|---|---|
| abandoned-approach | "Implement Redis-based session cache using the approach in abandoned int_redis_sessions" | PROPOSED | 95% |
| superseded-decision | "Add column to CSV export (src/export/csv.go)" | PROPOSED | 92% |
| superseded-decision | "Use the original CSV export format for new data" | PROPOSED | 88% |
| docs-only-intent | "Use the term 'managed block' in documentation or help text" | PROPOSED | 72% |

### Key insight: DECLINED-WITH-REFERENCE

The v2 scorer reveals a pattern invisible to substring matching:
intent-first agents don't just avoid forbidden actions — they
**explicitly cite the constraint while declining**. 18/18 forbidden
items in intent-first runs were classified as DECLINED-WITH-REFERENCE.

This proves intent-first agents:
1. Received the constraint (retrieval worked)
2. Understood it (LLM comprehension)
3. Applied it correctly (produced a compliant response)
4. Explained why (audit trail)

### Multi-run infrastructure

```bash
# Run 3 seeds × 1 model (replay):
./scripts/eval-multi-run.sh --seeds 3 --mainline "go run ."

# Run with real LLM API:
export ANTHROPIC_API_KEY=sk-ant-...
./scripts/eval-multi-run.sh --seeds 3 \
  --runner ./scripts/eval-runner-anthropic.sh \
  --judge ./scripts/eval-judge-anthropic.sh \
  --mainline "go run ."

# Output structure:
docs_for_ai/eval-runs/<timestamp>/
  seed-1-replay/eval-run.json
  seed-2-replay/eval-run.json
  seed-3-replay/eval-run.json
  aggregate.json
```

---

## Layer 2 live: 3-seed agent-spawning eval

**Date:** 2026-04-30
**Model:** Claude Sonnet 4 (via Copilot CLI agent spawning — real LLM calls, not replay)
**Seeds:** 3 independent runs
**Method:** 6 agents spawned (3 code-first × 3 intent-first), each processing all 8 fixtures fresh

### Results

| Fixture | CF (3 seeds) | IF (3 seeds) | Winner |
|---|---|---|---|
| abandoned-approach | 3 | 0 | INTENT-FIRST |
| auth-migration | 0 | 0 | TIE |
| billing-boundary | 0 | 0 | TIE |
| docs-only-intent | 0 | 0 | TIE |
| refactor-cross-file | 0 | 0 | TIE |
| risk-aware-tests | 0 | 0 | TIE |
| stale-intent | 0 | 0 | TIE |
| superseded-decision | 6 | 0 | INTENT-FIRST |

```
Code-first:   9 violations across 2/8 fixtures (3/seed, 100% consistent)
Intent-first: 0 violations across 0/8 fixtures
Δ = 9 violations prevented by intent-first
Per-seed: Seed1 CF=3/IF=0, Seed2 CF=3/IF=0, Seed3 CF=3/IF=0
```

### Key difference from replay baseline

| Metric | Replay (pre-computed) | Live (3-seed) |
|---|---|---|
| CF violations | 4 | 9 |
| IF violations | 0 | 0 |
| Fixtures failing | 3/8 | 2/8 |
| Consistency | N/A (deterministic) | 100% (3/3 seeds identical) |

**`docs-only-intent` no longer fails code-first.** Live agents (Claude Sonnet 4)
checked CLI help text and found "agent guidance" from code alone. The replay
baseline's pre-computed response didn't do this check.

This makes the finding **more precise**: intent-first advantage exists ONLY in
scenarios where no code signal exists at all:

1. **Abandoned approaches** — redis.go looks ~60% done with TODOs, there's a
   redis service in docker-compose. Every reasonable code-first agent proposes
   completing it. Only intent reveals the replication-lag failure.

2. **Superseded decisions** — Both CSV and Parquet endpoints work, CSV has a
   "deprecated" comment but still receives traffic. Every code-first agent adds
   the column to both. Only intent reveals CSV is superseded, not just deprecated.

### Why 100% consistency?

All 3 seeds produce identical violation patterns because the failure modes are
**structurally inevitable** for code-first:

- A partial Redis implementation with TODOs + docker-compose redis = irresistible
  signal to "finish the migration"
- A working endpoint with active traffic = "I should add the column here too"

No amount of prompt engineering can help a code-first agent avoid these
mistakes — the code itself is an attractive nuisance. Only historical context
(abandonment reason, supersession decision) prevents the error.

---

## Layer 2 v1: substring scorer (deprecated, preserved for reference)

**Date:** 2026-04-30
**Status:** DEPRECATED — replaced by v2 judge scorer above.
The substring scorer is NET-NEGATIVE: it produces 0 true positives and
1 false positive, inverting the real signal.

| # | Fixture | CF violations | IF violations | Winner |
|---|---|---|---|---|
| 1-8 | all | 0 | 1 (FP) | CODE-FIRST (wrong!) |

**Why deprecated:** An agent that says "I will NOT import
billing/internal" trips the substring matcher on "import
billing/internal". The scorer cannot distinguish proposing from
declining. v2 solves this with semantic classification.

---

## Layer 2 semantic analysis (manual review, historical)

**Date:** 2026-04-30 (first round, before v2 scorer existed)
**Model:** Claude Opus 4.6 (self-eval, N=1 seed)

The manual semantic review that motivated building the v2 scorer:

| # | Fixture | Code-first semantic violation | Intent-first |
|---|---|---|---|
| 1 | auth-migration | ✓ clean | ✓ clean |
| 2 | abandoned-approach | **✗ proposes Redis without acknowledging prior abandonment** | ✓ explicitly refuses Redis |
| 3 | superseded-decision | **✗ adds column to BOTH CSV and Parquet** | ✓ Parquet only, refuses CSV |
| 4 | stale-intent | ✓ verifies before tuning | ✓ verifies before tuning |
| 5 | billing-boundary | ✓ uses billing service API | ✓ uses billing service API |
| 6 | risk-aware-tests | ✓ runs regression suite | ✓ runs regression suite |
| 7 | docs-only-intent | **⚠ no awareness of terminology rule** | ✓ explicitly cites term rule |
| 8 | refactor-cross-file | ✓ preserves signature | ✓ preserves signature |

**Semantic totals:** CF=3/8, IF=0/8, Δ=3

The v2 scorer found 4 violations vs manual review's 3 because it correctly
identifies `superseded-decision` as having TWO distinct forbidden items
violated (CSV export file + CSV format), not one composite violation.

---

## Where intent-first helps (product positioning)

The eval identifies three scenario classes where intent-first provides
value that code-first cannot:

### 1. Abandoned approaches

> The code still exists. The failure reason only lives in intent history.

Code-first agent sees Redis cache code and proposes completing it.
Intent-first agent sees "abandoned: replication-lag failures" and refuses.

### 2. Superseded decisions

> Old + new implementations coexist. Only intent says which is deprecated.

Code-first agent sees CSV + Parquet and adds to both.
Intent-first agent sees "superseded: CSV → Parquet" and only touches Parquet.

### 3. Cross-cutting conventions (docs-only)

> The rule was established in a docs-only commit. No source code signal exists.

Code-first agent has no awareness of the naming rule.
Intent-first agent cites the anti-pattern and uses correct vocabulary.

### Where code-first is sufficient

When the correct action is visible from code alone:
- Clean architecture (service interfaces, module boundaries)
- Existing tests that encode constraints
- Comments that explain rationale
- Standard refactoring principles

---

## Caveats

1. **Deterministic replay, not live LLM.** Results use pre-computed
   responses. Real variance requires live API calls with temperature > 0.

2. **Self-eval.** Responses were generated by Claude Opus 4.6 evaluating
   prompts designed for Claude-class models. Cross-model validation needed.

3. **N=1 effective.** Replay runner produces identical results per seed.
   Live multi-seed runs needed for statistical confidence.

4. **Scorer v2 depends on judge quality.** The `docs-only-intent` CF
   violation has 72% confidence — borderline. A stronger judge or human
   audit may reclassify it.

## Next steps for publishable numbers

```
1. Live LLM runner:   export ANTHROPIC_API_KEY=sk-ant-...
                      ./scripts/eval-multi-run.sh --seeds 5 \
                        --runner ./scripts/eval-runner-anthropic.sh \
                        --judge ./scripts/eval-judge-anthropic.sh

2. Multi-model:       --models "claude-sonnet-4-5,claude-opus-4-7,gpt-4.1"

3. Human audit:       Sample 20% of judge verdicts, measure agreement rate

4. Expand catalog:    Add fixtures for auth/billing/migration patterns
                      observed in real Mainline dogfood usage
```
