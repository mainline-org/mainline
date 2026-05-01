---
name: mainline
description: "Use for any coding-agent work in a Git repository that uses or may need Mainline intent tracking: before reading or editing code, fixing bugs, adding features, refactoring, deleting code, changing tests or CI, committing, pushing, opening PRs, reviewing intent conflicts, or setting up Mainline CLI/hooks/agent guidance."
---

# Mainline

This skill is the agent-facing Mainline integration. It should be sufficient
even when the repository does not yet have Mainline text in AGENTS.md and when
Mainline hooks are not installed.

Mainline records why AI-driven changes happen, connects those intents to code
commits, and surfaces semantic conflicts before PR review. Treat it as part of
the coding workflow, not as optional documentation.

If the current session already contains a `mainline:context` block injected by
Mainline hooks, treat that hook context as already loaded. Do not re-run generic
bootstrap context commands just to duplicate it. Run task-specific context
commands only when needed.

## Language Rule (load-bearing)

Match the user's language in everything you write into Mainline:
the goal text on `mainline start`, every `mainline append` turn,
the seal `summary.title` / `what` / `why` / `user_goal` / decisions
/ risks / anti_patterns / followups, and PR description prose. If
the user wrote in Chinese, seal in Chinese. English in, English out.
Mixed inputs → match the dominant language.

Why this matters: the seal record is the team's long-term memory.
A teammate reading `mainline show <id>` later must recognise the
work as theirs. Translating a Chinese task into an English seal
makes the corpus harder to read for the people whose memory it is.

