# Mainline · v0.3 Patch — Coverage Model + Seal Snapshot Contract

> **状态**: **已实现** (v0.3)。基于 mainline-watch 朋友 dogfood 反馈合并两份独立
> issue 写成的统一 spec patch。所有 7 个 patch (A–G) 已落地,见 §10 实施步骤的对应
> 代码位置。
>
> **应用对象**: v0.2(已合并的 fan-out + cat-file batch + auto-pin-on-sync)
>
> **关键不变量**: main 上每个 commit 必须处于三态之一——`covered` / `skipped` /
> `uncovered`,且这是可由 git 直接验证的事实,不是 mainline 私有 schema。

---

## 修正动机

来自 mainline-watch 朋友的两条独立 issue,合并起来揭示同一个产品边界缺口。

### Issue 1: Seal evidence 可被静默污染

> "mainline-watch 里先写了计划文档,但文档一开始没有提交。此时运行 `mainline seal
> --prepare`,evidence 里没有这个文档。后来先提交文档,再重新 prepare,sealed intent
> 才正确包含它。命令成功了,但没有明确告诉 agent '工作区还有东西没进入 evidence'。"

`prepare` 输出虽然带 `current_head`,但 `submit` 完全不校验,且工作区 dirty / untracked
状态既不 surface 给用户、也不会落进 sealed event 的永久记录。

### Issue 2: 手动提交造成 coverage gap

> "intent sealed 到 `08c7f5d`。之后又有一个手动提交 `a5b7f73 sync mainline AGENTS.md`。
> 它不会破坏旧 intent,但旧 intent 的 evidence 也不应该回头覆盖它。"

同类问题不只来自手动提交,也来自 bot commit、cherry-pick、rebase、临时修补、auto-format
等。它们都是同一类:**git 有 commit、mainline 不知道为什么**。

### 合并后的统一不变量

两个 issue 表面是两件事,本质是**同一句话的两半**:

```
sealed intent 必须精确 claim 它所覆盖的 commit;
没被任何 sealed intent claim 的 commit 必须显式可见。
```

如果把 main 上的每个 commit 看成一个槽位,合并后的不变量是:

> **main 上每个可达 commit 处于以下三态之一,且仅一态**:
>
>  - `covered`   — `refs/notes/mainline/intents` 上有 note 指向至少一个 sealed intent
>  - `skipped`   — commit 消息含 `Mainline-Skip:` trailer 或匹配 `[mainline.skip]` 配置
>  - `uncovered` — 既无 note 也无 skip 标记
>
> **没有第四种状态**。

这个不变量把"如何 seal"和"如何观测"统一到同一个 git fact 之下,所有后续命令(status /
gaps / start --commits / 任何未来动词)只是这个状态的不同 view。

---

## §1 — 设计原则

```
1. 数据真相在 git 里,不在 mainline 私有 schema
   coverage 不是 .ml-cache/views/coverage.json 之类的派生物;
   它是 `git notes list` 输出 + commit 消息扫描的直接函数。

2. sealed intent 不可变
   旧 sealed intent 永远不会因为新 commit 出现而被 "回头扩展" claim。
   补录新 commit 走的是新 intent 的正常生命周期。

3. coverage 是状态,不是命令名
   先把状态做出来、可观测,再决定要不要新动词暴露交互。

4. 默认严格,逃生口显式
   evidence_complete=false 不是 warning 后就忘了;
   它进入 sealed event 永久可见。
   `--allow-dirty` 是显式 flag,不是 [seal] allow_dirty=true 这种 config 旁路。

5. 复用现有生命周期
   补录 = start → append → prepare → submit,不另起流水线。
   新增的只有 start 的"输入是哪些 commit"参数。
