# Spec #47 段三：上下文切分与端口收窄 — 开发计划与证据

> 状态：进行中（2026-10-21 开工）。本文件是段三唯一的实施依据。
> Spec：[`../../06-灵档产品开发规划/47-Spec-模块边界收口与知识归属归位.md`](../../06-灵档产品开发规划/47-Spec-模块边界收口与知识归属归位.md) 第四段「上下文切分与端口收窄」（决策 31–37）。
> 前置：段一（PR #285）、段二（PR #293，merge `633ba34`）已合入 main。

## 开工卡片

| 项 | 内容 |
| --- | --- |
| Spec | #47 第四段（决策 31–37） |
| 父票 | #259 |
| 本段票 | #266(47.11) #274(47.12) #267(47.13) #275(47.14) #268(47.15) #269(47.16) |
| 用户可见结果 | 无。本段是纯结构重构：对外契约（路由、信封、状态码、错误码、文案、重放元数据、数据库 schema、幂等记录格式）逐字不变。 |
| 目的地 | 传输层不再持有聚合体具体类型；宽口退役；上下文按簇拥有自己的窄口。 |
| 范围内 | 8 条传输面的接缝替换；窄口声明归位；成员与 Owner 上下文切出；两个（实为三个）宽口退役；结构断言与行为零变化证据。 |
| 范围外 | 聚合体 `*core.Service` 内部的进一步拆分；领域类型从 `workspacecore` 迁出；错误形态收敛（段二已完成）；G3/G4/G5 功能线（#246 / #248 / #149 / #323）。 |
| 依赖 | main `633ba34`；#268 额外依赖在途 PR #149 合入。 |
| 真实消费者 | `internal/container/lingdoc.go`（唯一生产组合根）、`internal/router/`（路由装配）、前端通过 HTTP 契约消费，段三不改 HTTP 契约。 |
| 集成分支 | `codex/lingdoc-architecture-improvement-20261021-arch-seg3`（从 main `633ba34` 起） |

## 依赖图与切片顺序

```
#265 47.10 已闭 ──┬─> #266 47.11 规则治理运行时/选区改写/审阅锚点   ┐
                  ├─> #274 47.12 全流程路由/全流程守卫              │
                  ├─> #267 47.13 影响中心路由                      ├─> #269 47.16 遗留宽口退役
                  ├─> #275 47.14 G2 路由/智能体上下文              │
                  └─> #268 47.15 成员与 Owner 上下文切出 ──────────┘
                        （#268 额外被在途 PR #149 阻塞）
```

- **#266 立范式**：第一张实现，用来定死「窄口怎么写、结构断言怎么写、行为零变化怎么证」。后三张照它复制。
- **#267 有前置**：票面要求「与在途票 #246 对齐后再动手」。#246 的两条影响中心缺口（G3-GAP-06 / G3-GAP-07）都是**验收证据类**缺口，能力侧已实现并在 head `a87459f` 落证；#267 只改接缝不改渲染，故必须证明影响中心的响应形状逐字未变（G3.7 行仍是 `NOT RUN`，不得因本次重构被记成 `PASS`）。
- **#269 是收益兑现点**：窄口新增但宽口还在，279 个方法仍然可达，复杂度不降。#269 把 `ApplicationService`(37) 与 `G2Application`(31) 删掉，才算真正关掉后门。

## 取证：迁移前的基线事实

### 聚合体

`*core.Service`（`core` = `internal/lingdoc/workspacecore`），声明于 `internal/lingdoc/workspacecore/service_facade.go:206`，**279 个方法**，字段 12 个（repository / templates / ruleSets / sources / authorizer / audit / mode / catalog / g2Runtime / impactPreflight / impactRules / runtimeRecovery）。

### 八条面：持有形态与实际调用集

机械扫描方法：对 `workspacecore` 全包收集 `func (s *Service) X(` 的方法名集合，再在每条面的文件里找 `<recv>.X(` 调用，接收者逐条人工复核。脚本见 `.scratch/lingdoc-arch-20261021/seg3/scan_surfaces.py`。

