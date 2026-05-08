# Mainline

[![CI](https://github.com/mainline-org/mainline/actions/workflows/ci.yml/badge.svg)](https://github.com/mainline-org/mainline/actions/workflows/ci.yml)
[![Go 1.22+](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: layered](https://img.shields.io/badge/License-Apache--2.0%20%2F%20CC--BY--4.0%20%2F%20Commercial-blue.svg)](#license)

- Website: https://mainline.sh
- Detailed reference: [docs/reference.md](./docs/reference.md)
- 中文版本: [README.zh.md](./README.zh.md)

**Stop AI coding agents from repeating old engineering mistakes.**

Mainline is a Git-native memory layer for coding agents. Before an agent edits
code, Mainline tells it why the code is the way it is: decisions, abandoned
approaches, reviewer constraints, validation notes, and related in-flight work.

It is not a Git replacement, PR system, session recorder, or productivity
dashboard. It is repo-local engineering memory that travels with your code.
It shifts review one level up: code review still matters, but agent-era work
also needs intent review and intent-aware collaboration before the diff lands.

<img width="2530" height="756" alt="Mainline overview" src="https://github.com/user-attachments/assets/e337559b-72cd-4fd4-b139-16754cc675f6" />

<img width="1600" alt="Mainline Hub showing a sealed intent for migrating auth to JWT" src="https://github.com/user-attachments/assets/2c740a17-019f-4f16-bd8a-e812d8a78f32" />

## The Problem

A billing team moves invoice export to a new `/exports/invoices` API, but keeps
the old `/reports/invoices.csv` route because three enterprise customers still
pull it from overnight reconciliation jobs until their migration window closes.

Three weeks later, a coding agent is asked to clean up legacy reporting code.
The old route has little product traffic, the new API is where active UI code
points, and the compatibility branch looks removable. The agent deletes it.
Unit tests pass. The dashboard looks clean. The next morning, customer finance
jobs fail.

Code alone did not tell the agent the important fact: **do not remove the
legacy CSV invoice export until the enterprise reconciliation migration is
complete**.

That is the class of problem Mainline solves. AI coding agents are fast, but
source code does not reliably explain:

- which approaches were tried and abandoned,
- which decisions superseded older implementations,
- which constraints reviewers expect future changes to preserve,
- which conventions live outside source code,
- which teammate or agent is already working on a related intent.

RAG can find similar code. Grep can verify what exists now. Mainline gives the
agent the missing layer: repo-level engineering fact.

## Why Comments Are Not Enough

A code comment can help when the future agent is already reading the exact line.
It is still worth writing good comments for local invariants.

But comments are a weak place to store repo-level intent:

- the agent may never open the file before planning the change,
- the decision may span several files, services, or release steps,
- abandoned approaches often live outside the current code,
- comments rarely show who is already working on related changes,
- stale comments do not carry lifecycle, validation, or review context,
- reviewers need to inspect intent before they inspect the diff.

Mainline keeps those facts as explicit, queryable repo memory instead of hoping
the next agent happens to read the right comment at the right time.

## The Solution

Mainline records the intent behind engineering work and surfaces it before the
next risky edit.

An intent answers:

- what changed,
- why the work existed,
- which decisions were made,
- which alternatives were rejected,
- what was validated,
- which constraints, risks, or follow-ups should survive the current session,
- which commit eventually carried the work onto `main`.

The result is simple: the next agent reads the durable memory before it writes a
diff. If the repo has a known constraint, abandoned approach, or overlapping
piece of in-flight work, the agent can stop before repeating the mistake.

## Review The Intent

When agents can produce a large diff quickly, reviewing only code is too late
and too narrow. The higher-leverage review moves earlier:

- review the intent before reviewing the implementation,
- check whether the goal conflicts with prior decisions,
- see which constraints and risks the agent believes are active,
- notice overlapping work before two agents create incompatible PRs,
- judge whether the validation matches the reason for the change.

Mainline gives humans and agents a shared surface for that review. The question
is no longer just "does this diff look right?" It is also "is this the right
change to make, given what this repo already knows?"

## How It Works

Mainline sits beside your normal Git workflow.

1. Run `mainline init` once in a repository.
2. Hooks give supported agents fresh `sync` + `status` context at session start.
3. Before non-trivial edits, the agent runs `mainline context`.
4. During the work, the agent records meaningful turns with `start` and
   `append`.
5. Before review, the agent seals the intent with decisions, validation notes,
   and a semantic fingerprint.
6. Humans review the intent and collaboration surface before or alongside the
   code diff.
7. After merge, `mainline sync` links the merged commit back to the intent.
8. Future agents read that history before the next risky edit.

Mainline stores durable team data in Git refs and Git notes. Local caches live
under `.ml-cache/` and can be rebuilt.

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

Open the human reading surface:

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

The agent-facing loop is usually run by the Mainline skill and hooks:

```bash
mainline context --current --json
mainline start "<the user's goal>"
mainline append "<meaningful progress>"
mainline seal --prepare --json > .ml-cache/seal.json
mainline seal --submit --json < .ml-cache/seal.json
```

Full install options, command reference, recovery rules, hook behavior,
configuration, storage layout, and development commands live in
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

Use Mainline before non-trivial agent work: architecture changes, refactors,
migrations, deletions, auth, billing, permissions, data model work, release/CI
changes, and questions like "can we delete this?" or "was this tried before?"

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
