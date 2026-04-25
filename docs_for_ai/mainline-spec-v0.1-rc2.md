# Mainline · 产品与技术方案 v0.1-rc2

> Consistency patch over previous final spec
> 状态：implementation-ready
> 修订重点：状态机一致性 / trailer 可靠性 / 配置位置 / seal 行为 / 命令范围对齐

---

## 修订摘要（相对于 v1.0 final）

P0 修正：
1. 状态机引入 `sealed_local`，与 `proposed` 严格区分
2. `seal --submit` 默认要求 working tree clean，不自动 commit 代码
3. 团队协议配置移到 `.mainline/config.toml`（tracked），本地缓存配置在 `.ml-cache/config.local.toml`
4. PR 主路径文案改为诚实版本，承认 GitHub squash 对配置敏感
5. `mainline merge` 默认禁止 fast-forward，必须创建 anchoring commit
6. `IntentSealedEvent` 增加 `seal_result` + `seal_result_hash` + `code_commit` + `code_tree`
7. `mainline reconcile`、`context`、`list-proposals`、`pr-description` 升级为 must-ship
8. `cancelled` 状态删除，统一为 `abandoned`
9. Force push 策略明确分两类：main vs actor log

P1 修正：
10. Actor log 写入用 `git commit-tree`（不污染工作树），文档明确隔离规则
11. Actor ID 从 email-derived 改为 random ULID
12. Intent 类型分层（DraftIntent / SealedIntentRecord / IntentView）
13. 同 actor 多机器 publish 冲突算法明确
14. Fetch refspec 写完整：`+refs/heads/_mainline/actor/*:refs/remotes/origin/_mainline/actor/*`

P2 修正：
15. `mainline init` 行为列表加入创建 PR template
16. `FileChange`、`DataModelChange`、`SealPreparePackage`、`CheckPreparePackage` 完整 schema
17. Supersede source of truth 明确（actor log event 优先）

---

## 1. 状态机（Patch A）

### 1.1 完整状态定义

```typescript
type IntentStatus =
  | "drafting"
  | "sealed_local"
  | "proposed"
  | "merged"
  | "abandoned"
  | "superseded"
  | "reverted";
```

### 1.2 状态语义（每条都是 normative）

**`drafting`**

- 本地 `.ml-cache/drafts/` 中存在 draft 对象
- Turns 持续累积
- Actor log 上**没有**对应的 sealed event
- 团队**不可见**

**`sealed_local`**

- Actor log 本地分支已写入 `intent.sealed` event
- 该 event **未成功 push** 到 origin
- 团队**不可见**
- 进入条件：
  - `mainline seal --submit --local-only`
  - 或 `mainline seal --submit` 但 push 失败

**`proposed`**

- Actor log 上的 sealed event **已成功 push** 到 origin
- 团队 sync 后**可见**
- 进入条件：`mainline seal --submit` 成功完成 push，或 `mainline publish` 补 push 成功

**`merged`**

- 满足以下条件之一：
  - `origin/main` first-parent history 中存在 commit 含 `Mainline-Intent: <intent_id>` trailer
  - 用户通过 `mainline reconcile` 手动确认（产生 `intent.merge_acknowledged` event）

**`abandoned`**

- Actor log 上存在 `intent.abandoned` event

**`superseded`**

- Actor log 上存在 `intent.superseded` event 引用本 intent

**`reverted`**

- `origin/main` first-parent history 中存在 commit 含 `Mainline-Reverts: <intent_id>` trailer

### 1.3 状态转移图

```
                ml start
                   │
                   ▼
              ┌─────────┐
              │drafting │
              └────┬────┘
                   │ ml seal --submit
              ┌────┴─────────┐
              │              │
       push succeeds   push fails / --local-only
              │              │
              ▼              ▼
         ┌────────┐    ┌──────────────┐
         │proposed│    │ sealed_local │
         └───┬────┘    └──────┬───────┘
             │                │ ml publish (succeeds)
             │                │
             │     ┌──────────┘
             │     ▼
             │  proposed
             │
             │ main commit with Mainline-Intent trailer
             │ OR ml reconcile
             ▼
         ┌────────┐
         │ merged │
         └────────┘

任意状态可转 abandoned / superseded
merged 可转 reverted (via Mainline-Reverts trailer)
```

### 1.4 视图层 vs Event 层的严格分离

| 状态 | 来源 |
|---|---|
| `drafting` | `.ml-cache/drafts/` 文件存在 |
| `sealed_local` | actor log 本地有 commit 但未 push |
| `proposed` | actor log remote tip 包含此 sealed event |
| `merged` | `Mainline-Intent` trailer in main first-parent history OR `intent.merge_acknowledged` event |
| `abandoned` | `intent.abandoned` event |
| `superseded` | `intent.superseded` event |
| `reverted` | `Mainline-Reverts` trailer in main first-parent history |

**Event 层永远不写"merged" / "reverted" 字段**——这两个状态是从 main commit history 或人工确认事件推导。

---

## 2. Seal 行为（Patch B）

### 2.1 `mainline seal --submit` 默认行为

```bash
mainline seal --submit <file>
mainline seal --submit -                    # stdin
mainline seal --submit <file> --local-only  # 不 push（escape hatch）
mainline seal --submit <file> --commit      # 允许自动 commit dirty tree（escape hatch）
```

### 2.2 严格的代码提交边界

**默认要求 working tree clean**。如果有未提交代码：

```json
{
  "error": {
    "code": "CODE_NOT_COMMITTED",
    "message": "Working tree has uncommitted changes. Mainline does not commit code by default.",
    "recoverable": true,
    "suggested_actions": [
      "git add <files> && git commit -m '<message>'",
      "Then re-run: mainline seal --submit <file>",
      "Or: mainline seal --submit <file> --commit (allows mainline to create a code commit)"
    ]
  }
}
```

`--commit` 是显式 escape hatch，需要单独权限：

```toml
[permissions]
seal_submit = "auto"
seal_submit_with_code_commit = "ask"
```

### 2.3 `seal --submit` 完整流程

