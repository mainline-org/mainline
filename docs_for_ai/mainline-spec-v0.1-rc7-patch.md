# Mainline · `trace` 命令实施说明

> 目标：实现 `mainline trace` 命令，展示一个 intent 的完整内部历史（turn timeline + tool calls + 文件影响）
> 受众：Claude Code（实施者）
> 应用对象：mainline v0.1-rc6（rc6 已应用）
> 工作量预估：1-2 天

---

## 1. 为什么做这个命令

### 1.1 背景

当前 mainline 有 `mainline log`（看 intents 列表）和 `mainline show <intent>`（看一个 intent 的 summary/decisions/risks）。

但**缺一个命令**：看一个 intent 的**内部 timeline**——它有几个 turn、每个 turn 在干什么、按时间怎么展开的。

### 1.2 核心使用场景

**场景 A：individual review（最重要）**

工程师让 Claude Code 跑了一个复杂任务，跑完想 review "AI 都干了啥"：

- scroll Claude Code 历史很难
- git diff 只看到结果
- 想要个时间序列视图

`mainline trace <intent>` 给出这个视图。

**场景 B：debug "为什么这个 PR 这样改"**

reviewer 看 PR，看到某个文件改动很奇怪。想知道 agent 当时为什么改这里。

`mainline trace` 展示该 intent 内 turn 的时间顺序——能看到 agent 改这个文件之前做了什么、之后做了什么、改了几次。

**场景 C：合规审计（未来 enterprise feature 基础）**

未来如果有受监管行业用户，trace 提供的"完整 turn 历史 + 时间戳 + 文件影响"是审计证据的基础。v0.2 先做基础版，未来扩展。

### 1.3 和现有命令的区别

```
mainline log         # 跨 intents 的列表视图（横向）
mainline show <id>   # 一个 intent 的语义摘要（summary/decisions/risks/fingerprint）
mainline trace <id>  # 一个 intent 的时间序列视图（纵向）  ← 新增
```

三个命令各司其职：
- log = 看哪些 intents 存在
- show = 看一个 intent 的"决策结论"
- trace = 看一个 intent 的"过程展开"

---

## 2. 命令规格

### 2.1 基本签名

```bash
mainline trace <intent_id> [flags]
```

**Flags**：

```
--json              # 机器可读 JSON 输出
--format <fmt>      # 输出格式: text(默认) | compact | detailed | json
--turns-only        # 只显示 turns，不显示元数据
--no-pager          # 不分页
--limit <N>         # 限制显示前 N 个 turn（默认全部）
```

### 2.2 输入

**必需**：`<intent_id>` —— intent 的完整 ID 或前缀（如 `int_b91e2f3a` 或 `int_b91e`）

**ID 解析规则**：
- 完全匹配优先
- 前缀匹配：如果只有一个 intent 匹配前缀，用它
- 多个匹配：报错并列出所有匹配项
- 无匹配：报错

**查询范围**：
- 默认：当前用户的 actor log + 已 sync 的 remote actor logs
- intent_id 不限制 actor——可以查任何已 sync 到本地的 intent

### 2.3 输出形态

#### Default text format（人类阅读）

```
Intent: int_b91e2f3a
Title: Refactor auth from session to JWT
Status: merged (in commit a3f8c9d)
Author: Bob
Thread: feature-jwt
Base: c0c0c0c (main, 2 days ago)

Timeline:
  Started:  2026-04-25 14:00:12 UTC
  Sealed:   2026-04-25 15:23:47 UTC
  Duration: 1h 23m 35s

Turns: 5
─────────────────────────────────────────────────
  #1  14:00:12  start
      "Refactor auth from session to JWT"

  #2  14:08:34  append (+8m22s)
      "Add JWT middleware in src/auth/"

  #3  14:42:11  append (+33m37s)
      "Add refresh token rotation"

  #4  15:15:03  append (+32m52s)
      "Update integration tests"

  #5  15:23:47  seal
      Sealed with: 8 files touched, 3 decisions, 2 risks
─────────────────────────────────────────────────

Files touched (from sealed event):
  src/auth/middleware.ts        (+45 -12)
  src/auth/jwt.ts               (new, +120)
  src/auth/types.ts             (+8 -3)
  tests/auth.test.ts            (+34 -5)
  ... 4 more files

Decisions: 3 (run `mainline show int_b91e2f3a` for details)
Risks: 2 (same)
```

