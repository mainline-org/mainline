# Mainline

**AI 辅助工程的 git-native intent memory。**

Mainline 让 coding agent 在理解当前代码之前，先理解历史上的 *why*。

一个人用，它是未来自己和下一个 agent 的记忆。
团队用，它让 review 和协作发生在共享 intent 之上。

> English version: [README.md](./README.md)

AI coding agent 写代码很快，但代码本身无法告诉它：

- 哪些方案试过然后放弃了；
- 哪些旧实现已经被新决策替代；
- 哪些团队约定不在源码里；
- 哪些约束 reviewer 期望未来变更继续遵守；
- 哪个队友正在推进相关 intent。

RAG 能找语义相似的代码。
Grep 能验证当前代码事实。
Mainline 提供缺失的第三层：**工程意图**。

避免 AI agent 悄悄推翻昨天的决策、重复已经失败的方案、漏掉 reviewer
关心的约束，或踩到队友正在推进的工作。

Mainline 记录每次工程变更为什么发生：decisions、risks、anti-patterns、
references 和 lifecycle，并在下一个 agent 或人类需要时把这些记录重新呈现出来。

## 谁适合使用 Mainline？

### 个人开发者 / OPC

一个人用 AI agent 开发时，最大问题是连续性。
一个 agent 可能放弃过某个方案、接受过某个风险、替代过某个旧决策。
下一个 agent 不会自动知道这些，除非 intent 被记录下来。

Mainline 给未来的自己和未来的 agent 留下一份"为什么代码是这样"的长期记忆。

Mainline 帮个人开发者：

- 避免重复已经失败的方案；
- 记录一次变更为什么发生；
- 记住哪些旧实现已经被新决策替代；
- 在多个 agent、分支、worktree、未来 session 之间交接上下文；
- 隔几周回来时，仍然知道代码为什么变成这样。

### 团队

团队使用 AI agent 时，最大问题是共享意图。
Reviewer 需要在看 diff 前理解 *why*。
队友需要知道彼此正在推进什么。
未来 agent 需要避免重复旧错误。

Mainline 把单次 AI-assisted change 变成团队共享的工程记忆：

- 在看 diff 前先 review intent；
- 把 decisions、risks、anti-patterns 绑定到工程变更；
- 在 PR 冲突出现前看到 proposed / in-flight intent；
- 保留 abandoned / superseded decisions，避免未来 agent 重复旧错误；
- 检查重要变更是否有 intent coverage；
- 帮新人理解代码背后的历史 *why*。

## Mainline 能带来什么

Mainline 不只是 AI 工作日志。它是贯穿工程协作流程的 intent memory layer：

1. **Agent 开工前记忆**
   Agent 在改代码前读取历史 decisions、risks、anti-patterns、abandoned approaches 和 superseded decisions。

2. **Intent 治理**
   团队可以看到重要变更是否有 intent coverage，sealed intent 质量是否足够，高风险变更是否缺少约束或理由。

3. **人类 review 意图**
   Reviewer 在看 diff 前先看 why、decisions、risks 和 constraints——把 review 从"猜作者意图"变成"验证实现是否符合意图"。

4. **长期决策记忆**
   未来维护者和新人可以理解文件为什么变成这样，哪些方案试过又放弃，哪些决策仍然有效。

5. **意图感知协作**
   团队可以 sync intent logs，看到 proposed / in-flight work，更早发现 overlap 和 conflict，避免在 PR 阶段才发现互相踩踏。

## 谁运行什么？

Mainline 在同一个仓库里有两类用户，他们运行不同的命令。

**你的 AI agent**（Cursor / Claude Code / Codex 等）在编辑前读 intent，
在编辑后写 intent。Agent 的循环：

```bash
mainline status                          # 会话开始时
mainline context --current --json        # 非平凡改动前 — 读历史 decisions / anti_patterns
mainline start "<用户的目标>"            # 认领工作
mainline append "<这次的改动>"           # 每个有意义的 turn 后
mainline seal --prepare > .ml-cache/seal.json   # → 修改 → mainline seal --submit < .ml-cache/seal.json
```

