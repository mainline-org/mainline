# Mainline for LayerFS

Give coding agents Mainline's Git-native engineering memory inside a LayerFS project.
The adapter runs the existing Mainline CLI in a Linux Workspace, then checkpoints
the whole task tree, including Git refs, notes and local drafts. Each command uses a
fresh Workspace. Agents keep Mainline's normal context, decision and seal workflow.

This is an agent tool and skill integration using `@layerfs/agent`. Its host launcher
uses `@layerfs/runtime` to start the macOS / Windows worker and keeps it alive while
the agent runs. The official Bash `layerfs` command is included from `@layerfs/cli`.

## Install

For a coding agent: execute each step once. If a command fails, stop and report its
exact output. Start in a Mainline checkout containing this plugin. Requirements:
Node 20+, npm 10+, Git, and Go 1.22+ for the default integration test.

1. Clone LayerFS beside Mainline and check out the exact commit from the repository's
   root [`layerfs.json`](../../layerfs.json). If that checkout already exists, inspect
   its HEAD and status before proceeding; preserve any existing changes.

   ```sh
   git clone https://github.com/Ephemeral-AI-Lab/layerfs ../layerfs
   git -C ../layerfs checkout --detach 9f97a7ec95b662e9cb288704afa165feff132cfa
   ```

2. Install into this plugin. The packages are private local packages; nothing needs
   to be published to npm. `install-links=true` copies them here. Install hooks
   verify the pinned checkout, installed sources and one shared copy of the SDK.
   All dependencies are local, so `--offline` works on a fresh npm cache.

   ```sh
   npm --prefix plugins/layerfs ci --offline
   node plugins/layerfs/bin/mainline-layerfs --help
   export PATH="$PWD/plugins/layerfs/bin:$PWD/plugins/layerfs/node_modules/.bin:$PATH"
   layerfs help
   ```

   On Windows, invoke the entry point with `node plugins/layerfs/bin/mainline-layerfs`;
   the official command's npm shim is `plugins/layerfs/node_modules/.bin/layerfs.cmd`.
   On Linux, set `LAYERFS_NATIVE` to the pinned build's Linux addon if the host has not
   already configured it. macOS / Windows agent calls use the host's worker endpoint.
   These installation steps leave the LayerFS checkout unchanged.

3. Install all three skills into a **fresh** directory your agent can load:

   ```sh
   npm --prefix plugins/layerfs run install-skills -- /tmp/mainline-layerfs-skills
   ```

   This copies Mainline's current skill, the integration skill and the pinned
   `layerfs-js-sdk` skill with all references and its MIT license. Configure your
   agent's skill loader to read that directory, or choose an empty repository-local
   `.agents/skills` directory as the destination. Existing skills are preserved; an
   existing destination causes a refusal before any copying.

4. Run the default tests:

   ```sh
   npm --prefix plugins/layerfs test
   ```

   These use public-client stand-ins, real subprocesses and a temporary Git repo
   with a freshly built Mainline CLI. They require no Docker, daemon, native addon,
   Store or worker. The first Go build may need to download Mainline's Go modules.

## Prepare the host and project