```
1. 校验 SealResult JSON schema
2. 校验 intent_id 存在且 status = drafting
3. 校验 files_touched 与真实 changed files 兼容
4. 检查 working tree:
   - clean: 跳到第 6 步
   - dirty + 无 --commit: 报错 CODE_NOT_COMMITTED
   - dirty + 有 --commit: 跳到第 5 步（需 seal_submit_with_code_commit 权限）
5. （可选）创建 git commit 包含未提交改动
6. 计算 seal_result_hash = sha256(canonical JSON of SealResult)
7. 构建 IntentSealedEvent（含 code_commit、code_tree、seal_result、seal_result_hash）
8. 用 git commit-tree 在 actor log branch 上创建新 commit（不 checkout）
9. git update-ref 更新 actor log tip
10. 删除 .ml-cache/drafts/<intent-id>.*
11. 状态：drafting → sealed_local
12. 除非 --local-only:
    a. git push origin _mainline/actor/<id>
    b. push 成功: sealed_local → proposed
    c. push 失败: 保持 sealed_local，提示运行 mainline publish
```

### 2.4 输出（JSON）

成功 + published：

```json
{
  "intent_id": "int_b91e2f3a",
  "status": "proposed",
  "published": true,
  "actor_log_commit": "abc123...",
  "seal_result_hash": "sha256:9f82a3...",
  "summary": {"title": "Refactor auth from session to JWT"}
}
```

成功 + push 失败：

```json
{
  "intent_id": "int_b91e2f3a",
  "status": "sealed_local",
  "published": false,
  "actor_log_commit": "abc123...",
  "seal_result_hash": "sha256:9f82a3...",
  "warning": "Sealed locally but failed to publish.",
  "suggested_actions": ["mainline publish"]
}
```

---

## 3. 配置位置（Patch C）

### 3.1 文件分布

```
<repo>/
├── .mainline/
│   └── config.toml                    # tracked, 团队协议
├── .github/
│   └── PULL_REQUEST_TEMPLATE.md       # tracked, PR template
├── AGENTS.md                          # tracked, agent 指引
├── .ml-cache/                         # gitignored
│   ├── config.local.toml              # 个人本地偏好
│   ├── identity.toml                  # 本仓库的 actor identity
│   ├── session/
│   ├── drafts/
│   ├── outbox/
│   ├── checks/
│   ├── views/
│   ├── worktrees/
│   └── logs/
└── .gitignore
```

`.gitignore` 增加：

```
.ml-cache/
```

`mainline init` 创建/更新四个 tracked 文件：
- `.mainline/config.toml`
- `.github/PULL_REQUEST_TEMPLATE.md`（如不存在）
- `AGENTS.md`
- `.gitignore`

### 3.2 `.mainline/config.toml`（团队协议，tracked）

```toml
schema_version = 1

[check]
fingerprint_threshold = 0.30
mainline_lookback = 50
require_semantic_judgment_for_suspicious = true
block_on_merged_conflict = true
warn_on_remote_proposed_conflict = true
block_on_remote_proposed_conflict = false

[merge]
require_check = true
require_fresh_sync = true
allow_missing_semantic_judgment = false
forbid_fast_forward = true              # v0.1 默认 true
sync_stale_minutes = 30

[seal]
require_clean_working_tree = true       # v0.1 默认 true
allow_local_only = true

[trailer]
require_seal_hash = true                # 是否要求 trailer 有 Mainline-Seal
verify_seal_hash_on_sync = true

[permissions]
status = "auto"
context = "auto"
list_proposals = "auto"
start = "auto"
append = "auto"
seal_prepare = "auto"
seal_submit = "auto"
seal_submit_with_code_commit = "ask"    # 单独权限
sync = "auto"
publish = "auto"
check_prepare = "auto"
check_submit = "auto"
log = "auto"
show = "auto"
reconcile = "ask"                       # 涉及视图状态变更
merge = "ask"
revert = "ask"
reset = "reject"
run_nested = "reject"
```

### 3.3 `.ml-cache/config.local.toml`（个人偏好，gitignored）

```toml
schema_version = 1

[ui]
prefer_json = false
no_color = false

[sync]
auto_sync_before_check = true
fetch_concurrency = 4

[notifications]
warn_unpublished_intents = true
warn_stale_sync_minutes = 30
```

如果同名 key 同时存在于 `.mainline/config.toml` 和 `.ml-cache/config.local.toml`，**team config 优先**——除了 `[ui]` 和 `[notifications]` 等明确属于个人偏好的 section。

### 3.4 `.ml-cache/identity.toml`（本仓库的 actor 身份）

```toml
schema_version = 1
actor_id = "act_01HW8ZK7Y6Y9E4W2N8M3"      # random ULID
display_name = "Alice"
email_hint_hash = "sha256:abc123..."        # optional, 用于跨设备辅助识别
```

详见 §11 Actor identity。

---

## 4. PR 主路径与 Trailer 可靠性（Patch D）

### 4.1 文档对外的诚实表述

**之前（不可靠）：**

> GitHub squash merge 默认会保留 PR description 作为 commit message，trailer 自然进入。

**之后（诚实）：**

> Mainline 通过 main commit message 中的 `Mainline-Intent` trailer 锚定 merged 状态。
>
> Trailer 落入 main commit 的可靠性取决于团队的 git host 配置。**Mainline 不假装这一定会自动发生**。
>
> 在 GitHub 上，要让 trailer 可靠落到 squash commit，团队需要：
>
> 1. 在仓库 Settings → Pull Requests 中，将默认 squash commit message 设置为 "Pull request title and description"
> 2. 在 PR template 中保留 Mainline trailer 段落
> 3. 在点击 Squash and merge 前，**审查 GitHub 弹出的 commit message 框**，确认 trailers 仍在
>
> 如果 trailer 因任何原因丢失，运行：
>
> ```bash
> mainline reconcile
> ```
>
> 这会让你手动确认哪些 proposed intent 实际上已 merge，并在 actor log 上记录这个确认。

### 4.2 `mainline init` 创建的 PR template

`.github/PULL_REQUEST_TEMPLATE.md`：

```markdown
## Summary

<!-- Describe what this PR does -->



## Mainline

<!-- 
The following metadata anchors this PR to its Mainline intent.
Do NOT remove. Verify it remains in the final squash commit message.

Run `mainline pr-description` to fill these in.
-->

Mainline-Intent: <intent_id>
Mainline-Seal: sha256:<hash>
```

### 4.3 `mainline reconcile`（v0.1 must-ship）

```bash
mainline reconcile [--json]
```

