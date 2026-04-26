// Package codex is a placeholder for OpenAI Codex CLI hook integration.
//
// The package intentionally does NOT call hooks.Register at init time:
// registering a non-functional agent would clutter `mainline hooks
// list-agents` and offer the user a broken Install flow. Once the
// integration ships, the implementation will live alongside cursor's
// in this directory and the cli will blank-import this package the
// same way it imports cursor.
//
// Pending work to remove the stub:
//   - decide on the on-disk hooks file once codex's hook spec settles
//     (likely a JSON or JSONL payload under .codex/)
//   - implement Install / Uninstall / IsInstalled following cursor's
//     pattern: incremental read-modify-write with managed-marker
//     detection, version field round-tripping
//   - implement ParseEvent for codex's lifecycle events; expect
//     coverage of session_start / session_end / turn_complete
//
// This file exists so the directory is part of the build (linters
// and refactoring tools see it) and so the extension contract — a
// per-agent package that imports internal/hooks — is concrete.
package codex
