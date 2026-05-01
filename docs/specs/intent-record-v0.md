# Mainline Intent Record Spec v0.1-draft

> **Status:** Experimental draft. Not a standard.
> Subject to breaking changes based on design-partner feedback.
>
> **Version:** 0.1-draft
> **Date:** 2026-05-01

## 1. What this document is

This document defines a git-native record format for engineering intent
in AI-assisted software development.

Git records **what** changed.
AI provenance tools record **how** code was produced.
Mainline intent records define **why** engineering work exists and what
future agents and reviewers must remember.

An intent record is the structured answer to:

> Why does the code look this way, what decisions were made, what was
> rejected, what risks were accepted, what constraints must future
> changes respect, and what happened to this intent over time?

## 2. Goals

- Preserve engineering intent as durable working memory.
- Give agents the historical *why* before broad code search.
- Help reviewers verify implementation against intent.
- Preserve abandoned and superseded decisions so future agents do not
  repeat them.
- Support git-native storage and local-first workflows.
- Work for solo developers and teams alike.

## 3. Non-goals

- AI line-level attribution (which lines were written by AI).
- Session transcript or conversation capture.
- Token usage or cost tracking.
- Developer productivity measurement.
- Project management, sprint tracking, or issue triage.
- Enforcing coding style or formatting rules.

## 4. Terminology

| Term | Definition |
|---|---|
| **Intent** | A unit of engineering work with a declared goal, summary, decisions, risks, constraints, and lifecycle. |
| **Sealed intent** | An intent whose summary and fingerprint are frozen. Immutable after seal. |
| **Decision** | A recorded choice: what was chosen, why, and what was rejected. |
| **Soft risk** | A warning about something that could go wrong. Advisory; may become irrelevant over time. |
| **Anti-pattern** | A hard constraint: something a future agent or developer MUST NOT do in this area. Carries a mandatory `why`. |
| **Inherited constraint** | An anti-pattern from a prior sealed intent that applies to the current change because of file or subsystem overlap. |
| **Reference** | A link to external material (session, issue, PR, doc, CI run). Metadata only — Mainline never reads the referenced content. |
| **Commit pin** | The association between a sealed intent and one or more commits on the main branch. |
| **Turn** | A lightweight record of one meaningful work fragment within a draft intent. Thinking scaffold for seal preparation. |
| **Fingerprint** | A structured summary of what the change touched: files, subsystems, tags, API changes, data model changes. Used for conflict detection and retrieval. |
| **Actor** | A human or AI agent identity that authors intents. |

## 5. Intent lifecycle

An intent moves through these states:

```
drafting → sealed_local → proposed → merged
                                   ↘ abandoned
                                   ↘ superseded
                                   ↘ reverted
```

| Status | Meaning |
|---|---|
| `drafting` | Local work in progress. Not yet sealed. |
| `sealed_local` | Summary and fingerprint frozen; not yet published to the team. |
| `proposed` | Published (pushed to shared refs). Visible to teammates and conflict detection. |
| `merged` | Code reached the main branch and the intent is pinned to the merge commit. |
| `abandoned` | The approach was explicitly abandoned. Preserved as a warning: future agents should not repeat it without understanding why. |
| `superseded` | Replaced by a newer intent. The newer intent's `supersedes` field references this one. |
| `reverted` | The merged code was reverted on main. |

**Transitions are one-way.** A merged intent cannot go back to proposed.
An abandoned intent is not re-opened; new work starts a new intent.

**Immutability rule:** once an intent is sealed, its `summary` and
`fingerprint` fields never change. Lifecycle transitions (abandon,
supersede, revert) are separate events that change `status` without
modifying the sealed payload.

## 6. Record schema

### 6.1 Intent record (sealed)

A sealed intent record contains these fields:

