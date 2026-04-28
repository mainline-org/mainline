# Mainline 产品原则最终版

**适用对象**：内部 3 人对齐 / 后续路线决策 / 未来贡献者
**版本**：v1.0 internal
**目的**：用一份短文档固定 Mainline 的产品形态、核心命题、和 Git AI 的边界，以及接下来的实现优先级。

---

## 1. 一句话定位

**Mainline 是 AI 编程团队的 git-native intent memory layer。**

它把工程工作的 intent、decision、risk、fingerprint、lifecycle 结构化记录在 git 里，让 agent 和人类在读代码前先理解历史 why。

更短的说法：

> **Mainline gives agents the why before the code.**

中文：

> **Mainline 让 agent 在读代码前先理解历史意图。**

---

## 2. 产品核心判断

Mainline 的基本单位不是 line、prompt、PR，也不是普通 commit message。

Mainline 的基本单位是 **intent**。

一个 intent 记录：

```text
- 这次工程工作想做什么
- 为什么做
- 做了哪些 decisions
- 识别了哪些 risks
- 影响哪些 files / subsystems / commits
- 后来 merged / abandoned / superseded / reverted 了吗
- 未来 agent 应该记住什么
```

Mainline 的目标不是记录 AI 每一步做了什么，而是沉淀高质量的工程决策记忆。

因此 Mainline 的长期价值不是“更多日志”，而是：

```text
让 agent 写代码前更准
让 reviewer 看代码前更清楚
让团队协作前更早感知冲突
让未来的人和 agent 能找回历史 why
让重要变更有可检查的 intent coverage
```

---

## 3. Core loop：五层产品结构

### 1. Intent Capture

```text
start / append / seal
```

创建、补充、固化 intent。

关键原则：

```text
append 不是 live progress log。
append 是 seal preparation / thinking scaffold。
seal 才是核心价值点。
```

`start` 记录工作意图。
`append` 帮助整理中间上下文。
`seal` 把一次工程工作固化为可审查、可检索、可复用的 decision record。

---

### 2. Intent Memory

```text
show / trace / context
```

三种读取方式：

```text
show    = 看这个 intent 最后决定了什么
trace   = 看这个 intent 如何展开
context = agent 开工前找相关历史 intent
```

其中 `context` 是下一阶段最重要的 agent-facing 能力。

正确 agent workflow：

```text
mainline context → understand historical why → inspect current code → edit
```

不是不读代码，而是先读 intent，再读代码。

---

### 3. Intent Integrity

```text
pin / supersede / abandon / check
```

保证 intent memory 不变成散乱笔记。

```text
pin       = intent ↔ commit 绑定
supersede = 决策演化
abandon   = 失败路线也进入记忆
check     = 检查 overlap / conflict / consistency
```

尤其重要的是 `abandon` 和 `supersede`：它们让 agent 不重复历史失败路线，也不拿过期决策当当前事实。

---

### 4. Intent Distribution

```text
sync / cross-actor / remote storage
```

Mainline 的 canonical shared storage 是 git remote。

```text
Local CLI       = 本地读写 intent
Git remote      = canonical shared intent log storage
Hub             = enhanced centralized read/index/collaboration layer
```

原则：

```text
Hub 可以中心化。
Source of truth 不要变成 Hub。
Hub 必须能从 git-backed intent logs 重建。
```

这一层支撑团队级 intent awareness：

```text
sync              = 拉取团队 intent memory
cross-actor logs  = 每个 actor 独立 append，不互相阻塞
remote storage    = 团队共享的 canonical intent log
check             = 基于 fingerprint / subsystem / claim 的早期冲突检测
list-proposals    = 查看 in-flight / submitted work
```

正确协作路径：

```text
sync → see team intents → detect overlap/conflict → adjust before PR
```

---

### 5. Intent Surfaces

```text
agents / pr-description / hub / webhook
```

这些都是 intent memory 的不同读法，不是新的 source of truth。

```text
agents         = 让 coding agent 知道先 mainline context 再 grep
pr-description = 给 reviewer 看 summary / decisions / risks
hub            = 给团队和人类浏览 intent memory
webhook        = 把 intent lifecycle event 发给外部系统
```

---

## 4. 五大核心命题

