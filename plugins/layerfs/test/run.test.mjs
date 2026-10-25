import assert from "node:assert/strict";
import { test } from "node:test";
import { runMainline } from "../src/run.mjs";
import { cli, parseRun } from "../src/cli.mjs";
import { buildCommand, MAX_INPUT_BYTES } from "../src/command.mjs";

const options = { project: "shop", sandbox: "2", branch: "task-a", args: ["preflight", "--json"] };
const ws = { sandbox: "2", path: "/workspaces/7", image: "sha256:test" };
const refused = (code, detail = null) => Object.assign(new Error(`original ${code}`), { code, detail });

function fixture({ errors = {}, exitCode = 0 } = {}) {
  const calls = [];
  const answer = async (name, args, value) => {
    calls.push([name, ...args]);
    if (errors[name]) throw errors[name];
    return value;
  };
  const client = {
    workspace: {
      mount: (...args) => answer("mount", args, ws),
      exec: (...args) => answer("exec", args, { exitCode, stdout: '{"ok":true}', stderr: "", truncated: false }),
      commit: (...args) => answer("commit", args, { kind: "Committed", head: "layerfs-commit" }),
      unmount: (...args) => answer("unmount", args, { kind: "Unmounted" }),
    },
    close: (...args) => answer("close", args),
  };
  return { calls, deps: { open: (...args) => answer("open", args, client), platform: "linux", env: {} } };
}

test("one mount/exec/checkpoint/unmount, including a nonzero Mainline exit", async () => {
  const { calls, deps } = fixture({ exitCode: 9 });
  const result = await runMainline(options, deps);
  assert.equal(result.execution.exitCode, 9);
  assert.equal(result.checkpoint.head, "layerfs-commit");
  assert.equal(result.layerfsBranch, "task-a");
  assert.equal(result.unmounted, true);
  assert.deepEqual(calls.map(([name]) => name), ["open", "mount", "exec", "commit", "unmount", "close"]);
  assert.deepEqual(calls[0], ["open", "shop"]);
  assert.deepEqual(calls[1], ["mount", "2", "task-a"]);
  for (const [name, workspace] of calls.slice(2, 5)) assert.equal(workspace, ws, name);
  assert.deepEqual(calls[4], ["unmount", ws]);
});

for (const stage of ["open", "mount", "exec", "commit", "unmount", "close"]) {
  test(`${stage} refusal is attempted once and retains the original error`, async () => {
    const original = refused(stage === "exec" ? "Unknown" : "Busy", { evidence: stage });
    const { calls, deps } = fixture({ errors: { [stage]: original } });
    await assert.rejects(runMainline(options, deps), (error) => {
      assert.equal(error.record.code, original.code);
      assert.equal(error.record.message, original.message);
      assert.equal(error.record.detail, original.detail);
      assert.equal(error.record.stage, stage);
      return true;
    });
    assert.equal(calls.filter(([name]) => name === stage).length, 1);
    if (["open", "mount", "exec"].includes(stage)) assert.ok(!calls.some(([name]) => name === "commit"));
    if (stage !== "open") assert.equal(calls.filter(([name]) => name === "close").length, 1);
  });
}

test("Conflict preserves the Workspace and both cleanup refusals without discard", async () => {
  const detail = { conflicts: [{ path: "/a.js", hunksOmitted: true }], conflictsTruncated: true };
  const { calls, deps } = fixture({ errors: { commit: refused("Conflict", detail), unmount: refused("Dirty"), close: refused("Failed") } });
  await assert.rejects(runMainline(options, deps), (error) => {
    assert.equal(error.record.code, "Conflict");
    assert.equal(error.record.detail, detail);
    assert.equal(error.record.workspace, ws);
    assert.equal(error.record.unmounted, false);
    assert.deepEqual(error.record.cleanupErrors.map((e) => e.code), ["Dirty", "Failed"]);
    return true;
  });
  assert.deepEqual(calls.find(([name]) => name === "unmount"), ["unmount", ws]);
  assert.equal(calls.find(([name]) => name === "commit").length, 3);
});

test("NoSuchSandbox is reported without creating or remounting a Sandbox", async () => {
  const { calls, deps } = fixture({ errors: { mount: refused("NoSuchSandbox", { containerGone: true }) } });
  await assert.rejects(runMainline(options, deps), (error) => error.record.code === "NoSuchSandbox");
  assert.deepEqual(calls.map(([name]) => name), ["open", "mount", "close"]);
});

test("non-Linux callers must receive a worker endpoint before opening a client", async () => {
  const { calls, deps } = fixture();
  for (const platform of ["darwin", "win32"]) {
    await assert.rejects(runMainline(options, { ...deps, platform }), /LAYERFS_WORKER_ENDPOINT/);
  }
  assert.deepEqual(calls, []);
  await runMainline(options, { ...deps, platform: "darwin", env: { LAYERFS_WORKER_ENDPOINT: "/private/endpoint.json" } });
  assert.equal(calls[0][0], "open");
});

test("invalid arguments stop before any LayerFS call", async () => {
  const { calls, deps } = fixture();
  for (const patch of [{ branch: "main" }, { sandbox: "0" }, { project: "Bad Project" }, { args: [] }, { args: ["--cwd=/tmp", "status"] }]) {
    await assert.rejects(runMainline({ ...options, ...patch }, deps));
  }
  assert.throws(() => buildCommand(["seal", "--submit"], Buffer.alloc(MAX_INPUT_BYTES + 1)), /stdin/);
  assert.deepEqual(calls, []);
});

test("CLI preserves Mainline output, exit code and explicit stdin", async () => {
  const { deps } = fixture({ exitCode: 7 });
  let output = "", error = "", inputPath;
  const code = await cli(["run", "--sandbox", "2", "--branch", "task-a", "--input", "-", "--", "seal", "--submit"], {
    ...deps, env: { LAYERFS_PROJECT: "shop" },
    readInput: async (path) => { inputPath = path; return Buffer.from('{"input":true}'); },
    stdout: { write: (s) => { output += s; } }, stderr: { write: (s) => { error += s; } },
  });
  assert.equal(code, 7);
  assert.equal(inputPath, "-");
  assert.equal(JSON.parse(output).execution.stdout, '{"ok":true}');
  assert.equal(error, "");
});

test("CLI prints the original LayerFS refusal with exit 125", async () => {
  const { deps } = fixture({ errors: { mount: refused("Capacity", { limit: 16 }) } });
  let error = "";
  const code = await cli(["run", "--project", "shop", "--sandbox", "2", "--branch", "task-a", "--", "status"], {
    ...deps, stderr: { write: (s) => { error += s; } }, stdout: { write() { assert.fail("unexpected stdout"); } },
  });
  assert.equal(code, 125);
  assert.equal(JSON.parse(error).code, "Capacity");
  assert.deepEqual(JSON.parse(error).detail, { limit: 16 });
});

test("plugin parsing leaves Mainline arguments untouched and rejects duplicates", () => {
  const tail = ["append", "text --branch 'quotes' $(touch /tmp/never)", "--json"];
  assert.deepEqual(parseRun(["--branch", "task-a", "--sandbox", "2", "--", ...tail], { LAYERFS_PROJECT: "shop" }).args, tail);
  assert.throws(() => parseRun(["--branch", "a", "--branch", "b", "--", "status"], {}), /repeated/);
  assert.throws(() => parseRun(["--sandbox", "--", "status"], {}), /missing/);
});
