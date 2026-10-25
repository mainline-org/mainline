import assert from "node:assert/strict";
import { test } from "node:test";
import { launchAgent, spawnAgent } from "../src/launch.mjs";
import { join } from "node:path";

const env = { LAYERFS_HOME: "/private/home", LAYERFS_IMAGE: "sha256:image", LAYERFS_BUNDLE: "/private/bundle", LAYERFS_PROJECT: "shop", PATH: "/bin" };

for (const platform of ["darwin", "win32"]) {
  test(`${platform} starts once, passes endpoint and closes after the agent exits`, async () => {
    const calls = [];
    const result = await launchAgent(["agent", "a b", "$(literal)"], {
      env, platform,
      openRuntime: async (settings) => {
        calls.push("open");
        assert.equal(settings.home, env.LAYERFS_HOME);
        assert.equal(settings.bundle, env.LAYERFS_BUNDLE);
        assert.equal(settings.sweep, false);
        return { worker: { start: async () => calls.push("start") }, close: async () => calls.push("close") };
      },
      runChild: async (command, args, childEnv) => {
        calls.push("agent");
        assert.equal(command, "agent");
        assert.deepEqual(args, ["a b", "$(literal)"]);
        assert.equal(childEnv.LAYERFS_WORKER_ENDPOINT, join(env.LAYERFS_HOME, "worker", "endpoint.json"));
        assert.equal(childEnv.LAYERFS_PROJECT, "shop");
        return { exitCode: 3, signal: null };
      },
    });
    assert.deepEqual(calls, ["open", "start", "agent", "close"]);
    assert.equal(result.exitCode, 3);
    assert.equal(env.LAYERFS_WORKER_ENDPOINT, undefined);
  });
}

test("Linux and a host-supplied endpoint launch without opening a runtime", async () => {
  for (const [platform, given] of [["linux", env], ["win32", { ...env, LAYERFS_WORKER_ENDPOINT: "/host/endpoint.json" }]]) {
    await launchAgent(["agent"], {
      platform, env: given,
      openRuntime: async () => assert.fail("unexpected runtime"),
      runChild: async (_command, _args, childEnv) => {
        assert.deepEqual(childEnv, given);
        return { exitCode: 0 };
      },
    });
  }
});

test("worker refusal is not retried, agent is not launched and original error survives close", async () => {
  const calls = [];
  await assert.rejects(launchAgent(["agent"], {
    env, platform: "win32",
    openRuntime: async () => ({
      worker: { start: async () => { calls.push("start"); throw Object.assign(new Error("worker owns home"), { code: "Busy", detail: { owned: true } }); } },
      close: async () => { calls.push("close"); throw new Error("still draining"); },
    }),
    runChild: async () => assert.fail("agent must not launch"),
  }), (error) => {
    assert.equal(error.record.code, "Busy");
    assert.deepEqual(error.record.detail, { owned: true });
    assert.equal(error.record.cleanupErrors[0].message, "still draining");
    return true;
  });
  assert.deepEqual(calls, ["start", "close"]);
});

test("missing deployment settings stop before runtime creation", async () => {
  await assert.rejects(launchAgent(["agent"], {
    env: {}, platform: "darwin", openRuntime: async () => assert.fail("unexpected runtime"),
  }), (error) => error.record.message.includes("LAYERFS_HOME"));
});

test("real child executable receives literal args and its exit code is preserved", async () => {
  const result = await spawnAgent(process.execPath, ["-e", "process.exit(process.argv[1] === 'a b;$(literal)' ? 6 : 9)", "a b;$(literal)"], process.env);
  assert.deepEqual(result, { exitCode: 6, signal: null });
});