```

---

## §2 — Patch A: Seal snapshot contract

### A.1 `seal --prepare` 输出 schema 升级

```diff
 {
   "kind": "mainline.seal.prepare",
-  "schema_version": 1,
+  "schema_version": 2,
   "intent": {
     "id": "int_xxx",
     "goal": "...",
     "thread": "...",
     "git_branch": "feature/x",
     "base_commit": "...",
-    "current_head": "..."
+    "current_head": "...",
+    "current_branch": "feature/x"
   },
+  "snapshot": {
+    "prepared_at": "2026-04-26T15:32:11Z",
+    "changed_files": [
+      {"path": "internal/foo.go", "added": 42, "removed": 5, "status": "modified"}
+    ],
+    "worktree_status": "clean" | "dirty" | "untracked",
+    "worktree_dirty_files": ["..."],
+    "evidence_complete": true | false
+  },
   "turns": [...]
 }
```

字段语义:

| 字段 | 来源 | 用途 |
|------|------|------|
| `current_head` | `git rev-parse HEAD` | submit 时 HEAD 一致性校验 |
| `current_branch` | `git rev-parse --abbrev-ref HEAD` | submit 时 branch 一致性校验 |
| `changed_files` | `git diff --numstat base_commit..HEAD` | 让用户/agent 看到 prepare 实际 claim 的文件 |
| `worktree_status` | `git status --porcelain` 解析 | 三态:clean / dirty / untracked |
| `evidence_complete` | `worktree_status == "clean"` | 一等真值,不只是 warning 信号 |

### A.2 `seal --submit` 不变量校验

submit 读取 SealResult 时,**强制校验**:

1. **HEAD 一致性**: `git rev-parse HEAD` == 入参 `intent.current_head`
   - 不一致 → fail,返回 `STALE_PREPARE`,提示"工作区已变,重新跑 `mainline seal --prepare`"
2. **Branch 一致性**: `git rev-parse --abbrev-ref HEAD` == 入参 `intent.current_branch`
   - 不一致 → fail,返回 `BRANCH_DRIFT`
3. **Worktree 完整性**: `evidence_complete == true`
   - 否则 fail,**除非** submit 调用带 `--allow-dirty`
   - `--allow-dirty` 是 CLI flag,不是 config

### A.3 默认严格,逃生口显式

| 场景 | 默认行为 | 逃生口 |
|------|---------|--------|
| HEAD 漂移 | fail | (无;必须重新 prepare) |
| Branch 漂移 | fail | (无;必须切回 prepare 时的 branch) |
| 工作区 dirty | fail | `--allow-dirty` flag |
| 配置项 `[seal] allow_dirty = true` | **不实现** | (反对方案) |

`[seal] allow_dirty = true` 这种 team-config 旁路被显式拒绝:配置项里关掉的安全检查
是 CI 定时炸弹。`--allow-dirty` 在 call site 暴露,每次都要主动声明。

---

## §3 — Patch B: Sealed event schema additions

`evidence_complete` 不能只是 prepare 时的临时提示——必须落进 sealed event,reviewer
查 audit trail 时永远可见。

### B.1 `IntentSealedEvent` 字段补充

```go
type IntentSealedEvent struct {
    BaseEvent
    // ... 现有字段 ...

    // v0.3 新增:
    EvidenceComplete  bool   `json:"evidence_complete"`
    WorktreeStatus    string `json:"worktree_status"`     // "clean" | "dirty" | "untracked"
    SealedAtBranch    string `json:"sealed_at_branch"`    // submit 时实际的 branch
    DirtyFiles        []string `json:"dirty_files,omitempty"` // 仅 worktree_status != "clean" 时填
}
```

### B.2 向后兼容

旧 sealed events 没有这些字段。view-rebuild 读取时:

- 缺失 `EvidenceComplete` → 默认 `true`(legacy 数据假设最优)
- 缺失 `WorktreeStatus` → 默认 `"clean"`
- 缺失 `SealedAtBranch` → 退化用 `GitBranch`(已有字段)

迁移 note:不需要 batch backfill。新事件按新 schema 写,旧事件按 legacy 默认读。

### B.3 显示

`mainline show <intent>` 在 evidence 段加一行:

```
Evidence:
  base_commit:   abc1234
  code_commit:   def5678
  files:         12 modified, 3 added
  evidence:      complete                        # 或 "incomplete (worktree was dirty)"
  branch:        feature/x
