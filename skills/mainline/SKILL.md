---
name: mainline
description: "Use for coding-agent work in Git repos that use or may need Mainline, or when users mention Mainline, intents, agent autonomy, agent_authority, max_autonomy, stop lines, allowed_boundary, inspect_or_stop, before_commit/proposed_intent/opened_pr, current-instruction overrides, commit/seal handoff, push/PR review boundaries, merge/release/deploy boundaries, auto-submit/auto-commit/auto-seal behavior, hooks, proposals, gaps, conflicts, committing, pushing, opening PRs, PR descriptions, or setup."
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

## Stop-Line Quick Matrix

Use this as the first-pass boundary decision; detailed workflows below explain
how to execute each boundary.

Interpret the latest user instruction semantically, not by keyword matching.

| Instruction class | Effective boundary | Do not cross without a new explicit request |
|---|---|---|
| Advice-only / read-only | `assist` | edits, commits, seals, metadata publish, push, or PR |
| Finish local work / commit / seal / handoff | `handoff` | code branch push or PR |
| Push branch / open or update PR | `review`, capped by team `max_autonomy` | push to `main`, merge, release, deploy, or post-merge cleanup |
| Continue next task | keep the current effective boundary | lifecycle advancement unless it is naturally next and permitted |
| Merge / release / deploy / package publish / post-merge cleanup | explicit delivery task, not autonomy | ambiguous target or missing repository workflow checks |

Vocabulary guardrails:

- `mainline publish` publishes Mainline intent metadata / actor-log state. It is
  distinct from pushing a Git branch, opening a PR, merging, package publishing,
  deployment, or product release. If the user says "publish" without a clear
  object, identify the target before acting.
- Git branch push, PR creation, PR merge, and product release/deploy are
  separate delivery steps. Do not collapse them into one "publish" action.
- A file overlap is only a signal. It becomes a conflict workflow only after
  inspection shows it is real and potentially contradictory.

## Language Rule (load-bearing)

Match the user's language in everything you write into Mainline:
the goal text on `mainline start`, every `mainline append` turn,
the seal `summary.title` / `what` / `why` / `user_goal` / decisions
/ review_notes, explicit signal command text, and PR description prose.
If the user wrote in Chinese, seal in Chinese. English in, English out.
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
  sealing, proposals, coverage, gaps, uncovered commits, agent autonomy,
  `agent_authority`, `max_autonomy`, stop lines, `allowed_boundary`,
  `inspect_or_stop`, current-instruction overrides, handoff / review / delivery
  boundaries, or auto-submit / auto-commit / auto-seal behavior.
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

