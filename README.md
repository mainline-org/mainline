# Mainline

**Mainline is a git-native intent memory layer for AI-assisted engineering.**
It gives coding agents the historical *why* before they inspect the current *what*.

> 中文版本: [README.zh.md](./README.zh.md)

Stop your AI agent from silently undoing yesterday's decision, repeating an
abandoned approach, or stepping on a teammate's in-flight work. Mainline
records *why* each AI-driven change was made — decisions, risks, anti-patterns
— and surfaces that record to the next agent (or human) at the moment they
need it.

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

You don't memorise this — `AGENTS.md` (which Mainline writes into your repo on
init) tells the agent the protocol. Modern agents read `AGENTS.md` at every
session.

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

## If you use Cursor (or Claude Code / Codex)

You probably want hooks. One-time setup per repo:

```bash
mainline init --actor-name "<your name>"
mainline hooks install --agent cursor      # or: --agent claudecode  /  --agent codex
```

That's it. Now at every Cursor session start, Mainline:

1. Runs `mainline sync` (fetches the latest team intent).
2. Runs `mainline status` (active intent, sync staleness, suggestions).
3. Injects the snapshot into Cursor's system context as `additional_context`.

Your agent now sees fresh team state at every session start without you
typing anything. The agent itself drives the rest of the workflow (start /
append / seal / check) per `AGENTS.md` — Mainline is a context provider, not
a workflow driver.

If you don't use a supported hook agent, your AI tool reads `AGENTS.md`
manually and follows the same protocol — both paths work.

## What problem this solves

| Pain | Without Mainline | With Mainline |
|---|---|---|
| Agent re-removes the legacy `/oauth` middleware you kept on purpose | Silent rework, prod outage | Agent reads the anti-pattern and stops before the diff |
| You forgot why you chose JWT over sessions 3 weeks ago | `git log` doesn't carry decisions | `mainline show <id>` returns title / what / why / decisions / risks |
| Two agents on the same repo solving the same problem differently | Discovered at PR-review time | `mainline check` flags the overlap on `seal --submit` |
| New maintainer asks "why is this code like this?" | Slack archaeology | `mainline context --files src/auth/middleware.go` |
| You want to know which commits on `main` have no recorded intent | No signal | `mainline gaps` |

> **Does it actually work?** We ran a controlled eval: 8 scenarios × 3 seeds ×
> 2 modes. Code-first agents committed 9 violations; intent-first agents
> committed 0. The advantage is 100% reproducible on abandoned-approach and
> superseded-decision tasks. [Full report →](./docs/eval-results.md)

## Table of contents

- [Install](#install)
- [Eval: does intent-first actually help?](#eval-does-intent-first-actually-help)
- [Five-minute quick start](#five-minute-quick-start)
- [How it fits your workflow](#how-it-fits-your-workflow)
- [Architecture](#architecture)
- [Concepts](#concepts)
- [Daily commands](#daily-commands)
- [Advanced commands](#advanced-commands)
- [Agent hooks (opt-in)](#agent-hooks-opt-in)
- [Webhook subscriptions](#webhook-subscriptions)
- [Configuration](#configuration)
- [FAQ](#faq)
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

Yes. On 8 synthetic scenarios with 3 independent seeds (live LLM, not replay):

| Mode | Violations | Consistency |
|---|---|---|
| **Intent-first** | **0 across all seeds** | 0/8 fixtures fail |
| Code-first | 9 violations (3/seed) | 2/8 fixtures fail, 100% reproducible |

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
runs (driven by `AGENTS.md`, or auto-injected if you installed hooks).

```bash
# [you] one-time per repo
cd your-repo
mainline init --actor-name "alice"     # or: export MAINLINE_ACTOR_NAME first
# if you add a git remote later, run: mainline init --rewire

# [you, optional] one-time per repo if you use Cursor/Claude/Codex
mainline hooks install --agent cursor

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
| `mainline doctor --setup` | Verify installation: refspecs, identity, AGENTS.md, PR template, .gitignore |
| `mainline init --rewire` | Re-apply remote refspec config + AGENTS.md + PR template (use after adding origin post-init) |

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
sole source of truth for them — exactly as `AGENTS.md` specifies in
the no-hooks flow. Hooks installed or not, the contract above never
changes.

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

## FAQ

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
