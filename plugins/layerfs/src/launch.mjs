import { spawn } from "node:child_process";
import { join } from "node:path";
import { errorRecord, OperationError, PluginError } from "./errors.mjs";

export function spawnAgent(command, args, env) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { env, stdio: "inherit", shell: false });
    const interrupt = () => child.kill("SIGINT");
    const terminate = () => child.kill("SIGTERM");
    const removeHandlers = () => {
      process.off("SIGINT", interrupt);
      process.off("SIGTERM", terminate);
    };
    process.on("SIGINT", interrupt);
    process.on("SIGTERM", terminate);
    child.once("error", (error) => { removeHandlers(); reject(error); });
    child.once("close", (exitCode, signal) => { removeHandlers(); resolve({ exitCode, signal }); });
  });
}

export async function launchAgent(argv, { openRuntime, runChild = spawnAgent, platform = process.platform, env = process.env }) {
  if (!argv.length || argv.some((arg) => typeof arg !== "string" || arg.includes("\0"))) {
    throw new PluginError("InvalidInput", "Pass an agent executable and its arguments after --");
  }
  if (!["linux", "darwin", "win32"].includes(platform)) {
    throw new PluginError("Unsupported", `LayerFS does not support ${platform}`);
  }
  const childEnv = { ...env };
  let runtime, result, failure;
  let stage = "runtime.open";
  const cleanupErrors = [];
  try {
    if (platform !== "linux" && !childEnv.LAYERFS_WORKER_ENDPOINT) {
      for (const name of ["LAYERFS_HOME", "LAYERFS_IMAGE", "LAYERFS_BUNDLE"]) {
        if (!env[name]) throw new PluginError("InvalidInput", `${name} is required to start the worker`);
      }
      const settings = {
        home: env.LAYERFS_HOME,
        image: env.LAYERFS_IMAGE,
        bundle: env.LAYERFS_BUNDLE,
        daemon: env.LAYERFS_DAEMON,
        docker: env.LAYERFS_DOCKER,
        containerPrefix: env.LAYERFS_CONTAINER_PREFIX,
        sweep: false,
      };
      if (platform === "win32") settings.wsl = {
        distribution: env.LAYERFS_WSL_DISTRIBUTION,
        user: env.LAYERFS_WSL_USER,
        node: env.LAYERFS_WSL_NODE,
        home: env.LAYERFS_WSL_HOME,
      };
      runtime = await openRuntime(settings);
      stage = "worker.start";
      await runtime.worker.start();
      childEnv.LAYERFS_WORKER_ENDPOINT = join(env.LAYERFS_HOME, "worker", "endpoint.json");
    }
    stage = "agent";
    result = await runChild(argv[0], argv.slice(1), childEnv);
  } catch (error) {
    failure = errorRecord(error, stage);
  } finally {
    if (runtime) {
      try { await runtime.close(); }
      catch (error) {
        const record = errorRecord(error, "runtime.close");
        if (failure) cleanupErrors.push(record);
        else failure = record;
      }
    }
  }
  if (failure) throw new OperationError({ ...failure, agent: result ?? null, cleanupErrors });
  return result;
}
