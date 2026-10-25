---
name: mainline-layerfs
description: Use Mainline's Git-native repository memory while working in a LayerFS project.
---

# Mainline in LayerFS

Read the `mainline` and `layerfs-js-sdk` skills alongside this one. Your host supplies
`LAYERFS_*` settings, a project, a task Branch, a running Sandbox, and a Sandbox image
with Linux Mainline, Git, Bash, coreutils (`timeout`, `base64`) and findutils.

The project is in LayerFS. Your local current directory is scratch. Run Mainline in
the project's Git repository with:

```sh
mainline-layerfs run --sandbox 1 --branch task-a -- preflight --json
mainline-layerfs run --sandbox 1 --branch task-a -- context --query 'export behavior' --json
mainline-layerfs run --sandbox 1 --branch task-a -- start 'Fix the export behavior' --json
mainline-layerfs run --sandbox 1 --branch task-a -- append 'Kept the enterprise reconciliation route' --json
```

Use the Sandbox number and Branch your host actually supplied. Project defaults to
`LAYERFS_PROJECT`, or pass `--project`. Each run makes one Workspace, runs one Mainline
command, checkpoints all changes including `.git` and `.ml-cache`, and unmounts. The
JSON result contains `execution.stdout`, `execution.stderr`, `execution.exitCode`,
`layerfsBranch`, `workspace`, `checkpoint`, and `unmounted`. Parse Mainline's JSON from
`execution.stdout`. Check both the command exit and Mainline's returned readiness /
authority fields; exit 0 alone does not authorize more work.

Mainline controls when to read context, record a decision, commit and seal. Preserve
its stop lines and the current user's instructions. Read-only analysis may stop after
preflight/context. Read `mainline` for the full workflow and use the user's language.

Work sequentially on a task Branch with a standalone `.git` directory. Separate
agents use separate LayerFS Branches. Share sealed intent using the existing
Mainline Git remote and refs/notes workflow. Local drafts remain in their task
Branch; independent LayerFS Branches are not linked Git worktrees. LayerFS Commits
checkpoint the filesystem; Mainline seals reference real Git commits. Keep both IDs.

For source edits and explicit Git commands, use the public SDK or official `layerfs`
command as taught by `layerfs-js-sdk`. Commit changed files to Git before preparing a
Mainline seal. Save the prepare result from `execution.stdout` to a local scratch JSON
file, fill its `seal_result_starter`, then pass the final SealResult as stdin:

```sh
mainline-layerfs run --sandbox 1 --branch task-a -- seal --prepare --json
mainline-layerfs run --sandbox 1 --branch task-a --input /tmp/seal-result.json -- seal --submit --json
```

`--input -` reads stdin; maximum 64 KiB. Redirection written after the final `--` is a
literal Mainline argument, so use `--input` to submit a seal. Run project commands with
relative paths; mount paths change. Use a complete fresh clone for each independent
actor, with Mainline initialized for that actor; cloning an initialized local identity
into concurrent agents would reuse the actor's log. Synchronize and publish metadata
through Mainline's authorized workflow before combining source work through Git.

Each LayerFS operation is attempted once. On refusal read `code`, `message`, `detail`
and `stage`. A failing shell command still checkpoints its changes and returns its
exit code. A LayerFS failure exits 125; it includes the Workspace and any cleanup
errors. `Conflict` or a refused cleanup can leave a Workspace mounted: preserve it
and inspect through the SDK before deciding on a new action. `Unknown` or `Failed`
means effects may be uncertain. Report the error and stop. Never replay a refused or
uncertain call, force-commit, discard changes, restart a worker, or create a replacement
Sandbox automatically.

On macOS / Windows the host must supply `LAYERFS_WORKER_ENDPOINT` or launch you through
`mainline-layerfs launch -- <agent executable>`. Host runtime management stays with the
host. On Linux Store calls require the configured local Store mount in the calling
process. Use the host's supplied SDK/CLI installation; LayerFS source imports and
`@layerfs/agent/internal` are outside the plugin contract.
