import { promisify } from "node:util";
import { execFile } from "node:child_process";
import { cp, mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";

export const execute = promisify(execFile);

export async function testProject(t) {
  const directory = await mkdtemp(join(tmpdir(), "mainline-layerfs-test-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const repo = join(directory, "project"), bin = join(directory, "bin");
  await mkdir(repo);
  await mkdir(bin);
  const env = {
    ...process.env,
    PATH: `${bin}:${process.env.PATH}`,
    GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: "/dev/null",
  };
  for (const name of ["GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_INDEX_FILE", "GIT_CONFIG_COUNT"]) delete env[name];
  for (const name of ["CLAUDE_SESSION_ID", "CODEX_SESSION_ID", "CURSOR_SESSION_ID", "COPILOT_SESSION_ID", "MAINLINE_SESSION_REF", "MAINLINE_SESSION_CLIENT"]) delete env[name];
  const git = (...args) => execute("git", args, { cwd: repo, env });
  await git("init", "-b", "main");
  await git("config", "user.name", "LayerFS test");
  await git("config", "user.email", "layerfs-test@example.invalid");
  await git("config", "commit.gpgsign", "false");
  await writeFile(join(repo, "README.md"), "# Initial project\n");
  await git("add", "README.md");
  await git("commit", "-m", "Initial project");
  // macOS hosts may lack GNU timeout. The real Linux image must provide it.
  // This stand-in removes timeout's three fixed options and runs the actual shell.
  await writeFile(join(bin, "timeout"), '#!/bin/sh\nshift\nshift\nshift\nexec "$@"\n', { mode: 0o755 });
  await writeFile(join(bin, "npx"), "#!/bin/sh\nexit 0\n", { mode: 0o755 });
  return { directory, repo, bin, env, git };
}

export async function shellResult(command, cwd, env) {
  try {
    const result = await execute("bash", ["-c", command], { cwd, env, maxBuffer: 16 * 1024 * 1024 });
    return { exitCode: 0, ...result, truncated: false };
  } catch (error) {
    if (typeof error.code !== "number") throw error;
    return { exitCode: error.code, stdout: error.stdout, stderr: error.stderr, truncated: false };
  }
}

// A filesystem stand-in at the public client boundary. Every mount reads the saved
// tree into a new location, including .git and ignored local Mainline state.
export function snapshotClient(project) {
  let counter = 0;
  const paths = new Map();
  return {
    workspace: {
      async mount(sandbox) {
        const path = join(project.directory, `mount-${++counter}`);
        await cp(project.repo, path, { recursive: true });
        const ws = { sandbox, path };
        paths.set(ws, path);
        return ws;
      },
      async exec(ws, command) { return shellResult(command, paths.get(ws), project.env); },
      async commit(ws) {
        await rm(project.repo, { recursive: true, force: true });
        await cp(paths.get(ws), project.repo, { recursive: true });
        return { kind: "Committed", head: `stand-in-checkpoint-${counter}` };
      },
      async unmount(ws) {
        await rm(paths.get(ws), { recursive: true, force: true });
        paths.delete(ws);
        return { kind: "Unmounted" };
      },
    },
    async close() {},
  };
}
