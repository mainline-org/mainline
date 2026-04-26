# Mainline · v0.1-rc5 工作流修正 Patch

> 状态：基于 dogfood 真实反馈的工作流优化
> 应用对象：v0.1-rc4（含 reconcile → pin 重命名）
> 修正核心：让"提前发现冲突"这个产品承诺真正兑现

---

## 修正动机

dogfood 数据揭示的真实问题：

1. **`mainline merge` 使用率为 0** —— 13 个 pinned intent 全部 `via: pin`，没有任何 `via: merge`。设计的"完整 merge 命令"在 GitHub PR 工作流下完全没用上。
2. **冲突发现时机太晚** —— 当前 v0.1 主要在 reviewer 跑 `mainline check` 时发现冲突，那已经是 PR 阶段。Seal 时不知道、sync 时不知道——两个人各自工作、谁也不会及时看到对方的 proposed intent。
3. **sync 是"用户主动行为"导致用户不会跑** —— agent 工作流里完全不调 sync，用户也不会习惯性跑 sync，结果 view 经常过时。

综合来看：mainline 在协作场景下的真实价值（提前发现意图冲突）**没有被真正交付**。

---

## 修正概览

### Patch 1：`mainline merge` 降级为 advanced 命令

不删，但移出主路径文档。所有面向新用户的内容只展示 GitHub PR + sync auto-pin 流程。

### Patch 2：sync 后自动跑 conflict check

每次 `mainline sync` 完成后，自动比对本地 proposed/sealed_local intent 和新 fetched 的 remote proposed intent，发现新冲突立即警告。

### Patch 3：`mainline seal --submit` 内部强制 sync + check

Seal 这个动作语义升级——不只是写本地 + push，而是"提交 intent 到团队 + 立即检查冲突"。

### Patch 4：特定命令前的按需 sync

`check`、`list-proposals`、`log --mainline` 等需要新鲜数据的命令，执行前自动检查 sync 时效性，过期则同步一次。

### Patch 5：`mainline status` 显示 sync 时效

长时间未 sync 时温和提醒，让用户养成 sync 习惯。

### Patch 6：AGENTS.md 不再让 agent 主动 sync

Sync 由 mainline 内部处理（在 seal --submit 时自带），agent 流程更简洁。

---

## Patch 1：`mainline merge` 降级

### 现状

v0.1-rc4 spec 中 `mainline merge` 是 must-ship 命令，文档作为 merge 流程的两条路径之一展示。

### 修正

**保留命令但降级展示**：

1. 主流程文档（README、tutorial、AGENTS.md、quickstart）**完全不提** `mainline merge`
2. 命令本身保留，移到 advanced/scripting 文档
3. `mainline merge --help` 输出加一条 NOTE：

   ```
   NOTE: For GitHub/GitLab PR workflows, you don't need this command.
   After merging via the web UI, run `mainline sync` and the merged
   commit will be automatically linked (auto-pin via tree hash).
   
   Use this command only if you don't have a PR system or want to
   merge programmatically (e.g., automation scripts).
   ```

4. v0.5 决策点：观察 6 个月使用率。如果仍然 < 5% 用户使用，正式标记为 deprecated。

### 主流程文档定型

README 的"How it works"段：

```markdown
## How Mainline fits your workflow

1. **Author**: Run your agent normally. It calls Mainline to record
   intent. When the task is done, Mainline publishes the intent to
   your team via your git remote.

2. **Reviewer**: Run `mainline sync` to see team intents. Run
   `mainline check <intent>` to detect semantic conflicts before
   reviewing code. Then review the PR on GitHub as usual.

3. **Merge**: Use GitHub's merge button as usual.

4. **Sync**: Next time anyone runs `mainline sync`, the merged commit
   is automatically linked to the intent. Done.

You never need to use a special "Mainline merge" command. Mainline
adds intent metadata on top of your existing PR workflow—it doesn't
replace your merge process.
```

---

## Patch 2：Sync 后自动 conflict check

### 行为

`mainline sync` 完成后，**自动**做一轮 phase 1 conflict check：

