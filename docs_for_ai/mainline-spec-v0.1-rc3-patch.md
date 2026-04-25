# Mainline · v0.1-rc3 最终修正 Patch

> 状态：架构层最后一次修正
> 应用对象：v0.1-rc2
> 修正核心：用 git notes 取代 commit trailer 作为机器可读元数据的载体

---

## 修正动机

回看 v0.1-rc2 中 trailer 在真实 `git log` 中的呈现方式，我们发现：

1. `git log --oneline` 完全不显示 trailer
2. `git log` 完整模式显示，但 hex intent ID 对人无意义
3. PR description markdown 才是对人有帮助的部分（决策、风险、follow-up）
4. Trailer 真正的价值是**机器可读**——但 notes 同样机器可读
5. Trailer 依赖 GitHub squash 配置正确才能落入 commit message——脆弱

**结论：** Trailer 嵌在 commit message 里是个折中，既污染 commit history 又不可靠。把它彻底移到 git notes 更干净。

---

## 新架构：双轨元数据

```
┌─────────────────────────────────────────────────────────────┐
│  机器可读元数据   →  refs/notes/mainline/intents            │
│                      (Mainline 工具读写，独立 ref)          │
│                                                             │
│  人类可读决策     →  PR description / commit message body   │
│                      (mainline pr-description 生成的 md)    │
│                                                             │
│  Commit message  →  保持干净，无 Mainline-* 字段            │
└─────────────────────────────────────────────────────────────┘
```

两条轨道独立：

- Notes 轨道是 source of truth，mainline view 完全基于 notes 重建
- PR description 轨道是 human aid，对 mainline view 不影响
- Commit message 不再含任何 mainline 元数据

---

## 改动列表

### Patch 1：删除所有 trailer 相关概念

**spec 中删除的内容：**

- `Mainline-Intent` trailer
- `Mainline-Seal` trailer
- `Mainline-Reverts` trailer
- `Mainline-Supersedes` trailer
- 整个 §13 Trailer 规范（v0.1-rc2 的）
- PR template 中的 trailer 段落
- `mainline pr-trailer` 命令
- 所有"在 commit message 里注入 trailer"的文档

### Patch 2：引入 git notes ref `refs/notes/mainline/intents`

新的 source of truth：

```
refs/notes/mainline/intents
```

**为什么是子 ref `mainline/intents` 而不是默认 `refs/notes/commits`：**

- 隔离命名空间，避免和团队其他 notes 用法冲突
- 未来可加 `refs/notes/mainline/checks` 等
- 显式表明这是 mainline 拥有的 ref

每个 main commit 上的 note 内容是结构化 JSON：

```json
{
  "schema_version": 1,
  "kind": "mainline.commit_note",
  "intents": [
    {
      "intent_id": "int_b91e2f3a",
      "seal_result_hash": "sha256:9f82a3..."
    }
  ],
  "reverts": [],
  "added_at": "2026-04-25T14:32:00Z",
  "added_by": "act_01HW8ZK7Y6Y9E4W2N8M3"
}
```

这是结构化数据，不是自由文本。Mainline 工具读 note 内容做 JSON 解析。

人类用 `git log --show-notes=mainline/intents` 看 note 时会看到这段 JSON——不漂亮但可读。如果团队希望 git log 显示更友好的格式，配置 `git log --pretty` 自定义。

---

## 新的 `mainline init`

```bash
mainline init
```

行为更新：

```
1-7. (同 v0.1-rc2)
8. 配置 git notes 同步：
   git config --add remote.origin.fetch '+refs/notes/mainline/*:refs/notes/mainline/*'
   git config --add remote.origin.push 'refs/notes/mainline/*:refs/notes/mainline/*'
9. 配置 git log 默认显示 mainline notes（可选）：
   git config notes.displayRef 'refs/notes/mainline/*'
10. 提示用户在 GitHub branch protection 中保护 refs/notes/mainline/*
```

`AGENTS.md` 不变（agent 不直接和 notes 打交道，由 mainline merge / sync 处理）。

PR template（`.github/PULL_REQUEST_TEMPLATE.md`）改成纯人类 markdown，不含 trailer：

