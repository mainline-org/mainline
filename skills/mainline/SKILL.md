---
name: mainline
description: Use when working in a repository that uses Mainline to track AI change intent, including intent-first context retrieval, start/append/seal workflows, conflict surfacing, and internal private-repo CLI installation.
---

# Mainline

Mainline records the intent behind AI-driven code changes and surfaces
semantic overlaps before work reaches PR review. Use this skill whenever the
repository has Mainline metadata, mentions Mainline in AGENTS.md, or the user
asks about Mainline intents, conflicts, agent workflow, or installation.

## CLI Availability

Before running a Mainline workflow, check that the CLI is available:

```bash
mainline status --json
```

If `mainline` is not on PATH and the user is working from the private
`mainline-org/mainline` repository, use Go's native installer as the internal
bootstrap path:

```bash
GOPRIVATE=github.com/mainline-org/* go install github.com/mainline-org/mainline@main
```

For a fixed internal version, install from a tag:

```bash
GOPRIVATE=github.com/mainline-org/* go install github.com/mainline-org/mainline@v0.1.0
```

This requires the machine to have Go installed and GitHub access to the private
repository, typically through SSH or a GitHub token. If Go cannot fetch the
private repository over HTTPS, prefer configuring Git to use SSH:

```bash
git config --global url."git@github.com:".insteadOf "https://github.com/"
```

After installation, ensure the Go binary directory is on PATH. It is commonly:

```bash
~/go/bin
```

If the CLI still cannot run, stop and ask the user to resolve the local CLI
installation or authentication problem before making Mainline intent changes.

## Intent-First Workflow

At the start of a non-trivial task, inspect Mainline state:

```bash
mainline status --json
```

If there is no active intent for the work, start one with the user's goal as
the goal text:

```bash
mainline start "<user goal>" --json
```

Before searching the codebase directly, retrieve relevant intent context:

```bash
mainline context --current --json
```

If the task names files, also retrieve file-specific context:

```bash
mainline context --files <path>... --json
```

If the task is semantic and not file-specific, retrieve query context:

```bash
mainline context --query "<task summary>" --json
```

Read the returned summaries, decisions, risks, and fingerprints, then verify
them against the current code before editing.

## In-Flight Work

Before designing a change, inspect current proposals so the work does not
collide with another actor's proposed intent:

```bash
mainline list-proposals --json
```

For relevant prior or proposed intents, inspect the structured conclusion and,
when useful, the event timeline:

```bash
mainline show <intent_id> --json
mainline trace <intent_id> --json
```

## Recording Turns

Append turns only at meaningful logical points: after a subtask completes,
after a pivot, or after a discovery changes the implementation approach.

```bash
mainline append "<what changed and why>" --json
```

Do not treat turns as a minute-by-minute activity log. They are working notes
that help produce an accurate seal.

## Finishing Work

Commit the code changes before sealing:

```bash
git add <files>
git commit -m "<conventional commit message>"
```

Prepare the seal package:

```bash
mainline seal --prepare --json
```

Generate a matching SealResult JSON. Populate the fingerprint generously:
include subsystems, files touched, API changes, behavioral changes, and tags
with synonyms or related concepts that help phase-1 conflict detection.

Submit the seal:

```bash
mainline seal --submit --json < seal.json
```

If the submit response includes a `conflicts` array, surface those conflicts
to the user verbatim before continuing.

## Skills CLI Distribution

For the internal private-repository loop, install this skill from the repository
over SSH:

```bash
npx skills add git@github.com:mainline-org/mainline.git --skill mainline -a codex
```

For local development of the skill from a checked-out repository:

```bash
npx skills add ./skills/mainline -a codex
```

When the repository becomes public, the short GitHub form can be used:

```bash
npx skills add mainline-org/mainline --skill mainline -a codex
```

The skill only teaches agents how to work with Mainline. It does not install
the `mainline` CLI binary; install the CLI separately through the Go bootstrap
path above or a future public distribution channel.