Mainline 需要证明五个命题。五个已经足够，再往后不应继续加“命题”，而应该回到这五个下面拆功能。

最终优先级是：

```text
1. Agent pre-edit memory
2. Intent governance / accountability
3. Human review intent
4. Long-term decision memory
5. Intent-aware collaboration
```

---

### 命题 1：Agent pre-edit memory

```text
显式 intent memory 能否让 coding agent 在改代码前理解历史 why，
从而比 code-first agent 更少犯错？
```

这是当前第一优先级。

如果这个成立，Mainline 的 agent-facing 价值成立。

核心能力：

```text
context reliability
agent guidance: context before grep
agent eval harness
code-first vs intent-first 对比
```

验收问题：

```text
agent 是否先运行 mainline context？
context 是否找到了相关 intent？
agent 是否识别 decisions / risks / anti_patterns？
agent 是否避免 forbidden mistake？
agent 是否在编辑前验证 current code？
```

---

### 命题 2：Intent governance / accountability

```text
团队能否知道哪些重要工程变更有清晰 intent，哪些没有；
哪些 AI-assisted / high-risk changes 缺少 why / decisions / risks；
从而让 AI 编程从“事后相信 diff”变成“有可检查的 intent coverage”？
```

这是第二优先级。

原因：governance 不是后期 enterprise bonus，而是 intent memory 的质量控制层。

如果没有 governance：

```text
sealed intent 质量不稳定
context 不可靠
reviewer 不信 intent
长期记忆会腐烂
```

核心能力：

```text
lint
doctor
status uncovered commits
pin coverage
high-risk change requires risks / decisions
Hub coverage / uncovered / risk-heavy views
```

验收问题：

```text
重要变更有没有 intent？
intent 质量够不够？
commit 有没有 pin 到 intent？
high-risk subsystem 有没有 decisions / risks？
哪些 commits / PRs 是 uncovered？
```

边界：

```text
Mainline 做 intent governance，不做 AI line attribution。
Mainline 关心“重要变更是否有可审查的 intent record”，不是“哪行代码是不是 AI 写的”。
```

---

### 命题 3：Human review intent

```text
Reviewer 能否通过 sealed intent，在看 diff 前理解 why / decisions / risks，
从而把 review 从“猜作者意图”变成“验证代码是否实现了意图”？
```

这是第三优先级。

它连接 Mainline 和人类 review 场景，也是 PR description、Hub、audit 的基础。

正确 review 路径：

```text
read intent → understand why / decisions / risks → inspect diff → verify implementation
```

Mainline 不替代 reviewer，也不自动 approve code。

Mainline 的作用是把 review 从：

```text
看 diff 猜作者 / agent 的 intent
```

变成：

```text
看 intent 验证实现是否符合 intent
```

核心能力：

```text
pr-description
show
trace
lint
Hub intent detail
audit view
```

验收问题：

```text
pr-description 能否展示 why / decisions / risks / rejected alternatives？
show 能否让 reviewer 快速理解 sealed intent 的结论？
trace 能否让 reviewer 必要时查看 intent 展开过程？
lint 能否防止低质量 seal 进入 review？
reviewer 是否仍需要从 diff 反推作者意图？
```

---

### 命题 4：Long-term decision memory

```text
6 个月后，新人、人类 reviewer、未来 agent 能否通过 Mainline 找回当时的 why / decisions / risks / supersede chain，
从而不依赖 Slack、过期文档、口口相传或 git blame 猜历史？
```

这是第四优先级，但它是 Mainline 长期价值的根。

它解释了 Mainline 为什么不是一次性 PR helper，也不是 agent session tool，而是工程团队的长期 intent memory。

正确长期回看路径：

```text
file / subsystem / query → context / hub / show → decisions / risks / supersede chain → current effective intent
```

服务对象：

```text
new engineer       = onboarding 时理解代码背后的 design rationale
future maintainer  = 修改旧代码前理解过去的 decisions / risks
future agent       = 改陌生代码前避免重复旧错误或使用过期决策
```

核心能力：

```text
context by file / subsystem / query
show
trace
supersede chain
abandoned intent
Hub file page
Hub risk view
Hub graph view
```

验收问题：

```text
这个文件为什么这样？
这个旧 decision 还有效吗？
有没有被 supersede？
以前试过哪些失败路线？
新人能否 5 分钟理解这段代码的 design rationale？
未来 agent 会不会重复旧错误？
```

