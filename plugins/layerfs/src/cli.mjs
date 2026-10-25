import { readFile } from "node:fs/promises";
import { constants } from "node:os";
import { errorRecord, OperationError, PluginError } from "./errors.mjs";
import { runMainline } from "./run.mjs";
import { launchAgent } from "./launch.mjs";
import { MAX_INPUT_BYTES } from "./command.mjs";

export async function readInputFile(path) {
  if (path !== "-") return readFile(path);
  const chunks = [];
  let size = 0;
  for await (const chunk of process.stdin) {
    size += chunk.length;
    if (size > MAX_INPUT_BYTES) throw new PluginError("InvalidInput", `stdin must be at most ${MAX_INPUT_BYTES} bytes`);
    chunks.push(chunk);
  }
  return Buffer.concat(chunks);
}

export const usage = `mainline-layerfs run [--project NAME] --sandbox NUMBER --branch BRANCH [--input FILE|-] -- MAINLINE_ARGS...
mainline-layerfs launch -- AGENT [ARGS...]

run prints JSON containing Mainline's output and LayerFS checkpoint.
Project defaults to LAYERFS_PROJECT. --input - reads stdin (maximum 64 KiB).
launch keeps the macOS/Windows runtime alive while the agent runs.
See plugins/layerfs/README.md for installation and LAYERFS_* settings.
`;

export function parseRun(argv, env) {
  const boundary = argv.indexOf("--");
  if (boundary < 0) throw new PluginError("Usage", "Separate plugin options from Mainline arguments with --");
  const options = { project: env.LAYERFS_PROJECT, args: argv.slice(boundary + 1) };
  const seen = new Set();
  const names = { "--project": "project", "--sandbox": "sandbox", "--branch": "branch", "--input": "inputPath" };
  for (let i = 0; i < boundary; i += 2) {
    const name = names[argv[i]];
    if (!name || seen.has(name) || i + 1 >= boundary || argv[i + 1].startsWith("--")) {
      throw new PluginError("Usage", `Invalid, repeated or missing plugin option: ${argv[i]}`);
    }
    seen.add(name);
    options[name] = argv[i + 1];
  }
  return options;
}

export async function cli(argv, {
  env = process.env,
  platform = process.platform,
  stdout = process.stdout,
  stderr = process.stderr,
  readInput = readInputFile,
  open = async (project) => (await import("@layerfs/agent")).LayerFS.open(project),
  openRuntime = async (settings) => (await import("@layerfs/runtime")).LayerFSRuntime.open(settings),
  runChild,
} = {}) {
  if (argv.length === 1 && ["help", "--help", "-h"].includes(argv[0])) {
    stdout.write(usage);
    return 0;
  }
  try {
    if (argv[0] === "run") {
      const options = parseRun(argv.slice(1), env);
      if (options.inputPath !== undefined) options.input = await readInput(options.inputPath);
      const result = await runMainline(options, { open, platform, env });
      stdout.write(`${JSON.stringify(result)}\n`);
      return result.execution.exitCode;
    }
    if (argv[0] === "launch" && argv[1] === "--") {
      const result = await launchAgent(argv.slice(2), { openRuntime, runChild, platform, env });
      return result.exitCode ?? (128 + (constants.signals[result.signal] ?? 1));
    }
    throw new PluginError("Usage", usage.trimEnd());
  } catch (error) {
    stderr.write(`${JSON.stringify(error instanceof OperationError ? error.record : errorRecord(error, "input"))}\n`);
    return error.code === "Usage" || error.code === "InvalidInput" ? 2 : 125;
  }
}
