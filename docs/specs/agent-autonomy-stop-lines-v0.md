# Mainline Agent Autonomy Stop Lines Design v0.1-draft

> **Status:** Design draft. Not yet implemented.
> Subject to change after dogfood and review.
>
> **Version:** 0.1-draft
> **Date:** 2026-05-10

## 1. What this document is

This document defines the proposed v1 design for Mainline's
**agent autonomy stop-line model**.

Mainline already shapes how coding agents collaborate with a repository:
agents read history, claim work, append meaningful turns, seal intent,
publish memory, create pull requests, and sometimes participate in
post-merge cleanup. Without an explicit governance model, agents infer
how far they may proceed from prompt wording, local habits, and skill
happy paths.

The goal of this design is to make that implicit behavior explicit:

> For a given repository and local actor, how far may an agent
> autonomously advance the memory and delivery lifecycle before it must
> stop for human judgment?

This is not a permissions matrix and not a release automation system.
It is an advisory, machine-readable stop-line contract for agent-native
engineering work.

## 2. Background

Mainline currently has three related but distinct lifecycles:

### 2.1 Memory lifecycle

Owned by Mainline:

```text
drafting → sealed_local → proposed → merged
```

This lifecycle answers:

> When is an intent still a local draft, when is it reviewable team
> memory, and when has it been linked to code on main?

### 2.2 Delivery lifecycle

Owned by Git and the platform:

```text
edit → test → commit → push branch → PR → merge → release
```

This lifecycle answers:

> How does code enter the repository, get reviewed, and reach main or
> production?

### 2.3 Agent authority model

Currently implicit:

> Under what conditions may an agent advance either lifecycle by itself?

The missing piece is not another field such as `auto_closeout`. The
missing piece is an explicit operating boundary for agent behavior.

## 3. Goals

- Define a small, human-readable autonomy model for coding agents.
- Make the effective autonomy visible to agents through `status` and
  `preflight`.
- Keep v1 advisory-only so existing commands do not become unexpectedly
  unusable.
- Separate Mainline memory lifecycle from Git/PR/merge delivery
  lifecycle.
- Let teams cap local dogfood preferences with a committed ceiling.
- Let local actors opt into higher autonomy without changing team policy.
- Let current user instructions temporarily lower or raise the stop line
  within the team ceiling.
- Preserve hard gates: dirty ownership, active intent mismatch,
  preflight block, verification gaps, wrong remote, main-branch push
  risk, and missing Mainline PR body markers.
- Update the Mainline skill from a linear happy path into a decision
  framework.

## 4. Non-goals

- No allow/deny matrix in v1.
- No CLI permission enforcement in v1.
- No global interception of Git commands.
- No config-set CLI in v1.
- No merge/release mode.
- No automatic PR merge based on autonomy.
- No natural-language parser inside the Mainline CLI.
- No replacement for GitHub, GitLab, release tooling, or branch
  protection.

## 5. Core concept: autonomy as a stop line

The proposed model is:

```toml
[agent]
  autonomy = "handoff"
```

`autonomy` describes the default lifecycle boundary the agent may reach
without asking again.

Valid values:

```text
assist < handoff < review
```

These are stop lines, not personalities.

| Autonomy | Default stop line | Meaning |
|---|---|---|
| `assist` | before commit/seal | Help with analysis, edits, and verification, but do not autonomously create the durable handoff. |
| `handoff` | proposed intent | Commit scoped work, seal intent, and make team memory visible; stop before pushing code or opening PRs. |
| `review` | opened/updated PR | Advance to external review by pushing a non-main branch and opening/updating a PR; stop before merge/release. |

## 6. Autonomy semantics

### 6.1 `assist`

`assist` means the agent is helping but should not autonomously create a
durable handoff.

Allowed by default:

- read repository state;
- run `mainline preflight`;
- inspect relevant Mainline context;
- analyze, propose, edit, and test when the user asked for actual work;
- for non-trivial actual work, `mainline start` / `append` may be used
  to preserve local work memory.

Required stop:

- before Git commit;
- before `mainline seal --submit`;
- before publishing intent;
- before branch push or PR creation.

Important distinction:

- If the user asks for read-only advice, plan review, or "先给建议，别直接改",
  the agent should not automatically `start` or `append`.
- If the user has allowed actual non-trivial work, `start` / `append` are
  acceptable local work-memory operations, but the agent still stops
  before commit/seal.

### 6.2 `handoff`

`handoff` means the agent may create a local durable handoff and make the
intent visible as team memory.

Allowed by default:

- everything in `assist`;
- commit scoped work when branch, dirty state, and intent ownership are
  clear;
- run `mainline seal --prepare`;
- submit the seal;
- reach the memory boundary `proposed` when possible.

Required stop:

