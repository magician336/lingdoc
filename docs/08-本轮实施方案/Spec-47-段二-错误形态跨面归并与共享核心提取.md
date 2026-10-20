# Spec #47 段二：错误形态跨面归并与共享核心提取 — 开发计划与证据

> 段一（47.01 / 47.02 / 47.04 / 47.05）已合入 `main`（PR #285，merge commit `2ad3bc5`）。
> 本档是**段二**的开工证据。段二另开 PR，集成分支 `codex/lingdoc-architecture-improvement-20261020-arch-seg2`（基于 `origin/main @ 2ad3bc5`）。

## 开工卡片

```text
Issue / Spec：#259（父票，段二不关闭它）/ 子票 #262 #263 #271 #273 #264 #265 / Spec #47
用户可见结果：同一类失败在所有传输面返回相同的状态码、错误码与文案；
              新增一种业务错误不需要改任何传输层映射表。
范围内：47.06 错误形态跨面归并（四条剩余平表并入 clienterror）
        47.07 旧平表 contract + 结构断言（平表删除、兜底语义、结构断言）
        47.03 章节「已确认」单一谓词（段一剩余，且是 47.08 的硬前置）
        47.08 共享核心成为独立深模块
        47.09 调研域并入主管道
        47.10 组合根缺能力 fail-closed
范围外：段三 47.11–47.16（上下文切分与端口收窄）
        数据库 schema、路由、成功信封、重放元数据、持久化幂等记录格式
前置依赖：#262 ← #272(已闭)；#263 ← #262；#271 ← #260(已闭)；#273 ← #271+#261(已闭)+#262；
          #264/#265 ← #273
真实消费者：HTTP 接缝——workspace 主路由、规则治理（g4）、规则集存储（rulesetstore）、
          候选采纳（candidateadoption）、生成（generation）、G2 全流程（g2_full_http）
领域不变量与事务边界：本段只动**对外形态的声明位置**与**归并口径**，
          不动任何事务边界、幂等记录格式、审计写入时序与数据库约束
成功 / 失败 / 权限 / 恢复场景：每条被迁移的错误都必须有迁移前后的逐条对照断言
持久化 / 并发 / 审计证据：本段不引入持久化或并发变更；沿用既有事务边界，迁移不得改变零写入与幂等语义
当前风险与未决决策：见「少数口径变更清单」与「未决决策记录」
```

## 依赖图与切片

```text
#272 47.05 已闭 ──> #262 47.06 ──> #263 47.07
                                  │
#260 47.02 已闭 ──> #271 47.03 ───┴──> #273 47.08 ──> #264 47.09
                                                    └──> #265 47.10 ──> 段三 6 张
```

**前沿（现在可开工）**：`#262`（47.06）与 `#271`（47.03）。两者阻塞边都已解除。

**为什么 47.03 必须并进段二的 PR**：47.08 的原生阻塞边里有 47.03。段一交付的是四张票，
47.03 是段一剩下的一张；不先做它，47.08 就不开工。为了「段二可独立交付」，47.03 并入段二分支，
并在 PR body 中明确标注它属于段一的收尾而非段二的新增。

## 取证：迁移前基线（5 张平表）

47.05 已把**工作区主路由**（`internal/lingdoc/workspace/http.go`）迁到 `internal/clienterror`，
主路由的 42 个分支已归零。剩下 5 处映射点如下，**本段的「迁移前」基线就是它们**。

### 面 1：`internal/lingdoc/rulesetstore/http.go` → `writeApplicationError`

| 匹配的错误 | 状态 | 错误码 | 文案 | retryable |
| --- | --- | --- | --- | --- |
| `ErrActorRequired`, `ErrRuleSetTenantRequired` | 401 | `unauthenticated` | 需要有效的租户身份。 | false |
| `ErrRuleSetAccessDenied` | 403 | `forbidden` | 当前租户成员无权执行此规则集操作。 | false |
| `ErrRuleSetNotFound` | 404 | `not_found` | 资源不存在或不可访问。 | false |
| `ErrRuleSetVersionExists`, `ErrRuleSetStateConflict`, `template.ErrRuleSetNotDraft`, `template.ErrRuleSetNotPublished` | 409 | **`state_conflict`** | 规则集版本状态已变化，请重新读取。 | false |
| `ErrAuthorizerUnavailable` | 503 | `authorization_unavailable` | 授权服务暂不可用，请稍后重试。 | **true** |
| `ErrDraftHashMustBeEmpty`, `template.ErrInvalidRuleSet` | 400 | `invalid_request` | 规则集不符合安全约束。 | false |
| `ErrInvalidRuleSetUseAudit` | 400 | `invalid_request` | 规则集使用审计查询范围无效。 | false |
| `ErrRuleSetMetricsUnavailable` | 503 | `metrics_unavailable` | 规则集运行指标暂不可用，请稍后再试。 | **true** |
| 兜底 | 500 | `internal_error` | 操作失败，请稍后再试。 | **true** |

信封：`{"error":{"code","message","retryable"},"request_id"}`——**无 `details` 键**。
`retryable` 由 `writeError` 按 `status >= 500` 推导。

### 面 2：`internal/lingdoc/workspace/g4_full_http.go` → `G4ErrorStatus`（治理面）

| 匹配的错误 | 状态 | 错误码 | 文案 |
| --- | --- | --- | --- |
| `ErrG4HumanDecisionRequired` | 403 | `human_decision_required` | 请使用当前人工账号复核并提交。 |
| `ErrG4CrossTenantSourceUnsupported` | 422 | `cross_tenant_source_unsupported` | 当前目录流程无法在同一事务复核跨租户共享授权。 |
| `governance.ErrUnauthorized` | 403 | `template_governance_authorization_unavailable` | 目录治理授权不可用。 |
| `governance.ErrSourceUnavailable` | 403 | `source_access_denied` | 来源已不可用或无当前授权。 |
| `governance.ErrUnavailable`, `governance.ErrModelUnavailable` | 503 | `template_governance_unavailable` | 目录或规则建议服务尚未配置。 |
| `governance.ErrNotFound`, `rulesetstore.ErrRuleSetNotFound` | 404 | `not_found` | 资源不存在或不可访问。 |
| `governance.ErrIdempotencyConflict` | 409 | `idempotency_conflict` | 同一个操作键对应不同请求。 |
| `governance.ErrVersionConflict`, `governance.ErrStale`, `rulesetstore.ErrRuleSetVersionExists`, `rulesetstore.ErrRuleSetStateConflict` | 409 | **`version_conflict`** | 内容或基线已变化，请先读取当前版本。 |
| `governance.ErrInvalidState`, `template.ErrRuleSetNotDraft`, `template.ErrRuleSetNotPublished` | 409 | **`invalid_state`** | 当前版本状态不允许此操作。 |
| `governance.ErrReplayRequired` | 422 | `template_replay_required` | 须用服务器注册且绑定当前版本的正反例完成回放。 |
| `governance.ErrInvalidOutput` | **502** | `invalid_model_output` | 模型输出不符合当前目标和规则约束。 |
| `governance.ErrInvalidRequest`, `template.ErrInvalidDefinition`, `template.ErrInvalidRuleSet`, `rulesetstore.ErrDraftHashMustBeEmpty` | 400 | `invalid_request` | 请求字段不符合约定。 |

