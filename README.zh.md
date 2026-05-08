# Mainline

[![CI](https://github.com/mainline-org/mainline/actions/workflows/ci.yml/badge.svg)](https://github.com/mainline-org/mainline/actions/workflows/ci.yml)
[![Go 1.22+](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![PBT](https://img.shields.io/badge/PBT-property--based%20testing-blueviolet)](#property-based-testing)
[![License: layered](https://img.shields.io/badge/License-Apache--2.0%20%2F%20CC--BY--4.0%20%2F%20Commercial-blue.svg)](#授权结构)

- 官网：https://mainline.sh
- 文档：https://mainline.sh/docs/
- 规范：https://mainline.sh/spec/
- 文章：https://mainline.sh/blog/why-coding-agents-need-repo-memory/

**阻止 AI coding agent 重复旧工程错误。**

Mainline 不是 Git 替代品、PR 系统或 session recorder。它是一个
Git-native memory layer：在 agent 改代码前，先告诉它代码为什么是现在这样。

<img width="1600" alt="Mainline Hub 展示把认证迁移到 jwt 的 sealed intent" src="https://github.com/user-attachments/assets/48edda0e-5d19-4e90-93af-6202775a6321" />

**状态：public alpha。** 核心 CLI、hooks、Hub 和 release packaging 已可用，
但 schema 和 workflow guidance 仍可能随着公开开发者体验打磨而调整。

**License model：** Mainline 采用分层授权结构：CLI core、agent skills /
hooks / adapters、SDKs / libraries 和 protocol specs 走 Apache-2.0；docs、
essays 和 examples 走 CC-BY-4.0 或 Apache-2.0；logo、name 和 brand 保留商标权；
hosted cloud products 走商业条款；hosted search、indexing、analytics 和
cloud infrastructure 不属于 open-source distribution。

Mainline 让 coding agent 在理解当前代码之前，先理解历史上的 *why*。

一个人用，它帮未来的你和下一个 agent 记住来龙去脉。
团队用，它让 review 和协作先对齐 intent，再看 diff。

> English version: [README.md](./README.md)

### 典型失败场景

团队把认证从 session 迁到 JWT，但特意保留了 legacy `/oauth` middleware：
OAuth callback 在 provider 迁完之前还需要 session state。

三周后，一个 coding agent 看到大部分认证逻辑已经是 JWT，`/oauth` 看起来
像没清干净的旧代码。它很可能顺手删掉 middleware。普通登录还过得去，线上
OAuth callback 却会坏。

Mainline 会在改动前把历史 intent 摆出来：**不要删除 legacy `/oauth`
middleware；OAuth callback 仍然依赖 session state**。Agent 先看到这条
anti-pattern，就不会写出错误 diff，而是换一条安全路径。

这就是核心产品：**存在 repo 里的工程记忆，阻止未来 agent 重复旧错误。**

AI coding agent 写代码很快，但只看代码，它不知道：

- 哪些方案试过然后放弃了；
- 哪些旧实现已经被新决策替代；
- 哪些团队约定不在源码里；
- 哪些约束 reviewer 期望未来变更继续遵守；
- 哪个队友正在推进相关 intent。

RAG 能找相似代码。
Grep 能验证当前代码长什么样。
Mainline 补上缺的那一层：**这个 repo 的工程事实**。

它防的是这些事：agent 悄悄推翻昨天的决策，重复已经失败的方案，漏掉
reviewer 关心的约束，或者踩到队友正在推进的工作。

Mainline 记录每次工程变更为什么发生：decisions、explicit risks、constraints、
references 和 lifecycle。下一个 agent 或人类需要时，它再把这些记录拿出来。

## 谁适合使用 Mainline？

### 个人开发者

一个人用 AI agent 开发时，最容易断的是上下文。
上一个 agent 可能放弃过某个方案、接受过某个风险，或者替换过某个旧决策。
下一个 agent 不会自动知道这些，除非你把 intent 记下来。

Mainline 给未来的你和未来的 agent 留下一份长期记忆：代码为什么会变成这样。

Mainline 帮个人开发者：

- 避免重复已经失败的方案；
- 记录一次变更为什么发生；
- 记住哪些旧实现已经被新决策替代；
- 在多个 agent、分支、worktree、未来 session 之间交接上下文；
- 隔几周回来时，仍然知道代码为什么变成这样。

### 团队

团队一起用 AI agent 时，最大问题是大家是不是共享同一份 repo 事实。
Reviewer 在看 diff 前，应该先知道 *why*。
队友需要知道彼此在推进什么。
未来的 agent 也要避开已经踩过的坑。

Mainline 把一次 AI-assisted change 变成团队共享的工程记忆：

- 看 diff 前先 review intent；
- 把 decisions、explicit risks、constraints 绑定到工程变更；
- 在 PR 冲突出现前看到 proposed / in-flight intent；
- 保留 abandoned / superseded decisions，避免未来 agent 重复旧错误；
- 检查重要变更是否有 intent coverage；
- 帮新人理解代码背后的历史 *why*。

### Mainline 在工作流里的位置

- **Agent 改代码前：** 非平凡改动先读 repo intent，再动手。
- **工作过程中：** 重要 turn、方案 pivot、风险和验证结果都写进 intent。
- **Review 之前：** 人类先看 pending intent、文件级约束和历史 decision，
  再看 diff。
- **Merge 之后：** sealed intent 留在 repo 里，成为下一个 agent 或维护者的
  长期记忆。

Mainline 首先是 agent workflow 的默认层，其次才是审计工具。CLI 在 agent
改代码前提供 context；Hub 给人类看 repo intent history、pending work、
risks 和 constraints。

### 如果 Cursor / Copilot / Claude 自带 memory 呢？

Agent vendor memory 通常绑定到**某个 vendor、某个用户、某个 workspace，
或者某段 conversation**。

Mainline 的边界不同：

- **跨 agent：** 同一个 repo intent 可以被 Codex、Claude Code、Cursor、
  Copilot 和人类读取。
- **Git-native：** 长期状态存在 Git refs 和 notes，不在某个 vendor 的
  workspace database 里。
- **可审计：** sealed intent 记录 decisions、risks、rejected approaches、
  constraints、lifecycle 和 commit pins。
- **repo-local：** 记录的是这个 repo 的工程事实，而不只是“我的个人上下文”。

Agent vendor memory 记的是 **my context**。Mainline 记录的是 **this repo's
engineering facts**。

## Mainline 能带来什么

Mainline 不只是 AI 工作日志。它是贯穿工程协作流程的 intent memory layer：

1. **Agent 开工前记忆**
   Agent 在改代码前读取历史 decisions、explicit constraints、abandoned approaches 和 superseded decisions。

2. **Intent 治理**
   团队可以看到重要变更是否有 intent coverage，sealed intent 质量是否足够，高风险变更是否缺少约束或理由。

3. **人类 review 意图**
   Reviewer 在看 diff 前先看 why、decisions、risks 和 constraints——把 review 从"猜作者意图"变成"验证实现是否符合意图"。

4. **长期决策记忆**
   未来维护者和新人可以理解文件为什么变成这样，哪些方案试过又放弃，哪些决策仍然有效。

5. **意图感知协作**
   团队可以 sync intent logs，看到 proposed / in-flight work，更早发现 overlap 和 conflict，避免在 PR 阶段才发现互相踩踏。

## 谁运行什么？

Mainline 在同一个仓库里有两层：

- **Human CLI and Hub** — 安装 Mainline、打开 Hub、浏览历史、查看
  decisions、发现 coverage gaps。
- **Agent protocol** — coding agent 的行为契约：在有风险的编辑前读
  context，记录有意义的 turn，seal intent，并把 conflict 或 anti-pattern
  显式暴露出来，而不是静默推进。

人类不应该被迫学习 agent JSON 协议。人类主线是：

```bash
mainline init                            # 一次性 repo setup
mainline hub open                        # 打开给人看的阅读界面
mainline status --actionable             # 现在最该处理的 Top items
mainline log                             # 最近发生了什么
mainline show <intent_id>                # 一个 decision 为什么存在
mainline gaps                            # main 上哪些 commit 没有 intent
```

Agent protocol 命令（`context`、`start`、`append`、`seal`）通常由 agent
通过 Mainline skill 和 hooks 运行。文档会写清楚这份 contract，目的是让
团队能审计 agent 行为，而不是要求每个普通用户每天手打这些命令。

## 安装

选择一种安装方式：

1. **安装脚本** — 推荐给 macOS 和 Linux 用户。
2. **GitHub Releases** — 下载并校验指定版本的预编译 archive。
3. **Go install** — 使用 Go 1.22+ 从源码构建安装。

### macOS / Linux 脚本安装

```bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | bash
```

安装脚本会从 GitHub Releases 下载当前平台对应的最新 archive，用
`checksums.txt` 校验后，把 `mainline` 安装到第一个可写的 PATH 目录：
`/usr/local/bin`、`/opt/homebrew/bin` 或 `~/.local/bin`。

也可以指定版本或安装目录：

```bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | MAINLINE_VERSION=v0.4.2 bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | MAINLINE_INSTALL_DIR="$HOME/.local/bin" bash
```

脚本支持 macOS / Linux 的 `amd64` 和 `arm64`。Windows 用户请从 GitHub
Releases 下载预编译 archive。

### GitHub Releases

从 [GitHub Releases](https://github.com/mainline-org/mainline/releases/latest)
下载预编译二进制。每个 release 都包含多平台 archive 和用于校验的
`checksums.txt`。

Archive 命名规则：

```text
mainline_<version>_<os>_<arch>.tar.gz
mainline_<version>_windows_amd64.zip
```

### Go install

```bash
go install github.com/mainline-org/mainline@latest
```

需要 Go 1.22 或更新版本。只有在明确想安装当前未发布开发版时，才用
`@main` 替代 `@latest`：

```bash
go install github.com/mainline-org/mainline@main
```

### 从源码构建

```bash
git clone https://github.com/mainline-org/mainline
cd mainline && go build -o mainline .
```

安装后可以随时检查本机配置：

```bash
mainline doctor --setup
```

## 让你的 agent 用起来

每个 repo 一次性配置：

```bash
mainline init --actor-name "<你的名字>"
```

`mainline init` 做三件事：

1. 写入 `.mainline/config.toml` 并配置 git refspecs。
2. 安装 **Mainline skill** — agent 的完整 workflow 说明书。
3. 安装 **repo-local hooks**（Cursor / Claude Code / Codex）— 每次
   会话开始时，hooks 跑 `mainline sync` + `mainline status`，把 snapshot
   注入 agent 的 system context。

你的 agent 现在在每次会话开始时就能看到团队最新状态，你不用打任何
命令。Agent 自己跑 workflow 里其余的步骤（start / append / seal /
check），这是 Mainline skill 规定的契约 — Mainline 是 context provider，
不是 workflow driver。

在已有项目里接入 Mainline 时，`mainline init` 会把当时的 `main` HEAD
记录成 coverage baseline。这个 commit 以及它之前的历史会显示为
pre-Mainline skipped history，不会一上来被算成 uncovered gaps。之后新进
`main` 的 commit 仍然需要正常 intent coverage；如果某些旧 commit 很重要，
可以用 `mainline start --commits <sha> "<why>"` 补一条解释。

如果你的 AI 工具不支持 hooks，它仍然可以通过 Mainline skill 按同样
的协议工作 — 两条路径都能工作。

如果团队需要显式的仓库级策略，`mainline agents install` 会写一个
轻量的 `AGENTS.md` policy pointer — 但这是 opt-in，不是必须的。

## 这工具解决什么问题

| 痛点 | 没有 Mainline | 有 Mainline |
|---|---|---|
| Agent 又一次删掉你为了 OAuth 故意保留的 legacy `/oauth` middleware | 静默返工，线上故障 | Agent 在产生 diff 前就读到人工确认的 constraint，停下来 |
| 你忘了三周前为什么选 JWT 不选 session | `git log` 里没有 decision | `mainline show <id>` 返回 title / what / why / decisions / risks |
| 同一个仓里两个 agent 用不同方式解决同一个问题 | 在 PR review 时才发现 | `mainline check` 在 `seal --submit` 时就标出重叠 |
| 新维护者问"这段代码为什么是这样"？ | Slack 考古 | `mainline context --files src/auth/middleware.go` |
| 你想知道 main 上哪些 commit 没有对应的 intent | 没信号 | `mainline gaps` |

> **有效吗？** 8 个场景 × 3 轮对照。只看代码的 agent 触发了 ❌ 9 次
> forbidden-list violation；带历史意图的 agent ✅ 0 次。优势集中在
> abandoned-approach 和 superseded-decision 两类场景——代码无法揭示约束的地方。
> [完整评测报告](./docs/eval-results.zh.md)。

## 人类五分钟快速上手

这是普通人类路径：安装 Mainline，让 agent 按协议工作，然后用 Hub、log、
show、gaps 理解这个 repo 的工程记忆。

```bash
cd your-repo
mainline init --actor-name "alice"
mainline hub open
mainline log
mainline show <intent_id>
mainline gaps
```

这就够了：安装一次，打开 Hub，看历史，查某个 decision，找还缺 intent
的 commit。

你的 coding agent 通过 Mainline skill 跑 protocol commands。如果 hooks
已安装，agent 在会话开始时也会自动收到最新团队状态。你仍然用正常的
GitHub / GitLab 流程 review 和 merge 代码。

## Agent 协议契约

Mainline 的核心资产不只是 CLI，而是一套让 coding agent 在 repo 中拥有
长期工程记忆的行为规范。

这份 contract 比“偶尔跑个命令”更严格：

- **先读再写：** 非平凡编辑前先取 repo intent。
- **记录意义，不记录键盘：** append 设计选择、完成的 slice、pivot、
  新发现的 risk、会改变信心的 validation。
- **把约束显式化：** 如果读到相关 anti-pattern，agent 要说明不会做什么
  以及为什么。
- **保守恢复：** dirty state、stale sync、branch drift、parse failure、
  conflict warning 都不能静默跳过。
- **留下可 review 的 intent：** 人类 reviewer 应能拿 intent 里的 why、
  decisions、risks、constraints 去验证实现。

### Agent 什么时候必须 call context

非平凡工作在编辑前必须检索 Mainline context：

- 架构调整、重构、迁移、删除、auth / billing / data model /
  permissions、release / CI、跨文件行为变化；
- “这个能不能删？”、“为什么这里是这样？”、“以前试过这个方案吗？”；
- 触碰有人工确认的 constraints、inherited constraints 或历史高风险 decision
  的文件。

Agent 可以跳过 Mainline 的范围很窄：typo、纯格式化、一行明显语法修复，
或用户明确要求只读查看单个文件。如果 agent 不确定一个改动是否 trivial，
就应该运行 `mainline context --current --json`。

### Agent 跑什么

```bash
mainline context --current --json        # 非平凡编辑前
mainline start "<用户的目标>"            # 认领真实工程工作
mainline append "<meaningful turn>"      # 完成一个有意义的逻辑步骤后
mainline seal --prepare > .ml-cache/seal.json
mainline seal --submit < .ml-cache/seal.json
```

`context` 是 pre-edit gate。`start` 认领一个真实工作单元。`append` 记录
有意义的进展。`seal --prepare` 冻结准备提交的证据。`seal --submit` 写入
最终 intent，并输出 lint 或 conflict summary。

append 的粒度是工程意义：一个设计选择、完成的 slice、一次 pivot、发现的
risk，或让信心变化的 validation。不要每个 shell command 都 append。

如果 agent 读到相关 anti-pattern，它应该显式说出不会做什么，例如：
“我不会删除 legacy `/oauth` middleware，因为 OAuth callback 仍然依赖
session state。”

### 失败时怎么恢复

- **Dirty worktree：** 如果 dirty 文件不明显属于当前任务，停止并询问，
  不要继续 append 或 seal。
- **允许 dirty worktree：** 即使用户明确允许 dirty work，agent 也要列出
  dirty 文件，并说明为什么可以带着它们继续。
- **Sync stale / branch drift：** 先 sync 或重跑 prepare；不要提交 stale
  evidence。
- **Seal parse 或 lint error：** 修 SealResult 后重新 submit；不要绕过
  deterministic error。
- **Conflict warning：** 显式暴露 conflict。语义上真实重叠时，运行
  `mainline check` 或请人类判断。
- **Anti-pattern conflict：** 不要静默推进。说明约束，然后 preserve、
  带 reviewer attention 地 intentionally change，或停止。

### Reviewer 怎么判断 intent 可信

可信的 sealed intent 应有具体 `what` 和 `why`、带 rationale 的 decisions、
必要时的 rejected alternatives、明确 files / subsystems / tags、诚实的
validation / review notes，以及对 inherited constraints 的显式 acknowledgement。

Boilerplate summary、模糊 risk、缺失 fingerprint、没有回应 anti-pattern，
都是 review smell。

Reviewer 最核心的问题是：未来 agent 能不能在编辑前读这条 intent，然后避免
重复同一个错误？如果不能，这条 intent 还不够好。

## Mainline 记录什么

一个 sealed intent 默认包含：

- **Why** — 这次工程工作为什么存在。
- **Decisions** — 团队最终做了哪些决策，带 rationale 和 rejected alternatives。
- **Validation 和 review notes** — reviewer 在当前 PR 需要知道的验证与范围说明。
- **Explicit signals** — 通过专门命令提升出来的 constraints、risks、follow-ups。
- **Lifecycle** — merged、abandoned、superseded、reverted。
- **References** — 可选链接，如 issue、PR、doc、CI run 或外部 session。
- **Commit pins** — intent 和实现 commit 的绑定。

Durable action signals 不走默认 seal 路径：

- `mainline guard add` — 人工确认的约束，未来 agent 必须遵守。
- `mainline risks add` — 带 trigger / impact / mitigation / validation / owner 的结构化风险。
- `mainline followups add` — 有明确来源的延期工作。

Mainline 不试图保存 AI session 的每一个 token。
它保存未来 agent 和 reviewer 真正需要的长期决策记录。

## CLI 和 Hub

Mainline 有两个一等入口：

- **CLI 用来执行动作**：人类主要用 `init`、`hub open`、`log`、`show`、
  `gaps`；agent 通过 protocol contract 使用 `context`、`start`、`append`、
  `seal`。
- **Hub 用来阅读和 review**：人类查看待审 intent、文件历史约束、重要决策
  和项目 intent 地形。

知道要做什么时用 CLI。
需要理解发生了什么、什么重要、下一步该看哪里时用 Hub。

本地生成并打开 Hub：

```bash
mainline sync
mainline hub open
```

`mainline hub open` 会基于已 sync 的 Mainline view 重建默认本地 Hub，
并在浏览器里打开。

如果只想生成静态导出，不自动打开浏览器：

```bash
mainline hub export ./mainline-hub
open ./mainline-hub/index.html      # macOS
xdg-open ./mainline-hub/index.html  # Linux
```

Hub 输出目录是本地生成状态，已被 gitignore。不要提交导出的 HTML；如果
文档或 release notes 需要截图，请从本地浏览器打开后的页面截取。

---

## FAQ

**Q: Mainline 是 RAG 或 grep 的替代品吗？**

不是。RAG 找语义相似的代码。Grep 验证当前代码事实。Mainline 找代码背后的历史工程意图。正确流程是：`mainline context` → 检查当前代码 → 修改 → seal 新 intent。Mainline 应该在大范围代码搜索前运行，但不替代代码验证。

**Q: Mainline 和 session-memory 工具有什么区别？**

Session-memory 工具记录 AI coding session 里的 prompts、responses、snapshots、tool calls 或 code diffs。它们帮助你回放、回滚或检查一次变更是怎么发生的。Mainline 记录未来工作应该遵守的工程意图：为什么这个变更存在、做了哪些 decisions、显式确认了哪些 constraints、留下了哪些 review-facing risks、以及这个 intent 后来 merged、abandoned、superseded 还是 reverted。Session history 是有用的证据；Mainline intent 是未来 agent 和 reviewer 的长期工作记忆。

**Q: Mainline 会记录 AI session 吗？**

不会。Mainline 不抓取 transcript、tool calls、token usage 或完整 session timeline。它可以给 intent 附加可选 references（session URL、issue、PR、doc、CI run），但 references 支撑 intent，不替代 intent。Sealed intent 才是长期决策记录。

**Q: Mainline 数据存在哪里？**

Mainline 的长期团队数据存在 Git 里，不依赖 hosted service。每个 actor 的
intent event log 存在 `refs/mainline/actors/<id>/log` 这种 custom Git ref，
刻意不放在 `refs/heads/` 下面，所以 GitHub 不会把它当成最近 push 的分支；
代码合并后的
intent pin 存在 Git notes：`refs/notes/mainline/intents`。`.ml-cache/` 只是
本地工作缓存，用来放 draft、重建后的 view、hook 状态和临时 seal 文件；
它已经被 gitignore，不能提交。`.mainline/config.toml` 是团队配置，会提交；
`.mainline/local.toml` 是本地 actor 配置，也应保持未跟踪。

**Q: 为什么不用 commit message 或 PR description 就够了？**

Commit message 通常很短，只描述最终改动。PR description 是 review 时的临时材料。两者都容易丢失、重写或被跳过。Mainline intent 是 git-backed、可检索、有生命周期的记录。它可以 abandoned、superseded、被文件继承、在编辑前检索，并作为 agent context 使用。

**Q: Mainline 是个人产能 dashboard 吗？**

不是。Mainline 不按 intent 数量、速度或产能给开发者排名。Hub 关注的是行动信号：哪些 work 需要 review、哪些文件有历史约束、哪些文件是 decision hotspots、最近有哪些重要决策、lifecycle 信号、coverage gaps。目标是 intent clarity 和更安全的工程协作，不是监控个人。

**Q: Agent 什么时候应该用 Mainline？**

在非平凡变更前使用：架构调整、重构、迁移、删除、auth/billing/permissions/data-model、跨文件行为变更、"能不能删这个？"、"以前试过这个方案吗？"。如果只是 typo 或格式调整，通常不需要 Mainline。

## Specs

Mainline 正在探索 engineering intent record 的开放格式。
以下 spec 均为 **v0.1-draft** — 实验性、可能变动、欢迎 design partner 反馈。

| Spec | 定义什么 |
|---|---|
| [Intent Record Spec v0.1](docs/specs/intent-record-v0.md) | 记录格式：字段、生命周期、schema、约束分类、git 存储模型。 |
| [Agent Context Protocol v0.1](docs/specs/agent-context-protocol-v0.md) | Agent 如何消费 intent record：检索模式、行为要求、pre-edit checklist。 |
| [Eval Fixture Spec v0.1](docs/specs/eval-fixtures-v0.md) | 如何测试 intent-first agent 是否真的少犯错：fixture 格式、评分方法论、catalog。 |

## Related tools and boundaries

Mainline 属于 AI-assisted engineering 的相邻工具生态。区别在于：到底记住什么。

| 类别 | 记住什么 | Mainline 的边界 |
|---|---|---|
| RAG / 代码索引 | 语义相似的代码片段和 repo context | Mainline 在代码搜索前检索 intent。 |
| Grep / agentic code search | 当前代码里的精确证据 | Mainline 告诉 agent 读代码前应该检查哪些历史约束。 |
| AI provenance 工具 | 哪些代码由哪个 AI / prompt / session 产生 | Mainline 不做 line attribution；它记录工程 intent。 |
| Session-memory 工具 | prompts、responses、snapshots、tool calls、code diffs | Mainline 可以把 session 作为 reference 链接，但 sealed intent 才是长期决策记录。 |
| PR / issue tracker | review 讨论、任务状态、项目流程 | Mainline 记录工程 why 和 intent lifecycle，不做通用项目管理。 |

这些工具可以互补。如果你的团队已经在其他工具里保存 session 或 checkpoint，Mainline 可以把它们作为 references 链接到 sealed intents。

---

## 性能调优

`mainline sync` 的瓶颈是 SSH 到 GitHub 的网络往返（~3s）。开启 SSH 连接复用后，重复 sync 可降到 ~1s：

```ssh-config
# ~/.ssh/config
Host github.com
  ControlMaster auto
  ControlPath ~/.ssh/sockets/%r@%h-%p
  ControlPersist 600
```

```bash
mkdir -p ~/.ssh/sockets
```

首次 sync 仍需完整握手；之后在 `ControlPersist` 窗口内复用连接，省掉 ~2s 的密钥交换。

`mainline doctor --setup` 会自动检测并提示。

---

## Eval: intent-first 真的比 code-first 少犯错吗？

第一轮受控评测：8 个工程场景 × 3 个独立 seed（真实 LLM 调用，非 replay）：

| 模式 | 违规数 | 一致性 |
|---|---|---|
| **Intent-first** | **所有 seed 均为 0** | 0/8 fixture 失败 |
| Code-first | 9 次违规（每 seed 3 次） | 2/8 fixture 稳定失败 |

code-first 在**代码无法揭示约束**的场景上稳定犯错：

1. **已废弃方案** — redis.go 看起来 60% 完成，docker-compose 有 Redis 服务。
   每个 code-first agent 都提议完成它。只有 intent 知道 replication-lag 导致废弃。
2. **被取代决策** — CSV 和 Parquet 端点都能工作且有流量。
   每个 code-first agent 都往两个端点加列。只有 intent 说"CSV 已废弃，只改 Parquet"。

intent-first agent 读取 `mainline context`，看到 anti-pattern，并**主动引用约束说明拒绝原因**。

这不是广义 benchmark 结论，而是一个早期信号：当正确动作依赖 abandoned approaches、
superseded decisions 或代码中不可见的团队约定时，intent memory 能帮助 agent 避免错误。

```bash
mainline eval run                                          # layer 1: retrieval 前置条件 (8/8 pass)
mainline eval agent --runner ./scripts/eval-runner-copilot.py \
  --judge ./scripts/eval-judge-copilot.py                  # layer 2: v2 scorer (CF=4, IF=0)
```

完整方法论、live 结果和限制条件 →
[docs/eval-results.zh.md](./docs/eval-results.zh.md)

---

## 授权结构

Mainline 的授权目标是：本地开发者和 agent 集成面尽量开放，方便企业、
平台和各类 coding agent 采用；hosted service、商标和品牌边界保持可控。

| 部分 | 推荐条款 | 目的 |
|---|---|---|
| CLI core | Apache-2.0 | 企业友好、平台友好，降低采用门槛。 |
| Agent skills / hooks / adapters | Apache-2.0 | 方便各类 agent、IDE 和自动化平台集成 Mainline。 |
| SDKs / libraries | Apache-2.0 | 最大化生态采用和实现复用。 |
| Intent Record Spec 和 Agent Context Protocol | Apache-2.0 | 允许兼容的独立实现，让 Mainline 成为 vendor-neutral protocol。 |
| Docs / essays / examples | CC-BY-4.0 或 Apache-2.0 | 鼓励复制、教学、引用和带署名传播。 |
| Logo / name / compatibility marks / brand | 保留商标权 | 防止其他项目或服务把自己包装成官方 Mainline。 |
| Hosted cloud / GitHub App / managed PR checks / team dashboards | 商业条款 | 作为托管产品和 managed service 的商业化入口。 |
| Hosted search / indexing / analytics / cloud infrastructure | 商业条款 / 不属于 open-source distribution | 保留 hosted service 的运营和基础设施优势。 |

本地开放面应该足够宽，方便企业、agent vendor、IDE 和 developer platform 嵌入。
托管服务则作为单独的商业产品边界。

Mainline Core 应该保持 local-first。除非用户显式连接 hosted service，否则 repo data
不应该发送到 Mainline Cloud。

Mainline 的 name 和 logo 不是 open-source assets。第三方可以描述兼容性，例如
“Compatible with Mainline”、“Implements the Mainline Intent Record Spec” 或
“Works with Mainline”；但未经许可，不能把自己包装成官方 Mainline 项目或服务，
也不能使用 “Official Mainline”、“Mainline Cloud”、“Mainline Enterprise”、
“Mainline Certified” 等容易让人误解为官方背书的名称。

---

完整的英文文档（架构 / 概念 / 详细命令 / 配置 / 存储布局 /
开发指南 / 路线图）在 [README.md](./README.md)。
