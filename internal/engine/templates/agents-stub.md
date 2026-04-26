## Mainline

<!-- mainline-agents-md-version: 4 -->

This project uses **Mainline** for AI-driven intent tracking and
conflict detection. The full agent workflow lives in `AGENTS.md` at
the repo root — read that file for the complete contract.

Quick reference:

```
mainline status                                      # see your state
mainline start "<goal>"                              # claim work
mainline append "<what changed>"                     # after each turn
git add ... && git commit -m ...                     # commit code
mainline seal --prepare > seal.json                  # then fill seal.json
mainline seal --submit < seal.json                   # auto syncs + checks
```

Sync, pin, merge are automatic — do not invoke them.