| Field | Type | Required | Description |
|---|---|---|---|
| `intent_id` | string | ✓ | Unique identifier. Format: `int_<hex>`. |
| `schema_version` | integer | ✓ | Schema version of this record. |
| `status` | string | ✓ | Lifecycle status (see §5). |
| `actor_id` | string | ✓ | Identity of the sealing actor. |
| `actor_name` | string | | Human-readable actor name. |
| `thread` | string | ✓ | Thread (branch group) this intent belongs to. |
| `git_branch` | string | ✓ | Git branch the work was done on. |
| `goal` | string | ✓ | Short description of the user's goal. Verbatim from the user when possible. |
| `base_commit` | string | ✓ | The commit the branch diverged from. |
| `code_commit` | string | ✓ | HEAD commit at seal time. |
| `code_tree` | string | ✓ | Git tree hash of `code_commit`. Used for commit-pin matching after squash merges. |
| `sealed_at` | string | ✓ | ISO 8601 timestamp of sealing. |
| `summary` | IntentSummary | ✓ | Structured summary (see §6.2). |
| `fingerprint` | SemanticFingerprint | ✓ | Structured change fingerprint (see §6.3). |
| `references` | Reference[] | | Links to external materials. |
| `backfill_commits` | string[] | | Explicit commit list for retroactive coverage. |

### 6.2 IntentSummary

The summary is the core human-and-agent-readable payload of a sealed
intent.

| Field | Type | Required | Description |
|---|---|---|---|
| `title` | string | ✓ | One-line title. |
| `what` | string | ✓ | What the change does. Must not be boilerplate ("implemented changes", "see diff"). |
| `why` | string | ✓ | Why the change was made. |
| `user_goal` | string | | The original user request, if different from `what`. |
| `decisions` | Decision[] | ✓ | At least one decision. Each records a choice point, what was chosen, and optionally the rationale and rejected alternatives. |
| `rejected` | RejectedAlternative[] | | Top-level rejected alternatives (beyond per-decision rejects). |
| `risks` | string[] | | Soft warnings. Free-form text. See §7. |
| `anti_patterns` | AntiPattern[] | | Hard constraints. See §7. |
| `followups` | string[] | | Suggested future work items. |
| `review_notes` | string[] | | Ephemeral notes for the PR reviewer. Not inherited, not surfaced in context retrieval. |

### 6.3 Decision

| Field | Type | Required | Description |
|---|---|---|---|
| `point` | string | ✓ | The decision point or question. |
| `chose` | string | ✓ | What was chosen. |
| `rationale` | string | | Why this choice was made. Recommended for non-trivial choices. |
| `rejected` | string[] | | Alternatives that were considered and rejected. |

### 6.4 AntiPattern (hard constraint)

| Field | Type | Required | Description |
|---|---|---|---|
| `what` | string | ✓ | The action to avoid. Must not be empty. |
| `why` | string | ✓ | Why this constraint exists. Must not be empty. Without a reason, future agents will ignore it. |
| `severity` | string | | `"high"` / `"medium"` / `"low"`. Defaults to medium if omitted. |

### 6.5 SemanticFingerprint

The fingerprint enables conflict detection and retrieval scoring
without reading the full diff.

| Field | Type | Required | Description |
|---|---|---|---|
| `subsystems` | string[] | ✓ | Logical areas touched (e.g. `"auth"`, `"billing"`, `"cli"`). |
| `files_touched` | string[] | ✓ | Repo-relative file paths changed. |
| `tags` | string[] | | Synonyms, parent concepts, related technologies. Populate generously for retrieval. |
| `architectural_claims` | string[] | | Structural assertions the change makes. |
| `behavioral_changes` | string[] | | Observable behavior differences. |
| `api_changes` | APIChange[] | | Structured API surface changes. |
| `data_model_changes` | DataModelChange[] | | Structured data model changes. |
| `security_implications` | string[] | | Security-relevant notes. |
| `migration_notes` | string[] | | Migration or rollback guidance. |

### 6.6 APIChange

| Field | Type | Required | Description |
|---|---|---|---|
| `kind` | string | ✓ | `"added"` / `"modified"` / `"removed"`. |
| `surface` | string | ✓ | `"http"` / `"function"` / `"class"` / `"cli"` / `"event"` / `"config"`. |
| `signature` | string | ✓ | The API signature or endpoint. |
| `compatibility` | string | ✓ | `"breaking"` / `"compatible"` / `"unknown"`. |

