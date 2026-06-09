# Mainline

[![CI](https://github.com/mainline-org/mainline/actions/workflows/ci.yml/badge.svg)](https://github.com/mainline-org/mainline/actions/workflows/ci.yml)
[![Go 1.22+](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: layered](https://img.shields.io/badge/License-Apache--2.0%20%2F%20CC--BY--4.0%20%2F%20Commercial-blue.svg)](#license)

- Website: https://mainline.sh
- Hosted Hub: https://mainline.sh/hub/
- Detailed reference: [docs/reference.md](./docs/reference.md)
- 中文版本: [README.zh.md](./README.zh.md)

**We have code review. Now we need intent review.**

Mainline is a Git-native memory layer for coding agents. It gives agents and
reviewers repo memory before the diff: prior decisions, constraints, abandoned
approaches, validation notes, and related in-flight work.

AI agents make code cheap to produce and harder to review. Mainline makes the
intent reviewable before the generated code lands.

Review the intent before you review the code.

<img width="2530" height="756" alt="Mainline overview" src="https://github.com/user-attachments/assets/e337559b-72cd-4fd4-b139-16754cc675f6" />

<img width="1600" alt="Mainline Hub showing a sealed engineering intent" src="https://github.com/user-attachments/assets/2c740a17-019f-4f16-bd8a-e812d8a78f32" />

## The Problem

Code review was built for a world where humans wrote most of the code. The diff
was expensive, so it was usually small enough for reviewers to infer the intent.

Agent work changes that. A coding agent can produce a wide diff quickly. The
hard review question moves up a level:

- is this the right problem to solve?
- did the agent understand the prior decision?
- is it repeating an abandoned approach?
- did it miss a reviewer constraint?
- is another agent already working on a related intent?
- does the validation actually match the reason for the change?

If reviewers only see the final diff, they are forced to reconstruct intent
after the work is already shaped.

### A Realistic Failure

A billing team moves invoice export to a new `/exports/invoices` API, but keeps
the old `/reports/invoices.csv` route because three enterprise customers still
pull it from overnight reconciliation jobs until their migration window closes.

Three weeks later, a coding agent is asked to clean up legacy reporting code.
The old route has little product traffic, the new API is where active UI code
points, and the compatibility branch looks removable. The agent deletes it.
Unit tests pass. The dashboard looks clean. The next morning, customer finance
jobs fail.

The important fact was not visible in the diff: **do not remove the legacy CSV
invoice export until the enterprise reconciliation migration is complete**.

## What Mainline Does

Mainline records the intent behind engineering work and makes it available
before the next risky edit.

An intent captures:

- the user goal,
- why the work exists,
- decisions and rejected alternatives,
- validation and review notes,
- explicit constraints, risks, and follow-ups,
- related files and subsystems,
- in-flight overlap with other agents or teammates,
- the commit that eventually carried the work onto `main`.

Mainline is not a Git replacement, PR system, session recorder, RAG index, or
productivity dashboard. It is repo-local engineering memory that travels with
your code through Git refs and Git notes. Read that memory with `mainline log`,
`mainline show <id>`, or `mainline hub open`.

## Why Comments Are Not Enough

Good comments still matter. If a function has a local invariant, write it down.

But comments are a weak place to store repo-level intent:

- the agent may plan the change before opening the right file,
- the decision may span services, release steps, customer migrations, or policy,
- abandoned approaches often live outside current code,
- comments rarely show in-flight work from another agent,
- stale comments do not carry lifecycle, validation, or reviewer context.

Mainline does not depend on the next agent finding the right comment. It gives
agents and reviewers a queryable intent layer before the diff.

## Install

Install the CLI:

```bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | bash
mainline doctor --setup
```

Other install paths are available in the detailed reference:

```bash
go install github.com/mainline-org/mainline@latest
```

Downloadable release archives and checksums are published on
[GitHub Releases](https://github.com/mainline-org/mainline/releases/latest).

## Getting Your Agent Started

Initialize a repository once:

```bash
cd your-repo
mainline init --actor-name "alice"
```

`mainline init` sets up repo-local Mainline state, configures the Git refs
Mainline needs, installs the Mainline skill, and installs hooks for supported
agents such as Codex, Claude Code, and Cursor.

Hooks run `mainline sync` and `mainline status` at session start so the agent
begins with fresh repo state. The hooks do not decide what to do. The agent
still reads context, records progress, seals the intent, and surfaces conflicts
through the Mainline skill workflow.

Existing agent skill installs are updated by the `skills` CLI, not by
`mainline agents update` or `mainline init --rewire`. If update cannot infer
the source, rerun the matching `skills add` command:

```bash
npx --yes skills update mainline --global --yes
npx --yes skills add mainline-org/mainline --skill mainline --agent codex claude-code cursor --global --yes
```

On an existing repository, `mainline init` treats the current `main` HEAD as the
coverage baseline. Older history is skipped by default; new commits should have
intent coverage.

## What Agents Run

For non-trivial work, the agent-facing loop is:

```bash
mainline preflight --json
mainline start "<the user's goal>" --json
mainline append "<meaningful progress>" --json
mainline seal --prepare --json > .ml-cache/seal.json
mainline seal --submit --json < .ml-cache/seal.json
```

`preflight` is the readiness and stop-line gate. It tells the agent whether to
continue, inspect overlaps, or stop before lifecycle advancement. Read-only
diagnosis or proposal-only work can stop after read-only inspection; it should
not run `start` until the task crosses into non-trivial edits or another
durable engineering record. `start` then claims the unit of work. `append`
records meaningful turns: decisions, pivots, completed slices, or validation
that changes confidence. `seal` turns the work into reviewable intent with a
summary, decisions, rejected alternatives, validation notes, and a semantic
fingerprint.

Review autonomy may push a non-main branch and open or update a PR. It never
authorizes pushing `main`, merging, releasing, or deploying.

Agents should run this before architecture changes, refactors, migrations,
deletions, auth/billing/permissions/data-model work, release/CI changes, and
questions like "can we delete this?" or "was this tried before?"

Tiny typo fixes, pure formatting, and one-line obvious syntax repairs can skip
Mainline.

## Workflow Fit

Mainline sits beside your normal Git workflow.

1. **Before editing**, the agent reads relevant intent with `mainline context`.
2. **During the work**, it records meaningful turns with `start` and `append`.
3. **Before review**, it seals the intent with decisions, validation notes, and
   a semantic fingerprint.
4. **During review**, humans inspect the intent and collaboration surface before
   or alongside the code diff.
5. **After merge**, `mainline sync` links the merged commit back to the intent.
6. **Next time**, future agents read that history before they edit.

The point is not ceremony. The point is that the team can review the intended
change, not just the generated code.

## CLI And Hub

Mainline has two surfaces:

- **CLI for action:** initialize the repo, sync state, record intent, inspect
  history, find gaps, and generate review material.
- **Hub for reading:** browse intent history, pending work, file-level context,
  coverage gaps, risks, and collaboration signals.

After at least one intent exists, open Hub:

```bash
mainline hub open
```

Useful human commands:

```bash
mainline status --actionable
mainline log
mainline show <intent_id>
mainline gaps
```

`mainline hub open` is most useful after the agent has produced at least one
intent. On a fresh repo, run the agent loop first, then open Hub to review what
was recorded.

For static export:

```bash
mainline hub export ./mainline-hub
```

Fork PRs may be shown as imported external contributions when the contributor
has no upstream-visible Mainline actor log:

```bash
mainline hub export ./mainline-hub --external-contributions fork-prs.json
```

If the fork contributor also uses Mainline, prefer importing their actor log
first. This is an explicit trust-boundary action by an upstream maintainer:

```bash
mainline actor import --actor actor_jiangge --remote jiangge
```

The command fetches that actor's `refs/mainline/actors/<actor>/log` from the
fork into a temporary import ref, validates the events, accepts the actor log
into the upstream namespace, rebuilds the view, and runs normal auto-pin. The
contributor's intent remains author-sealed, while Hub shows provenance such as
`accepted_actor_log`, who accepted it, and whether it was verified.

The `--external-contributions` file is only the fallback when no author-owned
actor log is available. Those records are labeled with provenance such as
`github_pr_imported` and `not author-sealed`. Hub does not treat GitHub PR
metadata or an empty `## Mainline Intent` PR-body template as a
contributor-authored sealed Mainline intent.

The public hosted Hub for Mainline is https://mainline.sh/hub/.

The detailed reference covers install variants, recovery rules, hook behavior,
webhooks, configuration, static Hub publishing, storage layout, and development commands:
[docs/reference.md](./docs/reference.md).

## Does It Work?

We ran a controlled eval: 8 scenarios, 3 seeds, 2 modes.

| Mode | Forbidden-list violations | Consistency |
|---|---:|---|
| Intent-first | 0 | 0/8 fixtures fail |
| Code-first | 9 | 2/8 fixtures fail consistently |

The wins showed up where code could not reveal the answer: abandoned approaches,
superseded decisions, and conventions outside source code.

Read the full methodology and caveats in [docs/eval-results.md](./docs/eval-results.md).

## When To Use It

Use Mainline before non-trivial agent work:

- architecture changes,
- refactors and migrations,
- deletions,
- auth, billing, permissions, and data model changes,
- release and CI changes,
- questions like "can we delete this?" or "was this tried before?",
- any work where another agent or teammate might be operating nearby.

Skip it for narrow typo fixes, pure formatting, and one-line obvious syntax
repairs.

## Learn More

- Detailed reference: [docs/reference.md](./docs/reference.md)
- Eval report: [docs/eval-results.md](./docs/eval-results.md)
- Intent Record Spec: [docs/specs/intent-record-v0.md](./docs/specs/intent-record-v0.md)
- Agent Context Protocol: [docs/specs/agent-context-protocol-v0.md](./docs/specs/agent-context-protocol-v0.md)
- Contributing: [CONTRIBUTING.md](./CONTRIBUTING.md)
- Security: [SECURITY.md](./SECURITY.md)
- Changelog: [CHANGELOG.md](./CHANGELOG.md)

## Development

```bash
go build -o mainline .
make quick-test
make test
make lint
```

Core subsystems are covered with property-based tests. The fast PR gate is
`make quick-test`; broader PBT coverage is documented in
[docs/reference.md](./docs/reference.md#development-and-testing).

## License

Mainline uses a layered licensing model. The local CLI, agent skills, hooks,
adapters, libraries, and protocol specs are intended to be open and embeddable.
Docs and examples are licensed for reuse with attribution. Hosted service
surfaces and brand assets remain separate.

See [docs/reference.md](./docs/reference.md#license-details) and
[LICENSE](./LICENSE) for details.
