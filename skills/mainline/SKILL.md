---
name: mainline
description: "Use for any coding-agent work in a Git repository that uses or may need Mainline intent tracking: before reading or editing code, fixing bugs, adding features, refactoring, deleting code, changing tests or CI, committing, deciding whether Git branch push is authorized, opening PRs, reviewing intent conflicts, or setting up Mainline CLI/hooks/agent guidance."
---

# Mainline

This skill is the agent-facing Mainline integration and the preferred full
workflow source of truth. Repo-local `AGENTS.md` blocks should be lightweight
project markers and bootstrap reminders, not a second copy of this manual.
This skill must still be sufficient when a repository has no Mainline text in
`AGENTS.md` and when Mainline hooks are not installed.

Mainline records why AI-driven changes happen, connects those intents to code
commits, and surfaces semantic conflicts before PR review. Treat it as part of
the coding workflow, not as optional documentation.

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

Pass the user's goal text through verbatim — `mainline start
"<goal>"` becomes the headline in `mainline log`. Code identifiers,
command names, file paths, and CLI snippets stay in their original
form regardless of conversation language.

## Trigger Policy

Use this skill for any task in a Git repository when one of these is true:

- The repository has `.mainline/config.toml`, `.ml-cache/`, a Mainline block in
  AGENTS.md, Mainline refs, or existing Mainline commands in project docs.
- The user mentions Mainline, intents, conflict checks, agent guidance, hooks,
  sealing, proposals, coverage, gaps, or uncovered commits.
- You are about to edit code, refactor, delete code, change tests or CI, commit,
  decide whether Git branch push is authorized, create a PR, review a PR, or
  investigate whether prior work already made a decision in a repository known
  to use Mainline.

If the skill triggers because the repository appears to use Mainline, run the
Mainline checks before broad code search or edits. If the repository does not
appear to use Mainline and the user did not ask to set it up, do not initialize
Mainline without user confirmation.

## Hard Boundaries / NEVER

These are load-bearing because Mainline writes collaboration metadata and can
coexist with normal Git remote writes:

- **NEVER treat `mainline publish`, `mainline sync`, or `mainline seal --submit`
  as authorization to `git push` the working branch.** They may publish
  Mainline metadata refs; Git branch push is a separate remote write.
- **NEVER push `main` or `master` without explicit current-turn user
  authorization that names that branch.** Prior push permission does not carry
  over to later commits, other branches, or protected branches.
- **NEVER run `mainline init`, `mainline init --rewire`, or
  `mainline doctor --setup --fix` merely because checks fail.** These commands
  modify repo guidance/refspecs; use them only for explicit setup/repair intent
  or repository policy that delegates setup.
- **NEVER reuse a sealed/proposed intent for new code changes.** Start a
  follow-up intent so the new why, files, and conflicts are recorded cleanly.
- **NEVER clean, revert, delete, or stage unrelated dirty/untracked user files
  to satisfy Mainline.** Keep your evidence scoped; use `--allow-dirty` only
  after noting the dirty state will be recorded.
- **NEVER continue silently after seal/check conflicts.** Surface conflicts to
  the user before proceeding so semantic overlap is visible.
- **NEVER rewrite published Git history as a Mainline rescue shortcut.** Use
  backfill/skip/accept-uncovered paths unless the user explicitly asks for
  history rewrite.

## Setup Responsibility

Do not assume the human has already installed or initialized Mainline. First
separate **missing tool**, **missing repo setup**, and **normal coding work**:

```bash
command -v mainline
mainline status --json
```

Decision tree:

- CLI missing -> install or help install it. During the private-repository phase,
  use Go install (`GOPRIVATE=github.com/mainline-org/* go install
  github.com/mainline-org/mainline@main`) and prefer SSH configuration over
  embedded credentials if GitHub HTTPS auth fails.
- Repo not initialized -> initialize only if the user asked to adopt/setup
  Mainline; otherwise continue without creating `.mainline/`, AGENTS guidance,
  or refspecs.
- Identity missing -> choose an actor name from explicit user input, git
  identity, or a stable local actor name, but only as part of setup/repair.
- Existing repo with stale/missing guidance -> report `mainline agents check` /
  `mainline agents diff`; do not rewrite guidance unless user requested setup,
  update, or repair.

If setup is authorized:

```bash
mainline init --actor-name "<name>"
mainline doctor --setup
```

## Hooks Are Optional

Hooks are an enhancement, not the source of truth. They may provide session
context automatically, but the agent must still run the semantic Mainline
workflow itself.

Use hooks only when the user asks for automation, setup, or better per-session
context, or when repository policy already says hooks should be installed:

```bash
mainline hooks status
mainline hooks install <agent>
```

If hooks are not installed, continue with the command workflow below. Do not
block code work solely because hooks are absent.

## Start Of Task

At the start of real work, orient first:

```bash
mainline status --json
mainline list-proposals --json
```

Intent decision tree:

- No active intent + non-trivial changes -> `mainline start "<user goal>" --json`.
- Active intent matches the branch and is still drafting -> append meaningful
  progress to it.
- Active intent belongs to another branch, or the relevant same-branch intent is
  sealed/proposed/merged -> start a new follow-up intent. Do not mutate old
  sealed intent state.
- User only asked a read-only question or one-file view -> no new intent unless
  repository policy explicitly requires one.
- Branch is behind remote -> you may inspect and merge/rebase according to repo
  workflow, but this still does not authorize Git branch push.

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

Before committing, inspect the unstaged and staged diff; stage only intended
files and preserve unrelated user work. Commit with the repository convention.

If the user asks for a commit or PR and the branch has no active intent, create
or backfill one before committing unless the change is truly mechanical and the
repository policy marks it skipped.

## Seal Workflow

After committing the code changes, prepare the seal:

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

## Mainline Publish, Git Push, And PR Workflow

`mainline publish` publishes Mainline intent metadata. It may push actor-log
refs to the configured remote, but it does not authorize `git push` for the
working Git branch.

Do not push a Git branch unless the user explicitly asked for push in the
current turn, or repository policy explicitly delegates push for this named
non-main branch. Never push `main` or `master` without explicit current-turn
user authorization that names the branch. Previous push permission applies only
to the named branch/commit scope and does not carry over to later commits or
different branches.

If Git branch push is authorized, first ensure the intent metadata is proposed
or publishable:

```bash
mainline status --json
mainline publish --intent <intent_id> --json
```

Then push only the authorized Git branch through the normal repository workflow.
Prefer feature branch + PR for collaboration. Humans merge PRs through the
GitHub UI unless the user explicitly asks for a non-PR merge path.

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

If status or gaps reports uncovered commits, inspect `mainline gaps --json` and
choose the least destructive path:

- Local and unpushed -> `git reset --soft HEAD^`, start the proper intent,
  recommit, and seal.
- Already pushed -> backfill an intent with `mainline start "<why>" --commits
  <sha>`, append the post-hoc explanation, then seal.
- Routine/out-of-scope -> add a `Mainline-Skip:` trailer or configure a skip
  pattern.
- Already distributed and not worth rewriting -> accept uncovered; the log is a
  record of reality, not aspiration.

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
