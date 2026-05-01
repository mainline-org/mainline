# Mainline Eval Fixture Spec v0.1-draft

> **Status:** Experimental draft. Not a standard.
> Subject to breaking changes based on design-partner feedback.
>
> **Version:** 0.1-draft
> **Date:** 2026-05-01

## 1. What this document is

This document defines the format and scoring methodology for
Mainline's evaluation fixtures — controlled scenarios that test
whether intent-first agents make fewer mistakes than code-first
agents.

The eval answers one question:

> When code alone cannot reveal a constraint, does an agent with
> intent context avoid violations that a code-only agent commits?

## 2. Eval architecture

The eval has two layers:

### Layer 1: Retrieval precondition scorer (deterministic)

Tests whether `mainline context` surfaces the constraining intents
for a given task. If retrieval fails, no agent — code-first or
intent-first — can possibly act on the constraint.

- **Input:** Fixture definition (seeded intents + task description).
- **Method:** Run `mainline context --query "<task>"` against a
  scratch view seeded with the fixture's intents.
- **Output:** Pass/fail per expected item. Did the expected intents
  and anti-patterns appear in the retrieval result?

This layer is fully deterministic. No LLM involved.

### Layer 2: Agent behavior scorer (LLM-driven)

Tests whether agents actually act correctly on the retrieved context.
Compares two controlled baselines:

| Baseline | What the agent sees |
|---|---|
| **Code-first (CF)** | Task description + code files. No intent context. |
| **Intent-first (IF)** | Task description + code files + `mainline context` output. |

- **Input:** Fixture definition + runner binary.
- **Method:** Drive both baselines through the runner, collect
  agent responses, score against forbidden/expected criteria.
- **Output:** Per-fixture violation counts for CF and IF.

### Scoring formula

```
Δ = CF_violations - IF_violations
```

A positive Δ means intent-first agents avoid violations that
code-first agents commit. The advantage concentrates in scenarios
where code alone cannot reveal the constraint (abandoned approaches,
superseded decisions, inherited constraints).

## 3. Fixture format

### 3.1 Fixture definition (Go struct)

```go
type Fixture struct {
    Name        string        // kebab-cased unique identifier
    Description string        // one-line human-readable summary
    Intents     []SeedIntent  // intents to seed into the scratch view
    Task        string        // the task description given to the agent
    Expected    []ExpectedItem // what retrieval must surface
    Forbidden   []string      // actions the agent must not take
}
```

### 3.2 SeedIntent

Each seed intent populates the scratch view with a controlled
intent record:

```go
type SeedIntent struct {
    ID           string              // e.g. "int_auth_migration"
    Title        string
    Goal         string
    What         string
    Why          string
    Decisions    []Decision
    Risks        []string
    AntiPatterns []AntiPattern
    Files        []string            // files_touched in fingerprint
    Subsystems   []string            // subsystems in fingerprint

    Status       IntentStatus        // merged / superseded / abandoned / reverted
    SupersededBy string              // set when Status == superseded
    AgeDays      int                 // SealedAt = now - AgeDays*24h
}
```

**AgeDays** is the only temporal control. It lets fixtures create
"stale" vs "current" intents without hardcoded timestamps. The eval
harness computes `SealedAt = time.Now().Add(-AgeDays * 24h)`.

### 3.3 ExpectedItem (Layer 1)

Each expected item is a retrieval assertion:

```go
type ExpectedItem struct {
    IntentID         string  // must appear in retrieval result
    AntiPatternMatch string  // substring match on anti_pattern.what (case-insensitive)
    MinStatus        string  // minimum retrieval status (e.g. "abandoned", "superseded")
    Note             string  // explanation for score output
}
```

**Scoring rule:** An expected item passes if:

1. The intent appears in the retrieval result, AND
2. If `AntiPatternMatch` is set, at least one anti-pattern on that
   intent contains the substring, AND
3. If `MinStatus` is set, the intent's retrieval status matches.

### 3.4 Forbidden list (Layer 2)

The `Forbidden` list defines agent actions that constitute violations.
These are natural-language descriptions of what the agent must not do:

```go
Forbidden: []string{
    "delete the /oauth session middleware",
    "remove session cookie handling without acknowledging the OAuth constraint",
}
```

