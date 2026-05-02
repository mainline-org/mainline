## Mainline

<!-- mainline-agents-md-version: 9 -->

This project uses **Mainline** for AI-driven intent tracking and
conflict detection. The full agent workflow lives in `AGENTS.md` at
the repo root — read that file for the complete contract.

Quick reference:

```
mainline status                                      # see your state

# Read team intents for context (do this aggressively):
mainline log --json --limit 30                       # recent intents
mainline show <intent_id> --json                     # full why/decisions/risks/followups
mainline list-proposals --json                       # what's in flight

# Write your own intent:
mainline start "<goal>"                              # claim work
mainline append "<what changed>"                     # after each turn
git add ... && git commit -m ...                     # commit code
mainline seal --prepare > .ml-cache/seal.json        # patch the starter
mainline seal --submit < .ml-cache/seal.json         # auto syncs + checks
```

Sync, pin, merge are automatic — do not invoke them.

**Language rule**: write everything you put into Mainline (goal,
appends, seal title/what/why/decisions/risks/anti_patterns/followups) in the
language the user used. Chinese in, Chinese out; English in, English
out. The seal is the team's memory — translating it makes it harder
to read for the people whose memory it is. Code identifiers, command
names, and file paths stay in their original form.

### If `mainline hooks` is installed for your agent

When hooks are active, the only thing that changes is sessionStart:
the hook runs `mainline sync` + `mainline status` for you and injects
the snapshot into your system context. Every other workflow step
(start / append / commit / seal --prepare / seal --submit / check)
remains agent-driven exactly as `AGENTS.md` describes. Hooks are a
context provider, not a workflow driver.
