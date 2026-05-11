## Mainline

<!-- mainline-agents-md-version: 24 -->

**Stop AI coding agents from repeating old engineering mistakes.**

Mainline is a Git-native memory layer that tells coding agents *why the code
is the way it is* before they edit it.

This project uses Mainline to record the intent behind every AI-driven
change and to surface conflicts between intents before they reach a PR
review. The agent is expected to both **read** team intents (for context)
and **write** its own intent (for the work it's doing). Both halves
matter — intents capture *why* changes were made, which is information
the diff alone cannot give you.

Respect agent autonomy stop lines surfaced by `mainline status` and
`mainline preflight`. Autonomy is advisory; hard gates and current user
instructions take priority. `assist` stops before commit/seal, `handoff`
stops after commit/seal/proposed intent and before push/PR, and `review`
may push a non-main branch and stops at an opened or updated PR. Review
autonomy never authorizes pushing `main`, merge, release, or post-merge
cleanup.

> **v0.3 invariant**: every commit on `main` is in exactly one of three
> states — `covered` (sealed intent claims it), `skipped` (`Mainline-Skip:`
> trailer or matched config pattern), or `uncovered` (neither). Run
> `mainline status` to see the rollup; `mainline gaps` to see uncovered
> commits with rescue suggestions.

### Language rule (load-bearing)

**Match the user's language in everything you write into Mainline:**
intent goal text, append turn descriptions, seal `summary.title` /
`what` / `why` / `user_goal` / decisions / review_notes, explicit
signal command text, and PR description prose. If the user wrote to you in
Chinese, seal in Chinese. English in, English out. Mixed → match
the dominant language of the user's request.

Why this is load-bearing: the seal record is the team's long-term
memory. A teammate reading `mainline show <id>` six months from now
has to recognise the work as theirs. Translating a Chinese-language
task into an English seal turns a clear record into something the
team has to re-translate at every read.

Two clarifications:

- **Don't translate the user's goal text.** `mainline start
  "<goal>"` should pass the user's words through verbatim — that
  text becomes the headline in `mainline log` for everyone. If the
  user said *"修复 OAuth 回调的会话丢失"*, start with exactly that
  string, not an English paraphrase.
- **Do not use context-dependent references as the goal.** If the
  user's latest instruction is not itself a durable description of
  the work, expand the actual requested work into a short
  human-readable goal in the user's language. Keep references in a
  turn / seal reference, not as the headline future readers see in
  `mainline log`.
- **Code identifiers, command names, file paths, and CLI snippets
  stay in their original form.** Match natural-language prose, not
  the technical substrate. `mainline seal --submit` is
  `mainline seal --submit` in any language; the surrounding *why*
  matches the user.

### At the start of a task

```
mainline preflight --json
```

Read `.data.agent_authority` from `preflight --json` or `status --json`
before deciding closeout. If `current.allowed_boundary` is `inspect_or_stop`,
inspect the named findings / overlaps before advancing the lifecycle; run
`mainline check` or ask for human judgment when the overlap is semantically
real. Otherwise advance only to the allowed boundary unless the current user
instruction explicitly lowers or raises the stop line within the team's
configured `max_autonomy` ceiling. Examples: "先给建议" lowers to `assist`;
"提交当前工作区" / "收口" maps to `handoff`; "直接 PR" / "可以提了吧" maps to
`review`; merge/release remains a separate explicit delivery task, not autonomy.

If there is no `active_intent`, start one (use the user's goal verbatim
when possible — it becomes the headline in `mainline log`):

```
mainline start "<short description of the user's goal>" --json
```

If `mainline status --json` or `mainline preflight --json` includes
`notes_health.likely_history_rewrite: true`, run the read-only notes
diagnosis before trusting proposed / coverage / context queues. That
health is cached by sync; if the user mentions a recent force-push,
rebase, filter-repo rewrite, author rewrite, contributors cleanup,
remote rollback, or suddenly wrong proposed / coverage state, run the
same diagnosis even when the cached warning is absent:

```
mainline doctor --notes --json
```

If doctor recommends migration, run only the preview first:

```
mainline migrate notes --infer --dry-run --json
```

Show the safe / review-required / unresolved counts to the user.
Do not run `mainline migrate notes --write` or `--push` unless the
user explicitly confirms the plan; `--push` changes the shared notes
ref and is a high-impact Git operation.

### Intent-first workflow (the load-bearing rule)

Before making any non-trivial code change, retrieve relevant intent
context **before** searching the codebase directly.

The default agent order is:

1. `mainline status` — overall state, identity, sync staleness, suggestions.
2. `mainline context --current --json` — historical intents relevant to
   your current branch + active draft + diff vs main.
3. If the task names files: `mainline context --files <path>... --json`.
4. If the task is semantic: `mainline context --query "<task summary>" --json`.
5. Read the returned intents' `summary`, `decisions`, lifecycle
   warnings, explicit `inherited_constraints`, and `fingerprint`.
6. **Only then** grep / read code to verify against the current
   implementation.
7. Edit.
8. When sealing, reference relevant prior intent IDs in your
   decisions. Do not create risks, followups, or constraints from seal.

Do not lead with `grep`, `rg`, or broad file reads for non-trivial
changes unless Mainline is unavailable or the task is purely mechanical.

`mainline context` does NOT replace code inspection. It provides the
historical *why* before the agent inspects the current *what*.

**Reading the retrieval output** — every returned intent carries a
`status` field that tells you how to use it RIGHT NOW (distinct from
its lifecycle status):

| status | how to read it |
|---|---|
| `current` | the current effective decision; verify against current code, then apply |
| `superseded` | replaced — read `superseded_by` instead and use this only for context |
| `abandoned` | this approach was tried and abandoned; do not repeat without understanding why |
| `stale` | files have churned or the intent is old; verify decisions still hold |

Each intent may also carry `guidance` — a single-line reminder derived
from `status`.

**Inherited constraints** — `mainline context` may surface an
`inherited_constraints` array listing human-promoted constraints
on the same files you are editing. Each carries a stable
`constraint_id`. When sealing, you must explicitly acknowledge each in
`acknowledged_constraints`:

```json
"acknowledged_constraints": [
  {
    "constraint_id": "int_abc123#0",
    "disposition": "preserved",
    "note": "kept the session middleware in place"
  }
]
```

Dispositions:
- `preserved` — constraint still applies, you respected it
- `mitigated` — you addressed the underlying concern differently
- `not_applicable` — you touched the file but this constraint is
  irrelevant to your change
- `intentionally_changed` — you knowingly relaxed/violated the
  constraint (reviewer attention needed)

### Pre-edit checklist for agents

Before editing code, answer:

- Did I run `mainline status`?
- Did I run `mainline context --current --json`?
- If files are involved, did I run `mainline context --files ... --json`?
- Did I read the relevant prior decisions and explicit constraints?
- Did I verify those intents against the current code?
- Am I about to repeat an abandoned or superseded approach?

### Task priority — when intent-first matters most

| Always mainline-first | Mainline-first preferred | Direct code OK |
|---|---|---|
| architecture changes / refactors | bug fixes | typo / formatting fixes |
| migrations / deletions | new feature additions | one-line obvious syntax fixes |
| auth / billing / data-model / permissions | API behaviour changes | mechanical rename, scoped |
| test-strategy changes | config / CI / release tweaks | user explicitly asks to view ONE file |
| any cross-file change | | |
| user asks "why is this here?" | | |
| user asks "can we delete this?" | | |
| user asks "did we try this before?" | | |

### Read team intents for context (do this aggressively)

Before working on anything non-trivial, scan recent intents for prior
work in the area you're about to touch. Each intent's `summary`
(what / why / decisions / review_notes) plus `fingerprint`
(subsystems, files_touched, tags) is **strictly richer than the diff** —
it tells you *why* the code looks the way it does, which decisions
were considered and rejected, and what the author flagged as a risk
or follow-up.

```
mainline log --json --limit 30
```

Filter by goal/title keywords matching the user's task. For each
relevant hit, pull the full record:

```
mainline show <intent_id> --json    # decisions / fingerprint / references
mainline trace <intent_id> --json   # turn timeline (when each turn
                                    # was added, how long it took)
```

`show` answers *what* the intent decided. `trace` answers *how* it
unfolded over time — useful when you're trying to understand why a
PR looks the way it does, or whether the agent got stuck and looped.

Before designing a change, also see what is currently in flight so
your work does not collide with someone else's proposed intent:

```
mainline list-proposals --json
```

`mainline context --json` is a quick agent-consumption snapshot of
the same data (current actor, active intent, recent merged) — useful
for orientation but does not replace the targeted log/show calls.

Use this aggressively. The cost is one or two CLI calls; the payoff
is correct architectural decisions and not duplicating someone's
just-finished work.

### Turns and intent history

Turns are a lightweight thinking scaffold used to prepare a good
seal. They are **not** expected to be a real-time activity log.

It is normal for several turns to be recorded together near seal
time, especially when an agent summarizes its work before sealing.
`mainline trace` will surface this honestly via the
`append_turns_recorded_together` flag — that is informational, not a
warning.

Use:

```
mainline show <intent_id> --json
```

to inspect the structured conclusion of an intent: summary,
decisions, fingerprint, and references.

Use:

```
mainline trace <intent_id> --json
```

to inspect how an intent unfolded over time: start, append, seal,
abandon, or supersede events.

`show` answers: *"What did this intent decide?"*
`trace` answers: *"How did this intent unfold?"*

### While working

Record turns at points that will help you write a good seal — when
a meaningful subtask completes, when you pivot, when a discovery
changes the plan. Many short turns or a few long turns are both
fine; what matters is that the seal author (you, later) has the
material to compose a faithful summary:

```
mainline append "<what changed and why>" --json
```

Turns are append-only. Don't try to amend or delete them — describe
the next state in a new turn.

### When the task is complete

1. Commit your code changes using the repository's normal workflow.
   Mainline needs a commit to reference before sealing, but it does
   not prescribe staging commands, commit grouping, or commit message
   style beyond the repository's own conventions. If you are the one
   creating the commit, inspect the unstaged and staged diff first and
   include only the intended files.

2. Ask Mainline to prepare a seal package:

   ```
   mainline seal --prepare --json > .ml-cache/seal.json
   ```

   The package includes a `seal_result_starter` field — a partially-
   filled `SealResult` with the deterministic bits (intent_id,
   fingerprint.files_touched, fingerprint.subsystems) pre-populated.
   Patch in the required agent-judgment fields rather than typing the
   JSON from scratch. The starter intentionally omits durable action
   signals; do not add `risks`, `anti_patterns`, or `followups`.

   Why `.ml-cache/`? Init writes that directory to `.gitignore`, so
   the temporary seal file stays out of git and does not trip the
   v0.3 worktree-clean snapshot contract on submit.

3. Generate a `SealResult` JSON matching the schema returned by
   `--prepare`. Populate the fingerprint generously — primary subsystem,
   synonyms, parent concepts, related technologies — so phase-1
   conflict detection has signal:

   ```
   "tags": ["auth", "authentication", "security", "jwt", "session"]
   ```

   Seal records decisions by default. It does not let agents create
   repo-wide constraints, risks, or follow-up queues. Use:

   - `review_notes` for ephemeral reviewer context.
   - `decisions` for accepted trade-offs and implementation limits.
   - `mainline risks add` only when a concrete failure mode also has a
     trigger or impact, plus mitigation / validation / owner.
   - `mainline followups add` only when the user explicitly deferred the
     work, an external issue/ticket/PR exists, or this PR explicitly cut
     real scope.
   - `mainline guard add` only through interactive human confirmation.

   Submit rejects any seal payload containing `summary.risks`,
   `summary.followups`, or `summary.anti_patterns`.

   If `mainline context` surfaced `inherited_constraints`, acknowledge
   each in the seal's `summary.acknowledged_constraints`:

   ```json
   "acknowledged_constraints": [
     {
       "constraint_id": "int_abc123#0",
       "disposition": "preserved",
       "note": "kept the session middleware in place"
     }
   ]
   ```

4. Submit it:

   ```
   mainline seal --submit --json < .ml-cache/seal.json
   ```

   Submit auto-syncs with the team and runs phase-1 conflict detection
   against proposed intents and merged intents that landed after your
   intent base. If the JSON response
   carries a `conflicts` array, **surface those conflicts to the user
   verbatim** before continuing. Do not silently move on.

5. (Optional but encouraged) Quality-check the seal:

   ```
   mainline lint <intent_id> --json
   ```

   `lint` runs deterministic checks against the sealed payload —
   empty / boilerplate `what`, missing decisions, decision without
   rationale, seal-embedded action signals, broken supersedes refs.
   Errors mean the seal will be hard for future retrieval to use;
   warnings are advisory. Lint is **not** wired into submit, so a
   bad seal still goes through — but a low-quality seal pollutes
   future `mainline context` results, which is the whole loop this
   workflow exists to keep healthy.

### If your workflow opens or updates a PR

Mainline does not require a Git push, a pull request, or GitHub.
Preserve the repository's existing review and release workflow unless
the user explicitly asks you to change it.

Before a remote branch push or PR creation that the user requested and the
stop line permits, ensure the intent is proposed or publishable:

```
mainline status --json
mainline publish --intent <intent_id> --json
```

When the user does ask you to open or update a PR, generate the PR body
from the sealed intent:

```
mainline pr-description --intent <intent_id> > .ml-cache/pr-description.md
```

Use that generated Markdown as the PR body. Do not hand-write a
replacement PR description when a sealed intent exists, and do not let a
generic GitHub publishing helper invent one. The generated body includes
the `mainline:pr-description` marker; the PR intent-comment workflow
uses that marker to avoid creating a duplicate sticky comment.

Before calling any GitHub publishing helper, connector, or `gh pr create`
fallback, inspect the generated file and verify that it still contains
`<!-- mainline:pr-description:start -->`. Pass that exact file content as
the PR body. Do not copy only the visible Markdown, regenerate a lookalike
body, or let the publishing helper overwrite the body with `--fill` /
default prose.

Do not push `main` as part of review autonomy. If the current branch is `main`,
create or switch to a non-main branch before pushing or opening a PR.

### When the user asks you to phase-2 check an intent

Phase 1 is automatic; phase 2 is invoked deliberately when phase 1
flags an overlap (`[check:~]` in `mainline log`) and the user wants a
real semantic judgment.

```
mainline check --prepare --intent <id> --json
```

Read each `judgment_task` in the package, judge whether it is a real
semantic conflict, and submit a `CheckJudgmentResult`:

```
mainline check --submit --json < judgment.json
```

The verdict surfaces in `mainline log`'s `[check:X]` column.

### Optional: agent hooks (opt-in context provider)

If `mainline hooks install <agent>` has been run for your agent
runtime (Cursor today; Codex / Claude Code reserved), the hook layer
runs **two mechanical operations** at session start and injects a
**status snapshot** into your system context — nothing more:

- At `sessionStart` the hook runs `mainline sync` (refreshes the team
  view) and `mainline status` (active intent, proposed count, synced
  head). It feeds that snapshot back to you as system-prompt context
  along with a pointer to this document. You no longer need to run
  `mainline status` as the very first call of a session — it has
  already run.
- At every other lifecycle event (turn start, turn end, subagent
  end, session end) the hook is a **no-op** for your reasoning. It
  fires webhook notifications for external observers (CI dashboards,
  pager integrations) and exits. It does NOT call `mainline start`,
  `mainline append`, `mainline seal --prepare`, or any other command
  that requires deciding what counts as a goal / a meaningful change /
  a fingerprint — those are LLM judgments and you remain the only
  party qualified to make them.

Concretely: every step described above (start when there is real
work, append after each meaningful logical change, commit, seal
--prepare, fill SealResult, seal --submit, surface conflicts) you do
yourself, hooks installed or not. The hook layer is a **context
provider**, not a workflow driver.

Run `mainline hooks status` to confirm whether hooks are wired and
whether `auto_sync_on_session_start` is on (the only mechanical
toggle). Disable it with `mainline hooks disable` if your network
makes the session-start sync painful — you can still drive the rest
of the workflow by hand.

### Hub: human reader surface (you don't run this; you suggest it)

`mainline hub export <dir>` and `mainline hub open` build a static
HTML site over the local synced intent view. It is for **humans**, not
agents — agents use `context` / `show` / `trace` / `gaps`.

You should suggest the hub when the user asks one of:

- *"What's the history of `<file>`?"* → hub's per-file page lists
  every intent that touched it.
- *"Who's been working on what lately?"* → hub's index shows the
  recent intents table; the actor pages give per-author rollups.
- *"Are there any conflicts or explicitly promoted signals I should review?"* →
  hub's signal/review pages and graph (supersessions, conflicts_with,
  shares_file edges) put the answer one click away.

