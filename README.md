# Mainline

[![CI](https://github.com/mainline-org/mainline/actions/workflows/ci.yml/badge.svg)](https://github.com/mainline-org/mainline/actions/workflows/ci.yml)
[![Go 1.22+](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: layered](https://img.shields.io/badge/License-Apache--2.0%20%2F%20CC--BY--4.0%20%2F%20Commercial-blue.svg)](#license)

- Website: https://mainline.sh
- Detailed reference: [docs/reference.md](./docs/reference.md)
- 中文版本: [README.zh.md](./README.zh.md)

**We have code review. Now we need intent review.**

AI agents make code cheap to produce and harder to review. Mainline gives
agents and reviewers repo memory before the diff: prior decisions, constraints,
abandoned approaches, validation notes, and related in-flight work.

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
your code through Git refs and Git notes.

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

## The Mainline Loop

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

## Quick Start

Install the CLI:

```bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | bash
mainline doctor --setup
```

Initialize a repository:

```bash
cd your-repo
mainline init --actor-name "alice"
```

Let your agent create the first intent:

```bash
mainline context --current --json
mainline start "<the user's goal>"
mainline append "<meaningful progress>"
mainline seal --prepare --json > .ml-cache/seal.json
mainline seal --submit --json < .ml-cache/seal.json
```

Then open the human reading surface:

```bash
mainline hub open
mainline log
mainline show <intent_id>
mainline gaps
```

`mainline hub open` is most useful after at least one intent exists. On a fresh
repo, run the agent loop first, then open Hub to review what was recorded.

In normal use, the Mainline skill and hooks run the agent-facing commands for
supported agents. Full install options, command reference, recovery rules, hook
behavior, configuration, storage layout, and development commands live in
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