```
sync():
  1. fetch main + actor logs + notes
  2. 重建视图
  3. (auto-pin if config.pin.auto_on_sync)
  4. push notes (if any new pins)
  
  ── 新增 ──
  5. 收集本地 active intents:
     - 本 thread 的 active draft (.ml-cache/drafts/)
     - 本 actor 的 sealed_local intents
     - 本 actor 的 proposed intents
  
  6. 收集新 fetched 的 remote proposed intents
     (上次 sync 之后新出现的)
  
  7. 对每对 (local, new_remote) 跑 phase 1 fingerprint 比对
  
  8. 输出新发现的可疑配对
```

### 输出格式

如果 sync 后无新冲突：

```
✓ Synced
  Fetched: 3 actor logs, 2 new sealed events, 1 new note
  
  No new conflicts detected.
```

如果发现新冲突：

```
✓ Synced
  Fetched: 3 actor logs, 2 new sealed events
  
⚠ Potential conflicts detected:

  Your int_a02c11e8 ("Add email verification flow") 
  may conflict with:
    int_b91e2f3a (Bob, sealed 2h ago)
    "Refactor auth from session to JWT"
    
    Both touch auth subsystem with potentially incompatible
    architectural directions.
    
    Run `mainline check int_a02c11e8` for full analysis.
```

### Draft 的 fingerprint 处理

本 thread 上的 active draft（还没 seal）也要参与比对——这是发现"我正在做的事和别人 sealed 的事冲突"的关键。

但 draft 没有完整 fingerprint。从 draft 推断 partial fingerprint：

```typescript
function partialFingerprintFromDraft(draft: DraftIntent): PartialFingerprint {
  return {
    // 从 turns 累积
    files_touched: union(draft.turns.map(t => t.files_changed.map(f => f.path))),
    
    // 从 goal + turn descriptions 提取关键词
    keywords: extractKeywords(draft.goal + " " + draft.turns.map(t => t.description).join(" ")),
    
    // 没法推断这些
    architectural_claims: [],
    behavioral_changes: [],
    api_changes: [],
  };
}
```

Phase 1 比对时如果是 partial fingerprint，使用降级评分：

```
score = 
    0.40 * jaccard(files_touched)         # 文件重叠主要信号
  + 0.40 * keyword_overlap(goal_keywords)  # 目标关键词
  + 0.20 * subsystem_inference            # 从 file paths 推断 subsystem
```

阈值降低到 `0.25`（partial fingerprint 信息少，需要更敏感的阈值）。

输出标注 confidence：

```
⚠ Potential conflict (low confidence due to partial information):

  Your draft "Refactor sync loop" 
  may conflict with int_b91e2f3a (Bob, sealed 2h ago)
  "Refactor auth from session to JWT"
  
  Reason: file overlap on src/sync/loop.go
  
  This is a heuristic match. Run `mainline check` after sealing
  for full analysis.
```

### 配置

```toml
[sync]
auto_check_after_sync = true                    # 默认开
warn_on_partial_fingerprint_match = true        # 默认开
```

### 实现复杂度

中等。需要：

- Sync 后跟踪 "新 fetched 的 events"（增量）
- Draft 到 partial fingerprint 的转换函数
- Phase 1 比对接受 partial fingerprint 的降级评分

代价 ~ 1 周工作量，价值很大。

---

## Patch 3：`seal --submit` 内部强制 sync + check

### 修改后的 seal --submit 流程

```
seal --submit <file>:

  1. 校验 SealResult schema
  2. 检查 working tree clean
  3. 校验 intent_id 存在且 status = drafting
  4. 校验 files_touched 与真实 changed files 兼容
  
  ── 修改部分开始 ──
  5. mainline sync (强制，除非 --offline)
     - 失败时 (网络问题): 警告但继续 (offline mode)
  
  6. 用刚 sealed 的 fingerprint 跑 phase 1 check vs:
     - merged mainline intents (近 N 个)
     - remote proposed intents
  
  7. 如果发现可疑配对:
     - 不阻止 seal (intent 仍会被写入)
     - 输出明显警告
     - JSON 模式包含 conflict 字段
  ── 修改部分结束 ──
  
  8. 写 IntentSealedEvent 到 actor log (commit-tree)
  9. push actor log
  10. 删除 draft
  11. 状态: drafting → proposed (或 sealed_local if push 失败)
  12. 输出结果 (含 conflict warnings if any)
```

### 输出示例

无冲突 + push 成功：