行为：

```
1. mainline sync (强制)
2. 找出本地视图中所有 proposed intent
3. 对每个 proposed intent，检查 main first-parent history:
   a. 是否有 trailer 引用此 intent? → 已 merged，无需 reconcile
   b. 是否有 commit 的 code_commit 匹配此 intent 的 code_commit?
      → 候选 likely_merged
   c. 是否有 commit 的 tree 匹配此 intent 的 code_tree?
      → 候选 likely_merged
   d. 都不匹配？保持 proposed
4. 对每个候选，交互式询问用户：
   ┌─────────────────────────────────────────────────────────────┐
   │ Found a proposed intent that may have been merged:          │
   │                                                             │
   │   int_b91e2f3a  "Refactor auth from session to JWT"         │
   │   Sealed on:   feature-jwt branch (commit def456)           │
   │   Suspected merge commit: a3f8c9d (commit hash matches)     │
   │                                                             │
   │   Mark as merged? [y/N/skip]                                │
   └─────────────────────────────────────────────────────────────┘
5. 用户确认后，写入 actor log:
   IntentMergeAcknowledgedEvent {
     intent_id, suspected_main_commit, reason: "missing_trailer_manual"
   }
6. push actor log
7. 重建本地视图
```

视图层处理：

- 含 `Mainline-Intent` trailer 的 → status = `merged`，confidence = `confirmed`
- 通过 reconcile 确认的 → status = `merged`，confidence = `acknowledged`

`mainline log --mainline` 在 `acknowledged` 项目旁标记 `[ack]`。

### 4.4 不强制 GitHub Action

`mainline init` **不**自动创建 GitHub Action。可选地输出提示：

```
Optional: install Mainline GitHub Action for automatic trailer verification:
  https://github.com/mainline-vcs/check-action
```

v0.5+ 才正式 ship Action。

---

## 5. `mainline merge` 行为（Patch E）

### 5.1 默认禁止 fast-forward

```bash
mainline merge [<intent-id>] [--yes] [--ff]
```

默认行为：

- 必须创建一个 anchoring commit
- 内部使用 `git merge --no-ff` 或 `git merge --squash` + `git commit`
- Trailer 注入此 anchoring commit 的 message

`--ff` 是显式 escape：

- 仅当 feature branch tip commit message 已含 `Mainline-Intent: <intent_id>` trailer 时允许
- 否则报错：

```
error: --ff requires the feature branch tip commit to already contain
the Mainline-Intent trailer. Without an anchoring commit, the trailer
cannot be added retroactively.
Suggested: drop --ff, or amend the tip commit to include the trailer.
```

### 5.2 完整流程

```
1. 强制 mainline sync
2. 验证 intent 状态 = proposed（不能是 sealed_local）
3. 验证 intent 属于当前 thread（多 intent merge 见 §5.3）
4. 跑 mainline check（必须通过，除非 --no-semantic）
5. 检测 fast-forward 可行性:
   - 如果可 ff 且 --ff: 验证 tip 含 trailer → ff
   - 否则: 创建 anchoring commit
6. Anchoring commit message 包含:
   <commit body>
   
   Mainline-Intent: int_b91e2f3a
   Mainline-Seal: sha256:9f82a3...
7. Atomic push (如果支持):
   git push --atomic origin main refs/heads/_mainline/actor/<id>
8. 如不支持 atomic: 先 push main，再 push actor log
9. 任一步骤失败 → 提示 mainline repair
```

### 5.3 多 intent squash 合并

```bash
mainline merge int_b91e2f3a int_c102de45
```

或：

```bash
mainline merge --thread <thread-name>     # 合并 thread 上所有 sealed intent
```

Anchoring commit message：

```
<body>

Mainline-Intent: int_b91e2f3a
Mainline-Seal: sha256:abc...
Mainline-Intent: int_c102de45
Mainline-Seal: sha256:def...
```

按 trailer 出现顺序作为 mainline 中的相对顺序。

### 5.4 `mainline merge` 是否要求所有 intent 来自当前 thread

v0.1：是。多 intent merge 时所有 intent 必须属于同一个 thread。

跨 thread 合并复杂度高（需要协调多个 actor log），推迟到 v0.5。

---

## 6. Event Schema（Patch F）

### 6.1 完整 event 类型

```typescript
type Event =
  | IntentSealedEvent
  | IntentSupersededEvent
  | IntentAbandonedEvent
  | IntentMergeAcknowledgedEvent
  | CheckJudgmentEvent;

interface BaseEvent {
  event_id: string;          // ev_<ulid>
  schema_version: 1;
  type: string;
  actor_id: string;
  created_at: string;        // ISO 8601 UTC
}

interface IntentSealedEvent extends BaseEvent {
  type: "intent.sealed";
  intent_id: string;         // int_<8hex>
  thread: string;
  git_branch: string;
  
  base_commit: string;
  code_commit?: string;      // intent 完成时的 code commit hash
  code_tree?: string;        // 该 commit 的 tree hash（用于 reconcile 匹配）
  
  goal: string;
  
  seal_result: SealResult;        // 完整存储，可审计
  seal_result_hash: string;       // sha256(canonical JSON of seal_result)
  
  // 冗余字段，方便不解析整个 seal_result 时的快速访问
  summary: IntentSummary;
  fingerprint: SemanticFingerprint;
  turns_summary: TurnSummary[];
}

interface IntentSupersededEvent extends BaseEvent {
  type: "intent.superseded";
  intent_id: string;
  superseded_by: string;     // 引用新 intent 的 ID
  reason?: string;
}

interface IntentAbandonedEvent extends BaseEvent {
  type: "intent.abandoned";
  intent_id: string;
  reason?: string;
}

interface IntentMergeAcknowledgedEvent extends BaseEvent {
  type: "intent.merge_acknowledged";
  intent_id: string;
  suspected_main_commit?: string;
  reason: "missing_trailer_manual" | "tooling_recovery";
}

interface CheckJudgmentEvent extends BaseEvent {
  type: "check.judgment_submitted";
  candidate_intent: string;
  judgment_result: CheckJudgmentResult;   // 完整存储
  judgment_result_hash: string;
}
```

### 6.2 Canonical JSON 规则（用于 hash）

`seal_result_hash` 和 `judgment_result_hash` 计算方式：

