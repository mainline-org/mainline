<!-- mainline:agents:start version=9 checksum=sha256:e477845c980e622967b731a19b369d0ea8fc1a3e276e83d420a3b2ca451f79e4 -->
## Mainline

<!-- mainline-agents-md-version: 7 -->

This project uses **Mainline** for AI-driven intent tracking and
conflict detection. The full agent workflow lives in `AGENTS.md` at
the repo root — read that file for the complete contract.

Quick reference:

```
mainline status                                      # see your state

# Read team intents for context (do this aggressively):
mainline log --json --limit 30                       # recent intents
mainline show <intent_id> --json                     # full why/decisions/risks
mainline list-proposals --json                       # what's in flight

# Write your own intent:
mainline start "<goal>"                              # claim work
mainline append "<what changed>"                     # after each turn
git add ... && git commit -m ...                     # commit code
mainline seal --prepare > seal.json                  # then fill seal.json
mainline seal --submit < seal.json                   # auto syncs + checks
```

Sync, pin, merge are automatic — do not invoke them.

### If `mainline hooks` is installed for your agent

When hooks are active, the only thing that changes is sessionStart:
the hook runs `mainline sync` + `mainline status` for you and injects
the snapshot into your system context. Every other workflow step
(start / append / commit / seal --prepare / seal --submit / check)
remains agent-driven exactly as `AGENTS.md` describes. Hooks are a
context provider, not a workflow driver.
<!-- mainline:agents:end -->