### 6.7 DataModelChange

| Field | Type | Required | Description |
|---|---|---|---|
| `kind` | string | ✓ | `"added"` / `"modified"` / `"removed"`. |
| `name` | string | ✓ | Type, field, or table name. |
| `location` | string | | File or module path. |
| `compatibility` | string | ✓ | `"breaking"` / `"compatible"` / `"unknown"`. |
| `migration_required` | boolean | ✓ | Whether a data migration is needed. |
| `migration_notes` | string | | Migration guidance. |

### 6.8 Reference

References are links to supporting material. Mainline stores only the
metadata — it never reads, parses, or indexes the referenced content.

| Field | Type | Required | Description |
|---|---|---|---|
| `kind` | string | ✓ | `"session"` / `"issue"` / `"pr"` / `"doc"` / `"ci"` / `"other"`. |
| `label` | string | | Human-readable description. |
| `client` | string | | Agent client identifier. |
| `ref` | string | | Session/checkpoint/provider ID. |
| `url` | string | | URL to the referenced material. |

At least one of `ref` or `url` must be non-empty.

### 6.9 Risk lifecycle (v0.4)

Risks are soft warnings stored as `string[]` on IntentSummary.
Starting with v0.4, risks have a lifecycle:

**Risk IDs** are deterministic: `{intent_id}#{array_index}`. Safe
because sealed intents are immutable — the risk array never changes
after seal.

**Risk status:**

| Status | Meaning |
|---|---|
| `open` | Active risk. Surfaced in context retrieval. |
| `resolved` | Explicitly resolved — by a later seal or manual action. |
| `expired` | Source intent was superseded, abandoned, or reverted. Risk is moot. |

**Resolution paths:**

- **Seal-time resolution:** A new sealed intent can declare
  `resolves_risks` entries, each naming a risk ID. Processed
  atomically with the seal event.
- **Manual resolution:** Via `mainline risks resolve <id>`. Writes a
  separate event to the actor log.
- **Automatic expiry:** When the source intent's status becomes
  `superseded`, `abandoned`, or `reverted`, all its risks are
  automatically expired. Expiry overrides resolution.

Risk view-models (with ID, status, resolution metadata) are
materialized at query time, not stored directly.

## 7. Constraints and risk taxonomy

Mainline distinguishes five types of constraint information:

| Type | Nature | Lifetime | Inherited? | Truncated in retrieval? |
|---|---|---|---|---|
| **Soft risk** | Advisory warning | Open → resolved / expired | No | Yes (top-N per intent) |
| **Anti-pattern** | Hard constraint | Permanent (with source intent) | Yes | **Never** |
| **Inherited constraint** | Propagated anti-pattern | Until source intent is abandoned/reverted | N/A (is propagation) | **Never** |
| **Conflict** | Detected overlap | Per-pair | N/A | N/A |
| **Coverage gap** | Missing intent | Until covered or skipped | N/A | N/A |

**The distinction matters:**

- A **risk** says "this might break old clients" — it is a warning to
  consider, and it may become irrelevant when code evolves.
- An **anti-pattern** says "do not delete the legacy session middleware
  on /oauth — the OAuth callback handler still requires session state"
  — it is a hard constraint that future agents must respect.
- An **inherited constraint** is an anti-pattern from a *prior* intent
  that applies to the *current* change because of file or subsystem
  overlap.

Anti-patterns are **never truncated** in retrieval. The safety
property is: if a constraint exists, the agent will see it before
editing.

## 8. References

References are metadata-only links. Mainline never fetches, parses,
or indexes the content at a reference URL. References exist to:

- Help reviewers find supporting material.
- Let agents record which session produced the work.
- Preserve provenance without coupling to external systems.

A reference is **not** a source of truth. The sealed intent's
summary is the source of truth for decisions and constraints.