```markdown
## Summary

<!-- Describe what this PR does -->



## Mainline Intent

<!--
This section is auto-filled by `mainline pr-description`.
It is for human reviewers; Mainline does not parse it.
-->



## Tested

<!-- How was this tested? -->
```

---

## 新的 `mainline merge`

完全不再注入 trailer。流程变为：

```
1. mainline sync (强制)
2. 验证 intent 状态 = proposed
3. 跑 mainline check（必须通过）
4. 验证 intent 属于当前 thread
5. 执行 git merge --no-ff（默认禁止 ff，同 v0.1-rc2）
6. 创建 anchoring commit
   commit message 是用户提供或自动生成的描述，无 Mainline-* 字段
7. 给该 anchoring commit 写 note:
   git notes --ref=mainline/intents add -m '<json>' <commit-sha>
8. Atomic push (如果支持):
   git push --atomic origin main refs/heads/_mainline/actor/<id> refs/notes/mainline/intents
9. 不支持 atomic 时按顺序:
   先 push main
   再 push actor log
   再 push notes
10. 任一失败 → mainline repair
```

`--ff` flag 的处理：

```bash
mainline merge --ff
```

允许 fast-forward 但要求 feature branch tip commit **已经有对应的 note**。如果没有，先给 tip 写 note，再 ff。

```
1. 验证 feature branch tip 上是否已有 note
2. 没有 → 先 git notes add 到 feature tip
3. git push origin <branch>:main (fast-forward)
4. push notes
```

这给了一个清晰的语义：**note 是 merged 的标志，不管是 anchoring commit 还是 ff 后的 tip**。

---

## 新的 `mainline sync`

视图重建从"解析 trailer"改成"读 notes"：

```
fn sync() {
    // 1. fetch main
    git_fetch("origin", "main");
    
    // 2. fetch actor logs
    git_fetch("origin", "refs/heads/_mainline/actor/*:refs/remotes/origin/_mainline/actor/*");
    
    // 3. fetch notes
    git_fetch("origin", "refs/notes/mainline/*:refs/notes/mainline/*");
    
    // 4. 处理 force push（同 v0.1-rc2 §10）
    
    // 5. 重建 mainline view
    let last_sync = read(".ml-cache/views/last-sync.json");
    let new_main_commits = git_log_since(last_sync.main_commit, "origin/main", first_parent: true);
    
    for commit in new_main_commits {
        // 读 note（不再解析 trailer）
        let note = git_notes_show("refs/notes/mainline/intents", commit.sha);
        if let Some(note_content) = note {
            let parsed: CommitNote = parse_json(note_content)?;
            for intent_ref in parsed.intents {
                // 验证 hash 一致性
                let event = lookup_actor_log_event(intent_ref.intent_id)?;
                if event.seal_result_hash != intent_ref.seal_result_hash {
                    warn("hash mismatch on commit {} intent {}", commit, intent_ref.intent_id);
                }
                update_mainline_view(intent_ref.intent_id, commit, parsed);
            }
        }
        // commit 没有 note? 不算 merged，sync 不报错
    }
    
    // 6. 解析 actor log events（同 v0.1-rc2）
    
    // 7. 重建索引、写 last-sync
}
```

关键变化：

- 视图重建**完全基于 notes**，不再扫 commit message
- Commit 没有 note = 不算 merged（自然 fallback 到 reconcile）
- Hash 校验仍然存在（note JSON 里的 seal_result_hash vs actor log 里的 seal_result_hash）

---

## 新的 `mainline reconcile`

之前是手动确认 trailer 缺失的 commit；现在是**手动给 commit 写 note**。

```bash
mainline reconcile [--json]
```

行为：

```
1. mainline sync
2. 找出所有 status = proposed 的 intent
3. 对每个 proposed intent，扫描 main first-parent history:
   - 是否有 commit 的 code_commit / code_tree 与 intent 匹配?
   - 候选列表展示给用户
4. 用户确认 → 给该 commit 写 note:
   git notes --ref=mainline/intents add -m '<json>' <commit-sha>
5. push notes
```

视图层不再需要"acknowledged confidence"区分——既然 reconcile 写的也是 note，和 mainline merge 写的 note 形式相同，view 看不出区别。

