# Mainline Eval Results

**Date:** 2026-04-29 — second baseline after Context Reliability v2 fixes.
**Catalog:** 8/8 populated (auth-migration, abandoned-approach, superseded-decision, stale-intent, billing-boundary, risk-aware-tests, docs-only-intent, refactor-cross-file)
**Run:** `mainline eval run` — precondition scorer only; LLM-runner round pending external runner wire-up.

This document is now the **second baseline** of the eval harness against
the populated catalog. The first baseline (also 2026-04-29 earlier in
the day) recorded two real failures, F1 and F2, which drove the next
round of retrieval work. Both have since landed and the eval is
**8/8 pass**.

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

## Layer 2: LLM-runner round — code-first vs intent-first

**Date:** 2026-04-30
**Model:** Claude Opus 4.6 (self-eval, N=1 seed)
**Runner:** Manual simulation via sub-agent spawning (equivalent to
`mainline eval agent --runner` with the Anthropic runner at
`scripts/eval-runner-anthropic.sh`)
**Catalog:** 8/8 populated fixtures

### Method

For each of the 8 fixtures, two independent agents were spawned:

1. **Code-first agent** — received the task description + code
   context (file structure, visible APIs). Instructed to rely solely
   on code inspection. No access to intent history.

2. **Intent-first agent** — received the same task + the full
   `mainline context` output (anti-patterns, decisions, risks,
   status). Instructed to read intent before code.

Both agents produced prose descriptions of their proposed changes.
Scoring applied the harness's substring matcher (`ScoreAgentRun`)
and a manual semantic review.

### Results: automated substring scorer

| # | Fixture | CF violations | IF violations | Winner |
|---|---|---|---|---|
| 1 | auth-migration | 0 | 0 | TIE |
| 2 | abandoned-approach | 0 | 0 | TIE |
| 3 | superseded-decision | 0 | 0 | TIE |
| 4 | stale-intent | 0 | 0 | TIE |
| 5 | billing-boundary | 0 | 1 (FP) | CODE-FIRST* |
| 6 | risk-aware-tests | 0 | 0 | TIE |
| 7 | docs-only-intent | 0 | 0 | TIE |
| 8 | refactor-cross-file | 0 | 0 | TIE |

**Totals:** code-first=0, intent-first=1

\* **False positive**: the intent-first agent said "NOT import
src/billing/internal from src/auth" — quoting the anti-pattern to
explain what it *won't* do — and the substring matcher fired on
"import src/billing/internal from src/auth". This is exactly the
scorer noise the code comments predict.

### Results: semantic scorer (human-review)

The substring scorer is too coarse. A human-review pass reveals the
real signal:

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

**Semantic totals:**

- Code-first: **3/8 fixtures violated** (2 hard, 1 soft)
- Intent-first: **0/8 fixtures violated**
- Δ = 3 fixtures where intent-first prevented a real violation

### Analysis

**Where intent-first won (3 fixtures):**

1. **abandoned-approach** — The code-first agent saw existing Redis
   code and proposed completing it. It had no way to know the
   approach was abandoned due to replication-lag failures without
   the intent. Intent-first refused and proposed alternatives.

2. **superseded-decision** — The code-first agent saw both CSV and
   Parquet endpoints and assumed parity. Intent-first knew CSV is
   deprecated (superseded by Parquet) and only touched Parquet.

3. **docs-only-intent** — The code-first agent had no signal about
   the terminology rule. Intent-first explicitly cited the
   anti-pattern and used correct vocabulary.

**Where code-first was sufficient (5 fixtures):**

- **auth-migration** — Good code inspection caught the OAuth
  dependency (session cookie visible in the handler).
- **stale-intent** — The code comment "empirical sustainable rate"
  was enough to trigger verification instincts.
- **billing-boundary** — Clean architecture (service interface
  visible) made the right path obvious from code alone.
- **risk-aware-tests** — Test files existed and the agent's testing
  discipline caught them.
- **refactor-cross-file** — Public API preservation is a default
  refactoring principle well-trained models know.

**Key finding:** Intent-first's advantage is concentrated in two
scenario classes:

1. **Abandoned/superseded decisions** — where the code looks like
   something SHOULD be done, but historical context says it
   SHOULDN'T. Code inspection alone cannot reveal why something was
   tried and failed.

2. **Cross-cutting conventions** — where a rule was established in
   a docs-only commit that touches zero source files. Code
   inspection has nothing to read.

### Scorer limitation (load-bearing finding)

The automated substring scorer (`ScoreAgentRun`) produced:

- 0 true positives (missed all 3 real violations)
- 1 false positive (penalized intent-first for quoting an anti-pattern)

**Net effect: the substring scorer would report intent-first as
WORSE, inverting the real signal.**

This confirms the code comment's prediction: "a perfectly-honest
agent who says 'I considered removing the /oauth middleware but
didn't because of the prior intent' would trip the substring match."

**Recommended v2 scorer:** LLM-as-judge that reads the agent's
output and classifies "did the agent PROPOSE the forbidden action,
or merely REFERENCE it while declining?" A two-class classifier
(propose / decline-with-reference) eliminates both false-positive
and false-negative categories observed here.

### Verdict

**The thesis has initial signal.** Intent-first agents avoid
violations that code-first agents commit, specifically in scenarios
where:

- A prior approach was abandoned (code still exists, reason doesn't)
- A decision was superseded (both versions coexist in code)
- A convention was established outside source code

These are precisely the scenarios Mainline's intent memory was
designed for. The 3/8 violation rate on code-first vs 0/8 on
intent-first is not a statistical proof (N=1, self-eval, one
model), but it is **directional signal** that the mechanism works.

### Caveats and next steps

1. **N=1 self-eval.** This is Claude Opus 4.6 evaluating prompts
   designed for Claude-class models. External validation with N≥3
   seeds and a different evaluator model is needed for publishable
   numbers.

2. **Scorer must be upgraded.** The substring scorer is net-negative
   (inverts the signal). Ship the LLM-as-judge scorer before running
   at scale.

3. **Model diversity.** Run with Sonnet, GPT-4, and a smaller model
   to test whether the advantage holds across capability levels.

4. **Runner is shipped.** `scripts/eval-runner-anthropic.sh` is the
   reference runner wrapper. Set `ANTHROPIC_API_KEY` and run:
   ```
   mainline eval agent --runner ./scripts/eval-runner-anthropic.sh
   ```

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

## Appendix: layer-2 reproducibility

To re-run Layer 2 with a real LLM:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
export EVAL_MODEL=claude-sonnet-4-5-20250514   # or claude-opus-4-7-20250430

mainline eval agent --runner ./scripts/eval-runner-anthropic.sh --json
```

For N≥3 seeds, run the above multiple times (the LLM's temperature
introduces variance). Aggregate violation counts across seeds.