```

---

## §4 — Patch C: Coverage 模型(基于 notes ref)

### C.1 三态定义(精确)

对 `refs/heads/<main_branch>` 上的每一个 reachable commit `C`:

```
covered   ⟺  notes-ref 有 C 的 note
              且 note 内 intents[] 至少含一个 sealed intent
              且该 intent 不是 abandoned

skipped   ⟺  C 的 commit message 含 trailer "Mainline-Skip: <reason>"
           或  C 的 commit message 匹配 [mainline.skip].patterns 中任一正则

uncovered ⟺  既不 covered 也不 skipped
```

优先级:`covered` > `skipped`(一个 commit 可以同时既被 sealed intent 覆盖又匹配 skip
模式,这种情况按 covered 计——seal 是更强的 claim)。

### C.2 为什么用 notes ref 做 source of truth

候选方案对比:

| 方案 | 优点 | 缺点 |
|------|------|------|
| **notes ref** ✓ | 已有基础设施(rc3+);跟 commit 走;rebase/cherry-pick 自动正确;多 intent 共享一个 commit 天然支持 | (无明显缺点) |
| Range overlap (`base..code_commit`) | 直观 | merge commit / cherry-pick / 重叠 range 立刻歧义;同一 commit 被多个 intent 覆盖时算法不清晰 |
| `.ml-cache/coverage.json` | 可缓存 | 派生数据,跟 git fact 漂移风险;增加同步债务 |

**结论**: notes ref 是已有事实,coverage 直接从它推导,**不引入新 schema**。

### C.3 边界 case

- **Merge commit**: merge commit 自身的 note 决定状态;feature 分支上的中间 commit 不
  在 main 的可达集里,不参与 coverage 计算。
- **被 force-push 重写的 main**: 老 commit 不再可达,自动从 coverage 视图消失;新
  commit 按规则评估。
- **多 intent 共享 commit**(squash merge 多个 intent): note 的 `intents` 数组天然
  支持,coverage = covered(被任一 sealed intent 引用即可)。
- **Note 指向已 abandoned intent**: 视为 uncovered(abandoned intent 不再算"为什么")。

---

## §5 — Patch D: Skip 机制

没有 skip 机制,coverage 报告会被 `bump version` / `chore: format` / `Merge branch ...`
这类 commit 淹没,信号变废。两种 skip 形式并存:

### D.1 Per-commit trailer

```
chore: bump version to 0.3.1

Mainline-Skip: routine version bump
```

trailer 在 commit message 里,有审计痕迹。`Mainline-Skip:` 后的字符串是必填 reason
(空字符串拒绝 → 强迫提交者写明为什么 skip)。

### D.2 Project-level pattern config

`.mainline/team-config.toml`:

```toml
[mainline.skip]
patterns = [
  "^bump:",
  "^chore: format",
  "^Merge branch ",
  "^Merge pull request "
]
```

正则,匹配 commit message 第一行(subject)。匹配即视为 skipped,reason 是
`"matched config pattern: <pattern>"`。

### D.3 "我忘了加 trailer" 怎么办?

补救路径:

| 状态 | 推荐 |
|------|------|
| commit 未 push | `git commit --amend` 加上 trailer |
| 已 push 但 noone pulled | force-push amend(团队约定允许时) |
| 已分发 | 改 config patterns 加一条匹配它的规则 |
| 都不行 | 接受 uncovered 状态(诚实记录漏过的 commit) |

不引入"retroactive skip note"——一个新的、并行于 commit-trailer 的 skip 机制会让两条
真相打架。

---

## §6 — Patch E: `mainline status` 默认显示 coverage

```text
$ mainline status
Branch:    main
Actor:     actor_xyz
Local:     a5b7f73
Synced:    a5b7f73
Intent:    (none active)
Proposed:  3 intents

