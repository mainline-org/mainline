// Package claudecode is a placeholder for Claude Code CLI hook
// integration.
//
// Same posture as the codex sibling package: the directory exists so
// the extension contract is concrete, but the package does NOT
// register with internal/hooks until the implementation lands.
//
// Pending work to remove the stub:
//   - implement Install writing to .claude/settings.json
//     (Claude Code reads its hooks from settings, not a dedicated
//     hooks file; the merge logic must preserve all unrelated keys
//     including permissions/, model/, env/)
//   - implement ParseEvent for Claude Code's lifecycle events:
//     SessionStart / Stop / SubagentStop / PreToolUse / PostToolUse /
//     PreCompact, mapping onto hooks.SessionStart / TurnEnd /
//     SubagentEnd / Compaction
//   - decide which Claude Code hooks are mainline-managed (the v1
//     position from cursor: only those needed for the auto-flow,
//     leaving the rest free for user-installed hooks)
package claudecode