#### Compact format

```
mainline trace int_b91e2f3a --format compact
```

```
int_b91e2f3a "Refactor auth from session to JWT" merged 5 turns 1h23m
  #1 14:00:12  start
  #2 14:08:34  append (+8m22s) "Add JWT middleware..."
  #3 14:42:11  append (+33m37s) "Add refresh token rotation"
  #4 15:15:03  append (+32m52s) "Update integration tests"
  #5 15:23:47  seal (8 files, 3 decisions)
```

#### Detailed format

加上每个 turn 的完整内容（不截断），加上 thread context、actor info 等。

#### JSON format

```bash
mainline trace int_b91e2f3a --json
```

```json
{
  "intent_id": "int_b91e2f3a",
  "title": "Refactor auth from session to JWT",
  "status": "merged",
  "actor_id": "act_abc123",
  "actor_display_name": "Bob",
  "thread": "feature-jwt",
  "base_commit": "c0c0c0c",
  "code_commit": "a3f8c9d",
  "merged_main_commit": "a3f8c9d",

  "started_at": "2026-04-25T14:00:12Z",
  "sealed_at": "2026-04-25T15:23:47Z",
  "duration_seconds": 5015,

  "turns": [
    {
      "index": 1,
      "type": "start",
      "timestamp": "2026-04-25T14:00:12Z",
      "elapsed_from_start_seconds": 0,
      "elapsed_from_previous_seconds": 0,
      "description": "Refactor auth from session to JWT",
      "metadata": {}
    },
    {
      "index": 2,
      "type": "append",
      "timestamp": "2026-04-25T14:08:34Z",
      "elapsed_from_start_seconds": 502,
      "elapsed_from_previous_seconds": 502,
      "description": "Add JWT middleware in src/auth/",
      "metadata": {}
    },
    // ... more turns
    {
      "index": 5,
      "type": "seal",
      "timestamp": "2026-04-25T15:23:47Z",
      "elapsed_from_start_seconds": 5015,
      "elapsed_from_previous_seconds": 524,
      "description": "Sealed",
      "metadata": {
        "files_touched_count": 8,
        "decisions_count": 3,
        "risks_count": 2
      }
    }
  ],

  "summary": {
    "total_turns": 5,
    "files_touched_count": 8,
    "files_touched": ["src/auth/middleware.ts", "..."],
    "all_turns_same_second": false  // 见下面 §2.6
  }
}
```

### 2.4 排序

Turn 按 `timestamp` 升序排列（最早 → 最晚）。

如果多个 turn 同 timestamp（比如批量产生）：
- 按 turn `index`（即 actor log event 的写入顺序）排
- 在输出里**显式标注**这种情况（见 §2.6）

### 2.5 边界情况

#### 情况 A：intent 还在 drafting（未 sealed）

可以 trace。显示 turns 但不显示 sealed 元数据：

```
Intent: int_xxx
Status: drafting (not yet sealed)
Started: ...
Duration so far: 25m

Turns: 3
  #1  ... start
  #2  ... append
  #3  ... append

(intent not yet sealed — no decisions/risks/files data available)
```

#### 情况 B：intent 不存在

```
Error: intent 'int_xxx' not found.
Did you mean: int_b91e2f3a (Refactor auth from...)?
```

#### 情况 C：intent 是 abandoned

```
Intent: int_xxx
Status: abandoned (reason: "Approach didn't work")
Abandoned at: ...

Turns: 4
  #1 ... start
  #2 ... append
  #3 ... append
  #4 ... abandon
```

#### 情况 D：intent 是 superseded

```
Intent: int_xxx
Status: superseded (by int_yyy)
Sealed at: ...
Superseded at: ...

Turns: 5
  #1 ... start
  ...
  #5 ... seal

This intent was superseded by int_yyy. To see the replacement:
  mainline trace int_yyy
```

#### 情况 E：intent 跨多个 actor（不应该发生但 defense in depth）

intent_id 应该是 globally unique，但万一查询命中多个 actor：报错并列出所有。

### 2.6 同秒 turn 标注

dogfood 数据揭示：所有 turn 经常在同一秒内产生（agent 批量写）。这不是 bug，是当前 turn 设计的现实——agent 在 seal 前一次性整理 turn 历史。