```
1. 序列化为 UTF-8 JSON
2. 字段名按字典序排序（递归）
3. 去除所有 whitespace
4. 数字按 RFC 8785 标准化
5. sha256
```

提供 `mainline canonical-hash <file>` 工具命令辅助调试。

---

## 7. 命令范围对齐（Patch G）

### 7.1 v0.1 Must ship（最终列表）

**核心生命周期：**

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
```

**审视与查询：**

```
mainline log
mainline show
mainline context              # ← 升级为 must-ship
mainline list-proposals       # ← 升级为 must-ship
```

**冲突检查：**

```
mainline check --prepare
mainline check --submit
mainline check
```

**Merge 流程：**

```
mainline merge
mainline pr-trailer
mainline pr-description       # ← 升级为 must-ship
mainline reconcile            # ← 升级为 must-ship（trailer 缺失 fallback）
```

**Thread 管理：**

```
mainline thread new
mainline thread list
mainline thread close
```

### 7.2 v0.1 不 ship（明确列表）

```
mainline run
mainline replay
mainline rebase
mainline review / comment
mainline why
mainline blame
mainline search
mainline fsck
mainline trust-actor-rewrite
GitHub Action（作为产品）
Web UI / Hub
MCP server
Local LLM
LLM API credentials
PTY trace capture
```

---

## 8. `mainline pr-description`（新增详细规范）

### 8.1 命令

```bash
mainline pr-description [--intent <id>] [--json]
```

输出 markdown，可粘贴到 PR description。

### 8.2 输出示例

```markdown
## Mainline Intent

**Intent:** `int_b91e2f3a`  
**Title:** Refactor auth from session to JWT  
**Author:** Alice  
**Sealed:** 2026-04-25 14:32 UTC

### What changed

Replaced session-based application authentication with JWT access tokens
and refresh token rotation.

### Why

The mobile beta requires stateless authentication; server-side sessions
were blocking deployment to multi-region edge.

### Decisions

- **Migration strategy:** gradual (JWT + session coexist 2 weeks)
  - Rejected: clean break (would break mobile beta)
  - Rejected: dual-write (too complex for the timeline)

- **Token signing:** HS256
  - Rejected: RS256 (key management overhead too high for current scale)

### Risks

- OAuth callback flows still depend on temporary session state
- Token revocation requires refresh token rotation

### Follow-ups

- Migrate OAuth callback to signed state cookies (next sprint)
- Document JWT secret rotation procedure

---

<!-- Mainline metadata - do not remove -->
<!-- Verify these remain in the final squash commit message -->

Mainline-Intent: int_b91e2f3a
Mainline-Seal: sha256:9f82a3b5c1d8e2f4a7b9c2d5e8f1a3b6c9d2e5f8a1b4c7d0e3f6a9b2c5d8e1f4
```

JSON 模式返回结构化版本供工具消费：

```json
{
  "intent_id": "int_b91e2f3a",
  "markdown": "...",
  "trailer_lines": [
    "Mainline-Intent: int_b91e2f3a",
    "Mainline-Seal: sha256:9f82a3..."
  ]
}
```

---

## 9. `mainline context` 算法（v0.1 实现细节）

### 9.1 命令

```bash
mainline context <query> [--json] [--limit <n>] [--type <status>] [--field <field>]
```

参数：
- `<query>`: 任意字符串，将被解释为关键词或路径前缀
- `--limit`: 默认 10
- `--type`: 过滤状态（proposed / merged / all）
- `--field`: 限定匹配字段（subsystems / tags / files / text）

### 9.2 匹配算法

对每个 intent（在视图中）计算 score：

```
score = 0

if query matches subsystems exactly:                  score += 10
if query matches any tag exactly (case-insensitive):  score += 8
if query is substring of summary.title:               score += 5
if query is path prefix of any files_touched:         score += 4
if query matches architectural_claims (substring):    score += 3
if query is substring of summary.what:                score += 2
if query is substring of summary.why:                 score += 2
if query is substring of decisions.point or chose:    score += 2
if query matches tags (substring):                    score += 1
```

返回 `score > 0` 的所有 intent，按 score 降序，取 top N。

### 9.3 输出

```json
{
  "query": "auth",
  "type_filter": "all",
  "matches": [
    {
      "intent_id": "int_b91e2f3a",
      "score": 18,
      "matched_fields": ["subsystems", "tags", "title"],
      "title": "Refactor auth from session to JWT",
      "status": "merged",
      "actor_id": "act_alice",
      "summary": { /* IntentSummary */ },
      "fingerprint_excerpt": {
        "subsystems": ["auth"],
        "tags": ["auth", "jwt", "session-removal"]
      }
    }
  ],
  "total_matched": 7,
  "returned": 3
}
```

### 9.4 AGENTS.md 给 agent 的指引

```markdown
Before working on unfamiliar code, search related intents:

    mainline context <keyword> --json

Use the most specific keyword likely to appear in subsystems or tags.
Examples: "auth", "billing", "src/payments/", "rate-limit".
Avoid full sentences as queries.

When sealing an intent, provide rich tags in the fingerprint to make
later context queries more effective:

    "tags": [
      "auth",                  // primary subsystem
      "authentication",        // common synonym
      "security",              // parent concept
      "jwt", "session"         // related technologies
    ]
```

### 9.5 实现注意

- 视图层在 sync 后构建一个反向索引（subsystem/tag/file → intent_id），加速 context 查询
- 索引重建是 O(N) on intent count，通常 < 100ms

---

## 10. Force push 处理（Patch I）

### 10.1 两类 force push 的不同处理

**Main branch force push**：

接受。Mainline 不拥有 main，只是观察它。

```
mainline sync 检测 origin/main 历史 rewrite:
  1. 警告："origin/main was force-pushed; rebuilding mainline view from new history"
  2. 清空旧 mainline view
  3. 全量重建（按新 main first-parent + actor logs）
  4. 旧 trailer 引用的 commit 如果不在新历史中，对应 intent 退回 proposed
  5. 记录到 .ml-cache/logs/
```

**Actor log force push**：

**默认拒绝**。这是对 intent 历史的篡改风险。

```
mainline sync 检测 actor log 历史 rewrite:
  1. 报错："actor log <id> was force-pushed; mainline refuses to update"
  2. 保留 .ml-cache/views/ 中已知的旧 actor 数据
  3. 该 actor 的新 events 不进入视图
  4. 提示用户运行: mainline trust-actor-rewrite --actor <id>
