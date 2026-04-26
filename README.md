# Mainline

**Distributed intent ledger for AI coding agents.**

Mainline records *why* each AI-driven change was made and surfaces conflicts between intents before they reach `main`. It rides on top of your existing git + PR workflow — no special merge command, no commit-message magic. Intent metadata lives in dedicated git refs that ship with `git push` and `git fetch`.

```
$ mainline log
int_d74f1892 [proposed] [check:~] 11:30 alice    Restructure README sections
int_1b37b496 [merged]              11:08 alice    Make [check:X] log column actually useful
int_1525611a [merged]              10:12 alice    rc5: conflict detection on sync+seal
int_ca0f10f0 [merged]              01:22 alice    Rename reconcile → pin
int_298b4476 [merged]              23:30 bob      Sync all actor logs
                          ↑
              "phase1 spotted an overlap with a teammate's intent —
               run `mainline check` to investigate"
```

## Table of contents

- [Why Mainline?](#why-mainline)
- [Install](#install)
- [Five-minute quick start](#five-minute-quick-start)
- [How it fits your workflow](#how-it-fits-your-workflow)
- [Architecture](#architecture)
- [Concepts](#concepts)
- [Daily commands](#daily-commands)
- [Advanced commands](#advanced-commands)
- [Configuration](#configuration)
- [FAQ](#faq)
- [Storage layout](#storage-layout)
- [Development](#development)
- [Project structure](#project-structure)
- [Roadmap](#roadmap)

## Why Mainline?

Two AI agents working on the same codebase will, by default, silently overwrite each other's intent. The git layer catches *file* conflicts at merge time but never the deeper "we're solving the same problem in opposite directions" case — which is exactly when AI parallelism turns into rework.

Mainline gives every change a structured **intent** — what's being done, why, what subsystem and files it touches — and runs a fast deterministic *phase 1* fingerprint check whenever new intents arrive. Surfaced overlaps are escalated to *phase 2*, where an agent reads the full summaries and judges whether the overlap is a real semantic conflict.

In one sentence: **catch intent conflicts before they reach a PR review**.

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

## Five-minute quick start

```bash
# 1. Initialise (once per repo)
cd your-repo
mainline init --actor-name "alice"
# If you add a git remote later (or this repo's remote was added after
# init), run `mainline init --rewire` to wire up notes / actor-log
# refspecs for cross-actor sync. `mainline doctor --setup` will tell
# you when that's needed.

# 2. While the agent works
mainline start "Add JWT auth"
# ... agent makes changes, commits ...
mainline append "Implemented JWT middleware"
mainline append "Added refresh-token rotation"

# 3. Seal at end of task — auto-syncs with team and runs phase1
mainline seal --prepare > seal.json
# agent fills in seal.json
mainline seal --submit < seal.json
# response includes a `conflicts: [...]` array if phase1 finds overlap

# 4. Open a PR on GitHub as usual; merge with the web UI

# 5. Anyone runs sync next
mainline sync
# tree-hash auto-pin links the squash commit to the intent
```

That's the whole loop. No special merge command required.

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

| Command | Use |
|---|---|
| `mainline init` | Initialise mainline in this repository |
| `mainline status` | Current intent + sync staleness + counts |
| `mainline start "..."` | Start an intent on the current branch |
| `mainline append "..."` | Record a turn against the active intent |
| `mainline seal --prepare` | Generate the seal-prepare package (JSON) |
| `mainline seal --submit` | Submit a SealResult; auto-syncs and runs phase 1. Use `--offline` to skip the network step. |
| `mainline sync` | Fetch remote state, rebuild views, **auto-pin merged commits**, surface new conflicts |
| `mainline log` | Intent history with author, time, and `[check:?\|~\|ok\|!\|human?]` |
| `mainline show <id>` | Full intent detail, including LastCheck summary |
| `mainline context` | State dump for agent consumption |
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
├── docs_for_ai/               # Spec patches (rc1 → rc5) + AGENTS.md
└── .github/workflows/ci.yml
```

## Roadmap

The current line is **v0.1-rc5** in spec, with the implementation tracking it.

| Milestone | Focus | Status |
|---|---|---|
| rc1–rc2 | Initial spec + actor log + trailers | Superseded |
| rc3 | git notes replaces commit-message trailers | Implemented |
| rc4 | `pin` (was `reconcile`) with strategy cascade; `upsertCommitNote`; phase1 PBT | Implemented |
| rc5 | Conflict detection on sync + seal; auto-before-command sync; status staleness; AGENTS.md v3 | Implemented |
| rc6 | `[check:X]` cascade with phase 1 / phase 2 priority; per-intent phase 1 cache; column drops on terminal status | Implemented |
| v0.2 | Drop deprecated `reconcile` alias; auto-pin on sync (no separate `pin` step in daily use; manual `pin <intent> <commit>` retained as fallback) | Implemented |
| v0.3 (planned) | GitHub Action for post-merge pin | Planned |
| v0.5 | Reviewer dashboards; multi-repo intent threading | Planned |

## License

MIT
