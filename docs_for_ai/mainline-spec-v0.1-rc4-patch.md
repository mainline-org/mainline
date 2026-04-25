# Mainline · v0.1-rc4 Patch — Reconcile usability fix

> 状态：实现层修正（含设计澄清）
> 应用对象：v0.1-rc3
> 修正核心：让 `mainline reconcile` 在 GitHub web UI merge 路径下真正能工作

---

## 修正动机

Dogfooding v0.1-rc3 暴露了一个 P0 设计接缝。Spec 隐含两条 merge 路径：

- **路径 A** — `mainline merge` 自动写 note；视图立即反映 merged。
- **路径 B** — GitHub web UI（"Merge pull request" / "Squash and merge"）触发合并；后续 `mainline reconcile` 写 note。

实践中**路径 B 是默认路径**：team lead approve PR 后随手点 GitHub 按钮，没人会切回 CLI 跑 `mainline merge`。所以路径 B 必须可靠。

rc3 的 reconcile 实现里有三处缺陷让路径 B 落空：

1. `Service.Reconcile()` 跳过 `iv.ActorID != identity.ActorID` 的 intent — 但 note 写在共享 main commit 上，不是写到别人的 actor log，actor 限制是错的。
2. 自动匹配启发式只用 `commit message contains intent.Goal`。GitHub 默认 merge commit subject 是 `Merge pull request #N from ...`，contains goal 的命中率 0。
3. 没有手动指认命令；操作员只能直接 `git notes --ref=mainline/intents add ...`。

本 patch 把这三处一次修掉，并加上一个新数据字段方便审计追溯。

---

## 改动列表

### Patch 1：取消 reconcile 的 actor 限制

`Service.Reconcile()` 不再按 `iv.ActorID` 过滤。任何 actor 都能为任何 proposed intent 写 note。

Note 的 `added_by` 字段记录真实操作者；intent 的 `actor_id` 字段保持原 owner — 两者解耦。

### Patch 2：策略级联匹配

`Service.Reconcile()` 内部按以下优先级尝试匹配：

```
对每个 proposed intent，遍历 main first-parent commits，依次尝试：
  1. tree_hash   — main 上某 commit 的 tree hash 等于 intent.code_commit 的 tree hash
                   (squash merge 不改 tree → 命中率接近 100%)
  2. commit_hash — main 上某 commit 的 hash 等于 intent.code_commit
                   (fast-forward / no-ff merge → 命中)
  3. goal_text   — main commit 的 full message 包含 intent.goal 子串
                   (mainline merge 自己写的 commit message → 命中；GitHub 默认 0)
```

第一个匹配的策略胜出。tree_hash 优先级最高，因为它是 squash 这种"丢 history 但保 tree"的合并的唯一可靠指纹，而 squash merge 是 GitHub 默认行为。

写入 main 的每一行 main commit tree hash 在 reconcile 调用内被缓存，避免 N×M 的 git 调用。

### Patch 3：新命令 `mainline reconcile <intent> <commit>`

显式手动指认。绕过启发式，直接写 `via=reconcile_manual` 的 note。

```bash
mainline reconcile int_d28d37b2 c3f2386
```

兜底场景：rebase 后 tree 改了、agent 没记 code_commit、跨 squash + cherry-pick 等 — 任意启发式都失败时的 last resort。

要求：
- intent 必须存在于本地 view 且状态为 `proposed`（merged/abandoned/superseded 不允许）
- commit 必须能被 `git rev-parse --verify <commit>^{commit}` 解析

### Patch 4：扩展 CommitNote 数据结构

```typescript
interface CommitNote {
  schema_version: 1;
  kind: "mainline.commit_note";

  intents: IntentReference[];
  reverts?: string[];

  added_at: string;
  added_by: string;        // 操作者 actor_id（不一定是 intent owner）

  via:
    | "merge"             // Service.Merge 写的
    | "reconcile_auto"    // Service.Reconcile 自动匹配后写的
    | "reconcile_manual"  // Service.ReconcileManual 写的
    | "reconcile"         // 历史值（rc3 及更早），等价于 reconcile_auto
    | "manual";           // 历史值（rc3 及更早），等价于 reconcile_manual

  match_strategy?:        // 仅 reconcile_auto / reconcile_manual 设置
    | "tree_hash"
    | "commit_hash"
    | "goal_text"
    | "manual";

  reconciled_at?: string;
  reconciled_by?: string;
}
```

`match_strategy` 是新字段，记录"这条 note 是由哪条规则触发的"。审计/调试场景下能直接看出"int_X 的 merged 状态来自 squash 的 tree match" vs "操作员手动指认"。

