# Mainline

**Distributed intent ledger for AI coding agents.**

Mainline coordinates multiple AI coding agents working on the same codebase by recording, checking, and merging their work intents. It provides a structured protocol for agents to declare what they're doing, detect semantic conflicts before they become merge conflicts, and produce rich PR descriptions automatically.

## Architecture

Mainline stores data in two places, both in your git remote:

1. **Per-actor logs** (git branches) — Each developer has their own
   append-only event log: `refs/heads/_mainline/actor/<id>`. This
   stores intent metadata. Only the actor writes to their own log.

2. **Pin notes** (git notes) — When code is merged to main, a note
   on the merge commit links it to the intent: `refs/notes/mainline/intents`.
   Anyone can write a pin note when they confirm a merge.

These serve different purposes: the actor log is what you've claimed;
the pin note is what's actually on main. Mainline's view is computed
from both.

```
┌─────────────────────────────────────────────────────────┐
│  Per-actor logs           Pin notes                      │
│  refs/heads/              refs/notes/                    │
│  _mainline/actor/<id>     mainline/intents               │
│       │                          │                       │
│       │  IntentSealedEvent       │  CommitNote           │
│       │  IntentAbandonedEvent    │   { intents: [...],   │
│       │  CheckJudgmentEvent      │     reverts: [...],   │
│       │  ...                     │     via: pin_auto,    │
│       │                          │     match_strategy }  │
│       └────────────┬─────────────┘                       │
│                    ▼                                     │
│              MainlineView                                │
│       (cached at .ml-cache/views/mainline.json,          │
│        rebuilt by every `mainline sync`)                 │
└─────────────────────────────────────────────────────────┘
```

## How it fits your workflow

1. **Author**: Run your agent normally. It calls Mainline to record
   intent (`start` / `append`). When the task is done, it submits a
   `seal --submit`, which auto-syncs with the team and runs phase1
   conflict detection in one step.
2. **Reviewer**: Run `mainline sync` (or any auto-syncing command) to
   fetch team activity. Sync runs phase1 over new remote intents and
   warns about overlaps with your active drafts.
3. **Merge**: Use GitHub's merge button as usual.
4. **Pin**: Any later `mainline sync` (or any auto-syncing command)
   automatically pins the merged commit to its intent via tree-hash
   matching. Done.

You never need to use `mainline merge` for the GitHub PR workflow —
Mainline rides on top of your existing merge process rather than
replacing it.

## Quick Start

```bash
# Install
go install mainline@latest

# Initialize in your repo
cd your-repo
mainline init --actor-name "claude-agent-1"

# Start an intent
mainline start "Add user authentication"

# Record work
mainline append "Implemented JWT middleware"
mainline append "Added login/logout endpoints"

# Seal (freeze code + generate summary). Auto-syncs and runs phase1
# conflict check; conflicts appear in the JSON response as a
# `conflicts: [...]` array. Add --offline to skip the network step.
mainline seal --prepare > seal-pkg.json
# ... agent generates SealResult ...
mainline seal --submit < seal-result.json

# Open a PR on GitHub as usual; merge with the web UI.

# After merge, anyone runs:
mainline sync
# → tree-hash auto-pin links the squash commit to the intent.
# → view shows the intent as merged.
```

## Commands