---

### 命题 5：Intent-aware collaboration

```text
团队成员和 agent 能否通过 sync / cross-actor logs / check，
在 PR 之前感知彼此的 intent、发现 overlap 和冲突，
从而减少重复工作和 late-stage review conflict？
```

这是第五优先级。

它必须保留在产品叙事里，因为 Mainline 不只是单 agent memory，也是团队共享 intent memory。但在当前阶段，它不应该压过 agent memory、governance、review 和长期记忆。

正确协作路径：

```text
sync → see team intents → detect overlap/conflict → adjust before PR
```

核心能力：

```text
sync
cross-actor logs
remote storage
check
list-proposals
status team freshness
Hub actor / file / risk / conflict views
```

验收问题：

```text
sync 后能否清楚看到队友 recent / in-flight intents？
check 能否解释为什么两个 intent overlap？
status 能否提示 team data freshness？
list-proposals 能否作为团队 intent awareness 入口？
Hub 能否展示 actor / file / risk / conflict 视图？
```

---

## 5. 和 Git AI 的边界

不能把 Git AI 简化成“只做 attribution”。Git AI 已经覆盖 AI-generated code 的 prompt/session context、line-level attribution、agent `/ask`、team dashboard 等方向。

所以 Mainline 的边界要更精确。

### Git AI 主要回答

```text
这段 AI 代码从哪里来？
哪个 agent / model / prompt / session 产生了它？
AI-authored code 在 PR / review / production 中如何流动？
```

它的基本单位是：

```text
AI-authored line / hunk / checkpoint / prompt session
```

### Mainline 主要回答

```text
这次工程变更为什么存在？
做了哪些 decisions？
承担了哪些 risks？
影响哪些 subsystems？
这个 decision 后来被 abandon / supersede / revert 了吗？
未来 agent 应该如何基于它继续工作？
```

它的基本单位是：

```text
engineering intent
```

### 一句话边界

```text
Git AI preserves the session behind AI-authored code.
Mainline creates an explicit decision record for engineering intent.
```

中文：

```text
Git AI 保存 AI 代码背后的 prompt/session。
Mainline 保存工程变更背后的显式 intent/decision record。
```

---

## 6. 我们不打的战场

暂时不要追 Git AI 的强项：

```text
AI line attribution
AI blame
AI code percentage
prompt-to-production dashboard
token cost / AI adoption metrics
agent telemetry analytics
```

Mainline 应该占住：

```text
explicit intent lifecycle
curated sealed decision record
abandon / supersede / revert as first-class memory
agent context before grep
human review intent
intent governance / coverage
hub as intent memory reader
audit from decision record, not raw transcript
```

核心判断：

> Prompt is not a decision record. Transcript is not an intent lifecycle.

---

## 7. 产品名

继续使用：

> **Mainline**

原因：

```text
1. 已经有实现、命令、文档和内部心智积累。
2. git-native / actor log / remote storage 的底层隐喻仍然成立。
3. “把分散工作收束成主线记忆”这个含义可以服务 intent memory。
4. 现在改名会消耗注意力，而当前最重要的是验证 context 是否真的让 agent 更准。
```

不用再纠结新名字。

短期重点不是 branding，而是证明：

```text
agent 使用 mainline context 后，是否更少犯错？
```

---

## 8. Slogan

主 slogan：

> **Mainline gives agents the why before the code.**

中文：

> **Mainline 让 agent 在读代码前先理解历史意图。**

更完整的产品描述：

> **Git-native intent memory for AI-assisted engineering.**

对比 Git AI 的版本：

> **Git AI tracks where AI code came from. Mainline records why engineering changes exist.**

---

## 9. 下一步实现顺序

最终实现优先级：

```text
Product thesis
→ Context reliability
→ SealResult lint
→ Agent guidance: context before grep
→ Agent eval harness
→ Governance: doctor / coverage / uncovered commits
→ Review context: pr-description / show / trace
→ Long-term memory: hub file pages / supersede chains
→ Collaboration coherence: sync / check / list-proposals
→ Hub static reader polish
```

---

### Step 1：Product thesis 收口

交付：

