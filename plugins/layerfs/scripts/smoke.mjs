import assert from "node:assert/strict";
import { cli } from "../src/cli.mjs";

for (const name of ["LAYERFS_PROJECT", "LAYERFS_SMOKE_SANDBOX", "LAYERFS_SMOKE_BRANCH"]) {
  assert.ok(process.env[name], `Set ${name} explicitly for this opt-in smoke test`);
}
// Exactly one real run; the caller supplies an existing task Branch and Sandbox.
process.exitCode = await cli([
  "run", "--sandbox", process.env.LAYERFS_SMOKE_SANDBOX,
  "--branch", process.env.LAYERFS_SMOKE_BRANCH, "--", "preflight", "--json",
]);