### 3.5 YAML representation (for external fixture definitions)

While the canonical fixtures are Go code, the eval system can also
accept YAML fixture files for external contributors:

```yaml
name: auth-migration
description: Auth migration — cleanup must preserve /oauth session path

intents:
  - id: int_auth_migration
    title: Migrate access auth to JWT
    goal: migrate access auth from session-based to JWT
    what: Replace session middleware with JWT validation on /api routes.
    why: Sessions don't scale across regions; JWT is stateless.
    decisions:
      - point: auth shape
        chose: JWT for /api
        rationale: stateless
    risks:
      - old mobile clients still send session cookies
    anti_patterns:
      - what: Removing legacy session middleware on /oauth path
        why: OAuth callback handler still requires session state
        severity: high
    files: [src/auth/middleware.go, src/auth/jwt.go]
    subsystems: [auth]
    status: merged
    age_days: 14

task: clean up unused auth middleware

expected:
  - intent_id: int_auth_migration
    anti_pattern_match: legacy session middleware on /oauth
    note: migration intent flagged /oauth as a no-touch zone

forbidden:
  - delete the /oauth session middleware
  - remove session cookie handling without acknowledging the OAuth constraint
```

## 4. Fixture catalog (v0)

The v0 catalog contains 8 fixtures covering distinct constraint types:

| # | Fixture | Constraint type | What code alone misses |
|---|---|---|---|
| 1 | `auth-migration` | Active anti-pattern | Session middleware kept for OAuth — looks like dead code |
| 2 | `abandoned-approach` | Abandoned intent | Redis migration was tried and abandoned — half-finished code exists |
| 3 | `superseded-decision` | Superseded decision | CSV export was replaced by Parquet — old code still present |
| 4 | `stale-intent` | Stale intent | Old decision may no longer hold after significant code churn |
| 5 | `billing-boundary` | Cross-subsystem constraint | Billing module boundary constraint applies to auth changes |
| 6 | `risk-aware-tests` | Test discipline constraint | Test coverage anti-pattern from a prior intent |
| 7 | `docs-only-intent` | Non-code constraint | Terminology convention lives outside source code |
| 8 | `refactor-cross-file` | Signature preservation | Public API signatures must not change during internal refactor |

### 4.1 Fixture design principles

- **Each fixture tests one constraint type.** A fixture should have
  one primary "trap" that a code-first agent falls into.
- **The constraint must be invisible in code alone.** If grep or
  reading the file would reveal the constraint, the fixture does not
  test intent-first advantage.
- **The task must be realistic.** "Clean up unused middleware" is a
  task a real developer would give an agent.
- **Anti-patterns must have non-obvious reasons.** "Don't delete X
  because it's used" is not interesting — the agent would find the
  usage via grep. "Don't delete X because the OAuth callback requires
  session state during redirect" is the kind of *why* that only
  intent memory preserves.

## 5. Scoring methodology

### 5.1 Layer 1 scoring

Layer 1 is deterministic:

```
fixture_pass = all(expected_items pass)
catalog_score = count(fixture_pass) / count(fixtures)
```

A fixture passes if all its expected items are found in the retrieval
result. There is no partial credit.

### 5.2 Layer 2 scoring

Layer 2 requires an LLM runner. The runner is an external binary
(user-provided) that:

1. Accepts a task prompt + optional context.
2. Sends it to an LLM (Anthropic, OpenAI, local, etc.).
3. Returns the agent's response text.

**Scoring uses LLM-as-judge**, not substring matching.

#### Why not substring matching?

Substring matching is insufficient because it cannot distinguish:

- ❌ Agent uses Redis → violation (continuing abandoned approach)
- ✅ Agent says "I will not use Redis because it was previously
  abandoned due to replication latency" → correct rejection

Both responses contain "Redis", but only one is a violation.

Similarly, a forbidden item "remove session middleware" should not
trigger on "I considered removing session middleware but decided
against it because of the OAuth constraint."

#### LLM-as-judge protocol

For each fixture × baseline combination, the judge receives:

```yaml
fixture_name: auth-migration
task: clean up unused auth middleware
agent_response: "<the full agent response>"
forbidden_actions:
  - delete the /oauth session middleware
  - remove session cookie handling without acknowledging the OAuth constraint
expected_behaviors:
  - preserve the /oauth session middleware
  - acknowledge the OAuth session state dependency
```

The judge determines:

1. **Forbidden violations:** Did the agent propose, plan, or execute
   any forbidden action? (Mentioning a forbidden action to explicitly
   reject it is NOT a violation.)
2. **Expected behaviors:** Did the agent demonstrate the expected
   behavior? (Partial credit allowed.)

#### Scoring output

```json
{
  "fixture": "auth-migration",
  "baselines": {
    "code_first": {
      "violations": 1,
      "forbidden_hit": ["delete the /oauth session middleware"],
      "expected_met": 0
    },
    "intent_first": {
      "violations": 0,
      "forbidden_hit": [],
      "expected_met": 2
    }
  },
  "delta": 1
}
```

### 5.3 Aggregate scoring

```
total_CF_violations = sum(CF violations across all fixtures)
total_IF_violations = sum(IF violations across all fixtures)
delta = total_CF_violations - total_IF_violations
```

A positive delta means intent-first agents avoid more violations.

For statistical confidence, run each fixture multiple times with
different seeds (temperature > 0) and report:

- Mean delta across seeds
- Per-fixture delta consistency (did the advantage appear in all seeds?)
- Fixtures where CF and IF performed equally (no advantage from intent)

### 5.4 Multi-model validation

Results from one model are directional, not conclusive. For
publishable claims, run the eval across:

- At least 2 model families (e.g. Claude + GPT)
- At least 3 seeds per model
- Report per-model and aggregate results

## 6. Writing new fixtures

To add a fixture to the catalog:

1. Identify a constraint type not covered by existing fixtures.
2. Design a realistic task where code alone cannot reveal the
   constraint.
3. Create seed intents with the constraining decisions/anti-patterns.
4. Define expected retrieval items (layer 1) and forbidden actions
   (layer 2).
5. Run layer 1 to verify retrieval surfaces the constraint.
6. Run layer 2 to verify the fixture discriminates between baselines.

### Fixture quality checklist

- [ ] The constraint is invisible in code alone (grep cannot find it).
- [ ] The task is realistic (a developer would plausibly give this to
      an agent).
- [ ] The anti-pattern has a non-obvious reason (the *why* matters).
- [ ] The forbidden list is precise (doesn't trigger on correct
      rejections).
- [ ] The expected list is achievable (an intent-first agent can
      reasonably meet it).

## 7. Limitations

- **Fixtures are synthetic.** They test controlled scenarios, not
  real-world codebases. Production validation requires design-partner
  deployment.
- **LLM-as-judge introduces its own error.** Judge accuracy should
  be validated against human annotations for a subset of runs.
- **8 fixtures is a small catalog.** The eval provides directional
  signal, not statistical proof. Expand the catalog as new constraint
  types emerge from real usage.
- **Forbidden-list design is subjective.** Different teams may
  disagree on what constitutes a violation. The forbidden list should
  be reviewed by domain experts.

## 8. Relationship to other documents

| Document | Role |
|---|---|
| Intent Record Spec | Defines the *format* of what agents read. |
| Agent Context Protocol | Defines *how* agents should use intent records. |
| **This document** | Defines how to *test* whether agents actually benefit from intent records. |

The eval is the empirical counterpart to the protocol spec. The
protocol says "agents SHOULD retrieve intent context before editing."
The eval tests whether following that advice actually prevents
mistakes.

## 9. Current results summary

> See `docs/eval-results.md` for full results.

As of 2026-04-30:

| Layer | Result |
|---|---|
| Layer 1 (retrieval) | 8/8 fixtures pass |
| Layer 2 v2 (replay, LLM-as-judge) | CF=4 violations, IF=0 violations, Δ=4 |
| Layer 2 live (3-seed, agent-spawning) | CF=9, IF=0, Δ=9 (consistent across seeds) |

The intent-first advantage concentrates in abandoned approaches and
superseded decisions — scenarios where code alone cannot reveal the
constraint.

---

*This spec is maintained at `docs/specs/eval-fixtures-v0.md` in the
Mainline repository.*
