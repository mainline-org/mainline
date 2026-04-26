# Mainline · v0.1-rc6 Patch

> 状态：基于 alpha 用户高质量 issue 反馈的正确性修正
> 应用对象：v0.1-rc5
> 修正核心：把 phase-1 的"什么是冲突候选"这层语义明确化、框架化
>
> **实现状态（v0.2 时点）**：本 patch 描述的 phase-1 eligibility filter
> **尚未实现**。`internal/engine/conflict.go` 仍是 rc5 的 scoring-only
> 行为(`detectSealedConflicts` / `detectSyncConflicts`)，没有 ancestry
> 跳过、没有 abandoned/superseded 跳过、没有 `--explain`。本文件保留
> 作为下一次 phase-1 迭代的设计基线;实现时按文末"实施步骤"推进。

---

## 修正动机

来自 alpha 用户的 issue 揭示了一个 phase-1 算法的**正确性 bug**：

> Phase-1 currently emits warnings for merged intents that are already
> ancestors of the candidate's base commit. This is not a real conflict—
> it's normal sequential development. The overlap score is correct, but
> the pair itself shouldn't have been a candidate.

具体例子：

```
A: Add the initial dashboard project (merged)
B: Add filtering and search to the dashboard (based on A)

两者都改 README.md, src/App.tsx, package.json
```

当前 phase-1 会报警告——但 B 是基于 A 之上的正常 follow-up，不是冲突。

更深一层的洞察是：**这不是单一 bug，是 phase-1 缺失"candidate eligibility"概念**。算法在比对 *任意* 两个 intent 的 fingerprint，但没问"这两个 intent 应不应该被比对"。

这个 patch 不只是修一个 bug，是把 phase-1 的两层语义明确化：

1. **Eligibility（资格）**：哪些 pair 该被纳入比对
2. **Scoring（评分）**：纳入的 pair 谁分高

之前两步混在一起，导致 false positives。

---

## Patch 1：引入 "Phase 1 candidate eligibility" 概念

### 当前算法

```
for each remote_intent in view:
    score = fingerprint_jaccard(candidate, remote_intent)
    if score > phase1_threshold:
        emit warning
```

### 修正后算法

```
for each remote_intent in view:
    if not is_eligible_phase1_candidate(candidate, remote_intent):
        continue   # 不该比对，直接跳过
    score = fingerprint_jaccard(candidate, remote_intent)
    if score > phase1_threshold:
        emit warning
```

### Eligibility 规则

`is_eligible_phase1_candidate(candidate, remote)` 定义：

```go
func isEligiblePhase1Candidate(candidate, remote IntentView) bool {
    // 规则 1: 不和自己比
    if candidate.IntentID == remote.IntentID {
        return false
    }
    
    // 规则 2: merged 的 remote intent 如果已被 candidate base 包含，跳过
    //         (这是这次 issue 的核心修正)
    if remote.Status == StatusMerged {
        if remote.StatusEvidence.MergedMainCommit != "" &&
           candidate.BaseCommit != "" {
            if isAncestor(remote.StatusEvidence.MergedMainCommit,
                          candidate.BaseCommit) {
                return false  // 已是历史的一部分，不是并发冲突
            }
        }
    }
    
    // 规则 3: abandoned 的 remote intent 不参与冲突候选
    if remote.Status == StatusAbandoned {
        return false
    }
    
    // 规则 4: superseded 的 remote intent 跟到 superseder
    if remote.Status == StatusSuperseded {
        if remote.StatusEvidence.SupersededByIntent != "" {
            if newer := lookupView(remote.StatusEvidence.SupersededByIntent); newer != nil {
                return isEligiblePhase1Candidate(candidate, *newer)
            }
        }
        return false
    }

    // 规则 5: 直接 supersede 关系不算冲突
    //         (B supersede A，B 不该和 A 报冲突)
    //
    // 注意：domain 里只存 `SupersededByIntent`(被谁取代)这一向，
    // 没有反向 `Supersedes` 字段。所以 "candidate supersedes remote"
    // 等价于 "remote.SupersededByIntent == candidate.IntentID"。
    if remote.StatusEvidence.SupersededByIntent == candidate.IntentID ||
       candidate.StatusEvidence.SupersededByIntent == remote.IntentID {
        return false
    }
    
    // 默认: 是合法 candidate
    return true
}
```

### 行为不变量

每条规则的设计原则：

- **保守跳过**：当 ancestry / supersede 关系**确定成立**时跳过；不确定时（缺数据、字段为空）继续比对
- **不引入 false negatives**：concurrent 的真实冲突永远不被这些规则跳过
- **不依赖 fingerprint 内容**：eligibility 是 metadata 层面判断，不看 SemanticFingerprint 字段

---

## Patch 2：Ancestry 检查的高效实现