- before pushing a code branch;
- before opening or updating a PR;
- before merge/release actions.

Target boundary:

```text
handoff = Git commit + Mainline proposed intent
```

`mainline publish` is not a mandatory happy-path stage. `seal --submit`
already attempts to publish the actor log when a remote is configured and
the command is not run with `--offline`. The standalone `publish` command
should be treated as a sealed-local retry or metadata repair path.

### 6.3 `review`

`review` means the agent may advance work to the external code-review
surface.

Allowed by default:

- everything in `handoff`;
- ensure work happens on a non-main branch;
- generate a PR body from the sealed Mainline intent;
- push the branch;
- open or update a PR.

Required stop:

- before merging the PR;
- before release actions;
- before post-merge cleanup unless explicitly requested.

Target boundary:

```text
review = opened or updated PR
```

`review` does not imply merge. Merge/release/post-merge cleanup are
explicit delivery tasks, not autonomy stop lines.

## 7. Configuration model

### 7.1 Team config

Committed file:

```toml
# .mainline/config.toml
[agent]
  autonomy = "handoff"
  # max_autonomy = "review" # optional; omitted means "review"
```

`autonomy` is the team default.

`max_autonomy` is an optional team ceiling. It limits local preference
and current-instruction upgrades.

### 7.2 Local config

Uncommitted file:

```toml
# .mainline/local.toml
[actor]
  id = "..."
  name = "..."

[agent]
  autonomy = "review"
```

Local autonomy is the actor's preferred default. It must never be
committed to the repository and must never exceed the team ceiling.

### 7.3 Built-in defaults

If no `[agent]` section exists:

```text
team autonomy = handoff
team max_autonomy = review
```

New `mainline init` should write:

```toml
[agent]
  autonomy = "handoff"
```

It should not write `max_autonomy = "review"` by default, because the
omitted value already means that and the explicit word "review" may
make new users think PR creation is the default behavior.

## 8. Effective autonomy calculation

Stop-line order:

```text
assist < handoff < review
```

Pseudo-code:

```text
preferred =
  current_instruction_override
  ?? local.agent.autonomy
  ?? team.agent.autonomy
  ?? "handoff"

ceiling =
  team.agent.max_autonomy
  ?? "review"

effective_autonomy = min(preferred, ceiling)
```

Hard gates do not change `effective_autonomy`. Instead, they lower the
current allowed boundary.

```text
current_allowed_boundary =
  min(stop_line(effective_autonomy), hard_gate_boundary)
```

For example, a repository may have effective autonomy `review`, but a
preflight block lowers the current boundary to `inspect_or_stop`.

## 9. Current user instruction override

The Mainline CLI should not parse natural language. Current instruction
override is interpreted by the agent/skill for the current turn only.

Examples:

| User instruction | Temporary effect |
|---|---|
| "先给建议" / "别直接改" / "不要提交" | lower to `assist` |
| "提交当前工作区" / "收口" / "seal 一下" | raise or lower to `handoff` |
| "可以提了吧" / "直接 PR" / "push and open PR" | raise to `review`, capped by `max_autonomy` |
| "merge 这个 PR" / "发布" / "PR 后收口" | explicit delivery task, not autonomy |

Current instruction can lower autonomy at any time. It can raise autonomy
only up to `max_autonomy`.

The override must not be written to `.mainline/config.toml` or
`.mainline/local.toml`.

## 10. Hard gates

Autonomy is advisory permission to proceed to a stop line. It is not a
license to ignore safety gates.

Agents must stop or ask for human judgment when:

- `preflight` returns `block`;
- dirty or untracked files do not clearly belong to the current task;
- active intent, branch, or dirty state are not aligned;
- the current branch is stale behind main and the changed files overlap
  relevant upstream work;
- the target remote/repository is unclear;
- verification appropriate to the change has not run;
- a PR would be opened from `main` or would push directly to `main`;
- a Mainline-backed PR body cannot be generated;
- the generated PR body is missing
  `<!-- mainline:pr-description:start -->`;
- schema, shared type, migration, CI, auth, security, or infrastructure
  boundaries are being changed without a clear plan or user-confirmed
  scope.

Hard gates take precedence over all autonomy settings.

## 11. Branch and delivery policy

### 11.1 Handoff on `main`

`handoff` may commit on the current branch, including `main`, when the
scope is small and clear, such as docs-only or explicitly requested
local commits.

For non-trivial engineering changes, the agent should prefer a feature
branch or independent worktree before committing.

`handoff` must not push or open a PR.

### 11.2 Review requires a non-main branch

`review` requires a non-main branch for branch push and PR creation.

If the agent is on `main` and the current instruction/effective autonomy
allows review, it should create or switch to a suitable branch/worktree
before committing or pushing.