```

`mainline trust-actor-rewrite`（v0.1 不 ship，v0.5 加）：

```
让用户显式确认接受这个 actor log 的新历史。
明确警告：旧 events 可能被永久丢失。
写入本地 trust 记录。
```

v0.1 的处理是**完全拒绝并显示报错**——不允许工具自动接受 actor log rewrite。如果真的需要修复，用户得手动操作（要么本地 reset actor log，要么联系作者）。

### 10.2 防护建议

`mainline init` 输出：

```
Setup recommended: protect actor log branches in your git host.
Disable force-push on refs matching:
  refs/heads/_mainline/actor/*

GitHub: Settings → Branches → Add branch protection rule
Pattern: _mainline/actor/*
☑ Restrict pushes that create matching branches
☑ Restrict force pushes
```

---

## 11. Actor identity（Patch K）

### 11.1 Actor ID 改为 random ULID

`.ml-cache/identity.toml`：

```toml
schema_version = 1
actor_id = "act_01HW8ZK7Y6Y9E4W2N8M3"     # 随机 ULID
display_name = "Alice"
email_hint_hash = "sha256:abc123..."        # optional
created_at = "2026-04-25T10:00:00Z"
```

### 11.2 首次生成

`mainline init` 在 identity.toml 不存在时：

```
1. 生成 random ULID 作为 actor_id
2. 读 git config user.name 作为 display_name
3. 读 git config user.email，存其 sha256 作为 email_hint_hash（可选，便于辅助识别）
4. 写入 .ml-cache/identity.toml
```

### 11.3 多设备同步

v0.1 不 ship 自动同步——每个设备生成自己的 actor_id。

后续：

```bash
mainline identity export > my-identity.json     # v0.5
mainline identity import my-identity.json       # v0.5
```

文档明确说明：

> Each clone of the repository generates its own actor_id by default.
> If you work from multiple machines, you can manually export/import
> identity to maintain a single actor identity across devices (v0.5+).
> 
> Until then, your contributions from different machines will appear
> as different actors. This is safe but less ergonomic.

### 11.4 Display name 来源

Mainline UI（log/show/context 输出）显示 actor 时：

1. 读 `email_hint_hash`，与本地 actor identities 比对找到 display_name
2. 找不到 → 读该 actor log 上最近 sealed event 的关联 git commit author 字段
3. 还找不到 → 显示 actor_id

这让用户在 `mainline log` 里看到 "Alice" 而不是 "act_01HW8...".

---

## 12. Actor log 写入（Patch H）

### 12.1 隔离要求

**严格规定：actor log commit 必须用 `git commit-tree` + `git update-ref` 创建，不通过 checkout。**

伪代码：

```
fn append_to_actor_log(event: Event) {
    let event_json = canonical_json(event);
    let event_filename = format!("events/{}.json", event.event_id);
    
    // 1. 读取当前 actor log tip 的 tree
    let parent_commit = git_rev_parse("refs/heads/_mainline/actor/{actor_id}")?;
    let parent_tree = git_rev_parse("{parent}^{tree}")?;
    
    // 2. 用临时 index 构建新 tree
    let temp_index = create_temp_index();
    git_read_tree(temp_index, parent_tree)?;
    
    let event_blob = git_hash_object(event_json);
    git_update_index(temp_index, event_filename, event_blob)?;
    
    let new_tree = git_write_tree(temp_index)?;
    
    // 3. 创建 commit
    let new_commit = git_commit_tree(
        new_tree,
        parent: parent_commit,
        author: identity,
        message: format!("seal {}", event.intent_id),
    )?;
    
    // 4. 原子更新 ref
    git_update_ref(
        "refs/heads/_mainline/actor/{actor_id}",
        new_commit,
        expected_old: parent_commit,  // CAS
    )?;
    
    // 5. 清理临时 index
    drop(temp_index);
}
```

**永远不**：
- 不 checkout 到 actor log branch
- 不在 working tree 写文件
- 不让 actor log tree 包含任何代码文件

### 12.2 Actor log tree 内容白名单

每个 actor log commit 的 tree 必须仅包含：

```
events/<event_id>.json     # 一次或多次（一次 commit 可包含多个 event）
manifest.json              # 可选：actor metadata 摘要
```

任何其他文件 → 拒绝并报告 actor log corruption。

### 12.3 同 actor 多机器 publish 冲突

`mainline publish` 算法：

```
fn publish() {
    for attempt in 0..MAX_RETRIES {
        // 1. fetch remote tip
        git_fetch("+refs/heads/_mainline/actor/{actor_id}:refs/remotes/origin/_mainline/actor/{actor_id}")?;
        
        let remote_tip = git_rev_parse("refs/remotes/origin/_mainline/actor/{actor_id}")?;
        let local_tip = git_rev_parse("refs/heads/_mainline/actor/{actor_id}")?;
        
        // 2. 检查关系
        if remote_tip == local_tip {
            return Ok();  // already in sync
        }
        
        if is_ancestor(remote_tip, local_tip) {
            // local ahead, push
            return git_push("origin", "refs/heads/_mainline/actor/{actor_id}");
        }
        
        if is_ancestor(local_tip, remote_tip) {
            // remote ahead, fast-forward local
            git_update_ref("refs/heads/_mainline/actor/{actor_id}", remote_tip)?;
            return Ok();  // nothing to push
        }
        
        // 3. divergence: 同 actor 多设备并发 publish
        // 重新基于 remote tip 应用本地 pending events
        let local_events = extract_events_since(common_ancestor, local_tip);
        let new_local_tip = reapply_on_top(local_events, remote_tip)?;
        git_update_ref("refs/heads/_mainline/actor/{actor_id}", new_local_tip)?;
        
        // 4. retry push
    }
    
    return Err("publish failed after retries; run mainline publish --repair");
}
```

文档明确：

> Cross-actor write contention does not occur (each actor has their own log).
> Same-actor multi-device contention is resolved by fetch-and-reappend during publish.

---

## 13. Sync fetch refspec（Patch L）

### 13.1 完整 refspec

```bash
git fetch origin '+refs/heads/_mainline/actor/*:refs/remotes/origin/_mainline/actor/*'
```

`+` 前缀允许 non-fast-forward update（用于检测 force push，但不自动接受——见 §10）。

实际上 v0.1 不用 `+`，因为我们要拒绝 actor log force push：

```bash
git fetch origin 'refs/heads/_mainline/actor/*:refs/remotes/origin/_mainline/actor/*'
```

如果 fetch 失败（non-fast-forward），就是 force push 信号 → 触发 §10.1 的拒绝路径。

### 13.2 Sync 增量算法

```
fn sync() {
    // 1. fetch main
    git_fetch("origin", "main");
    
    // 2. fetch actor logs
    let result = git_fetch("origin", "refs/heads/_mainline/actor/*:refs/remotes/origin/_mainline/actor/*");
    
    // 3. 处理 force push
    for failure in result.non_fast_forwards {
        warn_force_push_detected(failure.ref);
        // 不自动更新该 actor 的视图
    }
    
    // 4. 增量解析
    let last_sync = read(".ml-cache/views/last-sync.json");
    
    // 4a. main: 解析新 commits 的 trailer
    let new_main_commits = git_log_since(last_sync.main_commit, "origin/main", first_parent: true);
    for commit in new_main_commits {
        let trailers = parse_trailers(commit.message);
        for trailer in trailers {
            update_mainline_view(trailer, commit);
        }
    }
    
    // 4b. actor logs: 解析新 events
    for actor_ref in fetched_actor_refs {
        if force_push_detected(actor_ref) { continue; }
        
        let last_tip = last_sync.actor_tips.get(actor_ref);
        let new_events = extract_events_between(last_tip, current_tip);
        for event in new_events {
            apply_event_to_view(event);
        }
    }
    
    // 5. 重建 fingerprint index
    rebuild_fingerprint_index();
    
    // 6. 写入新 last-sync
    write(".ml-cache/views/last-sync.json", new_state);
}
```

---

## 14. Intent 类型分层（Patch J）

### 14.1 三个独立类型

```typescript
// 1. 本地 draft，存在于 .ml-cache/drafts/
interface DraftIntent {
  intent_id: string;
  schema_version: 1;
  status: "drafting";       // 唯一可能的状态
  
  thread: string;
  git_branch: string;
  base_commit: string;
  
  goal: string;
  
  turns: Turn[];            // 完整 turn 列表（含 caller info）
  
  created_at: string;
  last_modified_at: string;
}
```

```typescript
// 2. Actor log 上的不可变事实（IntentSealedEvent 已定义在 §6）
// 这就是 source of truth
```

```typescript
// 3. 视图层对象，存在于 .ml-cache/views/
interface IntentView {
  intent_id: string;
  schema_version: 1;
  
  // 派生自事件
  status: IntentStatus;
  status_evidence: {
    sealed_event_id?: string;
    superseded_by_intent?: string;
    abandoned_event_id?: string;
    merged_main_commit?: string;
    merged_confidence?: "confirmed" | "acknowledged";
    reverted_main_commit?: string;
  };
  
  publication: "local_only" | "published";
  
  // 来自 sealed event
  actor_id: string;
  thread: string;
  git_branch: string;
  goal: string;
  sealed_at?: string;
  code_commit?: string;
  
  // 摘要（来自 sealed event）
  summary?: IntentSummary;
  fingerprint?: SemanticFingerprint;
  
  // 视图元信息
  view_rebuilt_at: string;
}
```

### 14.2 关键约束

- **DraftIntent 永不进 actor log**——seal 时转换为 IntentSealedEvent
- **IntentSealedEvent 永不被修改**——append-only
- **IntentView 是派生数据**——`mainline sync` 后重建，永不 push 到 remote

实现层面应该有三个独立的 module：

```
ml-storage/
├── drafts.rs        // DraftIntent 读写
├── actor_log.rs     // Event 序列化、验证、append
└── views.rs         // IntentView 重建、查询
```

---

## 15. 数据结构补全（Patch M）

### 15.1 FileChange

```typescript
interface FileChange {
  path: string;
  status: "added" | "modified" | "deleted" | "renamed" | "copied";
  previous_path?: string;        // 仅 renamed/copied 时
  added?: number;                // 行数
  removed?: number;
}
```

### 15.2 DataModelChange

```typescript
interface DataModelChange {
  kind: "added" | "modified" | "removed";
  name: string;                  // table/schema/type 名
  location?: string;             // 文件路径
  compatibility: "breaking" | "compatible" | "unknown";
  migration_required: boolean;
  migration_notes?: string;
}
```

### 15.3 SealPreparePackage

```typescript
interface SealPreparePackage {
  kind: "mainline.seal.prepare";
  schema_version: 1;
  
  intent: {
    id: string;
    goal: string;
    thread: string;
    git_branch: string;
    base_commit: string;
    current_head: string;        // 当前 git HEAD
  };
  
  turns: TurnSummary[];
  
  diff_summary: {
    files: number;
    added: number;
    removed: number;
    files_changed: string[];
  };
  
  changed_files: FileChange[];
  
  output_schema: JsonSchema;     // SealResult 的 schema
  
  instruction: string;           // 给 agent 的自然语言指令
}
```

### 15.4 CheckPreparePackage

```typescript
interface CheckPreparePackage {
  kind: "mainline.check.prepare";
  schema_version: 1;
  
  candidate_intent: {
    id: string;
    title: string;
    summary: IntentSummary;
    fingerprint: SemanticFingerprint;
  };
  
  phase1: {
    lookback: number;
    below_threshold: number;
    suspicious_pairs: number;
  };
  
  judgment_tasks: CheckTask[];   // 空数组表示无可疑配对
  
  output_schema: JsonSchema;     // CheckJudgmentResult 的 schema
  
  instruction: string;
}

interface CheckTask {
  task_id: string;
  
  mainline_intent: {
    id: string;
    title: string;
    status: "merged" | "proposed";   // 区分对方是 merged 还是 proposed
    fingerprint: SemanticFingerprint;
  };
  
  candidate_intent: { id: string };
  
  fingerprint_overlap_score: number;
  
  instruction: string;
}
```

### 15.5 Fingerprint quality（v0.1 加）

```typescript
interface SemanticFingerprint {
  // ... 之前定义的字段
  
  quality?: {
    completeness_score?: number;     // 0-1，agent 自评
    suspected_missing_sections?: string[];
    needs_human_review: boolean;
  };
}
```

`mainline seal --submit` 校验：

```
if fingerprint.files_touched is empty AND
   fingerprint.architectural_claims is empty AND
   fingerprint.behavioral_changes is empty AND
   diff_summary.files > 0:
  warn "low_quality_fingerprint"
  accept anyway, mark in view
```

---

## 16. Supersede source of truth（Patch N）

### 16.1 规则

**`intent.superseded` actor log event 是 supersede 关系的唯一 source of truth**。

`Mainline-Supersedes` trailer 是可选的展示辅助（让 git log 能直接看到 supersede 关系）。

### 16.2 冲突处理

如果 actor log event 和 trailer 不一致：

- View 采用 actor log event 的事实
- Sync 时记录 warning：`"trailer/event mismatch on int_xxx"`
- 不自动修复

### 16.3 文档措辞

```markdown
Supersede source of truth: actor log `intent.superseded` event.
The optional `Mainline-Supersedes` trailer is a display aid for
people reading git log; it is not authoritative.

If they conflict, the actor log wins and sync issues a warning.
```

---

## 17. AGENTS.md 模板（更新）

```markdown
## Mainline

<!-- mainline-agents-md-version: 2 -->

This project uses Mainline to record the intent behind AI-assisted code changes.
You are expected to use Mainline while modifying code.

### Before changing code

Run:

    mainline status --json

If there is no active intent, start one using the user's goal:

    mainline start "<short description of the user's goal>" --json

Before touching an unfamiliar subsystem, query prior history:

    mainline context <keyword> --json

Use specific keywords likely to appear in subsystems or tags. Examples:
"auth", "billing", "src/payments/", "rate-limit". Avoid full sentences.

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

If there is no active intent:

    mainline append "<what changed>" --goal "<user's goal>" --json

### When the task is complete

1. Make sure all code changes are committed (mainline does not commit code by default):

       git add <files> && git commit -m "<message>"

2. Prepare a seal package:

       mainline seal --prepare --json

3. Generate JSON matching the returned schema. When generating fingerprint.tags,
   include primary subsystem term, common synonyms, parent concepts, and
   relevant technologies. Example:

       "tags": ["auth", "authentication", "security", "jwt", "session"]

4. Submit it:

       mainline seal --submit <file> --json

This will publish the intent to the team. The user will then review and merge.

### Semantic conflict checks

When asked to check semantic conflicts:

    mainline check --prepare --json

Generate JSON matching the returned schema. Then submit it:

    mainline check --submit <file> --json

### Do not run unless explicitly asked by the user

    mainline merge
    mainline reconcile
    mainline revert
    mainline reset
    mainline rebase

### When unsure

    mainline status --json
```

---

## 18. 校验规则（更新）

### 18.1 SealResult 校验（无变化）

详见之前 spec。

### 18.2 IntentSealedEvent 校验（新增）

`mainline seal --submit` 在写入 actor log 前必须验证：

1. `seal_result_hash == sha256(canonical_json(seal_result))`
2. `seal_result.intent_id == event.intent_id`
3. `code_commit` 如果存在，必须是 valid git commit hash
4. `code_tree` 如果存在，必须是该 commit 的 tree hash

### 18.3 Trailer 校验（更新）

`mainline sync` 解析 main history 时：

1. 提取 `Mainline-Intent` trailer
2. 在 actor logs 里查找对应 sealed event
3. 如果 trailer 含 `Mainline-Seal`，验证：
   `Mainline-Seal value == sealed_event.seal_result_hash`
4. 不匹配 → warning（视图仍可建，标记 `trailer_seal_mismatch`）
5. Trailer 引用的 intent 找不到 → warning（标记 `dangling_trailer`）

---

## 19. 验收测试更新

### 19.1 状态机测试

```bash
# sealed_local 状态测试
mainline seal --submit fixtures/seal-result.json --local-only --json
# 期望: status = sealed_local, published = false

mainline status --json
# 期望: unpublished_intents = 1

mainline publish
# 期望: status → proposed
```

### 19.2 Working tree clean 测试

```bash
echo "uncommitted change" >> some_file.ts
mainline seal --submit fixtures/seal-result.json
# 期望: error CODE_NOT_COMMITTED

mainline seal --submit fixtures/seal-result.json --commit
# 期望: 自动 commit + seal 成功（如果 seal_submit_with_code_commit 权限允许）
```

### 19.3 Reconcile 测试

setup: Alice 通过 GitHub PR squash merge 但 trailer 丢失。

```bash
cd bob
mainline sync
mainline log --mainline
# 期望: Alice 的 intent 仍是 proposed

mainline reconcile
# 交互确认 → 期望: 写入 acknowledged event，状态变 merged
```

### 19.4 Force push 测试

```bash
# Actor log force push
git push --force origin refs/heads/_mainline/actor/act_alice

cd bob
mainline sync
# 期望: 报错或显眼警告，alice 的视图不更新
```

### 19.5 Multi-device same actor

模拟 Alice 在两台机器上同时 seal 不同 intent：

```bash
# Machine A
mainline seal --submit fixtures/seal-a.json --json
# 期望: published

# Machine B (在 push 之前 seal 另一个 intent)
mainline seal --submit fixtures/seal-b.json --json
# 期望: push 时检测到 divergence，自动 reappend，publish 成功
```

### 19.6 PR description 测试

```bash
mainline pr-description --intent int_b91e2f3a --json
# 期望: markdown 含 summary、decisions、risks、followups + trailer
```

### 19.7 Context 查询测试

setup: 5 个 sealed intent，subsystem 分别为 auth, billing, search, auth, ui

```bash
mainline context auth --json
# 期望: 返回 2 个 auth-related intent，按 score 降序

mainline context src/auth/ --json
# 期望: 返回 files_touched 含此前缀的 intent
```

---

## 20. 里程碑（更新）

### M1: init + identity + tracked files

交付：
- repo 检测
- `.mainline/config.toml` 创建
- `.ml-cache/` 创建
- `.ml-cache/identity.toml` 生成（random ULID）
- `AGENTS.md` 追加（v2 模板）
- `.github/PULL_REQUEST_TEMPLATE.md` 创建（如不存在）
- `.gitignore` 更新

验收：

```bash
mainline init && mainline status --json
git status   # 期望: 4 个 tracked 文件被 stage 或已存在
```

### M2: thread = branch（同前）

### M3: start + append + status + log（基础读写）

加入：`mainline list-proposals`（基础版，列本地视图中所有 proposed intent）

### M4: actor log + seal prepare/submit + sealed_local 处理

交付：
- IntentSealedEvent schema（含 seal_result, hash, code_commit, code_tree）
- Actor log 用 git commit-tree 写入
- `seal --submit` 三态转换（drafting → sealed_local → proposed）
- `seal --submit` working tree clean 检查
- `mainline publish` 命令
- 多设备 same actor 冲突解决

### M5: sync + 视图重建

交付：
- 完整 refspec fetch
- main first-parent 解析 + trailer
- Force push 检测（main 接受 + 重建；actor log 拒绝）
- 三档视图状态：confirmed_merged / proposed / sealed_local
- 反向索引 for context

### M6: check + reconcile

交付：
- Phase 1 fingerprint 算法
- CheckPreparePackage / CheckJudgmentResult schema
- `mainline check --prepare/--submit/无参`
- `mainline reconcile`（v0.1 manual confirm 版）
- `mainline context` 实现（关键词 + 路径前缀）

### M7: merge

交付：
- `mainline merge` 默认禁止 ff
- Anchoring commit + trailer 注入
- `mainline pr-trailer`
- `mainline pr-description`
- Atomic push 处理 + repair 命令

### M8: 内部 adapter trait（同前）

### M9（可选）: why / blame / search / fsck

---

## 21. 开发规则（更新到 25 条）

1. 默认流程是 agent-first
2. AGENTS.md 是主集成接口
3. 每个 agent-facing 命令必须支持 stable `--json`
4. Mainline core **不**调 LLM API
5. v0.1 **无** API key 管理
6. **无** `ml-llm` crate
7. Seal 是 prepare/submit
8. Check 是 prepare/submit
9. `mainline seal --submit` 是 schema 校验 + 状态转换 + 自动 publish
10. `mainline seal --submit` 默认要求 working tree clean，除非 `--commit`
11. `mainline check --prepare` 只做确定性 Phase 1
12. Thread 等价于 git branch
13. Worktree 通过 `--parallel` 可选
14. `mainline run` **不**在 v0.1 公开
15. AgentAdapter 接口内部存在为未来预留
16. Merge 默认人类拥有
17. **`mainline merge` 默认禁止 fast-forward**
18. 无效 agent 输出产出 repairable JSON 错误
19. Agent 失败**永不**腐蚀 Mainline 状态
20. **团队协议在 `.mainline/config.toml`（tracked），本地缓存在 `.ml-cache/`（gitignored）**
21. Drafts 在 seal 之前**永不**进 git remote
22. **状态机：drafting → sealed_local → proposed → merged，每个状态语义明确**
23. **Actor log 写入用 `git commit-tree`，永不 checkout 到 actor log branch**
24. **Actor log force push 默认拒绝；main force push 接受并重建**
25. **Trailer 是主路径但有 reconcile fallback**——Mainline 不假装 trailer 一定可靠

---

## 22. 心智模型（更新）

```
Human uses agent normally
   ↓
Agent calls Mainline CLI with JSON
   ↓
Agent submits structured semantic metadata to Mainline
   ↓
Mainline appends event to actor log (sealed_local)
   ↓
Mainline pushes actor log to git remote (proposed)
   ↓
[Code goes through normal PR workflow]
   ↓
Merge commit on main contains Mainline-Intent trailer (merged)
[OR if trailer missing: mainline reconcile (merged with acknowledged confidence)]
   ↓
Each developer's local view is rebuilt from
  main first-parent history (trailers)
  + all actor logs (events)
```

核心：

> **Mainline is a protocol, not a service.**
> **Your git remote is the database.**
> **Each developer's actor log is their write channel.**
> **Main branch trailers anchor "merged" status (with reconcile as fallback).**
> **The view is rebuilt locally by everyone.**

---

## 附录：术语表（更新）

| 术语 | 定义 |
|---|---|
| Intent | 一段意图，三种形态：DraftIntent（本地）、IntentSealedEvent（actor log 上）、IntentView（视图层） |
| Turn | 一次 agent 工作片段的最小记录单位 |
| Thread | 一组相关 intent 的容器，等价于 git branch |
| Mainline | 已合入 intent 的派生历史视图（本地物化） |
| Seal | 把 turns 规范化为 SealResult 并写入 actor log |
| Publish | 把本地 actor log push 到 remote |
| Actor log | 单个开发者的 append-only event log（一个 git branch） |
| Event | actor log 上的不可变记录 |
| Trailer | main commit message 中锚定 intent 的 metadata |
| Reconcile | 当 trailer 丢失时手动确认 intent 已 merged |
| Sealed local | actor log 本地有事件但未 push |
| Proposed | actor log 已 push，团队可见 |
| Merged | main trailer 确认或 reconcile 确认 |
| Phase 1 | 确定性 fingerprint 比对，无 LLM |
| Phase 2 | Agent 提交的语义判断 |
| `.mainline/` | tracked 团队协议配置 |
| `.ml-cache/` | gitignored 本地状态 |

---

## 附录：开放问题

1. AGENTS.md 已存在但格式不兼容时怎么办？保留原文+追加 Mainline section，用版本注释识别。
2. Intent ID 8 hex 跨仓库碰撞——v0.1 不解决，未来可加 repo prefix。
3. GitHub squash 配置不当时 trailer 丢失——靠 reconcile 兜底。
4. Force push actor log 真的能恢复吗？不能完全恢复。强烈建议 branch protection。
5. Sync 在 50+ actors 团队的性能——增量 sync 应该 OK，但要实测。
6. Schema v2 何时引入？v0.1 只 ship v1。
7. fingerprint_threshold = 0.30 校准时机：收集 ≥50 真实冲突 case 后 grid search。
8. 多设备 same actor identity 同步：v0.1 接受为不同 actor，v0.5 加 export/import。

---

**文档版本**：v0.1-rc2
**状态**：implementation-ready
**变更管理**：通过 GitHub issue 提议、PR 修改

这是真正可以开工的版本。所有 P0/P1 矛盾已修复，所有引用类型已定义，所有命令范围与默认流程对齐。