兼容性：`Service` 内部新增 `normaliseVia()` 把所有历史 via 值（含 `reconcile`/`manual` 的旧形态）折叠到视图层的 `merged_via` 上，view consumer 看到的还是 `merge` 或 `reconcile` 两值。

### Patch 5：CLI 接口

```
mainline reconcile [<intent> <commit>]
```

无参数 → 自动多策略匹配 + push notes。
两参数 → 手动指认 + push notes。
一参数 → 错误（提示需要 commit 参数）。

JSON 输出新增 `links` 字段：

```json
{
  "reconciled": 2,
  "intent_ids": ["int_d28d37b2", "int_298b4476"],
  "links": [
    {"intent_id": "int_d28d37b2", "commit": "c3f2386...", "match_strategy": "tree_hash"},
    {"intent_id": "int_298b4476", "commit": "427ebb1...", "match_strategy": "tree_hash"}
  ]
}
```

人类输出：

```
Reconciled 2 intent(s)
  int_d28d37b2 -> c3f2386 (tree_hash)
  int_298b4476 -> 427ebb1 (tree_hash)
```

---

## 留给后续 rc 的 follow-up

### v0.2 候选：auto-on-sync

`mainline sync` 完成视图重建后，自动跑一次 `Reconcile()` 的高可信度部分（tree_hash + commit_hash），不需要交互。低可信度（goal_text）保留显式触发。

需要新增配置：

```toml
[reconcile]
auto_on_sync = true                       # 默认 false 至少跨一个版本观察一段
require_confirm_for_low_confidence = true # 弱策略仍需手动 reconcile
```

本 patch 不实现 auto-on-sync，保留为下个 rc 的工作 — 自动写共享 ref 是更敏感的默认值，需要更长的观察窗口。

### v0.5 候选：post-merge GitHub Action

```yaml
on:
  pull_request:
    types: [closed]
jobs:
  reconcile:
    if: github.event.pull_request.merged == true
    steps:
      - uses: mainline-vcs/post-merge-action@v1
```

完全消除人工干预。但依赖 GitHub Actions，不能作为 git-host-agnostic 的核心方案 — 仅作为额外便利。

### 命名讨论：reconcile vs link / acknowledge

"reconcile" 一词暗示"修复异常"，但在 rc4 后它实际上是 GitHub web UI merge 路径的**正常步骤**，每个 PR merge 后都需要它。

后续 rc 可以考虑加一个 alias：

```
mainline link <intent> <commit>     # = mainline reconcile <intent> <commit>
mainline link --auto                # = mainline reconcile (no args)
```

`reconcile` 留作"修复不一致状态"（例如 force push 或 history rewrite 后的修补）。`link` 是日常路径。

本 patch 不动 CLI 名字，避免 rc4 引入命名变动；保留为社区讨论。

---

## 心智模型（更新）

```
路径 A — mainline merge:
  feature branch → mainline merge → squash + write note + push
  视图立即 merged。

路径 B — GitHub web UI merge:
  feature branch → PR → "Squash and merge" → main 上多了一个 commit
  视图仍 proposed，因为没 note。
  → mainline reconcile 自动 tree_hash 匹配 → 写 note → push
  视图变 merged。

路径 C — 任意鬼才合并方式（rebase + cherry-pick + 重写 history 等）:
  自动匹配可能失败。
  → mainline reconcile <intent> <commit> 手动指认 → 写 note → push
  视图变 merged。
```

核心：

> Mainline 不强迫团队改 git 工作流。
> 任何让 main 上出现等价 tree 的合并方式，reconcile 都能自动认出。
> 即使是 rebase 重写过的、或者人手 cherry-pick 的，也有显式手动入口。

---

## 验收

实现后跑：

```bash
make quick-test          # 全套单元测试 + reconcile 新增测试通过
./mainline log           # 自己 dogfood：之前需要手补 note 的 4 个 intent，
                         # 现在 reconcile 一次就能正确标 merged
```

测试集（已落到 `internal/engine/reconcile_test.go`）覆盖：

- `TestReconcileAutoTreeHashMatch` — squash merge 路径
- `TestReconcileAutoCommitHashMatch` — fast-forward 路径
- `TestReconcileWorksAcrossActors` — 跨 actor 路径
- `TestReconcileManualPinsCommit` — 手动指认正常路径
- `TestReconcileManualRejectsBadCommit` — 不存在的 commit 拒绝
- `TestReconcileManualRejectsMergedIntent` — 已 merged 的 intent 拒绝
- `TestNormaliseViaBackwardCompat` — 历史 via 值的兼容性

---

**文档版本**：v0.1-rc4 patch
**应用对象**：v0.1-rc3 spec
**状态**：实现完成，待合并到 main