| 票 | 面 | 文件 | 持有形态 | 唯一方法数 |
| --- | --- | --- | --- | --- |
| #266 | ① 规则治理运行时 | `workspace/template_governance_runtime.go` | `projects *core.Service` | 3 |
| #266 | ② 选区改写 | `workspace/selected_rewrite.go` | `workspace ApplicationService`（**37 方法宽口**） | 4 |
| #266 | ③ 审阅锚点 | `workspace/review_anchor_http.go` | 已是 `ReviewAnchorRead/WriteApplication` 窄口 | 2（**无需改**） |
| #274 | ④ 全流程路由 | `workspace/full_workflow_http.go` | `FullWorkflowDependencies.Core *core.Service` | 17 |
| #274 | ⑤ 全流程守卫 | `workspace/full_workflow_guard.go` | `FullWorkflowGuard.Core *core.Service` | 16 |
| #267 | ⑥ 影响中心路由 | `workspace/impact_routes.go` + `roadmap_routes.go` + `impact_dependency_candidate_routes.go` | `impactRoutesHandler{service *core.Service}` | 46 |
| #275 | ⑦ G2 路由 | `workspace/g2_full_http.go` | `RegisterG2FullRoutes(group, service *core.Service)` | 32 |
| #275 | ⑧ 智能体上下文 | `workspace/agent_context.go` | `G2ActionExecutor.projects *core.Service` | 7 |

**主 Handler 不在清单里**：`internal/lingdoc/workspace/http.go` 里 `core.Service` 出现 **0 次**——段二（47.10）已把它收口成 `ProjectApplication` / `MembershipApplication` / `ChapterApplication` / `TemplateApplication` / `ChangeSetApplication` / `SourceApplicationService` 六个上下文口。

### 三个宽口（不是两个）

| 宽口 | 方法数 | 声明位置 | 用途 | 处置 |
| --- | --- | --- | --- | --- |
| `ApplicationService` | 37 | `workspace/contracts.go:16` | 「HTTP 面向的工作区契约」，段二起已被 `HandlerDependencies.Service` 标为「为兼容旧适配器保留」 | #269 删除 |
| `G2Application` | 31 | `workspace/agent_context.go:14` | 「additive transport port / 遗留兼容口」 | #269 删除 |
| `WorkspaceReadApplication` + `WorkspaceWriteApplication` | 12 + 22 = **34** | `workspace/contracts.go:59,76` | 段二遗留的**推断输入**：`resolvePorts` 用它推断上下文口，`http.go` 的 `split*Application` 适配器转发到上下文口 | #269 一并退役 |

第三个宽口是实测发现：Spec 决策 34 只点名了前两个，但 `Read`/`Write` 合起来覆盖 34 个操作、跨越全部上下文，若留着，决策 34 的「一个上下文一个窄口」就没兑现，`resolvePorts` 的多重推断（决策 37）也删不掉。**票 #269 的「全部调用方迁到按上下文收窄的端口」按此口径执行。**

### 组合根

`internal/container/lingdoc.go:59` 建一个 `projectService`（`*core.Service`），共享给 10 处：G2 运行时配置(:68)、G2ActionExecutor(:68)、TemplateGovernanceRuntime(:69)、FullWorkflowGuard(:75)、`FullWorkflowDependencies.Core`(:77)、`HandlerDependencies` 的 8 个字段(:78-87)、SelectedRewriteService(:89)、机器读(:94)、审阅锚点(:95)、容器对外字段 `Projects workspace.ApplicationService`(:29,:101)。

决策 35 原文：「一个实例被多个面与运行时共同持有，任一面可调用任一方法」——实测成立。

## 已裁决的四个分叉（用户 2026-10-21 裁决）

| # | 分叉 | 裁决 | 落地口径 |
| --- | --- | --- | --- |
| Q1 | 新增窄口声明在哪个包 | **A**：上下文语言由 `workspacecore` 拥有的 → 窄口声明在 `workspacecore`；用例自有的（选区改写、审阅锚点）留在 `workspace` | 已有先例：`workspacecore` 已导出用例级口 `G2ActionExecutor`（`agent_context.go:69` 使用）与 `ProjectAuthorizer`。**并须把 `workspace/contracts.go` 里已有的五个上下文口一并迁到 `workspacecore`**，否则同一种东西有两个家（决策 33 的一致应用）。`MembershipApplication` 按 #268 迁进新包。 |
| Q2 | 窄口粒度 | **A**：按上下文簇切，一条面依赖若干窄口 | 决策 34 原文「一个上下文一个窄口，只含该上下文用到的操作」。 |
| Q3 | 成员与 Owner 上下文落点 | **A**：新包 `internal/lingdoc/membership`，端口 + 实现 + 不变量 + 领域类型一并迁出；#268 等 #149 合入后再动手 | **修正（见下）**：Spec 决策 32 与票 #268 原文只要求「端口、实现与不变量」三类；「领域类型一并迁出」是问诊选项的措辞，不是 Spec 要求，且会造成导入环。类型留在 `workspacecore`。 |
| Q4 | 与在途 PR 的关系 | **A**：段三照做，同时给三个兄弟会话各发一条事实性通知 | 通知内容限于「我改哪些文件、为什么」，明确写「知悉即可，不必立即 rebase」。 |

