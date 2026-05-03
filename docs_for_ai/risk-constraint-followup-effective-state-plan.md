# Risk / constraint / follow-up effective state plan

日期：2026-05-03
状态：docs_for_ai 草案
Mainline intent：`int_dcb93449`

## 问题

Mainline intent summary 里有三类未来向字段：`risks`、`anti_patterns`
和 `followups`。它们本来要解决不同问题，但 dogfood 已经显示出同一类腐化：

1. **生产端诱导 agent 硬写字段**
   - agent 看到 schema 有字段，就倾向于补一条 risk / anti-pattern / follow-up。
   - 很多条目实际是 accepted trade-off、review note、scope note、nice-to-have idea。

2. **消费端把 raw history 当 current state 展示**
   - Hub 和 context 容易展示历史堆积，而不是“当前仍有效、需要处理”的状态。
   - 用户看到大量 prose，不知道哪些需要行动、哪些只是历史说明。

3. **生命周期不完整或没有形成使用闭环**
   - risk 已有 `open / resolved / expired` 设计，但 dogfood 中 resolved 基本为 0。
   - follow-up 已新增 lifecycle，但 open follow-ups 仍大量堆积。
   - anti-pattern / inherited constraint 缺少显式 retire / supersede 机制。

4. **消费触发点不够明确**
   - `seal --prepare`、`context`、`risks`、`followups`、Hub、lint 各自有部分逻辑，
     但没有统一的“effective state”契约。

## 当前已经修复的部分

这份计划只处理剩余问题；不要重复已经落地的工作。

- Follow-up lifecycle 已落地：`mainline followups`、`followups resolve`、
  `resolves_followups`、`applicable_open_followups`、context 的 `open_followups`。
- Hub 可读性已有改进：列表页显示 author，新增 `intents.html`，sidebar 缩放。
- Context query 调试输出已有降噪：breakdown 只在 query mode 输出。
- Preflight feature-branch drift 误报已有降噪。

## 术语边界

### Risk

`summary.risks` 是**当前 intent 在 seal 时新写入**的风险：

> 本次变更新引入、暴露或接受了一个具体失败模式，未来 reviewer / agent 需要知道。

历史风险的消费路径是：

- `applicable_open_risks`：当前改动文件关联到的历史 open risks。
- `resolves_risks`：当前 intent 明确解决了某个历史 risk。
- `mainline risks`：全局 open risk inbox。

不要把 accepted trade-off、review note、普通 follow-up 或“以后可以优化”写成 risk。

### Anti-pattern / constraint

`summary.anti_patterns` 是 hard negative knowledge：

> 未来改同一区域时必须避免的做法，并且必须有 load-bearing `why`。

它通过 `fingerprint.files_touched` 派生成 `inherited_constraints`。当前已经有：

- high-only inheritance
- file-only inheritance
- temporal filter
- `acknowledged_constraints`

缺口是：旧 constraint 没有明确失效 / 替代路径。

### Follow-up

`summary.followups` 是：

> 本次明确切出当前 scope 的后续工作。

最新实现已经把它物化成 follow-up lifecycle，但它仍不应变成 agent 随手生成的 backlog。

## 非目标

本轮不要解决以下问题：

- 不设计全局原则 / mailbox / project memory 系统。
- 不把用户项目原则写入 Mainline skill。
- 不默认修改用户项目的 `AGENTS.md`。
- 不把 Mainline 做成 issue tracker。
- 不一次性重写整个 Hub 信息架构。
- 不提前创建多个 implementation intents；先用这份 design intent 收束边界。

## 设计原则

1. **Effective state 优先**
   - 默认消费和展示当前仍有效的 open/effective 数据。
   - raw summary 字段是历史记录，只在 intent detail / audit 路径里展示。

2. **生产点、消费点、失效点必须分清**
   - seal 生产当前 intent 的新 risk / anti-pattern / follow-up。
   - context / seal prepare 消费历史 open/effective 信息。
   - resolved / expired / retired / converted 等状态决定是否继续出现在默认视图。

3. **CLI 消费必须真实触发**
   - 不能只靠 Hub 页面“提醒”。
   - `context`、`seal --prepare`、`mainline risks`、`mainline followups` 必须能直接返回有效状态。

4. **Hub 是阅读面，不是 source of truth**
   - Hub 默认展示 effective state。
   - Source of truth 仍是 Mainline event log / materialized view。

5. **先修已有三类字段，不扩展到全局原则**
   - 全局原则是相邻问题，但不是本轮第一目标。

