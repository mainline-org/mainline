## Mainline

<!-- mainline-agents-md-version: 14 -->

This project uses **Mainline** for AI-driven intent tracking and
conflict detection. The full agent workflow lives in `AGENTS.md` at
the repo root — read that file for the complete contract.

Quick reference:

```
mainline status                                      # see your state
mainline preflight --json                            # readiness + stop line

# Read team intents for context (do this aggressively):
mainline log --json --limit 30                       # recent intents
mainline show <intent_id> --json                     # full why/decisions/signals
mainline list-proposals --json                       # what's in flight

# Write your own intent:
mainline start "<goal>" --json                       # claim work
mainline append "<what changed>" --json              # after meaningful turns
git add ... && git commit -m ...                     # commit code
mainline seal --prepare --json > .ml-cache/seal.json # patch the starter
mainline seal --submit --json < .ml-cache/seal.json  # auto syncs + checks
```

Respect `.data.agent_authority` from status/preflight. If
`current.allowed_boundary` is `inspect_or_stop`, inspect the named finding or
overlap before lifecycle advancement; use `mainline check` only for real and
potentially contradictory overlap. `assist` stops before commit/seal, `handoff`
stops after commit/seal/proposed intent and before push/PR, and `review` may
push a non-main branch and stops at an opened or updated PR. Current user
wording can lower or raise within the team `max_autonomy` ceiling: "先给建议" /
"别直接改" => assist, "提交当前工作区" / "收口" => handoff, "直接 PR" / "可以提 PR 了吗"
=> review. Merge/release/发布 require explicit user instruction and review
autonomy never authorizes pushing `main`.

`mainline publish` publishes Mainline intent metadata. It is not product release
or deploy. `mainline agents update` refreshes repo AGENTS guidance only; global
skills need `npx --yes skills update mainline --global --yes` or the matching
`skills add` command.

Sync, pin, merge are automatic — do not invoke them.

**Language rule**: write everything you put into Mainline (goal,
appends, seal title/what/why/decisions/review_notes/signals) in the
language the user used. Chinese in, Chinese out; English in, English
out. The seal is the team's memory — translating it makes it harder
to read for the people whose memory it is. Code identifiers, command
names, and file paths stay in their original form.

Seal records decisions by default. Do not add `summary.risks`,
`summary.followups`, or `summary.anti_patterns` to a seal payload.
Use explicit `mainline risks add`, `mainline followups add`, or
interactive human-confirmed `mainline guard add` for durable signals.

### If `mainline hooks` is installed for your agent

When hooks are active, the only thing that changes is sessionStart:
the hook runs `mainline sync` + `mainline status` for you and injects
the snapshot into your system context. Every other workflow step
(start / append / commit / seal --prepare / seal --submit / check)
remains agent-driven exactly as `AGENTS.md` describes. Hooks are a
context provider, not a workflow driver.
