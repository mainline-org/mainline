# Mainline alpha walkthrough — friction list

> **Purpose**: harden the first-use experience by simulating a new
> user from `git init` to first sealed intent, finding what
> confuses them, and either fixing it inline or capturing it for
> later.
>
> **Method**: ran two scenarios — solo (brand new repo) and joining
> (clone of an existing mainline repo) — through every command on
> the daily path. For each step, asked: *(1) does the output tell
> me what to do next? (2) on error, do I know how to fix it?
> (3) do I need to read the README to continue?*
>
> **Date**: 2026-04-27. Mainline at PR #51 (status auto-sync).

---

## Verdict

The happy path works end-to-end. A user with `mainline init
--actor-name "x"` can reach a first sealed intent in under three
minutes following only on-screen prompts. The biggest single drag
on first-use credibility was **stale state surfacing as suggestions
that contradicted the rest of the output** — fixed inline below.

The walkthrough did NOT find a missing feature. It found three
small confidence-eroding bugs, three medium-quality gaps, and two
items that are honestly out-of-scope for v0.1.

---

## Fixed inline (this PR)

### F1. Init commit shows as uncovered on every status output

`mainline init` writes a commit named `mainline: init`. The default
skip patterns (`^Merge pull request `, `^Merge branch `, `^chore:
bump version`) didn't catch it, so a fresh-repo first
`mainline status` always showed:

```
⚠ Uncovered:  1
  <sha>  mainline: init
```

That's a false-alarm first impression. Added `^mainline: init` to
the default skip patterns and the storage-side backfill list.

### F2. `Unsealed intents` and `Suggestions` contradict the view

After an offline seal followed by `mainline status`, status's auto-
sync (PR #51) ran auto-pin which flipped the intent to `merged` in
the view. But the local draft file still said `sealed_local`.
Status's `collectUnsealedDrafts` read only the draft file, so:

- "Recent sealed intents" (from view) showed the intent — correct.
- "Unsealed intents" (from draft) ALSO showed the same intent — wrong.
- "Suggestions" said `git checkout main && mainline status # resume <id>` — wrong, the intent was already merged.

`collectUnsealedDrafts` now cross-references the view: any intent
the view considers `merged | abandoned | superseded | reverted` is
excluded regardless of what the draft file says. View overrides
draft for terminal states.

### F3. Bare `mainline init` silently stamps the user as `default-agent`

A fresh user running `mainline init` (no flags) got
`Actor name: default-agent` printed in the success block but
nothing told them this was a bug-prone default. Every commit note
they later wrote would be attributed to that placeholder identity.

`init` now prints a visible warning when `--actor-name` is omitted,
recommending they re-run with their name.

---

## Captured for follow-up (not in this PR)

### F4. Default `thread = branch name` collapses main work into one bucket

When a user runs `mainline start "..."` while on `main` (no feature
branch yet), the resulting intent has `thread: main`. Multiple
intents on main share the bucket, which is mostly harmless but odd
when read in `mainline log` — every entry's thread reads "main".

**Why not fix here**: thread semantics are a small UX issue, not a
bug. Solution likely involves either auto-deriving a thread name
from the goal text (LLM territory) or warning when `start` runs
on the default branch with no thread override. Worth a focused PR
once we see whether real users find it confusing or just ignore it.

### F5. `seal --prepare` with no committed work returns valid empty package

If a user runs `mainline seal --prepare` before any code commits,
the command produces a valid prepare JSON with `base_commit ==
current_head` and an empty diff. There's no error or warning that
the user is about to seal an empty intent.

**Why not fix here**: the snapshot contract in PR #43 already
catches the related "stale prepare" case at submit time. An empty-
diff warning at prepare time should probably be paired with an
upgrade to `mainline status` so the user gets nudged toward
committing first. Tracked.

### F6. `Suggestions` doesn't include `mainline trace` for review flows

Suggestions currently fires on lifecycle state (drafting → append+seal,
sealed_local → publish, idle+orphan → checkout, clean → start) but
doesn't surface `mainline trace <id>` even when there's an active
intent. A user reviewing what the agent did would benefit from
"trace your own active intent" being a one-line nudge.

**Why not fix here**: changes the Suggestions contract — currently
each phase suggests ONE primary action. Adding a secondary
suggestion needs a small UX call we should make once we have data
on whether the current Suggestions block is actually being read.

---

## Out of scope for v0.1

### O1. README "5-minute first sealed intent" hardening

The README quickstart already covers init → start → seal in 13
lines. The friction in those 13 lines is mostly aesthetic
(cleaner Step labels, an example seal.json shape), not blocking.
Real first-use feedback from the friend MVP rollout will tell us
where the actual stumbles are.

### O2. Identity gate error message wording

PR #43's `requireIdentity` already returns:

```
error [NOT_INITIALIZED]: this clone has no Mainline actor identity
  suggestion: mainline init --actor-name <your name>
```

That's the same shape as the alpha walkthrough hoped for. No
change needed.

---

## Non-issues (verified working)

- **Pre-init errors uniform and actionable** — `status` / `start` /
  `seal` / `trace` / `sync` all print
  `Run 'mainline init' first.` Could be tighter (drop "first") but
  not blocking.
- **`show` vs `trace` complementary** — show prints decisions /
  risks / fingerprint; trace prints timeline. Tested on
  `int_1d9fd63f`: zero overlap in output. The user is told the
  difference up-front by `mainline trace --help`.
- **Hooks not surfaced as required path** — README and AGENTS.md
  consistently treat hooks as optional context provider. The
  primary path remains `status → start → append → seal →
  log/show/trace`.
- **Abandoned intents don't pollute daily status** — a manual
  spot-check of `int_5286e23a` (which I abandoned during PR #36
  dogfood) shows it appears in `mainline log` with `[abandoned]`
  tag but does NOT appear in Recent sealed (which filters to
  `merged`) and would not appear in Unsealed drafts (filtered to
  `drafting | sealed_local`). Decision archaeology preserved;
  daily noise zero.
- **Cross-actor intents trace cleanly** — the rc7 cross-actor flag
  surfaces as a one-line note rather than corrupting the timeline.

---

## What this exercise revealed structurally

Before this walkthrough I had been adding features in response to
specific user feedback. The walkthrough revealed something the
features couldn't show: **the daily commands have grown numerous
enough that the failure mode is no longer "missing capability" but
"contradictory output across blocks"**. F2 is the canonical example
— two correctly-implemented blocks (Recent sealed + Unsealed
drafts) producing mutually inconsistent claims about the same
intent because they read from different sources of truth.

That's a maturity signal. The next iteration's question should be:
*"if a user reads only what status prints, do they reach a
coherent mental model of the repo's state?"* — not "what command
is missing".