### Q3 的两个修正（实测得出）

**修正一：领域类型不外迁。** `OwnerTransfer` 声明于 `workspacecore/model.go:158`，被 `workspacecore/service.go:645,652,660`（`gormTransaction` 的读写）、`service_facade.go`、`typed_contracts.go` 引用。类型一旦搬到 `membership`，`workspacecore` 就得 import `membership`；而 `membership` 需要 `core.Actor`（`model.go:389`）与 `core.Repository` → 双向依赖 → 导入环。
Spec 决策 32 的原文是「该上下文的**端口、实现与不变量**一并迁出」——类型不在其中。票 #268 原文同。**故：类型留在 `workspacecore`，迁出端口 + 实现 + 不变量。**

**修正二：#268 有一个未识别的前置——段二的共享操作管道以聚合体为键。**
`runOperation[T,I,R](s *Service, ...)`（`service_facade.go`）转发到 `runOperationThroughPipeline`（`operation_pipeline.go`），后者直接使用聚合体的私有成员：

- `s.repository.Transaction(...)`（开事务）
- `s.authorizeTenant` / `s.authorizeProject`（授权）
- `s.recordAuditInTransaction`（审计写入）
- `s.checkSources`（来源复核）
- `s.g1PrepareCachedChangeOperation`（重放缓存）

而成员与 Owner 的用例实现（`workspacecore/membership.go:99` `saveMemberPermissions`、`service_facade.go:3012/3109/3242`）全部经由 `runOperation(s, ...)`，并读取 `s.mode`（私有授权模式）。**因此「把成员用例的实现迁到聚合体之外」不能只搬方法体**：必须先把管道从 `*Service` 解耦成显式的协作者集合（决策 26「管道是独立深模块」的本来要求），否则新包要么拿不到私有协作者、要么被迫复制一份聚合体（违反决策 30「管道不引入第二个真相」）。

**这条前置落在段三之外还是之内，需要裁决。** 本文件按「之内」记录为 #268 的子切片 A（管道解耦），并标为 `WARNING`：它改的是段二刚落地的共享核心，段二的 16 项口径变更证据都绑定在它上面。

## 接口契约（先写接口）

> 规则：每个窄口的**方法签名逐字取聚合体现在的方法签名**，不改参数、不改返回值、不改语义（决策 33「方法集合与语义不变」）。窄口只做**收窄**：把聚合体的方法按上下文分桶。

### 拥有模块 = `internal/lingdoc/workspacecore`