`git push origin main` is not authorized by `review` autonomy.

## 12. Publish semantics

Mainline has two different "publish" concepts that must stay separate:

1. publishing Mainline actor-log metadata so the intent becomes
   `proposed`;
2. pushing code branches or opening PRs.

`handoff` includes the first concept, not the second.

Happy path:

```text
commit → seal --submit → status proposed
```

Fallback path:

```text
seal --submit → status sealed_local
mainline publish --intent <id> → status proposed
```

The standalone `mainline publish` command is therefore a retry/repair
command for `sealed_local`, not a separate happy-path lifecycle stage and
not authorization to push a code branch.

## 13. `status` and `preflight` output

Both `mainline status --json` and `mainline preflight --json` should
include a stable authority object.

### 13.1 JSON shape

Recommended v1 shape:

```json
{
  "agent_authority": {
    "schema_version": 1,
    "advisory_only": true,
    "team": {
      "autonomy": "handoff",
      "max_autonomy": "review",
      "source": ".mainline/config.toml"
    },
    "local": {
      "autonomy": "review",
      "source": ".mainline/local.toml"
    },
    "effective": {
      "autonomy": "review",
      "stop_line": "opened_pr"
    },
    "current": {
      "allowed_boundary": "opened_pr",
      "blocked_by_preflight": false
    },
    "warnings": []
  }
}
```

`status` primarily reports configuration truth and effective autonomy.

`preflight` reports the same effective autonomy plus the current
allowed boundary after readiness gates.

### 13.2 Stop-line and boundary names

Recommended machine values:

| Autonomy / condition | Stop line or boundary |
|---|---|
| `assist` | `before_commit` |
| `handoff` | `proposed_intent` |
| `review` | `opened_pr` |
| preflight block | `inspect_or_stop` |

### 13.3 Plain text output

Both plain `status` and plain `preflight` should print one short line:

```text
Agent autonomy: handoff — may commit/seal/propose; stop before push/PR.
```

If blocked:

```text
Agent autonomy: review — blocked now; resolve preflight findings before lifecycle advancement.
```

`preflight.recommended_next` should include one or two authority-aware
next steps, but the structured object remains the source of truth.

## 14. Invalid config behavior

Invalid autonomy config should not make Mainline commands fail in v1.

Fallback rules:

| Invalid field | Fallback |
|---|---|
| `agent.autonomy` | `handoff` |
| `agent.max_autonomy` | `review` |

The warning must be visible in `status` and `preflight`:

```json
"warnings": [
  "invalid team agent.autonomy \"turbo\"; using handoff"
]
```

This keeps v1 advisory-only and avoids turning a typo into a repository-
wide command outage.

## 15. Skill and agent guidance changes

The Mainline skill should stop presenting one fixed closeout script.
Instead it should be a decision framework:

```text
1. Establish collaboration context.
2. Determine effective autonomy and current user override.
3. Do the work.
4. Verify.
5. Advance only to the allowed lifecycle boundary.
6. Stop on hard gates.
```

### 15.1 Establish context

The skill should run:

```bash
mainline preflight --json
```

Then it should inspect:

- branch;
- active intent;
- dirty/untracked ownership;
- preflight findings/overlaps;
- agent authority;
- current user instruction.

### 15.2 Advance to stop line

The skill should map autonomy to behavior:

```text
assist:
  stop before commit/seal and report suggested next actions.

handoff:
  commit if scope is clear;
  seal;
  ensure proposed when possible;
  stop before push/PR.

review:
  ensure non-main branch;
  ensure proposed intent;
  generate PR description;
  push branch;
  open/update PR;
  stop before merge.
```

### 15.3 PR body hard gate

For Mainline-backed work, PR creation/update must use:

```bash
mainline pr-description --intent <intent_id> > .ml-cache/pr-description.md
```

The resulting body must contain:

```html
<!-- mainline:pr-description:start -->
```

If generation fails or the marker is missing, the agent must stop rather
than opening a generic PR body.

### 15.4 `continue` and closeout phase

In effective `review` autonomy, vague continuation instructions such as
"继续" or "auto next" may advance to the review stop line only when the
task is already in closeout phase:

- implementation is complete;
- verification has passed;
- no unresolved design questions remain;
- commit/seal/PR steps are the next natural boundary;
- preflight is not blocking.

Outside closeout phase, the agent should continue the next unfinished
implementation/design step, not jump to PR.

## 16. `AGENTS.md` and template guidance

Repo guidance should remain short. It should not duplicate the full
decision tree.

Recommended contract:

```text
This repository uses Mainline. For non-trivial agent work, use the
Mainline skill and respect agent autonomy stop lines from preflight/status.
Autonomy is advisory; hard gates and current user instructions take
priority. Review autonomy stops at PR, not merge.
```

