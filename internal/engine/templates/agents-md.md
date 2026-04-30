## Mainline

<!-- mainline-agents-md-version: 15 -->

This repository uses **Mainline** for AI-assisted intent tracking: agents read
team intent before changing code and record the intent behind their own changes.

This repo-local block is intentionally lightweight. The full workflow belongs in
the installed `mainline` agent skill or the team's global agent guidance. Use
this file as a project marker, bootstrap reminder, and hard remote-write
boundary — not as a second copy of the whole Mainline manual.

### Source of truth

1. Follow the current user request and any repository-specific rules first.
2. If your runtime exposes a `mainline` skill, load it and follow that skill for
   the full `status → context → start/append → commit → seal → review` workflow.
3. If the `mainline` skill is missing and installation is allowed:

   ```bash
   npx skills add mainline-org/mainline --skill mainline --global
   ```

4. Until the skill is available, use the bootstrap loop below and inspect
   `mainline --help` / `mainline <command> --help` for command details.

### Load-bearing rules

- Match the user's language in everything you write into Mainline: `start` goal,
  `append` text, seal summary fields, decisions, risks, anti_patterns,
  followups, and PR description prose. Code identifiers, command names, file
  paths, and CLI snippets stay in their original form.
- Before non-trivial code changes, read intent context before broad code search:

  ```bash
  mainline status --json
  mainline context --current --json
  # If files are known:
  mainline context --files <path>... --json
  # If the task is semantic:
  mainline context --query "<task summary>" --json
  ```

- Treat returned `anti_patterns` as hard constraints. Treat `risks` as soft
  warnings. Verify all retrieved intent guidance against the current code.
- Start a new intent for real non-trivial work when there is no active intent on
  the current branch:

  ```bash
  mainline start "<user goal>" --json
  ```

- Append only at meaningful pivots or completed subtasks:

  ```bash
  mainline append "<what changed and why>" --json
  ```

### Completion bootstrap

For code changes, commit first, then seal the intent:

```bash
git add <files> && git commit -m "<message>"
mainline seal --prepare --json > .ml-cache/seal.json
# Fill the SealResult fields in .ml-cache/seal.json.
mainline seal --submit --json < .ml-cache/seal.json
```

If `seal --submit` reports conflicts, surface them to the user verbatim before
continuing. `mainline lint <intent_id> --json` is a useful quality check for the
sealed record.

### Remote-write boundary

`mainline seal --submit`, `mainline sync`, and `mainline publish` may publish
Mainline metadata refs such as actor logs or notes. They are **not** permission
to push the working Git branch.

After commit and seal, stop and report that the branch is ready for push/PR
unless the user explicitly authorized pushing this branch in the current turn or
repo policy delegates that exact branch workflow. Never push `main` or `master`
without explicit current-turn authorization that names the branch.

Prefer feature branch + PR collaboration. Humans merge PRs through the hosting UI
unless the user explicitly asks for a non-PR merge path.

### Hooks are only context providers

If `mainline hooks` is installed, hooks may run `mainline sync` and
`mainline status` and inject a snapshot into the agent context. Hooks do not
choose goals, append turns, prepare seals, submit seals, judge conflicts, or
authorize Git branch pushes.