| 窄口 | 方法数 | 方法集 | 消费者 |
| --- | --- | --- | --- |
| `ProjectApplication`（迁入并增补） | 13 | 现 11 个 + `ListDiscardedProjects` + `RecordActivationRead` | 主 Handler、④ 全流程路由、⑧ 智能体上下文 |
| `ChapterApplication`（迁入） | 8 | 现有 8 个 | 主 Handler、⑧ 智能体上下文 |
| `ChangeSetApplication`（迁入） | 6 | 现有 6 个 | 主 Handler |
| `TemplateApplication`（迁入） | 6 | 现有 6 个 | 主 Handler |
| `ChangeSetDraftApplication`（新） | 5 | `CreateChangeDraft` `EditChangeDraft` `AssessChangeSet` `ReverseChangeSet` `StabilizeRevision` | ⑥ 影响中心、⑦ G2 路由、⑧ 智能体上下文 |
| `ImpactApplication`（新） | 7 | `GetImpactAssessment` `AcknowledgeImpactPreflight` `ReadImpactComputationReceipt` `ListContextRevisions` `ListRouteNodes` `ListImpactTasks` `ActOnImpactTask` | ⑥、⑤ 守卫 |
| `DependencyApplication`（新） | 6 | `ListDependencyEdges` `CreateDependencyEdge` `DecideDependencyEdge` `ListDependencyCandidates` `DecideDependencyCandidate` `SubmitDependencyCandidateBatch` | ⑥ |
| `RuntimeApplication`（新） | 6 | `ListRuntimeControls` `ControlRuntime` `ListRuntimeEvents` `GetRuntimeMetrics` `ListRuntimeAlerts` `SaveRuntimeAlertRule` | ⑥ |
| `TrialApplication`（新） | 10 | `ListTrialWindows` `CreateTrialWindow` `ActOnTrialWindow` `ListTrialSamples` `RecordTrialSample` `ReviewTrialSample` `GetTrialReport` `ListTrialDecisions` `CreateTrialDecision` `SignTrialDecision` | ⑥ |
| `RoadmapApplication`（新） | 12 | `ReadRoadmap` `ReadRouteEdges` `ReadRouteEdgeVersions` `ReadRoadmapNodeVersions` `ListRoadmapSessions` `GetRoadmapSession` `BeginRoadmapEdit` `CommitRoadmapEdit` `CancelRoadmapEdit` `ProposeRoadmapNode` `ProposeRoadmapEdge` `CompareRoadmapNodeVersions` | ⑥ |
| `ResearchTaskApplication`（新） | 8 | `SaveResearchTask` `ListResearchTasks` `ApproveResearchTask` `ControlResearchTask` `RunResearchTask` `GetResearchRun` `InspectResearchResult` `ListResearchRuns` | ⑦ |
| `ImportCandidateApplication`（新） | 5 | `CreateImportCandidate` `ConfirmImportCandidate` `RejectImportCandidate` `RefreshImportCandidate` `ListImportCandidates` | ⑦ |
| `EvidenceCardApplication`（新） | 4 | `SaveEvidenceCard` `ConfirmEvidenceCard` `ReviewEvidenceCard` `ListEvidenceCards` | ⑦、⑧ |
| `ResearchClaimApplication`（新） | 2 | `SaveResearchClaim` `ListResearchClaims` | ⑦ |
| `SourceAnchorApplication`（新） | 2 | `SaveSourceAnchor` `ListSourceAnchors` | ⑦ |
| `AgentSessionApplication`（新） | 4 | `CreateAgentSession` `ListAgentSessions` `GetAgentTurns` `SendAgentTurn` | ⑦ |
| `ActionProposalApplication`（新） | 4 | `CreateActionProposal` `ConfirmActionProposal` `RejectActionProposal` `ListActionProposals` | ⑦ |
| `AgentContextApplication`（新） | 2 | `BuildAgentContext` `GetProjectAssetPolicy` | ① 治理运行时、⑦ |
| `GovernanceReviewApplication`（新） | 2 | `AuthorizeInTransaction` `ValidationIssueDispositionsInTransaction` | ① |
| `ChapterReviewApplication`（新） | 4 | `GetChapterReview` `ChangeChapterReview` `ListChapterComments` `SaveChapterComment` | ④ |
| `DeliveryGuardApplication`（新） | 3 | `AuthorizeDeliveryExport` `PrepareDeliveryExportNativeBasis` `ValidateDeliveryExportNativeBasisInTransaction` | ⑤ |
| `SourceProvenanceApplication`（新） | 3 | `PrepareProjectProvenance` `PrepareFrozenProjectProvenance` `ValidateFrozenProjectProvenanceInTransaction` | ⑤ |
| `ImpactGuardApplication`（新） | 4 | `AssertRevisionStabilized` `ReadImpactComputationReceipt` `StoreImpactComputationReceipt` `ReadPreflightBasis` | ⑤ |
| `RuntimeGuardApplication`（新） | 3 | `CheckRuntimeOperation` `ObserveRuntime` `ObserveDeliveryRuntime` | ⑤ |

### 拥有模块 = `internal/lingdoc/workspace`（用例自有）

| 窄口 | 方法数 | 方法集 | 消费者 |
| --- | --- | --- | --- |
| `RewriteWorkspaceApplication`（新，声明在 `selected_rewrite.go`） | 4 | `Authorize` `GetProject` `GetWorkingCopy` `ApplyRewriteResult` | ② 选区改写 |
| `ReviewAnchorReadApplication`（已在 `review_anchor_http.go`） | 1 | `GetCurrentConfirmation` | ③ 审阅锚点 |
| `ReviewAnchorWriteApplication`（已在 `review_anchor_http.go`） | 1 | `SetReviewAnchor` | ③ 审阅锚点 |

