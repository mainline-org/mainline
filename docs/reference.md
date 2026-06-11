# Mainline Reference

This reference keeps the operational detail out of the README. Start with the
short overview in [README.md](../README.md), then use this page when you need
install variants, protocol details, command behavior, storage layout, or
development commands.

## Install

Choose one install path:

1. Install script: recommended for macOS and Linux users.
2. GitHub Releases: download and verify a specific prebuilt archive.
3. `go install`: build from source with Go 1.22+.

### macOS / Linux Install Script

```bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | bash
```

The installer downloads the latest GitHub Release archive for your platform,
verifies it against `checksums.txt`, and installs `mainline` into the first
writable PATH directory among `/usr/local/bin`, `/opt/homebrew/bin`, and
`~/.local/bin`.

Pin a version or choose an install directory with environment variables:

```bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | MAINLINE_VERSION=v0.4.2 bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | MAINLINE_INSTALL_DIR="$HOME/.local/bin" bash
```

The script supports macOS and Linux on `amd64` and `arm64`. Windows users should
download a prebuilt archive from GitHub Releases.

### GitHub Releases

Download a prebuilt binary from
[GitHub Releases](https://github.com/mainline-org/mainline/releases/latest).
Each release includes platform archives plus `checksums.txt` for verification.

Archive names follow this pattern:

```text
mainline_<version>_<os>_<arch>.tar.gz
mainline_<version>_windows_amd64.zip
```

### Go Install

```bash
go install github.com/mainline-org/mainline@latest
```

Requires Go 1.22 or newer. Use `@main` only when you explicitly want the current
unreleased development version:

```bash
go install github.com/mainline-org/mainline@main
```

### Build From Source

```bash
git clone https://github.com/mainline-org/mainline
cd mainline
go build -o mainline .
```

Verify setup at any time:

```bash
mainline doctor --setup
```

## Getting Started With An Agent

One-time setup per repository:

```bash
mainline init --actor-name "<your name>"
```

`mainline init` does three things:

1. Writes `.mainline/config.toml` and configures Git refspecs.
2. Installs the Mainline skill, which is the complete workflow manual for agents.
3. Installs repo-local hooks for supported agents such as Cursor, Claude Code,
   and Codex.

Fresh hook config files created by `init` are kept clone-local through
`.git/info/exclude` so they do not appear in the initial setup commit. If a repo
already tracks an agent hook file, Mainline preserves that convention and stages
the merged hook update with the rest of the init setup.

At every supported session start, hooks run `mainline sync` and
`mainline status`, then inject the snapshot into the agent's context. The agent
still makes the semantic decisions: when to start, what to append, how to seal,
and whether a warning is a real conflict.

Refresh AGENTS.md guidance with `mainline agents update`. Refresh the globally
installed Mainline skill separately with `npx --yes skills update mainline
--global --yes` (or rerun the matching `skills add` command). `mainline init
--rewire` repairs repo setup and does not reinstall skills.

The distribution surfaces are intentionally split: AGENTS guidance carries the
repo-local runtime contract, while the global skill carries the full workflow
manual. Updating one does not imply the other was refreshed.

When you adopt Mainline in an existing repository, `mainline init` records the
current `main` HEAD as the coverage baseline. Commits at or before that point
show as skipped pre-Mainline history. Future commits still need normal intent
coverage. Important old commits can be explained retroactively:

```bash
mainline start --commits <sha> "<why>"
```

For teams that want explicit repo-level policy, `mainline agents install` writes
a lightweight `AGENTS.md` pointer. This is optional.

Intent records travel through Git refs and Git notes. Use `mainline log`,
`mainline show <id>`, or `mainline hub open` to inspect them.

## Agent Protocol Contract

Mainline's core asset is a behavior contract for coding agents.

- Read before writing: retrieve repo intent before non-trivial edits.
- Record meaning, not keystrokes: append decisions, pivots, completed slices,
  and validation that changes confidence.
- Promote durable signals explicitly: constraints, risks, and follow-ups use
  dedicated commands instead of being buried in prose.
- Recover conservatively: dirty state, stale sync, branch drift, parse failure,
  and conflict warnings stop silent progress.
- Leave reviewable intent: a reviewer should be able to compare the
  implementation against the stated why, decisions, validation notes, and
  explicit signals.

### When Agents Must Call Context

Agents should retrieve Mainline context before:

- architecture changes, refactors, migrations, deletions, auth, billing, data
  model, permissions, release/CI, and cross-file behavior changes;
- questions like "can we delete this?", "why is this here?", or "was this tried
  before?";
- changes touching files with explicit constraints or important prior lifecycle
  warnings.

Agents can skip Mainline for typo fixes, formatting-only edits, one-line obvious
syntax fixes, or read-only inspection where the user explicitly asks to look at
one file.

### What Agents Run

```bash
mainline preflight --json
mainline start "<the user's goal>" --json
mainline append "<meaningful turn>" --json
mainline seal --prepare --json > .ml-cache/seal.json
mainline seal --submit --json < .ml-cache/seal.json
```

`preflight` is the readiness and stop-line gate. It tells the agent whether to
continue, inspect overlaps, or stop before lifecycle advancement. Read-only
diagnosis or proposal-only work can stop after read-only inspection; it should
not run `start` until the task crosses into non-trivial edits or another durable
engineering record. `start` then claims a real unit of engineering work.
`append` records meaningful progress. `seal --prepare` freezes the evidence
that will be submitted. `seal --submit` records the final intent and surfaces
lint or conflict summaries.

Review autonomy may push a non-main branch and open or update a PR. It never
authorizes pushing `main`, merging, releasing, or deploying.

Append at the granularity of engineering meaning: a design choice, a completed
slice, a pivot, or validation that changes confidence. Do not append every shell
command.

If the agent reads a relevant explicit constraint, it should say what it will not
do and why. Example: "I will not remove the legacy `/oauth` middleware because
OAuth callbacks still require session state."

### Recovery Rules

- Dirty worktree: if dirty files are not clearly owned by the current task, stop
  and ask before appending or sealing.
- Allowed dirty worktree: name the dirty files and explain why they are safe to
  carry.
- Sync stale or branch drift: sync or re-run prepare before sealing.
- Seal parse or lint error: fix the SealResult and re-submit.
- Conflict warning: surface the warning. Run `mainline check` or ask for human
  judgment when the overlap is semantically real.
- Constraint conflict: preserve the constraint, intentionally change it with
  reviewer attention, or stop.

### Review Quality

A trustworthy sealed intent has concrete `what` and `why`, decisions with
rationale, rejected alternatives when relevant, specific files or subsystems,
validation notes, and explicit acknowledgement of inherited constraints.

The reviewer question is: could a future agent read this intent before editing
and avoid the same mistake?

## Workflow Fit

Mainline keeps the normal GitHub/GitLab merge process.

```text
Author
  start -> append -> seal --submit
  open PR

Reviewer
  read intent in Hub, log, show, or PR description
  review code in the normal PR system

Merge
  use the normal merge button

Pin
  the next mainline sync links the merged commit to the intent
```

Mainline no longer provides its own merge command. Merge code through your
normal Git, GitHub, or GitLab workflow, then let `mainline sync` auto-pin the
merged commit to the sealed intent. If auto-pin misses an unusual history shape,
use the manual `mainline pin <intent> <commit>` fallback.

## What Mainline Records

A sealed Mainline intent contains:

- why the work exists,
- decisions and rationale,
- rejected alternatives,
- validation and review notes,
- explicit constraints, risks, and follow-ups,
- lifecycle state such as merged, abandoned, superseded, or reverted,
- references to issues, PRs, docs, CI runs, or external sessions,
- commit pins linking the intent to merged code.

Durable action signals live outside the default seal path:

- `mainline guard add`: human-confirmed constraints future agents must obey.
- `mainline risks add`: structured reviewer-facing failure modes.
- `mainline followups add`: explicit deferred work with provenance.

Mainline does not preserve every token of an AI session. It preserves the
decision record future agents and reviewers need.

## CLI And Hub

Mainline has two first-class surfaces:

- CLI for action: humans use `init`, `hub open`, `log`, `show`, and `gaps`;
  agents use `context`, `start`, `append`, and `seal`.
- Hub for reading: humans inspect pending work, file constraints, important
  decisions, risks, and coverage gaps.

Generate and open Hub locally:

```bash
mainline sync
mainline hub open
```

Generate a static export without opening a browser:

```bash
mainline hub export ./mainline-hub
open ./mainline-hub/index.html      # macOS
xdg-open ./mainline-hub/index.html  # Linux
```

For merged fork PRs, first ask whether the contributor also used Mainline. If
they did, import their actor log as an explicit upstream trust decision:

```bash
mainline publish --intent <id> --remote <fork>
```

Contributors use the command above when they can write to their fork but not to
the upstream Mainline remote. It pushes the actor log to the fork so an upstream
maintainer can import it.

```bash
mainline actor import --actor actor_jiangge --remote jiangge
```

`--remote` may be a configured Git remote or a fetchable URL. By default the
command fetches `refs/mainline/actors/<actor>/log` into
`refs/mainline/imports/<actor>/log`, validates that the events belong to the
expected actor, accepts the log into the upstream actor namespace, rebuilds the
view, and runs the normal auto-pin cascade. It also best-effort fetches fork
branches referenced by the accepted sealed intents into
`refs/mainline/imports/<actor>/branches/*`. Those hidden import refs keep the
contributor's original code commits and trees reachable in upstream, which is
what lets a squash/rebase PR pin by tree/content even when the original fork
commit is not in `main`.

When the upstream remote is configured, Mainline pushes the accepted contributor
actor ref, the maintainer's acceptance event, the imported fork branch refs, and
any new pin notes so the next `mainline sync` in another clone sees the same
author-sealed intent and the code objects needed to reason about it.

This command imports actor-log intent metadata and the referenced fork code
objects. It deliberately does not copy fork git notes into upstream because
notes are evidence about upstream main commits and should be written by upstream
pinning. If a referenced fork branch was deleted or cannot be fetched, the
actor-log accept can still succeed, but Mainline records object-fetch warnings
in the result/provenance and auto-pin may require a later manual fetch or
explicit `mainline pin`.

The accepted actor log must contain at least one author-sealed intent. Import
accepts only author seal/supersession/abandonment metadata from the fork actor
log; fork-side constraints, risks, follow-ups, check judgments, and merge
acknowledgements are rejected instead of being promoted into upstream team
signals. Upstream pin notes remain the source of merged evidence.

Maintainer backfills can coexist with later accepted contributor intents. A
backfill or explicit maintainer pin is the upstream maintainer's rescue record;
an accepted fork actor log is the contributor's author-sealed record. If both
refer to the same merge commit, the commit note may contain both intent
references. Coverage remains commit-level: one or more valid intent refs make
the commit covered, and accepting the contributor intent must not create a
review-queue conflict with the earlier backfill.

Upstream repositories can automate this maintainer-side import with
`.github/workflows/mainline-fork-pr-import.yml`. The workflow runs on
`pull_request_target` when a fork PR is closed as merged. It checks out the
trusted upstream base branch, initializes a `github-actions[bot]` Mainline
actor in the runner, retains the PR head object from `refs/pull/<number>/head`,
and runs:

```bash
mainline pr-import \
  --pr <number> \
  --fork-url <fork clone URL> \
  --head-ref <PR head branch> \
  --head-sha <PR head sha>
```

`pr-import` treats GitHub PR metadata only as locator information. It discovers
published actor logs under `refs/mainline/actors/*/log` in the fork, scores
sealed intents by matching `code_commit`, `code_tree`, and `git_branch`, and
imports only when there is a single best match. `imported` and
`already_imported` are both successful/idempotent outcomes. `no_actor_logs`,
`no_match`, and `ambiguous` leave upstream refs untouched and are reported in a
sticky PR comment so a maintainer can ask the contributor to publish or import
manually with `mainline actor import --actor ... --remote ...`.

The action needs `contents: write` to push Mainline refs/notes and
`pull-requests: write` / `issues: write` to upsert the PR comment. Because it
uses `pull_request_target`, the workflow must never checkout or execute fork
code. The shipped workflow checks out the upstream base ref and fetches fork
refs only as Git data for Mainline import.

When the contributor has no upstream-visible Mainline actor log, Hub can still
explain the merged PR with an explicit external-contribution file:

```bash
mainline hub export ./mainline-hub --external-contributions fork-prs.json
```

The file may be either an array or `{ "external_contributions": [...] }`.
Each row should carry GitHub PR metadata such as `author_login`,
`repository`, `pr_number`, `pr_url`, `merged_commit`, and `provenance`.
Hub currently treats these as imported/inferred contribution records, not as
author-owned Mainline intents. It forces `author_sealed=false`,
`not_author_sealed=true`, and `verified=false`, then links the row to any
upstream Mainline intent pinned to the same merge commit. This lets Hub explain
"who originally contributed this merged PR" without polluting actor counts,
review queues, coverage, or pin logic.

Do not use an empty `## Mainline Intent` section in a GitHub PR body as intent
evidence. PR descriptions are review-time artifacts; Mainline sealed intents
come from actor logs. GitHub PR imports must be labeled with provenance such as
`github_pr_imported` or `inferred` and must remain `not_author_sealed` unless a
real actor log has been accepted.

Hub output is generated local state and should not be committed.

### Publishing Hub With GitHub Pages

This repository ships a GitHub Pages workflow at
`.github/workflows/hub-pages.yml`. It builds the CLI, runs `mainline sync`,
exports Hub to `_site`, verifies the export has intent data, and deploys the
static artifact through GitHub Pages.

Use repository settings to set Pages source to **GitHub Actions**. The workflow
runs on `main` updates, manual dispatch, and a daily schedule. The scheduled run
is intentional: Mainline intent state also moves through Git refs and notes, so
the hosted Hub needs a refresh path that is not tied only to code diffs.

## Daily Commands

Intent inspection has three levels:

| Command | Purpose |
|---|---|
| `mainline log` | List intents across actors. |
| `mainline show <id>` | Show the structured conclusion of an intent. |
| `mainline trace <id>` | Show the internal timeline of an intent. |

Core human set:

| Command | Use |
|---|---|
| `mainline init` | Initialize Mainline in this repository. |
| `mainline hub open` | Build and open a static HTML site over the local intent view. |
| `mainline status --actionable` | Show top actionable items with why, risk, and next command. |
| `mainline log` | Show intent history with author, time, and check state. |
| `mainline show <id>` | Show intent detail, decisions, fingerprint, references, and explicit signals. |
| `mainline gaps` | List uncovered commits on `main` with rescue options. |

Reviewer and maintainer extras:

| Command | Use |
|---|---|
| `mainline status` | Current intent, sync staleness, counts, and coverage rollup. |
| `mainline sync` | Fetch remote state, rebuild views, auto-pin merged commits, and surface phase-1 overlap warnings. |
| `mainline lint [<id>]` | Advisory seal-quality checks. |
| `mainline guard add` | Interactively add a human-promoted constraint. |
| `mainline risks add` | Add a structured explicit risk. |
| `mainline followups add` | Add explicit deferred work. |
| `mainline check --prepare` | Prepare a phase-2 conflict review task package. |
| `mainline check --submit` | Submit phase-2 judgment. |
| `mainline doctor --setup` | Verify installation, refspecs, identity, `.gitignore`, and optional policy state. |
| `mainline init --rewire` | Re-apply refspec config, notes display refs, and `.gitignore` entries. |

All commands accept `--json`. The persistent `--no-sync` flag opts a command out
of the auto-sync wrapper.

## Advanced Commands

| Command | When you might use it |
|---|---|
| `mainline pin <intent> <commit>` | Manual escape hatch when auto-pin misses after rebase, cherry-pick, or unusual CI scripting. |
| `mainline list-proposals` | Browse proposed intents across the team. |
| `mainline pr-description --intent <id>` | Generate PR description markdown. |
| `mainline pr-import --fork-url <url> --head-ref <branch> --head-sha <sha>` | Automation helper for importing a fork contributor intent after a merged PR. |
| `mainline publish --intent <id>` | Push actor log explicitly. Add `--remote <fork>` when publishing fork-contributor metadata to a writable fork remote. |
| `mainline thread {new,list,close}` | Group multiple intents into a named thread. |
| `mainline canonical-hash <id>` | Debug the canonical hash of an intent. |

## Agent Hooks

Supported agents expose lifecycle hooks. `mainline hooks` uses those hooks as a
context provider: it runs mechanical `sync` and `status` operations and injects
the snapshot into the agent's context. Hooks do not decide when to start,
append, seal, or resolve conflicts.

```bash
mainline hooks list-agents
mainline hooks install --agent cursor
mainline hooks install --local-dev
mainline hooks install --bin ./mainline
mainline hooks status
mainline hooks uninstall --agent cursor
mainline hooks disable
mainline hooks enable
```

What hooks do:

| Hook event | Mainline action |
|---|---|
| `session_start` | Run `mainline sync` and `mainline status`; inject the snapshot into the agent context. |
| `before_submit_prompt`, `stop`, `subagent_stop`, `session_end` | Webhook fan-out only; the dispatcher does not touch the engine. |

Per-toggle controls live in `.mainline/config.toml` under `[hooks]`.

## Webhook Subscriptions

When an intent is sealed, a sync surfaces a conflict, or a phase-2 check is
judged, Mainline emits a typed domain event. `mainline webhook` lets external
services subscribe:

```bash
mainline webhook add https://hooks.example.com/mainline \
  --events intent_sealed,conflict_detected,sync_completed \
  --secret '$ENV:WEBHOOK_SECRET'
mainline webhook list
mainline webhook test <id>
mainline webhook retry
mainline webhook remove <id>
```

The single quotes are intentional: Mainline stores the literal
`$ENV:WEBHOOK_SECRET` reference in committed config and resolves the environment
variable only at delivery time.

Delivery is fire-and-forget. Events are serialized into
`.ml-cache/webhook-queue/`, then a detached subprocess performs the HTTP POST.
Payloads are HMAC-SHA256-signed with `X-Mainline-Signature`.

Prompt content from the agent is intentionally not included in webhook payloads.

## Configuration

`.mainline/config.toml` is committed team config. `.mainline/local.toml` holds
per-actor identity and is gitignored.

Common settings:

```toml
[check]
phase1_threshold = 0.10

[sync]
freshness_seconds = 300
stale_threshold_seconds = 86400
auto_check_after_sync = true

[mainline.coverage]
baseline_commit = "..."

[merge]
strategy = "squash"
```

Most teams rarely edit these by hand.

## Performance Tips

`mainline sync` is bounded by Git fetch latency. To reduce repeated SSH syncs,
enable SSH connection multiplexing:

```ssh-config
Host github.com
  ControlMaster auto
  ControlPath ~/.ssh/sockets/%r@%h-%p
  ControlPersist 600
```

```bash
mkdir -p ~/.ssh/sockets
```

`mainline doctor --setup` flags this when it is not configured.

## FAQ

**Is Mainline a replacement for RAG or grep?**

No. RAG retrieves semantically similar code. Grep verifies current code.
Mainline retrieves historical engineering intent before the agent searches or
edits code.

**How is Mainline different from session-memory tools?**

Session-memory tools record prompts, responses, snapshots, tool calls, or code
diffs. Mainline records durable engineering intent: why the change exists, what
decisions were made, which constraints future agents must obey, and whether the
intent was merged, abandoned, superseded, or reverted.

**Does Mainline record AI sessions?**

No. Mainline does not capture transcripts, tool calls, token usage, or full
session timelines. It can attach references to external materials, but the
sealed intent is the long-term decision record.

**Where is Mainline data stored?**

Durable team data lives in Git. Per-actor logs live under
`refs/mainline/actors/<id>/log`; merged-code pins live in Git notes under
`refs/notes/mainline/intents`. `.ml-cache/` is local-only cache.

Fork contributors are a trust-boundary case. An upstream repo only sees actor
logs that have been fetched and accepted into its Mainline view. Until that
exists, Hub can display an imported GitHub PR contribution with provenance
(`github_pr_imported` or `inferred`) and importer metadata, but it must not
present that row as a verified contributor-sealed intent.

**Why not use commit messages or PR descriptions?**

Commit messages are short and final-state oriented. PR descriptions are
review-time artifacts. Mainline intents are git-backed, queryable,
lifecycle-aware records that agents can retrieve before editing.

**Is Mainline a productivity dashboard?**

No. Mainline does not rank developers by intent count, velocity, or output. Hub
is organized around work needing review, inherited constraints, decision
hotspots, lifecycle signals, and coverage gaps.

**Do I need to run `sync` manually?**

Yes. `sync` fetches, rebuilds the view, auto-pins merged commits, runs phase-1
conflict detection on new remote intents, and writes the staleness record.
`seal --submit` and `check` auto-sync internally.

**What if auto-pin chooses the wrong commit?**

Use `mainline pin <intent> <commit>` to overwrite the pin manually. The note is
updated, not duplicated.

**Can two intents land on the same main commit?**

Yes. `CommitNote.intents` is a list. Mainline merges new intent references into
the existing note instead of overwriting it.

## Specs

Mainline is working toward an open format for engineering intent records.

| Spec | What it defines |
|---|---|
| [Intent Record Spec v0.1](specs/intent-record-v0.md) | Record fields, lifecycle, schema, constraints taxonomy, and Git storage model. |
| [Agent Context Protocol v0.1](specs/agent-context-protocol-v0.md) | Agent retrieval modes, behavior requirements, and pre-edit checklist. |
| [Eval Fixture Spec v0.1](specs/eval-fixtures-v0.md) | Fixture format, scoring methodology, and catalog for intent-first evals. |

## Related Tools And Boundaries

| Category | What it remembers | Mainline boundary |
|---|---|---|
| RAG / code indexing | Similar code snippets and repository context. | Mainline retrieves intent before code search. |
| Grep / agentic code search | Fresh, exact code evidence. | Mainline tells the agent which historical constraints to check. |
| AI provenance tools | Which AI produced which code from which prompt or session. | Mainline does not do line attribution; it records engineering intent. |
| Session-memory tools | Prompts, responses, snapshots, tool calls, code diffs. | Mainline can link sessions as references, but the intent is the durable record. |
| PR / issue trackers | Review discussion, task state, project workflow. | Mainline captures engineering why and lifecycle, not general project management. |

## Storage Layout

```text
.mainline/
  config.toml                    # Team-wide config, committed.
  local.toml                     # Per-actor identity, gitignored.

.ml-cache/                       # Local cache, gitignored.
  identity.json
  drafts/
  views/
    mainline.json
    proposed-index.json
    last-sync.json
    phase1-warnings.json
  threads/
  sessions/

git refs:
  refs/mainline/actors/<id>/log
  refs/notes/mainline/intents
```

## Development And Testing

```bash
go build -o mainline .
make quick-test
make test
make test-pbt
make bench
make lint
```

Property-based tests cover core invariants:

| Area | Properties |
|---|---|
| `rebuildView` state machine | Event replay determinism, status transitions, idempotency. |
| Pin cascade | Strategy priority, commit coverage, squash-merge handling. |
| `SealSubmit` | Snapshot contract, fingerprint completeness, conflict detection. |
| `detectSealedConflicts` | Symmetry, self-no-conflict, overlap monotonicity. |

Use `make quick-test` for the fast PR gate, `make test` for rapid PBT, and
`make test-pbt` for full coverage.

## Project Structure

```text
mainline/
  main.go
  internal/
    domain/       # Pure types.
    core/         # Canonical JSON, IDs, validators.
    gitops/       # Git CLI wrapper.
    storage/      # File I/O for .ml-cache and .mainline.
    engine/       # Init, status, intent, seal, sync, merge, conflict, query.
    agent/        # Agent adapter interface.
    cli/          # Cobra commands.
  docs/
  .github/workflows/ci.yml
```

## Roadmap

The current implementation is on the v0.4 line. Release packaging, CI
hardening, coverage invariants, seal snapshot evidence, auto-pin on sync,
context reliability, Hub export, and eval reporting are already part of the
working product. Remaining v0.4 work is public-launch polish. v0.5 focuses on
reviewer dashboards and multi-repo intent threading.

## Community And Security

- Contributing: [CONTRIBUTING.md](../CONTRIBUTING.md)
- Security reporting: [SECURITY.md](../SECURITY.md)
- Changelog: [CHANGELOG.md](../CHANGELOG.md)
- Bug reports and feature requests: [GitHub issue templates](../.github/ISSUE_TEMPLATE/)

## License Details

Mainline uses a layered licensing model. The local developer and agent surfaces
should be easy to adopt, embed, and standardize while hosted-service
infrastructure and brand rights stay protected.

| Area | Recommended terms | Purpose |
|---|---|---|
| CLI core | Apache-2.0 | Enterprise-friendly and platform-friendly adoption. |
| Agent skills, hooks, and adapters | Apache-2.0 | Let coding agents, IDEs, and automation platforms integrate Mainline freely. |
| SDKs and libraries | Apache-2.0 | Maximize ecosystem adoption and implementation reuse. |
| Intent Record Spec and Agent Context Protocol | Apache-2.0 | Allow compatible independent implementations and make Mainline vendor-neutral. |
| Docs, essays, and examples | CC-BY-4.0 or Apache-2.0 | Encourage copying, teaching, quoting, and propagation with attribution. |
| Logo, name, compatibility marks, and brand | Trademark reserved. | Prevent other projects or services from presenting themselves as official Mainline. |
| Hosted cloud, GitHub App, managed PR checks, and team dashboards | Commercial terms. | Keep hosted products separate from the local-first open surfaces. |
| Hosted search, indexing, analytics, and cloud infrastructure | Commercial / not part of open-source distribution. | Preserve the hosted service boundary. |

Repository data should not be sent to Mainline Cloud unless a user explicitly
connects a hosted service.