```
✓ Intent int_b91e2f3a sealed and published
  title: Refactor auth from session to JWT
  status: proposed
  
  No conflicts with team intents.
```

有冲突警告：

```
✓ Intent int_b91e2f3a sealed and published
  title: Refactor auth from session to JWT
  status: proposed

⚠ Potential conflict detected (intent is sealed but you should review):

  Your intent claims: removes server-side session state
  Conflicts with: int_a3f2c901 (Carol, merged 2h ago)
                  "Add OAuth2 provider support"
                  assumes: server-side session for OAuth callback
  
  Severity: high (vs merged mainline)
  
  Options:
  1. Coordinate with Carol about OAuth state strategy
  2. Supersede this intent with a JWT+OAuth-compatible approach
  3. If you accept the divergence, no action needed; reviewer will
     see the conflict during PR review
```

### 关键设计决定：不阻止 seal

即使发现冲突，**seal 仍然完成**。理由：

- Seal 是"我承诺这个 intent"的语义动作，用户已经决定了
- 工具不该替用户做架构决定
- 如果阻止 seal，用户会想办法绕过（`--force` 之类）
- 警告 + 让 reviewer 在 PR 阶段再看一次，是更健康的工作流

但 conflict 信息要明显——JSON 模式让 agent 把冲突报告给用户：

```json
{
  "intent_id": "int_b91e2f3a",
  "status": "proposed",
  "published": true,
  "conflicts": [
    {
      "type": "architectural",
      "severity": "high",
      "with_intent": "int_a3f2c901",
      "with_status": "merged",
      "explanation": "Server-side session state divergence",
      "suggested_actions": ["coordinate", "supersede", "accept"]
    }
  ]
}
```

### `--offline` 标志

允许用户显式跳过 sync：

```bash
mainline seal --submit <file> --offline
```

行为：

- 跳过 sync 和 check
- 写 actor log
- 不 push（offline 模式下网络不通）
- 状态: sealed_local
- 提示：`Run mainline publish when online to share with team.`

---

## Patch 4：特定命令前的按需 sync

### 哪些命令需要新鲜数据

```toml
[sync]
auto_before_command = [
  "check",              # 比对必须最新
  "seal_submit",        # 已在 Patch 3
  "list-proposals",     # 用户专门来看团队状态
  "log_mainline",       # 看主干历史
  "context",            # 跨 actor 查询
  "pin_auto",           # 自动 pin 需要看新 main commits
]

# 不在列表里的命令保持纯本地：
# status, append, start, seal_prepare, show, log (默认), pin (manual), 
# thread *, publish, check_prepare, check_submit
```

### 时效性策略

```toml
sync_freshness_seconds = 300   # 5 分钟内不重复 sync
```

如果 last_sync_at 在 5 分钟内，跳过；否则同步一下再执行命令。

### 实现

```
fn run_command(cmd: &str) -> Result<()> {
    if config.sync.auto_before_command.contains(cmd) {
        let last_sync = read_last_sync()?;
        let elapsed = now() - last_sync.at;
        if elapsed.as_secs() > config.sync.sync_freshness_seconds {
            // 同步触发 sync，让用户知道在等什么
            eprintln!("Syncing with team...");
            match sync() {
                Ok(_) => { /* continue */ }
                Err(NetworkError) => {
                    eprintln!("⚠ Sync failed (offline?). Using local data.");
                    // 不阻塞命令，继续用本地数据
                }
            }
        }
    }
    
    execute_command(cmd)
}
```

### 用户体验

```bash
mainline check int_b91e2f3a
```

```
Syncing with team...
✓ Synced

Checking int_b91e2f3a...
✓ No conflicts found
```

第一次跑加 1-2 秒。5 分钟内再跑：

```bash
mainline check int_b91e2f3a
```

```
Checking int_b91e2f3a...
✓ No conflicts found
```

直接跳过 sync，快。

### 离线 graceful degrade

Sync 失败时**不阻止命令**，只是用本地数据：

```
mainline check int_b91e2f3a
```

```
Syncing with team...
⚠ Sync failed (network error). Using local data (last synced 2h ago).

Checking int_b91e2f3a...
✓ No conflicts found (against locally known intents)

Note: Conflict detection may be incomplete due to stale data.
```

明确告诉用户"我用的是过时数据"。

### 显式 `--no-sync` flag