Use an existing LayerFS host to initialize a project and create a Sandbox. It must
provide the settings described by the pinned [agent guide](https://github.com/Ephemeral-AI-Lab/layerfs/blob/9f97a7ec95b662e9cb288704afa165feff132cfa/api/how-to-use/agent.md).
The Sandbox image needs Linux Mainline, Git, Bash, coreutils (`timeout`, `base64`) and
findutils. The imported project must contain a complete standalone `.git` directory,
with its objects and refs inside it. Linked worktree pointers, alternate object
stores and symlinks in `.git` are refused with command exit 78.

To extend an existing LayerFS runtime image, build Mainline for that image's Linux
architecture in a separate build directory, then use the included Dockerfile. For
an ARM64 runtime image, from the Mainline repository root:

```sh
mkdir -p /tmp/mainline-layerfs-image
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/mainline-layerfs-image/mainline .
docker build --build-arg LAYERFS_BASE_IMAGE="$LAYERFS_IMAGE" -f plugins/layerfs/Dockerfile --iidfile /tmp/mainline-layerfs-image/image-id /tmp/mainline-layerfs-image
export LAYERFS_IMAGE="$(cat /tmp/mainline-layerfs-image/image-id)"
```

Use `GOARCH=amd64` for an AMD64 base image. The immutable image ID is the value supplied
to a new Sandbox. This extends the existing image; the host supplies LayerFS's sealed
bundle and daemon from the pinned commit. The plugin does not build LayerFS itself.

Each independent agent needs a task Branch and its own Mainline actor identity.
Initialize Mainline in a fresh standalone clone for that actor before importing it
into LayerFS, or explicitly initialize it inside the task. A source snapshot copied
from an already initialized actor would reuse that actor's local identity. Choose
the ordinary Git branch before starting an intent. LayerFS Branch names and Git
branch names identify different things.

### Settings and worker lifetime

The agent reads the host's `LAYERFS_*` environment unchanged, including
`LAYERFS_HOME`, `LAYERFS_PROJECT`, `LAYERFS_STORE_PATH`, `LAYERFS_VOLUME`,
`LAYERFS_IMAGE`, `LAYERFS_DAEMON` and any optional Docker, native, trace and prefix
settings. `--project` overrides `LAYERFS_PROJECT` for one command.

- **Linux:** Store calls run in the calling Linux process, with the configured
  local Store volume mounted there. `launch` just runs the child with its environment.
- **macOS / Windows:** an existing host may supply `LAYERFS_WORKER_ENDPOINT`. Otherwise
  launch the agent through the wrapper, which opens one runtime, starts the worker
  once, supplies `<LAYERFS_HOME>/worker/endpoint.json` to the child, and closes the
  runtime after the child exits. A Windows worker is owned by that runtime process.

```sh
export LAYERFS_BUNDLE=/absolute/path/to/pinned-sealed-linux-bundle
mainline-layerfs launch -- node /absolute/path/to/your-agent.mjs
```

For runtime startup the wrapper maps `LAYERFS_HOME` → `home`, `LAYERFS_IMAGE` → `image`,
`LAYERFS_BUNDLE` → `bundle`, `LAYERFS_DAEMON` → `daemon`, `LAYERFS_DOCKER` → `docker` and
`LAYERFS_CONTAINER_PREFIX` → `containerPrefix`. On Windows optional
`LAYERFS_WSL_DISTRIBUTION`, `LAYERFS_WSL_USER`, `LAYERFS_WSL_NODE`, `LAYERFS_WSL_HOME`
map to the runtime's `wsl` settings. The Windows bundle path is a Linux path inside
that WSL distribution. Automatic sandbox sweeping is disabled for this launcher.

The endpoint file contains a secret. Pass its path to the agent; its contents stay
in the host's private file. The launcher relays SIGINT/SIGTERM to the child before
closing its owned runtime. A host-supplied worker stays under that host's ownership.

## Agent workflow

Given project `shop`, existing Sandbox `1` and task Branch `task-a`:

```sh
export LAYERFS_PROJECT=shop
mainline-layerfs run --sandbox 1 --branch task-a -- preflight --json
mainline-layerfs run --sandbox 1 --branch task-a -- start 'Fix export reconciliation' --json
mainline-layerfs run --sandbox 1 --branch task-a -- append 'Preserved the overnight reconciliation route' --json
```

Each result is JSON with `project`, `layerfsBranch`, `workspace`, `execution`,
`checkpoint`, and `unmounted`. Mainline's original output is in `execution.stdout`
and `execution.stderr`, with its exit code in `execution.exitCode`. Read the readiness
and authority fields from Mainline's returned JSON. A LayerFS checkpoint ID records
the filesystem step; Mainline's Git commit ID records the Git change.

Use the official `layerfs` command or public SDK to edit files, run tests and make
the explicitly authorized Git commits. Follow the SDK skill's mount → exec → commit
→ unmount lifecycle for those commands. Every changed file, including ignored files
and Git metadata, participates in a LayerFS checkpoint.

After committing the code to Git, prepare and submit a Mainline seal:

```sh
mainline-layerfs run --sandbox 1 --branch task-a -- seal --prepare --json > /tmp/layerfs-prepare.json
node -e 'const fs=require("node:fs");const run=JSON.parse(fs.readFileSync("/tmp/layerfs-prepare.json","utf8"));fs.writeFileSync("/tmp/mainline-prepare.json",run.execution.stdout)'
```

Read `/tmp/mainline-prepare.json`, fill its `seal_result_starter` following the Mainline
skill, and write that **SealResult** to `/tmp/seal-result.json`. Then:

```sh
mainline-layerfs run --sandbox 1 --branch task-a --input /tmp/seal-result.json -- seal --submit --json
```

If you explicitly pass Mainline's `--offline` to seal, its actor log is saved but
view rebuilding is deferred. Run an explicit `sync` before reading the sealed
summary from `show`'s `data.view.summary`.

`--input -` reads stdin. Inputs are limited to 64 KiB and are passed as literal bytes.
Arguments are shell-quoted; `--cwd` is rejected to keep execution in the project root.
Each execution has a 110-second timeout and five-second termination grace.

Mainline lifecycle commands and Git delivery remain explicit, governed by its skill
and the user's authorization. The adapter checkpoints a normally returned command's
changes even when its exit code is nonzero. It does not initiate Git commits, seals,
code pushes, project publication, branch merges or replacement Sandboxes itself.

One agent owns each task Branch and runs its commands sequentially. Shared sealed
intent travels through Mainline's ordinary remote refs/notes synchronization. Local
unsealed drafts remain in the task Branch. Mainline's sibling-Git-worktree discovery
does not discover independent LayerFS Branches. Combine code through the existing
Git workflow; treating `.git` as last-writer-wins state in a LayerFS merge could lose
another agent's refs or corrupt Git metadata.

## Refusals and exit codes

| Exit | Meaning |
| --- | --- |
| Mainline command exit | A completed run, with the checkpoint and output on stdout |
| 2 | Invalid plugin arguments; no LayerFS operation was attempted |
| 125 | LayerFS or launcher failure; original error and context on stderr |

Failure JSON retains the original `code`, `message`, `detail`, adds `stage`, and for
`run` includes the Workspace, completed execution/checkpoint and `unmounted` state.
Cleanup errors are separate in `cleanupErrors`, so they cannot replace the original
failure. An inner Mainline exit 125 is distinguishable by a completed-run JSON result
on stdout instead of LayerFS failure JSON on stderr.

Every public operation is attempted once. `Conflict` preserves its detail and marker
files; an unmount refused `Dirty` or `Busy` leaves the Workspace mounted for inspection.
`NoSuchSandbox` and `Capacity` retain the SDK's instructions for the caller.
`Unknown` / `Failed` report potentially uncertain effects. No refusal triggers retry,
replay, worker restart, force-commit, discard or automatic conflict resolution.

## Verification

Default `npm test` checks public-call sequencing and failure custody, safe shell args
and stdin, real child processes, worker routing/lifetime, offline skill installation,
and real Mainline draft/seal/actor-ref/notes persistence over a filesystem stand-in.
It verifies the installed package sources match the pinned checkout. These tests
exercise the adapter; real FUSE, Docker and worker behavior requires a separate smoke
run in a configured host:

```sh
export LAYERFS_SMOKE_SANDBOX=1
export LAYERFS_SMOKE_BRANCH=task-a
npm --prefix plugins/layerfs run test:smoke
```

That opt-in check runs one actual Mainline preflight in the supplied Sandbox and
Branch. On macOS / Windows run it with the host endpoint or through `launch`. Its
absence from default tests keeps installation checks usable without Docker.

See the approved [design](../../docs/layerfs-plugin-design.md), the
[pinned package map](https://github.com/Ephemeral-AI-Lab/layerfs/blob/9f97a7ec95b662e9cb288704afa165feff132cfa/api/README.md), and the vendored skill's
[upstream attribution](skills/layerfs-js-sdk/UPSTREAM.md).
