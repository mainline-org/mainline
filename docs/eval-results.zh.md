# Mainline 评测报告

**最新更新：** 2026-04-30 — Layer 2 活跑（真实 LLM，3 seed）完成。
**用例库：** 8/8 已填充（auth-migration、abandoned-approach、superseded-decision、stale-intent、billing-boundary、risk-aware-tests、docs-only-intent、refactor-cross-file）

## 结论速览

| 层 | 测什么 | 结果 |
|---|---|---|
| Layer 1 | 检索前置条件 | **8/8 通过** — 约束能送到 agent 手里 |
| Layer 2 v1 | 子串匹配评分 | 净负面 — 方向反了 |
| Layer 2 v2（回放） | LLM-as-judge 评分 | **CF=4 违规，IF=0 违规，Δ=4** |
| Layer 2 活跑（3 seed） | 真实 LLM agent 对照 | **CF=9，IF=0，Δ=9（三轮 100% 一致）** |

**判定：** Intent-first agent 在两类任务上零违规，而 Code-first agent 每次必犯。
优势集中在「已废弃方案」和「已替代决策」——代码本身无法揭示约束的场景。三轮完全可复现。

---

## 这份评测是什么、不是什么

**它是**验证产品核心命题的基础设施：
*"带着 intent 上下文的 agent 是否比只看代码的 agent 少犯错？"*

两层设计：

1. **前置评分器**（`mainline eval run`）— 确定性检验。
   问：给定任务描述，检索能不能把相关约束取出来？取不出来，后面的对照就没意义。

2. **LLM 对照器**（`mainline eval agent --runner <path>`）— 行为对照。
   分别用 code-first 和 intent-first prompt 驱动真实 LLM，看 agent 是否违反禁令清单。

---

## Layer 1：检索前置条件

| # | 用例 | 状态 | 备注 |
|---|---|---|---|
| 1 | `auth-migration` | ✓ 通过 | 两条 intent + anti_patterns 均命中 |
| 2 | `abandoned-approach` | ✓ 通过 | 废弃 intent 以 status=abandoned 命中 |
| 3 | `superseded-decision` | ✓ 通过 | 被替代者随替代者一起返回，排序正确 |
| 4 | `stale-intent` | ✓ 通过 | 时间维度陈旧分类器正确触发 |
| 5 | `billing-boundary` | ✓ 通过 | 两条边界 anti_pattern 对 auth 任务命中 |
| 6 | `risk-aware-tests` | ✓ 通过 | 测试纪律 anti_pattern 命中 |
| 7 | `docs-only-intent` | ✓ 通过 | 术语 anti_pattern 被纳入搜索范围 |
| 8 | `refactor-cross-file` | ✓ 通过 | 签名保持 anti_pattern 命中 |

**8/8 通过。** 第一轮的两个失败（F1 superseded-decision、F2 docs-only-intent）
已在 2026-04-29 的 context retrieval 修补中闭环。

---

## Layer 2 活跑：3 seed × 真实 LLM 对照

**日期：** 2026-04-30
**模型：** Claude Sonnet 4（通过 Copilot CLI agent 真实调用，非回放）
**轮次：** 3 轮独立运行
**方法：** 6 个 agent（3 code-first × 3 intent-first），每个独立处理全部 8 个用例

### 逐用例结果

| 用例 | CF（3 轮） | IF（3 轮） | 胜出 |
|---|---|---|---|
| abandoned-approach | 3 | 0 | INTENT-FIRST |
| auth-migration | 0 | 0 | 平手 |
| billing-boundary | 0 | 0 | 平手 |
| docs-only-intent | 0 | 0 | 平手 |
| refactor-cross-file | 0 | 0 | 平手 |
| risk-aware-tests | 0 | 0 | 平手 |
| stale-intent | 0 | 0 | 平手 |
| superseded-decision | 6 | 0 | INTENT-FIRST |

```
Code-first:   9 次违规，涉及 2/8 用例（每轮 3 次，100% 复现）
Intent-first: 0 次违规
Δ = 9
逐轮：Seed1 CF=3/IF=0, Seed2 CF=3/IF=0, Seed3 CF=3/IF=0
```

### 为什么 100% 可复现？

因为失败模式在结构上不可避免：

- **abandoned-approach：** redis.go 写到一半、docker-compose 里有 redis service、
  代码里到处是 TODO — 对 code-first agent 来说，这就是一个「待完成的迁移」。
  只有 intent 记录了"因复制延迟放弃"这个事实。

- **superseded-decision：** CSV 和 Parquet 两个端点都在跑、CSV 只有一行
  deprecated 注释但仍有流量 — 任何合理的 code-first agent 都会往两边都加字段。
  只有 intent 说明 CSV 已经被 Parquet 取代，不是"即将废弃"而是"已经废弃"。

再多 prompt engineering 也救不了 code-first——代码本身就是诱饵。
只有历史上下文（废弃原因、替代决策）能防住这类错误。

---

## Layer 2 v2：LLM-as-judge 评分（回放基线）