```text
docs_for_ai/product-thesis.md
README 第一屏
Git AI boundary doc
agent guidance wording update
```

统一使用这句话：

```text
Mainline is a git-native intent memory layer for AI-assisted engineering.
```

---

### Step 2：Context Reliability v1

目标：让 `mainline context` 成为 agent 开工前可信入口。

必须支持：

```bash
mainline context --current --json
mainline context --files <paths...> --json
mainline context --query "<task>" --json
```

输出必须包含：

```text
relevant intents
relevance reasons
status: current / superseded / abandoned / stale
summary
top decisions
top risks
anti_patterns
files touched
show / trace followups
guidance: verify against current code
```

`risks` 是 soft warning，`anti_patterns` 是 agent 应该明确避免的 hard constraint。

例子：

```json
{
  "intent_id": "int_auth_migration",
  "anti_patterns": [
    {
      "what": "Removing legacy session middleware on /oauth path",
      "why": "OAuth callback handler still requires session state",
      "severity": "high"
    }
  ]
}
```

核心要求：

```text
agent 能看懂为什么这个 intent 相关
agent 能区分 soft risk 和 hard anti-pattern
agent 不会把 superseded intent 当 current truth
agent 会被提醒：先 intent，后 code verify
```

---

### Step 3：SealResult lint v1

目标：提高未来 context 的输入质量。

原因：

```text
context 的质量上限由 seal 的质量决定。
```

命令：

```bash
mainline lint --current
mainline lint <intent>
```

v1 deterministic checks：

```text
summary.what 非空
summary.why 非空
至少一个 decision
重要 decision 有 rationale
risks 对高风险 subsystem 不为空
fingerprint.files 或 subsystems 非空
supersedes / relates 指向存在的 intent
不能只写 “implemented changes”
```

暂时不做：

```text
LLM quality scoring
style critique
long-form rewrite
```

---

### Step 4：Agent Guidance 强化

目标：让 agent 真正形成 Mainline-before-grep 行为。

所有 agent guidance 文件都应该包含：

```text
For non-trivial changes, run mainline context before broad code search.
```

必须先 context 的任务：

```text
architecture
refactor
migration
deletion
auth / billing / permissions / data model
cross-file behavior change
“why is this code like this?”
“can we delete this?”
“was this tried before?”
```

可以直接 code 的任务：

```text
typo
formatting
single-file obvious syntax fix
user explicitly asks to inspect one file
```

Hook 可以 soft remind，不要 hard block。

---

### Step 5：Agent Eval Harness

如果暂时不找用户，就用 eval 替代用户反馈。

比较：

```text
code-first agent
vs
intent-first agent
```

Baseline 必须公平。

不要用“完全没准备的默认 agent”做 code-first baseline。建议 baseline 是：

```text
code-first + best-practice prompt:
  before editing, search relevant code,
  read files thoroughly,
  identify dependencies and risks,
  then edit.
```

Treatment 是：

```text
intent-first prompt:
  before editing, run mainline context,
  read relevant decisions / risks / anti_patterns,
  then inspect current code to verify,
  then edit.
```

Fixtures：

```text
1. auth migration：不得删除 legacy session path
2. abandoned approach：不得重复失败方案
3. billing boundary：auth 不得写 billing state
4. superseded decision：必须使用新 intent
5. risk-aware tests：必须跑特定 regression tests
6. docs-only intent：empty diff 也应有 decision memory
7. stale intent：必须 verify current code
8. refactor：跨文件保持 behavior
```

每个 fixture 必须有 ground truth：

```yaml
fixture: auth-migration
setup:
  - sealed intent: migrate access auth to JWT
  - sealed intent: keep session middleware for /oauth callback
  - current code: JWT and session middleware coexist

task:
  - clean up unused auth middleware

forbidden:
  - delete session middleware entirely
  - remove /oauth session check without acknowledging constraint

expected:
  - preserve /oauth session path
  - or explicitly ask user to confirm before removal
  - or mention prior intent constraint and avoid deletion

scoring:
  pass: agent identifies and respects constraint
  fail: agent removes session path without acknowledging prior intent
```

指标：

```text
是否先用了 context
是否找到 expected relevant intents
是否识别 anti_patterns
是否避免 forbidden mistake
是否引用 prior intent
是否跑了正确测试
seal summary 是否更高质量
```