**不应该警告这种情况**——它是正常的、不是问题。

但**应该让用户知道**——避免误以为 timestamp 反映真实工作时间。

具体做法：

如果 intent 的所有 turn 都在同一秒内（除了 start 和 seal）：

```
Turns: 5
─────────────────────────────────────────────────
  #1  14:00:12  start
  #2  15:23:45  append   ┐
  #3  15:23:45  append   │ ← all created at seal time
  #4  15:23:45  append   │   (turns recorded together)
  #5  15:23:47  seal     ┘
─────────────────────────────────────────────────

Note: turns #2-#4 share timestamps, indicating they were
recorded together rather than as live progress events.
This is normal — turns serve as a sealing checklist,
not a live activity log.
```

JSON 输出里标记 `summary.all_turns_same_second: true`。

这是产品诚实化——告诉用户 timestamp 的语义，避免误读。

---

## 3. 数据来源

### 3.1 actor log 上的 events

每个 intent 的 turn 数据已经存在 actor log 上：

- `IntentStartedEvent` —— 对应 turn type `start`
- `IntentAppendedEvent` —— 对应 turn type `append`
- `IntentSealedEvent` —— 对应 turn type `seal`
- `IntentAbandonedEvent` —— 对应 turn type `abandon`
- `IntentSupersededEvent` —— 对应 turn type `supersede`

每个 event 含 `created_at` timestamp 和 `intent_id`。

trace 命令的本质：**根据 intent_id 过滤所有相关 event，按 timestamp 排序**。

### 3.2 索引

如果当前实现已经有 intent_id → events 的索引（应该有，给 mainline show 用），trace 直接用。

如果没有，trace 实施时**不应该现做索引**——直接遍历 actor logs 找。性能问题以后再说。

但应该考虑：能否复用 mainline show 的数据加载逻辑？

### 3.3 跨 actor 查询

intent_id 是 globally unique，但事件分散在不同 actor logs 上。

trace 应该：
1. 从本地视图拿到 IntentView（已有数据，知道这个 intent 属于哪个 actor）
2. 读那个 actor 的 actor log（本地或 remote-tracking）
3. 提取所有 `intent_id == target` 的 events
4. 排序、格式化

---

## 4. 实施细节

### 4.1 文件位置

参考现有命令的组织结构：

```
internal/cli/trace.go         # 命令入口（cobra command）
internal/engine/trace.go      # trace 业务逻辑（如果业务复杂度需要）
internal/format/trace.go      # 输出格式化（text/compact/detailed）
```

如果 mainline 当前不是 Go 项目而是 Rust（基于之前的 dogfood 描述不确定），调整为对应语言的 idioms。

### 4.2 复用现有代码

- IntentView 加载：复用 mainline show 的逻辑
- Actor log 读取：复用现有 git ops
- JSON 输出：复用现有 json formatter

### 4.3 不要做的事

**不要**为这个命令做新的索引/缓存。直接读 actor log。

**不要**做花哨的 visualization（progress bar、ASCII art、颜色突出）。简单文本输出就够。

**不要**实现交互式 mode（`--replay` flag）。这是未来 v0.2+ 的事，现在不做。

**不要**改 actor log schema。所有数据已经在 events 里。

**不要**默认调用 sync。trace 是只读的，不需要新鲜数据（如果用户要看的 intent 在本地视图里就能 trace）。

### 4.4 测试要求

```
internal/cli/trace_test.go    # 命令级集成测试
internal/engine/trace_test.go # 业务逻辑单元测试（如果有）
```

**必须覆盖的测试 case**：

1. **基本场景**：intent 有 5 个 turn（start + 3 append + seal），text format 输出
2. **JSON format**：同样 intent，JSON 输出 schema 正确
3. **Drafting intent**：intent 还没 seal，trace 能展示 turns
4. **Abandoned intent**：状态显示正确，含 abandon 原因
5. **Superseded intent**：状态显示，提示 superseder
6. **Same-second turns**：检测同秒、显示提示
7. **Intent not found**：错误信息含 suggestion
8. **Prefix match**：`int_b91e` 能找到 `int_b91e2f3a`
9. **Ambiguous prefix**：多个匹配，列出选项
10. **Cross-actor intent**：朋友的 intent 也能 trace（已 sync 到本地）