信封：`sendErrorDetails(..., nil)` → **带 `"details": null` 键**；`retryable` 由
`sendErrorDetails` 写死为 **false**（连 503 也是 false）。

### 面 3：`internal/lingdoc/generation/http.go` → `generationHTTPStatus` + `writeGenerationError`

| 错误 | 状态 | 错误码 | 文案 | retryable |
| --- | --- | --- | --- | --- |
| `ErrInvalidRequest` | 400 | `invalid_request` | 请求字段不符合约定。 | false |
| `ErrInsufficientEvidence` | 422 | `insufficient_evidence` | 允许资料不足以生成该章节。 | false |
| `ErrAssetNotReady` | 422 | `asset_not_ready` | 资料尚未处理完成。 | false |
| `ErrPreflightBlocked` | 422 | `preflight_blocked` | 请先处理交付阻断项。 | false |
| `ErrInvalidState` | 422 | `invalid_state` | 当前阶段不能执行该动作。 | false |
| `ErrAssetNotAuthorized` | 422 | `asset_not_authorized` | 请求中存在未获授权的资料，未开始处理。 | false（**带 `details`**） |
| `ErrNotFound` | 404 | `not_found` | 资源不存在或不可访问。 | false |
| `ErrForbidden` | 403 | `forbidden` | 无权执行该动作。 | false |
| `ErrSourceAccessDenied` | 403 | `source_access_denied` | 资料授权已不可用。 | false |
| `ErrVersionConflict` | 409 | `version_conflict` | 输入版本已变化，请重新读取。 | false |
| `ErrIdempotencyConflict` | 409 | `idempotency_conflict` | 同一幂等键对应不同请求。 | false |
| `ErrDependencyUnavailable` | 503 | `dependency_unavailable` | 依赖暂时不可用。 | **true** |
| 401 兜底 | 401 | `unauthenticated` | 请登录后重试。 | false |
| 兜底 | 500 | `internal_error` | 服务器暂时无法完成请求。 | **true** |

信封：`details` 只在非空时出现（`AssetNotAuthorizedError` 那条带 `details`）。
`retryable` 按 `status >= 500` 推导。

**取证中发现的一处不一致**：`ErrStaleInput` 出现在 `generationHTTPStatus`（→ 409），
但**没有**出现在 `writeGenerationError` 的分支里，因此它现在答 **409 + `internal_error` +
「服务器暂时无法完成请求。」**。这是收口前就存在的落空，见「未决决策记录 D-47-04」。

### 面 4：`internal/lingdoc/candidateadoption/candidate_adoption_http.go`

| 错误 | 状态 | 错误码 | 文案 | retryable |
| --- | --- | --- | --- | --- |
| `ErrInvalidRequest` | 400 | `invalid_request` | 请求字段不符合约定。 | false |
| `ErrNotFound` | 404 | `not_found` | 资源不存在或不可访问。 | false |
| `ErrForbidden` | 403 | `forbidden` | 无权执行该动作。 | false |
| `ErrSourceAccessDenied` | 403 | `source_access_denied` | 资料授权已不可用。 | false |
| `ErrStaleInput` | 409 | `stale_input` | 候选稿或快照基于旧输入。 | false |
| `ErrVersionConflict` | 409 | `version_conflict` | 内容已变化，请先读取当前版本。 | false |
| `ErrInvalidState` | 422 | `invalid_state` | 当前阶段不能执行该动作。 | false |
| `ErrIdempotencyConflict` | 409 | `idempotency_conflict` | 同一幂等键对应不同请求。 | false |
| `ErrDependencyUnavailable` | 503 | `dependency_unavailable` | 候选采纳依赖尚未就绪，请稍后重试。 | **true** |
| 401 兜底 | 401 | `unauthenticated` | 请登录后重试。 | false |
| 兜底 | 500 | `internal_error` | 服务器暂时无法完成请求。 | false（**注意：500 也不可重试**） |

信封：无 `details` 键。`retryable` 默认 false，只有 `ErrDependencyUnavailable` 显式为 true。

### 面 5：`internal/lingdoc/workspace/g2_full_http.go` → `g2FullError`

5 个分支（`ErrFormalChangeRequired` / `ErrG2ConsentRequired` / `ErrG2EvidenceIncomplete` /
`ErrG2OutcomeUnknown` / `ErrG2Unavailable`），未命中者落到工作区主路由的 `sendError`。

### 已被 47.05 迁走的工作区主路由

`internal/lingdoc/workspace/http.go` 的 `sendError` 现在只做两件事：渲染 `clienterror` 声明的形态，
以及给未声明形态的错误兜底 500 `internal_error`。主路由的 45 个错误形态由
`internal/lingdoc/workspace/client_form_contract_test.go` 逐条钉住。

## 已核实的对外分歧（收口必须消解）

### 规则集状态类错误（DR-47-02 的对象）