```bash
mainline check int_b91e2f3a --no-sync
```

跳过自动 sync。用于离线场景或 debug。

---

## Patch 5：`mainline status` 显示 sync 时效

### 修改后的 status 输出

```bash
mainline status
```

```
Mainline ready

Thread:
  branch: feature-jwt
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

如果 sync 时效 > 24 小时：

```
Sync:
  last sync: 2 days ago ⚠
  
  Run `mainline sync` to see recent team activity.
```

如果从未 sync：

```
Sync:
  never synced
  
  Run `mainline sync` to see team intents.
```

JSON 模式：

```json
{
  "sync": {
    "last_sync_at": "2026-04-23T10:20:00Z",
    "elapsed_seconds": 178200,
    "stale": true,
    "stale_threshold_seconds": 86400,
    "unpublished_intents": 0
  }
}
```

`stale` 字段让 agent 知道是不是该提示用户 sync。

### 配置

```toml
[sync]
stale_threshold_seconds = 86400   # 24 小时默认
```

---

## Patch 6：AGENTS.md 简化

### 修改后的 AGENTS.md（关键段落）

```markdown
## Mainline

<!-- mainline-agents-md-version: 3 -->

This project uses Mainline to record the intent behind AI-assisted code changes.

### Before changing code

Run:

    mainline status --json

If there is no active intent, start one:

    mainline start "<short description of the user's goal>" --json

For unfamiliar subsystems, query history:

    mainline context <keyword> --json

### While working

After each meaningful logical change, record a turn:

    mainline append "<specific description of what changed>" --json

If there is no active intent:

    mainline append "<what changed>" --goal "<user's goal>" --json

### When the task is complete

1. Make sure all code changes are committed:

       git add <files> && git commit -m "<message>"

2. Prepare a seal package:

       mainline seal --prepare --json

3. Generate JSON matching the returned schema. Include rich tags in
   the fingerprint (primary subsystem, synonyms, parent concepts,
   related technologies):

       "tags": ["auth", "authentication", "security", "jwt", "session"]

4. Submit it:

       mainline seal --submit <file> --json

   Mainline will automatically sync with the team and check for
   conflicts during seal. If the response includes a `conflicts`
   field, report it to the user clearly.

### Semantic conflict checks

When asked to check semantic conflicts:

    mainline check --prepare --json

Generate JSON matching the schema, then submit:

    mainline check --submit <file> --json

### Do not run unless explicitly asked

    mainline merge
    mainline pin (without --auto)
    mainline revert
    mainline reset

### When unsure

    mainline status --json
```

### 关键变化

**之前**：

```markdown
1. Make sure all code changes are committed
2. Prepare seal package
3. Generate SealResult JSON
4. Submit
5. mainline sync
6. mainline pin --auto
```

**现在**：

```markdown
1. Make sure all code changes are committed
2. Prepare seal package
3. Generate SealResult JSON
4. Submit (sync + check happen automatically inside seal --submit)
```

更短、更清楚。Sync 不再是 agent 的事，是 mainline 内部的事。

---

## 综合修正后的工作流

把所有 patch 应用之后，完整的协作流程是这样：

### 作者侧（B）

```
B: claude
B: "Refactor auth from session to JWT"

agent 内部流程:
  mainline status --json
  mainline start "Refactor auth from session to JWT" --json
  mainline context auth --json   ← 自动触发 sync (5min staleness)
                                    顺手扫一下相关历史
  
  agent 改代码
  
  mainline append "Add JWT middleware" --json
  mainline append "Add refresh token rotation" --json
  mainline append "Update integration tests" --json
  
  mainline seal --prepare --json
  agent 生成 SealResult JSON
  mainline seal --submit /tmp/seal.json --json
  
  ← 内部自动 sync + check
  ← 如果有冲突，JSON 输出 conflicts 字段
  ← agent 把 conflicts 汇报给用户

B: git push origin feature-jwt
B: mainline pr-description --intent int_xxx
B: 在 GitHub 开 PR, 粘贴 markdown
```

### Reviewer 侧（你）

```
你: mainline sync
   ← 自动 fetch + auto-pin + auto-check
   ← 输出: "Fetched 3 actor logs, 2 new sealed events"
   ←       "⚠ Your int_a02c... may conflict with int_b91e... (Bob)"
   ← 你立刻知道有冲突！

