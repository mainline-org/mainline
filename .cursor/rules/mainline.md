<!-- mainline:begin -->
## Mainline

<!-- mainline-agents-md-version: 6 -->

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

Run `mainline hooks status` once per session to find out. When hooks
are active, the agent runtime auto-invokes mainline at session and
turn boundaries:

- `session_start` triggers `mainline sync` and surfaces conflicts.
- `turn_start` may auto-`mainline start "<goal>"` if no draft exists.
- `turn_end` auto-`mainline append "<summary of turn>"`.
- `session_end` auto-`mainline seal --prepare`, leaving `seal.json`
  next to your worktree for you to fill in fingerprint/risks/followups
  and submit with `mainline seal --submit < seal.json`.

You still own the seal payload — hooks only stage the work. Always
read stderr after each turn: any conflicts or hook errors surface
there as one-line `[mainline]` notices.
<!-- mainline:end -->
