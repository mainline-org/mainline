import assert from "node:assert/strict";
import { test } from "node:test";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import { runMainline } from "../src/run.mjs";
import { execute, snapshotClient, testProject } from "./support.mjs";

test("real Mainline retains drafts, sealed actor refs and Git notes across fresh Workspaces", { timeout: 240_000 }, async (t) => {
  const project = await testProject(t);
  await execute("go", ["build", "-o", join(project.bin, "mainline"), "."], {
    cwd: fileURLToPath(new URL("../../../", import.meta.url)),
    env: { ...project.env, GOCACHE: join(tmpdir(), "mainline-layerfs-go-cache") },
    maxBuffer: 16 * 1024 * 1024,
  });
  const client = snapshotClient(project);
  const invoke = async (args, input) => {
    const result = await runMainline({ project: "test", sandbox: "1", branch: "task-a", args, input }, {
      open: async () => client, platform: "linux", env: project.env,
    });
    assert.equal(result.execution.exitCode, 0, result.execution.stderr);
    assert.equal(result.unmounted, true);
    return result.execution.stdout;
  };
  await invoke(["init", "--actor-name", "LayerFS integration test", "--json"]);
  const preflight = JSON.parse(await invoke(["preflight", "--no-sync", "--json"]));
  assert.equal(preflight.ok, true);
  await project.git("switch", "-c", "task-git");
  const goal = "Preserve decision memory across isolated commands 'quotes' $(literal)";
  const started = JSON.parse(await invoke(["start", goal, "--json"]));
  assert.equal(started.data.goal, goal);
  const intent = started.data.intent_id;
  const appended = JSON.parse(await invoke(["append", "Keep Git refs and local drafts with the task filesystem", "--json"]));
  assert.equal(appended.data.intent_id, intent);
  const status = JSON.parse(await invoke(["status", "--no-sync", "--json"]));
  assert.equal(status.data.turn_count, 1);
  await writeFile(join(project.repo, "README.md"), "# Initial project\n\nDecision memory survives a fresh mount.\n");
  await project.git("add", "README.md");
  await project.git("commit", "-m", "Preserve the decision record");
  const prepared = JSON.parse(await invoke(["seal", "--prepare", "--json"]));
  const seal = prepared.seal_result_starter;
  assert.equal(seal.intent_id, intent);
  Object.assign(seal.summary, {
    title: "Preserve decision memory across LayerFS commands",
    what: "Updated README.md to explain persistent decision memory across mounts",
    why: "Agents need durable engineering judgment when each command uses a fresh Workspace",
    decisions: [{ point: "Memory storage", chose: "Retain the complete Git repository with each saved tree", rationale: "Actor refs and drafts must survive remounts" }],
    rejected: [], review_notes: ["Verified with real Mainline and temporary Git repositories using a public-client stand-in"],
  });
  seal.fingerprint.tags = ["layerfs", "mainline", "memory"];
  seal.confidence = { summary: 0.95, fingerprint: 0.95 };
  const sealed = JSON.parse(await invoke(["seal", "--submit", "--offline", "--json"], Buffer.from(JSON.stringify(seal))));
  assert.equal(sealed.data.intent_id, intent);
  assert.equal(sealed.data.status, "sealed_local");
  const local = JSON.parse(await invoke(["show", intent, "--json"]));
  assert.equal(local.data.intent.intent_id, intent);
  assert.equal(local.data.intent.status, "sealed_local");
  const refs = await project.git("for-each-ref", "--format=%(refname)", "refs/mainline/actors");
  assert.match(refs.stdout, /refs\/mainline\/actors\/.+\/log/);
  // --offline deliberately skips view rebuilding. Explicit sync replays the saved
  // actor log in a later Workspace; this repository has no remote or network I/O.
  const synced = JSON.parse(await invoke(["sync", "--json"]));
  assert.equal(synced.data.view_rebuilt, true);
  assert.equal(synced.data.fetched, false);
  const shown = JSON.parse(await invoke(["show", intent, "--json"]));
  assert.ok(shown.data.view, "Explicit sync must expose the sealed view in show");
  assert.equal(shown.data.view.intent_id, intent);
  assert.equal(shown.data.view.summary.title, seal.summary.title);
  // Notes are opaque filesystem state too; verify the actual notes ref survives.
  await project.git("notes", "--ref=mainline/intents", "add", "-m", "LayerFS notes persistence fixture", "HEAD");
  await invoke(["version"]);
  const note = await project.git("notes", "--ref=mainline/intents", "show", "HEAD");
  assert.equal(note.stdout.trim(), "LayerFS notes persistence fixture");
});