Concretely:

```
mainline hub open                     # build + open in the default browser
mainline hub export ./hub-snapshot    # write a portable copy elsewhere
```

Both commands default the output to `<os-temp>/mainline-hub/<repo>`
so the static site never enters git.

Hub is read-only and rebuildable from the synced view; it never
modifies repo files outside the user-chosen output directory.

### What you do NOT need to run

- `mainline sync` — runs automatically inside `seal --submit` and
  whenever a fresh-data command (`check`, `pin`) needs it.
- `mainline pin` — runs automatically after every sync; the strategy
  cascade (tree_hash → commit_hash → goal_text) catches GitHub
  squash-merges with near-100 % reliability.
- `mainline merge` — humans merge via the GitHub PR UI; the next
  `mainline sync` auto-pins the squash commit.

### Do not run unless the user explicitly asks

```
mainline pin <intent> <commit>      # manual fallback
mainline merge --intent <id>        # non-PR pipeline only
mainline init --rewire              # repo setup repair
mainline doctor --setup --fix       # repo setup repair
```

### Encountering an uncovered commit (v0.3 rescue)

If `mainline status` or `mainline gaps` flags an uncovered commit (one
that landed on main with no intent), pick the **best** path you still
can — ordered by reversibility, cheapest first:

1. **Unpushed** — optionally undo and redo via the proper flow:

   ```
   git reset --soft HEAD^         # un-commit, keep changes
   mainline start "<goal>" --json
   <continue normal flow>
   ```