### 朴素实现的问题

```go
// O(N×M) shell-out
for each remote:
    git.MergeBase("--is-ancestor", remote.MergedMainCommit, candidate.BaseCommit)
```

100 个 candidate × 50 个 merged remote = 5000 次 git 调用，每次几 ms，总共 10-30 秒。
对 sync 内置的 phase-1 这个延迟太高。

### 优化：一次性构建 ancestor set

```go
// O(N+M)，一次 rev-list 调用
func buildAncestorSet(commit string) (map[string]bool, error) {
    // 注意：不要加 --first-parent。我们要的是"祖先集合"(包括通过 merge
    // commit 的第二父级带进来的提交);加 --first-parent 会漏掉这些,
    // 让一个真实已合并的 intent 错误地通过 eligibility 过滤。
    output, err := git.Run("rev-list", commit)
    if err != nil {
        return nil, err
    }

    set := make(map[string]bool)
    for _, line := range strings.Split(output, "\n") {
        if line != "" {
            set[line] = true
        }
    }
    // git rev-list <commit> 的输出已包含 commit 自己，无需额外塞入。
    return set, nil
}

// Phase 1 主循环
ancestors, err := buildAncestorSet(candidate.BaseCommit)
if err != nil {
    // 降级: 跳过 ancestry 优化，仍然比对
    log.Warn("ancestry check unavailable", "err", err)
    ancestors = nil
}

for remote in view:
    if remote.Status == StatusMerged && ancestors != nil {
        if ancestors[remote.StatusEvidence.MergedMainCommit] {
            continue  // ancestor，跳过
        }
    }
    // ... 其他 eligibility checks
    // ... fingerprint scoring
```

### 缓存策略

`buildAncestorSet(commit)` 的结果可以缓存：

```
.ml-cache/views/ancestor-sets/<commit_short>.json
```

每个 candidate base commit 的 ancestor set 缓存到本地。Sync 时如果 main 移动了，对应的旧 ancestor set 仍然有效（git 历史 immutable），只需要补充新增部分。

但这是**优化的优化**——v0.1 阶段先实现 in-memory 一次性构建，缓存 v0.2+ 再加。

---

## Patch 3：所有 phase-1 surface 一致行为

修正后的 eligibility filter 必须在**所有 phase-1 触发点**生效。

### 影响范围

```
internal/engine/check.go         # mainline check
internal/engine/conflict.go      # 共享冲突检测逻辑
internal/engine/seal.go          # seal --submit 内部 check
internal/engine/sync.go          # sync auto-check
```

### 实现：抽出共享函数

```go
// internal/engine/conflict.go

func FindPhase1Candidates(
    candidate IntentView,
    view *MainlineView,
    git GitInterface,
) []ConflictCandidate {
    // 一次性构建 ancestor set
    ancestors, _ := buildAncestorSet(candidate.BaseCommit, git)
    
    var candidates []ConflictCandidate
    
    for _, remote := range view.AllIntents() {
        if !isEligiblePhase1Candidate(candidate, remote, ancestors) {
            continue
        }
        
        score := fingerprintJaccardScore(candidate.Fingerprint, remote.Fingerprint)
        if score > view.Config.Phase1Threshold {
            candidates = append(candidates, ConflictCandidate{
                With:  remote,
                Score: score,
            })
        }
    }
    
    return candidates
}
```

所有 phase-1 surface 调用同一个 `FindPhase1Candidates`——保证一致行为。

---

## Patch 4：观测和 debug 支持

### 新增 `--explain` flag for `mainline check`

```bash
mainline check int_xxx --explain
```

输出 phase-1 决策详情：

```
Phase 1 analysis for int_b91e2f3a:

Eligible candidates:
  vs int_concurrent (proposed by Bob)
    score: 0.45 (above threshold 0.30) → WARNING
    
Skipped (not eligible):
  vs int_dashboard (merged 2 days ago)
    reason: merged_main_commit a3f8c9d is ancestor of candidate base
  vs int_oauth (abandoned)
    reason: status = abandoned
  vs int_old_jwt (superseded by int_new_jwt)
    reason: status = superseded; check forwarded to int_new_jwt
    int_new_jwt analysis: skipped, also merged ancestor

Score breakdown for int_concurrent:
  subsystems jaccard: 0.5 (auth)
  files jaccard: 0.3 (src/auth/middleware.ts overlap)
  api overlap: 0.0
  ...
```

这让 debug "为什么没/为什么有警告" 变得可能。也是给 alpha 用户的工具——他们能用 `--explain` 帮你诊断更多 phase-1 问题。

### Sync 输出中的 skipped count（可选）

```bash
mainline sync
```

```
✓ Synced
  Fetched: 3 actor logs, 2 new sealed events
  
Phase 1 check:
  Eligible candidates: 8
  Skipped (ancestry/abandoned): 12
  Above threshold: 1
  
⚠ 1 potential conflict (run mainline check for details)
```