你不用记这些 — `AGENTS.md`（Mainline 在 init 时写入仓库）会告诉
agent 这套协议。现代 agent 每次会话都会读 `AGENTS.md`。

**你**（人类）review intent、浏览历史、检查团队 record 的质量：

```bash
mainline log                             # 最近 ship 了什么
mainline show <intent_id>                # 一个 decision 的完整 record
mainline trace <intent_id>               # 一个 decision 是怎么一步步展开的
mainline hub open                        # 在浏览器里浏览历史
mainline gaps                            # main 上没有 intent 的 commit
mainline lint <intent_id>                # 检查队友 seal 的质量
```

这些你不必输入 — 记住一个 `mainline hub open` 就够了；其余的在你需要
的时候才用。

## 如果你用 Cursor（或 Claude Code / Codex）

那你大概想装 hooks。每个仓库一次性配置：

```bash
mainline init --actor-name "<你的名字>"
mainline hooks install --agent cursor      # 或: --agent claudecode  /  --agent codex
```

完成。从此每次 Cursor 会话开始时，Mainline 会：

1. 跑 `mainline sync`（拉最新的团队 intent）。
2. 跑 `mainline status`（active intent / sync 新鲜度 / 建议）。
3. 把 snapshot 注入到 Cursor 的 system context 作为 `additional_context`。

你的 agent 现在在每次会话开始时就能看到团队最新状态，你不用打任何
命令。Agent 自己跑 workflow 里其余的步骤（start / append / seal /
check），这是 `AGENTS.md` 规定的契约 — Mainline 是 context provider，
不是 workflow driver。

如果你不用支持的 hook agent，你的 AI 工具会读 `AGENTS.md` 并按同样
的协议手动执行 — 两条路径都能工作。

## 这工具解决什么问题

| 痛点 | 没有 Mainline | 有 Mainline |
|---|---|---|
| Agent 又一次删掉你为了 OAuth 故意保留的 legacy `/oauth` middleware | 静默返工，线上故障 | Agent 在产生 diff 前就读到 anti_pattern，停下来 |
| 你忘了三周前为什么选 JWT 不选 session | `git log` 里没有 decision | `mainline show <id>` 返回 title / what / why / decisions / risks |
| 同一个仓里两个 agent 用不同方式解决同一个问题 | 在 PR review 时才发现 | `mainline check` 在 `seal --submit` 时就标出重叠 |
| 新维护者问"这段代码为什么是这样"？ | Slack 考古 | `mainline context --files src/auth/middleware.go` |
| 你想知道 main 上哪些 commit 没有对应的 intent | 没信号 | `mainline gaps` |

> **有效吗？** 8 个场景 × 3 轮对照。只看代码的 agent 触发了 ❌ 9 次
> forbidden-list violation；带历史意图的 agent ✅ 0 次。优势集中在
> abandoned-approach 和 superseded-decision 两类场景——代码无法揭示约束的地方。
> [完整评测报告](./docs/eval-results.zh.md)。

## 五分钟快速上手

带 **[you]** 的行是你输入的；其他行是 agent 跑的（受 `AGENTS.md`
驱动，或装了 hooks 后自动注入）。

