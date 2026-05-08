# Mainline

[![CI](https://github.com/mainline-org/mainline/actions/workflows/ci.yml/badge.svg)](https://github.com/mainline-org/mainline/actions/workflows/ci.yml)
[![Go 1.22+](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: layered](https://img.shields.io/badge/License-Apache--2.0%20%2F%20CC--BY--4.0%20%2F%20Commercial-blue.svg)](#授权)

- 官网：https://mainline.sh
- 详细文档：[docs/reference.zh.md](./docs/reference.zh.md)
- English version: [README.md](./README.md)

**阻止 AI coding agent 重复旧工程错误。**

Mainline 是一个 Git-native memory layer。Agent 改代码前，它先告诉 agent：
这段代码为什么会变成这样，哪些方案已经试过又放弃，哪些约束 reviewer 以后
还会在意，哪些工作正在并行推进。

它不是 Git 替代品，不是 PR 系统，不是 session recorder，也不是个人产能
dashboard。它是存在 repo 里的工程记忆。

<img width="1600" alt="Mainline Hub 展示把认证迁移到 jwt 的 sealed intent" src="https://github.com/user-attachments/assets/71bd98d0-64db-4f41-86eb-342dbafbdfc3" />

## 问题是什么

团队把认证从 session 迁到 JWT，但特意保留了 legacy `/oauth` middleware：
OAuth callback 在 provider 迁完之前还需要 session state。

三周后，一个 coding agent 看到大部分认证逻辑已经是 JWT，`/oauth` 看起来像
没清干净的旧代码。它顺手删掉 middleware。普通登录还正常，线上 OAuth
callback 却坏了。

代码本身没有告诉 agent 那个关键事实：**不要删除 legacy `/oauth`
middleware；OAuth callback 仍然依赖 session state**。

Mainline 解决的就是这类问题。AI coding agent 写代码很快，但只看代码，它
很难知道：

- 哪些方案试过然后放弃了；
- 哪些旧实现已经被新决策替代；
- 哪些约束是 reviewer 期望未来继续保留的；
- 哪些团队约定不在源码里；
- 哪个队友或 agent 正在做相关 intent。

RAG 能找相似代码。Grep 能确认当前代码事实。Mainline 补的是另一层：
这个 repo 的工程事实。

## Mainline 解决什么

Mainline 记录一次工程工作的 intent，并在下一个危险改动前把它拿出来。

一条 intent 回答：

- 这次改了什么；
- 为什么要做这件事；
- 做了哪些决策；
- 放弃了哪些方案；
- 验证过什么；
- 哪些 constraints、risks、follow-ups 应该留给未来；
- 最后是哪一个 commit 把这件事带进了 `main`。

结果很直接：下一个 agent 在写 diff 前先读到长期记忆。如果这个 repo 有已知
约束、失败方案、被取代决策，或者相关的 in-flight work，agent 可以在犯错前
停下来。

## 它怎么工作

Mainline 不替换你的 Git 流程，它贴在旁边：

1. 在 repo 里跑一次 `mainline init`。
2. 支持 hooks 的 agent 会在每次 session start 自动收到 `sync` + `status`。
3. 非平凡改动前，agent 跑 `mainline context`。
4. 做事过程中，agent 用 `start` 和 `append` 记录有意义的 turn。
5. review 前，agent 用 `seal` 写下 decisions、validation notes 和 fingerprint。
6. merge 之后，`mainline sync` 把 main 上的 commit 和 intent 绑定起来。
7. 人类通过 CLI 或 Hub 看历史、看约束、看 coverage gap。

长期团队数据存在 Git refs 和 Git notes 里；`.ml-cache/` 只是本地缓存，可以
重建。

## 快速开始

安装 CLI：

```bash
curl -fsSL https://raw.githubusercontent.com/mainline-org/mainline/main/install.sh | bash
mainline doctor --setup
```

初始化一个 repo：

```bash
cd your-repo
mainline init --actor-name "alice"
```

打开给人看的阅读界面：

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

Agent-facing loop 通常由 Mainline skill 和 hooks 执行：

```bash
mainline context --current --json
mainline start "<用户的目标>"
mainline append "<有意义的进展>"
mainline seal --prepare --json > .ml-cache/seal.json
mainline seal --submit --json < .ml-cache/seal.json
```

安装细节、完整命令、恢复规则、hooks 行为、配置、存储布局和开发命令，放在
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

非平凡 agent 工作前使用 Mainline：架构调整、重构、迁移、删除、auth、billing、
permissions、data model、release/CI，以及“这个能不能删？”、“以前试过吗？”
这类问题。

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
