import assert from "node:assert/strict";
import { writeFile, rename, mkdir, symlink } from "node:fs/promises";
import { test } from "node:test";
import { join } from "node:path";
import { buildCommand } from "../src/command.mjs";
import { shellResult, testProject } from "./support.mjs";

test("shell metacharacters and binary stdin arrive literally", async (t) => {
  const project = await testProject(t);
  await writeFile(join(project.bin, "mainline"), `#!${process.execPath}\nlet chunks=[]; for await (const c of process.stdin) chunks.push(c); console.log(JSON.stringify({args:process.argv.slice(2),input:Buffer.concat(chunks).toString('base64')}));\n`, { mode: 0o755 });
  const args = ["append", "quotes ' \" `touch escaped` $(touch escaped) ; \\ \n中文", "--json", ""];
  const input = Buffer.from([0, 255, 10, 39, 36, 96]);
  const result = await shellResult(buildCommand(args, input), project.repo, project.env);
  assert.equal(result.exitCode, 0, result.stderr);
  assert.deepEqual(JSON.parse(result.stdout), { args, input: input.toString("base64") });
  const status = await project.git("status", "--porcelain");
  assert.equal(status.stdout, "");
});

test("a linked worktree pointer is refused before Mainline executes", async (t) => {
  const project = await testProject(t);
  await rename(join(project.repo, ".git"), join(project.directory, "outside-git"));
  await writeFile(join(project.repo, ".git"), "gitdir: ../outside-git\n");
  const result = await shellResult(buildCommand(["version"]), project.repo, project.env);
  assert.equal(result.exitCode, 78);
  assert.match(result.stderr, /standalone .git directory/);
});

test("external alternate objects and symlink metadata are refused", async (t) => {
  const project = await testProject(t);
  await mkdir(join(project.repo, ".git", "objects", "info"), { recursive: true });
  await writeFile(join(project.repo, ".git", "objects", "info", "alternates"), "/outside/objects\n");
  const result = await shellResult(buildCommand(["version"]), project.repo, project.env);
  assert.equal(result.exitCode, 78);
  assert.match(result.stderr, /alternate object stores/);
  // A distinct repository exercises the separate symlink refusal.
  const other = await testProject(t);
  await symlink("/outside/config", join(other.repo, ".git", "external"));
  const symlinkResult = await shellResult(buildCommand(["version"]), other.repo, other.env);
  assert.equal(symlinkResult.exitCode, 78);
  assert.match(symlinkResult.stderr, /symlinks/);
});