你: mainline list-proposals     # 看完整列表 (sync 5min 内不重复)
你: mainline show int_b91e...   # 看 B 的 intent 摘要
你: mainline check int_b91e...  # 完整 check (sync 5min 内不重复)
你: 在 GitHub review 代码
你: Approve

B: GitHub web UI: Squash and merge

任何人下次:
  mainline sync
   ← 自动 auto-pin (tree hash match)
   ← intent 状态变 merged
```

### 关键改进

对比 v0.1-rc4：

- **冲突发现时机提前**：从"reviewer 主动 check"提前到"sync 时自动 + seal 时自动"
- **agent 流程简化**：不再调 sync/pin，全在 mainline 内部
- **用户体验更顺**：sync 是"必要时自带"而不是"手动跑"

对应你 dogfood 中的真实痛点：

- 你和朋友各自工作 → seal 时自动 sync + check，互相看到
- 朋友 GitHub merge 后 → 你下次 sync 自动 auto-pin
- 长时间未 sync → status 提示

---

## 配置文件更新

完整的 `.mainline/config.toml` 新增/修改部分：

```toml
schema_version = 1

# ... 之前的部分

[sync]
auto_check_after_sync = true                    # 新增 (Patch 2)
warn_on_partial_fingerprint_match = true        # 新增 (Patch 2)

auto_before_command = [                          # 新增 (Patch 4)
  "check",
  "seal_submit",
  "list-proposals",
  "log_mainline",
  "context",
  "pin_auto",
]
sync_freshness_seconds = 300                    # 新增 (Patch 4)
stale_threshold_seconds = 86400                 # 新增 (Patch 5)

[seal]
require_sync_before_submit = true               # 新增 (Patch 3)
allow_offline_mode = true                       # 新增 (Patch 3, --offline flag)
warn_on_seal_conflict = true                    # 新增 (Patch 3)
block_on_seal_conflict = false                  # 新增 (Patch 3, 默认不阻止)

[pin]
auto_on_sync = true                             # 已有
high_confidence_strategies = ["tree_hash", "commit_hash"]
medium_confidence_strategies = ["pr_number"]
require_confirm_for_medium_confidence = false
```

---

## 状态机更新

**无变化**——状态机本身没改，但状态转移流程里加了"sync 触发时机"和"check 触发时机"的细节：

```
drafting
  │ ml seal --submit
  │
  │ [seal --submit 内部:]
  │   1. validate
  │   2. sync (强制)
  │   3. check vs remote (warning if conflict)
  │   4. write actor log
  │   5. push
  │
  ▼ (push 成功)
proposed
```

```
proposed (本 actor)
  │ 任何 sync 时触发的 auto-check
  │   if 新增 remote proposed 和本 intent 冲突 → warning
  │
  │ 之后通过 GitHub PR + auto-pin 走向 merged
  │
  ▼
