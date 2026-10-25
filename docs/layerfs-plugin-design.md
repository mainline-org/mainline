# Mainline for LayerFS：首版设计

状态：用户已确认，按此设计实现；实际验证结果记录在插件 README 与交付说明。

## 目标与位置

让 LayerFS 中的 coding agent 使用 Mainline 原有的仓库记忆：开工前读取历史判断，工作中记录决策，交接时关联 Git 提交与意图。

插件放在本仓库的 `plugins/layerfs/`，提供 Node 20+ 命令 `mainline-layerfs`、安装说明和 agent skill。根目录 `layerfs.json` 固定 LayerFS 为 `9f97a7ec95b662e9cb288704afa165feff132cfa`；安装时核验同级 `../layerfs` 的 HEAD。LayerFS checkout 保持干净；插件仅从该版本安装公开包。

## 包与公开调用

| 包 | 使用者与职责 | 首版调用 |
| --- | --- | --- |
| `@layerfs/agent` | agent 侧适配器，在已有项目和指定 Sandbox 中执行 Mainline | `LayerFS.open`、`workspace.mount`、`workspace.exec`、`workspace.commit`、`workspace.unmount`、`close` |
| `@layerfs/runtime` | 宿主启动器，为 macOS、Windows 启动 worker 并运行 agent 进程 | `LayerFSRuntime.open`、`worker.start`、`close` |

这是 agent 工具／skill 集成。已有 LayerFS host 负责项目初始化、Sandbox 创建和镜像选择。所有 LayerFS 导入使用包的公开入口。

## 运行位置

- Node 适配器运行在 agent 能调用工具的位置。它读取 `LAYERFS_PROJECT` 及指南规定的 `LAYERFS_*` 设置，显式接收 Sandbox 和 Branch。只挂载任务 Branch；LayerFS 的 `main` 是发布视图。
- Mainline CLI、Git 和任务代码运行在 Sandbox 的 Linux Workspace 中。镜像须已包含 Linux 版 Mainline、Git、Bash 和 `timeout`。
- Workspace 必须包含独立且完整的 Git 仓库。安装检查拒绝指向外部目录的 `.git` 文件或依赖外部对象库的仓库。Mainline 的 refs、notes 和本地草稿随 Workspace 一起保存。
- 每个任务使用自己的 LayerFS Branch；同一任务的命令顺序执行。LayerFS Branch、LayerFS Commit、Git branch 和 Git commit 在输出中分别标识。
- Linux 的 Store 调用只在已挂载 Store 的 Linux 进程中运行。macOS、Windows 的宿主启动器通过 runtime 启动一次 worker，将文档规定的 endpoint 文件路径作为 `LAYERFS_WORKER_ENDPOINT` 传给 agent 子进程，并保持 runtime 存活至子进程结束。已有 host 提供 endpoint 时，直接使用其配置。
- 宿主 runtime 的选项由 `LAYERFS_*` 设置和安装时确认的 bundle 位置映射；agent 侧仅导入 `@layerfs/agent`。

## 命令流程与 agent 所见

示意接口（实现时保持这一职责划分）：

```sh
mainline-layerfs run --project shop --sandbox 1 --branch task-a -- preflight --json
mainline-layerfs run --project shop --sandbox 1 --branch task-a -- start '修复导出逻辑' --json
mainline-layerfs run --project shop --sandbox 1 --branch task-a -- append '确认旧导出仍被夜间对账使用' --json
```

一次 `run` 打开客户端、挂载一个 Workspace、执行一次 Mainline 命令、保存一次 LayerFS Commit，再在 `finally` 中尝试卸载并关闭客户端。Mainline 参数按 shell 参数安全编码，命令用 `timeout` 限时；stdin 可显式传入，以支持 `seal --submit`。输出同时保留 Mainline 的 stdout、stderr、退出码及 LayerFS 保存结果。

命令正常返回非零退出码时，仍按 LayerFS 指南保存其已经产生的文件变化。Mainline 的 `start`、`append`、Git commit、`seal` 和元数据发布由 agent 按 Mainline skill 与用户授权显式决定；适配器负责执行和保存。跨隔离 Branch 的已共享意图通过 Mainline 现有 Git remote 与 refs/notes 同步；未共享草稿保留在所属任务 Branch 中，本机 sibling-worktree 草稿发现机制不会因此扩展到独立的 LayerFS Branch。

为 agent 提供 Mainline skill、该提交的完整 `layerfs-js-sdk` skill（含 references）和集成说明。复制到插件内的上游 skill 保留版本来源，指向 checkout 外部的文档链接改为该提交的固定链接。只有 Bash 的 agent 使用官方 `layerfs` 命令做文件、Branch 和 Sandbox 操作，使用 `mainline-layerfs` 调用 Mainline。

## 拒绝、验证与交付

每个调用只尝试一次。保留 `code`、`message`、`detail`；`Conflict` 保留带标记的 Workspace，`Busy`、`NoSuchSandbox`、`Capacity` 按指南提示交由调用者处理，`Unknown`、`Failed` 报告结果可能不确定。清理不使用 `discard` 或 `force`；清理被拒绝时保留 Workspace 标识及清理错误，不掩盖原始失败。适配器不自动重放命令、创建替代 Sandbox 或解决冲突。

默认测试无需 Docker：公开客户端替身验证调用次数、失败后的状态保留、stdin 与参数转义、worker 的平台路由及环境传递；临时 Git 仓库与真实 Mainline CLI 验证实际命令及记忆保存。真实 LayerFS/Docker 验证单独显式运行，报告其是否执行。

README 的 Install 给出 coding agent 可执行的版本核验、依赖安装、运行环境检查、skill 安装和示例。实现与验证完成后按授权提交并推送功能分支，再读取官方提交页面的 listing prompt 完成登记。

依据：固定提交中的 `api/README.md`、`api/how-to-use/agent.md`、`api/how-to-use/runtime.md`、`.agents/skills/layerfs-js-sdk/SKILL.md` 及其 Workspace／conflict 参考。