| Command | Description |
|---------|-------------|
| `mainline init` | Initialize mainline in current repository |
| `mainline status` | Show current status, sync staleness, and active intent |
| `mainline start "..."` | Start a new intent on the current branch |
| `mainline append "..."` | Record a turn against the active intent |
| `mainline seal --prepare` | Generate seal-prepare package (JSON) |
| `mainline seal --submit` | Submit SealResult; auto-syncs + checks conflicts. `--offline` skips network. |
| `mainline sync` | Fetch remote state, rebuild views, surface new conflicts. Writes `.ml-cache/views/last-sync.json`. |
| `mainline publish` | Push actor log to remote (manual; usually automatic in seal) |
| `mainline check --prepare` | Generate phase2 conflict-check package (auto-syncs) |
| `mainline check --submit` | Submit CheckJudgmentResult; surfaces in `IntentView.LastCheck` |
| `mainline pin` | Auto-link merged commits to intents via tree_hash → commit_hash → goal_text cascade (auto-syncs) |
| `mainline pin <intent> <commit>` | Manually pin one intent to one commit |
| `mainline log` | Show intent history with author, time, and `[check:ok\|!\|?\|-]` marker |
| `mainline show <id>` | Show intent details, including LastCheck summary |
| `mainline context` | Full context dump for agent consumption (auto-syncs) |
| `mainline list-proposals` | List proposed intents (auto-syncs) |
| `mainline thread {new,list,close}` | Manage threads |
| `mainline pr-description --intent ID` | Generate PR description markdown |
| `mainline canonical-hash <id>` | Compute canonical hash of an intent |
| `mainline reconcile` | Deprecated alias of `mainline pin` |
| `mainline merge` | Advanced; squash-merge + write note in one step. Most teams should use the GitHub PR + auto-pin path instead. |

All commands support `--json` for machine-readable output. The persistent
`--no-sync` flag opts a command out of the auto-sync wrapper.

## Key Concepts

### Intent Lifecycle

```
drafting ──→ sealed_local ──→ proposed ──→ merged ──→ reverted
    │              │              │
    ├──→ abandoned ├──→ abandoned ├──→ abandoned
    └──→ superseded└──→ superseded└──→ superseded
```

Valid transitions:
- `drafting` → `sealed_local`, `abandoned`, `superseded`
- `sealed_local` → `proposed`, `abandoned`, `superseded`
- `proposed` → `merged`, `abandoned`, `superseded`
- `merged` → `reverted`
- Terminal states: `abandoned`, `superseded`, `reverted` (no outgoing transitions)

### Turn

A turn is a single work fragment within an intent recording what changed and why.

### Thread

A thread groups related intents, typically mapped 1:1 to a git branch.

### Per-actor log

Each agent has an append-only log stored as a custom git ref:
`refs/heads/_mainline/actor/<actor-id>`. Each event (sealed, abandoned,
superseded, check-judgment) becomes one commit on that ref carrying
a single `event.json` blob. Logs sync via `git fetch/push` of the ref
namespace. Only the owning actor writes to their own log.

### Pin note

A `CommitNote` (JSON) attached at `refs/notes/mainline/intents/<commit>`
binds one or more intents to a main-branch commit. Anyone can write a
pin note. The same note can carry multiple intents (squash of multiple
PRs) via `upsertCommitNote`, which read-modify-writes the existing
note rather than overwriting it.

### Auto-pin strategy cascade

`mainline pin` (formerly `reconcile`) walks each proposed intent and
tries to match it to a main commit by:

1. **tree_hash** — main commit's tree equals intent.code_commit's tree
   (squash merge preserves the tree byte-for-byte → near-100 % hit on
   GitHub web-UI merges).
2. **commit_hash** — main commit's hash equals intent.code_commit
   (fast-forward / no-ff merge).
3. **goal_text** — main commit message contains intent.goal verbatim
   (`mainline merge` writes this kind of message).

The first matching strategy wins. Each pin note records which
strategy fired (`match_strategy: "tree_hash"`), so audit can trace
how the link was established.

### Semantic Fingerprint

A structured summary of what an intent touches: subsystems, files,
API changes, behavioral changes, etc. Generated at seal time. Used
for fast conflict pre-screening (Phase 1).

### Partial Fingerprint

For drafts that have not yet been sealed, Mainline derives a partial
fingerprint from the goal text and turn descriptions (keyword set,
files-touched, path-derived subsystems). Sync uses these to warn
"the work you're drafting may overlap with what your teammate just
sealed", before the user even reaches `seal --prepare`.

