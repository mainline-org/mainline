import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { checkout, verifyCheckout } from "./verify-layerfs.mjs";

const require = createRequire(import.meta.url);
const agent = require.resolve("@layerfs/agent/package.json");
const runtime = require.resolve("@layerfs/runtime/package.json");
const cli = require.resolve("@layerfs/cli/package.json");
assert.equal(createRequire(runtime).resolve("@layerfs/agent/package.json"), agent, "runtime and adapter must load one copy of @layerfs/agent");
assert.equal(createRequire(cli).resolve("@layerfs/agent/package.json"), agent, "CLI and adapter must load one copy of @layerfs/agent");

function compareTree(source, installed) {
  for (const entry of readdirSync(source)) {
    if (entry === "node_modules") continue;
    const from = join(source, entry), to = join(installed, entry);
    if (statSync(from).isDirectory()) compareTree(from, to);
    else assert.deepEqual(readFileSync(to), readFileSync(from), `Installed LayerFS file differs: ${relative(checkout, from)}`);
  }
}

verifyCheckout();
for (const [source, installed] of [["api/agent/sdk", agent], ["api/agent/cli", cli], ["api/runtime", runtime]]) {
  assert.deepEqual(readFileSync(installed), readFileSync(join(checkout, source, "package.json")));
  compareTree(join(checkout, source, "src"), join(installed, "../src"));
}
console.log("Installed packages match the pinned checkout; one shared agent SDK");
