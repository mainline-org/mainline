# Mainline 参考文档

这份文档承接 README 里不该展开的细节。先看
[README.zh.md](../README.zh.md) 理解问题和主线；需要安装变体、agent 协议、
命令说明、存储结构、开发命令时，再看这里。

## 安装

三种安装方式：

1. 安装脚本：推荐给 macOS / Linux。
2. GitHub Releases：下载并校验指定版本的预编译 archive。
3. `go install`：用 Go 1.22+ 从源码安装。

### macOS / Linux 安装脚本

```bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | bash
```

脚本会从 GitHub Releases 下载当前平台的 archive，用 `checksums.txt` 校验，
再把 `mainline` 安装到第一个可写 PATH 目录：`/usr/local/bin`、
`/opt/homebrew/bin` 或 `~/.local/bin`。

指定版本或安装目录：

```bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | MAINLINE_VERSION=v0.4.2 bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | MAINLINE_INSTALL_DIR="$HOME/.local/bin" bash
```

脚本支持 macOS / Linux 的 `amd64` 和 `arm64`。Windows 用户请从 GitHub
Releases 下载预编译 archive。

### GitHub Releases

从 [GitHub Releases](https://github.com/mainline-org/mainline/releases/latest)
下载预编译二进制。每个 release 包含多平台 archive 和 `checksums.txt`。

```text
mainline_<version>_<os>_<arch>.tar.gz
mainline_<version>_windows_amd64.zip
```

### Go install

```bash
go install github.com/mainline-org/mainline@latest
```

需要 Go 1.22 或更新版本。只有明确要安装未发布开发版时才使用：

```bash
go install github.com/mainline-org/mainline@main
```

### 从源码构建

```bash
git clone https://github.com/mainline-org/mainline
cd mainline
go build -o mainline .
```

随时检查本机配置：

```bash
mainline doctor --setup
```

## 让 Agent 用起来

每个 repo 一次性配置：

```bash
mainline init --actor-name "<你的名字>"
```

`mainline init` 做三件事：

1. 写入 `.mainline/config.toml` 并配置 Git refspecs。
2. 安装 Mainline skill，也就是 agent 的完整 workflow 手册。
3. 安装 Cursor、Claude Code、Codex 等支持的 repo-local hooks。

`init` 新创建的 hook 配置文件会通过 `.git/info/exclude` 保持为当前 clone
本地文件，不进入初始 setup commit。如果仓库原本已经 track 某个 agent hook
文件，Mainline 会尊重这个习惯，把合并后的 hook 更新和其他 init setup 一起
stage。

支持 hooks 的 agent 每次 session start 会自动跑 `mainline sync` 和
`mainline status`，并把 snapshot 注入上下文。Agent 仍然负责语义判断：什么时候
start、append 什么、如何 seal、warning 是否代表真实 conflict。

用 `mainline agents update` 刷新 AGENTS.md guidance。全局安装的 Mainline
skill 需要单独通过 `npx --yes skills update mainline --global --yes` 刷新
（或重新运行对应的 `skills add` 命令）。`mainline init --rewire` 只修复
repo setup，不会重装 skill。

这两个分发表面故意分开：AGENTS guidance 承载 repo-local runtime contract，
global skill 承载完整 workflow 手册。更新其中一个，不代表另一个也已刷新。

已有项目接入 Mainline 时，`mainline init` 会把当前 `main` HEAD 记为 coverage
baseline。这个 commit 及之前的历史显示为 skipped pre-Mainline history。之后
进入 `main` 的 commit 仍然需要正常 intent coverage。重要旧 commit 可以补写：

```bash
mainline start --commits <sha> "<why>"
```

团队如果需要显式 repo policy，可以跑 `mainline agents install` 写入轻量
`AGENTS.md` pointer；这不是必需步骤。

Intent 记录会通过 Git refs 和 Git notes 跟随代码流转。用 `mainline log`、
`mainline show <id>` 或 `mainline hub open` 查看它们。

## Agent 协议契约

Mainline 的核心不是一组命令，而是 coding agent 的行为契约。

- 先读再写：非平凡编辑前先取 repo intent。
- 记录意义，不记录键盘：append decisions、pivot、完成的 slice，以及会改变信心的 validation。
- 显式提升长期信号：constraints、risks、follow-ups 用专门命令，不埋在普通 prose 里。
- 保守恢复：dirty state、stale sync、branch drift、parse failure、conflict warning 不能静默跳过。
- 留下可 review 的 intent：reviewer 应该能用 why、decisions、validation notes 和 explicit signals 去验证实现。

### 什么时候必须取 context

这些场景编辑前应该取 Mainline context：

- 架构调整、重构、迁移、删除、auth、billing、data model、permissions、release/CI、跨文件行为变化；
- “这个能不能删？”、“为什么这里是这样？”、“以前试过这个方案吗？”；
- 触碰带 explicit constraints 或重要 lifecycle warning 的文件。

Typo、纯格式化、一行明显语法修复，或用户明确要求只读查看单个文件，可以跳过。

### Agent 跑什么

```bash
mainline preflight --json
mainline start "<用户的目标>" --json
mainline append "<有意义的 turn>" --json
mainline seal --prepare --json > .ml-cache/seal.json
mainline seal --submit --json < .ml-cache/seal.json
```

`preflight` 是 readiness 和 stop-line gate，会告诉 agent 是继续、先检查 overlap，
还是在生命周期推进前停下。只读诊断或只给方案的工作可以停在只读检查后；在任务
进入非平凡编辑或其他需要持久记录的工程工作前，不应该跑 `start`。之后 `start`
才认领真实工程工作。`append` 记录有意义进展。`seal --prepare` 固化准备提交的
证据。`seal --submit` 写入最终 intent，并输出 lint 或 conflict summary。

Review autonomy 可以推非 `main` 分支并打开或更新 PR，但不授权 push `main`、
merge、release 或 deploy。

append 粒度是工程意义：一个设计选择、完成的 slice、一次 pivot，或会改变信心的
validation。不要每个 shell command 都 append。

如果读到相关 explicit constraint，agent 应该说清楚不会做什么以及为什么。例如：
“我不会删除 legacy `/oauth` middleware，因为 OAuth callback 仍然依赖 session state。”

### 失败时怎么恢复

- Dirty worktree：dirty 文件不明显属于当前任务时，停止并询问。
- 允许 dirty worktree：列出 dirty 文件，并说明为什么可以带着继续。
- Sync stale 或 branch drift：先 sync 或重跑 prepare。
- Seal parse 或 lint error：修 SealResult 后重新 submit。
- Conflict warning：暴露 warning。语义上真实重叠时运行 `mainline check` 或请人判断。
- Constraint conflict：preserve、带 reviewer attention 地 intentionally change，或停止。

### 怎么判断 seal 质量

可信的 sealed intent 应该有具体 `what` 和 `why`，带 rationale 的 decisions，
必要时的 rejected alternatives，明确 files/subsystems，validation notes，以及
对 inherited constraints 的 acknowledgement。

核心问题是：未来 agent 能不能在编辑前读这条 intent，然后避免重复同一个错误？

## 工作流位置

Mainline 保留普通 GitHub / GitLab merge 流程。

```text
Author
  start -> append -> seal --submit
  open PR

Reviewer
  在 Hub、log、show 或 PR description 里读 intent
  在正常 PR 系统里 review code

Merge
  使用普通 merge button

Pin
  下一次 mainline sync 把 merged commit 绑定到 intent
```

`mainline merge` 适合非 PR pipeline，不是 GitHub/GitLab 团队的默认路径。

## Mainline 记录什么

一条 sealed intent 包含：

- 工作为什么存在；
- decisions 和 rationale；
- rejected alternatives；
- validation 和 review notes；
- explicit constraints、risks、follow-ups；
- merged、abandoned、superseded、reverted 等 lifecycle；
- issue、PR、doc、CI run 或外部 session references；
- intent 和 merged commit 的 pin。

长期 action signals 不走默认 seal 路径：

- `mainline guard add`：人工确认、未来 agent 必须遵守的约束。
- `mainline risks add`：面向 reviewer 的结构化失败模式。
- `mainline followups add`：有来源的 deferred work。

Mainline 不保存 AI session 的每个 token。它保存未来 agent 和 reviewer 真正需要的
长期决策记录。

## CLI 和 Hub

两个一等入口：

- CLI 做动作：人类用 `init`、`hub open`、`log`、`show`、`gaps`；agent 用
  `context`、`start`、`append`、`seal`。
- Hub 用来阅读：人类看 pending work、文件约束、重要决策、risks 和 coverage gaps。

本地生成并打开 Hub：

```bash
mainline sync
mainline hub open
```

只生成静态导出：

```bash
mainline hub export ./mainline-hub
open ./mainline-hub/index.html      # macOS
xdg-open ./mainline-hub/index.html  # Linux
```

对于已 merge 的 fork PR，先判断 contributor 是否也使用了 Mainline。如果有，
upstream maintainer 应该显式接受他的 actor log：

```bash
mainline actor import --actor actor_jiangge --remote jiangge
```

`--remote` 可以是已配置的 Git remote，也可以是可 fetch 的 URL。默认会从 fork 拉
`refs/mainline/actors/<actor>/log` 到
`refs/mainline/imports/<actor>/log`，校验 event 都属于指定 actor，然后把这条
actor log 接受到 upstream actor namespace，重建 view，并运行正常的 auto-pin。
如果 upstream remote 已配置，Mainline 会把已接受的 contributor actor ref、
maintainer 写下的 accept event，以及新增 pin notes 推上去；其他 clone 下一次
`mainline sync` 就能看到同一条 author-sealed intent。

这个命令只导入 actor-log intent metadata，不会把 fork 里的 git notes 原样复制到
upstream。notes 是关于 upstream main commit 的 pin 证据，应该由 upstream 这边的
pin 逻辑写入。

maintainer 回填可以和之后接受的 contributor intent 共存。回填 / explicit pin 是
upstream maintainer 的 rescue 记录；accepted fork actor log 是 contributor 自己
sealed 的记录。如果两者指向同一个 merge commit，这个 commit note 可以同时包含两条
intent reference。coverage 仍然是 commit-level：一个 commit 只要有一个或多个有效
intent ref 就是 covered；接受 contributor intent 不应该和已有回填形成 review queue
冲突。

如果 contributor 没有 upstream-visible Mainline actor log，可以传入显式 import
文件，让 Hub 解释这条外部贡献：

```bash
mainline hub export ./mainline-hub --external-contributions fork-prs.json
```

文件可以是数组，也可以是 `{ "external_contributions": [...] }`。每行通常携带
`author_login`、`repository`、`pr_number`、`pr_url`、`merged_commit` 和
`provenance` 等 GitHub PR metadata。Hub 会把这些记录当成 imported / inferred
contribution，而不是作者自己的 Mainline intent：导入时强制
`author_sealed=false`、`not_author_sealed=true`、`verified=false`，并把它关联到
同一 merge commit 上已有的 upstream Mainline intent。这样 Hub 能说明“这个已
merge PR 的原作者是谁”，但不会污染 actor count、review queue、coverage 或 pin
语义。

不要把 GitHub PR body 里空的 `## Mainline Intent` 模板当作 intent 证据。
PR description 是 review-time artifact；Mainline sealed intent 来自 actor log。
GitHub PR import 必须标注 `github_pr_imported` / `inferred` 这类 provenance，并保持
`not_author_sealed`，除非已经接受了真实 actor log。

Hub 输出是本地生成状态，不应提交。

### 用 GitHub Pages 发布 Hub

这个仓库带了 `.github/workflows/hub-pages.yml`。它会 build CLI，跑
`mainline sync`，把 Hub 导出到 `_site`，确认导出里有 intent 数据，再通过 GitHub
Pages 部署这份静态 artifact。

在仓库设置里把 Pages source 设成 **GitHub Actions**。workflow 会在 `main` 更新、
手动触发、每天定时跑一次。这个定时不是装饰：Mainline intent state 也会通过 Git
refs 和 notes 流动，所以 hosted Hub 需要一条不依赖代码 diff 的刷新路径。

fork contributor 是 trust-boundary 场景。upstream repo 只能信任已经由 maintainer
显式接受进 Mainline view 的 actor log。在这之前，Hub 可以显示带 provenance
（`github_pr_imported` 或 `inferred`）和 importer metadata 的 GitHub PR import，
但不能把它展示成 verified contributor-sealed intent。

## 常用命令

Intent inspection 三层：

| Command | 目的 |
|---|---|
| `mainline log` | 列出所有 actor 的 intents。 |
| `mainline show <id>` | 看某条 intent 的结构化结论。 |
| `mainline trace <id>` | 看某条 intent 的内部时间线。 |

人类核心命令：

| Command | 用途 |
|---|---|
| `mainline init` | 初始化当前 repo。 |
| `mainline hub open` | 构建并打开本地 Hub。 |
| `mainline status --actionable` | 显示当前最该处理的事项、原因、风险和下一步命令。 |
| `mainline log` | 查看 intent history、作者、时间和 check 状态。 |
| `mainline show <id>` | 查看 decisions、fingerprint、references 和 explicit signals。 |
| `mainline gaps` | 列出 `main` 上没有 intent coverage 的 commits。 |

Reviewer / maintainer 额外命令：

| Command | 用途 |
|---|---|
| `mainline status` | 当前 intent、sync staleness、counts 和 coverage rollup。 |
| `mainline sync` | fetch remote state、rebuild view、auto-pin merged commits，并暴露 phase-1 overlap warnings。 |
| `mainline lint [<id>]` | advisory seal-quality checks。 |
| `mainline guard add` | 添加人工确认约束。 |
| `mainline risks add` | 添加结构化 explicit risk。 |
| `mainline followups add` | 添加 explicit deferred work。 |
| `mainline check --prepare` | 准备 phase-2 conflict review task package。 |
| `mainline check --submit` | 提交 phase-2 judgment。 |
| `mainline doctor --setup` | 检查安装、refspecs、identity、`.gitignore` 和 policy state。 |
| `mainline init --rewire` | 重新应用 refspec config、notes display refs 和 `.gitignore` 条目。 |

所有命令都支持 `--json`。`--no-sync` 可以跳过 auto-sync wrapper。

## 高级命令

| Command | 什么时候用 |
|---|---|
| `mainline pin <intent> <commit>` | rebase、cherry-pick 或特殊 CI 脚本导致 auto-pin miss 时手动修正。 |
| `mainline merge --intent <id>` | 非 PR pipeline 里 squash 并写 note。 |
| `mainline list-proposals` | 浏览团队里的 proposed intents。 |
| `mainline pr-description --intent <id>` | 生成 PR description markdown。 |
| `mainline publish --intent <id>` | 显式 push actor log。 |
| `mainline thread {new,list,close}` | 把多个 intents 归到一个 thread。 |
| `mainline canonical-hash <id>` | 调试某条 intent 的 canonical hash。 |

## Agent Hooks

`mainline hooks` 是 context provider：它运行机械的 `sync` 和 `status`，并把
snapshot 注入 agent context。它不决定什么时候 start、append、seal 或解决 conflict。

```bash
mainline hooks list-agents
mainline hooks install --agent cursor
mainline hooks install --local-dev
mainline hooks install --bin ./mainline
mainline hooks status
mainline hooks uninstall --agent cursor
mainline hooks disable
mainline hooks enable
```

| Hook event | Mainline action |
|---|---|
| `session_start` | 跑 `mainline sync` 和 `mainline status`，并注入 agent context。 |
| `before_submit_prompt`、`stop`、`subagent_stop`、`session_end` | 只做 webhook fan-out；dispatcher 不碰 engine。 |

开关在 `.mainline/config.toml` 的 `[hooks]` 下。

## Webhook Subscriptions

Intent sealed、sync 发现 conflict、phase-2 check judged 时，Mainline 会发 typed
domain event。外部服务可以订阅：

```bash
mainline webhook add https://hooks.example.com/mainline \
  --events intent_sealed,conflict_detected,sync_completed \
  --secret '$ENV:WEBHOOK_SECRET'
mainline webhook list
mainline webhook test <id>
mainline webhook retry
mainline webhook remove <id>
```

单引号是有意的：Mainline 把 literal `$ENV:WEBHOOK_SECRET` 存进 committed config，
发送时才解析环境变量。

Delivery 是 fire-and-forget。事件先写入 `.ml-cache/webhook-queue/`，再由 detached
subprocess 做 HTTP POST。Payload 使用 `X-Mainline-Signature` 做 HMAC-SHA256 签名。
Agent prompt content 不会进入 webhook payload。

## 配置

`.mainline/config.toml` 是 committed team config。`.mainline/local.toml` 保存本地
actor identity，并且 gitignored。

常见设置：

```toml
[check]
phase1_threshold = 0.10

[sync]
freshness_seconds = 300
stale_threshold_seconds = 86400
auto_check_after_sync = true

[mainline.coverage]
baseline_commit = "..."

[merge]
strategy = "squash"
```

多数团队很少需要手改这些配置。

## 性能调优

`mainline sync` 的瓶颈通常是 Git fetch。可以开启 SSH connection multiplexing：

```ssh-config
Host github.com
  ControlMaster auto
  ControlPath ~/.ssh/sockets/%r@%h-%p
  ControlPersist 600
```

```bash
mkdir -p ~/.ssh/sockets
```

`mainline doctor --setup` 会检查并提示。

## FAQ

**Mainline 是 RAG 或 grep 的替代品吗？**

不是。RAG 找语义相似的代码。Grep 验证当前代码。Mainline 在 agent 搜索和编辑代码前
取历史工程意图。

**和 session-memory 工具有何区别？**

Session-memory 工具记录 prompts、responses、snapshots、tool calls 或 code diffs。
Mainline 记录长期工程 intent：为什么改、做了哪些 decisions、哪些 constraints 未来
必须遵守，以及 intent 是 merged、abandoned、superseded 还是 reverted。

**Mainline 会记录 AI session 吗？**

不会。Mainline 不抓 transcript、tool calls、token usage 或完整 session timeline。
它可以引用外部材料，但 sealed intent 才是长期决策记录。

**数据存在哪里？**

长期团队数据存在 Git。每个 actor 的 log 在 `refs/mainline/actors/<id>/log`；
merged-code pins 在 Git notes：`refs/notes/mainline/intents`。`.ml-cache/` 是本地缓存。

**为什么不用 commit message 或 PR description？**

Commit message 短，并偏最终状态。PR description 是 review-time artifact。Mainline
intent 是 git-backed、可检索、有生命周期、可在编辑前提供给 agent 的记录。

**Mainline 是个人产能 dashboard 吗？**

不是。Mainline 不按 intent 数、速度或 output 给人排名。Hub 关注需要 review 的 work、
历史约束、decision hotspots、lifecycle signals 和 coverage gaps。

## Specs

Mainline 正在探索 engineering intent record 的开放格式。

| Spec | 定义什么 |
|---|---|
| [Intent Record Spec v0.1](specs/intent-record-v0.md) | record fields、lifecycle、schema、constraints taxonomy、Git storage model。 |
| [Agent Context Protocol v0.1](specs/agent-context-protocol-v0.md) | agent retrieval modes、behavior requirements、pre-edit checklist。 |
| [Eval Fixture Spec v0.1](specs/eval-fixtures-v0.md) | intent-first eval 的 fixture format、scoring methodology 和 catalog。 |

## 相关工具边界

| 类别 | 记住什么 | Mainline 边界 |
|---|---|---|
| RAG / code indexing | 相似代码片段和 repo context。 | Mainline 在代码搜索前取 intent。 |
| Grep / agentic code search | 当前代码里的精确证据。 | Mainline 告诉 agent 要先检查哪些历史约束。 |
| AI provenance tools | 哪个 AI 用哪个 prompt/session 生成了哪些代码。 | Mainline 不做 line attribution；它记录 engineering intent。 |
| Session-memory tools | prompts、responses、snapshots、tool calls、code diffs。 | Mainline 可以链接 session，但 intent 才是长期记录。 |
| PR / issue trackers | review 讨论、任务状态、项目流程。 | Mainline 记录 engineering why 和 lifecycle，不做通用项目管理。 |

## 存储布局

```text
.mainline/
  config.toml                    # 团队配置，committed。
  local.toml                     # 本地 actor identity，gitignored。

.ml-cache/                       # 本地缓存，gitignored。
  identity.json
  drafts/
  views/
    mainline.json
    proposed-index.json
    last-sync.json
    phase1-warnings.json
  threads/
  sessions/

git refs:
  refs/mainline/actors/<id>/log
  refs/notes/mainline/intents
```

## 开发和测试

```bash
go build -o mainline .
make quick-test
make test
make test-pbt
make bench
make lint
```

Property-based tests 覆盖核心 invariants：

| Area | Properties |
|---|---|
| `rebuildView` state machine | event replay determinism、status transitions、idempotency。 |
| Pin cascade | strategy priority、commit coverage、squash-merge handling。 |
| `SealSubmit` | snapshot contract、fingerprint completeness、conflict detection。 |
| `detectSealedConflicts` | symmetry、self-no-conflict、overlap monotonicity。 |

快速 PR gate 用 `make quick-test`；快速 PBT 用 `make test`；完整覆盖用
`make test-pbt`。

## 项目结构

```text
mainline/
  main.go
  internal/
    domain/       # Pure types.
    core/         # Canonical JSON, IDs, validators.
    gitops/       # Git CLI wrapper.
    storage/      # .ml-cache 和 .mainline 的文件 I/O。
    engine/       # Init, status, intent, seal, sync, merge, conflict, query.
    agent/        # Agent adapter interface.
    cli/          # Cobra commands.
  docs/
  .github/workflows/ci.yml
```

## Roadmap

当前实现处在 v0.4 线。Release packaging、CI hardening、coverage invariants、
seal snapshot evidence、auto-pin on sync、context reliability、Hub export 和
eval reporting 已经进入工作产品。v0.4 剩余重点是 public-launch polish；v0.5
重点是 reviewer dashboards 和 multi-repo intent threading。

## 社区和安全

- Contributing: [CONTRIBUTING.md](../CONTRIBUTING.md)
- Security reporting: [SECURITY.md](../SECURITY.md)
- Changelog: [CHANGELOG.md](../CHANGELOG.md)
- Bug reports and feature requests: [GitHub issue templates](../.github/ISSUE_TEMPLATE/)

## 授权细节

Mainline 使用分层授权。目标是让本地 developer 和 agent surfaces 容易采用、
嵌入和标准化，同时保护 hosted-service infrastructure 和品牌权益。

| 部分 | 推荐条款 | 目的 |
|---|---|---|
| CLI core | Apache-2.0 | 企业友好、平台友好。 |
| Agent skills、hooks、adapters | Apache-2.0 | 方便 coding agents、IDEs 和 automation platforms 集成。 |
| SDKs 和 libraries | Apache-2.0 | 最大化生态采用和实现复用。 |
| Intent Record Spec 和 Agent Context Protocol | Apache-2.0 | 允许兼容的独立实现，让 Mainline vendor-neutral。 |
| Docs、essays、examples | CC-BY-4.0 或 Apache-2.0 | 鼓励复制、教学、引用和带署名传播。 |
| Logo、name、compatibility marks、brand | 保留商标权。 | 防止其他项目或服务伪装成官方 Mainline。 |
| Hosted cloud、GitHub App、managed PR checks、team dashboards | 商业条款。 | 和 local-first open surfaces 分开。 |
| Hosted search、indexing、analytics、cloud infrastructure | 商业条款 / 不属于 open-source distribution。 | 保留 hosted service 边界。 |

除非用户显式连接 hosted service，否则 repo data 不应该发送到 Mainline Cloud。