### 拥有模块 = `internal/lingdoc/membership`（#268 新包）

| 窄口 | 方法数 | 方法集 | 消费者 |
| --- | --- | --- | --- |
| `MembershipApplication`（方法集合不变，迁入） | 4 | `SaveMembersResult` `TransferOwnerResult` `RequestOwnerTransferResult` `AcceptOwnerTransferResult` | 主 Handler |
| `MemberLifecycleApplication`（新） | 11 | `AcceptProjectInvitation` `ChangeMemberState` `EffectivePermission` `InviteProjectMember` `ListMyProjectInvitations` `ListOwnerTransfers` `ListProjectInvitations` `ProjectMemberDirectory` `RejectOwnerTransfer` `SaveProjectAPIKeyGrant` `SetMemberAssignment` | ④ 全流程路由 |

### 组合根的端口束

窄口共 24 个，若逐个注入，组合根的依赖数会从今天的 8 涨到 24——依赖数量是复杂性的成本项。按 Ousterhout「深模块 / 复杂性预算」，**每条面用一个按面组织的端口束**（struct of narrow ports），它不拓宽任何一个口，但把组合根的依赖数控制在每面一个：

```go
// workspace 包，按面组织
type FullWorkflowRoutePorts struct {
    Members    core.MemberLifecycleApplication
    Reviews    core.ChapterReviewApplication
    Projects   core.ProjectApplication
}
type FullWorkflowGuardPorts struct {
    Delivery   core.DeliveryGuardApplication
    Provenance core.SourceProvenanceApplication
    Impact     core.ImpactGuardApplication
    Runtime    core.RuntimeGuardApplication
}
type ImpactRoutePorts struct {
    ChangeSets core.ChangeSetDraftApplication
    Impact     core.ImpactApplication
    Dependency core.DependencyApplication
    Runtime    core.RuntimeApplication
    Trials     core.TrialApplication
    Roadmap    core.RoadmapApplication
}
type G2RoutePorts struct {
    Research       core.ResearchTaskApplication
    Imports        core.ImportCandidateApplication
    EvidenceCards  core.EvidenceCardApplication
    Claims         core.ResearchClaimApplication
    Anchors        core.SourceAnchorApplication
    AgentSessions  core.AgentSessionApplication
    Proposals      core.ActionProposalApplication
    Context        core.AgentContextApplication
}
```

束里每项都是一个**上下文窄口**，束本身不引入新语义。

### #268 的新包接口

```go
// package membership
// Application 是成员与 Owner 上下文的应用口。
// 方法集合与语义与迁移前逐字相同（Spec 决策 33）。
type Application interface {
    SaveMembersResult(context.Context, core.Actor, string, string, core.SaveMembersInput) (core.ProjectMutationResult, error)
    TransferOwnerResult(context.Context, core.Actor, string, string, core.TransferOwnerInput) (core.ProjectMutationResult, error)
    RequestOwnerTransferResult(context.Context, core.Actor, string, string, core.OwnerTransferInput) (core.OwnerTransferResult, error)
    AcceptOwnerTransferResult(context.Context, core.Actor, string, string, string) (core.OwnerTransferResult, error)
}

// Service 是成员与 Owner 上下文的实现。端口、实现与不变量一并迁到本包。
type Service struct { /* 协作者集合 */ }

// 聚合体不再实现该端口：core.Service 上不再有这四个方法。
```

**「编译期断言反向」的实现形式**：Go 的接口是结构化的，`core.Service` 只要方法集对得上就仍然满足 `membership.Application`，所以「不再实现」只能靠**方法集层面**钉住，用仓库既有形态（段二 `workspacecore/g2_gate_declaration_test.go` 的 go/ast 做法）：

- 正向：`var _ membership.Application = (*membership.Service)(nil)`（编译期）
- 反向：go/ast 结构断言 `core.Service` 的方法集中**不含** `SaveMembersResult` / `TransferOwnerResult` / `RequestOwnerTransferResult` / `AcceptOwnerTransferResult`，并做阴性验证（人为加一个同名方法，断言必须失败）。

## 不变量