让用户感受到 "filter 在工作"——透明度增强信任。

---

## Patch 5：Spec 中正式定义 "Phase 1 candidate eligibility"

在 v0.1-rc5 的 §10 (语义冲突检测) 后增加新章节：

### §10.X Phase 1 candidate eligibility

Phase 1 conflict detection has two distinct steps:

**Step 1: Eligibility filtering** — Decide which (candidate, remote)
pairs should be scored at all.

**Step 2: Fingerprint scoring** — For eligible pairs, compute
similarity and emit warnings above threshold.

This separation is important: high fingerprint similarity is not
sufficient evidence of conflict if the pair shouldn't have been
compared in the first place.

#### Eligibility rules (v0.1)

A pair `(candidate, remote)` is **eligible** for phase 1 scoring
if and only if all of the following hold:

1. They are different intents (`candidate.id ≠ remote.id`)
2. `remote` is not abandoned
3. If `remote` is superseded, the eligibility check forwards to
   `remote.status_evidence.superseded_by_intent`. If the forwarded
   chain is also non-eligible, the pair is non-eligible.
4. If `remote` is merged AND `remote.status_evidence.merged_main_commit`
   is an ancestor of (or equal to) `candidate.base_commit` AND both
   fields are present, the pair is non-eligible.
5. They are not in a direct supersede relationship — equivalently,
   neither side's `status_evidence.superseded_by_intent` points at
   the other intent's id. (Domain stores only the
   "X is superseded by Y" direction; the reverse is computed by
   matching ids across both sides.)

#### Fallback to compare

When data is incomplete (missing `base_commit`, missing
`merged_main_commit`, broken supersede chain), the eligibility check
**defaults to eligible**. Conservative: better to over-warn than
miss a real conflict.

#### Future eligibility rules (v0.2+)

These are candidates for adding later, based on real-world feedback:

- Same-author followup intents (B by same author as A, based on A)
- Different repository contexts (multi-repo workspaces)
- Intent age (very old proposed intents may be stale)

These are NOT in v0.1 — they require dogfood data to calibrate.

---

## Patch 6：Conflict-cases fixture 增加 ancestry test cases

在 conflict-cases fixture 集中增加这些 case，作为回归测试和文档：

### `case-followup-no-conflict/`

```
Story: A merges, B is based on A, both touch shared files.
Expected: phase-1 outputs zero candidates.

intent A (merged):
  Title: "Add initial dashboard"
  base_commit: c0c0c0
  merged_main_commit: a3f8c9d
  files_touched: [src/App.tsx, README.md, package.json]

intent B (proposed, base_commit = a3f8c9d):
  Title: "Add filtering and search to dashboard"
  files_touched: [src/App.tsx, README.md, package.json,
                  src/Filters.tsx]

Expected phase-1 output:
  eligible_candidates: 0
  skipped:
    - int_A (reason: merged ancestor of base)
```

### `case-concurrent-followup-real-conflict/`

```
Story: A and B both started from c0c0c0 (concurrent).
Both touch dashboard files. A merged first. B's base 
still c0c0c0, NOT a3f8c9d.
Expected: phase-1 reports them as candidate.

intent A (merged, merged_main_commit = a3f8c9d):
  base_commit: c0c0c0
  
intent B (proposed):
  base_commit: c0c0c0          ← key: still old base
  
Expected phase-1 output:
  eligible_candidates: 1
  - int_A (because a3f8c9d is NOT ancestor of c0c0c0)
```

这个 case 测试了 base_commit 的精确语义——B 没 rebase 过，phase-1 必须仍然把 A 作为候选。

### `case-stale-merged-no-conflict/`

```
Story: A merged 6 months ago. B starts fresh today.
Same fingerprint subsystems but unrelated work.
Expected: skipped because A's merge is ancestor of B's base.

(同 case-followup 但时间跨度大、文件可能不同)
```

### `case-abandoned-skipped/`

```
Story: A was sealed but then abandoned.
B touches similar areas.
Expected: skipped because A is abandoned.
```

### `case-superseded-forwarded/`

```
Story: A sealed, then superseded by A2. A2 merged.
B's base contains A2's merge.
Expected: A→A2 forwarded; A2 is ancestor of B's base; both skipped.
```

每个 case 配 setup script + expected outputs（`expected-phase1.json` 显示 `eligible_candidates: []` 和 `skipped: [...]`）。

---

## Patch 7：Sync auto-check 输出更新

v0.1-rc5 的 sync 后 auto-check 输出在 patch 后调整：

之前：

```
✓ Synced
  Fetched: 3 actor logs, 2 new sealed events
  
  No new conflicts detected.
```

之后（更准确）：