Detailed rules belong in the Mainline skill and reference docs.

## 17. Implementation slice

Recommended v1 vertical slice:

1. Add `AgentSection` to team config and local config domain types.
2. Backfill defaults:
   - team autonomy: `handoff`;
   - max autonomy: `review`;
   - local autonomy: optional.
3. Add normalisation and effective-autonomy calculation.
4. Make new `mainline init` write `[agent] autonomy = "handoff"`.
5. Add `agent_authority` to `status --json`.
6. Add `agent_authority` to `preflight --json`.
7. Add one-line plain output for `status` and `preflight`.
8. Add authority-aware `recommended_next` lines to preflight.
9. Update the Mainline skill decision tree.
10. Add concise AGENTS/template guidance.

Do not include in v1:

- config set/get CLI;
- allow matrix;
- command blocking;
- Git hook enforcement;
- merge/release mode;
- PR merge automation.

## 18. Test plan

Focused tests should cover:

- default team config includes `agent.autonomy = handoff` for new init;
- missing `[agent]` backfills to handoff/review;
- local `review` overrides team `handoff` when max is omitted;
- team `max_autonomy = handoff` caps local `review`;
- invalid autonomy falls back with warnings and does not fail commands;
- `status --json` includes `agent_authority`;
- `preflight --json` includes `agent_authority`;
- preflight block sets `current.allowed_boundary = inspect_or_stop`;
- preflight block does not mutate `effective.autonomy`;
- plain status/preflight include one-line autonomy summary;
- existing command behavior remains advisory-only.

## 19. Important decisions

### D1. Make autonomy a first-class Mainline concept

Decision: Mainline should formally expose agent autonomy/stop-line state
through config and command output.

Rationale: Keeping this only in prompt text makes agent behavior depend
on ambiguous wording and prevents teams from setting a shared ceiling.

### D2. Use stop lines, not a permissions matrix

Decision: v1 uses `assist | handoff | review`.

Rationale: A matrix such as `allow.commit`, `allow.push_branch`, and
`allow.merge_pr` is too heavy for v1 and makes Mainline look like an ACL
system. Stop lines are easier for humans and agents to reason about.

### D3. Default to `handoff`

Decision: built-in and new-init default is `handoff`.

Rationale: Mainline is agent-native. The useful default is not pure
advice; it is a completed local/team-memory handoff. PR and merge remain
outside the default.

### D4. `handoff` includes commit and proposed intent

Decision: `handoff` includes scoped Git commit plus Mainline seal to
`proposed` when possible.

Rationale: A seal needs a commit to reference, and team memory is not
useful if it remains hidden as `sealed_local` without reason.

### D5. `review` stops at PR, not merge

Decision: `review` may push a non-main branch and open/update a PR, but
does not imply merge/release/post-merge cleanup.

Rationale: Opening review and changing shared main have different risk
profiles. Merge must remain an explicit delivery task.

### D6. Team `max_autonomy` is a hard ceiling

Decision: current user instruction and local preference cannot exceed
team `max_autonomy`.

Rationale: Without a hard ceiling, team policy cannot safely constrain
local dogfood preferences.

### D7. V1 is advisory-only

Decision: autonomy output guides agents but does not block CLI commands.

Rationale: This avoids surprising command failures and lets teams dogfood
the model before deciding which gates deserve enforcement.

### D8. Invalid config falls back with warnings

Decision: invalid autonomy values should warn and fall back, not fail
the command.

Rationale: Since v1 is advisory-only, a typo should not make Mainline
unusable.

### D9. `publish` is retry/repair, not a happy-path stage

Decision: `seal --submit` is the happy path to `proposed`; standalone
`mainline publish` is for sealed-local retry or metadata repair.

Rationale: Dogfood uses the proposed state, but a fixed `seal → publish`
script confuses metadata publish with code branch push.

### D10. Skill owns current-instruction interpretation

Decision: current user instruction override is interpreted by the agent
skill, not stored or parsed by the CLI.

Rationale: Natural-language intent is session-local and should not pollute
repository config.

### D11. PR body marker failure is a review hard stop

Decision: in `review`, Mainline-backed PRs must use generated PR
description with the hidden marker.

Rationale: Opening a generic PR body bypasses the memory/review surface
that Mainline is meant to provide.

## 20. Open questions

- Should `status` plain output always show autonomy, or should it become
  quiet when all values are defaults after users get familiar with the
  feature?
- Should a future v2 add soft enforcement for selected Mainline commands
  such as `seal`, `publish`, or `merge`?
- Should there be a future `mainline config set-local agent.autonomy
  review` helper for local dogfood setup?
- Should Hub surface autonomy in a team settings panel, or keep it CLI-only?