| 编号 | 不变量 | 守在哪里 |
| --- | --- | --- |
| I-1 | 传输层不得出现聚合体具体类型（`*core.Service` / `workspace.Service` / `core.Service`） | go/ast 结构断言，扫 `internal/lingdoc/workspace` 全部非测试文件 |
| I-2 | 每个窄口的方法集合与语义在迁移前后逐字相同 | 窄口方法集与聚合体方法集逐条对照的 go/ast 断言 + 签名 diff |
| I-3 | 聚合体不得再实现已被切出的上下文的端口 | go/ast 方法集断言（#268） |
| I-4 | 路由树、生产装配面、错误形态表在 base/head 之间逐条相同 | base/head 同树对照（段二同款：`scripts/ci/check_lingdoc_routes.py` + 装配面计数） |
| I-5 | 组合根缺必需端口仍在启动期失败并指名 | 沿用 `workspace/composition_test.go` 形态 |
| I-6 | 组合根不再做多重推断：一个能力只有一个注入位 | `resolvePorts` 的推断分支删除后仍能装配；`application_ports_test.go` 断言上下文口直接来自 `HandlerDependencies` |
| I-7 | 无面消费遗留宽口 | go/ast 断言 `ApplicationService` / `G2Application` / `WorkspaceReadApplication` / `WorkspaceWriteApplication` 在传输层无引用 |

## 验收矩阵

> 每行必须有**当前 head** 的证据；`PASS` / `NOT RUN` / `N/A` / `WARNING` / `PASS (follow-up)` 严格区分。结构断言每条都要**阴性验证**。

| 验收项 | 用户入口 | 不变量 | 成功 | 失败/权限/恢复 | 证据 | 状态 |
| --- | --- | --- | --- | --- | --- | --- |
| 传输层无聚合体具体类型（8 条面） | 全部 HTTP 路由 | I-1 | 路由树与 base 逐条相同 | — | go/ast 断言 + 阴性验证 | TODO |
| 窄口按上下文收窄 | 全部 HTTP 路由 | I-2 | 每个口只含本上下文操作 | 跨上下文调用编译不过 | 口清单对照 | TODO |
| 行为零变化 | 全部 HTTP 路由 | I-4 | base/head 路由树、装配面、错误形态表全等 | — | 对照脚本输出 | TODO |
| 组合根 fail-closed 不回退 | 服务启动 | I-5 | 缺端口仍启动失败并指名 | — | 既有 `composition_test.go` | TODO |
| 组合根单注入位 | 服务启动 | I-6 | 推断分支删除后仍能装配 | — | `application_ports_test.go` | TODO |
| 成员与 Owner 上下文切出 | 成员/Owner 路由 | I-3 | 端口方法集合不变；聚合体不再实现 | 授权拒绝、版本冲突、幂等冲突逐条等价 | 对照测试 + go/ast 反向断言 | TODO |
| 遗留宽口退役 | 全部 HTTP 路由 | I-7 | 三个宽口从传输层消失 | — | go/ast 断言 + 编译通过 | TODO |
| 影响中心与 #246 对齐 | 影响中心路由 | I-4 | 响应形状逐字未变 | G3.7 仍是 `NOT RUN` | base/head 响应形状对照 | TODO |
| 隔离 PostgreSQL 轮 | PG 路径 | — | 单独轻负载跑一轮 | — | 隔离实例 55433 | NOT RUN（环境待安排） |

## 切片 0：窄口契约（先写接口）

**目的**：在任何传输面改动之前，先把 20 个上下文窄口的**接口契约**写下来并钉住。收窄只发生在「哪些方法分到哪个口」，方法本身逐字不动；这条承诺由机器断言守住。

**产出**：

| 文件 | 内容 |
| --- | --- |
| `internal/lingdoc/workspacecore/seg3_context_ports.go` | 20 个窄口的声明（`GovernanceReviewApplication` `AgentContextApplication` `ChangeSetDraftApplication` `ImpactApplication` `DependencyApplication` `RuntimeApplication` `TrialApplication` `RoadmapApplication` `ResearchTaskApplication` `ImportCandidateApplication` `EvidenceCardApplication` `ResearchClaimApplication` `SourceAnchorApplication` `AgentSessionApplication` `ActionProposalApplication` `ChapterReviewApplication` `DeliveryGuardApplication` `SourceProvenanceApplication` `ImpactGuardApplication` `RuntimeGuardApplication`） |
| `internal/lingdoc/workspacecore/seg3_context_ports_test.go` | 结构断言：窄口方法集 = 登记表；窄口方法签名**逐字**等于聚合体同名方法；没有窄口是「改了名的宽口」 |