## Slice 1：Risk effective consumption

### 目标

让 risk 的默认消费都走 materialized lifecycle，而不是 raw `summary.risks`。

### 设计方向

- `mainline risks` 保持全局 open risk inbox。
- `mainline risks --file <path>` 作为文件相关 risk 入口。
- `context --files/current` 只返回 open risks，且保持 top-N。
- `seal --prepare` 返回 `applicable_open_risks`。
- Hub risks 页默认只展示 open risks；resolved/expired 收起到历史视图。

### 待设计点

是否新增历史 risk disposition：

```json
"acknowledged_risks": [
  {
    "risk_id": "int_xxx#0",
    "disposition": "resolved | still_open | not_applicable",
    "note": "..."
  }
]
```

如果不新增字段，至少需要强化 seal instruction：agent 必须检查
`applicable_open_risks`，已解决时写 `resolves_risks`。

### 验收

- Hub risks 页不再平铺 raw historical `summary.risks`。
- `mainline risks --all` 可以清楚区分 open / resolved / expired。
- `seal --prepare` 中的 risk 列表足够可操作，不是热文件上的长噪音墙。
- dogfood 中至少能产生一条真实 resolved risk，证明闭环可用。

## Slice 2：Constraint retirement / effective constraints

### 目标

让 inherited constraints 有显式失效机制，context/Hub 只展示 effective constraints。

### 设计方向

新增一种显式失效路径，例如：

```json
"retires_constraints": ["int_xxx#0"]
```

或等价的 event / seal field。语义：当前 intent 明确声明某条历史约束不再适用。

失效来源：

- source intent abandoned / reverted（已有部分语义）。
- later intent 显式 retire。
- 文件删除 / rename 时由 doctor 标记 stale candidate，等待人确认。

### 验收

- retired constraint 不再进入 `inherited_constraints` 默认输出。
- Hub 能区分 active constraints 与 retired historical constraints。
- `lint` 只要求 acknowledge effective high-severity constraints。
- 老约束仍可在 intent detail / audit 路径里查到。

## Slice 3：Follow-up triage UX

### 目标

基于已落地的 follow-up lifecycle，让 follow-up 成为可操作 inbox，而不是永久 backlog prose。

### 设计方向

- 保留 `mainline followups` / `mainline followups --file`。
- 默认只展示 open follow-ups。
- resolved / expired 默认隐藏。
- 评估是否增加：

```text
mainline start --from-followup <id>
```

或在 resolution 中记录：

```json
"converted_to_intent": "int_xxx"
```

### 待设计点

当前 `seal --prepare` 可返回大量 `applicable_open_followups`。需要决定：

- 是否 top-N？
- 是否只显示高相关 / 近期 / 明确 action-oriented 的 follow-up？
- 是否把 follow-ups 从 context 默认输出中降级，只保留 followups CLI / Hub inbox？

### 验收

- `mainline followups` 是用户可操作入口。
- `context` 和 `seal --prepare` 不会一次喷几十条 follow-up。
- 用户能从 follow-up 启动新 intent，或明确 resolve/expire 它。

## Slice 4：Hub effective-state view

### 目标

Hub 默认显示当前有效状态，而不是 raw historical prose。

默认视图应突出：

- open risks
- effective constraints
- open follow-ups
- stale / noisy / untriaged counts

raw `summary.risks`、`summary.anti_patterns`、`summary.followups` 只保留在 intent detail / history 区域。

### 验收

- Risks 页不显示 abandoned/expired source 的 risk，除非用户进入 all/history。
- File page 显示 effective constraints，而不是所有历史约束墙。
- Follow-ups 有独立 inbox 或清晰入口。
- 用户从 Hub 能看出“现在该处理什么”。

## 实施顺序

推荐顺序：

1. Risk effective consumption
2. Constraint retirement / effective constraints
3. Follow-up triage UX
4. Hub effective-state view

Hub 放最后，因为它依赖前三个领域的 effective model。

## 开放问题

1. Risk 是否需要 `acknowledged_risks`，还是先只强化 `resolves_risks`？
2. Constraint 失效字段叫 `retires_constraints`、`supersedes_constraints` 还是事件形式？
3. Follow-up 是否需要 `start --from-followup`，还是只需要 `resolve --by-intent`？
4. Context 默认是否应该输出 open follow-ups？如果输出，如何限量？
5. Hub 是否需要 all/history toggle 来访问 raw historical data？
