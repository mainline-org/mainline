# Mainline Agent Context Protocol v0.1-draft

> **Status:** Experimental draft. Not a standard.
> Subject to breaking changes based on design-partner feedback.
>
> **Version:** 0.1-draft
> **Date:** 2026-05-01

## 1. What this document is

This document defines how AI coding agents should consume Mainline
intent records — what to retrieve, when to retrieve it, and how to
act on the retrieved context.

This is a **workflow specification**, not a wire protocol. It defines
agent behavior requirements using RFC 2119 keywords (MUST, SHOULD,
MAY) applied to the agent's reasoning and tool-calling behavior.

## 2. Core principle

> Before broad code search, agents SHOULD retrieve relevant intent
> context. Intent context provides the historical *why* that code
> alone cannot reveal.

The default agent workflow is:

```
1. mainline status              → overall state, identity, sync freshness
2. mainline context --current   → intents relevant to the current branch + diff
3. Read decisions, lifecycle warnings, and explicit inherited constraints
4. Verify intent context against current code
5. THEN grep / read code
6. THEN edit
```

This order — intent before code — is the load-bearing workflow
property. It prevents agents from silently undoing yesterday's
decision, repeating an abandoned approach, or violating a hard
constraint they never saw.

## 3. Retrieval modes

Mainline context retrieval supports three modes:

| Mode | Input | Use when |
|---|---|---|
| `--current` | Current branch + diff vs main | Starting any non-trivial change. Default mode. |
| `--files <paths>` | Specific file paths | The task names specific files to modify. |
| `--query "<text>"` | Free-form text | The task names a feature area or concept. |

Agents SHOULD use `--current` as the default. If the task explicitly
names files, agents SHOULD additionally use `--files`. If the task
is conceptual, agents MAY use `--query`.

## 4. Retrieval output shape

The retrieval result contains:

```json
{
  "query": {
    "mode": "current",
    "files": ["src/auth/middleware.go"],
    "text": ""
  },
  "relevant_intents": [
    {
      "intent_id": "int_abc123",
      "title": "Migrate API auth to JWT",
      "status": "current",
      "relevance": { "score": 0.85, "reasons": ["file match", "subsystem match"] },
      "summary": "...",
      "decisions": ["JWT for /api (stateless)"],
      "guidance": "current effective decision; verify against current code, then apply",
      "followups": {
        "full_record": "mainline show int_abc123 --json",
        "timeline": "mainline trace int_abc123 --json"
      }
    }
  ],
  "inherited_constraints": [
    {
      "source_intent": "int_abc123",
      "constraint_id": "guard_1a2b3c4d",
      "what": "Removing legacy session middleware on /oauth path",
      "why": "OAuth callback handler still requires session state",
      "severity": "high",
      "matched_by": ["file:src/auth/middleware.go"]
    }
  ],
  "notes": [
    "Verify each intent's decisions against current code before applying.",
    "Only human-promoted constraints are hard rules; lifecycle warnings are not new constraints."
  ]
}
```

## 5. Retrieval status

Each retrieved intent carries a **retrieval status** — distinct from
lifecycle status — that tells the agent how to use the intent *right
now*:

| Retrieval status | Meaning | Agent behavior |
|---|---|---|
| `current` | This intent is the current effective decision. | MUST verify against current code, then apply. |
| `superseded` | This intent was replaced by another. | SHOULD read the superseding intent instead. Use this only for historical context. |
| `abandoned` | This approach was tried and abandoned. | MUST NOT repeat this approach without understanding why it was abandoned and getting explicit user confirmation. |
| `stale` | This intent is old or its files have churned significantly. | SHOULD verify that decisions still hold before relying on them. |

## 6. Agent behavior requirements

### 6.1 Pre-edit retrieval (SHOULD)

For non-trivial engineering changes, agents SHOULD retrieve intent
context before broad code search.

"Non-trivial" includes:

- Architecture changes or refactors
- Migrations or deletions
- Auth, billing, data-model, or permissions changes
- Test strategy changes
- Any cross-file change
- Answering "why is this here?" or "can we delete this?"

"Trivial" (direct code OK, retrieval not required):

- Typo or formatting fixes
- One-line obvious syntax fixes
- Mechanical rename with scoped impact

### 6.2 Verifying intent against code (MUST)

Agents MUST verify retrieved intent context against current code
before editing. Intent records reflect the state at seal time; code
may have changed since then.

Verification means: read the relevant source files and confirm that
the decisions described in the intent still match the implementation.
If they don't, note the discrepancy and proceed with the current
code as ground truth.

### 6.3 Respecting constraints (MUST)

Explicit constraints are hard rules. Agents MUST NOT violate a
constraint without explicit user acknowledgment.