merged
```

---

## 验收测试更新

新增几个关键测试：

### Test: Sync 后自动检测冲突

setup：

- Alice 已 sealed int_alice ("Refactor auth to JWT")
- Bob 在本地有 active draft（goal: "Add session-based feature"）
- Bob 还没 sync

执行：

```bash
cd bob
mainline sync
```

期望：

- sync 完成后输出包含 conflict warning
- warning 引用 int_alice 和 Bob 的 draft
- 由于 draft 是 partial fingerprint，confidence 标为 low

### Test: Seal 时自动 sync + check

setup：

- Alice 已 sealed int_alice ("Refactor auth to JWT")
- Alice push 完成
- Bob 本地从未 sync (cached view 没有 int_alice)
- Bob seal 一个冲突 intent (claim: "session-based")

执行：

```bash
cd bob
mainline seal --submit /tmp/bob-seal.json --json
```

期望：

- 输出 JSON 包含 conflicts 数组
- conflicts[0].with_intent = int_alice
- conflicts[0].severity = "high"
- intent 状态仍变为 proposed (seal 没被阻止)
- exit code 0 (warning 不算失败)

### Test: 离线 seal

setup：

- Bob 网络不通
- 本地有 draft

执行：

```bash
mainline seal --submit /tmp/bob-seal.json --offline --json
```

期望：

- 跳过 sync
- 写 actor log
- 不 push
- 状态: sealed_local
- 输出 published: false
- 提示运行 mainline publish

### Test: 命令前按需 sync

setup：

- 上次 sync 是 10 分钟前
- 期间 origin 有更新

执行：

```bash
mainline check int_xxx
```

期望：

- 命令开始时输出 "Syncing with team..."
- sync 完成后才执行 check
- check 用的是新 fetched 数据

第二次执行（5 分钟内）：

```bash
mainline check int_yyy
```

期望：

- 不再触发 sync（< sync_freshness_seconds）
- 直接执行 check

### Test: Status 显示 sync 时效

setup：上次 sync 是 25 小时前

执行：

```bash
mainline status
```

期望：输出含 `last sync: 25 hours ago ⚠` 和 sync 提示

JSON 模式：

```json
{
  "sync": { "stale": true, "elapsed_seconds": 90000 }
}
```

---

## 里程碑更新

新增 M-rc5 作为 v0.1-rc5 的实施里程碑：

### M-rc5: Workflow refinements

交付：

- Sync 后 auto conflict check
- Seal --submit 内部 sync + check
- 命令前按需 sync 机制
- Status sync staleness 提示
- AGENTS.md v3 模板
- mainline merge 文档降级

验收：

- 所有 6 个新增/修改测试通过
- dogfood 验证：你和朋友各自工作时，seal 时刻能看到对方 proposed
- 文档 review：README 主路径不再提及 mainline merge

时间估计：1 周（已有 sync/check 基础设施，主要是粘合和触发逻辑）

---

## 开发规则（v0.1-rc5 增补）

补充第 27-30 条：

```
27. Sync 是按需触发的同步操作，不是定期后台任务。
    特定命令前 (check, seal_submit, list-proposals, log_mainline, 
    context, pin_auto) 自动 sync，其他命令保持纯本地。

28. Sync 后自动跑一轮 phase 1 conflict check，发现新冲突立即警告。

29. Seal --submit 内部强制 sync + check，但 conflict 不阻止 seal。
    Conflict 信息通过 JSON 输出和警告文本传递给用户。

30. mainline merge 命令保留但不在主路径文档中展示。
    主流程是 GitHub PR + auto-pin。
```

---

## v0.1-rc5 是否真的最终

接近了。这次 patch 主要解决"工作流接缝"——之前 spec 设计的命令都对，但触发时机和组合方式没充分设计。dogfood 暴露了这一点。

修正后剩余的开放问题：

1. **partial fingerprint 算法的准确率**——需要 dogfood 验证。可能阈值要调。
2. **GitHub Action 自动 pin（v0.5+）**——彻底消除"用户忘了跑 sync"的边缘 case。
3. **Watch 模式（v0.5+）**——给重度协作团队的实时通知。

这些不影响 v0.1 完成度，可以收集 dogfood 反馈后决定。

---

## 应用 patch 的具体步骤

如果你要把这个 patch 应用到现有代码：

1. **修改 `mainline sync` 实现**（Patch 2）
   - 在 sync 完成后调用 `auto_conflict_check()`
   - 实现 partial fingerprint 推导
   
2. **修改 `mainline seal --submit` 实现**（Patch 3）
   - 在写 actor log 之前插入 sync + check
   - 添加 `--offline` flag
   - 输出 JSON 包含 conflicts 字段
   
3. **新增 `auto_before_command` 拦截器**（Patch 4）
   - 命令派发层加一个 wrapper
   - 检查 last-sync 时效，过期则触发 sync
   - 添加 `--no-sync` flag
   
4. **修改 `mainline status` 输出**（Patch 5）
   - 计算 sync staleness
   - 添加 stale 字段到 JSON
   - 文本模式加 ⚠ 标记
   
5. **更新 AGENTS.md 模板**（Patch 6）
   - bump 到 v3
   - 简化 seal 流程描述
   - 移除 sync/pin 命令
   
6. **更新文档**（Patch 1）
   - README 主路径不提 mainline merge
   - merge 命令文档移到 advanced/
   - help text 加 NOTE

每一步都可以独立测试。

---

**文档版本**：v0.1-rc5 patch
**应用对象**：v0.1-rc4 spec
**状态**：基于真实 dogfood 数据的工作流优化
**核心目标**：让"提前发现冲突"这个产品承诺真正兑现