唯一可选的区别：reconcile 写的 note 多一个字段 `via: "reconcile"`，让审计能追溯哪些 note 是 merge 时写的、哪些是 reconcile 写的：

```json
{
  "schema_version": 1,
  "kind": "mainline.commit_note",
  "intents": [{"intent_id": "int_b91e2f3a", "seal_result_hash": "sha256:..."}],
  "via": "reconcile",
  "reconciled_at": "...",
  "reconciled_by": "act_..."
}
```

在视图里 reconcile 的 intent 仍然标记为 `merged`，但 `view.merged_via = "reconcile"` 让审计可见。

---

## 新的 `mainline pr-description`

输出 markdown，**不含 trailer**：

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
```

JSON 模式：

```json
{
  "intent_id": "int_b91e2f3a",
  "markdown": "..."
}
```

**没有 `trailer_lines` 字段了。**

---

## 删除 `mainline pr-trailer`

整个命令删除。AGENTS.md 不再提它。

---

## 新增数据结构：CommitNote

```typescript
interface CommitNote {
  schema_version: 1;
  kind: "mainline.commit_note";
  
  intents: IntentReference[];     // 一个 commit 可关联多个 intent (squash 多 intent)
  reverts?: string[];             // 被此 commit revert 的 intent ID
  
  added_at: string;
  added_by: string;               // actor_id
  
  via?: "merge" | "reconcile" | "manual";   // 来源
}

interface IntentReference {
  intent_id: string;
  seal_result_hash: string;       // 用于一致性校验
}
```

---

## 视图层简化

`IntentView` 移除 `status_evidence.merged_confidence` 字段——不再有 confirmed vs acknowledged 区分（因为 reconcile 也写 note，和 merge 写的 note 是同形态）。

```typescript
interface IntentView {
  intent_id: string;
  schema_version: 1;
  
  status: IntentStatus;
  status_evidence: {
    sealed_event_id?: string;
    superseded_by_intent?: string;
    abandoned_event_id?: string;
    merged_main_commit?: string;
    merged_via?: "merge" | "reconcile";   // ← 替换之前的 confidence
    reverted_main_commit?: string;
  };
  
  publication: "local_only" | "published";
  
  actor_id: string;
  thread: string;
  git_branch: string;
  goal: string;
  sealed_at?: string;
  code_commit?: string;
  
  summary?: IntentSummary;
  fingerprint?: SemanticFingerprint;
  
  view_rebuilt_at: string;
}
```

---

## Force push 处理（更新）

**Notes ref force push** 的处理同 actor log force push：

```
mainline sync 检测 refs/notes/mainline/intents 历史 rewrite:
  1. 拒绝接受新 notes
  2. 保留旧视图
  3. 提示用户运行 mainline trust-notes-rewrite
