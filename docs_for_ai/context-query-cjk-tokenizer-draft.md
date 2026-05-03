# context --query 中文分词方案草案

状态：draft

目标：在 `int_9b30f19a` 已合入的 CJK fallback 基础上，补上真正的中文 query tokenization，改善无空格中文长句的 recall，同时不改变 conflict scoring 的共享 tokenizer。

## 当前结论

推荐方案：**在 `context --query` 专属 tokenizer 中引入 `github.com/go-ego/gse`，使用 embedded 简体词典 + 少量 Mainline 领域词典；SQLite 继续做候选过滤，不在本阶段切 FTS。**

理由：

- `gse` 是纯 Go；`CGO_ENABLED=0 go test` 可过，符合 CLI 跨平台分发预期。
- `gojieba` 分词效果也可以，但依赖 cgo/C++；`CGO_ENABLED=0` 下不可用，不适合作为默认 CLI 依赖。
- SQLite 当前已有 `modernc.org/sqlite`，FTS5/trigram 可用，但它解决的是索引/substring 检索，不是中文词典分词；内置 `unicode61` 对中文长串基本按整段处理，`trigram` 又不覆盖二字词。
- 当前 corpus 规模是几百到几千 intents，瓶颈不是索引性能；先把 query terms 做对，比先换 SQLite FTS 更直接。

## 当前行为与问题

`int_9b30f19a` 已解决两件事：

1. 含 CJK 的 query term 不再被标成 `unsupported_non_ascii`。
2. query mode 不再允许 recency-only 假阳性进入结果。

但它不是中文分词。当前逻辑仍主要按 whitespace/punctuation 切：

- `不要重新引入 继承` -> `不要重新引入`、`继承`
- `不要重新引入继承约束` -> 仍可能是一个长 CJK chunk

这会导致用户自然输入无空格中文长句时，候选召回和 scoring 都不稳定。

## 本轮验证摘要

### gse

临时 probe：`github.com/go-ego/gse@v1.0.2`

样例输出：

```text
不要重新引入继承约束 -> 不要 / 重新 / 引入 / 继承 / 约束
继承约束确认 -> 继承 / 约束 / 确认
中文分词的问题要选什么方案 -> 中文 / 分词 / 的 / 问题 / 要选 / 什么 / 方案
acknowledged_constraints确认 -> acknowledged / _ / constraints / 确认
```

验证：

```bash
CGO_ENABLED=0 go test ./...
# gse probe: pass
```

模块观察：

- 最新可见版本：`v1.0.2`
- `go.mod` 依赖：`github.com/vcaesar/cedar`、`github.com/vcaesar/tt`
- 没有 `import "C"` / `#cgo`
- 支持 `LoadDictEmbed("zh_s")` / `NewEmbed(...)`，可以避免运行时依赖 module cache 路径。

### gojieba

临时 probe：`github.com/yanyiwu/gojieba@v1.4.7`

样例输出与 gse 类似，但：

```bash
CGO_ENABLED=0 go test ./...
# fail: undefined: gojieba.NewJieba
```

模块中存在 `#cgo` 与 `import "C"`，不适合作为 Mainline CLI 默认依赖。

### SQLite FTS5 / trigram

本地 `modernc.org/sqlite` 观察：

```text
sqlite_version 3.46.0
ENABLE_FTS5
fts5_trigram <nil>
```

probe 结果：

```text
unicode61 MATCH 中文 -> 0
unicode61 MATCH 为什么不支持中文 -> 1
trigram MATCH 中文 -> 0
trigram MATCH 支持中文 -> 1
trigram LIKE %中文% -> 1
```

解释：

- `unicode61` 不是中文分词器；中文长串倾向按整 token 匹配。
- `trigram` 能改善三字以上 substring，但二字词如 `中文` 不命中。
- SQLite FTS 适合后续优化候选索引，不应替代 query tokenizer。

## 方案对比

