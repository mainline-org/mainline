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

它真正要改变的，是 review 的重心：代码当然还要 review，但只盯 diff 已经
不够了。Agent 时代，更关键的是先 review 意图，以及围绕意图的协作。

<img width="1600" alt="Mainline Hub 展示把认证迁移到 jwt 的 sealed intent" src="https://github.com/user-attachments/assets/71bd98d0-64db-4f41-86eb-342dbafbdfc3" />

## 问题是什么

一个很普通的 SaaS 后台。账单导出迁到了新的 `/exports/invoices` API，但老的
`/reports/invoices.csv` 还不能删。

原因也不复杂：三个企业客户的财务系统，还在每天凌晨拉这个 CSV 做自动对账。
迁移窗口排在下个季度。前端埋点看不到，普通用户也点不到，代码里只剩一段
compatibility branch。

三周后，agent 接到任务：清理 legacy reporting code。它扫了一圈：新 UI 都走
新 API，老 route 没什么产品流量，测试也不覆盖那几个客户的凌晨任务。

于是它删了。单测通过，dashboard 正常。第二天早上，客户财务对账全断。

代码本身没有告诉 agent 那个关键事实：**企业客户迁移完成前，不能删除 legacy
CSV invoice export**。

Mainline 解决的就是这类问题。AI coding agent 写代码很快，但只看代码，它
很难知道：

- 哪些方案试过然后放弃了；
- 哪些旧实现已经被新决策替代；
- 哪些约束是 reviewer 期望未来继续保留的；
- 哪些团队约定不在源码里；
- 哪个队友或 agent 正在做相关 intent。

RAG 能找相似代码。Grep 能确认当前代码事实。Mainline 补的是另一层：
这个 repo 的工程事实。

## 为什么不是加注释就够了

现在很多团队的简单办法是：在关键代码旁边加注释，把重要 fact 写进去，避免
agent 误解。

这当然有用。局部 invariant、边界条件、奇怪实现，都应该写清楚。

但它解决不了 repo-level 的问题：

- agent 可能还没打开那个文件，就已经定了改动计划；
- 决策可能跨多个文件、多个服务、多个发布阶段；
- 被放弃的方案，往往已经不在当前代码里；
- 注释很难告诉你谁正在做相关 intent；
- 注释老了以后，很难看出它是否仍然有效；
- reviewer 需要先看意图，再看 diff，而不是在 diff 里猜意图。

所以 Mainline 不赌下一次 agent 一定会读到正确注释。它把这些事实变成可检索、
可 review、可协作的 repo memory。

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

## 先 Review 意图

Agent 写代码太快了。快到只 review 代码，很多时候已经晚了。

真正要前移的，是 review 意图：

- 这次到底要解决什么问题；
- 这个目标是否撞上旧决策；
- agent 认为哪些约束还有效；
- 哪些 risk 需要 reviewer 先看到；
- 有没有另一个 agent 正在做相邻工作；
- 验证方式是否真的匹配这次变更的原因。

代码 review 仍然重要。但更高杠杆的动作，是在 diff 出现前后，先把 intent
review 清楚。否则团队 review 的只是“写出来的代码”，不是“这件事该不该这样做”。

## 它怎么工作

Mainline 不替换你的 Git 流程，它贴在旁边：

1. 在 repo 里跑一次 `mainline init`。
2. 支持 hooks 的 agent 会在每次 session start 自动收到 `sync` + `status`。
3. 非平凡改动前，agent 跑 `mainline context`。
4. 做事过程中，agent 用 `start` 和 `append` 记录有意义的 turn。
5. review 前，agent 用 `seal` 写下 decisions、validation notes 和 fingerprint。
6. 人类先看 intent 和协作面，再看或同时看 code diff。
7. merge 之后，`mainline sync` 把 main 上的 commit 和 intent 绑定起来。
8. 下一个 agent 在危险改动前读到这段历史。

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
