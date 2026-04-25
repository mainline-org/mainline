# Mainline

**Distributed intent ledger for AI coding agents.**

Mainline coordinates multiple AI coding agents working on the same codebase by recording, checking, and merging their work intents. It provides a structured protocol for agents to declare what they're doing, detect semantic conflicts before they become merge conflicts, and produce rich PR descriptions automatically.

## Architecture

```
┌──────────────────────────────────────────────┐
│                  CLI (cobra)                  │
├──────────────────────────────────────────────┤
│                   Engine                      │
│  ┌─────────┐ ┌─────────┐ ┌────────────────┐ │
│  │  Init    │ │  Seal   │ │  Check (Phase1)│ │
│  │  Start   │ │  Sync   │ │  Fingerprint   │ │
│  │  Append  │ │  Merge  │ │  Overlap       │ │
│  │  Status  │ │ Publish │ │  Scoring       │ │
│  └─────────┘ └─────────┘ └────────────────┘ │
├──────────────────────────────────────────────┤
│  Core          │  Storage        │  GitOps   │
│  Validation    │  .ml-cache/     │  Plumbing │
│  Canonical JSON│  .mainline/     │  Trailers │
│  ID Generation │  Actor Logs     │  Diff     │
├──────────────────────────────────────────────┤
│               Domain Types                    │
│  Intent, Turn, Thread, Event, Fingerprint    │
└──────────────────────────────────────────────┘
```

## Quick Start

```bash
# Install
go install mainline@latest

# Initialize in your repo
cd your-repo
mainline init --actor-name "claude-agent-1"

# Start an intent
mainline start --goal "Add user authentication"

# Record work
mainline append "Implemented JWT middleware"
mainline append "Added login/logout endpoints"

# Seal (freeze code + generate summary)
mainline seal --prepare > seal-pkg.json
# ... agent generates SealResult ...
mainline seal --submit < seal-result.json

# Publish to team
mainline publish

# Check for conflicts
mainline check --prepare --intent int_abc12345 > check-pkg.json
# ... agent analyzes conflicts ...
mainline check --submit < check-result.json

# Merge
mainline merge --intent int_abc12345
```

## Commands

| Command | Description |
|---------|-------------|
| `mainline init` | Initialize mainline in current repository |
| `mainline status` | Show current status |
| `mainline start --goal "..."` | Start a new intent |
| `mainline append "description"` | Record a turn |
| `mainline seal --prepare` | Generate seal prepare package (JSON) |
| `mainline seal --submit` | Submit seal result from stdin |
| `mainline sync` | Fetch remote state and rebuild views |
| `mainline publish` | Push actor log to remote |
| `mainline check --prepare` | Generate conflict check package |
| `mainline check --submit` | Submit check judgment result |
| `mainline merge --intent ID` | Merge intent into main |
| `mainline log` | Show intent history |
| `mainline show ID` | Show intent details |
| `mainline context` | Show full context (for agent consumption) |
| `mainline thread new NAME` | Create a new thread |
| `mainline thread list` | List threads |
| `mainline thread close NAME` | Close a thread |
| `mainline pr-trailer --intent ID` | Output PR trailer |
| `mainline pr-description --intent ID` | Generate PR description |
| `mainline reconcile` | Acknowledge merged intents |
| `mainline list-proposals` | List proposed intents |
| `mainline canonical-hash ID` | Compute canonical hash |

All commands support `--json` for machine-readable output.

## Key Concepts

### Intent Lifecycle (State Machine)

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

### Actor Log
Each agent has an append-only log stored as git objects (not in the working tree). Events are committed via `git hash-object` / `commit-tree` / `update-ref`, synced via `git push/fetch` of custom refs under `refs/mainline/actors/`.

### Semantic Fingerprint
A structured summary of what an intent touches: subsystems, files, API changes, behavioral changes, etc. Used for fast conflict pre-screening (Phase 1 check).

### Phase 1 Check (Deterministic)
Computes weighted Jaccard similarity across fingerprint dimensions to find suspicious pairs of intents. Pairs above the threshold are forwarded to an agent for deeper semantic analysis (Phase 2).

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
    "suggested_actions": ["mainline start --goal 'your goal'"]
  }
}
```

## Storage Layout

```
.mainline/           # Committed to repo
  config.toml        # Team configuration
  local.toml         # Local config (gitignored)

.ml-cache/           # Gitignored, local only
  identity.json      # Actor identity
  drafts/            # Draft intents + turns JSONL
  views/             # Materialized views
    mainline.json
    proposed-index.json
  threads/           # Thread state
  sessions/          # Session state

refs/mainline/actors/<actor-id>/log   # Actor event log (git refs)
```

## Development

```bash
# Build
go build -o mainline .

# Test (with race detector)
go test -race ./...

# Benchmarks
go test -bench=. -benchmem ./...

# Verbose test
go test -v ./...

# Lint
golangci-lint run
```

## Project Structure

```
mainline/
├── main.go                    # Entry point
├── go.mod
├── internal/
│   ├── domain/                # Core types
│   ├── core/                  # Validation, canonical JSON, ID generation
│   ├── gitops/                # Git CLI wrapper
│   ├── storage/               # File I/O for .ml-cache and .mainline
│   ├── engine/                # Business logic
│   ├── agent/                 # Agent adapter interface (v0.1 stub)
│   └── cli/                   # Cobra commands
├── .github/workflows/ci.yml   # CI/CD
└── assets/                    # Templates
```

## License

MIT