2. **Pushed** — backfill an intent that retroactively claims the commit:

   ```
   mainline start "<why this commit was made>" --commits <sha> --json
   mainline append "<turn-by-turn description, post-hoc>" --json
   mainline seal --prepare --json > .ml-cache/seal.json
   <fill .ml-cache/seal.json>
   mainline seal --submit --json < .ml-cache/seal.json
   ```

   The seal flow auto-pins the new intent to the listed commit on next
   `mainline sync`.

3. **Routine** (chore / format / version bump) — optionally mark as
   deliberately skipped:

   ```
   git commit --amend             # add `Mainline-Skip: <reason>` trailer
   ```

   Or add a pattern in `.mainline/config.toml` under `[mainline.skip]`
   so future similar commits classify automatically:

   ```toml
   [mainline.skip]
   patterns = ["^chore: format", "^bump:"]
   ```

4. **Already distributed, regrettably** — accept uncovered. The
   mainline log is a record of reality, not aspiration.

### Seal snapshot contract (v0.3)

`mainline seal --prepare` snapshots the worktree state (HEAD, branch,
clean/dirty/untracked) and persists it. `mainline seal --submit`
validates the live repo against that snapshot — HEAD drift, branch
drift, or dirty worktree all fail by default with a typed error. The
escape hatch is the explicit CLI flag `--allow-dirty`; even then, the
sealed event permanently records `worktree_status` so reviewers see
the audit trail.

Before `mainline seal --prepare`, make sure the intended code state is
committed using the repository's normal workflow. Untracked files
(planning docs, scratch notes) do **not** enter sealed evidence.