| 面 | 错误 | 状态 | 错误码 | 文案 |
| --- | --- | --- | --- | --- |
| 治理面 | `template.ErrRuleSetNotDraft`, `template.ErrRuleSetNotPublished` | 409 | `invalid_state` | 当前版本状态不允许此操作。 |
| 治理面 | `rulesetstore.ErrRuleSetVersionExists`, `rulesetstore.ErrRuleSetStateConflict` | 409 | `version_conflict` | 内容或基线已变化，请先读取当前版本。 |
| 规则集面 | 上列 4 个错误**合并在一支** | 409 | `state_conflict` | 规则集版本状态已变化，请重新读取。 |
| 工作区核心 | `lingdoctemplate.ErrRuleSetNotPublished` → 翻译成 `ErrInvalidState` | **422** | `invalid_state` | 当前项目状态或研究条件不满足操作要求。 |

**多数口径**：状态码 409（两处 409 对一处 422）；错误码 `invalid_state`（两处对一处）。
**DR-47-02 裁定**：归并后只有两类语义——「状态不允许该操作」→ 409 `invalid_state`；
「版本或基线已变化」→ 409 `version_conflict`。对外错误码 `state_conflict` 退役。

### 5xx 的可重试标记（DR-47-03 的对象）

| 面 | 5xx 的 retryable | 来源 |
| --- | --- | --- |
| 规则集面 | **true** | `status >= 500` |
| 生成面 | **true** | `status >= 500` |
| 候选采纳面 | **仅 `ErrDependencyUnavailable` 为 true**，其余 5xx 为 false | 逐个分支显式赋值 |
| 治理面 | **恒为 false** | `sendErrorDetails` 写死 |
| 工作区主路由 | 随形态声明 | `clienterror.Form.Retryable` |

**DR-47-03 暂定口径**：「4xx 不可重试、5xx 可重试」。按此，治理面的 503 与候选采纳面的其余 5xx
会从 false 变 true——这两处是**少数口径变更**，必须逐条列出。

### 502 孤例

`governance.ErrInvalidOutput` → **502 `invalid_model_output`**。全套对外形态里唯一的 502。
Spec 决策 19：保留状态码与错误码，但必须**进入统一语义表**，不再由个别传输面私有。

### 信封字段集（段一 45/45 比对漏掉的第 5 维）

契约 `docs/08-本轮实施方案/contracts/openapi.json` 的 `components.schemas.Error` 声明：
`properties` = code / message / retryable / **details**；`required` = code / message / retryable；
`details` 的 type 是 **object**（不是 null）。契约里 444 个错误样例中 **442 个不带 `details`**，
只有 2 个带（`DeniedAssetsError` 与 `AssetNotAuthorizedError` 那两条合法带 details 的路径）。

现状：

- 工作区主路由（47.05 已迁）：`{"error":{"code","message","retryable"},"request_id"}`——**无 `details`**。
- 规则集面、候选采纳面、生成面：**无 `details`**。
- 治理面：**带 `"details": null`**——而 `null` 不符合契约里 `details` 的 `"type": "object"`。

**结论**：段一迁移掉的 45 个错误，原先都发 `"details": null`；新实现不带该键。
**去掉 `details: null` 是向契约靠拢，不是契约破坏**——契约从未要求 `details`，且 `null` 违反其类型声明。
47.06/47.07 的对照表必须把**信封字段集**作为第 5 维，逐条记录迁移前后各面的字段集合。

## 统一语义表（收口后的形态目录）

| 形态 | 状态 / 错误码 | 文案 | 使用者 |
| --- | --- | --- | --- |
| 资源不存在 | 404 `not_found` | 资源不存在或不可访问。 | 规则集面、治理面、生成面、候选采纳面、工作区主路由 |
| 请求不合法 | 400 `invalid_request` | 请求字段不符合约定。 | 治理面、生成面、候选采纳面、工作区主路由 |
| 幂等冲突 | 409 `idempotency_conflict` | 同一个操作键对应不同请求。 | 治理面、工作区主路由 |
| 版本冲突 | 409 `version_conflict` | 内容已变化，请先读取当前版本。 | 治理面、候选采纳面、工作区主路由 |
| 状态不满足（项目） | 422 `invalid_state` | 当前项目状态或研究条件不满足操作要求。 | 工作区主路由 |
| 状态不满足（阶段） | 422 `invalid_state` | 当前阶段不能执行该动作。 | 生成面、候选采纳面 |
| 状态不满足（规则集） | 409 `invalid_state` | 当前版本状态不允许此操作。 | 治理面、规则集面（收口后） |
| 快照输入已变 | 409 `stale_input` | 快照基于的输入已变化，请重新准备。 | 工作区主路由 |
| 候选稿输入已变 | 409 `stale_input` | 候选稿或快照基于旧输入。 | 候选采纳面 |
| 依赖不可用 | 503 `dependency_unavailable` | 依赖暂时不可用。 | 生成面 |
| 候选采纳依赖未就绪 | 503 `dependency_unavailable` | 候选采纳依赖尚未就绪，请稍后重试。 | 候选采纳面 |

**「同码不同文」的处理原则**（票 #262 明确要求）：**不得把同码的文案合并成一句**。
上表中 `invalid_state` 有 3 种文案、`version_conflict` 有 2 种、`stale_input` 有 2 种、
`dependency_unavailable` 有 2 种——它们**各自是独立形态**，只是共享错误码。
共享形态目录只收「状态码 + 错误码 + 文案」三者全同的那一簇。

## 两条归属判据（形态由谁决定）

1. **形态由错误本身决定** → 在**错误定义处**声明。做法：sentinel 自带形态
   （`clienterror.New` / `clienterror.Wrap`）。新增同类错误一行到位，不改任何映射表。
2. **形态由发生场景决定** → 在**知道调用者处境的地方**包一个具名类型。
   沿用现役模式：影响任务的资料不可用（`impactTaskSourceUnavailable`）、审计写失败
   （`auditWriteFailed`）。

**本段必须用判据 2 的地方**：规则集状态错误在工作区核心被翻译成 `ErrInvalidState`（422），
但收口后它要答 409 `invalid_state`。同一个 sentinel 不能有两种形态，所以核心翻译处
必须改成一个**具名的场景类型**（例如 `workspacecore.ErrRuleSetStateNotAllowed`），
由它携带 409 `invalid_state` 的形态。

## 不可变契约（写代码前先定）

1. **真相主人**：对外形态由**错误自己**声明（`internal/clienterror`）。传输层只渲染，不决定。
2. **共享形态目录只有一个主人**：`internal/clienterror` 是全部域包都已依赖的宿主层叶子包，
   共享形态目录放在它旁边（新增 `catalog.go`），**不新增任何依赖边**。
