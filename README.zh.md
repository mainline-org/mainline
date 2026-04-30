# Mainline

**Mainline 是为 AI-assisted 工程提供的 git-native intent memory layer。**
它让 coding agent 在读代码之前先理解历史 *why*。

阻止你的 AI agent 在不知情的情况下推翻你昨天的决策、重复一个已被放弃
的方案，或踩到队友正在进行的工作。Mainline 把每一次 AI 改动的 *why*
（decisions / risks / anti-patterns）记录下来，并在下一个 agent（或人）
需要的那一刻把它呈现出来。

> English version: [README.md](./README.md)

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

> **有效吗？** 8 个场景 × 3 轮对照。只看代码的 agent 犯了 ❌ 9 次错；
> 带历史意图的 agent ✅ 0 次。详见
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

---

完整的英文文档（架构 / 概念 / 详细命令 / FAQ / 配置 / 存储布局 /
开发指南 / 路线图）在 [README.md](./README.md)。

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

是的。8 个合成场景 × 3 个独立 seed（真实 LLM 调用，非 replay）：

| 模式 | 违规数 | 一致性 |
|---|---|---|
| **Intent-first** | **所有 seed 均为 0** | 0/8 fixture 失败 |
| Code-first | 9 次违规（每 seed 3 次） | 2/8 fixture 失败，100% 可复现 |

code-first 在**代码无法揭示约束**的场景上 100% 失败：

1. **已废弃方案** — redis.go 看起来 60% 完成，docker-compose 有 Redis 服务。
   每个 code-first agent 都提议完成它。只有 intent 知道 replication-lag 导致废弃。
2. **被取代决策** — CSV 和 Parquet 端点都能工作且有流量。
   每个 code-first agent 都往两个端点加列。只有 intent 说"CSV 已废弃，只改 Parquet"。

intent-first agent 读取 `mainline context`，看到 anti-pattern，并**主动引用约束说明拒绝原因**。

```bash
mainline eval run                                          # layer 1: retrieval 前置条件 (8/8 pass)
mainline eval agent --runner ./scripts/eval-runner-copilot.py \
  --judge ./scripts/eval-judge-copilot.py                  # layer 2: v2 scorer (CF=4, IF=0)
```

完整方法论、live 结果和限制条件 →
[docs/eval-results.zh.md](./docs/eval-results.zh.md)