```
✓ Synced
  Fetched: 3 actor logs, 2 new sealed events
  
Phase 1 check:
  Eligible pairs: 8 (out of 25 total intents in view)
  Above threshold: 0
  
  No new conflicts detected.
```

让用户看到 "8 个被实际比对、其余 17 个是历史/abandoned 跳过"——透明度提高，也是教育用户什么是 eligible。

非交互场景或简洁模式可以保持原样输出。

---

## 实施步骤

按工作量排序：

### Step 1: 实现 eligibility filter（1 天）

- 在 `internal/engine/conflict.go` 加 `isEligiblePhase1Candidate` 函数
- 实现 `buildAncestorSet`（in-memory，先不做缓存）
- 单元测试覆盖各 eligibility 规则

### Step 2: 集成到所有 phase-1 surface（1 天）

- `check.go` / `seal.go` / `sync.go` 都用共享的 `FindPhase1Candidates`
- 集成测试：构造 fixture 验证四个入口行为一致

### Step 3: Conflict-cases fixture（1 天）

- 5 个 case：followup、concurrent、stale、abandoned、superseded
- setup scripts + expected outputs
- CI 跑这些 fixture 验证 phase-1 行为

### Step 4: --explain 支持（半天）

- `mainline check --explain` 输出 eligibility 决策
- 简单的文本格式，不需要漂亮

### Step 5: Spec 文档更新（半天）

- §10.X 章节
- 更新 sync auto-check 输出格式
- AGENTS.md 不需要改（agent 不直接关心 eligibility）

**总共 4 天工作量**。

---

## 测试验收

### 必须通过

```bash
# 用 case-followup-no-conflict fixture
mainline _fixture-import --case followup-no-conflict
mainline check int_B --json
# 期望: empty conflicts array
# 期望: 不在 sync 后 phase1 warnings cache 中
```

```bash
# 用 case-concurrent-real fixture  
mainline _fixture-import --case concurrent-real-conflict
mainline check int_B --json
# 期望: conflicts 含 int_A
```

### 性能验收

构造 100 个 merged intent + 1 个 candidate 的 view，测量：

```
sync (含 auto-check): < 5 秒
mainline check: < 2 秒
```

如果超过——上 ancestor set 缓存。

### 现有测试通过

之前的 PBT 和 integration test 不应该 regress。如果某些之前期望 warning 的 case 现在不 warning 了，检查它们：

- 是 fixture 设计错误（应该 update fixture）
- 还是真实回归（必须修）

---

## 这次 patch 比 v0.1-rc5 的不同

v0.1-rc5 是**工作流补完**——把 spec 里设计的命令串成顺畅流程。

v0.1-rc6 是**正确性修正**——发现产品语义层面的 bug，修概念定义。

这是产品演化的两个不同阶段：

```
v0.1-rc5: "命令对了，但触发时机不对" → 修触发
v0.1-rc6: "算法对了，但输入选错了" → 修边界
```

随着 alpha 用户增加，会出现更多类似 rc6 这种"细化产品语义"的修正。这是好信号——意味着用户在用产品的概念框架推理，能帮你完善边界。

---

## 后续

在 spec 里把 v0.1-rc1 → v0.1-rc6 整合成一份 v0.1 final 时，结构应该是：

```
§10. 语义冲突检测
  §10.1 总体策略 (phase 1 + phase 2)
  §10.2 Phase 1: candidate eligibility   ← 新增 (rc6)
  §10.3 Phase 1: fingerprint scoring     ← 原 §10.1 (rc1-5)
  §10.4 Phase 2: agent-driven judgment   ← 原 §10.2 (rc1-5)
  §10.5 触发时机                          ← 整合 rc5 内容
```

这样阅读 spec 的人能看到 phase-1 的两层结构而不是一团。

---

## 给 alpha 用户的反馈

这个 issue 的写作者（无论是你、朋友还是新 alpha 用户）应该收到具体反馈：

```
Thanks for the detailed analysis—you correctly identified that this 
is a candidate filtering gap, not a scoring issue. We're shipping 
the fix in v0.1-rc6, with the eligibility framework formalized in 
the spec. Your example cases became part of our regression fixture 
set. Credit in CHANGELOG.
```

这种 issue 是产品最珍贵的礼物。回应必须是：

1. 快速响应（确认要修，给时间承诺）
2. 修得彻底（不只修这个 case，修整个概念）
3. 显眼归功（CHANGELOG / commit / release notes）
4. 邀请深度参与（下次设计时 ping 他们）

---

**文档版本**：v0.1-rc6 patch
**应用对象**：v0.1-rc5
**核心修正**：把 phase-1 的 candidate eligibility 概念从隐式变显式，修复 ancestry-aware filtering 这个具体 bug
**工作量估计**：4 天实现 + 测试 + 文档
