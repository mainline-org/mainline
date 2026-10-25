import assert from "node:assert/strict";
import { test } from "node:test";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import { execute } from "./support.mjs";

const plugin = fileURLToPath(new URL("../", import.meta.url));

test("documented CLI entry runs directly without a native addon", async () => {
  const result = await execute(join(plugin, "bin/mainline-layerfs"), ["--help"]);
  assert.match(result.stdout, /mainline-layerfs run/);
  assert.equal(result.stderr, "");
});

test("included official Bash CLI opens help without a Store or native addon", async () => {
  const result = await execute(join(plugin, "node_modules/.bin/layerfs"), ["help"]);
  assert.match(result.stdout, /layerfs/);
  assert.equal(result.stderr, "");
});

test("installed public packages match the pinned checkout and share one SDK", async () => {
  await execute(process.execPath, [join(plugin, "scripts/verify-packages.mjs")]);
  const { LayerFS } = await import("@layerfs/agent");
  const { LayerFSRuntime } = await import("@layerfs/runtime");
  assert.equal(typeof LayerFS.open, "function");
  assert.equal(typeof LayerFSRuntime.open, "function");
});

test("skills install offline with all references and preserve existing destinations", async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "mainline-layerfs-skills-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  await execute(process.execPath, [join(plugin, "scripts/install-skills.mjs"), directory]);
  assert.match(await readFile(join(directory, "mainline/SKILL.md"), "utf8"), /name: mainline/);
  assert.match(await readFile(join(directory, "layerfs-js-sdk/references/methods/workspace.md"), "utf8"), /workspace.commit/);
  assert.match(await readFile(join(directory, "mainline-layerfs/SKILL.md"), "utf8"), /name: mainline-layerfs/);
  const before = await readFile(join(directory, "mainline/SKILL.md"));
  await assert.rejects(execute(process.execPath, [join(plugin, "scripts/install-skills.mjs"), directory]), (error) => {
    assert.match(error.stderr, /already exists/);
    return true;
  });
  assert.deepEqual(await readFile(join(directory, "mainline/SKILL.md")), before);
});

test("stdin reader accepts fd 0 without requiring the LayerFS native addon", async () => {
  const source = `import {readInputFile} from ${JSON.stringify(new URL("../src/cli.mjs", import.meta.url).href)}; const data=await readInputFile('-'); process.stdout.write(data);`;
  const result = await new Promise((resolve, reject) => {
    import("node:child_process").then(({ execFile }) => {
      const child = execFile(process.execPath, ["--input-type=module", "-e", source], (error, stdout, stderr) => error ? reject(error) : resolve({ stdout, stderr }));
      child.stdin.end('Line 1\n{"seal":true}\n');
    }, reject);
  });
  assert.equal(result.stdout, 'Line 1\n{"seal":true}\n');
  assert.equal(result.stderr, "");
});