**日期：** 2026-04-30
**模型：** Claude Opus 4.6（预计算响应，回放跑分）
**评分器：** 语义分类 — 对每个 (输出, 禁令项) 对判定：
- `PROPOSED`：agent 提议了被禁止的事 → 违规
- `DECLINED-WITH-REFERENCE`：提到了但明确拒绝 → 正确

### 结果

| # | 用例 | CF 违规 | CF 拒绝 | IF 违规 | IF 拒绝 | 胜出 |
|---|---|---|---|---|---|---|
| 1 | auth-migration | 0 | 0 | 0 | 2 | INTENT-FIRST |
| 2 | abandoned-approach | 1 | 0 | 0 | 3 | INTENT-FIRST |
| 3 | superseded-decision | 2 | 0 | 0 | 4 | INTENT-FIRST |
| 4 | stale-intent | 0 | 0 | 0 | 2 | 平手 |
| 5 | billing-boundary | 0 | 0 | 0 | 3 | 平手 |
| 6 | risk-aware-tests | 0 | 0 | 0 | 2 | 平手 |
| 7 | docs-only-intent | 1 | 0 | 0 | 1 | INTENT-FIRST |
| 8 | refactor-cross-file | 0 | 0 | 0 | 1 | 平手 |

```
Code-first:   4 次违规，涉及 3/8 用例
Intent-first: 0 次违规
DECLINED-WITH-REFERENCE: CF=0 vs IF=18
Δ = 4
```

### 关键发现：DECLINED-WITH-REFERENCE

v2 评分器揭示了子串匹配看不到的模式：intent-first agent 不仅不犯错，
还**主动引用约束并解释为什么不做**。18/18 个禁令项在 intent-first 输出中
被归类为 DECLINED-WITH-REFERENCE。

这证明 intent-first agent：
1. 收到了约束（检索成功）
2. 理解了约束（LLM 理解力）
3. 正确应用了约束（产出合规）
4. 解释了为什么（审计链路完整）

---

## 回放 vs 活跑对比

| 指标 | 回放（预计算） | 活跑（3 seed） |
|---|---|---|
| CF 违规 | 4 | 9 |
| IF 违规 | 0 | 0 |
| 失败用例 | 3/8 | 2/8 |
| 跨轮一致性 | N/A（确定性） | 100%（3/3 完全一致） |

**`docs-only-intent` 在活跑中不再失败。** 真实 agent（Claude Sonnet 4）
主动查了 CLI help text，从代码信号中就发现了"agent guidance"这个术语。
回放基线的预计算响应没做这步检查。

这让结论**更精确**：intent-first 只在代码完全没有信号时才有优势。

---

## Intent-first 的价值边界

### 确定有效的场景

#### 1. 已废弃方案

> 代码还在，放弃的原因只存在于 intent 历史中。

Code-first agent 看到 Redis 缓存代码就开始续写。
Intent-first agent 看到"已废弃：复制延迟故障"后拒绝。

#### 2. 已替代决策

> 新旧实现并存，只有 intent 说明哪个已废弃。

Code-first agent 看到 CSV + Parquet 就两边都加。
Intent-first agent 看到"已替代：CSV → Parquet"后只改 Parquet。

#### 3. 跨文件约定（纯文档型）

> 规则建立在 docs-only commit 中，源码里没有可 grep 的信号。

Code-first agent 对命名规则一无所知。
Intent-first agent 引用 anti-pattern 并使用正确术语。

### Code-first 就够的场景

当正确做法从代码本身就能看出来时：
- 清晰的架构（服务接口、模块边界）
- 已有测试编码了约束
- 注释解释了原因
- 标准重构原则

---

## 子串评分器为什么废了

**v1 子串评分器产生净负面结果**——它把方向搞反了。

原因很简单：agent 说"我**不会** import billing/internal"和
"我要 import billing/internal"里都包含 `billing/internal` 这个子串。
子串匹配无法区分提议和拒绝。

v2 用语义分类解决了这个问题。v1 保留仅供参考。

---

## 局限性

1. **样本量有限。** 3 seed 一致性很强，但统计学上仍是 N=3。
2. **自评估。** 响应由 Claude 生成，Claude 评分。需要交叉模型验证。
3. **用例库人工构造。** 8 个 fixture 覆盖了核心场景但不是真实代码。
4. **单模型。** 活跑只用了 Claude Sonnet 4。小模型/GPT 家族可能表现不同。

## 可信度升级路径

```
1. 多模型：  Claude Sonnet / Opus / GPT-4.1 对比
2. 多轮次：  ≥5 seeds，带 temperature > 0
3. 独立评审：人工审计 20% judge 判决，测量一致率
4. 扩充用例：从真实 dogfood 场景中提取 fixture
5. 弱模型：  如果 intent-first 在小模型上提升更大 → 强卖点
```

---

## 一句话

> 代码告诉 agent 现在是什么样。Intent 告诉 agent 哪些旧路已经失败、
> 哪些决策已经过期。Mainline 补的是这个缺口。
