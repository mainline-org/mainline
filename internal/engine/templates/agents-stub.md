## Mainline

<!-- mainline-agents-md-version: 15 -->

This repository uses **Mainline** for AI-assisted intent tracking and conflict
detection.

The full workflow belongs in the installed `mainline` agent skill or the team's
global agent guidance. The repo-root `AGENTS.md` contains the lightweight
project marker, bootstrap reminders, and remote-write boundary for this repo.

If the `mainline` skill is missing and installation is allowed:

```bash
npx skills add mainline-org/mainline --skill mainline --global
```

Quick bootstrap until the skill is available:

```bash
mainline status --json
mainline context --current --json
mainline start "<goal>" --json                 # for real non-trivial work
mainline append "<what changed and why>" --json
mainline seal --prepare --json > .ml-cache/seal.json
mainline seal --submit --json < .ml-cache/seal.json
```

`mainline seal --submit`, `mainline sync`, and `mainline publish` may publish
Mainline metadata refs, but they do not authorize `git push` for the working
branch. Never push `main` or `master` without explicit current-turn user
authorization that names the branch.

Hooks, when installed, are context providers only. They do not choose goals,
append turns, seal intents, judge conflicts, or authorize Git branch pushes.
