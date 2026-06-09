# Mainline

[![CI](https://github.com/mainline-org/mainline/actions/workflows/ci.yml/badge.svg)](https://github.com/mainline-org/mainline/actions/workflows/ci.yml)
[![Go 1.22+](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: layered](https://img.shields.io/badge/License-Apache--2.0%20%2F%20CC--BY--4.0%20%2F%20Commercial-blue.svg)](#授权)

- 官网：https://mainline.sh
- Hosted Hub：https://mainline.sh/hub/
- 详细文档：[docs/reference.zh.md](./docs/reference.zh.md)
- English version: [README.md](./README.md)

**代码 review 还不够，AI 时代要先 review 意图。**

Mainline 是一个面向 coding agent 的 Git-native memory layer。它在 diff 出现
前，把这个 repo 的历史决策、约束、废弃方案、验证记录和正在推进的相关工作交给
agent 和 reviewer。

AI agent 让代码变得很便宜，也让 review 变得更难。Mainline 让意图先变得可
review，再让代码进入 review。

先 review 意图，再 review 代码。

<img width="1600" alt="Mainline Hub 展示一条 sealed engineering intent" src="https://github.com/user-attachments/assets/71bd98d0-64db-4f41-86eb-342dbafbdfc3" />

## 问题是什么

过去做 code review，diff 往往还不算太大。人写代码慢，reviewer 可以从改动里
慢慢反推作者意图。

Agent 改变了这件事。

它可以很快生成一大段 diff。真正难 review 的，反而不是某一行代码，而是更前面
的几个问题：

- 这次到底是不是在解决正确的问题；
- agent 有没有理解历史决策；
- 它是不是重复了早就废弃的方案；
- 它有没有漏掉 reviewer 之前强调过的约束；
- 旁边是不是还有另一个 agent 在做相关工作；
- 它的验证方式，是否真的对应这次变更的原因。

如果 reviewer 只看到最后的 diff，就只能在代码里猜意图。猜对了还好，猜错了，
问题就会被 merge 进 repo。

### 一个更现实的故障

一个普通 SaaS 后台。账单导出迁到了新的 `/exports/invoices` API，但老的
`/reports/invoices.csv` 还不能删。

原因不复杂：三个企业客户的财务系统，还在每天凌晨拉这个 CSV 做自动对账。
迁移窗口排在下个季度。前端埋点看不到，普通用户也点不到，代码里只剩一段
compatibility branch。

三周后，agent 接到任务：清理 legacy reporting code。它扫了一圈：新 UI 都走
新 API，老 route 没什么产品流量，测试也不覆盖那几个客户的凌晨任务。

于是它删了。单测通过，dashboard 正常。第二天早上，客户财务对账全断。

关键事实不在 diff 里：**企业客户迁移完成前，不能删除 legacy CSV invoice
export**。

## Mainline 做什么

Mainline 记录一次工程工作的 intent，并在下一个危险改动前把它拿出来。

一条 intent 会说明：

- 用户真正要解决的问题；
- 这次工作为什么存在；
- 做了哪些决策；
- 放弃了哪些方案；
- 验证过什么；
- 哪些 constraints、risks、follow-ups 要留给未来；
- 涉及哪些文件和子系统；
- 有没有相邻的 in-flight work；
- 最后是哪一个 commit 把这件事带进了 `main`。

Mainline 不是 Git 替代品，不是 PR 系统，不是 session recorder，不是 RAG index，
也不是个人产能 dashboard。它是跟着代码一起走的 repo-local engineering memory，
存在 Git refs 和 Git notes 里。用 `mainline log`、`mainline show <id>` 或
`mainline hub open` 阅读这些记忆。

## 为什么不是加注释就够了

好的注释当然要写。局部 invariant、边界条件、奇怪实现，都应该写清楚。

但注释很难承担 repo-level intent：

- agent 可能还没打开那个文件，就已经定了改动计划；
- 决策可能跨服务、跨发布步骤、跨客户迁移窗口；
- 被放弃的方案，往往已经不在当前代码里；
- 注释很难告诉你另一个 agent 正在做什么；
- 注释过期以后，很难看出它是否仍然有效；
- reviewer 需要在看 diff 前看到意图，而不是在 diff 里猜意图。

Mainline 不赌下一次 agent 一定会读到正确注释。它把这些事实变成可检索、可
review、可协作的 intent layer。

## 安装

安装 CLI：

```bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | bash
mainline doctor --setup
```

也可以用 Go 安装：

```bash
go install github.com/mainline-org/mainline@latest
```

预编译 archive 和 checksums 在
[GitHub Releases](https://github.com/mainline-org/mainline/releases/latest)。

## 让 Agent 用起来

每个 repo 初始化一次：

```bash
cd your-repo
mainline init --actor-name "alice"
```

`mainline init` 会写入 repo-local Mainline 状态，配置需要的 Git refs，安装
Mainline skill，并给 Codex、Claude Code、Cursor 等支持的 agent 安装 hooks。

Hooks 会在 session start 跑 `mainline sync` 和 `mainline status`，让 agent 一
开始就拿到新鲜的 repo 状态。但 hooks 不替 agent 做语义判断。什么时候读
context、什么时候 append、怎么 seal、是否有 conflict，仍然由 agent 按 Mainline
skill workflow 执行。

已有项目接入时，`mainline init` 会把当前 `main` HEAD 当作 coverage baseline。
更早的历史默认 skipped；之后的新 commit 应该有 intent coverage。

## Agent 跑什么

非平凡工作时，agent-facing loop 是：

```bash
mainline context --current --json
mainline start "<用户的目标>"
mainline append "<有意义的进展>"
mainline seal --prepare --json > .ml-cache/seal.json
mainline seal --submit --json < .ml-cache/seal.json
```

`context` 是 pre-edit gate。只读诊断或只给方案的工作可以停在只读检查后；在任务
进入非平凡编辑或其他需要持久记录的工程工作前，不应该跑 `start`。之后 `start`
才认领这一单工作。`append` 记录有工程意义的 turn：决策、pivot、完成的 slice，
或会改变信心的 validation。`seal` 把这次工作变成可 review 的 intent：summary、
decisions、rejected alternatives、validation notes 和 semantic fingerprint。

架构调整、重构、迁移、删除、auth/billing/permissions/data-model、release/CI，
以及“这个能不能删？”、“以前试过吗？”这类问题，都应该先跑 Mainline。

Typo、纯格式化、一行明显语法修复，通常不用。

## 工作流位置

Mainline 不替换你的 Git 流程，它贴在旁边：

1. **改代码前**，agent 用 `mainline context` 读取相关 intent。
2. **工作过程中**，agent 用 `start` 和 `append` 记录有意义的 turn。
3. **review 前**，agent 用 `seal` 写下 decisions、validation notes 和 semantic fingerprint。
4. **review 时**，人类先看 intent 和协作面，再看或同时看 code diff。
5. **merge 后**，`mainline sync` 把 main 上的 commit 和 intent 绑定起来。
6. **下一次改动前**，未来 agent 先读到这段历史。

重点不是多一套仪式。重点是团队 review 的不只是“生成出来的代码”，而是“这件事
到底该不该这样做”。

## CLI 和 Hub

Mainline 有两个入口：

- **CLI 做动作：** 初始化 repo、sync 状态、记录 intent、查看历史、发现 gaps、
  生成 review material。
- **Hub 用来阅读：** 浏览 intent history、pending work、文件级上下文、coverage
  gaps、risks 和协作信号。

已经有至少一条 intent 后，打开 Hub：

```bash
mainline hub open
```

人类常用命令：

```bash
mainline status --actionable
mainline log
mainline show <intent_id>
mainline gaps
```

`mainline hub open` 最适合在 agent 已经产生至少一条 intent 之后打开。新仓库刚
初始化时，Hub 里当然没什么内容；先让 agent 跑完一轮 intent，再打开 Hub 看记录。

静态导出：

```bash
mainline hub export ./mainline-hub
```

如果 fork contributor 也在本地使用 Mainline，优先由 upstream maintainer 显式
接受他的 actor log：

```bash
mainline actor import --actor actor_jiangge --remote jiangge
```

这个命令会从 fork 拉取 `refs/mainline/actors/<actor>/log`，校验事件属于指定
actor，接受进 upstream actor namespace，并 best-effort 拉取 sealed intent 里引用的
fork branch 到 `refs/mainline/imports/<actor>/branches/*`。这样即使 PR 是
squash/rebase merge，upstream 仍能拿到 contributor 原始 code commit/tree object，
再用正常 auto-pin 把 author-sealed intent pin 到 upstream merge commit。Hub 会显示
`accepted_actor_log`、接受者、verified 状态和导入的 code refs。

fork PR 在 contributor 没有 upstream 可见 Mainline actor log 时，才作为
imported external contribution fallback 显示：

```bash
mainline hub export ./mainline-hub --external-contributions fork-prs.json
```

这类记录会标明 `github_pr_imported`、`not author-sealed` 等 provenance /
trust boundary。Hub 不会把 GitHub PR metadata，或 PR body 里空的
`## Mainline Intent` 模板，当成 contributor 自己 sealed 的 Mainline intent。

Mainline 的公开 Hosted Hub 入口是：https://mainline.sh/hub/

安装变体、恢复规则、hooks 行为、webhooks、配置、静态 Hub 发布、存储布局和开发命令，放在
[docs/reference.zh.md](./docs/reference.zh.md)。

## 有效果吗

我们做过一轮受控评测：8 个场景，3 个 seed，2 种模式。

| 模式 | forbidden-list violation | 一致性 |
|---|---:|---|
| Intent-first | 0 | 0/8 fixture 失败 |
| Code-first | 9 | 2/8 fixture 稳定失败 |

优势集中在代码看不出来的地方：abandoned approaches、superseded decisions，
以及源码之外的团队约定。

完整方法论和限制条件见 [docs/eval-results.zh.md](./docs/eval-results.zh.md)。

## 什么时候用

非平凡 agent 工作前使用 Mainline：

- 架构调整；
- 重构和迁移；
- 删除代码；
- auth、billing、permissions、data model；
- release / CI；
- “这个能不能删？”、“以前试过吗？”；
- 任何可能和另一个 agent 或队友相邻的工作。

Typo、纯格式化、一行明显语法修复，通常不用。

## 继续阅读

- 详细参考：[docs/reference.zh.md](./docs/reference.zh.md)
- Eval 报告：[docs/eval-results.zh.md](./docs/eval-results.zh.md)
- Intent Record Spec：[docs/specs/intent-record-v0.md](./docs/specs/intent-record-v0.md)
- Agent Context Protocol：[docs/specs/agent-context-protocol-v0.md](./docs/specs/agent-context-protocol-v0.md)
- 贡献指南：[CONTRIBUTING.md](./CONTRIBUTING.md)
- 安全：[SECURITY.md](./SECURITY.md)
- Changelog：[CHANGELOG.md](./CHANGELOG.md)

## 开发

```bash
go build -o mainline .
make quick-test
make test
make lint
```

核心子系统有 property-based tests。快速 PR gate 是 `make quick-test`；更完整
的 PBT 说明在 [docs/reference.zh.md](./docs/reference.zh.md#开发和测试)。

## 授权

Mainline 使用分层授权。本地 CLI、agent skills、hooks、adapters、libraries 和
protocol specs 尽量开放，方便企业、IDE、agent vendor 和自动化平台集成。
Docs 和 examples 鼓励带署名复用。Hosted service 和品牌资产保留独立边界。

详情见 [docs/reference.zh.md](./docs/reference.zh.md#授权细节) 和
[LICENSE](./LICENSE)。