```bash
# [you] 每个 repo 一次
cd your-repo
mainline init --actor-name "alice"     # 或先 export MAINLINE_ACTOR_NAME
# 如果你之后才加 git remote，跑：mainline init --rewire

# [you, 可选] 如果你用 Cursor / Claude Code / Codex，每个 repo 一次
mainline hooks install --agent cursor

# [agent] 会话开始
mainline status

# [agent] 非平凡编辑前 — 读历史 intent
mainline context --current --json
# 返回相关历史 intent，带 status / anti_patterns / decisions

# [agent] 认领工作
mainline start "Add JWT auth"

# [agent] 每个有意义的 turn 后
mainline append "Implemented JWT middleware"
mainline append "Added refresh-token rotation"

# [you OR agent] 正常方式 commit 代码
git add . && git commit -m "Add JWT auth"

# [agent] 任务结束时 seal
mainline seal --prepare > .ml-cache/seal.json
# (.ml-cache/ 已被 init 写进 .gitignore，所以这个临时文件不会被 git
# 追踪，也不会触发 dirty-worktree 检查)
# 包里有一个 `seal_result_starter` 字段，已经填好了 intent_id +
# files_touched + subsystems；agent 在此基础上补上
# title/what/why/decisions/risks/anti_patterns/confidence 后提交
mainline seal --submit < .ml-cache/seal.json
# 如果 seal 有问题，会内联打印 soft-lint 摘要；如果有 phase-1 conflict，
# 会显式提示 `mainline check --prepare` 跟进

# [you] 在 GitHub 上开 PR；用 web UI merge

# [you 或 agent] 下次 sync 会自动把 squash commit 绑定到 intent
mainline sync
```

这就是完整的循环。不需要任何特殊的 merge 命令。

跑过几个 intent 之后，跑 `mainline hub open`（你自己跑，或让 agent
建议）— Mainline 会打开一个静态 HTML 浏览器，里面有最近的 intent /
按文件的历史 / risks / supersedes / conflict 关系图。这是给人看的页面；
agent 用上面那些 JSON 命令。

## Mainline 记录什么

一个 sealed intent 包含：

- **Why** — 这次工程工作为什么存在。
- **Decisions** — 团队最终做了哪些决策，带 rationale 和 rejected alternatives。
- **Risks** — reviewer 需要知道的软风险。
- **Anti-patterns** — 未来 agent 必须避免的硬约束。
- **Inherited constraints** — 来自历史 intent 的文件级约束。
- **Lifecycle** — merged、abandoned、superseded、reverted。
- **References** — 可选链接，如 issue、PR、doc、CI run 或外部 session。
- **Commit pins** — intent 和实现 commit 的绑定。

Mainline 不试图保存 AI session 的每一个 token。
它保存未来 agent 和 reviewer 真正需要的长期决策记录。

## CLI 和 Hub

Mainline 有两个一等入口：

- **CLI 用来执行动作**：start、append、seal、lint、sync、context、show、trace。
- **Hub 用来阅读和 review**：查看待审 intent、文件历史约束、重要决策和项目 intent 地形。

知道要做什么时用 CLI。
需要理解发生了什么、什么重要、下一步该看哪里时用 Hub。

---

## FAQ

**Q: Mainline 是 RAG 或 grep 的替代品吗？**

不是。RAG 找语义相似的代码。Grep 验证当前代码事实。Mainline 找代码背后的历史工程意图。正确流程是：`mainline context` → 检查当前代码 → 修改 → seal 新 intent。Mainline 应该在大范围代码搜索前运行，但不替代代码验证。

**Q: Mainline 和 session-memory 工具有什么区别？**

Session-memory 工具记录 AI coding session 里的 prompts、responses、snapshots、tool calls 或 code diffs。它们帮助你回放、回滚或检查一次变更是怎么发生的。Mainline 记录未来工作应该遵守的工程意图：为什么这个变更存在、做了哪些 decisions、接受了哪些 risks、未来 agent 必须避免哪些 anti-patterns、以及这个 intent 后来 merged、abandoned、superseded 还是 reverted。Session history 是有用的证据；Mainline intent 是未来 agent 和 reviewer 的长期工作记忆。

**Q: Mainline 会记录 AI session 吗？**

不会。Mainline 不抓取 transcript、tool calls、token usage 或完整 session timeline。它可以给 intent 附加可选 references（session URL、issue、PR、doc、CI run），但 references 支撑 intent，不替代 intent。Sealed intent 才是长期决策记录。

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

完整的英文文档（架构 / 概念 / 详细命令 / 配置 / 存储布局 /
开发指南 / 路线图）在 [README.md](./README.md)。
