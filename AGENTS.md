<!-- mainline:agents:start version=12 checksum=sha256:62ee66d15a420f45eb3c1403cffe332072b56e14044597a18ddcc71fa14a0d83 -->
## Mainline

<!-- mainline-agents-md-version: 12 -->

**Mainline is a git-native intent memory layer for AI-assisted engineering.**
It gives coding agents the historical *why* before they inspect the
current *what*.

This project uses Mainline to record the intent behind every AI-driven
change and to surface conflicts between intents before they reach a PR
review. The agent is expected to both **read** team intents (for context)
and **write** its own intent (for the work it's doing). Both halves
matter — intents capture *why* changes were made, which is information
the diff alone cannot give you.

> **v0.3 invariant**: every commit on `main` is in exactly one of three
> states — `covered` (sealed intent claims it), `skipped` (`Mainline-Skip:`
> trailer or matched config pattern), or `uncovered` (neither). Run
> `mainline status` to see the rollup; `mainline gaps` to see uncovered
> commits with rescue suggestions.

### At the start of a task

```
mainline status --json
```

If there is no `active_intent`, start one (use the user's goal verbatim
when possible — it becomes the headline in `mainline log`):

```
mainline start "<short description of the user's goal>" --json
```

### Intent-first workflow (the load-bearing rule)

Before making any non-trivial code change, retrieve relevant intent
context **before** searching the codebase directly.

The default agent order is:

1. `mainline status` — overall state, identity, sync staleness, suggestions.
2. `mainline context --current --json` — historical intents relevant to
   your current branch + active draft + diff vs main.
3. If the task names files: `mainline context --files <path>... --json`.
4. If the task is semantic: `mainline context --query "<task summary>" --json`.
5. Read the returned intents' `summary`, `decisions`, `risks`,
   `anti_patterns`, and `fingerprint`.
6. **Only then** grep / read code to verify against the current
   implementation.
7. Edit.
8. When sealing, reference relevant prior intent IDs in your
   decisions, and record any new `anti_patterns` future agents must
   avoid in this area.

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

Each intent also carries:

- `risks` — soft warnings to weigh.
- `anti_patterns` — **hard constraints**. Each one carries a `what`,
  a `why`, and a `severity`. Do not violate them. The retrieval API
  never truncates `anti_patterns`, so if you see one, it is in scope.
- `guidance` — a single-line reminder derived from `status`.

### Pre-edit checklist for agents

Before editing code, answer:

- Did I run `mainline status`?
- Did I run `mainline context --current --json`?
- If files are involved, did I run `mainline context --files ... --json`?
- Did I read the relevant prior decisions and risks?
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
(what / why / decisions / risks / followups) plus `fingerprint`
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
mainline show <intent_id> --json    # decisions / risks / fingerprint
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
decisions, risks, and fingerprint.

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

1. Commit your code changes the normal way:

   ```
   git add <files> && git commit -m "<message>"
   ```

2. Ask Mainline to prepare a seal package:

   ```
   mainline seal --prepare --json > .ml-cache/seal.json
   ```

   The package includes a `seal_result_starter` field — a partially-
   filled `SealResult` with the deterministic bits (intent_id,
   fingerprint.files_touched, fingerprint.subsystems) pre-populated.
   Patch in the agent-judgment fields rather than typing the JSON
   from scratch.

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

   When the work establishes constraints future agents must respect,
   record them as `anti_patterns` (NOT as `risks`). Each entry MUST
   carry both `what` and `why`; empty `why` is rejected at seal time.

   ```json
   "anti_patterns": [
     {
       "what": "Removing legacy session middleware on /oauth path",
       "why":  "OAuth callback handler still requires session state",
       "severity": "high"
     }
   ]
   ```

   Use `risks` for soft warnings the reviewer should weigh; use
   `anti_patterns` for hard constraints the next agent must not
   violate. Anti-patterns are surfaced uncapped in `mainline context`,
   so future agents will always see them.

4. Submit it:

   ```
   mainline seal --submit --json < .ml-cache/seal.json
   ```

   Submit auto-syncs with the team and runs phase-1 conflict detection
   against every other proposed/merged intent. If the JSON response
   carries a `conflicts` array, **surface those conflicts to the user
   verbatim** before continuing. Do not silently move on.

5. (Optional but encouraged) Quality-check the seal:

   ```
   mainline lint <intent_id> --json
   ```

   `lint` runs deterministic checks against the sealed payload —
   empty / boilerplate `what`, missing decisions, decision without
   rationale, missing risks/anti_patterns, broken supersedes refs.
   Errors mean the seal will be hard for future retrieval to use;
   warnings are advisory. Lint is **not** wired into submit, so a
   bad seal still goes through — but a low-quality seal pollutes
   future `mainline context` results, which is the whole loop this
   workflow exists to keep healthy.

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
- *"Are there any conflicts or risky changes I should review?"* →
  hub's risks page and graph (supersessions, conflicts_with,
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

1. **Unpushed** — undo and redo via the proper flow:

   ```
   git reset --soft HEAD^         # un-commit, keep changes
   mainline start "<goal>"
   <continue normal flow>
   ```

2. **Pushed** — backfill an intent that retroactively claims the commit:

   ```
   mainline start "<why this commit was made>" --commits <sha>
   mainline append "<turn-by-turn description, post-hoc>"
   mainline seal --prepare > .ml-cache/seal.json
   <fill .ml-cache/seal.json>
   mainline seal --submit < .ml-cache/seal.json
   ```

   The seal flow auto-pins the new intent to the listed commit on next
   `mainline sync`.

3. **Routine** (chore / format / version bump) — mark as deliberately
   skipped:

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

Always commit your code BEFORE `mainline seal --prepare`. Untracked
files (planning docs, scratch notes) do **not** enter sealed evidence.
<!-- mainline:agents:end -->