3. **传输层映射器只允许兜底**：任何按业务错误逐个分支的映射表都不得存在。
4. **兜底语义不变**：未声明形态的错误仍答 500 `internal_error`，且**不泄漏**内部机制
   （响应里不出现原始数据库错误、SQLSTATE 或包名）。
5. **信封统一**：所有面答 `{"error":{"code","message","retryable"},"request_id"}`；
   只有合法携带 details 的两条路径带 `details`。
6. **事务边界、幂等记录格式、审计时序、数据库 schema 一律不动**。
7. **收口后对外错误码集合冻结**：`state_conflict` 退役是唯一被授权的删除。

## 少数口径变更清单（必须在 PR body 逐条列出）

| # | 变更 | 影响面 | 理由 |
| --- | --- | --- | --- |
| 1 | `state_conflict` 拆入 409 `invalid_state` 与 409 `version_conflict` | 规则集存储路由 | DR-47-02 裁定：多数口径为准 |
| 2 | 对外错误码 `state_conflict` 退役 | 规则集相关在途票 #187、#193 | DR-47-02 裁定 |
| 3 | 规则集状态错误在工作区核心从 422 改为 409 `invalid_state` | 工作区核心 3 处翻译点 | DR-47-02 多数口径；需场景类型 |
| 4 | 治理面 5xx 的 `retryable` 从 false 改为 true | 规则治理路由 | DR-47-03 暂定口径 |
| 5 | 候选采纳面除依赖不可用外的 5xx 的 `retryable` 从 false 改为 true | 候选采纳路由 | DR-47-03 暂定口径 |
| 6 | 治理面错误信封去掉 `"details": null` | 规则治理路由 | 向契约靠拢（契约 `details` 的 type 是 object，且非必填） |
| 7 | `ErrStaleInput` 在生成面从 409 + `internal_error` 改为 409 `stale_input` | 生成路由 | **未决**，见 D-47-04 |
| 8 | 稳定化的影响解析不再附加「章节版本的创建修订 == 项目当前修订」这一条额外判断 | 稳定化影响解析 | 三处消费者必须同答；确认记录才是「针对哪版研究条件做的确认」的权威 |
| 9 | **工作区主路由的兜底 500 的 `retryable` 从 false 改为 true** | 工作区主路由（未映射错误） | 兜底是 5xx，DR-47-03 说 5xx 可重试；不统一的话，同一个 `internal_error` 在主路由答 false、在另外三个面答 true——同一语义两个答案 |

**第 9 项与 47.07 验收标准的冲突**：47.07 写的是「兜底仍为内部错误且**不可重试**」。
收口前五个面的兜底本来就分成两派（规则集面、生成面按状态码推导 → true；
主路由、治理面、候选采纳面写死 → false）。本项按 DR-47-03 统一到 **true**，
理由是它只改一个面，而统一到 false 要改四个面、还会新增两处未授权的对外变化。
**47.07 的该条验收标准在兜底这一点上被 DR-47-03 取代**，该取代记在这里，并随 #292 一并确认。

## 未决决策记录

### D-47-04（已裁定 2026-10-20）：生成面 `ErrStaleInput` 的落空 → 修正

现状：生成面 `ErrStaleInput` 答 409 + `internal_error` + 「服务器暂时无法完成请求。」——
状态码对，错误码与文案是兜底值。收口到统一机制后，它会自然变成 409 `stale_input` + 相应文案。

**裁定：修正**（选项 A）。理由：`internal_error` 在这里不是「这个错误的样子」，是映射表漏了一支；
保留它等于把缺陷写进新机制，并让「同一类错误在不同面返回不同错误码」继续存在
（候选采纳面与工作区主路由对同类错误都答 409 `stale_input`）。
影响面：生成路由 409 响应的错误码与文案。已登记为「少数口径变更清单」第 7 项。

### D-47-05（暂定待确认，已单独成票）：5xx 可重试口径

DR-47-03 已暂定「4xx 不可重试、5xx 可重试」。本段按此执行并逐条列出少数口径变更（清单第 4、5 项）。