**生成方式**：窄口签名不是手抄的。脚本从 `workspacecore` 全包解析出 `func (s *Service) X(...)` 的原始文本，按上下文分桶后生成声明文件；人只负责分桶与注释。脚本见 `.scratch/lingdoc-arch-20261021/seg3/gen_context_ports.py`（不入库）。

**阴性验证**（`PASS`，脚本 `.scratch/lingdoc-arch-20261021/seg3/negative_verify_ports.py`，每种扰动后还原文件并复跑绿灯）：

| # | 扰动 | 被谁抓到 | 结果 |
| --- | --- | --- | --- |
| 1 | 窄口里一个方法少一个参数 | 编译期断言 `var _ Port = (*Service)(nil)` | 测试失败 ✅ |
| 2 | 窄口多出一个未登记的方法 | 编译期断言 | 测试失败 ✅ |
| 3 | 登记表少登记一个方法 | go/ast 断言「窄口方法集 = 登记表」 | 测试失败 ✅ |
| 4 | 一个口塞进 10 个调研操作（窄口变宽口） | 编译期断言 | 测试失败 ✅ |
| 5 | 只改聚合体的**形参名**、类型不变，窄口不动 | **只有 go/ast 断言能抓到**（Go 的接口满足只看类型，编译照过） | 测试失败 ✅ |

第 5 项是这条断言的用武之地：签名文本漂移不会让编译失败，但会让窄口变成对聚合体的第二份陈述。

**验证命令与结果**（head = 本切片提交）：

```
go build ./internal/...                                   PASS
gofmt -l internal/lingdoc/workspacecore/                  （无输出）
go test -count=1 -run 'TestEveryNarrowPort|TestTheNarrowPorts' ./internal/lingdoc/workspacecore/
    ok  github.com/Tencent/WeKnora/internal/lingdoc/workspacecore  0.917s
python .scratch/lingdoc-arch-20261021/seg3/negative_verify_ports.py
    5 种扰动全部被断言抓到；还原后 exit 0
```

## 切片顺序

1. **#266**（立范式）：① 治理运行时 + ② 选区改写 + ③ 审阅锚点（③ 只需证明已合规）。
2. **#274**：④ 全流程路由 + ⑤ 全流程守卫。守卫额外子切片：把 `SetG2ActionExecutor` / `SetImpactPreflightChecker` / `SetRuntimeRecoveryChecker` 三个注入动作从守卫移到组合根，守卫不再需要聚合体来装自己。
3. **#267**：⑥ 影响中心路由（含 `roadmap_routes.go` 与 `impact_dependency_candidate_routes.go`），并与 #246 对齐。
4. **#275**：⑦ G2 路由 + ⑧ 智能体上下文（退役 `G2Application` 的最后消费者）。
5. **#268**：新包 `membership`；子切片 A = 管道与聚合体解耦，子切片 B = 端口 + 实现 + 不变量迁出，子切片 C = 聚合体删方法 + 反向断言。
6. **#269**：删 `ApplicationService` / `G2Application` / `WorkspaceReadApplication` / `WorkspaceWriteApplication`；`resolvePorts` 退化为只做必需端口检查；删 `applicationServiceStub` 等测试替身。

## 风险与未决

| 风险 | 影响 | 处置 |
| --- | --- | --- |
| 在途 PR #149 / #248 / #323 改同一批文件 | 段三合入后它们需 rebase | 已裁决：段三照做 + 通知三个兄弟会话（只通知，不要求立即动） |
| #268 需先解耦段二的共享操作管道 | 改的是刚落地的核心，段二的 16 项口径变更证据绑定其上 | 记 `WARNING`；子切片 A 单独提交、单独取证 |
| `*core.Service` 仍有 279 个方法 | 段三只关掉传输层的门，聚合体内部仍是上帝对象 | 属后续段；本段不扩大范围 |
| 隔离 PostgreSQL 轮未跑 | PG 路径无本段证据 | 记 `NOT RUN`，单独一轮轻负载补 |

## 变更记录

- 2026-10-21：开工。记录开工前取证（八条面持有形态与调用集、三个宽口、组合根共享）、四个分叉裁决、Q3 两处修正、24 个窄口清单与端口束形状、7 条不变量、9 行验收矩阵、6 段切片顺序。
