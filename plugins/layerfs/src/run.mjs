import { buildCommand } from "./command.mjs";
import { errorRecord, OperationError, PluginError } from "./errors.mjs";

// open(project) supplies the public LayerFS client; tests use the same public surface.
export async function runMainline({ project, sandbox, branch, args, input }, { open, platform = process.platform, env = process.env }) {
  if (!/^[a-z0-9](?:[a-z0-9_.-]{0,61}[a-z0-9])?$/.test(project ?? "")) {
    throw new PluginError("InvalidInput", "A valid LayerFS project is required (--project or LAYERFS_PROJECT)");
  }
  if (typeof sandbox !== "string" || !/^[1-9][0-9]*$/.test(sandbox)) {
    throw new PluginError("InvalidInput", "--sandbox must name an existing Sandbox number");
  }
  if (typeof branch !== "string" || !branch.length || branch === "main" || branch.includes("\0")) {
    throw new PluginError("InvalidInput", "--branch must name a writable task Branch");
  }
  if (!["linux", "darwin", "win32"].includes(platform)) {
    throw new PluginError("Unsupported", `LayerFS does not support ${platform}`);
  }
  if (platform !== "linux" && !env.LAYERFS_WORKER_ENDPOINT) {
    throw new PluginError("InvalidInput", "Set LAYERFS_WORKER_ENDPOINT from your host, or run the agent with mainline-layerfs launch");
  }
  const command = buildCommand(args, input);
  let client, workspace, execution, checkpoint, failure;
  let stage = "open", unmounted = false;
  const cleanupErrors = [];
  try {
    client = await open(project);
    stage = "mount";
    workspace = await client.workspace.mount(sandbox, branch);
    stage = "exec";
    execution = await client.workspace.exec(workspace, command, "Run Mainline in the task Git repository");
    stage = "commit";
    checkpoint = await client.workspace.commit(workspace, `Mainline command (exit ${execution.exitCode})`);
  } catch (error) {
    failure = errorRecord(error, stage);
  } finally {
    if (workspace) {
      try {
        await client.workspace.unmount(workspace);
        unmounted = true;
      } catch (error) {
        const record = errorRecord(error, "unmount");
        if (failure) cleanupErrors.push(record);
        else failure = record;
      }
    }
    if (client) {
      try {
        await client.close();
      } catch (error) {
        const record = errorRecord(error, "close");
        if (failure) cleanupErrors.push(record);
        else failure = record;
      }
    }
  }
  const result = { project, layerfsBranch: branch, workspace: workspace ?? null, execution: execution ?? null, checkpoint: checkpoint ?? null, unmounted };
  if (failure) throw new OperationError({ ...failure, ...result, cleanupErrors });
  return result;
}