Coverage (last 30 commits on main):
  ✓ Covered:    27 (8 sealed intents)
  ⏭ Skipped:    2 (matched: ^chore: format)
  ⚠ Uncovered:  1
    a5b7f73  sync mainline AGENTS.md  (2h ago)

  Run `mainline gaps` for details + rescue options.
```

设计原则:

- **默认显示**(不是 `--coverage` flag)——coverage 只有显眼才有用
- **限制窗口**(`last 30 commits` 之类),否则旧仓库会刷屏;窗口可配
- **uncovered 列出每条;covered/skipped 只给 count**——uncovered 才是 actionable 的
- **JSON 模式**(`mainline status --json`) 同样输出三态分类,供 agent 消费

---

## §7 — Patch F: `mainline gaps` 命令

详细列表 + 建议下一步,从 status 的简短摘要升级成 actionable 视图。

```text
$ mainline gaps
Uncovered commits (oldest first):

  a5b7f73  sync mainline AGENTS.md
           by atlas-comstock  2h ago
           files: AGENTS.md (+12 -3)
           reason: no note on commit, no skip trailer/pattern match

           Suggested actions (best first):
             1. If not pushed:  git reset --soft HEAD^
                                then re-do via `mainline start ...`
             2. If pushed:      mainline start --commits a5b7f73
                                "<your why>"
             3. If routine:     amend with `Mainline-Skip: <reason>`
                                or add config pattern
```

### F.1 命令名为什么是 `gaps` 不是 `reconcile`

`reconcile` 在 v0.2 已被改名为 `pin` 因其语义糊。把它再用在新场景会把改名的成本作废。
其他候选:

- `mainline coverage` — 太抽象,听起来像配置查询命令
- `mainline gaps` — 直白,跟 status 摘要里的"⚠ Uncovered"语义一致 ✓
- `mainline holes` — 同义,但 gap 更专业

选 `gaps`。

### F.2 JSON 模式

```json
{
  "kind": "mainline.gaps",
  "schema_version": 1,
  "uncovered": [
    {
      "commit": "a5b7f73...",
      "subject": "sync mainline AGENTS.md",
      "author": "atlas-comstock",
      "committed_at": "2026-04-26T13:30:00Z",
      "files": [{"path": "AGENTS.md", "added": 12, "removed": 3}],
      "suggestions": [
        {"action": "reset", "applicable": "if not pushed", "command": "git reset --soft HEAD^"},
        {"action": "backfill", "applicable": "if pushed", "command": "mainline start --commits a5b7f73"},
        {"action": "skip", "applicable": "if routine", "command": "amend with Mainline-Skip trailer"}
      ]
    }
  ],
  "skipped": [
    {"commit": "...", "reason": "matched: ^chore: format"}
  ],
  "covered_count": 27
}
```

agent 拿到这个 JSON 可以自动决策(前提是它清楚自己是 push 前还是 push 后)。

---

## §8 — Patch G: 补录入口 `mainline start --commits`

### G.1 语法

```
mainline start "<goal>" --commits <sha[,sha,...]>
mainline start "<goal>" --range <base>..<head>
mainline start "<goal>"                           # 当前行为(走 active branch)
```

`--commits` 是底层原语,接受 commit SHA 列表(逗号分隔或多次 `--commits`)。
`--range <base>..<head>` 是糖,内部 expand 成 `git rev-list base..head` 的输出。

### G.2 与现有 start 的差异

| | 普通 start | `--commits` 补录 |
|---|---|---|
| `intent.git_branch` | 当前 branch | 解析自所列 commits 共同的 branch tip(若不一致则用 main) |
| `intent.base_commit` | branch 的 fork point | `<commits>` 中最早的 parent |
| `intent.code_commit` | seal 时取 HEAD | seal 时取 `<commits>` 中最新的 |
| 工作流 | start → 写代码 → seal | start → 写 turns 描述 → seal(无新代码) |

补录 intent 的 turns 由 agent 写,描述每个 commit 的 why——而不是再写代码。Seal 时
prepare 看到 `worktree_status: clean`(因为没改代码),evidence_complete: true。

### G.3 补录 intent 的 commit_note

submit 后 auto-pin 走 commit-hash 策略(strategy 已存在,见 v0.2 pin cascade),把这
个新 sealed intent 的 ref 写到 `--commits` 列表里每一个 commit 的 mainline note 上。
原本 uncovered 的 commit 立刻变 covered。

---

## §9 — Rescue 优先级文档化

补录工作流不是"建议路径",而是按可逆性排序的有序选项。AGENTS.md 加一段:

```
Encountering an uncovered commit:

1. UNPUSHED — best path
   git reset --soft HEAD^        # un-commit but keep changes
   mainline start "<goal>"
   <continue normal flow>

2. PUSHED, NO FOLLOWUP — backfill path
   mainline start --commits <sha> "<why>"
   mainline append "<turn-by-turn description, post-hoc>"
   mainline seal --prepare > seal.json
   <fill seal.json>
   mainline seal --submit < seal.json

3. PUSHED + ROUTINE — skip path
   git commit --amend                       # if you can still amend
       (add: Mainline-Skip: <reason>)
   git push --force-with-lease              # team must allow this
   - or -
   add a pattern in [mainline.skip] config

4. ALREADY DISTRIBUTED, REGRETTABLY — accept
   Leave it uncovered. Be honest. The mainline log is a record of
   reality, not an aspiration.
```

排序原则:**reversibility first**。reset 是 0-cost、零信息丢失;amend 已有信息丢失风
险;接受 uncovered 是兜底。

---

## §10 — 实施步骤

按依赖关系排序。

### Step 1: Coverage 模型的纯计算函数(0.5 天)

`internal/engine/coverage.go`:

```go
type CoverageState string
const (
    CoverageCovered   CoverageState = "covered"
    CoverageSkipped   CoverageState = "skipped"
    CoverageUncovered CoverageState = "uncovered"
)

type CommitCoverage struct {
    Commit      string
    State       CoverageState
    IntentIDs   []string  // 当 covered 时
    SkipReason  string    // 当 skipped 时
}

// CoverageWindow walks the last N commits on mainRef and classifies each.
// Reads notes ref + commit messages + skip-pattern config. Single pass
// using cat-file --batch (already shipped).
func (s *Service) CoverageWindow(n int) ([]CommitCoverage, error)
```

单元测试覆盖:三态各 case + 优先级(covered > skipped) + abandoned intent 视为
uncovered。

### Step 2: Skip 配置 + trailer 解析(0.5 天)

- `[mainline.skip] patterns = [...]` 在 TeamConfig 加一段
- `Mainline-Skip:` trailer 复用现有 `gitops.ParseTrailers`
- 单元测试:trailer 解析 / pattern 匹配 / 空 reason 拒绝

### Step 3: Seal snapshot contract(1 天)

- `seal --prepare` 增加 snapshot 段
- `seal --submit` 三项 invariant 校验
- `--allow-dirty` flag
- `IntentSealedEvent` schema 升级 + view-rebuild legacy 兼容
- 集成测试:HEAD 漂移 / branch 漂移 / dirty 的 fail 路径 + `--allow-dirty` 显式开

### Step 4: status / gaps surface(1 天)

- `mainline status` 默认显示 coverage 段
- 新命令 `mainline gaps`(text + JSON)
- 集成测试:已知 covered/skipped/uncovered 混合的仓库 → 显示正确

### Step 5: `start --commits` / `--range`(0.5 天)

- start 接受 `--commits`(列表) 和 `--range`(糖)
- 解析逻辑,base_commit / code_commit 的推导
- auto-pin commit-hash 策略覆盖列表里每条 commit
- 集成测试:补录 → uncovered 立刻变 covered

### Step 6: 文档(0.5 天)

- AGENTS.md rescue 优先级一段
- README:status 输出新格式 + gaps 命令简介
- spec doc 状态切到 "implemented in v0.3"

**总计**: 4 天工作量(实现 + 测试 + 文档)。

---

## §11 — 测试验收

### 必须通过

```bash
# Snapshot contract
mainline seal --prepare > /tmp/seal.json
echo "// stray edit" >> some_file.go
mainline seal --submit < /tmp/seal.json     # 期望 fail: HEAD 没漂但 worktree dirty
mainline seal --submit --allow-dirty < /tmp/seal.json   # 期望 success,
                                                         # 但 sealed event 里
                                                         # evidence_complete=false