**测试 fixture**：

可以基于现有的 conflict-cases fixture 或 dogfood 数据构造。一个有 5+ turn 的 intent 就够。

### 4.5 文档更新

更新这些文档：

- `mainline --help` 输出含 `trace` 命令
- `mainline trace --help` 完整描述（含示例）
- AGENTS.md：在"viewing intent history"段落加 trace（agent 可能想用）
- README：如果 readme 列了主要命令，加上 trace

**AGENTS.md 加这段**：

```markdown
### Viewing intent details

To see an intent's structured summary (decisions, risks, fingerprint):

    mainline show <intent_id> --json

To see an intent's turn timeline (when each turn was added, how long it took):

    mainline trace <intent_id> --json

`show` is for understanding the conclusions; `trace` is for understanding
the process.
```

---

## 5. 验收标准

实施完成的标志：

### 必须满足

- [ ] `mainline trace <intent>` 命令存在且可调用
- [ ] 默认 text 输出格式如 §2.3 所示
- [ ] `--json` flag 输出 schema 如 §2.3 所示
- [ ] 支持 intent_id 前缀匹配（同 mainline show 行为一致）
- [ ] 同秒 turn 显示提示（§2.6）
- [ ] drafting / abandoned / superseded 状态各自正确显示
- [ ] 错误情况（not found, ambiguous prefix）友好提示
- [ ] 10 个测试 case 全通过
- [ ] `mainline --help` 含 trace
- [ ] `mainline trace --help` 完整

### 不在这个 PR 范围内

- [ ] `--replay` 交互式模式（v0.2+）
- [ ] tool use trace（依赖 hooks，v0.2+）
- [ ] reasoning trace（依赖 hooks 或 agent 集成，v0.2+）
- [ ] file diff per turn（需要 turn 携带 diff，未来 schema 扩展）
- [ ] 多 intent compare（未来 v0.3+）

---

## 6. 后续路线（参考，非本次范围）

实施这个 v1 trace 命令后，未来扩展方向：

### v0.2：基础增强

- `--limit N` flag 实施（v1 不一定要做）
- `--from <time>` / `--until <time>` 过滤
- `--turns-only` flag

### v0.3：hook 集成（如果 cross-agent neutral 战略验证）

- 接受 agent runtime 通过 hook 写入 tool_use events
- 新增 turn type: `tool_use`
- trace 输出展示 tool use（read/write/bash）

### v0.4：交互式

- `--replay` mode：交互式逐步展示
- `--compare <intent2>` 对比两个 intent 的 trace

### v0.5+：审计模式

- `--audit` flag：合规导出格式
- 含 cryptographic verification chain
- 给受监管行业用

这些都不在本次实施范围内。

---

## 7. 命名说明（背景）

之前候选命名讨论：

- `replay` —— 暗示真"重播"，夸大了功能（实际只是展示 timeline），放弃
- `trace` —— 准确（"展开 trace"）、developer 熟悉（stack trace、OpenTelemetry）、跨场景适用、可扩展，**选定**
- `recap` / `inspect` / `timeline` —— 各有优点但综合不如 trace

`replay` 这个词留作未来 trace 的子模式（`mainline trace --replay`）—— 真正的交互式逐步重演时再用。

实施时使用 `trace` 作为命令名。

---

## 8. 给实施者的最后说明

### 这个命令的产品意图

不要把它实施成一个"另一个看 intent 的命令"。它有清晰的不同定位：

- `show` 回答 **"What did this intent decide?"**
- `trace` 回答 **"How did this intent unfold over time?"**

text 格式的 output 应该让这个区别**直觉可见**——show 显示决策结论，trace 显示时间展开。

### 不要做"完美"

v1 trace 不需要漂亮——需要 useful。

如果你纠结于："timeline visualization 是不是该用 box drawing characters 更好看"——**停下，用最简单的格式**。

未来如果用户反馈"我想要更漂亮"——再做。

### 验证你的实施

实施完后用 dogfood 数据测试：

1. 跑 `mainline trace <existing intent>` 看输出是否合理
2. 跑 `mainline trace <drafting intent>` 看 drafting 状态
3. 跑 `--json` 看 schema 是否完整
4. 跑同秒 turn 的 intent 看提示是否显示

如果输出"看起来像个 trace 命令该有的样子"——成功。