按 47.06 验收标准「DR-47-03 单独成子项」，该确认已单独开票：
[#292](https://github.com/magician336/lingdoc/issues/292)「47.03b 5xx 的『可重试』标记口径确认」，
挂在 #259 下，标签 `needs-info`。票里附了三张面在 5xx 上的现状取证，并点名一个待裁定的细节：
规则治理面的 503「目录或规则建议服务尚未配置」是**尚未配置**（重试也不会变好），
与候选采纳面的「依赖尚未就绪」业务含义不同。**该票不阻塞 47.06 的其余部分**；
若确认结论与暂定口径不同，只需改 `clienterror.Form.Retryable` 的取值规则。

## 验收矩阵（段二当前范围）

| 验收项 | 用户可见入口 | 领域不变量 / 事务边界 | 成功 | 失败 / 权限 / 恢复 | 持久化与并发 | 测试或运行证据 | 状态 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 47.06 四条平表并入同一机制 | 规则治理 / 规则集存储 / 候选采纳 / 生成 四个 HTTP 面 | 形态由错误自己声明；传输层只渲染 | 四面都能渲染 `clienterror.Form` | 每面未命中形态仍落兜底 500 | 不涉及 | 迁移前后逐条对照脚本 + 各面 HTTP 测试 | TODO |
| 47.06 规则集状态类归并（DR-47-02） | 规则集存储路由、规则治理路由、工作区核心 | 状态不允许该操作 → 409 `invalid_state`；版本或基线已变化 → 409 `version_conflict` | `state_conflict` 不再出现 | 三面同一错误答同一状态码与错误码 | 不涉及 | 跨面一致性测试 | TODO |
| 47.06 502 进入统一语义表 | 规则治理路由 | 状态码与错误码保留 502 `invalid_model_output` | 502 不再是私有一支 | — | 不涉及 | 形态目录断言 | TODO |
| 47.06 5xx 可重试口径（DR-47-03） | 四个面 | 4xx 不可重试、5xx 可重试 | 逐面 retryable 与状态码一致 | 少数口径变更逐条列出 | 不涉及 | 逐条对照表 | TODO |
| 47.06 同码不同文不合并 | 四个面 + 工作区主路由 | 同码不同文的形态各自独立 | 资料未授权 3 种、依赖不可用 3 种、输入已变 2 种文案均保留 | — | 不涉及 | 形态目录快照 | TODO |
| 47.07 旧平表删除，映射器只剩兜底 | 五个面的映射器 | 传输层不按业务错误逐个分支 | 映射器只认兜底 | 未映射错误仍 500 `internal_error` 且不泄漏内部机制 | 不涉及 | 结构断言（负例验证） | TODO |
| 47.07 兜底不泄漏 | 五个面的兜底 | 响应里无原始数据库错误 / SQLSTATE / 包名 | 兜底只给通用文案 | — | 不涉及 | 兜底契约测试 | TODO |
| 47.07 迁移前后对外形态对照表 | 全部被迁移的错误 | 状态码 / 错误码 / 文案 / 响应头 / **信封字段集** 五维 | 逐条相同（少数口径变更除外） | — | 不涉及 | 对照表落盘 `docs/08-本轮实施方案/` | TODO |
| 47.03 章节「已确认」单一谓词 | 交付与工作区 HTTP 面 | 确认事实唯一来源；空章节标识不被接受 | 三处判定答同一结果 | 空标识与非空不符分别判定 | 不涉及 | 谓词断言 + HTTP 测试 | TODO |
| 47.08 共享核心成为独立深模块 | 工作区核心 | 身份/哈希真相唯一；授权、审计、幂等与业务写入同事务；来源复核在只读事务内 | 上层只声明「做什么操作」 | 失败时零业务写入 | 迁移、重启读回、竞态 | 既有测试全绿 + 结构断言 | TODO |
| 47.09 调研域并入主管道 | 调研域 HTTP 面 | 不存在第二套幂等/重放/审计实现 | 五处行为逐条等价 | — | 幂等记录格式与写入时序不变 | 等价对照断言 | TODO |
| 47.10 组合根缺能力 fail-closed | 启动期 | 缺必需端口 → 启动期明确失败 | 正常装配启动成功 | 缺必需端口启动失败 | 不涉及 | 装配测试 | TODO |
| 全仓编译与静态检查 | — | — | — | — | — | `go vet ./internal/...`、gofmt、golangci-lint（CI 口径） | TODO |
| 真实服务链路 | 外部模型 / 真实模型调用 | — | — | — | — | 本段不涉及外部服务；标 `N/A` | N/A |

矩阵中的 `TODO` 在当前范围内闭环之前，PR 保持 Draft。

## 47.06 的实现结果（2026-10-20）

### 五张平表已空

`rulesetstore` / `candidateadoption` / `generation` / `g4_full_http` / `g2_full_http` 五个传输面
不再持有按业务错误逐个分支的映射表；六个渲染器只剩兜底。工作区主路由早在 47.05 就已空。

### 共享形态目录

`internal/clienterror/catalog.go`：只收**状态码 + 错误码 + 文案三者全同**的形态。
「同码不同文」的形态各自独立声明，永不并成一句。目录放在 `clienterror` 包内是有意的——
所有消费方都已经依赖它，这样共享形态**不新增任何依赖边**。

### 两条归属判据都落地了

- **形态由错误本身决定**：sentinel 自带形态（`clienterror.New` / `clienterror.Wrap`）。
- **形态由发生场景决定**：用具名场景值**包住**原 sentinel（`errors.Is` 仍然成立）。
  规则集存储面、候选采纳面、G2 面的同码不同文冲突都这样解。

`workspacecore.ErrRuleSetStateNotAllowed` 是第 3 项的落点：工作区核心把「规则集未发布」
翻译成失败时，改用具名场景类型，答 409 `invalid_state`。

### 迁移前后对照表（机械生成）

`docs/08-本轮实施方案/47.06-错误形态对照表.md`，由脚本从 `2ad3bc5` 解析旧平表、
与工作树的声明逐条比对，**五维**（状态码 / 错误码 / 文案 / retryable / 信封字段集）。

结果：比对 **67** 条，完全一致 **33** 条，有差异 **34** 条（维度级 42 行），
**未授权差异 0**——每一处差异都落在「少数口径变更清单」的某一项上。脚本的结论行是
`all differences are approved`。

### 结构断言与阴性验证

`internal/lingdoc/workspace/client_form_convergence_test.go`：

1. `TestTransportFilesHoldNoBusinessErrorBranch`——五个传输文件内不得有按业务错误分支的
   switch/条件，不得有按业务错误判断的 `errors.Is/As`（唯一豁免是合法携带 details 的路径）。
2. `TestTransportMappersOnlyFallBack`——六个渲染器内不得有 switch、不得有业务错误判断。

**阴性验证**：向 `rulesetstore/http.go` 注入 `if errors.Is(err, ErrRuleSetNotFound)` →
两条断言各报一条，带文件与行号；向 `generation/http.go` 注入 `switch { case errors.Is(err, ErrNotFound) }`
→ 报出「分支按业务错误选择结果」与「还在按业务错误分支」。两次注入均已撤销并复跑通过。

另有三个包内的兜底测试（`http_fallback_test.go` × 3）钉住兜底语义：未映射错误仍答
500 `internal_error`，且响应里不出现原始数据库错误、SQLSTATE 或包名。

### 计划未覆盖处所做的决定

1. **G2 面的信封也去掉了 `details: null`**——计划的基线表没记这个面，但不可变契约第 5 条
   要求所有面答同一字段集，G2 也不是合法携带 details 的两条路径之一，故一并收口（第 6 项延伸）。
2. 生成面 `ErrStaleInput` 的文案取候选采纳面措辞「候选稿或快照基于旧输入。」。
3. 治理面与候选采纳面的**兜底 500** 也按第 4/5 项改为 `retryable: true`。
4. 规则集存储面、候选采纳面、G2 面的同码不同文冲突，一律用具名场景值承载各面自己的措辞。
5. `impact_routes.go` 与 `g5_machine_read_http.go` 仍带 `details: null`（不在五张平表内，未动）。
6. `ErrAdoptionServiceUnavailable`（503 + `invalid_state`）保留旧形态，以逐字节不变。

## 47.08 的第一片：共享核心模块与两条规则归一（2026-10-20）

### 模块

`internal/lingdoc/operationcore` 拥有操作管道的知识：步骤顺序、授权先于重放、
幂等键与请求体摘要的唯一计算、来源复核在只读事务内、审计写入与业务写入同事务。
公开面小（`Identity` / `Authorization` / `Ledger` / `Request` / `Errors` / `Options` /
`Outcome` / `Run`），聚合侧通过接缝提供领域事实。

管道**不拥有**对外错误呈现：错误由聚合侧通过 `Errors` 交进来，管道原样抛出，
所以每个失败的对外形态一个字都没变。

### 两条规则归一（11 处副本 → 1 处主人）

| 规则 | 原状 | 现在 |
| --- | --- | --- |
| 幂等键长度（8..128） | LingDoc 域内被写死 11 处 | 一律 `operationcore.ValidKey(key)` |
| 请求体摘要（sha256 over JSON，hex） | 主管道、调研管道、改写面各算一遍 | 一律 `operationcore.RequestHash` / `ValueHash` |

两处替换都**逐字等价**：`len(key) < 8 || len(key) > 128` 与 `!ValidKey(key)` 等价；
摘要仍是 `json.Marshal` 后 sha256 再 hex，`ValueHash` 在值无法编码时返回空编码的摘要，
与原先 `raw, _ := json.Marshal(value)` 后哈希 `raw` 的行为一致。

**已知例外**：`internal/evidence/binding.go` 仍有一份键规则。`internal/evidence` 是宿主层，
不能反向依赖 LingDoc 域包，故该副本保留并在此记录。

### 结构断言

`internal/lingdoc/operationcore/ownership_test.go` 扫 `internal/lingdoc/**`（排除本包与测试文件），
报出任何残留的键长度规则写死、或就地计算的摘要。
**已做阴性验证**：注入探针函数写死键规则，断言精确报出
`internal/lingdoc/workspacecore/zz_negative_probe.go: 幂等键长度规则被写死（应经过 operationcore.ValidKey）`，
随后删除探针并复跑通过。

### 尚未完成（下一片）

聚合侧还没有改为**组合**这个模块：`internal/lingdoc/workspacecore/service_facade.go` 的
`runOperation` 仍是自己实现管道。下一片把它改成填充接缝并调用 `operationcore.Run`。
本片先把模块与规则立住，让接线的每一步都有一处可验证的主人。

**一处修正**：起草中的 `Run` 漏了原管道对 actor 的合法性检查（`validActor`），
那会让一个无主体的请求穿过前两道门。已补回（`Request.ValidActor` 接缝），
并且**在接缝缺失时默认不检查**——接线时必须填上。

## 47.03 的实现结果与数据核查（2026-10-20）

### 一个谓词，三处消费者

新增 `internal/lingdoc/workspacecore/chapter_confirmation.go`：
`ChapterConfirmed(basis, facts)` 与它的变体 `ChapterConfirmationOf`。判据是票里的七条：
存在有效确认记录，且**章节标识、章节版本标识、研究条件修订、模板版本、确认人、确认时间**
全部与当前状态一致。

三处消费者现在共用它：

| 消费者 | 位置 | 改动 |
| --- | --- | --- |
| 交付预检 | `workspacecore/service.go` 的 `chapterView` | 内联判定换成谓词 |
| 规则治理的影响阻断判定 | `workspace/template_governance_runtime.go` | 内联判定换成谓词；**「章节标识为空也算」收紧为相等**（DR-47-01） |
| 稳定化的影响解析 | `workspacecore/impact_service.go` | 去掉额外判断，只用谓词的答案 |

**为什么治理面此前要读 details 里的 `chapter_id`**：行上的 `chapter_id` 列在查询条件里，
而 details JSON 里另有一份 `chapter_id`。宽松判定「为空也算」针对的就是后者。生产写入路径
（`candidateadoption/candidate_adoption_store.go:256`）用**同一个** `in.ChapterID` 同时写行外键列
与 details JSON，两份值恒等。

### 47.03 带来的一处对外行为变化（少数口径变更第 8 项）

稳定化的影响解析此前是 `chapter.ConfirmationValid && version.SpecRevision == p.SpecRevision`。
后半句用的是**章节版本的创建修订**——它回答的是「这个版本是什么时候创建的」，
而不是「这次确认是针对哪一版研究条件做的」。一个在新版研究条件下被重新确认过的章节，
创建修订较旧，于是交付预检说「已确认」、稳定化闸门说「未确认」——正是本票要消除的分歧。

现在由确认记录的修订与当前状态是否一致来判定（谓词），稳定化与另外两处同答。
**影响面**：被重新确认过的章节（创建修订较旧）在稳定化影响解析里从「未确认」变为「已确认」。

### 确认失效的单一写入点

`markChapterUnconfirmed(chapter *Chapter)` 是唯一写这个标志的地方。
「章节正文改写」「工作副本提交」两处的内存标志，以及「正式依据变更」那处的持久化失效，
都收在这一个机制下。持久化那半在 `InvalidateChapterConfirmations`：
一个不再是当前版本的章节版本，任何确认记录都对不上（谓词要比对章节版本标识），
所以不需要额外调用。

结构断言（`internal/lingdoc/workspacecore/chapter_confirmation_ownership_test.go`）扫本包源码，
报出所有**不在** `markChapterUnconfirmed` 与 `chapterView` 里的 `ConfirmationValid` 赋值。
断言计数也统计被允许的两个写入点，否则代码变干净后断言会变成空转。
**已做阴性验证**：注入一个探针函数直接写该字段，断言精确报出 `zz_negative_probe.go:7`，随后删除探针。

### 数据核查结论

| 问题 | 结论 | 证据 |
| --- | --- | --- |
| 确认记录表的章节标识可能为空吗？ | **不可能** | PostgreSQL `migrations/versioned/000099_lingdoc_candidate_adoption.up.sql:32`：`chapter_id varchar(36) NOT NULL REFERENCES lingdoc_chapters(id)`；SQLite `migrations/sqlite/000020_lingdoc_candidate_adoption.up.sql:30,35`：`chapter_id TEXT NOT NULL` + 外键 |
| details JSON 里的章节标识可能为空吗？ | **不可能** | 生产写入用同一个 `in.ChapterID` 写行与 details（`candidate_adoption_store.go:251-256`） |
| 确认人 / 确认时间可能缺失吗？ | **不可能** | `candidateadoption/confirmation.go:185-189` 的 `newConfirmation` 总会写 `ActorUserID` 与 `CreatedAt` |
| 章节标识可能与章节版本不匹配吗？ | **schema 不禁止，但谓词会拒绝** | 两个外键各自独立；谓词同时比对章节标识与章节版本标识，对不上就不算确认 |

这与 DR-47-01 已核实的结论一致（「确认记录表的章节标识在两个数据库上都是非空且带外键」），
**不需要数据迁移**。

## 47.08 的第二片：聚合体组合共享核心（2026-10-20）

### 管道现在有一个主人

`runOperation` 原来在 `workspacecore/service_facade.go` 里自己实现整条管道：
键检查 → 逆调用方检查 → 人工决策门 → 请求摘要 → 溯源准备 → 来源复核 →
授权 → 审计写入 → 幂等记录 → 业务写入 → 投影。

现在它只声明「做什么操作」，管道由 `internal/lingdoc/operationcore` 的 `Run` 执行：

| 原来 | 现在 |
| --- | --- |
| `runOperation` 自己实现 15 步 | `runOperation` → `runOperationThroughPipeline` → `operationcore.Run` |
| 管道步骤散在聚合体里 | 管道步骤在 `operationcore`，聚合体只填接缝 |

接线层是 `internal/lingdoc/workspacecore/operation_pipeline.go`。它**不做任何决定**，
只提供管道不知道的领域事实：谁是主体、项目是什么、操作记录存在哪、失败对外读作什么。

### 三条规则归一（本片新增）

第一片归一了幂等键长度与请求体摘要两条。本片归一了另外三条：

| 规则 | 原状 | 现在 |
| --- | --- | --- |
| 人工决策门 | `workspacecore.requiresHumanOperation` + `workspacecore.RequireHumanDecision` 各一份 | `operationcore.RequiresHumanDecision` + `operationcore.RequireHumanDecision`；工作区改为调用 |
| 重放载荷解码失败标记 | `workspacecore.replayPayloadDecodeError` 自有类型 | `operationcore.ReplayPayloadDecodeError`；工作区的标记与识别都改为委托 |
| 写后溯源的事务句柄 | 管道接缝给不出 `tx` | `Request.StoreProvenance` 增加 `tx Tx` 参数（管道内部本就有事务） |

### 三处类型适配（不改行为）

工作区的类型与共享核心的类型不是同一套，适配层做三处无损转换：

1. **身份**：`OperationIdentity`（具体类型）↔ `operationcore.Identity[Actor]`（泛型）。
   操作名、目标、键三者的字符串形式逐字相同。
2. **结果**：`OperationResult.Output` 是 `operationOutputKind`，`operationcore.Result.Output` 是 `string`。
   `operationOutputKind` 底层就是 string，往返逐字相等；且 `Output` **根本不落库**
   （`operationRow` 只存 `BodyHash`/`ResponseJSON`/`ResponseCode`），持久化路径上无损失。
3. **授权**：`AuthorizationRequest.Action` 是 `AuthorizationAction`，
   `operationcore.Authorization.Action` 是 `string`。往返后 `capability()` 的拼法完全一致：
   资源为空取动作，否则 `动作:资源`——两处实现逐字相同（`typed_contracts.go:142` 与
   `operationcore.go:154`），审计记录的 capability 不变。

### 不变契约在管道里成立

47.08 的验收标准第 3 条逐项对照 `operationcore.Run`：

| 不变契约 | 管道里的落点 |
| --- | --- |
| 身份与哈希真相唯一 | `operationcore.Identity` + `RequestHash`（管道内算一次） |
| 授权、审计、幂等记录与业务写入同事务 | `Run` 的写事务内依次执行授权 → 审计 → 幂等记录 → 业务写入（`operationcore.go:406-493`） |
| 来源复核在只读事务内且不跨事务持连接 | 只读事务内复核 + 事务外再查一次（`operationcore.go:366-403`） |
| 失败时零业务写入（审计是唯一允许的写入） | 业务写入在事务内，失败即回滚；审计是唯一在授权失败时单独写的记录 |

`typed_contracts_regression_test.go:31` 断言「输出不匹配时 `repository.writes == 0`」，
是「失败时零业务写入」在真实接缝上的守门用例，本片后仍然通过。

### 结构断言

新增 `internal/lingdoc/workspacecore/operation_pipeline_ownership_test.go`：
扫工作区核心源码，报告任何**在管道接缝之外写入幂等记录**的地方。

断言范围经过收窄：只断言「写入」不断言「读取」。理由是读取有两类：
管道通过 `Ledger` 接缝读一次（管道自己的），而 `g1_operation_provenance.go:66` 的
溯源证明校验还要读同一张表来确认「操作记录与已存证明仍然一致」——那是工作区自己的
一致性规则，不是管道步骤。写入则是管道的独占职责：一条已存记录就是「这个操作发生过」
的证明，只有管道能创建。

**阴性验证**：注入 `zz_negative_probe.go`（在接缝外调用 `operations.SaveOperation`），
断言精确报出 `internal/lingdoc/workspacecore/zz_negative_probe.go:11 幂等记录被绕过管道写入
（应经过 operationcore 的 Ledger 接缝）`；随后删除探针并复跑通过。

### 验证

| 命令 | 结果 |
| --- | --- |
| `go build ./internal/...` | PASS |
| `go vet ./internal/...` | PASS |
| `gofmt -l internal/` | 无输出 |
| `git diff --check` | PASS |
| `go test -p 1 -count=1 ./internal/lingdoc/...` | PASS（13 个包全绿，含 `workspace` 123s、`workspacecore` 37s） |

## 47.09 的第一片：调研域并入共享管道（2026-10-20）

### 第二套管道没有了

调研域原来有自己的管道：`g2Operation`（`g2_service.go`）自己实现幂等键检查、
请求体摘要、只读复核、来源重查、授权、幂等记录读写、业务写入、投影。

现在它走共享管道：`g2Operation` 只声明「做什么操作」，管道由
`internal/lingdoc/operationcore` 的 `Run` 执行。域内 **18 个**调用点全部走这一条管道。

调研域自己保留的是**领域事实**，不是管道步骤：

| 调研域的事实 | 声明在哪里 |
| --- | --- |
| 需要哪个 capability | `g2Operation` 的 `capability` 参数 |
| 要不要人工决策门 | 每次调用显式声明（见下） |
| basis 由什么字段构成 | `Basis` 接缝：项目版本 + 研究条件修订 + 上下文修订（三字段） |
| 审计写什么 | `AfterWrite` 接缝：`g2.<操作名>` + `{context_revision}` |
| 来源怎么收集 | `SourceIDs` 接缝 |

### 人工决策门改成声明式

调研域的 18 个调用点**对门的要求不一致**，而且同一个操作的要求取决于调用：

| 情形 | 例子 |
| --- | --- |
| 一定要门 | `saveResearchClaim`、`confirmEvidenceCard`、`rejectActionProposal` |
| 一定不要门 | `createActionProposal`、`createAgentSession`、`createImportCandidate` |
| 取决于调用 | `sendAgentTurn`：只有 `input.SensitiveContextConfirmed` 时才要门 |

按操作名静态判定装不下这三种。所以门改成**由调用方声明**：

- 共享核心新增 `HumanGate` 三态枚举：`HumanGateRequired` / `HumanGateNotRequired` /
  `HumanGateUndeclared`；
- **零值是 `HumanGateUndeclared`**，管道遇到未声明就拒绝（答 `invalid_request`）。
  这不是多余的第三态：`bool` 的零值会静默表示「不要门」，那是**悄悄削弱检查**的方向；
  枚举的零值若表示「要门」，未声明的调用方会被莫名拒绝。三态让「没说」被识别出来。
- 共享核心**不再持有**操作名单。哪些操作要门是各域的知识：工作区有自己的名单
  （`workspaceHumanDecisionOperations`），调研域每次调用显式声明。
- 调研域用例层的 10 处 `RequireHumanDecision(ctx)` 检查**移除**——门由管道声明承担，
  同一个检查跑两次没有意义。

`sendAgentTurn` 的条件门由用例层算出（`g2AgentTurnGate(input)`）再声明进去。

### 门的位置变了（一处口径变更）

原来调研域的门是**用例的第一道检查**（在字段校验、来源检查、授权之前）。
现在门在**管道内**，位于键检查之后、授权之前。

因此对**机器身份**发起一个本身就不合法的请求时，答案会变——由「哪道检查先失败」
决定，而不是一律由门决定：

| 场景 | 原来 | 现在 |
| --- | --- | --- |
| 机器身份 + 未确认 | 403 `human_decision_required` | 422 `confirmation_required` |
| 机器身份 + 字段非法 | 403 `human_decision_required` | 400 `invalid_request` |
| 机器身份 + 授权不通过 | 403 `human_decision_required` | 404 `not_found` |
| 机器身份 + 请求本身合法 | 403 `human_decision_required` | 403 `human_decision_required`（不变） |

**合法请求的答案不变**；只有「本来就会被拒绝的请求」换了错误形态。这记为
**少数口径变更第 10 项**。

**安全语义没有变**：机器身份仍然被拒绝，且仍然不触发任何外部效果。
`TestG2MachineCannotAssertHumanDecisionDespiteProjectOwnerAndConfirmedFlag`
的守门断言——「机器身份不得触发外部效果」「机器身份不得改写卡片真相」——**全部通过**，
只有「必须是 `ErrHumanDecisionRequired`」这一条形态断言改成了「必须被拒绝」。

归一后另有一条断言**证明门本身仍然拦得住**：
`TestTheGateRefusesAMachineIdentityOnAnOtherwiseValidRequest` 用一个「其它检查都通过」
的请求，断言答案是 `ErrHumanDecisionRequired`——即拒绝确实来自门，而不是授权。

### 另外两处口径变更

**第 11 项：调研域在授权失败时也写审计。**
共享管道在授权失败时写一条授权决定审计（`RecordAudit(..., authErr)`）；调研域原来
只在业务写入成功后写业务审计，授权失败不写。归一后调研域的授权失败会多一条审计记录。

**第 12 项：调研域在写事务内多做一次来源集合比较。**
共享管道在来源敏感的操作上，会在只读复核与写事务之间比较来源集合与「是否命中已有
记录」；调研域原来只查一次。并发的写入会让两者不一致，管道此时拒绝（`version_conflict`），
调研域原来会放过。这是**收紧**，不是放宽。

### 证据

| 断言 | 覆盖 |
| --- | --- |
| `TestResearchDomainBehavesLikeTheSharedPipeline` | 五处行为逐条：幂等键冲突、请求体哈希、重放顺序、来源重查、人类决策门 |
| `TestTheResearchDomainDeclaresItsGatePerCall` | 同一个机器身份，走门的操作被门拒、不走门的操作不被门拒 |
| `TestThePipelineHasNoCopyOfTheGateOperationList` | 结构断言：共享核心不得再持有按操作名判门的名单 |
| `TestHumanGateTreatsAnUndeclaredGateAsRefused` | 三态的零值是「未声明」 |
| `TestRunRefusesAnUndeclaredGate` | 未声明时管道拒绝操作 |

**阴性验证**（每条都做过：注入违规 → 精确报出 → 回滚）：

| 断言 | 注入的违规 | 报出 |
| --- | --- | --- |
| 共享核心不得持有门名单 | 在 `operationcore` 注入 `RequiresHumanDecision(operation)` | `zz_negative_probe.go: RequiresHumanDecision decides the gate from the operation name: the caller declares it` |

另外，门的状态用**机械扫描**核对过：从基线代码读出每个方法自己是否调用
`RequireHumanDecision`，与归一后的声明逐条比对，**18 个调用点的门要求全部一致**。

## 遗留与风险

- **`ErrStaleInput` 的生成面落空**是本段取证中新发现的既有缺陷，D-47-04 需要裁决。
- **CI 的「按 diff 内容分级门禁」只看最后一次推送**（ADR 0005）：合并前必须让最后一次推送含 `.go` 改动，
  否则 lint job 与 PR gate 的 provider/frontend 步骤会被标成 NOT RUN。
- **隔离 PostgreSQL 与全量回归并发会被 Windows 资源压垮**（autovacuum worker `0xC0000142` 退出并带走库）。
  证据必须分两轮：全量轮不设 `LINGDOC_TEST_POSTGRES_DSN`（PG 用例按仓内约定 SKIP，记 NOT RUN），
  PG 轮单独轻负载跑。