If the CLI is missing, prefer the public install script on macOS/Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | bash
```

If the user prefers Go's native installer or the install script is unavailable,
use Go 1.22+:

```bash
go install github.com/mainline-org/mainline@latest
```

Use `@main` only when the user explicitly wants the current unreleased
development version:

```bash
go install github.com/mainline-org/mainline@main
```

After install, ensure the install directory (commonly `~/.local/bin` for the
script or `~/go/bin` for `go install`) is on PATH. Re-run
`mainline status --json`.

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

If the goal is too vague to make a useful intent, prefer one quick clarifying question before `mainline start`; do not turn this into PRD/task planning.
If you proceed anyway, use the safest narrow interpretation and record the assumption in the first `mainline append`.

At the start of a real task:

```bash
mainline preflight --json
```

`preflight` is the low-noise readiness gate. If it returns
`"level": "ok"` and `"ok_to_continue": true`, continue without expanding the
context surface just to be safe. If it returns `warn` or `block`, read the
`findings`, `overlaps`, and `recommended_next` fields and only then run the
targeted follow-up commands it points to (`show`, `trace`, `context
--files/--query`, or `check`).

Also read `agent_authority` when it is present. CLI JSON wraps command
payloads under `.data`, so the runtime path is usually
`.data.agent_authority`; examples and tests may show the unwrapped engine field
as `agent_authority`. It is advisory, but it is the team-visible stop-line
contract for how far the agent may advance without a fresh human instruction:

- `assist` / `before_commit`: analyze, edit, and verify, then stop before
  commit, seal, publish, push, or PR unless the user explicitly asks for
  handoff.
- `handoff` / `proposed_intent`: commit scoped work and seal when ready, then
  stop before pushing a code branch or opening/updating a PR unless the user
  explicitly asks for review.
- `review` / `opened_pr`: advance to external review on a non-main branch with
  a generated Mainline PR body, then stop before merge, release, or post-merge
  cleanup.

Hard gates and current user instructions take priority. Team `max_autonomy` is
a ceiling: a current user instruction can lower the stop line or raise it up to
that ceiling, but it cannot authorize a boundary above the team cap. Never
write a one-turn override into `.mainline/config.toml` or
`.mainline/local.toml`.

If `preflight` lowers `.data.agent_authority.current.allowed_boundary` to
`inspect_or_stop`, do not advance the lifecycle blindly. Inspect the named
findings / overlaps first. Classify each overlap:

- Same branch, same goal, or explicit follow-up → record the relationship.
- Adjacent / complementary protocol or documentation work → record why it is not
  contradictory.
- Upstream drift that changes the same behavior → rebase / merge / inspect
  before implementation or closeout.
- Real and potentially contradictory semantic conflict → run `mainline check` or
  ask for human judgment.

Do not run `mainline check` just because files overlap. Do not ignore an
overlap silently; record the classification in the next append / seal.

`notes_health.likely_history_rewrite` on `status` or `preflight` is cached by
the most recent sync, not recomputed on every hot-path command. If the user
mentions a recent force-push, rebase, filter-repo rewrite, author rewrite,
contributors cleanup, remote rollback, or suddenly wrong proposed / coverage
state, run the read-only diagnosis even if the cached warning is absent:

```bash
mainline doctor --notes --json
```

If doctor recommends migration, preview first:

```bash
mainline migrate notes --infer --dry-run --json
```

Do not run `mainline migrate notes --write` or `--push` unless the user
explicitly confirms the plan; `--push` changes the shared notes ref.

If the installed binary is older and lacks `preflight`, fall back to:

```bash
mainline status --json
```

Then run `mainline list-proposals --json` and targeted `mainline context
--files ... --json` only when status or the task suggests overlap risk.

## History Rewrite And Notes Recovery

If `mainline preflight --json` reports a `notes_rewrite_drift` finding, or
`mainline status --json` includes `notes_health.likely_history_rewrite: true`,
run the read-only diagnosis first:

```bash
mainline doctor --notes --json
```

Also run `mainline doctor --notes --json` when the user mentions a recent
force-push, rebase, filter-repo rewrite, author rewrite, contributors cleanup,
remote rollback, or a sudden spike in proposed intents / uncovered commits
after history changed.

If doctor recommends a migration, preview only:

```bash
mainline migrate notes --infer --dry-run --json
```

Show the safe / review-required / unresolved counts to the user before any
write. Do not run `mainline migrate notes --write` or `--push` unless the user
explicitly confirms the plan. `--push` changes the shared notes ref and must be
treated like a high-impact Git operation.

If there is no active intent and the task will make non-trivial changes, start
one using the user's actual goal:

```bash
mainline start "<user goal>" --json
```

If `mainline start` returns an existing draft for the branch, verify it matches
the user's task before appending. If it does not, stop and isolate the work.

If a sealed or proposed intent already exists for the same branch and the user
is asking for follow-up changes, start a new intent for the follow-up rather
than trying to mutate the sealed one.

Before designing the change, inspect in-flight work:

```bash
mainline list-proposals --json
```

You do not need this separate `list-proposals` call when `preflight` is
available and green; `preflight` already reads the proposed index and reports
file overlap. Use `list-proposals` as the old-binary fallback or when
`preflight` says an overlap needs investigation.

## Intent-First Code Reading

Before broad grep, file reads, or implementation on non-trivial work, prefer
the compact preflight gate first:

```bash
mainline preflight --json
```

If it reports an overlap, stale view, or branch drift, expand narrowly:

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

Do not run broad `context --current`, `list-proposals`, `show`, and `trace`
as a ritual on every task. The intended flow is: preflight first; green means
continue; yellow/red means inspect the named intents/files and resolve the
specific risk.

Overlap convergence tree:

- Upstream already solved the user's goal → abandon the local draft or stop
  before duplicating it.
- Local work still has incremental value → narrow it into a follow-up intent.
- The two intents are semantically mutually exclusive → run `mainline check`
  or ask for human judgment before continuing.

## Worktree And Intent Ownership

Reuse the current worktree when it is clean or clearly owned by the current
task.

Before `git switch`, `mainline append`, or `mainline seal`, confirm the branch,
active intent, and dirty/untracked files still point to the same task. If not,
stop and isolate the work; prefer a separate Git worktree for parallel active
intents.

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

Keep the intent drafting while exploring, proving an idea, or while the branch
is likely to be rebased, amended, or rescoped. A local commit is useful evidence,
but it does not by itself mean the work is ready to submit as team memory.

## Stop-Line Workflow

Use `agent_authority` plus the current user instruction to decide the closeout
boundary. Do not treat "work is implemented" as automatic permission to advance
past that boundary. Current-instruction overrides are interpreted by the agent
for this turn only; never write them to `.mainline/config.toml` or
`.mainline/local.toml`.

1. Establish collaboration context with `mainline preflight --json`.
2. Read `.data.agent_authority.current.allowed_boundary` and decide any current
   user override.
3. Do the work and verify it.
4. Advance only to the allowed boundary.
5. Stop on hard gates.

The effective boundary is the lower of team policy, hard gates, and the current
instruction. A direct user request can raise a turn to `review` only when
`.data.agent_authority.team.max_autonomy` permits it; merge, release, and
post-merge cleanup remain explicit delivery tasks, not autonomy.

Current instruction override classes:

| Instruction class | Treat as |
|---|---|
| Advice-only / read-only | lower to `assist` |
| Finish local work / commit / seal / handoff | set boundary to `handoff` |
| Push branch / open or update PR | raise to `review`, capped by team `max_autonomy` |
| Merge / release / deploy / package publish / post-merge cleanup | explicit delivery task, not autonomy; identify the target and proceed only if directly requested and other hard gates allow it |

Vague continuation instructions only advance to PR in effective `review`
autonomy when implementation is complete, verification has passed, no
unresolved design questions remain, and commit/seal/PR is the next natural
boundary. Otherwise continue the next unfinished implementation or design step.

## Commit Workflow

Mainline does not prescribe how a repository stages changes, writes commits, or
groups commits. Use the repository's existing Git workflow and commit
conventions. If you are the one creating the commit, inspect the unstaged and
staged diff first and include only the intended files.

Before committing or sealing, re-run the readiness gate:

```bash
mainline preflight --json
```

If it reports `block`, stop and resolve or escalate the named overlap/drift
instead of producing a dirty-only or stale-base seal. If it reports only
`warn`, mention the warning in your handoff and make a conscious decision
before continuing.

Before sealing, there must be a commit for Mainline to reference. Mainline does
not create that commit for you.

If effective autonomy is `assist`, stop here after reporting the diff and
verification status. The user may then explicitly ask you to commit and seal,
which is a current-instruction handoff.

If the user asks for a commit or PR and the branch has no active intent, create
or backfill one before committing unless the change is truly mechanical and the
repository policy marks it skipped.

## Seal Workflow

Only seal when the work is ready for handoff, review, PR, push, or another
team-visible memory boundary. Do not seal merely because a local experiment or
intermediate commit exists.

Do not seal in effective `assist` autonomy unless the user has explicitly asked
for a handoff, commit, or seal boundary.

When the repository has the intended commit and the work is ready for that
handoff boundary, prepare the seal:

```bash
mainline seal --prepare --json > .ml-cache/seal.json
```

`.ml-cache/` is gitignored by `mainline init`, so the temp seal file
stays out of git AND keeps the v0.3 worktree-clean check happy on
submit. The package contains a `seal_result_starter` field with the
deterministic bits (intent_id, fingerprint.files_touched,
fingerprint.subsystems) pre-populated — patch in title / what / why /
decisions / rejected / acknowledged_constraints when applicable /
review_notes / fingerprint / confidence rather than typing the JSON
from scratch.

Seal records decisions by default. The seal summary write schema does
not include durable signal creation fields such as legacy
`summary.risks`, `summary.followups`, or `summary.anti_patterns`; old
temp seal files containing those keys are rejected and should be
regenerated with `mainline seal --prepare --json`.

Use explicit signal commands only when the source is real:

- `mainline risks add` only for a concrete failure mode with trigger or
  impact, plus mitigation / validation / owner.
- `mainline followups add` only when the user explicitly deferred the
  work, an external issue/ticket/PR exists, or this PR explicitly cut
  real scope.
- Do not create constraints yourself. `mainline guard add` is
  interactive and requires human confirmation.

If you only have reviewer context, validation notes, accepted trade-offs,
scope explanation, or a "maybe later" thought, keep it in `review_notes`,
`decisions`, final response, or leave it out. Candidate constraints can
be proposed to the user, but only the human-confirmed guard command
makes them durable.

Generate a SealResult JSON matching the returned schema. The fingerprint must
be specific enough for conflict detection:

- `summary.decisions` is an array of objects (`point`, `chose`, optional
  `rationale`, optional `rejected` string array), not a string array.
- `summary.rejected` is an array of objects (`alternative`, optional `reason`);
  keep it as `[]` when there are no top-level rejected alternatives.
- `summary.review_notes` is a string array; keep it as `[]` when there is no
  ephemeral reviewer context.
- `fingerprint.api_changes` and `fingerprint.data_model_changes` are arrays of
  objects; keep them as `[]` when none apply.
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

If the response includes `conflicts`, treat them as phase-1 overlap warnings,
not semantic conflict judgments. Inspect and classify them before closeout:
adjacent, complementary, or already-accounted-for overlap should be summarized
briefly in human terms only when useful; do not paste raw JSON by default. If
you cannot classify a warning or it looks like a real semantic conflict,
escalate to the user with the relevant intents, why it may conflict, and the
recommended next action. Include raw JSON only when the user asks for debug
detail or the tool output itself is needed to diagnose a failure.

If submit sealed locally but failed to publish because the network was down,
retry later:

```bash
mainline publish --intent <intent_id> --json
```

This is metadata publish, not product release or deploy. It only retries
publishing the sealed Mainline intent state.

Do not use `mainline abandon` as a routine repair path for seal wording, lint
warnings, or commit-hash drift after rebase/amend. Use abandon for cancellation
or rejection of the work; if a submitted seal looks wrong, report the problem
and choose an explicit repair/replacement path with the user.

## Publishing, Pushes, And PRs

Mainline does not require a Git push, a pull request, or GitHub. Preserve the
repository's existing review and release workflow unless the user explicitly
asks you to change it.

Effective `handoff` autonomy stops before pushing a code branch or opening /
updating a PR. Effective `review` autonomy may push a non-main branch and open
or update a PR, but it still stops before merge, release, or post-merge cleanup.
If the current branch is `main`, create or switch to a non-main branch before
pushing or opening PR; `review` autonomy never authorizes `git push origin main`.

Before any remote branch push or PR creation that the user requested and the
stop line permits, ensure the intent is proposed or publishable:

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

Before calling any GitHub publishing helper, connector, or `gh pr create`
fallback, inspect the generated file and verify that it still contains
`<!-- mainline:pr-description:start -->`. Pass that exact file content as the
PR body. Do not copy only the visible Markdown, regenerate a lookalike body, or
let the publishing helper overwrite the body with `--fill` / default prose.
For the `gh` fallback, the safe shape is:

```bash
gh pr create --body-file .ml-cache/pr-description.md
```

Do not use `gh pr create --fill` or any connector default body when a sealed
intent exists.

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
work unfolded. Use `check` only when inspected phase-1 overlap is real and
potentially contradictory. Adjacent or complementary overlap should be recorded
as a judgment in append / seal instead of escalated into `check`.

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

Install this skill with `npx skills` for supported target agents:

```bash
npx --yes skills add mainline-org/mainline --skill mainline --agent codex claude-code cursor --global --yes
```

For local development from this repository:

```bash
npx --yes skills add ./skills/mainline --skill mainline --agent codex claude-code cursor --global --yes
```

`mainline init` best-effort installs the default skill and hooks. Existing
global skill installs are not refreshed by `mainline agents update` or
`mainline init --rewire`; refresh them explicitly with:

```bash
npx --yes skills update mainline --global --yes
```

If update cannot determine the source, rerun the matching `skills add` command
above. The durable purpose of the skill is to teach agents to install,
initialize, use, publish, and review Mainline intent data correctly.
