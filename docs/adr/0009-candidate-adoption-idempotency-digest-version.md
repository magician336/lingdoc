# 9. 候选采纳的幂等摘要版本化：旧键显式失效，不回落成冲突

**状态**：已采纳（2026-10-21，由 Spec [#134](https://github.com/magician336/lingdoc/issues/134) 的施工面 [Issue #326](https://github.com/magician336/lingdoc/issues/326) 的开发计划产生）

**修订关系**：本决策**不修订** [ADR 0008](0008-writing-action-vocabulary-and-adoption-model.md) 的采纳模型，只决定该模型引入新字段后**幂等摘要如何演进**。本决策**不修订**仓库既有的幂等语义：同键同请求仍重放、同键异请求仍是冲突。

## 背景

候选采纳的幂等摘要是**显式白名单**，不是对整个请求体做哈希：

```go
// internal/lingdoc/candidateadoption/candidate_adoption.go:405-416
// 摘要覆盖：ProjectID, ChapterID, CandidateID,
//           ExpectedChapterVersionID, ExpectedSpecRevision, ReplaceExisting
```

`IdempotencyKey` 与 `ActorID` **不在摘要内**——它们是查询键，不是内容。幂等行落在 `lingdoc_candidate_adoptions`（`candidate_adoption_store.go:111-120`），唯一键 `(project_id, chapter_id, actor_id, idempotency_key)`。摘要比对有**两处**：回放路径 `:397` 与写事务内 `:430`，现状两处都直接返回 `ErrIdempotencyConflict`。

**另有一处与本决策相关的既有事实**：`operationcore/ownership_test.go:80-108` 声称「摘要被就地计算（应经过 operationcore.RequestHash / ValueHash）」，但它的 AST 检查只拦**内联式** `sha256.Sum256(json.Marshal(x))`——要求 `json.Marshal` 是 `Sum256` 的直接实参。现有 `candidateadoption.requestHash` 用的是**两步式**（先把 `json.Marshal` 的结果赋给变量，再 `sha256.Sum256(该变量)`），**逃过了检查**。也就是说仓里实际存在**第二个摘要规则拥有者**，而那正是那个测试声称要防止的东西。本决策的「第一步」消除它，「第五步」防止再出现一个。

[ADR 0008](0008-writing-action-vocabulary-and-adoption-model.md) 要求采纳请求新增**确认载荷**（候选动作、采纳目标、目标范围、确认时刻、候选来源）与**部分采纳参数**。这些字段必须进摘要，否则同一个幂等键下「采纳整段」与「采纳片段」会被当成同一个请求。

但白名单一改，历史行全部失配。

**失配后会发生什么**：现有代码路径是「摘要不同 → `ErrIdempotencyConflict`（409 idempotency_conflict）」。于是昨天成功过的一个采纳键，今天重试返回「幂等键冲突」。

**这个错误对上层没有决策价值**。两种情况需要两种不同的恢复动作：

| 情况 | 上层该做什么 |
| --- | --- |
| 摘要不同 = 同一键下发了**不同的请求** | 停下来查：是不是重试时改了内容？是不是两个客户端抢同一个键？ |
| 摘要版本不同 = 这个键属于**旧版摘要** | 用**新幂等键**重试。请求本身没问题，只是键过期了。 |

把它们合并成一个错误，等于让用户在一个「键过期」的场景里去排查「是不是发了不同的请求」——错误信息把上层引向错误的排查方向。这是 Ousterhout 意义上的「异常处理器是接口的一部分，每增加一个可见异常就增加所有调用者的认知和测试成本」。

## 决策

### 一、先换实现，再扩白名单——顺序固定

现有 `candidateadoption.requestHash`（`candidate_adoption.go:405-416`）用**两步式**计算摘要：

```go
b, _ := json.Marshal(struct{ /* 6 个字段 */ }{...})
sum := sha256.Sum256(b)
return hex.EncodeToString(sum[:])
```

**第一步：把实现换成 `operationcore.RequestHash`，不扩白名单。**
`operationcore.digestJSON`（`operationcore/operationcore.go:117-128`）就是 `hex(sha256(json.Marshal(v)))`，与现状**算法等价**。所以这一步**不改变任何历史摘要值**，历史幂等行不会因此失配。

这一步顺带消除一个**第二个摘要拥有者**：`operationcore/ownership_test.go:80-108` 的 AST 检查只拦**内联式** `sha256.Sum256(json.Marshal(x))`（要求 `json.Marshal` 是 `Sum256` 的直接实参），而现有代码是两步式，**逃过了检查**——仓里实际存在第二个摘要规则拥有者，正是那个测试声称要防止的东西。

**第二步：扩白名单并加版本前缀。** 此时才有失配，且失配由版本化接住。

**摘要结构体必须是专用结构**（现状如此），**不能**直接哈希 `AcceptCandidateInput`——后者含 `IdempotencyKey` 与 `ActorID`（它们是查询键，不在摘要内），进了摘要会让**全部**历史行失配，而不只是带新字段的请求。

### 二、摘要带版本前缀

`requestHash` 的摘要结构体**第一个字段**是 `DigestVersion int`（当前值 `2`）。Go 的 `json.Marshal` 对 struct 按字段声明顺序输出，因此版本前缀的位置稳定。

幂等行新增 `digest_version` 列。回放时先比版本：

| 版本 | 摘要 | 结论 |
| --- | --- | --- |
| 相同 | 相同 | 正常重放（`replayed=true`） |
| 相同 | 不同 | `ErrIdempotencyConflict`（真冲突，语义不变） |
| **不同** | — | **新错误 `idempotency_key_expired`（409）**，语义是「这个键属于旧版摘要，请用新键重试」 |

新错误形态进 `internal/clienterror/catalog.go`，与既有 `ErrIdempotencyConflict` 并列。

### 三、两处比对点都必须版本感知

回放路径（`candidate_adoption_store.go:397`，`row.RequestHash != requestHash(in)`）与写事务内（`:430`，`existing.RequestHash != hash`）**都要**先比版本。现状两处都直接返回 `ErrIdempotencyConflict`。

**只改一处就是后门泄漏**：旧行在写事务内被当成「摘要不同」而返回冲突，在回放路径却被当成可重放——同一个键在两个路径上得到两种结论。

### 四、旧键显式失效，不静默兼容

版本不同的键**不复用**。调用方必须用新键重试。

不采用「保留两条白名单，旧行走旧白名单」的方案：旧白名单不知道「部分采纳」字段，会**放行语义不同的重放**——同一个键下先整段采纳、后部分采纳，都会被旧白名单判为「同一请求」而静默重放。这是比冲突更危险的结果。

### 五、补上守护测试的洞

`operationcore/ownership_test.go` 的 AST 检查要扩到**两步式**摘要（先 `json.Marshal` 赋给变量、再 `sha256.Sum256(该变量)`），否则下一个两步式摘要会继续逃逸，仓里会再次出现第二个摘要拥有者。这条与上面的「第一步」是同一件事的两面：**换实现**消除现有的第二个拥有者，**补检查**防止再出现一个。

## 考虑过的替代方案

| 备选 | 为什么否掉 |
| --- | --- |
| **新字段不进摘要** | 同键下「采纳整段」与「采纳片段」无法区分，复用判据失效，重试可能重复写入或采纳错内容。 |
| **直接接受历史键失配，回放返回 `idempotency_conflict`** | 错误对上层不传达真实含义；用户重试旧键得到的是「冲突」而不是「键已过期」，排查方向被引偏。 |
| **保留两条白名单（旧行旧、新行新）** | 要同时维护两套白名单；旧白名单漏检新字段会放行语义不同的重放。 |
| **不改摘要，改用新幂等键前缀约定** | 把判断责任推给调用方，无法机器保证；同一请求可能因前缀写法不同而重复写入。 |

## 影响

- `lingdoc_candidate_adoptions` 新增 `digest_version` 列，与 [ADR 0008](0008-writing-action-vocabulary-and-adoption-model.md) 的候选表变更**合并为同一个迁移版本**（`migrations/versioned/000125_*` 与 `migrations/sqlite/000046_*`，各含 up/down）。
- `internal/clienterror/catalog.go` 新增 `idempotency_key_expired` 形态。
- 采纳请求的幂等语义从二态变为三态：重放 / 真冲突 / 键过期。前端 `frontend/src/views/lingdoc/candidateAdoption.ts` 的 `keyForAttempt` 复用判据（以请求体签名为键）**不变**——它本来就在签名变化时铸新键；变的是服务端对**历史键**的回放结论。
- 新增回归门槛：迁移 up/down 各跑一遍 SQLite 与 PostgreSQL；旧幂等行以 `digest_version = 1` 读回并触发 `idempotency_key_expired`；同版本同键重放仍返回 `replayed=true`；同版本异键异内容仍返回 `idempotency_conflict`。

## 风险与限制

- **「键过期」是一个新的用户可见状态**。前端需要能区分它与真冲突：前者提示「请重试」，后者提示「这次重试的内容与之前不同」。若前端把两者都显示成「冲突」，本决策的收益就丢了一半——这是 ADR 0008 的前端交付物的责任。
- **摘要版本号是一次性承诺**。将来若采纳请求再次扩字段，版本号要再升一次，并同样保留上一版的失效路径。**每次升级都必须在同一个迁移里写清旧版键的行为**——否则又回到「静默失配」。
- **本决策的实现尚未落地**，相关回归门槛在 [Issue #326](https://github.com/magician336/lingdoc/issues/326) 的开发计划 §8 中登记为 TODO。
