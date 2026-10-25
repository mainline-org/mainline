# Mainline

[![CI](https://github.com/mainline-org/mainline/actions/workflows/ci.yml/badge.svg)](https://github.com/mainline-org/mainline/actions/workflows/ci.yml)
[![Go 1.22+](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: layered](https://img.shields.io/badge/License-Apache--2.0%20%2F%20CC--BY--4.0%20%2F%20Commercial-blue.svg)](#license)

- Website: https://mainline.sh
- Hosted Hub: https://mainline.sh/hub/
- Detailed reference: [docs/reference.md](./docs/reference.md)
- 中文版本: [README.zh.md](./README.zh.md)

**Git for the AI era.**

https://github.com/user-attachments/assets/75826aba-0016-448c-b889-eb2dea83ca36

Mainline lets agents save developer intent and decisions to Git alongside the
code.

Git already records the history of the code. Mainline adds the engineering
judgment behind agent work to the same collaboration layer: the original goal,
reasoning path, key decisions, trade-offs, validation, explicit constraints,
abandoned routes, and the commit that eventually carried the work.

Teams do not need to keep long AI transcripts around, and they do not need to
invent a separate shared memory service. Agents read prior judgment from Git,
do the work, and write the new judgment back to Git.

## Why It Matters

Agents can move fast, but code alone does not carry the full history. Mainline
is not another knowledge base for humans to maintain; it is a tool agents can
read and write as naturally as they use Git.

### Stop Repeating Abandoned Paths

Some approaches were tried, rejected, and then disappeared from the code.

Maybe the team once tried to process billing events through a Redis queue, then
abandoned it after replication lag caused duplicate charges. The next agent may
grep its way to a half-finished TODO and decide it only needs to complete the
work. Mainline tells the agent before it edits: this path was already tried,
where it failed, and why it should not be revived casually.

### Catch Logical Conflicts Before Git Conflicts

Git can detect two edits to the same line. It cannot tell when two agents are
changing the same product logic in incompatible ways.

If another agent has already started changing a billing rule, there may be no PR
yet, but there is already an intent. Other agents can see that intent during
preflight and avoid the collision from the start by changing scope, splitting
the boundary, or syncing with the teammate first.

### Read Constraints Before Editing Risky Code

Some code looks odd because it carries real history, and comments rarely capture
all of it.

An auth middleware path may exist only because a previous cleanup caused a
rollback. Mainline can put the rollback reason, module constraints, and related
intent in front of the agent before it edits, so the agent understands why that
code is not safe to "clean up" blindly.

### Review The Intent Behind The Diff

Reviewers should see more than the code diff. They should see the implementer's
original goal, reasoning path, and key decisions.

Review no longer has to start by guessing why a diff exists. The reviewer can
read the intent first, then verify whether the implementation matches that
intent. The conversation moves earlier, toward direction, risk, and trade-offs,
instead of only catching syntax and edge cases at the end.

## Git-Native Workflow

Mainline's storage and collaboration model is designed around Git.

| Capability | How Mainline Works |
|---|---|
| Data lives in the Git repo | Intent event logs live in Git refs; commit pins live in Git notes |
| Git-native workflow | Intent travels through fetch, branch, merge, and fork workflows |
| Agents read and write automatically | Hooks, the Mainline skill, and the CLI let agents read history, record judgment, and seal intent |
| No vendor lock-in | Codex, Claude Code, Cursor, Pi, Copilot, and internal agents can work from the same repo facts |
| No disruption to your workflow | Keep using your editor, agent, GitHub / GitLab PRs, and CI as usual |

Mainline does not replace Git. It puts the engineering judgment future agents
need back into Git.

## How Agents Use Mainline

Mainline is mainly operated by agents. After the initial setup, developers do
not need extra daily commands, and their existing AI coding workflow does not
change.

It plugs into agent workflows through three pieces:

- **Agent hooks**: sync repo intent at session start and inject fresh state;
- **Mainline skill**: tells the agent when to read history, record judgment, and stop;
- **CLI**: provides auditable commands such as `preflight`, `start`, `append`, and `seal`.

If an agent does not have a hook integration yet, it can still use Mainline
through the skill and CLI. The rule stays the same: read prior judgment from Git
before editing, then write the new judgment back to Git when the work is done.

## The Full Loop

A typical agent run looks like this:

```bash
mainline preflight --json
mainline start "clean up old export logic" --json
mainline append "confirmed the legacy CSV route is still used for overnight enterprise reconciliation" --json
mainline seal --prepare --json > .ml-cache/seal.json
mainline seal --submit --json < .ml-cache/seal.json
```

Those steps map to the agent loop:

1. **Get context**: the agent checks the current branch, prior intents, collaboration risks, and relevant constraints.
2. **Do the work**: it reads code, edits, validates, and responds to reviewer feedback as usual.
3. **Record pivots**: when a decision, rejected path, risk change, or validation result matters, `append` records it.
4. **Seal the decision record**: `seal` turns the work into intent that can be reviewed, synced, and retrieved by future agents.
5. **Let it flow through Git**: Git refs and notes carry the intent through fetch, branch, and merge workflows.

Future agents do not have to search an old chat transcript for clues. They
inherit context from the history the repository already carries.

## Quick Start

Install the CLI:

```bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | bash
mainline version
```

You can also install with Go:

```bash
go install github.com/mainline-org/mainline@latest
```

Downloadable release archives and checksums are published on
[GitHub Releases](https://github.com/mainline-org/mainline/releases/latest).

`mainline version` only verifies that the CLI is installed and on your `PATH`.
`mainline doctor --setup` is a per-repository wiring check and expects to run
inside a Git repository.

Initialize each repo once:

```bash
cd your-repo
mainline init --actor-name "alice"
```

`mainline init` will:

- write repo-local Mainline configuration;
- configure the required Git refs and notes;
- install the Mainline skill;
- install repo-local hooks for supported agents such as Codex, Claude Code, Cursor, and Pi;
- record the current `main` HEAD as the coverage baseline for an existing repository.

If a repository was initialized before a given agent hook existed, install hooks
again:

```bash
mainline hooks install
# or only install one agent integration
mainline hooks install --agent pi
```

Use the `skills` CLI to update the global skill:

```bash
npx --yes skills add mainline-org/mainline --skill mainline --agent codex claude-code cursor pi --global --yes
```

## Commands Humans Usually Run

Most day-to-day commands are run by agents. Humans usually read state, inspect
intent, and open Hub.

```bash
mainline status --actionable
mainline log
mainline show <intent_id>
mainline gaps
mainline hub open
```

Hub lets you browse intent history, pending work, file-level context, coverage
gaps, risks, and collaboration signals.

<img width="1600" alt="Mainline Hub showing a sealed engineering intent" src="https://github.com/user-attachments/assets/2c740a17-019f-4f16-bd8a-e812d8a78f32" />

For static export:

```bash
mainline hub export ./mainline-hub
```

The public hosted Hub for Mainline is https://mainline.sh/hub/.

The detailed reference covers install variants, recovery rules, hook behavior,
webhooks, configuration, static Hub publishing, storage layout, and development
commands: [docs/reference.md](./docs/reference.md).

## Team Collaboration

For agents working in LayerFS Workspaces, see
[Mainline for LayerFS](plugins/layerfs/README.md) for the CLI adapter, host worker
launcher and agent skills. Its default tests run without Docker.

Mainline gets its collaboration model from Git.

Teammates, branches, forks, CI, and other agents can all share the same intent
history as long as they can reach the same repository. A teammate should not
have to ask "why did yesterday's agent do this?" or dig through somebody else's
local chat transcript.

Typical collaboration patterns:

- New teammates read `mainline log` and Hub to catch up on recent project judgment.
- Reviewers read intent before reading the diff.
- Agents see nearby intent during preflight and avoid starting with a conflicting plan.
- Fork contributors can publish their actor log, and upstream maintainers can explicitly accept it.
- After merge, the commit and intent are pinned together as context for the next round of work.

Fork contributor flows, external PRs, and actor-log import are covered in
[docs/reference.md](./docs/reference.md).

## When To Use It

Use Mainline before agent work that is more than a tiny edit:

- architecture changes,
- refactors and migrations,
- deletions,
- auth, billing, permissions, and data model changes,
- release and CI changes,
- questions like "can we delete this?",
- questions like "was this tried before?",
- any work where another agent or teammate might be operating nearby.

Skip it for narrow typo fixes, pure formatting, and one-line obvious syntax
repairs.

## Does It Work?

We ran a controlled eval: 8 scenarios, 3 seeds, 2 modes.

| Mode | Forbidden-list violations | Consistency |
|---|---:|---|
| Intent-first | 0 | 0/8 fixtures fail |
| Code-first | 9 | 2/8 fixtures fail consistently |

The wins showed up where code could not reveal the answer: abandoned approaches,
superseded decisions, and conventions outside source code.

Read the full methodology and caveats in [docs/eval-results.md](./docs/eval-results.md).

## Learn More

- Detailed reference: [docs/reference.md](./docs/reference.md)
- Eval report: [docs/eval-results.md](./docs/eval-results.md)
- Intent Record Spec: [docs/specs/intent-record-v0.md](./docs/specs/intent-record-v0.md)
- Agent Context Protocol: [docs/specs/agent-context-protocol-v0.md](./docs/specs/agent-context-protocol-v0.md)
- Agent Autonomy Stop Lines: [docs/specs/agent-autonomy-stop-lines-v0.md](./docs/specs/agent-autonomy-stop-lines-v0.md)
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

## Project Status

Mainline is in public alpha. The CLI, skill, hooks, Hub, and Git refs / notes
storage model are usable today, but the schema, agent workflow guidance, and
Hosted Hub experience are still evolving.

Mainline is a good fit for teams that already use agents in real engineering
workflows and are starting to worry about context getting lost. Feedback is
welcome.

## License

Mainline uses a layered licensing model. The local CLI, agent skills, hooks,
adapters, libraries, and protocol specs are intended to be open and embeddable.
Docs and examples are licensed for reuse with attribution. Hosted service
surfaces and brand assets remain separate.

See [docs/reference.md](./docs/reference.md#license-details) and
[LICENSE](./LICENSE) for details.