---

**文档版本**：v1（initial implementation spec）
**应用对象**：mainline v0.1-rc6
**实施工具**：Claude Code
**预估工作量**：1-2 天（含测试和文档）

这版 **`mainline trace` 实施说明是一个很好的收敛**：它把前面关于 turn 的争论落到了一个正确的产品动作上——**不夸大 turn 是 live log，但仍然把它作为 intent 内部 timeline 暴露出来**。

我觉得这份 spec 的方向是对的，尤其是三点：

## 关键判断是稳的

### 1. `trace` 不是 hooks

这很重要。

这版没有把 `trace` 做成“agent runtime instrumentation”，而是只基于已有 actor log events：

* `IntentStartedEvent`
* `IntentAppendedEvent`
* `IntentSealedEvent`
* `IntentAbandonedEvent`
* `IntentSupersededEvent`

这避免了上一轮讨论里的过度扩张。`trace` 是 **现有数据的纵向展开**，不是新系统。

### 2. 同秒 turn 的处理很成熟

你没有把同秒 turn 作为 warning，而是作为 timestamp 语义提示：

> turns recorded together rather than as live progress events

这个非常关键。它既诚实，又不羞辱当前用法，也不会把正常 dogfood 行为误报成异常。

### 3. `show` / `trace` 分工清楚

这句定位很准：

> `show` 回答 “What did this intent decide?”
> `trace` 回答 “How did this intent unfold over time?”

这让 `trace` 有存在意义，而不是变成另一个 `show`。

## 我建议微调的地方

### 1. 不要在 v1 强承诺 “tool calls + 文件影响”

标题里写的是：

> turn timeline + tool calls + 文件影响

但正文后面又说：

> tool use trace 依赖 hooks，v0.2+
> file diff per turn 未来 schema 扩展

所以 v1 实际能做的是：

> turn timeline + sealed fingerprint summary

不是完整 tool calls，也不是 per-turn 文件影响。

我建议把标题改成：

```md
目标：实现 `mainline trace` 命令，展示一个 intent 的内部时间线（turn timeline + sealed fingerprint summary）
```

否则 Claude Code 实施时可能误以为需要找 tool call 数据。

### 2. `--limit` 要么 v1 做，要么从 flags 里删掉

§2.1 把 `--limit <N>` 列为 flag，但 §6 又说：

> `--limit N` flag 实施（v1 不一定要做）

这会造成验收边界不清。

建议二选一：

* 简单做掉 `--limit`，因为实现成本很低；
* 或从 v1 flags 删除，放到 “后续路线”。

我倾向于 **v1 做掉**，因为 trace 很容易变长，`--limit` 是自然保护。

### 3. `--json` 和 `--format json` 需要定义优先级

现在同时支持：

```bash
--json
--format <fmt>
```

建议明确：

```md
`--json` is shorthand for `--format json`.
If both are provided, `--json` takes precedence.
```

或者更简单：只保留 `--json`，不要做 `--format json`。

### 4. “all_turns_same_second” 命名可以更精确

因为 spec 说“除了 start 和 seal”，实际检测的是 append turns 是否同秒。

建议 JSON 字段改成：

```json
"append_turns_recorded_together": true
```

比 `all_turns_same_second` 更符合语义，也避免 start/seal 不同秒时字段名误导。

### 5. v1 不该承诺 “文件影响”过细

默认输出里写：

```text
src/auth/middleware.ts        (+45 -12)
src/auth/jwt.ts               (new, +120)
```

如果 sealed fingerprint 当前没有 line-level diff stats，那这里会导致实现复杂化。

建议改成更保守：

```text
Files touched (from sealed event):
  src/auth/middleware.ts
  src/auth/jwt.ts
  src/auth/types.ts
  tests/auth.test.ts
  ... 4 more files
```

除非 rc6 已经有 `+/-` 数据，否则别在 v1 要求它。

## 总体判断

这份 spec 可以直接给 Claude Code 实施，但我会先做一个小 patch，把范围收紧：

* 标题去掉 tool calls
* v1 输出只承诺 turn timeline + sealed metadata
* 明确 `--json` / `--format json`
* 决定 `--limit` 是否进 v1
* 把同秒字段改成 `append_turns_recorded_together`
* 文件列表不要求 diff stats