| 方案 | 优点 | 问题 | 结论 |
| --- | --- | --- | --- |
| 保持现有 CJK fallback | 零依赖、已合入 | 不是真分词，无空格长句 recall 弱 | 不够 |
| CJK bigram/trigram 自研 | 零依赖、可控 | 噪音高，词义弱，ranking 需要额外压权 | 可作为 fallback |
| SQLite FTS5 unicode61 | 已有 SQLite | 不支持中文词典分词 | 不选 |
| SQLite FTS5 trigram | 无 Go 分词依赖，可索引 substring | 二字词缺口，不懂词，schema 变更大 | 后续索引优化可评估 |
| gojieba | jieba 生态熟悉，效果好 | cgo/C++，交叉编译和 release 复杂 | 不作为默认 |
| gse | 纯 Go、可 embed dict、效果够用 | 增加词典体积和初始化成本 | 推荐 |

## 推荐设计

### 1. 加 query-only tokenizer adapter

新增一层内部 adapter，避免把实现细节散在 `context_query_terms.go`：

```go
type QueryTokenizer interface {
    Terms(text string) []string
}
```

默认实现：

- ASCII 仍沿用当前 `asciiQueryFields`、短 token allowlist、alias expansion。
- CJK 路径调用 gse search-mode tokenizer。
- 保留原始 CJK phrase/chunk 作为一个低成本 exact substring term。
- 去掉纯空白、标点、单字 stopword；二字以上中文词保留。
- 明确只在 `context --query` 使用，不触碰 `keywordsFromText` 和 conflict scoring。

### 2. 使用 gse embedded dictionary

初始化建议：

- 使用 `gse.NewEmbed("zh_s")` 或 `LoadDictEmbed("zh_s")`。
- 避免 `LoadDict()`，因为它从模块源码路径加载文件，编译后的发布二进制不应依赖 module cache。
- 懒加载 + `sync.Once`，失败时回退到现有 CJK fallback。

### 3. 加 Mainline 领域词典

用 gse 支持的 embedded/custom dict 追加高权重领域词，第一批只放少量确定词：

```text
继承约束
重新引入
反模式
反模式传播
确认机制
中文分词
上下文检索
召回质量
```

英文/代码词继续走 ASCII token path，不强行交给中文分词器。

### 4. SQLite 暂不切 FTS

当前 `ReadIntentViewsByQuery(keyword)` 的 `LIKE` 候选过滤可继续使用。分词后会产生更多 terms，候选 union 逻辑已有基础。

本阶段只需要注意：

- 不让过多 CJK tokens 放大候选集；建议限制 query terms 上限，例如 16 或 24。
- 对 gse 产出的单字词默认丢弃，除非进入极少量 allowlist。
- 保留 recency-only guard，防止 query 无内容命中时返回最近 intent。

FTS/trigram 作为后续阶段：当 intent 数量上万、LIKE 明显慢时，再基于相同 tokenizer 产物做索引设计。

## 验收用例

新增/更新测试建议：

1. 无空格中文长句：
   - query：`不要重新引入继承约束`
   - 期望：effective keywords 至少包含 `重新` / `引入` / `继承` / `约束`，并命中 inherited constraints 相关 intent。

2. 领域词：
   - query：`继承约束确认机制`
   - 期望：能命中 explicit ack / inherited constraints 相关 intent。

3. 中英混合：
   - query：`不要重新引入 subsystem 继承`
   - 期望：同时保留 `subsystem` 和中文分词 tokens。

4. 无意义中文负例：
   - query：`完全不存在的中文主题测试词`
   - 期望：`result_count = 0`，不返回 recency-only。

5. 发布约束：
   - `CGO_ENABLED=0 go test ./...` 或至少覆盖新增 tokenizer package。
   - `go test ./internal/engine -run 'TestContextRetrieval_Query' -count=1`。

## 不做的事

- 不修改 `keywordsFromText`。
- 不把 CJK 分词用于 conflict detection。
- 不在这一 PR 切换 SQLite schema 或 FTS5。
- 不引入 cgo 依赖。
- 不把“分词成功”当成“排序一定正确”；仍用 golden/eval 验证 top-N。

## 下一步实现切片

建议开一个小 PR：

1. 新增 `internal/engine/context_query_tokenizer.go`。
2. 引入 `github.com/go-ego/gse@v1.0.2`。
3. 在 `queryTermsFromText()` 中用 gse 结果替代当前 `cjkQueryTerms()` 的 whitespace fallback。
4. 添加 focused tests 和 before/after smoke。
5. 仅提交 tokenizer + tests，不动 SQLite schema。