```

`mainline init` 提示用户在 GitHub 上保护 notes ref：

```
Setup recommended:
  Protect notes branches in your git host:
  - refs/heads/_mainline/actor/*  (actor logs)
  - refs/notes/mainline/*         (intent metadata)
  
  GitHub: Settings → Branches → Add branch protection rule
  Pattern: _mainline/actor/*
  Pattern: refs/notes/mainline/*
  ☑ Restrict force pushes
```

---

## v0.1 Must ship 列表（更新）

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
mainline context
mainline list-proposals
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
mainline pr-description
mainline reconcile
```

**Thread 管理：**

```
mainline thread new
mainline thread list
mainline thread close
```

**删除：**

```
mainline pr-trailer    ← 不再 ship
```

---

## 里程碑更新

### M7 重写：merge + notes（替代之前的 trailer）

交付：

- `mainline merge` 默认禁止 ff
- Anchoring commit + 写 note（不再注入 trailer）
- `mainline pr-description` markdown 输出
- Atomic push（main + actor log + notes）
- `mainline repair` 处理 push 失败

验收：

- `mainline merge` 对无冲突 proposed intent 工作
- 该 commit 上有 `refs/notes/mainline/intents` 的 note
- 团队 sync 后 mainline view 反映 merged
- commit message 干净，无 Mainline-* 字段

---

## 开发规则（更新到 25 条）

之前 25 条的 #25 改为：

```
25. Mainline 元数据走 git notes (refs/notes/mainline/intents)，
    不嵌入 commit message。
    Commit message 永远干净，不含任何 Mainline-* 字段。
```

新增第 26 条：

```
26. mainline init 必须配置 git notes 的 fetch/push refspec，
    否则 sync/merge 不能正常工作。
```

---

## 心智模型（更新）

```
Human uses agent normally
   ↓
Agent calls Mainline CLI
   ↓
Agent submits SealResult JSON
   ↓
mainline seal --submit:
   - writes IntentSealedEvent to actor log
   - pushes actor log
   ↓
[code goes through PR / mainline merge]
   ↓
On merge:
   - anchoring commit on main (clean message)
   - git note attached (machine-readable metadata)
   - actor log event (already there from seal)
   ↓
mainline sync:
   - fetches main + actor logs + notes
   - rebuilds view from notes (NOT from commit messages)
   ↓
View shows:
   - merged intents (those with notes on main commits)
   - proposed intents (sealed but no note yet)
   - sealed_local intents (not yet pushed)
```

核心：

> **Mainline 的元数据完全在 git notes 里。**
> **Commit message 是 git 的事，Mainline 不碰。**
> **Notes 是 source of truth；reconcile 是补 note 不是补 trailer。**

---

## 这套方案相对 v0.1-rc2 的全面对比

| 维度 | v0.1-rc2（trailer + notes 双写候选） | v0.1-rc3（纯 notes） |
|---|---|---|
| 元数据载体 | commit message trailer | git notes ref |
| Commit message | 含 Mainline-* 字段 | 干净 |
| Squash merge 配置依赖 | 强（trailer 易丢） | 无（notes 独立） |
| 修复 trailer 缺失 | reconcile 交互确认 | reconcile 写 note |
| 没装 mainline 的开发者 | git log 看到无意义 hex ID | 完全无感（除非 fetch notes） |
| Pull request review 体验 | trailer 在 PR description 里 | markdown 在 PR description 里 |
| Mainline 工具读取 | 解析 commit message + notes | 只读 notes |
| 复杂度 | 双写、双读、一致性维护 | 单一来源 |
| GitHub 网页显示 intent ID | 可见但无用 | 不可见（PR description markdown 可见） |
| 力度保护 | trailer 受 main protection 保护 | 需独立保护 notes ref |

**v0.1-rc3 整体更简单、更纯粹、更不依赖 host 配置**。

---

## 唯一保留的小妥协

人类阅读 git log 时如何知道某个 commit 关联哪个 intent？

**答案**：

```bash
git log --show-notes=mainline/intents
```

或全局配置：

```bash
git config notes.displayRef 'refs/notes/mainline/*'
```

之后 `git log` 默认显示 note。Note 内容是 JSON——不漂亮但能读。

或者用 mainline 自己的命令：

```bash
mainline log --mainline
```

这个命令显示**人类格式**的 mainline 视图（merged intent + 关联 commit + intent 标题），不需要解读 JSON。

**对人友好的查看方式留给 mainline 工具**——这和"寄生而非替代"的哲学一致：用户体验由 mainline 提供，git 层保持机械干净。

---

## v0.1-rc3 是否真的最终

是。这个 patch 移除了 spec 中最后一处主要架构选择（trailer vs notes）。

之后的修改应该都是**实现细节**和**文档措辞**，不再是架构层。

下一步实施流程建议：

1. **把 v0.1-rc2 + 本 patch 合并成一份完整的 v0.1-rc3 spec 文档**——可以请我做
2. **写实际的 JSON Schema 文件**——SealResult、CheckJudgmentResult、SealPreparePackage、CheckPreparePackage、CommitNote、所有 Event 类型
3. **逐字打磨 AGENTS.md 模板**——用真实 Claude Code session 测试
4. **准备 conflict-cases 测试集**——50 个真实语义冲突 case
5. **实施 M1–M7**

这是真正可以开工的版本。

---

**文档版本**：v0.1-rc3 patch
**应用对象**：v0.1-rc2 spec
**状态**：架构设计完成
