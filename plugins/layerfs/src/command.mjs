import { PluginError } from "./errors.mjs";

export const MAX_INPUT_BYTES = 64 * 1024;

export function shellQuote(value) {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

export function buildCommand(args, input) {
  if (!Array.isArray(args) || !args.length || args.some((arg) => typeof arg !== "string" || arg.includes("\0"))) {
    throw new PluginError("InvalidInput", "Pass Mainline arguments after --; arguments cannot contain NUL");
  }
  if (args.some((arg) => arg === "--cwd" || arg.startsWith("--cwd="))) {
    throw new PluginError("InvalidInput", "Mainline runs at the Workspace root; --cwd is unavailable");
  }
  if (input !== undefined && (!Buffer.isBuffer(input) || input.length > MAX_INPUT_BYTES)) {
    throw new PluginError("InvalidInput", `stdin must be at most ${MAX_INPUT_BYTES} bytes`);
  }
  const script = [
    "set -euo pipefail",
    "unset GIT_DIR GIT_WORK_TREE GIT_COMMON_DIR GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_INDEX_FILE",
    "fail() { printf '%s\\n' \"$1\" >&2; exit 78; }",
    "[ -d .git ] && [ ! -L .git ] || fail 'A standalone .git directory is required; linked worktrees are unsupported'",
    "[ ! -e .git/commondir ] && [ ! -e .git/objects/info/alternates ] || fail 'Git common directories and alternate object stores must be inside this Workspace'",
    "[ -z \"$(find .git -type l -print -quit)\" ] || fail 'Git metadata must be self-contained; symlinks inside .git are unsupported'",
    "[ \"$(git rev-parse --show-toplevel)\" = \"$(pwd -P)\" ] || fail 'Git worktree must be the Workspace root'",
    "[ \"$(git rev-parse --absolute-git-dir)\" = \"$(pwd -P)/.git\" ] || fail 'Git directory must be the Workspace .git directory'",
    "command -v mainline >/dev/null || fail 'Install Linux Mainline in the Sandbox image'",
  ];
  if (input === undefined) {
    script.push('exec mainline "$@" </dev/null');
  } else {
    script.push(
      'input_file=$(mktemp /tmp/mainline-layerfs.XXXXXXXX)',
      "trap 'rm -f -- \"$input_file\"' EXIT",
      `printf '%s' ${shellQuote(input.toString("base64"))} | base64 -d > "$input_file"`,
      'mainline "$@" < "$input_file"',
    );
  }
  return `timeout --signal=TERM --kill-after=5s 110s bash -c ${shellQuote(script.join("\n"))} mainline-layerfs ${args.map(shellQuote).join(" ")}`;
}