git checkout other-branch
mainline seal --submit < /tmp/seal.json     # 期望 fail: BRANCH_DRIFT

git checkout original-branch
git commit -m "another change"
mainline seal --submit < /tmp/seal.json     # 期望 fail: STALE_PREPARE

# Coverage model
git commit -m "manual change"               # uncovered
mainline status                              # 期望显示 1 uncovered
mainline gaps                                # 期望列出该 commit + 3 条建议
mainline start "explain the manual change" --commits HEAD
<seal flow>
mainline status                              # 期望 0 uncovered

# Skip mechanism
git commit --amend -m "chore: bump version

Mainline-Skip: routine version bump"
mainline status                              # 期望该 commit 进入 skipped
```

### 不应 regress

- 现有 `seal --prepare` / `seal --submit` 在 worktree clean / no drift 路径下行为不变
- `mainline status` 在没有 uncovered 时,coverage 段简洁(不刷屏)
- view-rebuild 读取 v0.2 sealed events(无新字段)行为不变

### 性能验收

```
mainline status              < 200ms (含 coverage 段)
mainline gaps                < 500ms (含逐 commit detail)
seal --submit invariant 校验 < 50ms (HEAD/branch/status 三个 git fact)
```

`cat-file --batch` 已经 ship,coverage 计算用同一基础设施,性能预算很宽。

---

## §12 — 与 rc6 / 未来工作的关系

### 与 v0.1-rc6 patch(phase-1 eligibility)

正交。rc6 改的是"哪些 intent 该被 phase-1 比对",v0.3 改的是"哪些 commit 被 intent
覆盖"。两者一前一后:rc6 优化已有 sealed 之间的关系,v0.3 让"什么算 sealed 覆盖"本身
变成强不变量。可独立实施;若同时实施,无相互依赖。

### 未来扩展

v0.3 之后可能的演化:

- **`prepare` 创建临时 ref**(`refs/mainline/prepares/<actor>/<intent>`):比 JSON
  自描述更难篡改。本次设计选 JSON-embedded `prepare_head` + invariant 校验,够用;
  如果出现规模化篡改/丢失场景再升级。
- **Coverage 历史趋势**: `mainline coverage --since 7d` 显示一周内 covered/skipped/
  uncovered 比例。dogfood 数据决定要不要做。
- **Auto-suggest skip pattern**: 当 `mainline gaps` 检测到同一类 subject(`^chore:
  bump`)反复 uncovered,提示"加进 [mainline.skip] patterns?"。便利特性,非必需。

### 不会做的事

- **`mainline adopt`**: 不引入新动词;补录复用 `start --commits`。
- **Coverage cache file**: 不引入派生数据;coverage 永远从 git fact 现算。
- **Per-team config 关闭 dirty 检查**: 已被 §A.3 显式拒绝。

---

## 一句话总结

> v0.3 把 mainline 的两个边界(seal 在 claim 什么 / commit 没被 claim 怎么办)合并
> 成一个**强不变量**:main 上每个 commit 三态之一,且这是可由 git 直接验证的事实。
> 所有命令(status / gaps / start --commits)都服务这个模型。

---

**文档版本**: v0.3 spec patch
**应用对象**: v0.2(已合并)
**核心修正**: snapshot contract + coverage 模型,合并到同一个不变量之下
**工作量估计**: 4 天实现 + 测试 + 文档