When an agent's task would conflict with a constraint, the agent
SHOULD:

1. Surface the constraint to the user.
2. Explain the conflict.
3. Wait for explicit confirmation before proceeding.

### 6.4 Respecting inherited constraints (SHOULD)

Inherited constraints are explicit constraints, plus legacy
high-severity anti-patterns, that apply because of file overlap.
Agents SHOULD acknowledge high-severity inherited constraints in
their seal result using `acknowledged_constraints` or a decision that
shows how the rule was handled.

The acknowledgment does not need to be verbatim. It needs to
demonstrate that the agent saw the constraint and consciously
decided how to handle it.

### 6.5 Handling abandoned intents (MUST NOT repeat)

When retrieval surfaces an intent with `status: abandoned`, the
agent MUST NOT repeat that approach without:

1. Reading the `abandoned_reason` (if available).
2. Understanding why the approach was abandoned.
3. Getting explicit user confirmation that the situation has changed.

This is the most common violation in code-first agents: they find
half-finished code from an abandoned approach and complete it,
unaware that the approach was explicitly rejected.

### 6.6 Handling superseded intents (SHOULD follow chain)

When retrieval surfaces an intent with `status: superseded`, the
agent SHOULD follow the `superseded_by` reference to find the
current decision. The superseded intent is valuable context
(what was tried before) but is not the current truth.

### 6.7 Explicit signal writes (MUST NOT use seal)

When sealing, agents MUST NOT create durable action signals by filling
`summary.risks`, `summary.followups`, or `summary.anti_patterns`.
Seal records decisions by default.

Agents SHOULD:

- Move acceptable trade-offs to `decisions[].chose` with rationale.
- Move reviewer-only context to `review_notes`.
- Use `mainline risk add` only for a concrete failure mode with
  trigger or impact, plus mitigation / validation / owner.
- Use `mainline followup add` only for explicit user deferral, an
  external issue/ticket/PR reference, or a real cut-scope task.
- Never create constraints; a human must confirm `mainline guard add`.

### 6.8 Writing seals (MUST)

When an agent completes non-trivial work, it MUST seal the intent.
A well-formed seal:

- Has a non-boilerplate `what` (not "implemented changes" or "see diff").
- Has a meaningful `why`.
- Has at least one `decision` with a choice point and what was chosen.
- Has `fingerprint.subsystems` and `fingerprint.files_touched` populated.
- Has `tags` populated generously (synonyms, parent concepts).
- Does not contain `summary.risks`, `summary.followups`, or
  `summary.anti_patterns`.

## 7. Task priority matrix

When should agents retrieve intent context?

| Always intent-first | Intent-first preferred | Direct code OK |
|---|---|---|
| Architecture changes / refactors | Bug fixes | Typo / formatting fixes |
| Migrations / deletions | New feature additions | One-line obvious syntax fixes |
| Auth / billing / data-model / permissions | API behavior changes | Mechanical rename, scoped |
| Test strategy changes | Config / CI / release tweaks | User explicitly asks to view ONE file |
| Any cross-file change | | |
| User asks "why is this here?" | | |
| User asks "can we delete this?" | | |
| User asks "did we try this before?" | | |

## 8. Pre-edit checklist

Before editing code, an intent-aware agent should answer:

- [ ] Did I run `mainline status`?
- [ ] Did I run `mainline context --current --json`?
- [ ] If the task names files, did I run `mainline context --files ... --json`?
- [ ] Did I read the relevant prior decisions and risks?
- [ ] Did I verify those intents against the current code?
- [ ] Am I about to repeat an abandoned or superseded approach?
- [ ] Are there high-severity inherited constraints I must acknowledge?

## 9. Conflict awareness (SHOULD)

After sealing, `mainline seal --submit` runs phase-1 conflict
detection automatically. If the response contains a `conflicts`
array, the agent SHOULD surface those conflicts to the user before
continuing.

Phase-1 conflicts are file/subsystem overlap signals (not semantic
judgments). Phase-2 semantic judgment is invoked deliberately when
phase-1 flags an overlap.

## 10. What agents do NOT need to run

| Command | Why not |
|---|---|
| `mainline sync` | Runs automatically inside `seal --submit`. |
| `mainline pin` | Runs automatically after sync. |
| `mainline merge` | Humans merge via PR UI; sync auto-pins. |

## 11. Compatibility

This protocol spec tracks the Mainline CLI. As the CLI evolves,
this document will be updated. Agents should handle gracefully:

- Unknown fields in retrieval output (ignore them).
- Missing optional fields (use defaults).
- New retrieval statuses (treat as `stale` if unrecognized).

---

*This spec is maintained at `docs/specs/agent-context-protocol-v0.md`
in the Mainline repository.*