Pass the user's substantive goal text through verbatim — `mainline start
"<goal>"` becomes the headline in `mainline log`. If the latest instruction is
only a context-dependent reference rather than a durable description of the
work, expand it into a short, human-readable goal in the user's language.
Preserve the reference in a turn or seal reference instead of making it the
headline. Code identifiers, command names, file paths, and CLI snippets stay in
their original form regardless of conversation language.

## Trigger Policy

Use this skill for any task in a Git repository when one of these is true:

- The repository has `.mainline/config.toml`, `.ml-cache/`, a Mainline block in
  AGENTS.md, Mainline refs, or existing Mainline commands in project docs.
- The user mentions Mainline, intents, conflict checks, agent guidance, hooks,
  sealing, proposals, coverage, gaps, or uncovered commits.
- You are about to edit code, refactor, delete code, change tests or CI, commit,
  push, create a PR, review a PR, or investigate whether prior work already made
  a decision in a repository known to use Mainline.

If the skill triggers because the repository appears to use Mainline, run the
Mainline checks before broad code search or edits. If the repository does not
appear to use Mainline and the user did not ask to set it up, do not initialize
Mainline without user confirmation.

## Setup Responsibility

Do not assume the human has already installed or initialized Mainline. If the
task needs Mainline and `mainline` is missing, install or help install it before
continuing.

First check:

```bash
command -v mainline
mainline status --json
```

If the CLI is missing, prefer the public install channel once available. For
the current private-repository phase, use Go's native installer:

```bash
GOPRIVATE=github.com/mainline-org/* go install github.com/mainline-org/mainline@main
```

If installing a fixed internal version:

```bash
GOPRIVATE=github.com/mainline-org/* go install github.com/mainline-org/mainline@v0.1.0
```

If Go cannot fetch the private GitHub repository over HTTPS, configure GitHub
SSH access rather than embedding credentials:

```bash
git config --global url."git@github.com:".insteadOf "https://github.com/"
```

After install, ensure the Go binary directory, commonly `~/go/bin`, is on
PATH. Re-run `mainline status --json`.

If the CLI exists but the repository is not initialized and the user asked to
set up Mainline, initialize it. `mainline init` installs the default Mainline
skill and repo-local hook integrations; it does not write AGENTS.md unless the
user explicitly asks for repo-level policy:

```bash
mainline init --actor-name "<name>"
```

Choose `<name>` from explicit user input, existing git identity, or a stable
local actor name. If initialization would modify shared repository guidance or
Git refspecs and the user only asked for a narrow code change, ask before
initializing.

## Hooks

Hooks provide dynamic context and improve trigger rate, but they are not the
source of truth. The skill remains the full workflow authority, and the agent
must still make the semantic decisions itself.

`mainline init` installs hooks by default. Use these commands when the user asks
to inspect, repair, or manually install hook integrations:

```bash
mainline hooks status
mainline hooks install
```

If hooks are not installed, continue with the command workflow below. Do not
block code work solely because hooks are absent.

## Start Of Task

At the start of a real task:

```bash
mainline status --json
```

If there is no active intent and the task will make non-trivial changes, start
one using the user's actual goal:

```bash
mainline start "<user goal>" --json
```

If a sealed or proposed intent already exists for the same branch and the user
is asking for follow-up changes, start a new intent for the follow-up rather
than trying to mutate the sealed one.

Before designing the change, inspect in-flight work:

```bash
mainline list-proposals --json
```

## Intent-First Code Reading

Before broad grep, file reads, or implementation on non-trivial work, retrieve
intent context:

```bash
mainline context --current --json
```

If the user named files or the likely files are already known:

```bash
mainline context --files <path>... --json
```

If the task is semantic:

```bash
mainline context --query "<task summary>" --json
```

Read returned summaries, decisions, risks, and fingerprints. Use them as
historical context, then verify against current code. Do not repeat abandoned
or superseded approaches unless the user explicitly asks to revisit them.

For relevant intents:

```bash
mainline show <intent_id> --json
mainline trace <intent_id> --json
```

## Editing Workflow

After the Mainline context pass, inspect code normally and make the requested
change.

Record turns at meaningful points:

```bash
mainline append "<what changed and why>" --json
```

Append after a completed subtask, a pivot, or a discovery that changes the
approach. Do not append a minute-by-minute activity log.

Never revert unrelated user changes to satisfy Mainline. If the worktree has
unrelated files, leave them alone and keep your intent evidence scoped to your
own changes.

## Commit Workflow

Mainline does not prescribe how a repository stages changes, writes commits, or
groups commits. Use the repository's existing Git workflow and commit
conventions. If you are the one creating the commit, inspect the unstaged and
staged diff first and include only the intended files.

Before sealing, there must be a commit for Mainline to reference. Mainline does
not create that commit for you.

If the user asks for a commit or PR and the branch has no active intent, create
or backfill one before committing unless the change is truly mechanical and the
repository policy marks it skipped.

## Seal Workflow

After the repository has a commit for this work, prepare the seal:

```bash
mainline seal --prepare --json > .ml-cache/seal.json
```

`.ml-cache/` is gitignored by `mainline init`, so the temp seal file
stays out of git AND keeps the v0.3 worktree-clean check happy on
submit. The package contains a `seal_result_starter` field with the
deterministic bits (intent_id, fingerprint.files_touched,
fingerprint.subsystems) pre-populated — patch in title / what / why /
decisions / risks / anti_patterns / confidence rather than typing
the JSON from scratch.

Generate a SealResult JSON matching the returned schema. The fingerprint must
be specific enough for conflict detection:

- Subsystems and parent concepts
- Files touched
- Architectural claims
- Behavioral changes
- API or CLI changes
- Data model changes
- Security implications
- Migration notes
- Tags, including synonyms and related technologies

Submit the seal:

```bash
mainline seal --submit --json < .ml-cache/seal.json
```

If the worktree has unrelated dirty or untracked files that cannot be cleaned
because they belong to the user, use `--allow-dirty` only after noting that
Mainline will permanently record the dirty state:

```bash
mainline seal --submit --allow-dirty --json < .ml-cache/seal.json
```

If the response includes `conflicts`, surface them to the user verbatim before
continuing. Do not silently move on from Mainline conflicts.

If submit sealed locally but failed to publish because the network was down,
retry later:

```bash
mainline publish --intent <intent_id> --json
```

## Publishing, Pushes, And PRs

Mainline does not require a Git push, a pull request, or GitHub. Preserve the
repository's existing review and release workflow unless the user explicitly
asks you to change it.

Before any remote branch push or PR creation that the user requested, ensure
the intent is proposed or publishable:

```bash
mainline status --json
mainline publish --intent <intent_id> --json
```

If the user's workflow opens or updates a PR, generate the PR body from the
sealed Mainline intent:

```bash
mainline pr-description --intent <intent_id> > .ml-cache/pr-description.md
```

Use that generated markdown as the PR body. Do not hand-write a replacement PR
description when a sealed intent exists, and do not rely on a generic GitHub
publish helper's default body. The generated body includes the
`mainline:pr-description` marker; the PR intent-comment workflow uses that
marker to avoid creating a duplicate sticky comment.

If the user did not ask to push or open a PR, stop after sealing/publishing the
Mainline intent and report the local result. Do not introduce a remote workflow
just because Mainline metadata is ready.

Do not run these unless the user explicitly asks:

```bash
mainline merge --intent <id>
mainline pin <intent> <commit>
mainline init --rewire
mainline doctor --setup --fix
```

## Review And Conflict Workflow

Use these commands when asked to review Mainline state, explain prior work, or
judge conflict markers:

```bash
mainline log --json --limit 30
mainline show <intent_id> --json
mainline trace <intent_id> --json
mainline check --prepare --intent <intent_id> --json
mainline check --submit --json < judgment.json
```

Use `show` to understand decisions and risks. Use `trace` to understand how the
work unfolded. Use `check` only when phase-1 overlap needs a semantic judgment.

## Coverage And Rescue

If status or gaps reports uncovered commits:

```bash
mainline gaps --json
```

Choose the least destructive rescue path. These are recovery options, not a
replacement for the repository's normal Git workflow:

- If the commit is local and unpushed, you may undo the commit with `git reset
  --soft HEAD^`, start the proper intent, recommit using the repository's
  normal workflow, and seal.
- If the commit is already pushed, backfill an intent with `mainline start
  "<why>" --commits <sha>`, append the post-hoc explanation, then seal.
- If it is routine and deliberately outside Mainline, add a
  `Mainline-Skip:` trailer or configure a skip pattern.

Do not rewrite published history unless the user explicitly asks.

## Skill Distribution

Install this skill with `npx skills` for the target agent. During the current
private-repository phase:

```bash
npx skills add git@github.com:mainline-org/mainline.git --skill mainline -a codex
```

For local development:

```bash
npx skills add ./skills/mainline -a codex
```

When the repository is public:

```bash
npx skills add mainline-org/mainline --skill mainline -a codex
```

This distribution section is temporary operational scaffolding. The durable
purpose of the skill is to teach agents to install, initialize, use, publish,
and review Mainline intent data correctly.