### Phase 1 Check (Deterministic)

Computes weighted Jaccard similarity across fingerprint dimensions
(`subsystems` 0.25, `files` 0.30, `architecture` 0.15, `behavioral`
0.15, `api` 0.10, `tags` 0.05). Pairs above `phase1_threshold` (0.10
default) are returned to the agent for Phase 2 deep analysis. Partial
fingerprints use a separate floor (0.25) and are tagged with
`confidence: "low"`.

### Phase 2 Check (Agent)

The agent reads the Phase 1 task package, decides whether each pair
is a real semantic conflict, and submits a `CheckJudgmentResult`.
The latest judgment is materialised onto `IntentView.LastCheck` so
`mainline log` and `mainline show` can render it without re-reading
the actor log.

### Sync staleness

Every `mainline sync` writes `.ml-cache/views/last-sync.json`.
`mainline status` shows the elapsed time and flags `(stale)` past
24 hours (configurable via `[sync] stale_threshold_seconds`). The
auto-sync wrapper short-circuits when the last sync is within
`[sync] freshness_seconds` (5 minutes default).

## JSON Protocol

All commands support `--json` for structured output:

```json
{"ok": true, "data": { ... }}
```

Errors:

```json
{
  "ok": false,
  "error": {
    "code": "NO_ACTIVE_INTENT",
    "message": "no active intent on current branch",
    "recoverable": true,
    "suggested_actions": ["mainline start 'your goal'"]
  }
}
```

`mainline seal --submit --json` and `mainline sync --json` both carry
a `conflicts` (or `new_conflicts`) array when phase1 detects overlap
with team intents.

## Storage Layout

```
.mainline/                       # Committed to repo
  config.toml                    # Team configuration

.mainline/local.toml             # Local actor config (gitignored)

.ml-cache/                       # Gitignored, local only
  identity.json                  # Actor identity
  drafts/                        # Active draft intents + turns
  views/
    mainline.json                # Materialised IntentView cache
    proposed-index.json          # Proposed-only fast lookup
    last-sync.json               # Sync staleness record (rc5)
  threads/                       # Thread state
  sessions/                      # Session state

git refs (in your remote):
  refs/heads/_mainline/actor/<id>     # One per actor — append-only event log
  refs/notes/mainline/intents          # Pin notes on main commits
```

## Development

```bash
# Build
go build -o mainline .

# Inner-loop test (skips PBT files via -tags quick, ~15s)
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

## Project Structure

```
mainline/
├── main.go                    # Entry point
├── go.mod
├── internal/
│   ├── domain/                # Pure data types: Intent, Turn, Event,
│   │                          #   Fingerprint, ConflictPair, LastSync
│   ├── core/                  # Canonical JSON, ID generation, validators
│   ├── gitops/                # git CLI wrapper (notes, refs, plumbing)
│   ├── storage/               # File I/O for .ml-cache and .mainline
│   ├── engine/                # Business logic
│   │   ├── engine.go          #   Init, Status (with sync staleness)
│   │   ├── intent.go          #   Start, Append, Show, Abandon
│   │   ├── seal.go            #   SealPrepare, SealSubmit (auto sync+check)
│   │   ├── sync.go            #   Sync, view rebuild, normaliseVia
│   │   ├── merge.go           #   Pin (auto + explicit), Merge (advanced)
│   │   ├── conflict.go        #   Phase1 scoring, partial fingerprints
│   │   ├── check.go           #   Phase1 prepare, Phase2 submit
│   │   ├── notes.go           #   upsertCommitNote (multi-intent merge)
│   │   └── query.go           #   Log, Context, ListProposals
│   ├── agent/                 # Agent adapter interface
│   └── cli/                   # cobra commands; PersistentPreRun handles
│                              #   the auto-before-command sync wrapper
├── docs_for_ai/               # Spec patches (rc1 → rc5) + AGENTS.md
└── .github/workflows/ci.yml
```

## License

MIT