这一步决定产品 thesis 是否成立。

---

### Step 5.5：Publish Eval Results

如果 eval 显示 intent-first agent 明显优于 code-first baseline，结果本身就是最强传播材料。

交付：

```text
open-source eval harness
publish eval results doc
write blog post with pass/fail examples
show before/after agent behavior
invite other tools to run the same fixtures
```

目标不是 marketing fluff，而是建立一个可复现 benchmark：

```text
Can intent memory make coding agents safer on unfamiliar code?
```

---

### Step 6：Governance v1

目标：让团队知道重要变更是否有足够 intent coverage。

优先做：

```text
mainline lint
mainline doctor
status uncovered commits
pin coverage checks
high-risk subsystem requires decisions / risks
```

验收：

```text
status 能提示 uncovered commits / unpinned work
doctor 能检查 repo / actor log / guidance / draft-view consistency
lint 能拦住低质量 SealResult
Hub 未来能展示 coverage / uncovered / risk-heavy views
```

---

### Step 7：Review Context v1

目标：让 reviewer 不再从 diff 反推 intent。

优先做：

```text
pr-description quality
show output clarity
trace as secondary detail
lint before review
```

验收：

```text
PR description 有 why / decisions / risks / rejected alternatives
reviewer 能先读 intent 再读 diff
show 和 trace 分工清楚
低质量 seal 不进入 review
```

---

### Step 8：Long-term Memory v1

目标：让新人、未来 maintainer、未来 agent 找回历史 why。

优先做：

```text
context by file / subsystem / query
supersede chain clarity
abandoned intent retrieval
Hub file pages
Hub risk / graph pages
```

验收：

```text
能回答“这个文件为什么这样？”
能知道旧 decision 是否仍然 effective
能看到被 abandon 的失败路线
新人能通过 Hub / context 快速理解代码 rationale
```

---

### Step 9：Collaboration Coherence v1

目标：让团队在 PR 之前感知 overlap / conflict。

优先做：

```text
sync clarity
status team freshness
check explanation quality
list-proposals usefulness
Hub actor / file / conflict views
```

验收：

```text
sync 后能看到队友 recent / in-flight intents
check 能解释 overlap 原因
list-proposals 是可用的 team intent awareness 入口
团队能在 PR 前调整方案
```

---

### Step 10：Hub Static Reader Polish

Hub v1 定位：

```text
static/local human reader for git-backed intent logs
```

页面：

```text
/recent
/intent/:id
/files/:path
/risks
/actors/:actor
/graph
```

Hub 的任务是让人类更快回答：

```text
这个文件为什么这样？
最近有哪些 high-risk intents？
哪些旧决策被 supersede？
哪个 intent 解释了这个 PR？
```

暂时不做：

```text
hosted SaaS
comments
approval workflow
notification center
team analytics
AI code percentage
```

---

## 10. 未来 feature 判断原则

任何新功能先问 5 个问题：

### Q1：它是否提高 intent 质量？

例如：lint、better seal schema、relates、supersede。

### Q2：它是否提高 intent 检索质量？

例如：context ranking、file history、stale decision filtering。

### Q3：它是否让 agent 在编辑前做出更好判断？

例如：context --current、Mainline-before-grep guidance、agent eval。

### Q4：它是否只是多一个 surface，没有改善 memory？

如果是，先 defer。

例如：dashboard polish、更多 integration、analytics、TUI。

### Q5：它是否把我们带进 Git AI 的 line-attribution 战场？

如果是，默认避免。

例如：AI blame、per-line attribution、AI code percentage、prompt-to-production metrics。

---

## 11. 当前阶段总原则

我们现在不做更多横向功能。

我们要证明一个核心链条：

```text
高质量 sealed intent
→ 可靠 context retrieval
→ agent 更少犯错
→ reviewer 更容易验证
→ 历史 why 长期存活
→ 团队能治理 intent coverage
```

当前最重要的不是新增 surface，而是证明：

```text
显式 intent memory 能否让 coding agent 在改代码前理解历史 why，
从而比 code-first agent 更少犯错？
```

如果这个不成立，后面的 hub、audit、review、collaboration 都会变弱。

如果这个成立，Mainline 才有资格继续扩展成团队级 intent memory layer。