## 9. Git storage model

Intent records are stored in git refs, not in the working tree.

| What | Where |
|---|---|
| Draft intents | `.ml-cache/drafts/` (local, gitignored) |
| Actor event logs | `refs/mainline/actors/<actor_id>/log` |
| Materialized view | `.mainline/view.json` (derived, gitignored) |
| Commit pins | `refs/notes/mainline/intents` (git notes on main commits) |

**Actor logs** are append-only JSON-lines blobs. Each event has a
type, timestamp, actor ID, and event-specific payload. The
materialized view is rebuilt by replaying all events.

**Commit pins** associate main-branch commits with sealed intents.
Pinning uses a strategy cascade optimized for GitHub's squash-merge
workflow:

1. **tree_hash** — commit's tree hash matches intent's `code_tree`.
2. **commit_hash** — commit hash matches intent's `code_commit`.
3. **goal_text** — commit message contains intent's `goal` verbatim.

This cascade achieves near-100% automatic pin success for
squash-merged PRs.

## 10. Commit coverage model

Every commit on `main` is in exactly one of three states:

| State | Meaning |
|---|---|
| **Covered** | A sealed intent is pinned to this commit. |
| **Skipped** | Deliberately excluded via `Mainline-Skip:` trailer or config pattern (e.g. formatting, version bumps). |
| **Uncovered** | Neither covered nor skipped. A gap in intent memory. |

## 11. Compatibility and evolution

This spec is **v0.1-draft**. Expect additive changes:

- New optional fields may be added to any record type.
- Existing required fields will not be removed or renamed without a
  major version bump.
- Readers SHOULD ignore unknown fields.
- Writers SHOULD NOT omit required fields.

The spec will be versioned using `schema_version` on records.
Readers that encounter a `schema_version` they do not understand
SHOULD warn and attempt best-effort parsing.

## 12. Minimal example

```json
{
  "intent_id": "int_abc123",
  "schema_version": 1,
  "status": "merged",
  "actor_id": "actor_def456",
  "thread": "feat-jwt-auth",
  "git_branch": "feat-jwt-auth",
  "goal": "migrate access auth from session-based to JWT",
  "base_commit": "aaa1111",
  "code_commit": "bbb2222",
  "code_tree": "ccc3333",
  "sealed_at": "2026-04-15T10:30:00Z",
  "summary": {
    "title": "Migrate API auth to JWT, keep OAuth session path",
    "what": "Replace session middleware with JWT validation on /api routes. Keep legacy session middleware on /oauth callback path.",
    "why": "Sessions don't scale across regions; JWT is stateless and works behind any load balancer.",
    "decisions": [
      {
        "point": "auth mechanism for API routes",
        "chose": "JWT with RS256",
        "rationale": "stateless, works across regions",
        "rejected": ["session cookies", "API keys"]
      },
      {
        "point": "OAuth callback auth",
        "chose": "keep session middleware",
        "rationale": "OAuth provider redirect carries state in session cookie"
      }
    ],
    "risks": [
      "old mobile clients still send session cookies to /api — need graceful degradation"
    ],
    "anti_patterns": [
      {
        "what": "Removing legacy session middleware on /oauth path",
        "why": "OAuth callback handler still requires session state during redirect",
        "severity": "high"
      }
    ],
    "followups": [
      "add JWT key rotation cron job"
    ]
  },
  "fingerprint": {
    "subsystems": ["auth"],
    "files_touched": [
      "src/auth/middleware.go",
      "src/auth/jwt.go",
      "src/auth/oauth.go"
    ],
    "tags": ["auth", "jwt", "oauth", "session", "security"],
    "behavioral_changes": [
      "API routes now require Bearer token instead of session cookie"
    ]
  },
  "references": [
    {
      "kind": "issue",
      "label": "Migrate to JWT for multi-region support",
      "url": "https://github.com/org/repo/issues/42"
    }
  ]
}
```

---

*This spec is maintained at `docs/specs/intent-record-v0.md` in the
Mainline repository.*
