# Mainline

[![CI](https://github.com/mainline-org/mainline/actions/workflows/ci.yml/badge.svg)](https://github.com/mainline-org/mainline/actions/workflows/ci.yml)
[![Go 1.22+](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![PBT](https://img.shields.io/badge/PBT-property--based%20testing-blueviolet)](#property-based-testing)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

**Git-native intent memory for AI-assisted engineering.**

Mainline gives coding agents the historical *why* before they inspect the current *what*.

Use it alone to give your future agents memory.
Use it with a team to make intent visible before review and collaboration.

> 中文版本: [README.zh.md](./README.zh.md)

AI coding agents are fast, but code alone cannot tell them:

- which approaches were tried and abandoned,
- which decisions superseded older implementations,
- which conventions live outside source code,
- which constraints reviewers expect future changes to preserve,
- which teammate is already working on a related intent.

RAG can retrieve similar code.
Grep can verify what code exists right now.
Mainline gives agents the missing layer: **engineering intent**.

Stop your AI agent from silently undoing yesterday's decision, repeating an
abandoned approach, missing a reviewer constraint, or stepping on a teammate's
in-flight work.

Mainline records *why* each engineering change was made — decisions, risks,
anti-patterns, references, and lifecycle — then surfaces that record to the
next agent or human at the moment they need it.

## Who is Mainline for?

### Solo builders

When you work alone with AI agents, the problem is continuity.
One agent may abandon an approach, accept a risk, or supersede a decision.
The next agent will not know unless that intent is recorded.

Mainline gives your future self and future agents a durable memory of *why the code is this way*.

Mainline helps solo developers:

- avoid repeating abandoned approaches,
- preserve why a change was made,
- remember which decisions replaced older implementations,
- hand off context between agents, branches, and future sessions,
- return to a codebase weeks later and understand why it looks this way.

### Teams

When a team works with AI agents, the problem is shared intent.
Reviewers need to understand *why* before diff.
Teammates need to know what is in flight.
Future agents need to avoid old mistakes.

Mainline turns individual AI-assisted changes into shared engineering memory:

- review intent before reviewing diffs,
- keep decisions, risks, and anti-patterns attached to the work,
- see proposed or in-flight intent before PR conflicts appear,
- preserve abandoned and superseded decisions for future agents,
- track whether important changes have intent coverage,
- onboard new teammates into the *why* behind the code.

## What Mainline enables

Mainline is not just a log of AI work. It is an intent memory layer for the whole engineering loop:

1. **Agent pre-edit memory**
   Agents read prior decisions, risks, anti-patterns, abandoned approaches, and superseded decisions before editing code.

2. **Intent governance**
   Teams can see whether important changes have intent coverage, whether sealed intents are high-quality, and whether risky changes are missing constraints or rationale.

3. **Human review intent**
   Reviewers read the why, decisions, risks, and constraints before reviewing the diff — turning review from "guess the author's intent" into "verify the implementation against the intent."

4. **Long-term decision memory**
   Future maintainers and new teammates can understand why files are the way they are, which approaches were tried and abandoned, and which decisions are still effective.

5. **Intent-aware collaboration**
   Teams can sync intent logs, see proposed or in-flight work, detect overlap and conflicts earlier, and avoid stepping on a teammate's work before PR review.

## Who runs what?

Mainline has two audiences in the same repo. They run different commands.

**Your AI agent** (Cursor, Claude Code, Codex, etc.) reads intent before it
edits and writes intent after it edits. The agent's loop:

```bash
mainline status                          # at session start
mainline context --current --json        # before non-trivial edits — read prior decisions / anti-patterns
mainline start "<the user's goal>"       # claim work
mainline append "<what changed>"         # after each meaningful turn
mainline seal --prepare > .ml-cache/seal.json   # → patch → mainline seal --submit < .ml-cache/seal.json
```

You don't memorise this — `mainline init` installs a Mainline skill that gives
the agent the complete protocol. If hooks are installed (also done by init),
the agent receives fresh team state at session start automatically.

**You** (the human) review intent, browse history, and quality-check the
team's record:

```bash
mainline log                             # what's been shipped recently
mainline show <intent_id>                # full record of one decision
mainline trace <intent_id>               # how a decision unfolded turn-by-turn
mainline hub open                        # browse history in a browser
mainline gaps                            # commits on main that have no recorded intent
mainline lint <intent_id>                # quality-check a teammate's seal
```

You don't have to type these — `mainline hub open` is the one to remember; the
rest are there when you want them.

## Getting started with your agent

One-time setup per repo:

```bash
mainline init --actor-name "<your name>"
```

`mainline init` does three things:

1. Writes `.mainline/config.toml` and configures git refspecs.
2. Installs the **Mainline skill** — the complete workflow manual for agents.
3. Installs **repo-local hooks** for supported agents (Cursor, Claude Code,
   Codex) — at every session start, hooks run `mainline sync` + `mainline
   status` and inject the snapshot into the agent's system context.

Your agent now sees fresh team state at every session start without you
typing anything. The agent itself drives the rest of the workflow (start /
append / seal / check) per the Mainline skill — Mainline is a context
provider, not a workflow driver.

If your AI tool doesn't support hooks, it can still follow the same
protocol via the Mainline skill — both paths work.

For teams that want explicit repo-level policy, `mainline agents install`
writes a lightweight `AGENTS.md` policy pointer — but this is opt-in,
not required.

## What problem this solves

| Pain | Without Mainline | With Mainline |
|---|---|---|
| Agent re-removes the legacy `/oauth` middleware you kept on purpose | Silent rework, prod outage | Agent reads the anti-pattern and stops before the diff |
| You forgot why you chose JWT over sessions 3 weeks ago | `git log` doesn't carry decisions | `mainline show <id>` returns title / what / why / decisions / risks |
| Two agents on the same repo solving the same problem differently | Discovered at PR-review time | `mainline check` flags the overlap on `seal --submit` |
| New maintainer asks "why is this code like this?" | Slack archaeology | `mainline context --files src/auth/middleware.go` |
| You want to know which commits on `main` have no recorded intent | No signal | `mainline gaps` |

> **Does it actually work?** We ran a controlled eval: 8 scenarios × 3 seeds ×
> 2 modes. Code-first agents committed 9 forbidden-list violations; intent-first
> agents committed 0. The advantage consistently reproduced on abandoned-approach
> and superseded-decision tasks — scenarios where code cannot reveal the
> constraint. [Full report →](./docs/eval-results.md)

## Table of contents

- [Install](#install)
- [What Mainline enables](#what-mainline-enables)
- [Eval: does intent-first actually help?](#eval-does-intent-first-actually-help)
- [Five-minute quick start](#five-minute-quick-start)
- [How it fits your workflow](#how-it-fits-your-workflow)
- [What Mainline records](#what-mainline-records)
- [CLI and Hub](#cli-and-hub)
- [Architecture](#architecture)
- [Concepts](#concepts)
- [Daily commands](#daily-commands)
- [Advanced commands](#advanced-commands)
- [Agent hooks (opt-in)](#agent-hooks-opt-in)
- [Webhook subscriptions](#webhook-subscriptions)
- [Configuration](#configuration)
- [FAQ](#faq)
- [Specs](#specs)
- [Related tools and boundaries](#related-tools-and-boundaries)
- [Storage layout](#storage-layout)
- [Development](#development)
- [Project structure](#project-structure)
- [Roadmap](#roadmap)
- [Community and security](#community-and-security)
- [License](#license)

## Install

```bash
go install github.com/mainline-org/mainline@latest
```

Or build from source:

```bash
git clone https://github.com/mainline-org/mainline
cd mainline && go build -o mainline .
```

After installing, verify your setup at any time:

```bash
mainline doctor --setup
```

## Eval: does intent-first actually help?

In our first controlled eval, we tested 8 engineering scenarios with two agent modes:

- **code-first**: task + code only
- **intent-first**: task + code + Mainline historical intent context

Across 3 independent Claude Sonnet 4 runs, code-first agents repeatedly failed
in two history-dependent scenarios:

| Mode | Violations | Consistency |
|---|---|---|
| **Intent-first** | **0 across all seeds** | 0/8 fixtures fail |
| Code-first | 9 violations (3/seed) | 2/8 fixtures fail consistently |

Code-first fails on exactly the scenarios where **code cannot reveal the
constraint:**

1. A prior approach was **abandoned** — redis.go looks 60% done with TODOs and
   docker-compose has Redis defined. Every code-first agent proposes finishing it.
   Only intent reveals the replication-lag failure.
2. A decision was **superseded** — Both CSV and Parquet endpoints work and receive
   traffic. Every code-first agent adds the column to both. Only intent says
   "CSV is deprecated, Parquet only".

Intent-first agents read `mainline context`, see the anti-pattern, and
explicitly decline with reference. Code-first agents have no signal.

This is not a broad benchmark claim. It is an early signal that intent memory
helps when the correct action depends on abandoned approaches, superseded
decisions, or conventions not visible in code.

**Run it yourself:**

```bash
mainline eval run                                          # layer 1: retrieval preconditions (8/8 pass)
mainline eval agent --runner ./scripts/eval-runner-copilot.py \
  --judge ./scripts/eval-judge-copilot.py                  # layer 2: v2 scorer (CF=4, IF=0)
```

Full methodology, per-fixture breakdowns, and caveats →
[docs/eval-results.md](./docs/eval-results.md)

## Five-minute quick start

The lines marked **[you]** are what you type. The rest are what the agent
runs (driven by the Mainline skill, or auto-injected via hooks).

```bash
# [you] one-time per repo
cd your-repo
mainline init --actor-name "alice"     # or: export MAINLINE_ACTOR_NAME first
# if you add a git remote later, run: mainline init --rewire

# hooks are installed by init; to repair or add a specific agent:
# mainline hooks install --agent cursor

# [agent] at session start
mainline status

# [agent] before non-trivial edits — read prior intent
mainline context --current --json
# returns relevant historical intents with status / anti_patterns / decisions

# [agent] claim work
mainline start "Add JWT auth"

# [agent] after each meaningful turn
mainline append "Implemented JWT middleware"
mainline append "Added refresh-token rotation"

# [you OR agent] commit code the normal way
git add . && git commit -m "Add JWT auth"

# [agent] seal at end of task
mainline seal --prepare > .ml-cache/seal.json
# (.ml-cache/ is gitignored by init, so this temp file does not
# trip the dirty-worktree check)
# the package contains a `seal_result_starter` field with intent_id
# + files_touched + subsystems pre-filled; the agent patches in
# title/what/why/decisions/risks/anti_patterns/confidence and submits
mainline seal --submit < .ml-cache/seal.json
# inline soft-lint summary appears if the seal has issues; conflicts
# (phase 1) print with explicit `mainline check --prepare` follow-up

# [you] open a PR on GitHub; merge with the web UI

# [you or agent] next sync auto-pins the squash commit to the intent
mainline sync
```

That's the whole loop. No special merge command required.

After a few intents have landed, run `mainline hub open` (yours, or
suggested by the agent) — Mainline opens a static HTML browser of recent
intents, per-file history, risks, and supersedes/conflict graph. This is
the human-reader surface; agents use the JSON commands above.

## How it fits your workflow

```
┌────────────────────────────────────────────────────────────────┐
│ Author                                                          │
│   start → append → seal --submit  ─┐                            │
│                                     │ auto sync + phase1 check  │
│                                     ▼                           │
│   open PR on GitHub                Conflict warnings printed    │
│                                                                 │
│ Reviewer                                                        │
│   sync   →   sees [check:~] in log if phase1 caught something   │
│   check       runs phase2 agent judgment                        │
│   review code on GitHub as usual                                │
│                                                                 │
│ Merge                                                           │
│   GitHub web UI → squash and merge                              │
│                                                                 │
│ Pin                                                             │
│   anyone's next `mainline sync`                                 │
│   →  tree-hash auto-pin links commit to intent  →  status flips │
│      to merged                                                  │
└────────────────────────────────────────────────────────────────┘
```

Mainline never asks you to change your merge process. The `mainline merge` command exists for non-PR pipelines but is **not** part of the supported default flow.

## What Mainline records

A sealed Mainline intent contains:

- **Why** — why this engineering work exists.
- **Decisions** — what the team decided, with rationale and rejected alternatives.
- **Risks** — soft risks reviewers should be aware of.
- **Anti-patterns** — hard constraints future agents must avoid.
- **Inherited constraints** — file-level constraints from prior intents.
- **Lifecycle** — merged, abandoned, superseded, or reverted.
- **References** — optional links to issues, PRs, docs, CI runs, or external sessions.
- **Commit pins** — links between intents and the commits that implemented them.

Mainline does not try to preserve every token of an AI session.
It preserves the durable decision record future agents and reviewers need.

## CLI and Hub

Mainline has two first-class surfaces:

- **CLI for action** — start, append, seal, lint, sync, context, show, trace.
- **Hub for reading** — review pending work, inspect file constraints, read important decisions, and orient around the repo's intent history.

Use the CLI when you know what to do.
Use Hub when you need to understand what happened, what matters, and where to look next.

## Architecture

Mainline stores data in two places, both in your git remote:

1. **Per-actor logs** (git branches) — Each developer has their own append-only event log: `refs/heads/_mainline/actor/<id>`. This stores intent metadata. Only the actor writes to their own log.
2. **Pin notes** (git notes) — When code is merged to main, a note on the merge commit links it to the intent: `refs/notes/mainline/intents`. Anyone can write a pin note when they confirm a merge.

These serve different purposes: the actor log is what you've claimed; the pin note is what's actually on main. Mainline's view is computed from both.

```
Per-actor logs                 Pin notes
refs/heads/                    refs/notes/
_mainline/actor/<id>           mainline/intents
       │                              │
       │  IntentSealedEvent           │  CommitNote
       │  IntentAbandonedEvent        │   { intents: [...],
       │  CheckJudgmentEvent          │     reverts: [...],
       │  ...                         │     via: pin_auto,
       │                              │     match_strategy: tree_hash }
       └────────────┬─────────────────┘
                    ▼
              MainlineView
       (cached at .ml-cache/views/mainline.json,
        rebuilt by every `mainline sync`)
```

## Concepts

### Intent

A piece of work — its *what*, *why*, and the subsystems / files it touches. Lifecycle:

```
drafting → sealed_local → proposed → merged → reverted
   │            │            │
   ├─ abandoned ┤  abandoned  │  abandoned
   └─ superseded┴─ superseded ┴─ superseded
```

Terminal states (`abandoned`, `superseded`, `reverted`) have no outgoing transitions.

### Turn

A single recorded fragment of work within an intent — what changed and why, captured by `mainline append`.

### Semantic Fingerprint

Generated at seal time. Lists subsystems, files, API changes, behavioural changes, tags. Used for phase 1 conflict screening. Drafts (no SealResult yet) get a *partial* fingerprint inferred from the goal and turn descriptions, scored at a slightly looser threshold.

### Phase 1 / Phase 2 check

| | Phase 1 | Phase 2 |
|---|---|---|
| Who runs it | Mainline (auto on sync + seal) | Agent (manual `check --submit`) |
| What it does | Weighted Jaccard overlap of fingerprint dimensions | Reads full summaries, judges semantic conflict |
| When it fires | Every sync, every seal | When a reviewer or author chooses |
| Output | `[check:~]` marker in log | `[check:ok]` / `[check:!]` / `[check:human?]` |
| Latency | < 100 ms | seconds (LLM call) |

### Pin

Pinning a `CommitNote` to a main-branch commit is what marks the intent **merged**. Pins are written by either:

- `mainline sync` — runs the strategy cascade (`tree_hash → commit_hash → goal_text`) automatically after every rebuild; tree hash matches GitHub squash merges with near-100 % reliability
- `mainline merge` (if you use it) — at squash time
- `mainline pin <intent> <commit>` — manual escape hatch when no heuristic matches

## Daily commands

These are the commands a human or agent will actually run.

The three intent-inspection commands form a clean trichotomy:

| Command | Purpose |
|---|---|
| `mainline log` | List intents across actors |
| `mainline show <id>` | Show the structured conclusion of an intent |
| `mainline trace <id>` | Show the internal timeline of an intent |

`log` answers *"what intents exist?"*, `show` answers *"what did this intent decide?"*, `trace` answers *"how did this intent unfold?"*.

Full daily set:

| Command | Use |
|---|---|
| `mainline init` | Initialise mainline in this repository |
| `mainline status` | Current intent + sync staleness + counts + coverage rollup |
| `mainline start "..."` | Start an intent on the current branch |
| `mainline append "..."` | Record a turn against the active intent (see [Turns and intent history](#concepts) for what turns are and aren't) |
| `mainline seal --prepare` | Generate the seal-prepare package (JSON) |
| `mainline seal --submit` | Submit a SealResult; auto-syncs and runs phase 1. Use `--offline` to skip the network step. `--allow-dirty` to bypass the worktree-clean check (recorded in audit trail). |
| `mainline abandon <id>` | Drop a drafting/sealed/proposed intent — drafts deleted, sealed/proposed get an abandon event published to the team |
| `mainline sync` | Fetch remote state, rebuild views, **auto-pin merged commits**, surface new conflicts |
| `mainline log` | Intent history with author, time, and `[check:?\|~\|ok\|!\|human?]` |
| `mainline show <id>` | Full intent detail (decisions, risks, fingerprint) |
| `mainline trace <id>` | Turn timeline (start/append/seal/abandon/supersede with elapsed time) |
| `mainline gaps` | List uncovered commits on main with reversibility-ranked rescue options |
| `mainline context --current` | Relevance-ranked prior intents for the active branch + diff-vs-main (read this BEFORE grepping) |
| `mainline context --files <p>` | Same retrieval, scoped to specific files |
| `mainline context --query "..."` | Same retrieval, keyword-driven |
| `mainline lint [<id>]` | Advisory seal-quality checks (boilerplate, missing decisions, broken refs). Never blocks. |
| `mainline hub open` | Build + open a static HTML site over the local intent view (humans, not agents) |
| `mainline check --prepare` | Phase 2 task package; auto-syncs first |
| `mainline check --submit` | Submit phase 2 judgment; result surfaces in log column |
| `mainline doctor --setup` | Verify installation: refspecs, identity, .gitignore; report optional AGENTS.md policy state |
| `mainline init --rewire` | Re-apply remote refspec config + notes.displayRef + .gitignore (use after adding origin post-init) |

All commands accept `--json`. The persistent `--no-sync` flag opts a command out of the auto-sync wrapper.

## Advanced commands

You usually don't need these. Documented for completeness.

| Command | When you'd want it |
|---|---|
| `mainline pin <intent> <commit>` | Manual escape hatch when sync's auto-pin cascade misses (rebased history, cherry-picks across forks, CI scripting). Two args required — there's no batch mode; sync covers that. |
| `mainline merge --intent <id>` | Squash + write note in one step. Use when there's no PR system or you need it inside an automation pipeline. |
| `mainline list-proposals` | Browse proposed intents across the team |
| `mainline pr-description --intent <id>` | Generate PR description markdown |
| `mainline publish --intent <id>` | Push actor log explicitly (usually automatic in seal) |
| `mainline thread {new,list,close}` | Group multiple intents into a named thread |
| `mainline canonical-hash <id>` | Debug: compute the canonical hash of an intent |

## Agent hooks (opt-in)

Agents like Cursor (today; Codex / Claude Code reserved) expose
session/turn lifecycle hooks. `mainline hooks` plugs into those as a
**context provider** — it runs the two mechanical operations the
agent would otherwise have to remember (`sync` + `status`) and
injects the snapshot into the agent's system context. It does **not**
drive the rest of the workflow.

```bash
mainline hooks list-agents             # what's supported
mainline hooks install --agent cursor  # writes .cursor/hooks.json
mainline hooks status                  # show installed agents + dispatcher toggles
mainline hooks uninstall --agent cursor
mainline hooks disable                 # soft-disable without uninstalling
mainline hooks enable
```

What hooks DO at each event (cursor today, others reserved):

| Hook event | mainline action |
|---|---|
| `session_start` | `mainline sync` + `mainline status`; cursor receives the snapshot as `additional_context` so the agent starts every session knowing the team state without an extra CLI call |
| `before_submit_prompt` / `stop` / `subagent_stop` / `session_end` | webhook fan-out only; the dispatcher does NOT touch the engine |

What hooks deliberately do NOT do: deciding when to `mainline start`,
what the goal text should be, when to `mainline append`, what to
write in the append, building the seal fingerprint, or judging
phase-2 conflicts. Those are LLM judgments and the agent stays the
sole source of truth for them — exactly as the Mainline skill specifies.
Hooks installed or not, the contract above never changes.

Per-toggle controls live in `.mainline/config.toml` under `[hooks]`
(`enabled`, `auto_sync_on_session_start`); everything is fail-soft
(a missing `mainline` binary never breaks the agent). Read-modify-
write into `.cursor/hooks.json` preserves any user-managed entries —
only the entries Mainline owns are added / removed, never the whole
file.

## Webhook subscriptions

When an intent is sealed, a sync surfaces a conflict, or a phase-2
check is judged, mainline emits a typed domain event. `mainline
webhook` lets external services (notification centers, dashboards,
auditing pipelines) subscribe:

```bash
mainline webhook add https://hooks.example.com/mainline \
  --events intent_sealed,conflict_detected,sync_completed \
  --secret '$ENV:WEBHOOK_SECRET'
mainline webhook list
mainline webhook test <id>             # send a synthetic event
mainline webhook retry                 # re-queue past failures
mainline webhook remove <id>
```

The single quotes are intentional: Mainline stores the literal
`$ENV:WEBHOOK_SECRET` reference in committed config and resolves the
environment variable only at delivery time.

Delivery is fire-and-forget: each event is serialized into
`.ml-cache/webhook-queue/`, then a detached subprocess (`mainline
__webhook-dispatch`) does the actual HTTP POST so the foreground CLI
never blocks on network. Failures retry with exponential backoff and
are persisted as `*.failed.json` for `mainline webhook retry`.

Privacy: only domain events are emitted. Prompt content from the
agent is intentionally **never** included in webhook payloads.
Payloads are HMAC-SHA256-signed using the per-subscription secret
(header: `X-Mainline-Signature: <hex>`).

## Configuration

`.mainline/config.toml` is committed to the repo (team-wide); `.mainline/local.toml` holds per-actor identity and is gitignored.

Most teams never edit these. The settings worth knowing:

```toml
[check]
phase1_threshold = 0.10              # below this, fingerprint pairs ignored
                                     # 0.10 chosen from rc4 dogfood; calibrate
                                     # per team

[sync]
freshness_seconds = 300              # auto-sync wrapper short-circuits within
                                     # this window
stale_threshold_seconds = 86400      # `mainline status` flags (stale) past this
auto_check_after_sync = true         # phase1 runs over the delta of new
                                     # remote intents on every sync

[merge]
strategy = "squash"                  # only consulted by `mainline merge`
```

## Performance tips

`mainline sync` is bounded by `git fetch` network latency (~3s on SSH to GitHub). To cut repeat-sync latency to ~1s, enable SSH connection multiplexing:

```ssh-config
# ~/.ssh/config
Host github.com
  ControlMaster auto
  ControlPath ~/.ssh/sockets/%r@%h-%p
  ControlPersist 600
```

```bash
mkdir -p ~/.ssh/sockets
```

The first sync still pays the full SSH handshake; subsequent syncs within the `ControlPersist` window reuse the tunnel and skip the ~2s key exchange.

`mainline doctor --setup` will flag this if it's not configured.

## FAQ

**Q: Is Mainline a replacement for RAG or grep?**

No. RAG retrieves semantically similar code. Grep verifies what code exists right now. Mainline retrieves the historical engineering intent behind the code. A good agent workflow is: `mainline context` → inspect current code → edit → seal new intent. Mainline should run before broad code search, not instead of code verification.

**Q: How is Mainline different from session-memory tools?**

Session-memory tools record prompts, responses, snapshots, tool calls, or code diffs from AI coding sessions. They help you replay, rollback, or inspect how a change happened. Mainline records the engineering intent that should guide future work: why the change exists, what decisions were made, what risks were accepted, which anti-patterns future agents must avoid, and whether the intent was merged, abandoned, superseded, or reverted. Session history is useful evidence. Mainline intent is durable working memory for future agents and reviewers.

**Q: Does Mainline record AI sessions?**

No. Mainline does not capture transcripts, tool calls, token usage, or session timelines. It can attach optional references to real external materials (session URLs, issues, PRs, docs, CI runs), but references support the intent — they don't replace it. The sealed intent remains the durable decision record.

**Q: Why not just use commit messages or PR descriptions?**

Commit messages are short and final-state oriented. PR descriptions are review-time artifacts. Both are easy to lose, rewrite, or skip. Mainline intents are git-backed, queryable, lifecycle-aware records. They can be abandoned, superseded, inherited by files, retrieved before editing, and shown to agents as context.

**Q: Is Mainline a productivity dashboard?**

No. Mainline does not rank developers by intent count, velocity, or productivity. Hub is designed around action signals: work needing review, inherited constraints, decision hotspots, important decisions, lifecycle signals, and coverage gaps. The goal is intent clarity and safer engineering, not surveillance.

**Q: When should agents use Mainline?**

Before non-trivial changes: architecture changes, refactors, migrations, deletions, auth/billing/permissions/data-model work, cross-file behavior changes, "can we delete this?" questions, "was this tried before?" questions. For trivial typo or formatting fixes, Mainline may be unnecessary.

**Q: Why is there no `mainline merge` in the quick start?**

GitHub's merge button is the supported default. After a merged PR, the next `mainline sync` will find the squash commit by tree hash and auto-pin the intent. `mainline merge` is for non-PR pipelines only.

**Q: My `mainline log` shows `[check:?]` for everything new — what should I do?**

Nothing. `?` means "no overlap detected, no judgment requested". You only act on `~` (phase 1 spotted overlap) or `!` (phase 2 confirmed conflict).

**Q: Phase 1 vs phase 2 — when do I run phase 2?**

Only when you see `[check:~]` on an intent and want to know whether the overlap is a real conflict. `mainline check --prepare --intent <id>` produces a task package; the agent reads it and submits a judgment via `mainline check --submit`.

**Q: Do I need to run `sync` manually?**

Yes — `sync` is now the single command that drives the team-aware loop: it fetches, rebuilds the view, **auto-pins merged commits**, runs phase 1 conflict detection on the delta, and writes the staleness record. `seal --submit` and `check` auto-sync internally; read-only displays (`log`, `status`, `context`) skip the network to stay snappy.

**Q: What happens if the heuristics pin the wrong commit?**

Use `mainline pin <intent> <commit>` to overwrite the pin manually. The note is updated, not duplicated; existing intents on the same commit are preserved.

**Q: Can two intents land on the same main commit?**

Yes. `CommitNote.intents` is a list. Mainline's `upsertCommitNote` merges new intent references into the existing note on a commit instead of overwriting.

## Specs

Mainline is working toward an open format for engineering intent records.
These specs are **v0.1-draft** — experimental, subject to change, and
seeking feedback from design partners.

| Spec | What it defines |
|---|---|
| [Intent Record Spec v0.1](docs/specs/intent-record-v0.md) | The record format: fields, lifecycle, schema, constraints taxonomy, git storage model. |
| [Agent Context Protocol v0.1](docs/specs/agent-context-protocol-v0.md) | How agents should consume intent records: retrieval modes, behavior requirements, pre-edit checklist. |
| [Eval Fixture Spec v0.1](docs/specs/eval-fixtures-v0.md) | How to test whether intent-first agents actually avoid mistakes: fixture format, scoring methodology, catalog. |

## Related tools and boundaries

Mainline is part of a broader ecosystem of tools for AI-assisted engineering.
The difference is the unit of memory.

| Category | What it remembers | Mainline boundary |
|---|---|---|
| RAG / code indexing | Similar code snippets and repository context | Mainline retrieves intent before code search. |
| Grep / agentic code search | Fresh, exact code evidence | Mainline tells the agent what historical constraints to check before reading code. |
| AI provenance tools | Which AI produced which code, from which prompt/session | Mainline does not do line attribution; it records engineering intent. |
| Session-memory tools | Prompts, responses, snapshots, tool calls, code diffs | Mainline can link sessions as references, but keeps the sealed intent as the durable decision record. |
| PR / issue trackers | Review discussion, task state, project workflow | Mainline captures the engineering why and lifecycle of intent, not general project management. |

These tools can be complementary. If your team already stores sessions or checkpoints elsewhere, Mainline can link them as references on sealed intents.

## Storage layout

```
.mainline/
  config.toml                    # Team-wide config (committed)
  local.toml                     # Per-actor identity (gitignored)

.ml-cache/                       # Local-only cache (gitignored)
  identity.json                  # Actor identity
  drafts/                        # Active drafts + turn streams
  views/
    mainline.json                # Materialised IntentView
    proposed-index.json          # Fast lookup of proposed-only
    last-sync.json               # Sync staleness record
    phase1-warnings.json         # Snapshot of current phase 1 pairs
  threads/
  sessions/

git refs (in your remote):
  refs/heads/_mainline/actor/<id>     # Per-actor append-only event log
  refs/notes/mainline/intents          # Pin notes on main commits
```

## Development

```bash
# Build
go build -o mainline .

# Inner-loop test (skips PBT files, ~15s)
make quick-test

# -short mode runs rapid PBT at 20 samples each (~2m)
make test

# Full PBT coverage (100 samples each, used in CI)
make test-pbt

# Benchmarks
make bench

# Lint
make lint
```

### Property-based testing

Core subsystems are covered by property-based tests (PBT) that verify
invariants across randomly generated inputs rather than hand-picked cases:

| Area | Properties |
|------|-----------|
| `rebuildView` state machine | event replay determinism, status transitions, idempotency |
| Pin cascade | strategy priority, commit coverage, squash-merge handling |
| `SealSubmit` | snapshot contract, fingerprint completeness, conflict detection |
| `detectSealedConflicts` | symmetry, self-no-conflict, overlap monotonicity |

Run `make test` for rapid PBT (20 samples each) or `make test-pbt` for full
coverage (100 samples, used in CI).

## Project structure

```
mainline/
├── main.go                    # Entry point
├── internal/
│   ├── domain/                # Pure types: Intent, Turn, Event,
│   │                          #   Fingerprint, ConflictPair, LastSync,
│   │                          #   Phase1WarningsCache
│   ├── core/                  # Canonical JSON, ID generation, validators
│   ├── gitops/                # git CLI wrapper (notes, refs, plumbing)
│   ├── storage/               # File I/O for .ml-cache and .mainline
│   ├── engine/
│   │   ├── engine.go          # Init, Status (with sync staleness)
│   │   ├── intent.go          # Start, Append, Show, Abandon
│   │   ├── seal.go            # SealPrepare, SealSubmit (auto sync+check)
│   │   ├── sync.go            # Sync, view rebuild, normaliseVia
│   │   ├── merge.go           # Pin (auto + explicit), Merge (advanced)
│   │   ├── conflict.go        # Phase 1 scoring, partial fingerprints
│   │   ├── check.go           # Phase 1 prepare, Phase 2 submit
│   │   ├── notes.go           # upsertCommitNote (multi-intent merge)
│   │   └── query.go           # Log, Context, ListProposals
│   ├── agent/                 # Agent adapter interface
│   └── cli/                   # cobra commands; PersistentPreRun is the
│                              #   auto-before-command sync wrapper
├── docs/                      # Eval reports and public-facing docs
├── docs_for_ai/               # Historical specs and agent-facing notes
└── .github/workflows/ci.yml
```

## Roadmap

The current implementation is on the **v0.3** line: coverage invariants,
seal snapshot evidence, auto-pin on sync, context reliability, hub export,
and eval reporting are already part of the working product.

| Milestone | Focus | Status |
|---|---|---|
| rc1–rc2 | Initial spec + actor log + trailers | Superseded |
| rc3 | git notes replaces commit-message trailers | Implemented |
| rc4 | `pin` (was `reconcile`) with strategy cascade; `upsertCommitNote`; phase1 PBT | Implemented |
| rc5 | Conflict detection on sync + seal; auto-before-command sync; status staleness; AGENTS.md v3 | Implemented |
| rc6 | `[check:X]` cascade with phase 1 / phase 2 priority; per-intent phase 1 cache; column drops on terminal status | Implemented |
| v0.2 | Drop deprecated `reconcile` alias; auto-pin on sync (no separate `pin` step in daily use; manual `pin <intent> <commit>` retained as fallback) | Implemented |
| v0.3 | Coverage model, seal snapshot contract, context reliability, eval reporting, hub export | Implemented |
| v0.4 | Release packaging, public security process, CI hardening, hosted docs polish | In progress |
| v0.5 | Reviewer dashboards; multi-repo intent threading | Planned |

## Community and security

- Contributing: [CONTRIBUTING.md](./CONTRIBUTING.md)
- Security reporting: [SECURITY.md](./SECURITY.md)
- Bug reports and feature requests: [GitHub issue templates](./.github/ISSUE_TEMPLATE/)

## License

MIT. See [LICENSE](./LICENSE).
