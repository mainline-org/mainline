# Mainline · 产品与技术方案（最终版）

> v0.1 final spec
> 状态：开发实施依据
> 替代之前所有版本

---

## 目录

0. [核心结论](#0-核心结论)
1. [产品定位](#1-产品定位)
2. [核心哲学](#2-核心哲学)
3. [核心对象模型](#3-核心对象模型)
4. [Intent 状态机](#4-intent-状态机)
5. [v0.1 默认用户流程](#5-v01-默认用户流程)
6. [命令规范](#6-命令规范)
7. [AGENTS.md](#7-agentsmd)
8. [数据结构](#8-数据结构)
9. [存储与同步设计](#9-存储与同步设计)
10. [Mainline 派生视图](#10-mainline-派生视图)
11. [Merge 与审核流程](#11-merge-与审核流程)
12. [冲突类型与处理](#12-冲突类型与处理)
13. [Trailer 规范](#13-trailer-规范)
14. [语义冲突检测](#14-语义冲突检测)
15. [Agent 集成](#15-agent-集成)
16. [信任与权限](#16-信任与权限)
17. [Git 与 Thread 模型](#17-git-与-thread-模型)
18. [实现架构](#18-实现架构)
19. [Schema 演进](#19-schema-演进)
20. [校验规则](#20-校验规则)
21. [错误处理与失败模式](#21-错误处理与失败模式)
22. [v0.1 范围](#22-v01-范围)
23. [里程碑](#23-里程碑)
24. [验收测试](#24-验收测试)
25. [非功能需求](#25-非功能需求)
26. [产品文案](#26-产品文案)
27. [开发规则](#27-开发规则)
28. [心智模型](#28-心智模型)

---

## 0. 核心结论

Mainline v0.1 是一个**给 coding agent 使用的分布式意图账本**。

它不需要自己的服务器，不需要账号，不需要 API key——它寄生在团队已有的 git remote 上。

**架构核心：**

- 每个开发者维护自己的 append-only event log（一个 git branch）
- 所有 actor logs 在 git remote 上 union，构成"在飞" intent 的全局视图
- main branch 的 commit trailer 锚定"已合入 mainline"的事实
- "主 mainline" 不是物理存储，是从 main + actor logs 派生的本地物化视图
- 没有中心服务，没有写竞争，没有 single point of failure

**核心 axiom：**

> Agent 写代码。Agent 生成语义描述。
> Mainline 校验、固化、索引、合并。
> 人定义信任边界。
>
> 不拥有服务，不拥有模型，不拥有 workflow。
> 借 Git 做复制，借 agent 做智能，Mainline 维护协议和结构。

---

## 1. 产品定位

### 1.1 一句话定义

**Mainline 是 coding agent 的分布式意图账本。**

> Git versions code. Mainline versions intents.

### 1.2 v0.1 目标用户

只服务一类人：

> 已经重度使用 Claude Code / Codex / Cursor / Aider 的开发者，以及他们所在的小团队（2-5 人）。

不试图服务普通 git 用户，也不试图成为通用 VCS。

### 1.3 主要痛点

AI 编程后，代码变化越来越多来自 agent 对话，而不是人手写 commit。三个系统性问题：

1. **决策蒸发**——prompt、约束、被拒绝的方案、架构判断都消失在最终 diff 之外
2. **意图悄悄撞车**——两个分支文本不冲突，但架构假设互相冲突，到 merge 才发现
3. **历史不可重用**——后续 agent 很难知道某段代码为什么存在

### 1.4 不做什么

v0.1 明确不做：

- 不做新的 coding agent
- 不要求用户先进入 Mainline 工作流
- 不直接调用 OpenAI / Anthropic / Gemini API
- 不管理 API key
- 不内嵌本地模型
- 不做 hub / web UI / 中心服务
- 不做账号系统
- 不公开 `mainline run`

---

## 2. 核心哲学

### 2.1 Agent-first

旧流程：

```
mainline start "..."
# Run your agent now
claude
```

新流程：

```
claude
# agent reads AGENTS.md and calls mainline commands
```

**Mainline 的默认用户是 agent**。人类用户负责初始化、审查、合并。

### 2.2 Mainline 不思考，只结构化

- Seal 的 summary/fingerprint 由当前 agent 生成
- Check 的 semantic judgment 由当前 agent 生成
- Mainline 校验、固化、索引

### 2.3 智能不是 core dependency

v0.1 必须在没有任何 LLM 配置的情况下工作。

### 2.4 寄生而非替代

- 寄生在 git 上：所有数据存在 git remote 的 refs 里
- 寄生在 git host 上：用 PR 工作流做审核、用 commit trailer 锚定 merged
- 寄生在 agent 上：智能由 agent 提供
- 寄生在 git host 平台：GitHub/GitLab/Gitea/自建 git 都能用

**不发明新的协作流程**——把 intent 这一层加进 git 已有的协作流程里。

---

## 3. 核心对象模型

```
                   Mainline (派生视图)
                         ▲
                         │
              ┌──────────┴──────────┐
              │                     │
         main branch           actor logs
       (顺序锚点 trailer)      (内容真相)
              │                     │
              ▼                     ▼
            Thread                Intent
                                    │
                                    ▼
                                  Turn
```

### 3.1 Turn

最小记录单位。一次有意义的 agent 工作片段。

例：

```bash
mainline append "Add JWT middleware with bearer token verification"
mainline append "Add refresh token rotation endpoint"
mainline append "Update auth integration tests for JWT flow"
```

属性：turn id、intent id、index、description、changed files、diff stats、timestamp、caller info。

v0.1 不追求捕获 agent 完整对话——只要求 agent 主动调用 `mainline append`。

### 3.2 Intent

核心版本对象。一段有业务或架构意义的工作。

属性：goal、turns、status、summary、decisions、rejected alternatives、semantic fingerprint、code commit、related files、author/agent info。

**Sealed 后内容不可变**。修改 = 创建 superseding intent。

### 3.3 Thread

**Thread 在 v0.1 等价于 git branch**。

不需要 mode 配置。Lazy registration——任何 mainline 命令在未注册的 git branch 上首次运行时，自动注册该 branch 为 thread。

显式创建：

```bash
mainline thread new <name>              # 创建 branch + 注册
mainline thread new <name> --parallel   # 上述 + 创建 worktree
```

并行 worktree 是 thread 的可选属性。

### 3.4 Mainline（主干）

**主 mainline 不在任何固定物理位置。它是从两个 source of truth 派生的本地物化视图：**

| 数据 | Source of truth | 物理位置 |
|---|---|---|
| Intent 的合入顺序 | main branch first-parent history | `refs/heads/main` |
| 单个 intent 的内容 | actor log events | `refs/heads/_mainline/actor/<id>` |
| 主 mainline | 派生视图 | `.ml-cache/views/mainline.json`（本地） |

每个开发者本地都有一份 mainline view。fetch 相同 refs 后视图完全一致。

无中心服务、无写竞争、无 single point of failure。

---

## 4. Intent 状态机

```typescript
type IntentStatus =
  | "drafting"
  | "proposed"
  | "merged"      // 视图层状态：从 main commit trailer 推导
  | "cancelled"
  | "superseded"  // 视图层状态：从 supersedes 事件推导
  | "reverted";   // 视图层状态：从 revert 事件 + main commit 推导
```

**重要：merged / superseded / reverted 不是 actor log 上的字段**——它们是从事件 + main history 推导出的视图状态。

**核心路径：**

```
agent starts work
   │
   ▼
drafting              ← turns 累积，本地私密 (.ml-cache/drafts/)
   │ mainline append...
   │
   ▼
mainline seal --submit
   │ (auto publish to actor log)
   ▼
proposed              ← 已 push 到 actor log，团队 sync 后可见
   │ mainline check
   │ git push code branch
   │ PR review + merge (with Mainline-Intent trailer)
   ▼
merged                ← main commit history 反映
```

**Seal 的物理含义**：把事件 append 到 actor log 并 push。draft 从 `.ml-cache/drafts/` 物理消失。

---

## 5. v0.1 默认用户流程

### 5.1 初始化

```bash
mainline init
```

输出：

```
✓ Mainline initialized
✓ AGENTS.md updated
✓ Actor identity configured (act_<hash>)
✓ Agents can now record intent history with mainline commands
```

### 5.2 用户照常使用 agent

```bash
claude
```

用户："Refactor auth from session to JWT."

Agent 读 AGENTS.md，执行：

```bash
mainline status --json
mainline start "Refactor auth from session to JWT" --json
mainline context auth --json

# 修改代码后
mainline append "Add JWT middleware with bearer token verification" --json
mainline append "Add refresh token rotation endpoint" --json
mainline append "Update auth integration tests for JWT flow" --json

# 完成时
mainline seal --prepare --json > /tmp/seal-input.json
# agent 生成 SealResult JSON
mainline seal --submit /tmp/seal-result.json --json
# seal --submit 自动 publish 到 remote
```

### 5.3 用户审查

```bash
mainline show --latest
```

### 5.4 用户检查冲突

```bash
mainline sync          # fetch 最新 actor logs 和 main
mainline check
```

如果 Phase 1 通过：

```
✓ Checked against merged mainline: no conflicts
✓ Checked against remote proposed intents: no conflicts
```

如果有可疑：

```
⚠ 1 candidate conflict needs semantic judgment

Ask your agent to run:
  mainline check --prepare --json
  mainline check --submit <judgment.json>
```

### 5.5 用户合并

走 GitHub PR 主流程：

```bash
git push origin feature-jwt
# Open PR on GitHub
# PR description 含 Mainline-Intent: int_b91e2f3a trailer
# Reviewer approves
# Squash merge with trailer preserved
```

或后备命令：

```bash
mainline merge
```

---

## 6. 命令规范

所有命令支持：

```
--json          机器可读输出
--quiet         抑制非错误输出
--no-color      去除 ANSI
--cwd <path>    指定工作目录
```

CLI binary 名: **`mainline`**。可选 alias `ml`（用户自配置）。所有官方文档用全名。

### 6.1 `mainline init`

```
mainline init [--no-agents-md] [--actor-name <n>]
```

行为：

1. 检查当前目录是否是 git repo
2. 创建 `.ml-cache/` 目录
3. 在 `~/.config/mainline/identity.toml` 中确保有 actor 身份（首次运行时生成）
4. 写入 `.ml-cache/config.toml`
5. 创建或追加 `AGENTS.md` Mainline section
6. 更新 `.gitignore` 加入 `.ml-cache/`
7. **不要求**用户配置模型或 API key

`.gitignore` 增加：

```
.ml-cache/
```

`~/.config/mainline/identity.toml`：

```toml
actor_id = "act_<hash>"      # sha256(git config user.email)[:16]
display_name = "Alice"
```

Actor ID 派生自 email：同一个 email 永远生成同一个 actor_id。`mainline log` 显示用 git commit author 字段（真实姓名/邮箱）。

### 6.2 `mainline status`

```
mainline status [--json]
```

人类输出：

```
Mainline ready

Thread:
  branch: refactor-auth-jwt
  intent: int_b91e2f3a (drafting)
  turns: 3

Sync:
  last sync: 12 minutes ago
  unpublished intents: 0
  unsynced remote actors: 0

Next:
  mainline append "..."
  mainline seal --prepare
```

JSON：

```json
{
  "ready": true,
  "thread": {
    "name": "refactor-auth-jwt",
    "git_branch": "refactor-auth-jwt"
  },
  "active_intent": {
    "id": "int_b91e2f3a",
    "status": "drafting",
    "turn_count": 3
  },
  "sync": {
    "last_sync_at": "2026-04-25T10:20:00Z",
    "stale": false,
    "unpublished_intents": 0
  },
  "next_actions": ["append", "seal_prepare"]
}
```

### 6.3 `mainline start`

```
mainline start "<goal>" [--json]
```

行为：

1. 当前 git branch 映射为 thread（lazy register if needed）
2. 如果 thread 已有 drafting intent，返回 existing，不报错
3. 创建 intent，写入 `.ml-cache/drafts/<intent-id>.json`
4. 写入 `.ml-cache/session/current.json`

JSON 输出（新建）：

```json
{
  "intent_id": "int_b91e2f3a",
  "status": "drafting",
  "thread": "refactor-auth-jwt",
  "created": true,
  "next": [
    "mainline context <topic> --json",
    "mainline append \"<what changed>\" --json",
    "mainline seal --prepare --json"
  ]
}
```

### 6.4 `mainline append`

```
mainline append "<description>" [--goal "<goal>"] [--allow-empty] [--json]
```

Agent-first 容错命令。

行为：

1. 查找当前 active intent
2. 没有 + 有 `--goal` → 自动创建 intent，再追加 turn
3. 没有 + 没 `--goal` → 返回 agent-friendly error
4. 计算自上次 append 以来的 diff stats
5. 创建 turn，追加到 `.ml-cache/drafts/<intent-id>.turns.jsonl`

JSON 输出：

```json
{
  "intent_id": "int_b91e2f3a",
  "turn_id": "turn_000001",
  "intent_created": true,
  "turn_index": 1,
  "files_changed": ["src/auth/middleware.ts", "src/auth/jwt.ts"],
  "diff_stats": {"files": 2, "added": 84, "removed": 21}
}
```

### 6.5 `mainline context`

```
mainline context <topic> [--json] [--limit <n>]
```

Agent-facing 读命令。基于 sync 后的本地视图查询。

输出包括 merged mainline 中相关 intent + remote proposed intents（标识来源）。

### 6.6 `mainline seal --prepare`

```
mainline seal --prepare [--intent <id>] [--json]
```

**不调用模型，不启动 agent**。准备 seal package 给 agent。

返回 schema、turns 列表、diff summary、instruction。详见数据结构章节。

### 6.7 `mainline seal --submit`

```
mainline seal --submit <file>          # 文件
mainline seal --submit -               # stdin
mainline seal --submit ... --local-only   # 不 push 到 remote（escape hatch）
```

行为（关键流程）：

1. 读 SealResult JSON
2. 校验 schema、文件、状态
3. 如有未提交代码，创建 git commit
4. **创建 sealed 事件**追加到本地 actor log（`refs/heads/_mainline/actor/<id>`）
5. **自动 push** actor log 到 origin（除非 `--local-only`）
6. 删除 `.ml-cache/drafts/<intent-id>.*`
7. 状态变为 `proposed`
8. 更新 `.ml-cache/views/proposed-index.json`

Push 失败处理：

- intent 状态仍变为 proposed（本地 log 已更新）
- 警告："Sealed locally but failed to publish. Run `mainline publish` to retry."
- `mainline publish` 命令存在，专用于补 push

JSON 输出：

```json
{
  "intent_id": "int_b91e2f3a",
  "status": "proposed",
  "sealed": true,
  "published": true,
  "actor_log_commit": "abc123...",
  "summary": {"title": "Refactor auth from session to JWT"}
}
```

### 6.8 `mainline seal`（人类便利）

```
mainline seal
```

如有 pending prepare 或已 submitted result，提示下一步。否则等价于 `mainline seal --prepare` 的人类输出。

**不调用 LLM、不启动 agent**。

### 6.9 `mainline sync`

```
mainline sync [--json] [--actors <pattern>]
```

行为：

1. `git fetch origin main`
2. `git fetch origin 'refs/heads/_mainline/actor/*'`
3. 增量解析新的 actor log events
4. 重建 `.ml-cache/views/mainline.json`（merged 视图）
5. 重建 `.ml-cache/views/proposed-index.json`（活跃 proposed 视图）
6. 检测 force push（actor log 历史 rewrite）→ 警告
7. 更新 `.ml-cache/views/last-sync.json`

JSON 输出：

```json
{
  "fetched": {
    "main": "a3f8c9d",
    "actor_count": 5,
    "new_events": 12
  },
  "view": {
    "merged_intents": 47,
    "remote_proposed_intents": 3
  },
  "warnings": []
}
```

性能：增量 sync 在 50-actor 团队、稳定网络下应在 3 秒内完成。

### 6.10 `mainline publish`

```
mainline publish [--repair]
```

行为：检查本地 actor log 和 origin 的差异，push 未同步部分。

`--repair` 模式：检测、诊断、尝试修复（用于 seal --submit 时 push 失败的场景）。

### 6.11 `mainline check`

```
mainline check [<intent-id>] [--against <thread>] [--json]
```

行为：

1. 检查上次 sync 时间，stale（默认 > 30 分钟）→ 自动 sync
2. 跑 Phase 1 fingerprint 比对
3. 比对两类对象：
   - **Merged mainline intents**（来自 mainline 视图）
   - **Remote proposed intents**（来自 proposed 视图）
4. 无可疑 → 返回成功
5. 有可疑 + 已有 judgment → 渲染（exit 1 if conflict）
6. 有可疑 + 无 judgment → 提示 agent 怎么补（exit 3）

**冲突处理：**

| 冲突对象 | 默认严重度 |
|---|---|
| 与 merged intent 冲突 | blocking（exit 1） |
| 与 remote proposed intent 冲突 | warning（exit 0 但有警告） |

可在 config.toml 升级：

```toml
[check]
block_on_remote_proposed_conflict = true   # 严格模式
```

退出码：

```
0   no conflict（含 warning）
1   blocking conflict
2   check failed
3   semantic judgment required
```

### 6.12 `mainline check --prepare` / `--submit`

```
mainline check --prepare [--intent <id>] [--json]
mainline check --submit <file>
```

行为同 v0.1 之前的设计：Mainline 准备 judgment task，agent 生成 JSON，Mainline 校验存储。详见 §14。

### 6.13 `mainline merge`

```
mainline merge [<intent-id>] [--yes]
```

人类拥有的命令。**主推荐路径是 GitHub PR merge button**（见 §11），此命令是后备。

行为：

1. **强制先 sync**——确保看到最新世界
2. 验证 intent 是 proposed 且属于当前 thread
3. 跑 mainline check（必须通过）
4. 执行 git merge（fast-forward / merge / squash 用户选）
5. 创建 merge commit，**注入 trailer**：
   ```
   Mainline-Intent: int_b91e2f3a
   Mainline-Seal: sha256:<hash>
   ```
6. 原子 push（如果支持）：
   ```bash
   git push --atomic origin main refs/heads/_mainline/actor/<id>
   ```
7. 不支持 atomic 时按顺序：先 push main，再 push actor log
8. push 失败 → 提供 `mainline repair`

### 6.14 `mainline pr-trailer`

```
mainline pr-trailer [--intent <id>] [--json]
```

输出可粘贴到 PR description 的 trailer：

```
Mainline-Intent: int_b91e2f3a
Mainline-Seal: sha256:9f82a3...
```

用户复制到 GitHub PR description。GitHub squash merge 在合理配置下保留 trailer 进 commit message。

### 6.15 Thread 命令

```
mainline thread new <n> [--from <branch>] [--parallel]
mainline thread list [--json]
mainline thread close [<n>] [--delete-branch]
```

### 6.16 读命令

```
mainline log [--mainline] [--thread <n>]
mainline show [<intent-id>] [--latest] [--turns]
mainline why <file>[:<line>]                  # v0.1 可选
mainline blame <file>                         # v0.1 可选
mainline search "<query>"                     # v0.1 可选
mainline list-proposals                       # 列出 remote proposed
```

---

## 7. AGENTS.md

`mainline init` 写入项目根：

```markdown
## Mainline

<!-- mainline-agents-md-version: 1 -->

This project uses Mainline to record the intent behind AI-assisted code changes.
You are expected to use Mainline while modifying code.

### Before changing code

Run:

    mainline status --json

If there is no active intent, start one using the user's goal:

    mainline start "<short description of the user's goal>" --json

Before touching an unfamiliar subsystem, query prior history:

    mainline context <topic> --json

### While working

After each meaningful logical change, record a turn:

    mainline append "<specific description of what changed>" --json

Good examples:
- "Add JWT middleware with bearer token verification"
- "Add refresh token rotation endpoint"
- "Update auth integration tests for JWT flow"

Bad examples:
- "fix stuff"
- "update files"
- "continue"

If there is no active intent:

    mainline append "<what changed>" --goal "<user's goal>" --json

### When the task is complete

Prepare a seal package:

    mainline seal --prepare --json

Generate JSON matching the returned schema. Then submit it:

    mainline seal --submit <file> --json

This will publish the intent to the team. The user will then review and merge.

### Semantic conflict checks

When asked to check semantic conflicts:

    mainline check --prepare --json

Generate JSON matching the returned schema. Then submit it:

    mainline check --submit <file> --json

### Do not run unless explicitly asked by the user

    mainline merge
    mainline revert
    mainline reset
    mainline rebase

### When unsure

    mainline status --json
```

设计规则：简洁、命令化、面向 agent、无哲学、有例子。

---

## 8. 数据结构

### 8.1 Intent

```typescript
interface Intent {
  id: string;                       // int_<8hex>，碰撞检测重试
  schema_version: 1;

  thread: string;
  git_branch: string;

  parent_intent?: string;
  supersedes?: string;
  depends_on?: string[];

  actor_id: string;                 // sha256(email)[:16]

  created_at: string;
  sealed_at?: string;

  goal: string;
  status: IntentStatus;             // 状态机定义的状态

  base_commit: string;
  code_commit?: string;

  turns_summary: TurnSummary[];

  summary?: IntentSummary;
  fingerprint?: SemanticFingerprint;

  seal?: {
    sealed_by: "agent-submit" | "manual" | "invoke-agent";
    seal_result_hash: string;
  };
}
```

### 8.2 Turn

```typescript
interface Turn {
  id: string;
  intent_id: string;
  index: number;
  created_at: string;
  description: string;
  files_changed: FileChange[];
  diff_stats: { files: number; added: number; removed: number };
  caller: { pid?: number; process_name?: string; cwd: string };
}

interface TurnSummary {
  index: number;
  description: string;
  files_changed: string[];
}
```

完整 turn 留在 `.ml-cache/drafts/`。Sealed 后只有 TurnSummary 进 actor log。

### 8.3 Event（actor log 的核心）

```typescript
type Event =
  | IntentSealedEvent
  | IntentSupersededEvent
  | IntentAbandonedEvent
  | CheckJudgmentEvent;

interface BaseEvent {
  event_id: string;          // ev_<ulid>，事件流用 ULID 避免顺序碰撞
  schema_version: 1;
  type: string;
  actor_id: string;
  created_at: string;
}

interface IntentSealedEvent extends BaseEvent {
  type: "intent.sealed";
  intent_id: string;
  thread: string;
  base_commit: string;
  code_tip: string;
  goal: string;
  summary: IntentSummary;
  fingerprint: SemanticFingerprint;
  turns_summary: TurnSummary[];
}

interface IntentSupersededEvent extends BaseEvent {
  type: "intent.superseded";
  intent_id: string;
  superseded_by: string;
}

interface IntentAbandonedEvent extends BaseEvent {
  type: "intent.abandoned";
  intent_id: string;
  reason?: string;
}

interface CheckJudgmentEvent extends BaseEvent {
  type: "check.judgment_submitted";
  candidate_intent: string;
  judgments: ConflictJudgment[];
  // ... CheckJudgmentResult 的内容
}
```

### 8.4 IntentSummary

```typescript
interface IntentSummary {
  title: string;
  what: string;
  why: string;
  user_goal: string;
  decisions: Decision[];
  rejected: RejectedAlternative[];
  risks: string[];
  followups: string[];
}

interface Decision {
  point: string;
  chose: string;
  rationale?: string;
  rejected?: string[];
}

interface RejectedAlternative {
  alternative: string;
  reason?: string;
}
```

### 8.5 SemanticFingerprint

```typescript
interface SemanticFingerprint {
  subsystems: string[];
  files_touched: string[];
  architectural_claims: string[];
  behavioral_changes: string[];
  api_changes: ApiChange[];
  data_model_changes: DataModelChange[];
  security_implications: string[];
  migration_notes: string[];
  tags: string[];
}

interface ApiChange {
  kind: "added" | "modified" | "removed";
  surface: "http" | "function" | "class" | "cli" | "event" | "config";
  signature: string;
  compatibility: "breaking" | "compatible" | "unknown";
}
```

### 8.6 SealResult（agent 生成提交）

```typescript
interface SealResult {
  intent_id: string;
  summary: IntentSummary;
  fingerprint: SemanticFingerprint;
  confidence: {
    summary: number;          // 0.0 - 1.0
    fingerprint: number;
  };
  unsupported_claims?: string[];
}
```

### 8.7 CheckJudgmentResult（agent 生成提交）

```typescript
interface CheckJudgmentResult {
  candidate_intent: string;
  judgments: ConflictJudgment[];
  overall: {
    has_conflict: boolean;
    highest_severity: "none" | "low" | "medium" | "high";
    needs_human_review: boolean;
  };
}

interface ConflictJudgment {
  task_id: string;
  has_conflict: boolean;
  type?: "architectural" | "behavioral" | "api_breaking"
       | "data_model" | "security" | "intent_contradiction";
  severity: "low" | "medium" | "high";
  confidence: number;
  explanation: string;
  evidence: {
    mainline_intent: string;
    candidate_intent: string;
    mainline_aspect: string;
    candidate_aspect: string;
    why_incompatible: string;
  }[];
  resolution_options: string[];
  needs_human_review: boolean;
}
```

### 8.8 Thread

```typescript
interface Thread {
  name: string;
  git_branch: string;
  worktree_path?: string;
  base_commit?: string;
  intents: string[];
  status: "active" | "merged" | "abandoned";
  created_at: string;
  closed_at?: string;
}
```

---

## 9. 存储与同步设计

### 9.1 总体架构

**Mainline 不通过 main 分支文件树同步元数据。**

**Mainline 不在仓库里建 `.ml/` 目录用于多人共享数据。**

**Mainline 通过 git remote 上的独立 branch 同步数据。**

```
                   Git Remote
       ┌──────────────────────────────────┐
       │ refs/heads/main                  │  ← 代码 + Mainline-Intent trailer
       │ refs/heads/feature/*             │  ← 开发分支
       │                                  │
       │ refs/heads/_mainline/actor/<id>  │  ← 各开发者的 event log
       │ refs/heads/_mainline/actor/<id>  │     (per-actor，无写竞争)
       │ ...                              │
       └──────────────────────────────────┘
                       │
                  mainline sync
                       │
                       ▼
              ┌──────────────────┐
              │ Local materialized│
              │ views in          │
              │ .ml-cache/views/  │
              └──────────────────┘
```

### 9.2 Actor logs

每个开发者拥有一个 git branch 作为 append-only event log：

```
refs/heads/_mainline/actor/act_<hash>
```

**为什么是 branch 而不是自定义 ref？**

GitHub/GitLab/Gitea 对 `refs/heads/*` 完全支持（push/pull/网页浏览），对自定义 namespace 支持参差不齐。Branch 是最大公约数。

**为什么有下划线前缀 `_mainline/`？**

让用户能在 `git branch -l` 里用 pattern 过滤掉。也明显标记"这是工具用的，不是代码分支"。

**Branch 内容**：每次 sealed/superseded/abandoned 创建一个 commit，commit 包含一个或多个 event JSON 文件：

```
refs/heads/_mainline/actor/act_alice
└── (latest commit)
    └── events/
        ├── ev_01HW8ZK7Y6Y9E4W2N8M3.json
        ├── ev_01HW8ZK8A1G7K9Q...json
        └── ...
```

每个 event 是不可变 append。同一个 actor 的 log 顺序由 git commit history 自然决定。

### 9.3 Main branch trailer

每次 intent merge 到 main 时，merge commit 的 message 含：

```
Mainline-Intent: int_b91e2f3a
Mainline-Seal: sha256:9f82a3...
```

详见 §13。

### 9.4 本地工作目录

```
<repo>/
├── .git/                       # git 自己
├── .ml-cache/                  # 本地状态，gitignored
│   ├── config.toml
│   ├── session/
│   │   └── current.json
│   ├── drafts/
│   │   ├── int_b91e2f3a.json
│   │   └── int_b91e2f3a.turns.jsonl
│   ├── outbox/
│   │   └── pending-events/    # 待 push 的事件
│   ├── checks/
│   │   ├── chk_001.prepare.json
│   │   └── chk_001.result.json
│   ├── views/                  # 物化视图
│   │   ├── mainline.json
│   │   ├── proposed-index.json
│   │   ├── intent-locator.json # intent_id → actor_id 索引
│   │   ├── fingerprint-index.json
│   │   └── last-sync.json
│   ├── worktrees/              # --parallel 时用
│   └── logs/
└── AGENTS.md                   # 唯一被 git 追踪的 mainline 文件
```

`.gitignore`:

```
.ml-cache/
```

### 9.5 隐私边界

- **未 sealed 的内容**：永远在本地 `.ml-cache/drafts/`，永不 push
- **Sealed 后**：append 到 actor log，push 到 remote，团队可见
- **Seal 这一刻是公开承诺**——一旦 seal --submit，数据就上 remote 了（除非 `--local-only`）

### 9.6 Identity 配置

`~/.config/mainline/identity.toml`：

```toml
actor_id = "act_<sha256(email)[:16]>"
display_name = "Alice"
```

Actor ID 派生自 git config user.email。同一 email 永远生成同一 actor_id（一致性）。

`mainline log` 显示用 git commit author 字段（真实姓名）—— actor_id 仅用于 ref 命名。

### 9.7 配置文件

`.ml-cache/config.toml`（仓库级）：

```toml
schema_version = 1

[sync]
auto_sync_before_check = true
sync_stale_minutes = 30
fetch_actor_pattern = "refs/heads/_mainline/actor/*"

[seal]
auto_publish_on_submit = true
allow_local_only = true

[check]
fingerprint_threshold = 0.30
mainline_lookback = 50
require_semantic_judgment_for_suspicious = true
block_on_merged_conflict = true
warn_on_remote_proposed_conflict = true
block_on_remote_proposed_conflict = false   # 严格模式可改

[merge]
require_check = true
require_fresh_sync = true
allow_missing_semantic_judgment = false

[permissions]
status = "auto"
context = "auto"
start = "auto"
append = "auto"
seal_prepare = "auto"
seal_submit = "auto"
sync = "auto"
publish = "auto"
check_prepare = "auto"
check_submit = "auto"
merge = "ask"
revert = "ask"
reset = "reject"
run_nested = "reject"
```

明确**没有**：

```toml
# 不存在
[llm]
provider = ...
api_key = ...
```

---

## 10. Mainline 派生视图

### 10.1 派生算法

`mainline sync` 后或视图过期时，重建本地 mainline 视图：

```
1. git fetch origin main
2. git fetch origin 'refs/heads/_mainline/actor/*'

3. 扫描 origin/main 的 first-parent history（自上次 sync 以来的增量）
   for each commit in history:
       parse commit message for trailers
       extract Mainline-Intent: int_xxx 标记

4. 扫描 actor logs（自上次 sync tip 以来的新 commits）
   for each new event in any actor log:
       index by intent_id → actor_id
       update intent-locator.json

5. 重建 mainline.json：
   for each Mainline-Intent in main history (按 git 顺序):
       look up intent in intent-locator → actor_id
       fetch event from that actor's log
       add to merged_intents list

6. 重建 proposed-index.json：
   for each sealed intent in any actor log:
       if not in main history (not merged):
           add to proposed_index
   filter out: superseded, abandoned

7. 重建 fingerprint-index.json：
   for each merged intent: index its fingerprint
   for each proposed intent: index its fingerprint

8. 写入 .ml-cache/views/last-sync.json
```

### 10.2 增量 vs 全量重建

- **增量**：基于 `last-sync.json` 记录的 main commit + 各 actor log tip，只处理新增部分。日常 sync 用这个。
- **全量**：删除 `.ml-cache/views/`，从头重建。用于：
  - 视图损坏
  - Force push 检测后
  - `mainline fsck --rebuild`

全量重建在 100 actor + 1000 intent 规模下应在 30 秒内完成。

### 10.3 视图结构

`.ml-cache/views/mainline.json`：

```json
{
  "schema_version": 1,
  "rebuilt_at": "2026-04-25T10:32:00Z",
  "rebuilt_from": {
    "main_commit": "a3f8c9d",
    "actor_log_tips": {
      "act_alice": "ref_tip_xxx",
      "act_bob": "ref_tip_yyy"
    }
  },
  "merged_intents": [
    {
      "intent_id": "int_a3f2c901",
      "merge_commit": "abc123",
      "merged_at": "2026-04-20T...",
      "actor_id": "act_bob",
      "title": "Add OAuth2 provider support",
      "summary": { /* IntentSummary */ },
      "fingerprint": { /* SemanticFingerprint */ }
    }
  ]
}
```

`.ml-cache/views/proposed-index.json`：

```json
{
  "schema_version": 1,
  "rebuilt_at": "...",
  "proposed_intents": [
    {
      "intent_id": "int_b91e2f3a",
      "actor_id": "act_alice",
      "thread": "refactor-auth-jwt",
      "sealed_at": "...",
      "title": "Refactor auth from session to JWT",
      "fingerprint": { /* ... */ }
    }
  ]
}
```

### 10.4 Force push 检测

每次 sync 时记录 actor log tip。下次 sync 时检测：

- 旧 tip commit 是否仍在新历史里？（`git merge-base --is-ancestor old-tip new-tip`）
- 不是 → force push 检测到

处理：

```
⚠ Force push detected on actor log: act_bob
Old tip abc123 is no longer in history.
Some events may have been removed or rewritten.

This is unusual. Mainline will rebuild views from current state, but
you should investigate with the actor.
```

记录到 `.ml-cache/logs/`。强烈建议在 git host 上对 `_mainline/actor/*` branches 启用 branch protection（禁止 force push）。`mainline init` 应提示这个（用户在 GitHub 设置里配置）。

---

## 11. Merge 与审核流程

### 11.1 总原则

**Mainline 不发明新的 review 机制，完全寄生在团队已有的 PR 工作流上。**

Reviewer 在 GitHub 上做的 review 自然包含：

- **代码改动**（GitHub 默认 PR diff 视图）
- **Intent 内容**（不在 diff 里，但能查）

Intent 内容怎么 review？

- 命令行：`mainline sync && mainline show int_xxx`
- PR description：作者用 `mainline pr-description --intent int_xxx` 一键生成 markdown 粘贴到 PR

### 11.2 主路径：GitHub PR + merge button

```
1. Alice: mainline seal --submit       (auto publish)
2. Alice: git push origin feature-jwt
3. Alice: 在 GitHub 开 PR
4. Alice: 把 mainline pr-description 的输出贴到 PR description
   该 description 包含 Mainline-Intent trailer
5. Bob (reviewer): mainline sync, mainline show int_b91e
6. Bob: 在 GitHub approve
7. Alice: 点 Squash and merge
8. GitHub 创建 squash commit，message 保留 PR description（含 trailer）
9. 团队任何人下次 sync 时，mainline 视图反映 int_b91e 已 merged
```

**关键点：trailer 进 squash commit 的机制**

GitHub squash merge 默认行为是把 PR description 作为 commit message。如果 trailer 在 description 里，它就会在 commit message 里。

需要：

1. 仓库 squash merge commit message 设置选 "Pull request title and description"（GitHub repo settings）
2. PR template 提示作者保留 trailer

`mainline init` 时创建 `.github/PULL_REQUEST_TEMPLATE.md`：

```markdown
<!-- Describe your changes -->



<!-- Mainline metadata - do not remove -->
<!-- Run `mainline pr-trailer` to fill in -->

Mainline-Intent: <intent_id>
Mainline-Seal: <hash>
```

### 11.3 后备路径：`mainline merge` 命令

详见 §6.13。用于：

- 不用 GitHub PR 工作流的团队
- CI 场景
- 紧急 fallback

行为：本地 merge，强制注入 trailer，原子 push。

### 11.4 GitHub Action（v0.5+，留接口）

```yaml
name: Mainline check
on:
  pull_request:
    types: [opened, synchronize]
jobs:
  check:
    steps:
      - uses: mainline-vcs/check-action@v1
```

Action 在 PR 上跑 `mainline check`，检测到冲突时 PR 标记 fail。

v0.1 不实现这个 Action，但 Mainline CLI 必须能在 CI 环境正常工作（headless、`--json` 输出）。

### 11.5 v0.1 的诚实边界

**未 publish 的本地 intent，团队不可能看到。**

这不是 Mainline 的限制，是信息论。没有共享 remote、没有 publish 动作，就没有可见性。

文档和 README 必须明显地说出这一点：

> Mainline can detect semantic conflicts against published intent history.
> Local-only drafts are invisible to teammates by design.

`mainline status` 输出里显眼显示"unpublished intents: N"。

---

## 12. 冲突类型与处理

六种典型场景：

### 12.1 代码层冲突（git conflict）

**触发**：两个 PR 改了同一行代码。

**处理**：标准 git merge conflict。Mainline 不参与。

**Intent 状态**：不变。同一个 int_xxx，没有新事件。代码 commit 变了。

### 12.2 语义冲突（候选 vs merged）

**触发**：`mainline check` 发现候选 intent 和已 merged intent 有架构冲突。

**Mainline 行为**：

- `mainline check` exit code 1
- `mainline merge` 拒绝
- 提示用户 resolution options

**用户处理**：

- 选项 A：和原作者协商，决定怎么处理
- 选项 B：调整自己的 intent（supersede 或重新 seal）
- 选项 C：开 resolution intent，原作者先 merge resolution

**Mainline 角色**：让冲突可见，不替用户做架构决定。

### 12.3 语义冲突（候选 vs remote proposed）

**触发**：`mainline check` 发现和别人尚未 merge 的 intent 冲突。

**默认行为**：warning 不阻塞（因为对方 intent 不一定会 merge）。

**严格模式**：`block_on_remote_proposed_conflict = true` 阻塞。

**用户处理**：通常直接和对方协商。

### 12.4 Race condition（同时 merge）

**触发**：Alice 和 Bob 同时 merge 各自 PR。

**git 行为**：先到的成功，后到的 PR 落后。

**Mainline 行为**：

- 落后方下次 sync 看到新的 mainline 状态
- 之前的 check 结果失效，需要重新 check
- 如果产生新冲突 → 回到场景 12.2

### 12.5 撤销已 merged intent

**触发**：用户想撤销已经 merged 的 intent。

**两条路径：**

**路径 A：git revert + intent revert**

```bash
git revert <merge-commit>
mainline start "Revert: undo JWT migration"
mainline append "Revert int_b91e2f3a, return to session-based auth"
mainline seal --submit
mainline merge   # 注入 trailer:
                 # Mainline-Intent: int_revert_001
                 # Mainline-Reverts: int_b91e2f3a
```

`mainline log --mainline` 显示：

```
int_revert_001  reverted int_b91e2f3a
int_b91e2f3a    [REVERTED]
int_a3f2c901    Add OAuth2 provider support
```

原 intent 数据不变（actor log append-only），视图反映 reverted 状态。

**路径 B：Supersede（不 git revert）**

代码不变，只更新 intent 描述：

```bash
mainline start "Refactor auth (corrected)"
mainline append "..."
mainline seal --submit --supersedes int_b91e2f3a
```

actor log 加一个 `intent.superseded` 事件。视图里旧的标 superseded。

### 12.6 Force push（main 或 actor log）

**Main force push：**

- Sync 检测到 origin/main 历史 rewrite
- 警告
- 重建 mainline 视图基于新 history
- 旧的 merge commit 如果被 drop，相关 intent 变回 proposed 状态（这是对的——它们事实上不在代码主线了）

**Actor log force push：**

- Sync 检测到 actor log 历史 rewrite
- 警告全队
- 拒绝接受这个 actor log 的更新（直到人工干预）
- 强烈建议在 git host 启用 branch protection 防止这种情况

---

## 13. Trailer 规范

### 13.1 格式

按 git trailer 标准（commit message 末尾，空行隔开正文）：

```
<commit message body>

Mainline-Intent: int_b91e2f3a
Mainline-Seal: sha256:9f82a3b5c1d8e2f4...
```

### 13.2 字段

| Trailer | 必需 | 含义 | 可重复 |
|---|---|---|---|
| `Mainline-Intent` | 是 | 此 commit 所合入的 intent | 是（squash 多个 intent） |
| `Mainline-Seal` | 推荐 | SealResult JSON 的 sha256 hash，用于完整性校验 | 跟 Intent 1:1 |
| `Mainline-Reverts` | 否 | 撤销关系 | 是 |
| `Mainline-Supersedes` | 否 | supersede 关系 | 是 |

### 13.3 多 intent squash

```
Mainline-Intent: int_b91e2f3a
Mainline-Seal: sha256:9f82a3...
Mainline-Intent: int_c102de45
Mainline-Seal: sha256:abcd12...
```

按 trailer 出现顺序作为 intent 在 mainline 中的相对顺序。

### 13.4 校验

`mainline sync` 解析 main history 时：

1. 提取每个 commit 的 Mainline-Intent trailer
2. 在 actor logs 里查找对应 intent
3. 如果有 Mainline-Seal，验证 hash 与 actor log 中的 SealResult 匹配
4. Hash 不匹配 → 警告（可能是手动改了 trailer 或 actor log 损坏）

### 13.5 缺失 trailer 的处理

main commit 没有 trailer 怎么办？

- **可能场景**：第三方 push、手动 merge、early adopter 仓库的旧 commit
- **处理**：
  - Sync 时跳过，不算入 mainline
  - 警告："commit abc123 has code changes but no Mainline-Intent trailer"
  - 提供 `mainline reconcile` 命令（v0.5）让用户事后补 trailer

### 13.6 第三方工具修改 commit message

git rebase / cherry-pick / amend 都可能保留或丢失 trailer：

- **rebase**：保留
- **cherry-pick**：保留
- **amend**：保留（除非用户主动删）
- **squash 合并多个 commit**：trailer 累积（用户应该手动检查）

`mainline init` 时建议在团队中文档化"不要手动修改 Mainline-* trailer"。

---

## 14. 语义冲突检测

### 14.1 Phase 1：确定性 fingerprint 比对

无模型、无 agent、完全可测。

```
score =
    0.35 * jaccard(subsystems)
  + 0.25 * jaccard(files_touched)
  + 0.20 * api_overlap(api_changes)
  + 0.10 * keyword_overlap(architectural_claims)
  + 0.10 * keyword_overlap(behavioral_changes)
```

阈值 `0.30`。

**这些权重是 v0.1 的初始值，待真实数据校准**。收集 ≥50 真实冲突 case 后通过 grid search 重新拟合。

低于阈值 → `phase1_passed`；高于阈值 → `needs_judgment`。

### 14.2 Phase 2：agent 提交的语义判断

Mainline 准备 judgment task，agent 生成结构化 JSON，Mainline 校验存储。

**Mainline v0.1 不自己做语义判断**。

### 14.3 比对范围

`mainline check` 同时比对：

- **Merged mainline intents**（从 mainline 视图）
- **Remote proposed intents**（从 proposed 视图，不含自己的）

冲突分级：

| 冲突对象 | 默认 | 严格模式 |
|---|---|---|
| 与 merged 冲突 | blocking | blocking |
| 与 remote proposed 冲突 | warning | blocking |

### 14.4 关键行为

Phase 1 找到可疑配对但 Phase 2 缺失时 → 退出码 3，提示 agent 怎么补：

```
⚠ Semantic judgment required

Phase 1 found 2 suspicious intent pairs.
Ask your agent to run:
  mainline check --prepare --json
  mainline check --submit <judgment.json>
```

`mainline merge` 在缺少 judgment 时拒绝执行。

---

## 15. Agent 集成

### 15.1 主集成路径

```
AGENTS.md teaches agent
agent calls mainline CLI
mainline returns JSON
agent submits JSON
```

跨 agent 工作，Mainline 不需要管 agent runtime。

### 15.2 内部 adapter 接口（不 v0.1 公开）

代码中保留 trait，但 v0.1 不暴露：

```typescript
interface AgentAdapter {
  name(): string;
  available(): Promise<boolean>;
  version(): Promise<string | null>;
  readsAgentsMd(): boolean;
  invoke(opts: InvokeOptions): Promise<InvokeResult>;
  exec(opts: ExecOptions): Promise<ExecResult>;
}
```

理由：未来 replay/rebase 需要、CI 场景需要、避免 breaking refactor。

v0.1 实现要求：

- 定义 trait
- 实现 detection stubs
- **不暴露** `mainline run` 命令
- `invoke`/`exec` 可返回 `Unsupported`
- seal/check core 不依赖 adapter

### 15.3 未来命令（保留，v0.1 不实现）

```
mainline run                  # 启动 agent 执行 intent
mainline replay               # 重新执行历史 intent
mainline rebase               # 在新 base 上重执行 thread
mainline seal --invoke-agent  # 自动启动 agent 生成 seal
mainline check --invoke-agent # 自动启动 agent 做判断
```

---

## 16. 信任与权限

### 16.1 原则

权限是**动作级**的，不是**身份级**的。

不区分"人"vs"agent"——CLI 不能可靠分辨调用方。用 config + TTY 检测决定行为。

### 16.2 权限值

```
auto      任何调用方直接执行
ask       交互式确认（TTY 检测）；非交互场景拒绝
reject    命令直接失败
```

### 16.3 默认配置

```toml
[permissions]
status = "auto"
context = "auto"
start = "auto"
append = "auto"
seal_prepare = "auto"
seal_submit = "auto"
sync = "auto"
publish = "auto"
check_prepare = "auto"
check_submit = "auto"
log = "auto"
show = "auto"

merge = "ask"
revert = "ask"
reset = "reject"
run_nested = "reject"
```

### 16.4 Seal 默认 auto 的理由

Seal 改变 Mainline 元数据，但**不发布代码、不 merge、不破坏代码状态**。

如果 seal 质量差：

- 用户可创建 superseding intent
- 未来 `mainline seal --regenerate` 可改进
- intent 仍可审计

把记录和结构化做成自然发生的动作，把人的决策保留给真正改变共享状态的动作（merge）。

---

## 17. Git 与 Thread 模型

### 17.1 Thread = git branch

```bash
git branch --show-current
# refactor-auth-jwt
```

→

```
thread.name = refactor-auth-jwt
thread.git_branch = refactor-auth-jwt
```

无 mode 配置。

### 17.2 Lazy registration

任何 mainline 命令在未注册的 git branch 上首次运行时，自动创建 thread metadata。

用户用 git 切 branch，Mainline 在后台跟上。

### 17.3 Worktree 是可选属性

只在 `mainline thread new <n> --parallel` 时创建。

### 17.4 Branch 命名约定

| 前缀 | 用途 |
|---|---|
| `main` / `master` | 代码主干 |
| `feature/*`, `<n>` 等 | 普通代码分支 = thread |
| `_mainline/actor/*` | Mainline actor logs（工具用，下划线前缀） |

`mainline init` 在 `.git/info/exclude` 或 PR template 里提示团队不要手动操作 `_mainline/*` branches。

---

## 18. 实现架构

### 18.1 语言

**Rust**。

理由：CLI 启动速度、单二进制分发、git/file lock/process 处理、JSON schema 校验、未来扩展性。

### 18.2 Workspace

```
mainline/
├── Cargo.toml
├── crates/
│   ├── ml-cli/          # 命令解析、输出渲染
│   ├── ml-core/         # 状态机、领域规则
│   ├── ml-storage/      # .ml-cache/ 读写
│   ├── ml-sync/         # actor log fetch、视图重建
│   ├── ml-protocol/     # agent-facing JSON schema
│   ├── ml-check/        # fingerprint scoring、check runs
│   ├── ml-agent/        # 内部 AgentAdapter trait（v0.1 stub）
│   └── ml-git/          # git 操作包装
├── agents-md/
│   └── default.md
├── pr-templates/
│   └── default.md
├── schemas/
│   ├── seal-result.schema.json
│   └── check-judgment-result.schema.json
├── tests/
│   ├── integration/
│   ├── fixtures/
│   └── conflict-cases/
└── docs/
```

**不创建**：`ml-llm/`、`credentials.toml`、provider API key 管理、本地模型 runtime。

### 18.3 主要依赖

```
clap, serde, serde_json, toml, thiserror, anyhow,
tokio, jsonschema, schemars, fs2, tracing,
ulid (event_id 生成),
sha2 (actor_id derivation, seal hash)
```

git 策略：v0.1 shell out 到 `git` 命令，所有调用包在 `ml-git` 后面。

### 18.4 Day 1 dev experience

新工程师应该能：

1. Clone repo，`cargo build` 在 10 分钟内成功
2. 跑 `tests/integration/` 看到通过
3. 读 `docs/architecture.md`，30 分钟理解 8 个 crate 的边界
4. 在 1 天内做出第一次贡献

---

## 19. Schema 演进

所有持久化对象带 `schema_version`。

v0.1 只 ship v1。

前向兼容规则：

- v2 引入时，v0.x CLI 看到 v2 对象 → 报清晰错误，拒绝处理
- v2 CLI 看到 v1 对象 → 必须能读，写时可选迁移
- 迁移单向，不支持降级
- Schema version bump 是产品决定，不是随意改动

未知字段策略：

- v0.1：严格拒绝未知顶层字段（防止 agent 编造）
- 嵌套对象内部允许未知字段（前向兼容空间）

事件流的特殊考虑：

- 旧 ml 看到新事件类型（type: "..." 不识别）→ 忽略 + 警告
- 新 ml 看到旧事件类型 → reduce 时按旧规则处理
- 永远不删除事件类型，只 deprecate

---

## 20. 校验规则

### 20.1 SealResult 校验

`mainline seal --submit` 必须验证：

1. JSON 解析成功
2. Schema 通过
3. `intent_id` 存在
4. Intent status = drafting
5. 提交的 files_touched 与真实改动文件兼容（子集 + 1 个 fudge factor）
6. `summary.title` 非空
7. fingerprint 数组存在（即使为空）
8. confidence 值在 [0, 1]
9. 无未知顶层字段
10. 无 repo 外文件路径

失败返回 agent-friendly error。

### 20.2 CheckJudgmentResult 校验

1. JSON 解析成功
2. Schema 通过
3. `candidate_intent` 匹配 prepared task
4. 每个 `task_id` 已知
5. confidence 在 [0, 1]
6. severity 合法
7. `has_conflict = true` → evidence 非空
8. high severity → `needs_human_review` 应为 true（除非显式说明）

### 20.3 Trailer 校验

`mainline sync` 解析 main history 时：

1. Mainline-Intent 引用的 intent 在某个 actor log 中存在
2. 如有 Mainline-Seal，hash 与 actor log 中事件的 SealResult hash 匹配
3. 不匹配 → 警告但不失败（视图仍可建，标记可疑）

---

## 21. 错误处理与失败模式

### 21.1 错误格式

所有 agent-facing 错误尽量可恢复。

```json
{
  "error": {
    "code": "NO_ACTIVE_INTENT",
    "message": "There is no active drafting intent on this branch.",
    "recoverable": true,
    "suggested_actions": [
      "Run: mainline start \"<user goal>\" --json",
      "Or: mainline append \"<what changed>\" --goal \"<user goal>\" --json"
    ]
  }
}
```

CLI 错误三段式：

1. **What** happened
2. **Why** it matters
3. **Exact next command**

### 21.2 Agent 失败模式

| 模式 | 处理 |
|---|---|
| Agent 未安装/crash | exit 4，明确信息，状态不变 |
| Agent 超时 | 默认 60s，超时 → 状态保留可重试 |
| Agent 返回无效 JSON | Schema validation error，状态保留 |
| Agent 返回部分 JSON | 同无效 JSON |
| Agent 返回有效但可疑 JSON | 接受但标记 needs_human_review |

**不变量：**

- Agent 失败永远不腐蚀 Mainline 状态
- Drafts 在 seal 失败后保留完整
- 重试不需要重做之前所有工作

### 21.3 Sync 失败模式

| 场景 | 处理 |
|---|---|
| 网络断开 | 报错退出，保留旧视图，下次重试 |
| Force push 检测 | 警告 + 全量重建视图 |
| Actor log 损坏 | 警告 + 跳过该 actor，继续其他 |
| 视图损坏 | 提示 `mainline fsck --rebuild` |

### 21.4 Push 失败模式

| 场景 | 处理 |
|---|---|
| Seal 时 actor log push 失败 | 本地 sealed 但未发布，提示 `mainline publish` |
| Merge 时 main push 失败 | 全部回滚，提示重试 |
| Merge 时 actor log push 失败但 main 成功 | 警告 + 提示 `mainline publish --repair` |
| 非 atomic 顺序：先 main 成功再 actor log 失败 | 同上 |

---

## 22. v0.1 范围

### 22.1 Must ship

**核心命令：**

```
mainline init
mainline status
mainline start
mainline append
mainline seal --prepare
mainline seal --submit
mainline seal
mainline sync
mainline publish
mainline check --prepare
mainline check --submit
mainline check
mainline merge
mainline pr-trailer
mainline log
mainline show
mainline thread new
mainline thread list
mainline thread close
```

**存储：**

- `.ml-cache/` 目录布局
- Actor log（git branch）读写
- 视图重建算法
- `.gitignore` 自动维护
- AGENTS.md 自动维护

**Agent 集成：**

- AGENTS.md 模板和版本
- 全部命令支持 `--json`
- seal/check prepare-submit 协议
- agent-friendly errors

**Trailer：**

- Trailer 解析
- `mainline pr-trailer` 命令
- PR template 生成
- Merge 命令注入 trailer

**内部（不公开）：**

- AgentAdapter trait
- detection stubs

### 22.2 不 ship

```
public mainline run
public mainline replay
public mainline rebase
mainline review / comment
GitHub Action (作为产品)
Web UI / Hub
MCP server
local LLM
LLM API credentials
PTY trace capture
```

### 22.3 Optional（时间允许）

```
mainline context
mainline why
mainline blame
mainline search
mainline fsck
mainline reconcile
mainline list-proposals
```

---

## 23. 里程碑

### M1: init + identity + AGENTS.md

- repo 检测
- `.ml-cache/` 创建
- identity.toml 生成
- AGENTS.md + PR template 追加
- `.gitignore` 更新

验收：`mainline init && mainline status --json` 工作。

### M2: thread = branch

- 当前 branch lazy 注册
- `mainline thread new`/list/close
- `--parallel` 创建 worktree

验收：`git checkout -b auth && mainline status --json` 显示 thread。

### M3: start + append + status + log（基础读写）

- `mainline start`
- `mainline append`、`--goal`、`--allow-empty`
- `.ml-cache/drafts/` 写入
- diff stats
- `mainline log` 基础读取

验收：`mainline append "X" --goal "Y" --json` 自动创建 intent + turn。

### M4: seal prepare/submit + actor log

- `mainline seal --prepare`
- SealResult schema
- `mainline seal --submit`
- 写入 actor log（git commit）
- 自动 push（auto_publish_on_submit）
- `mainline publish` 命令

验收：

- agent 用 JSON 完成 seal
- intent 出现在 `refs/heads/_mainline/actor/<id>` 上
- 无效 JSON 返回 repairable error

### M5: sync + 视图重建

- `mainline sync`
- 解析 main history trailer
- Fetch actor logs
- 增量重建 mainline 视图
- 重建 proposed 视图

验收：

- 两人协作场景下，sync 后 Bob 能看到 Alice 的 proposed intent
- main 上 trailer 正确反映在 mainline 视图中

### M6: check prepare/submit

- fingerprint overlap 算法
- 双视图比对（merged + proposed）
- CheckJudgment schema
- submit 校验
- check 渲染

验收：

- 人造的 auth/OAuth 冲突被标记
- 与 proposed 冲突标 warning
- 与 merged 冲突标 blocking

### M7: merge + trailer

- 合并前要求 sync
- 合并前要求 check
- git merge
- 注入 trailer
- 原子 push（如果支持）
- `mainline pr-trailer`

验收：

- `mainline merge` 对无冲突 proposed intent 工作
- merge commit 含正确 trailer
- 团队 sync 后 mainline 视图反映 merged

### M8: 内部 adapter trait

- AgentAdapter trait 定义
- invoke/exec 接口
- detection stubs
- 不依赖于任何 v0.1 命令

验收：编译通过、interface 完整、v0.1 命令不依赖。

### M9（可选）: 增强读 + show extras

- `mainline context <topic>`
- `mainline why <file>`
- `mainline show --turns`
- `mainline list-proposals`

---

## 24. 验收测试

### 24.1 Agent-first happy path（单人）

```bash
git init demo && cd demo
echo "# Demo" > README.md && git add . && git commit -m "init"

mainline init
git checkout -b refactor-auth-jwt

echo "// jwt" > auth.ts
mainline append "Add JWT helper" --goal "Refactor auth" --json

mainline seal --prepare --json > /tmp/seal-input.json
mainline seal --submit fixtures/seal-result-auth.json --json

mainline show --latest
```

期望：

- intent 创建
- turn 记录
- seal 接受
- actor log 创建（`git branch -l '_mainline/*'`）
- `.ml-cache/drafts/` 中对应文件被清理

### 24.2 双人协作（核心）

setup：

```bash
# 准备一个 bare repo 作为 origin
mkdir origin.git && cd origin.git && git init --bare
cd ..

# Alice 和 Bob 各 clone
git clone origin.git alice && cd alice
git clone ../origin.git ../bob

# 都 mainline init
```

Alice：

```bash
cd alice
git checkout -b feature-jwt
echo "auth.ts" > auth.ts
mainline append "Add JWT" --goal "JWT migration" --json
mainline seal --submit fixtures/seal-jwt.json --json
# 自动 publish
```

Bob：

```bash
cd ../bob
mainline sync
mainline list-proposals --json
```

期望：Bob 能看到 Alice 的 proposed intent。

### 24.3 语义冲突检测（双人）

setup：Alice merged 一个 OAuth intent；Bob 提了一个冲突 JWT intent。

```bash
cd bob
mainline sync
mainline check
```

期望：Phase 1 标记可疑（exit 3），提示 agent 提交 judgment。

提交 conflict judgment 后：

```bash
mainline check
```

期望：exit 1，渲染 conflict（与 merged 冲突 = blocking）。

### 24.4 Merge with trailer

```bash
cd alice
mainline merge --yes
git log -1 --pretty=full
```

期望：merge commit message 含 `Mainline-Intent: int_xxx`。

```bash
cd ../bob
mainline sync
mainline log --mainline
```

期望：Bob 看到 int_xxx 已 merged。

### 24.5 Invalid seal recovery

```bash
mainline seal --submit fixtures/invalid-seal.json --json
```

期望：

- schema error
- 无状态转移
- draft 完整保留
- actor log 无新 commit
- 返回 suggested action

### 24.6 Force push 检测

模拟 Alice force push actor log：

```bash
git push --force origin refs/heads/_mainline/actor/act_alice
```

```bash
cd bob
mainline sync
```

期望：检测到 force push，警告，重建视图。

### 24.7 Worktree 可选

```bash
mainline thread new feature-a              # 仅 branch
mainline thread new feature-b --parallel   # branch + worktree
```

### 24.8 Lazy thread registration

```bash
git checkout -b new-thing
mainline append "did stuff" --goal "Try X" --json
```

期望：thread `new-thing` 自动注册。

---

## 25. 非功能需求

### 25.1 性能

不含 agent 工作和网络 I/O：

```
mainline status                      < 50ms
mainline append                      < 150ms
mainline seal --prepare              < 300ms
mainline seal --submit (本地)         < 500ms
mainline log 100 intents             < 200ms
mainline check phase1 50 intents     < 500ms
```

含网络（5-actor 团队，稳定网络）：

```
mainline sync 增量                   < 3s
mainline sync 全量重建（100 intent） < 30s
mainline seal --submit (含 push)     < 5s
```

### 25.2 可靠性

- Draft 写入用 temp file + atomic rename
- 失败的 seal --submit 永不破坏 draft
- 失败的 merge 永不标记 intent 为 merged
- Schema version 在所有持久化对象上必填
- Actor log 写入失败时本地状态可重试

### 25.3 隐私

默认：

- raw turns 本地（`.ml-cache/`）
- drafts 本地
- sealed intent 在 actor log（git remote 上，团队可见）
- 无 model provider 配置
- 无 API key 存储

文案：

> Mainline does not call model providers directly.
> Agents generate semantic metadata and submit it to Mainline.
> Mainline does not run servers. Your data stays in your git remote.

### 25.4 兼容性

- macOS
- Linux
- Windows via WSL2
- Git 2.30+
- GitHub / GitLab / Gitea / 自建 git remote

原生 Windows 推迟到后续版本。

---

## 26. 产品文案

### 26.1 一句话定位

> **Git versions code. Mainline versions intents.**

中文：

> **Git 管代码版本，Mainline 管产生代码的意图的版本。**

### 26.2 用什么样的话

✓ "A distributed intent ledger for coding agents."

✓ "Mainline lives in your git remote. No servers. No accounts."

✓ "Keep using your agent. Mainline gives it structured memory."

✓ "Mainline lets your agent record intent history and judge semantic conflicts before merge."

---

✗ "Mainline uses LLMs to detect conflicts."

✗ "Run Mainline, then start your agent."

✗ "Sign up to start using Mainline."

### 26.3 命令展示原则

文档中所有命令展示用全名 `mainline`，不用 `ml`。每次敲命令都在加深产品名记忆。

### 26.4 边界诚实化

README 必须显眼地说明：

> Mainline can detect semantic conflicts against published intent history.
> Local drafts are invisible to teammates by design.
>
> A team without a shared git remote cannot use Mainline's collaboration features.
> However, single-user mode still provides full intent journaling.

---

## 27. 开发规则

22 条实施红线：

1. 默认流程是 agent-first
2. AGENTS.md 是主集成接口
3. 每个 agent-facing 命令必须支持 stable `--json`
4. Mainline core **不**调 LLM API
5. v0.1 **无** API key 管理
6. **无** `ml-llm` crate
7. Seal 是 prepare/submit
8. Check 是 prepare/submit
9. `mainline seal --submit` 是 schema 校验 + 状态转换 + 自动 publish
10. `mainline check --prepare` 只做确定性 Phase 1
11. Thread 等价于 git branch
12. Worktree 通过 `--parallel` 可选
13. `mainline run` **不**在 v0.1 公开
14. AgentAdapter 接口内部存在为未来预留
15. Merge 默认人类拥有
16. 无效 agent 输出产出 repairable JSON 错误
17. Agent 失败**永不**腐蚀 Mainline 状态
18. 数据在 actor logs（git remote），缓存在 `.ml-cache/`
19. Drafts 在 seal 之前**永不**进 git remote
20. 主 mainline 是派生视图，不是物理存储
21. main commit trailer 是 merged intent 的 source of truth
22. 不发明新协作流程，寄生在 git 和 git host 已有的流程上

---

## 28. 心智模型

旧模型（被替换）：

```
Human starts Mainline
Mainline starts or guides agent
Mainline asks LLM to summarize/check
Mainline stores everything in main branch
```

新模型（v0.1 真实形态）：

```
Human uses agent normally
Agent calls Mainline CLI with JSON
Agent submits structured semantic metadata
Mainline appends events to per-actor logs
Each developer pulls actor logs from shared git remote
Each developer's local view is rebuilt from
  main branch trailers (order)
  + actor logs (content)
Human reviews via PR; merge commits anchor intents into mainline
```

核心隐喻：

> **Mainline is a protocol, not a service.**
> **Your git remote is the database.**
> **Each developer's actor log is their write channel.**
> **The main branch decides what counts as "merged".**
> **The mainline view is rebuilt locally by everyone.**

---

## 附录 A：术语表

| 术语 | 定义 |
|---|---|
| Intent | prompt + 约束 + 由它产生的执行过程，Mainline 的核心版本对象 |
| Turn | 一次 agent 工作片段的最小记录单位 |
| Thread | 一组相关 intent 的容器，等价于 git branch |
| Mainline | 已合入 intent 的派生历史视图（本地物化） |
| Seal | 把 turns 规范化为 proposed intent 并发布到 actor log 的关键操作 |
| Actor log | 单个开发者的 append-only event log（一个 git branch） |
| Event | actor log 上的不可变记录（intent.sealed、intent.superseded 等） |
| Trailer | main commit message 中锚定 intent 的 metadata |
| Semantic fingerprint | Intent 的结构化摘要，用于快速冲突预判 |
| Drafting | Intent 初始状态，对话中、本地私密 |
| Phase 1 | 确定性 fingerprint 比对，无 LLM |
| Phase 2 | Agent 提交的语义判断 |
| AGENTS.md | 项目根目录的 agent 指引文件 |
| `.ml-cache/` | gitignored 的本地缓存 |
| `_mainline/actor/<id>` | actor log 的 git branch 名 |
| Sync | fetch actor logs + main，重建本地视图的操作 |

---

## 附录 B：开放问题

需要在实现过程中决定的：

1. **AGENTS.md 已存在但格式不兼容时怎么办？**
   v0.1：保留原文，只在末尾加 Mainline section。检测 `<!-- mainline-agents-md-version: N -->` 标记决定升级。

2. **Intent ID 8 hex 真的够吗？**
   单团队估算碰撞概率极低，碰撞时检测重试。但跨仓库场景未解决——v0.1 不解决，未来可加 repo prefix。

3. **Squash merge 时 GitHub UI 不保留 trailer 怎么办？**
   v0.1：依赖 PR template + repo settings 引导。v0.5：GitHub Action 自动维护。

4. **Force push actor log 后真的能恢复吗？**
   不能完全恢复。强烈建议 branch protection。文档要明显警告。

5. **Sync 性能在大规模团队（50+ actors）会爆炸吗？**
   增量 sync 应该 OK，但要实测。可能需要按需 fetch（`--actors=alice,bob`）。

6. **Schema v2 何时引入？**
   v0.1 只 ship v1。何时升级是产品决策。

7. **`fingerprint_threshold = 0.30` 的真实校准时机？**
   收集 ≥50 真实冲突 case 后 grid search。

---

**文档版本**：v1.0 final
**状态**：可作为开发实施规范
**变更管理**：通过 GitHub issue 提议、PR 修改

任何不清楚的地方在 issue 或 channel 上提，本文档随实现持续维护。
