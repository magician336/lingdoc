# PF-3 开发计划：审计告警接线与 AuditEvent 字段

> 交付单元：施工并行面 PF-3（票 [#327](https://github.com/magician336/lingdoc/issues/327)）｜规格单元：Spec [#130](https://github.com/magician336/lingdoc/issues/130)（G5 审计与权限可见性，即 `docs/06-灵档产品开发规划/29-Spec-G5审计与权限可见性.md`）
> 基线：`origin/main` @ `c4c9ac93`｜方法：Ousterhout《软件设计的哲学》（ch02/ch04/ch05/ch07/ch10/ch11/ch15/ch17）+ `lingdoc-development-gates` 两道门
> 状态：**代码前门已锁定**（2026-10-22 裁决：Q1–Q5 均按推荐）｜实现门未开始

## 0. 一句话

普通读的审计写入失败时，除了记日志，还要**在 G6 告警源里留下一条运行事件**；同时 `AuditEvent` 补上 `target_version` 与 `project_revision` 两个一等字段并真正落库。

## 1. 目的地与范围

### 1.1 用户可见结果

运维负责人在 G6 面板（`GET /runtime/events`、`GET /runtime/alerts`）上能看到「审计写入失败」这件事本身，而不是只在日志里翻。Spec #130 User Story 12。

这条路径是 G6 的前置：路径图缺口簇② 记的是 `#130 → #145 → 卡 G6 整条`（`docs/08-本轮实施方案/G1-G7-Spec-完成路径图.md`），PF-3 是解开这个卡点的在办票。

### 1.2 范围内

| # | 内容 |
|---|---|
| R1 | 读类审计（授权审计、机器读取）写入失败时发布一条 `Category="audit"` 的运行事件，并进入 G6 告警匹配管线 |
| R2 | `AuditEvent` 补 `target_version`（目标版本标识）与 `project_revision`（项目修订号），**独立列**落库，读回可读 |
| R3 | 审计记录与告警内容均不含正文、摘录、文件、密钥 |
| R4 | 关键写仍是 fail-closed，且这一轮**不改**它的任何语义 |

### 1.3 范围外（明确不做）

| # | 不做的事 | 原因 |
|---|---|---|
| O1 | 平台事务外通道（`internal/container/lingdoc.go:231-257` 的 `workspaceAuditSink.Record`，写 `audit_logs`）的失败传播 | 该通道写失败**恒返回 nil**（`:256`），失败连错误都不产生；改它会牵出平台侧责任边界，Spec #130 明说「Reuse the existing workspace audit sink ... do not create a second platform audit system」⇒ 单开 follow-up 票 |
| O2 | `membership` 的「事务后影子审计」（`internal/lingdoc/membership/results.go:316-320,410-414`）告警 | 那是关键写的附属审计，不是「普通读取」；本轮只覆盖 Spec 明写的普通读 |
| O3 | shadow 模式审计（`service_facade.go:369,382`）告警 | 同上，非普通读 |
| O4 | `operationcore` 授权拒绝分支（`internal/lingdoc/operationcore/operationcore.go:442,449`）的 fail-open 告警 | 那是**写操作**被拒时的审计；写路径一律 fail-closed 语义，不发 fail-open 告警（不变量 I5） |
| O5 | 预置任何告警规则 | 规则要填 `responsible_id`（真实责任人），仓里没有可信默认值 ⇒ 预置一条无主规则比不预置更糟 |
| O6 | 补齐 `AuditEvent` 已有的 5 个不落库字段（`TenantID`/`UserID`/`Role`/`Capability`/`Decision`） | 见 §7 U1；本轮不假装它们存在 |
| O7 | 把读类审计移出业务事务（让 PostgreSQL 上普通读真正 fail-open） | 已实测该缺口成立，另开 [#387](https://github.com/magician336/lingdoc/issues/387)；本轮只交付告警接线 |

### 1.4 前置依赖与真实消费者

- **零实现依赖**：不依赖 PF-1/PF-4/PF-5/PF-6 的任何新行为。运行事件与告警管线已在 main 上。
- **真实消费者**：
  - 运维面板：G6 `GET /runtime/events`（`internal/lingdoc/workspace/impact_routes.go:141`）、`GET /runtime/alerts`（`:147`）、`POST /runtime/alert-rules`（`:150`）
  - 审计查询：`GET /projects/:projectId/audit`（`internal/lingdoc/workspace/http.go:145`）

## 2. 事实基线（机器核实，2026-10-22，基线 `c4c9ac93`）

### 2.0 基线前移带来的四处返工（开工前必须重核的实例）

本计划最初写在 `87b3cecc` 上。写完到落库之间，`main` 前移并合入了 Spec #48 命名收口（#373）、PF-1（#325）、PF-2（#357）。逐条重核后：

| 项 | 旧基线 | 新基线 | 处理 |
|---|---|---|---|
| 文件名 | `g6_runtime.go` | `runtime_control.go` | 全部指针改写（Spec-48 是**纯改名**，行号未变） |
| | `g6_trace.go` | `runtime_trace.go` | 同上 |
| | `g5_machine_read.go` | `machine_read_grant.go` | 同上 |
| | `membership/g5_firstpass_test.go` | `membership/first_pass_membership_test.go` | 同上 |
| 迁移编号 | `sqlite/000047`、`versioned/000126` | **已被上游占用**（`000047_lingdoc_candidate_storage_shape` 等） | 改为 **`sqlite/000052`、`versioned/000131`**（两方言偏移量固定为 79） |
| 术语 | 自造「审计与告警」一节 | 上游已有「运行事件」「告警规则」「告警」三词条 | **复用**上游词条，本计划只补审计侧缺口词（见 §3.3） |
| 登记表机制 | `lingdoc_parallel_surfaces.py` | 新增 `lingdoc_parallel_state.py`（命名收口线的状态视图，票 #362–#369） | 两套并行机制并存：PF-3 属于**施工并行面**，只登记前者 |

⇒ **一条纪律**：票面与方案档里凡是「X 在某个文件」这类**关于现状的断言**，都要在开工前按当前 head 重核；改名与编号争用都会让它们过期。

### 2.1 fail-open 落点全清单

| 落点 | 位置 | 生产 GORM 路径是否真写审计 | 本轮是否接告警 |
|---|---|---|---|
| `Service.Authorize` | `workspacecore/service_facade.go:535` | projectID 非空时**是** | ✅ R1 |
| `Service.GetProject` | `workspacecore/service_facade.go:546` | **是**（projectID 必非空） | ✅ R1 |
| `Service.AuthorizeRequest` | `workspacecore/typed_contracts.go:419` | `request.ProjectID` 非空时**是** | ✅ R1 |
| `machine.read` | `workspacecore/machine_read_grant.go:331-349`（`:337` 是丢弃错误那一行） | **是**（独立事务） | ✅ R1 |
| `Service.ListProjects` | `workspacecore/service_facade.go:565,568` | **否** —— `projectID==""` 且 GORM 时 `recordAuditInTransaction` 直接返回 nil（`:458-462`） | ❌ 生产上不存在此落点 |
| shadow 模式 | `workspacecore/service_facade.go:369,382` | 是（但非普通读） | ❌ O3 |
| membership 影子审计 | `membership/results.go:316-320,410-414` | 是（但非普通读） | ❌ O2 |
| operationcore 授权拒绝 | `operationcore/operationcore.go:442,449` | 是（但属写路径） | ❌ O4 |
| operation pipeline | `workspacecore/operation_pipeline.go:235`、`workspacecore/agent_action_service.go:201` | 是（写路径，fail-closed） | ❌ O4 |

> **口径修正**：初版计划把读类落点记成 5 个（含 `ListProjects`）。实测 `recordAuditInTransaction` 在 GORM 分支对空 `projectID` 直接返回 nil（租户级审计没有项目行可外键），所以 `ListProjects` 在生产上**根本不写审计行**，也就不存在审计失败。真实落点是 **4 个**。

### 2.2 告警管线

- 唯一入口 `applyRuntimeAlerts`（`workspacecore/runtime_control.go:471`），**唯一调用方** `workspacecore/runtime_trace.go:71`。
- 匹配式（`runtime_control.go:482`）：`rule.Signal == event.Status` ∨ `rule.Signal == event.Category+"/"+event.Status` ∨ `rule.Signal == event.ErrorCode`。
- `validRuntimeEvent` 的 Category 白名单**已含 `"audit"`**（`runtime_control.go:337`）⇒ 枚举不用扩。
- ⚠️ **但 category 不会被自动推断出来**：`RecordRuntimeOperationContext` 的推断分支（`runtime_trace.go:40-60`）只认 `changeset`/`impact_task`/`stabilization`/`preflight`/`snapshot`/`download`/`export`，**没有 `"audit"`** ⇒ 接线点必须**显式**设置 category，不能指望 operation 名推出来。
- **全仓无任何种子规则** ⇒ 无规则时 `applyRuntimeAlerts` 静默跳过（O5 的直接后果，必须写进交付说明）。
- `applyRuntimeAlerts` 首行是**裸类型断言** `tx.(runtimeTransaction)`（`runtime_control.go:472`）⇒ 新增调用点必须自己保证 tx 实现该端口。
- `RecordRuntimeEvent`（reported 来源，`runtime_control.go:358-380`）**不调用** `applyRuntimeAlerts` ⇒ **绝不接在那里**，否则会破坏既有守护 `TestG6ReportedEventsCannotTriggerAutomaticRuntimePause`（`workspacecore/impact_g6_extended_test.go:218`）。

### 2.3 审计持久化现状

- 表 `lingdoc_project_audits` 只有 7 列：`id, project_id, actor_id, action, target, details_json, created_at`（当前生效定义见 `migrations/sqlite/000046_lingdoc_audit_action_bounds.up.sql:18-26` 与 `migrations/versioned/000106_lingdoc_draft_provenance.up.sql:18-27`）。**无 `tenant_id`/`user_id`/`role`/`decision`/`capability` 列。**
- `AuditEvent` 有 13 个字段，GORM 路径**只落库 7 列**：`TenantID`/`UserID`/`Role`/`Capability`/`Decision` **根本不落库**，`Reason` 被塞进 `details_json` 的 `reason` 键（`workspacecore/service.go:409-424`）。读回同样只还原 7 字段（`service.go:426-443`，构造在 `:440`）⇒ `ListAuditEvents` 的角色过滤实际只靠 `item.ActorID`（`item.UserID` 恒空，过滤段 `service_facade.go:2599-2608`）。
- 无可复用列：`target` 是对象 ID 不是版本；`action` 有 64 字符硬边界（`migrations/*/000046`、`*/000125`）；`details_json` 是唯一无界位置。
- `internal/lingdoc/membership/fixture_test.go:253` 有一份**镜像的 `auditEventRow`**，改列定义时必须同侧同步。
- 历史演进：涉及该表的迁移共 4 组 8 个文件，**从未有过「加列」迁移** ⇒ 本轮是第一次。

### 2.4 既有测试

| 事实 | 位置 |
|---|---|
| **唯一** fail-open 断言，且**不检查任何告警** | `workspacecore/service_facade_test.go:462` `TestAuthorizationAuditFailureDoesNotBlockDecision`（用内存仓库，走 `recordAuditInTransaction` 的非 GORM 分支） |
| 可复用的失败 sink 夹具 | `service_facade_test.go:438-442` `failingAuditSink` |
| 告警规则建法与「上报事件不触发 pause」的边界 | `workspacecore/impact_g6_extended_test.go:218-238` |
| 敏感字段断言**只覆盖正文**，不覆盖摘录/文件/密钥 | `membership/first_pass_membership_test.go:231` `TestFirstPassAuditVisibilityAndNoResearchText` |
| 「审计失败 → 产生告警事件」的测试 | **不存在**，本轮净新增 |
| 全仓 `Test.*Audit` 共 **60** 个，其中 fail-closed 类（业务写与审计一起回滚）已有十余组，可直接引证 | `workspacecore/`、`membership/`、`workspace/`、`rulesetstore/`、`templategovernance/`、`delivery/`、`candidateadoption/` |

## 3. 不可变契约（先写接口）

### 3.1 领域级接口

告警事实归 `runtime_control.go`（运行事件 + `alert_rule` + `alert` 三类记录）；审计事实归 `model.go` 的 `AuditEvent` + `auditEventRow` + 迁移。`service_facade.go` / `machine_read_grant.go` / `typed_contracts.go` 只是**调用方**。

```go
// EmitAuditFailureAlert publishes one operational event for an audit write
// that failed on a fail-open path.
//
// It writes on its own transaction, never the caller's: the caller's
// transaction may be rolled back by the very failure being reported, and an
// alert written inside it would vanish exactly when it is needed.
//
// It never changes an authorization decision and never blocks the read that
// reported it. A failure to write the alert is logged and dropped.
//
// The event carries no research content and no raw error text: only the
// failed audit action, the tenant and project aliases the runtime event model
// already uses, and a bounded error code.
func (s *Service) EmitAuditFailureAlert(ctx context.Context, actor Actor, projectID, auditAction string, cause error) error
```

```go
// readWithAudit runs body in one transaction and records the read's
// authorization decision on a fail-open path: when the audit write fails the
// read still succeeds, and the failure is published as an operational alert
// after the transaction has ended.
//
// It is deliberately not the same seam as recordAuditInTransaction. Write
// paths must fail closed, and a caller that cannot tell the two apart would
// have to re-implement that difference at every call site.
func (s *Service) readWithAudit(ctx context.Context, actor Actor, projectID, capability string, options TransactionOptions, body func(tx Transaction) error) error
```

**接线点（ch08/ch15 的落点决定）**：告警事件走 `RecordRuntimeOperationContext`（`runtime_trace.go:31`），但用 `WithRuntimeTrace` **显式**给出 `Category: "audit"` —— 因为它的自动推断分支（`:40-60`）产不出 `"audit"`。事件字段由该函数统一构造与落库（`runtime_trace.go:61-71`），**不重复实现** `RuntimeEvent` 的组装。

**错误码**：`ErrorCode` 必须匹配 `runtimeSafeCode`（`runtime_control.go:334`，`^[a-zA-Z0-9_.:/-]{1,128}$`），因此**不放原始错误文本**（含空格与中文会违规），只放固定码（如 `audit_write_failed`），原始错误留在日志里。

**实现形状**（S2 落地后回填）：

| 关注点 | 落点 |
|---|---|
| 读类接缝 | `service_facade.go` 新增 `readWithAudit`：包事务 → `body` 做授权判定 → 记审计并**记住**（不返回）审计错误 → 事务结束后发告警 |
| 写类接缝 | 不动 `recordAuditInTransaction`（返回错误）⇒ 关键写仍 fail-closed |
| 告警发射 | `runtime_control.go` 的 `EmitAuditFailureAlert`：独立事务 + `WithRuntimeTrace` 显式 `Category="audit"` + `ErrorCode=audit_write_failed` ⇒ 复用 `RecordRuntimeOperationContext` 落库并触发 `applyRuntimeAlerts` |
| 项目作用域 | 告警事件用 `Project{ID: projectID}`（`applyRuntimeAlerts` 只用 `p.ID`）⇒ 无需再查项目、无需再授权 ⇒ 授权本身失败时也能发告警 |
| 机器读取 | `machine_read_grant.go` 的 `auditMachineRead` 审计失败时发告警（该点本就是独立事务） |

### 3.2 不变量

| | 不变量 | 阴性验证方式 |
|---|---|---|
| I1 | 告警**永不**把 deny 变成 allow | 注入审计失败 + 越权请求 ⇒ 断言仍然拒绝 |
| I2 | 告警**永不**阻塞那一次读；告警自身失败只记日志 | 让运行事件写入失败 ⇒ 断言读仍成功 |
| I3 | 告警写在自己的事务里，不与被报告的事务共享 | 断言告警事件在业务事务回滚后仍然存在 |
| I4 | 告警与审计内容不含正文/摘录/文件/密钥 | sentinel 注入 ⇒ 断言两个出口都查不到 |
| I5 | fail-closed 路径**不**发 fail-open 告警 | 注入关键写审计失败 ⇒ 断言无 `Category=audit` 事件，且业务回滚 |
| I6 | 审计记录追加式、不可变；加字段**不回改**历史行 | 迁移前后行数与旧列值逐字相同 |
| I7 | 审计失败事件的 `Category="audit"`、`Status="failed"`，能被 `Signal="audit/failed"` 的规则命中 | 建规则 ⇒ 断言产生 `RuntimeAlert` |
| I8 | 告警**不**接到上报路径 | 断言 `applyRuntimeAlerts` 的调用点集合不变（守护测试） |

### 3.3 术语（domain-modeling）

**复用**上游已有的「运行事件」「告警规则」「告警」（`GLOSSARY.md` 的「G6 运行观测与试用」一节）——PF-3 不另造平行词汇。

**本轮新增**（审计侧缺口，写入 `GLOSSARY.md` 的「审计与失败策略」一节）：「审计事件」「读类审计」「关键写审计」「审计失败告警」「目标版本」「项目修订号」。

「审计失败告警」的定义必须显式指向既有词条：它是**一条 `Category=audit` 的运行事件及其触发的告警**，不是一个新概念。

## 4. 验收矩阵

| 验收项 | 用户可见入口 | 领域不变量/事务边界 | 成功 | 失败/权限/恢复 | 持久化与并发 | 测试或运行证据 | 状态 |
|---|---|---|---|---|---|---|---|
| 读审计失败 → 运行事件 | `GET /runtime/events` | I1 I2 I3 I7 | 产生 `Category=audit`/`Status=failed` 事件 | 越权请求仍拒绝 | 独立事务，业务回滚后事件仍在 | `audit_failure_alert_test.go`（SQLite）+ `TestAuditFailurePublishesRuntimeEventAndAlertOnPostgres`（PG 实测） | **PASS** |
| 读审计失败 → 告警命中 | `POST /runtime/alert-rules` + `GET /runtime/alerts` | I7 | 建 `Signal=audit/failed` 规则后产生 `RuntimeAlert` | 无规则时不假装有 | 同上 | 同上（规则命中后恰好 1 条告警，Severity/Action 与规则一致；SQLite 与 PG 各一份） | **PASS** |
| 告警内容无敏感字段 | `GET /runtime/events` | I4 | 无正文/摘录/文件/密钥 | —— | —— | 同上（sentinel 断言：正文/摘录/文件/密钥均不出现） | **PASS** |
| 审计内容无敏感字段 | `GET /projects/:id/audit` | I4 | 扩到摘录/文件/密钥（现有断言只覆盖正文） | —— | —— | `audit_event_version_fields_test.go` + `audit_failure_alert_test.go` | **PASS** |
| 关键写仍 fail-closed | ChangeSet apply / 正式导出 / Owner 转让 | I5 | 业务+审计原子成功 | 审计失败 ⇒ 全回滚、无部分版本 | 无重复副作用、可安全重试 | 复用既有十余组故障注入测试（`fault_injection_test.go:83`、`change_set_test.go:1317`、`membership/auth_review_test.go:266`、`workspace/full_workflow_delivery_regression_test.go:389` 等） | **PASS** |
| 不误接上报路径 | —— | I8 | `RecordRuntimeEvent` 不产生告警 | —— | —— | 守护测试 + 复用 `impact_g6_extended_test.go:218` | **PASS** |
| `AuditEvent` 两字段落库 | `GET /projects/:id/audit` | I6 | 写入后读回带值 | 历史行不被回改 | 迁移 + 重启读回 | `audit_event_version_fields_test.go` + 迁移彩排（SQLite 与 PG 各一份） | **PASS** |
| 迁移两方言一致 | —— | I6 | SQLite 与 PostgreSQL 列集合一致 | down 可回退 | 迁移前后逐字比对 | 迁移彩排测试 | **PASS** |
| PG 上普通读真 fail-open | 真实 PG 演练 | I1 | 读成功 | **实测不成立**（已转 [#387](https://github.com/magician336/lingdoc/issues/387)） | 隔离级别 | 隔离实例实测（§5 B1）；PG 端告警用例同时实测到「读失败但告警仍发出」 | **FAIL（已实测）** —— 不在本票范围 |

矩阵行与 `docs/agents/development-rules.md` §一.2 的九列一一对齐。**逐行状态与证据见 [`T327-验证报告.json`](T327-验证报告.json)**：AC-327-01 至 AC-327-14 全部 passed（含五条结构断言的阴性验证与复原）；「PG 上普通读真 fail-open」一行仍为 failed，已转 [#387](https://github.com/magician336/lingdoc/issues/387)，不在本票范围。**PostgreSQL 端的告警注入用例已于 2026-10-23 补齐并通过**（`TestAuditFailurePublishesRuntimeEventAndAlertOnPostgres`）；`go test -race` 全仓本地仍未跑（兄弟会话并行），但 CI 的 PR gate 已在当前 head 上对本次改动的两个包跑了 `go test -race`（`PROVIDERS: success`）。

## 5. 实施切片

每个提交一个可验证目的，文件集互不重叠。

### B0 前置核实（已完成）

**B1 —— PG 上普通读是否真 fail-open：已出结论，「不成立」。**

2026-10-22 在隔离 PostgreSQL 17.11 实例（`postgres:17`，127.0.0.1:55436，一次性合成账号）上实测：

| 层 | 动作 | 结果 |
|---|---|---|
| 第 1 层（纯 PG 语义） | `BEGIN` → 成功语句 → `INSERT INTO no_such_table_xyz`（失败）→ `SELECT 1` → `COMMIT` | `INSERT` 报 `relation "no_such_table_xyz" does not exist`；**同事务内的 `SELECT` 报 `current transaction is aborted, commands ignored until end of transaction block`**；`COMMIT` 变成 **ROLLBACK** |
| 第 2 层（LingDoc 真实行为） | 在 `lingdoc_project_audits` 上装 `BEFORE INSERT` trigger 抛异常，再调 `GetProject` / `Authorize` | 两者**都返回 `commit unexpectedly resulted in rollback`**；去掉 trigger 后立刻恢复正常 |
| 对照面（SQLite） | `TestAuthorizationAuditFailureDoesNotBlockDecision`（`workspacecore/service_facade_test.go:462`） | **PASS**（本轮实跑）——读继续 |

**结论**：读类审计写在**调用方事务内**（`service_facade.go:452`），在 PostgreSQL 上审计 INSERT 失败会让**整个事务** aborted、提交变成回滚 ⇒ **读随之失败**。Spec #130 的「普通读取的审计写入失败采用 fail-open」**只在 SQLite 上成立，在生产数据库上不成立**。

**这个发现不改变 PF-3 的范围**，但改变两件事：

1. **告警接线在 PG 上仍然可行**：`recordAuditInTransaction` 捕获到的是**原始**错误（实测打印 `ERROR: synthetic pf3 audit failure (SQLSTATE P0001)`），所以在它的错误处理里发告警，**在两种方言上都会触发**。
2. **「PG 上读类审计失败不让读失败」是一个独立缺口**，已开 follow-up 票 [#387](https://github.com/magician336/lingdoc/issues/387) 跟踪（§7 U7）。本票**不修**它。

> 顺带纠正一处既有断言：`machine.read` 的 fail-open 在 PG 上**是**成立的——它的审计本来就写在**独立事务**里（`machine_read_grant.go:337`），事务回滚不影响已经完成的读。这正是读类审计应当采用的那个形状。

**B2 —— 迁移编号占用核对**：`sqlite/000052` + `versioned/000131` 当前未被占用（已核实两目录最大编号为 `000051`/`000130`；两方言偏移量固定为 79）。**提交前再核一次**——上游合并会占用编号。

### B1 切片 S1：字段契约与迁移（垂直切片）

| 文件 | 动作 |
|---|---|
| `workspacecore/model.go` | `AuditEvent` 加 `TargetVersion string` / `ProjectRevision int64`（带接口注释）；`auditEventRow` 加 `target_version` / `project_revision` 两列 |
| `workspacecore/service.go` | `RecordAudit` 写入新列；`AuditEvents` 读回新列 |
| `migrations/sqlite/000052_lingdoc_audit_version_fields.{up,down}.sql` | 新增两列 |
| `migrations/versioned/000131_lingdoc_audit_version_fields.{up,down}.sql` | 新增两列 |
| `internal/lingdoc/membership/fixture_test.go` | 镜像 `auditEventRow` 同步加两列 |
| `workspacecore/audit_event_version_fields_test.go`（新） | 落库/读回/迁移彩排/历史行不回改 |

验收：S1 的矩阵行全绿 ⇒ 提交。

### B2 切片 S2：告警接线（垂直切片）

| 文件 | 动作 |
|---|---|
| `workspacecore/runtime_control.go` | `EmitAuditFailureAlert`：显式 `Category="audit"` 的运行事件 ⇒ 独立事务落库 ⇒ 触发 `applyRuntimeAlerts`；告警自身失败只记日志 |
| `workspacecore/service_facade.go` | `readWithAudit` 包装器；`Authorize`/`GetProject` 改走它；`ListProjects` 不改（GORM 下不写审计） |
| `workspacecore/typed_contracts.go` | `AuthorizeRequest` 改走 `readWithAudit` |
| `workspacecore/machine_read_grant.go` | `auditMachineRead` 失败时发告警（该点本就是独立事务） |
| `workspacecore/audit_failure_alert_test.go`（新） | 事件产生、规则命中、I1–I5 与 I8 的阴性验证 |

验收：S2 的矩阵行全绿 ⇒ 提交。

### B3 切片 S3：证据与文档同步

`docs/08-本轮实施方案/T327-验证报告.json`｜`docs/adr/0011-audit-failure-alert-on-its-own-transaction.md`｜`docs/adr/0012-audit-version-fields-are-first-class-columns.md`｜`GLOSSARY.md`｜`CONTEXT.md`｜路径图、登记处与 `docs/08-本轮实施方案/README.md` 索引同步｜票面回写。

### B4 加两列打红的夹具：实测与收口（2026-10-23）

S1 落地后，**本机全仓跑**（123 个包）抓出一个真实回归：**29 条用例因审计表缺两列而失败**。

**CI 没有抓到它**：基线合并那一次 push 的判定层输出 `surface_changed=false / base_moved=true`，仓库级矩阵（`app.yml` 的 `Format, vet, test, and build`）被门禁按「贡献面内容未变」缩面跳过；PR gate 只对本次改动的两个包跑 `-race`。⇒ **「CI 全绿」不等于「仓库被测过」**。

**失败形态很难看**：审计写失败被 HTTP 层映射成不透明的 `500 internal_error`，看不出是 schema 漂移。

**根因是同一类**：夹具各自持有一份审计表的形状。

| 夹具 | 形状来源 | 修法 |
|---|---|---|
| `internal/lingdoc/workspace/{change_set_http,change_set_delivery,delivery_http}_test.go` | **手抄**列清单（7 列） | 改为主动应用生产迁移（新增 `audit_event_table_fixture_test.go`） |
| `internal/lingdoc/workspace/save_chapter_policy_test.go` | 跑迁移子集（止于 000041） | 子集补 `000052` |
| `internal/container/lingdoc_ruleset_delivery_e2e_test.go` | SQLite 子集（止于 000042）+ versioned 子集（止于 000121） | 两份子集分别补 `000052` 与 `000131` |
| `internal/router/lingdoc_integration_test.go` | 两份迁移子集（止于 000041 / 000051） | 两份分别补 `000052` |
| `internal/database/migration_sqlite_versioned_schema_test.go` | 版本常量 | `expectedSQLiteMigrationVersion` 51 → 52 |

后两个文件按登记处「共享维护点」的约定修改，**不登记进任何面的路径集**；其余按实测补进 PF-3 的路径集（20 条，零重叠）。

**复跑结果**：`internal/lingdoc/workspace` ok（196.4s）｜`workspacecore` ok（41.0s）｜`membership` ok（2.6s）｜`internal/database` ok（22.3s）｜`internal/router` ok（1.4s）｜`internal/container` ok（7.8s）。

**留下的机制性缺口**：「测试夹具自持一份生产 schema」这个机制还在——下一次给任何表加列仍会重演。已列入 §10.3 的重构动作（审计表的「模型字段集合 == 表列集合」守护测试 + 夹具默认应用生产迁移）。

## 6. 阻塞边

| 边 | 类型 | 状态 |
|---|---|---|
| PF-3 → #130（Spec G5） | 交付 | 本面交付 |
| PF-3 → #145 / G6 整条 | 交付 | 本面解除（缺口簇②） |
| PF-3 → PF-4 | **路径零重叠** | PF-3 独占 `model.go` 与 `runtime_control.go`，PF-4 的 `member_lifecycle_state.go` 是新文件 ⇒ 登记处自检已验 |
| PF-3 → PF-2 | **路径零重叠** | PF-2 拥有 `workspacecore/human_decision.go` 等，与本面无交集 |
| S1/S2 → PR Ready | 实现门 | 矩阵全绿 + `lingdoc-health-scoring` 复评 |

无「行为未稳定」级的实现依赖。

## 7. 未决事项与风险

| # | 事项 | 处置 |
|---|---|---|
| U1 | `AuditEvent` 已有 5 个字段（`TenantID`/`UserID`/`Role`/`Capability`/`Decision`）不落库，本轮只补 2 个 ⇒ 模型与持久化的不一致**继续存在** | 不在本轮修（O6）；但**必须**写进交付说明与 ADR，避免下一个人以为「审计事件有 decision 和 capability」 |
| U2 | `ListAuditEvents` 的角色过滤实际只靠 `item.ActorID`（`item.UserID` 恒空） | 同 U1 |
| U3 | 全仓无种子告警规则 ⇒ 默认部署下 User Story 12 不成立 | 已在 O5 记录；交付说明必须写「需运维配置规则」，并开 follow-up 票 |
| U4 | 告警规则若配成 `Action=pause`，审计 sink 故障会暂停项目 | 文档警示；建议默认 `notify` |
| U5 | 平台事务外通道（O1）失败不可观测 | 单开 follow-up 票 |
| U6 | `applyRuntimeAlerts` 的裸类型断言（`runtime_control.go:472`） | 新调用点自行保证 tx 实现 `runtimeTransaction`；不顺手改既有函数（本轮范围外） |
| U7 | **PG 上读类审计写入失败会让读一并失败**（已实测，§5 B1） | 已开 follow-up 票 [#387](https://github.com/magician336/lingdoc/issues/387) 跟踪「把读类审计移出业务事务」；PF-3 只做告警接线。I1 不受影响：审计失败不会把 deny 变成 allow。修复方向已由实测指明：`machine.read` 的审计本就写在独立事务里（`machine_read_grant.go:337`），那才是读类审计应当采用的形状 |
| U8 | 迁移编号会被上游合并抢占 | 提交前重核 `000052`/`000131`；被占则改号并**同 PR 改登记处** |
| U9 | 新文件必须过命名棘轮守护（`scripts/ci/test_lingdoc_naming_registry.py`） | 代码层文件名与标识符**不得含编号**；本轮新文件均无编号，文档层保留编号是允许的 |
| U10 | **哪个业务写方填 `target_version` / `project_revision` 尚未定口径** | Spec 只说「审计事件要含这些字段」，没说谁填、填什么。授权审计的 `project_revision` 最自然的来源是项目当前上下文修订号，但授权路径手上只有 projectID，要多查一次项目；`target_version` 的口径随对象而异（章节版本 / 变更集版本 / 授权版本），需逐个写方定。**本票交付的是「字段成为一等公民并可落库可读回」**（port 级往返测试证明），**不替业务方决定填值口径**；另开决策票跟踪 |

## 8. 验收证据的交付口径

- 证据落 `docs/08-本轮实施方案/T327-验证报告.json`，**不贴 issue**（票面要求）。
- 每条矩阵行必须绑**当前 head**，并标 `PASS` / `PASS (follow-up)` / `N/A` / `WARNING` / `NOT RUN` / `BLOCKED`；不得用旧 head 的证据。
- `go test -race ./...`、隔离 PostgreSQL 演练若因本机环境未跑，必须标 `NOT RUN` 并写清事实、影响与解除条件。
- 报「CI 全绿」必须逐 job 看 step 的 conclusion，并说清覆盖的是 PR 层还是 main push 层。

## 9. 健康度评分（Spec 完成时点）

按 `docs/项目健康度评分协议.md` 执行。**取样范围 = 审计与告警子域（PF-3 面的路径 + 审计/告警管线 + 相关测试 + 本计划与 ADR）**，**不是全仓**，因此与协议的「当前暂定基线」（`H_L` 66 / `F_R` 69）**不可直接比较**。

- Git SHA：`c4c9ac93`（基线；本轮计划文档尚未提交）
- Spec：#130 / `docs/06-灵档产品开发规划/29-Spec-G5审计与权限可见性.md`
- PR：无（代码前门阶段）
- 阶段：**Spec 完成**（代码证据按协议标记为待验证）

### 9.1 `H_L` = 56.0 / 100，置信度 **B**

| 维度 | 二级指标 | 分 | 证据 | 改进动作 |
|---|---|---:|---|---|
| 复杂性控制 | 变化放大 | 2 | `AuditEvent` 的同一形状散在 5 处（`model.go:114-128`、`model.go:516-524`、`service.go:423`、`service.go:440`、`membership/fixture_test.go:253`），加字段要同步 5 处，且**已经错过一次**（5 个字段没落库） | 模型与表的列集合一致性守护测试；把不落库的字段从结构体上删掉而不是留着 |
| | 认知负荷 | 2 | fail-open / fail-closed 由「调用方吞不吞错误」表达（`operationcore.go:442` 吞、`:458` 返），读者必须逐调用点推断 | 拆成两个显式接缝，策略写在名字里 |
| | 特例与信息泄漏 | 2 | 租户级审计不落库（`service_facade.go:458-462`）、平台 sink 写失败恒返回 nil（`container/lingdoc.go:256`）两条特例都没有契约声明 | 把两条特例写进术语与接口注释 |
| 模块深度与边界 | 接口成本/隐藏收益 | 3 | `applyRuntimeAlerts` 是深模块（隐藏规则匹配 + alert 落库 + pause 副作用），但要 `tx`+`actor`+`Project` 三参才调得动 | — |
| | 信息隐藏 | 2 | 「什么该进审计」靠各调用点白名单逐字构造 details，无集中脱敏机制，且守护只覆盖正文 | 抽集中脱敏器 + sentinel 守护覆盖摘录/文件/密钥 |
| | 层次错位 | 3 | `operationcore` 知道 audit 的失败语义，审计策略细节却在 workspacecore 的 service 层 | — |
| | 耦合与透传 | 3 | `recordAuditInTransaction` 5 个位置参数 + 3 个分支 | — |
| 演化式设计 | 结构不同的候选方案 | 4 | 本轮两组候选（独立事务 vs 同事务、独立列 vs details）均有记录（ADR 0011/0012） | — |
| | 增量与回退 | 4 | 迁移有 down；既有迁移有彩排测试 | — |
| | 持续重构 | 2 | 声明/持久化不一致存在已久且未被收口 | 同「变化放大」 |
| 战略执行 | 设计投资与债务偿还 | 3 | 本轮只补 2 个字段、不补那 5 个，等于在同一处债务上加一笔；ADR 0012 已记账 | — |
| | 路线与依赖一致性 | 4 | 路径图 / 登记处 / 票面三处已同步 | — |
| | 变更局部性 | 4 | 路径集补齐后与其余五面零重叠（登记处自检通过） | — |
| 一致性与显然性 | 领域术语/文档 | 3 | GLOSSARY 原缺审计侧词条，本轮已补，并**复用**上游的运行事件/告警词条；仓库级术语表 `16-术语表.md` 未同步 | 同步 `16-术语表.md` |
| | 契约与非显然注释 | 3 | SQLite 死锁注释到位；fail-open/fail-closed 的语义无注释说明 | — |
| | 代码语义一致性 | 2 | 声明 13 字段、落库 7 列 | 同「变化放大」 |
| | 自动化约束 | 5 | 登记处自检 + `scripts/ci` 守护用例；路径集重叠直接拒绝加载；命名棘轮守护 | — |
| 证据、反馈与恢复 | 未知发现 | 2 | fail-open 落点无清单也无守护测试，「哪些落点在生产上真写审计」要靠人工核实才发现 | 落点清单 + 守护测试 |
| | 失败/权限/恢复测试 | 3 | fail-closed 有十余组故障注入测试；fail-open 只有 1 个且不检查告警 | 本轮补告警断言 |
| | CI/观测/回退 | 2 | G6 告警管线存在但**无种子规则**，默认部署观测不到 | 预置策略待定（O5） |

维度得分：复杂性控制 8.0｜模块深度与边界 10.8｜演化式设计 10.0｜战略执行 10.8｜一致性与显然性 9.4｜证据反馈恢复 7.0 ⇒ **56.0**

健康等级：**40–59「脆弱，应先治理再扩展」**（对本子域而言）。

### 9.2 `F_R` = 61.6 / 100，置信度 **B**

| 维度 | 二级指标 | 分 | 证据 |
|---|---|---:|---|
| 复杂性控制 | 变化放大 / 认知负荷 / 特例与信息泄漏 | 4 / 4 / 2 | 路径集零重叠 + 登记处机器校验；平台 sink 吞错是**跨仓库边界**的特例，workspacecore 控制不了 |
| 模块深度与边界 | 接口成本/隐藏收益 / 信息隐藏 / 层次错位 / 耦合与透传 | 2 / 2 / 3 / 3 | `AuditSink` 端口契约说「返回错误」，实现却吞掉 ⇒ **契约与实现语义不符**；宿主层把 details 重新合并一遍（`container/lingdoc.go:235-238`）；`audit_logs` 与 `lingdoc_project_audits` 两套审计存储 |
| 演化式设计 | 候选方案 / 增量回退 / 持续重构 | 4 / 4 / 2 | 同 H_L |
| 战略执行 | 设计投资 / 路线一致性 / 变更局部性 | 3 / 5 / 4 | 路径图、登记处、票面三处一致 |
| 一致性与显然性 | 术语文档 / 非显然注释 / 代码语义 / 自动化约束 | 3 / 3 / 2 / 5 | 仓库级术语表未同步 |
| 证据反馈恢复 | 未知发现 / 失败权限恢复测试 / CI 观测回退 | 2 / 3 / 2 | 同 H_L |

维度得分：14.0｜9.4｜10.0｜11.8｜9.4｜7.0 ⇒ **61.6**

健康等级：**60–79「可持续开发，但存在明确治理债务」**。

### 9.3 阻断风险与最高优先级重构动作

**前置事实已定（不再是未知）**：PG 上普通读是否真 fail-open —— **已实测，不成立**（§5 B1）。结论：**不构成 PF-3 的实现门阻断**，因为 PF-3 交付的是告警接线，而 `recordAuditInTransaction` 捕获的是原始错误，告警在两种方言上都会触发。「PG 上读类审计失败不让读失败」是**另一个缺口**，已单开 follow-up 票（§7 U7）。

**本票真正的实现门阻断项**仍然是矩阵里的 `TODO` 行：S1/S2 的矩阵行未闭环 ⇒ PR 保持 Draft。

**最低分二级指标均为 2/5，未低于 2/5**，按协议不构成独立的维度阻断项；但多项逼近阻断线，且集中在同一个根因：**「同一件事有两个拥有者」**——审计事件的形状有 5 个拥有者，审计的失败语义有 2 个拥有者。

按「复杂性降低收益」排序的重构动作：

1. `workspacecore` 审计写缝 → **结构问题**：同一个函数靠调用方丢弃错误来表达两套语义 → **动作**：拆成 `readWithAudit`（吞 + 发告警）与 `recordAuditInTransaction`（返错），策略写进名字 → **预期减少**：新增审计落点不必重新推断该吞还是该返 → **回归门槛**：新增 fail-open 落点必须有告警断言测试
2. `AuditEvent` 模型 → **结构问题**：声明 13 字段、落库 7 列，加字段要同步 5 处 → **动作**：全部落库，或把不落库的字段从结构体删掉（降级为 details 里的显式键） → **预期减少**：加字段从「同步 5 处」降到「同步 1 处」 → **回归门槛**：模型字段集合与表列集合的一致性守护测试
3. `container/lingdoc.go` 平台 sink → **结构问题**：端口契约说「返回错误」，实现吞掉，契约与实现不符 → **动作**：实现返回错误，由调用方按 fail-open/fail-closed 处置 → **预期减少**：平台侧审计失败不再静默（本轮 O1 未做，需另票） → **回归门槛**：平台 sink 失败的可观测性测试
4. 审计脱敏 → **结构问题**：白名单逐字构造、无集中机制、守护只覆盖正文 → **动作**：抽集中脱敏器，sentinel 守护覆盖正文/摘录/文件/密钥 → **预期减少**：新增审计字段时不必重新判断「会不会泄露」 → **回归门槛**：sentinel 守护测试

### 9.4 本轮验证记录（PR 阶段）

| 命令 | 结果 |
|---|---|
| `python scripts/lingdoc_parallel_surfaces.py` | **PASS** — `6 parallel work faces validated`（PF-3 按实测补到 20 条路径后重跑） |
| `python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py'` | **PASS** — `Ran 322 tests ... OK`；命名棘轮自检 `0/0/0`（本轮新增文件未引入编号） |
| 文档行号指针逐条核对（grep + read） | **PASS** — 在 `c4c9ac93` 上复核：Spec-48 是纯改名，**行号全部未变**；已改写文件名指针 |
| 迁移编号占用核对 | **PASS** — sqlite 下一个 `000052`、versioned 下一个 `000131`（偏移量 79） |
| `go test -p 1 -count=1 ./internal/lingdoc/workspacecore/ ./internal/lingdoc/membership/` | **PASS** — 两包全绿（41.0s / 2.6s，workspacecore 一轮带 PostgreSQL 方言） |
| `internal/lingdoc/workspace` / `internal/database` / `internal/router` / `internal/container` | **PASS** — 196.4s / 22.3s / 1.4s / 7.8s（夹具回归修完后的复跑） |
| PostgreSQL 隔离实例上的既有 PG 用例 | **PASS** — `TestPostgres*` 与 `TestPR150Postgres*` 全绿（127.0.0.1:55437，postgres:17） |
| **PostgreSQL 端「审计失败 → 告警」端到端** | **PASS** — `TestAuditFailurePublishesRuntimeEventAndAlertOnPostgres`（2026-10-23 补齐）：PL/pgSQL trigger 注入失败，仍产出 `Category=audit` 事件并命中规则；同时实测到「读失败但告警仍发出」 |
| PostgreSQL 上普通读 fail-open 核实 | **FAIL（已实测）** — `GetProject` / `Authorize` 均返回 `commit unexpectedly resulted in rollback`；详见 §5 B1；缺口转 [#387](https://github.com/magician336/lingdoc/issues/387) |
| `golangci-lint run --new-from-rev=origin/main`（六个改动包） | **PASS** — `0 issues`（首轮 9 issues 已逐条修掉） |
| SQLite fail-open 定向测试 | **PASS** — `TestAuthorizationAuditFailureDoesNotBlockDecision` |
| 五条结构断言的阴性验证 | **PASS** — 去掉落库 / 迁移回填 / 去掉显式类别 / 让审计错误返回 / 去掉 PG 失败注入，各自主张变红并复原 |
| **加列打红的夹具回归** | **PASS（修复后）** — 本机全仓跑出 29 条失败（6 处夹具：3 处手抄列清单、3 处跑迁移子集），逐处修完复跑全绿；详见 §5 B4 |
| **本机全仓 Go 套件（123 包）** | **PASS（改动面全绿）** — `go vet` 全过；`go test` 仅剩 12 条与本票无关的既有 Windows 失败（`internal/agent/tools` 6 条 + `internal/application/service` 6 条），在 `origin/main` 基线副本上**逐条原样复现** |
| **CI 逐 check 结论** | **PASS** — 必需检查 `LingDoc PR gate` 实跑，内部对本次改动的两个包执行 `go test -race`（`PROVIDERS: success`）；仓库级矩阵在基线合并那次 push 上被门禁缩面跳过（`surface_changed=false`），本轮改到 Go 面后重新参与 |
| `go test -race ./...`（全仓本地） | **NOT RUN** — 本机有兄弟会话并行，资源会互相干扰；这一层由 CI 的 PR gate 在 Linux 上覆盖到改动面；解除条件见验证报告 |

未覆盖风险：PG 行为已实测且为负（已转票）；无种子规则的实际观测效果未验证；`AuditEvent` 既有 5 个不落库字段导致的读侧角色过滤缺口未修；**夹具自持一份 schema 形状的机制仍在**（本轮修掉 6 处，但下一次加列仍会重演）。

## 10. PR 合并前复评（PR 阶段健康度评分）

按 `docs/项目健康度评分协议.md` 的第二个评分时点执行。**取样范围与 §9 相同（审计与告警子域）**，因此与 §9 可直接比较；与协议的「当前暂定基线」（`H_L` 66 / `F_R` 69）仍**不可直接比较**。

- Git SHA：见 [`T327-验证报告.json`](T327-验证报告.json) 的 `head`（证据产出的那棵树）
- Spec：#130 / `docs/06-灵档产品开发规划/29-Spec-G5审计与权限可见性.md`
- PR：[#389](https://github.com/magician336/lingdoc/pull/389)
- 阶段：**PR 合并前**

### 10.1 `H_L` = 64.4 / 100，置信度 **B**（Δ = **+8.4**）

| 维度 | 二级指标 | §9 | 本次 | 证据 | 改进动作 |
|---|---|---:|---:|---|---|
| 复杂性控制 | 变化放大 | 2 | 2 | `AuditEvent` 的同一形状仍散在 5 处（`model.go:126`、`model.go:530`、`service.go:409`、`service.go:431`、`internal/lingdoc/membership/fixture_test.go`），加字段仍要同步多处；本轮还实测出**第六处重复**：`internal/lingdoc/workspace/` 的三个夹具手抄了审计表列清单，加两列后审计写失败，而失败被 HTTP 层转成 500 ⇒ 同一份 schema 知识在多处各写一遍 | 已把这三个夹具改为主动应用生产迁移（`internal/lingdoc/workspace/audit_event_table_fixture_test.go`）；剩余的模型↔表不一致仍要守护测试 |
| | 认知负荷 | 2 | **4** | fail-open / fail-closed 已拆成两个显式接缝：`readWithAudit`（吞 + 发告警，`service_facade.go:505`）与 `recordAuditInTransaction`（返错），策略写进名字；四个读类落点都走前者 | — |
| | 特例与信息泄漏 | 2 | **3** | 两条特例（租户级审计在 GORM 上不写、平台 sink 写失败恒返回 nil）已写进术语、ADR 0011 与票面边界；平台 sink 的「契约说返错、实现吞掉」仍未修（O1） | 平台 sink 另开票 |
| 模块深度与边界 | 接口成本/隐藏收益 | 3 | 3 | `applyRuntimeAlerts` 仍是深模块；`EmitAuditFailureAlert`（`runtime_control.go:533`）把「发一条审计失败告警」压成一个方法，但签名 5 参 | — |
| | 信息隐藏 | 2 | **3** | 告警路径已有显式的「只带固定错误码、不带正文/摘录/文件/密钥」边界并有 sentinel 断言；**审计 details 的集中脱敏器仍未抽** | 抽集中脱敏器 + sentinel 覆盖四类敏感内容 |
| | 层次错位 | 3 | 3 | `operationcore` 仍知道审计的失败语义 | — |
| | 耦合与透传 | 3 | 3 | `recordAuditInTransaction` 仍是 5 个位置参数 | — |
| 演化式设计 | 结构不同的候选方案 | 4 | 4 | 两组候选均有记录（ADR 0011/0012） | — |
| | 增量与回退 | 4 | 4 | 两方言迁移都有 down，且有迁移彩排 | — |
| | 持续重构 | 2 | 2 | 声明 15 字段、落库 9 列，同源问题未收口 | 同「变化放大」 |
| 战略执行 | 设计投资与债务偿还 | 3 | 3 | 本轮补 2 个字段、不补那 5 个，债务上又加一笔（ADR 0012 已记账） | — |
| | 路线与依赖一致性 | 4 | 4 | 路径图 / 登记处 / 票面三处已同步；登记处已补 4 条实测路径 | — |
| | 变更局部性 | 4 | 4 | 路径集补齐后与其余五面零重叠（登记处自检通过） | — |
| 一致性与显然性 | 领域术语/文档 | 3 | 3 | GLOSSARY 已补审计侧一节并复用上游词条；仓库级术语表 `16-术语表.md` 仍未同步 | 同步 `16-术语表.md` |
| | 契约与非显然注释 | 3 | **4** | `readWithAudit` 的注释直接写明「为什么不是 `recordAuditInTransaction`」；ADR 0011 记下了「告警必须走自己的事务」的推理 | — |
| | 代码语义一致性 | 2 | **3** | 新增字段已跑通「声明→列→写→读回→两方言迁移」整条链并有守护用例；但仍有 5 个字段不落库 | 同「变化放大」 |
| | 自动化约束 | 5 | 5 | 登记处自检 + `scripts/ci` 守护用例 + 命名棘轮守护；夹具回归由夹具改造堵住 | — |
| 证据、反馈与恢复 | 未知发现 | 2 | **3** | 四个读类落点已列清并各有告警断言；但落点清单仍是文档事实，没有机器守护（新增落点可能漏接告警） | 落点清单守护测试 |
| | 失败/权限/恢复测试 | 3 | **4** | SQLite 四条 + PostgreSQL 一条端到端 + 五条阴性验证 + 既有十余组 fail-closed 故障注入 | — |
| | CI/观测/回退 | 2 | 2 | G6 告警管线存在但**无种子规则**，默认部署观测不到（O5 刻意取舍） | 规则由运维配 |

维度得分：复杂性控制 11.8｜模块深度与边界 12.0｜演化式设计 10.0｜战略执行 10.8｜一致性与显然性 10.8｜证据反馈恢复 9.0 ⇒ **64.4**

健康等级：**60–79「可持续开发，但存在明确治理债务」**（对本子域而言；§9 为 40–59「脆弱」，本轮跨过该线但债务仍在）。

### 10.2 `F_R` = 65.0 / 100，置信度 **B**（Δ = **+3.4**）

| 维度 | 二级指标 | §9 | 本次 | 证据 |
|---|---|---:|---:|---|
| 复杂性控制 | 变化放大 / 认知负荷 / 特例与信息泄漏 | 4 / 4 / 2 | 4 / 4 / 2 | 路径集零重叠 + 登记处机器校验；平台 sink 吞错是**跨仓库边界**的特例，workspacecore 控制不了（O1 未做） |
| 模块深度与边界 | 接口成本/隐藏收益 / 信息隐藏 / 层次错位 / 耦合与透传 | 2 / 2 / 3 / 3 | 2 / 2 / 3 / 3 | `AuditSink` 端口契约说「返回错误」、实现却吞掉 ⇒ **契约与实现语义不符**；宿主层把 details 重新合并一遍；`audit_logs` 与 `lingdoc_project_audits` 两套审计存储 |
| 演化式设计 | 候选方案 / 增量回退 / 持续重构 | 4 / 4 / 2 | 4 / 4 / 2 | 同 H_L |
| 战略执行 | 设计投资 / 路线一致性 / 变更局部性 | 3 / 5 / 4 | 3 / 5 / 4 | 路径图、登记处、票面三处一致 |
| 一致性与显然性 | 术语文档 / 非显然注释 / 代码语义 / 自动化约束 | 3 / 3 / 2 / 5 | 3 / 4 / 3 / 5 | 仓库级术语表仍未同步 |
| 证据反馈恢复 | 未知发现 / 失败权限恢复测试 / CI观测回退 | 2 / 3 / 2 | 3 / 4 / 2 | 同 H_L |

维度得分：14.0｜9.4｜10.0｜11.8｜10.8｜9.0 ⇒ **65.0**

健康等级：**60–79「可持续开发，但存在明确治理债务」**。

### 10.3 阻断风险与最高优先级重构动作

**没有二级指标低于 2/5** ⇒ 按协议不构成独立的维度阻断项。**PR 合并前的阻断项是矩阵与证据**，不是分数：矩阵全部闭环（§4）、PG 端用例已补（AC-327-14）、合并前复评已完成（本节）。

按「复杂性降低收益」排序的重构动作（沿用 §9.3 的编号，标注本轮进展）：

1. `workspacecore` 审计写缝 → **结构问题**：同一个函数靠调用方丢弃错误来表达两套语义 → **动作**：拆成 `readWithAudit`（吞 + 发告警）与 `recordAuditInTransaction`（返错） → **本轮已完成** → **预期减少**：新增审计落点不必重新推断该吞还是该返 → **回归门槛**：新增 fail-open 落点必须有告警断言测试
2. `AuditEvent` 模型 → **结构问题**：声明 15 字段、落库 9 列，加字段要同步 5 处；**且表的形状在测试夹具里还有第 6 处手抄副本** → **动作**：模型与表列集合的一致性守护测试 + 把不落库的字段从结构体删掉（降级为 details 里的显式键） → **本轮只堵住夹具这一处**（改为主动应用生产迁移） → **预期减少**：加字段从「同步 5 处」降到「同步 1 处」 → **回归门槛**：模型字段集合与表列集合的一致性守护测试
3. `container/lingdoc.go` 平台 sink → **结构问题**：端口契约说「返回错误」，实现吞掉 → **动作**：实现返回错误，由调用方按 fail-open/fail-closed 处置 → **本轮未做（O1，需另票）** → **预期减少**：平台侧审计失败不再静默 → **回归门槛**：平台 sink 失败的可观测性测试
4. 审计脱敏 → **结构问题**：白名单逐字构造、无集中机制 → **动作**：抽集中脱敏器，sentinel 守护覆盖正文/摘录/文件/密钥 → **本轮已把 sentinel 扩到告警与审计两侧，但脱敏器未抽** → **预期减少**：新增审计字段时不必重新判断「会不会泄露」 → **回归门槛**：sentinel 守护测试
5. **（本轮新增）读类落点清单** → **结构问题**：哪些落点会写审计、哪些是 fail-open，现在只有文档事实，没有机器约束；新增落点可能漏接告警 → **动作**：把落点清单做成守护测试（枚举读类落点并断言其走 `readWithAudit`） → **预期减少**：漏接告警的失败从「上线后没人发现」变成「提交即红」 → **回归门槛**：守护测试本身进 `scripts/ci` 套件
6. **（本轮新增）PG 上读类审计写在业务事务内** → **结构问题**：审计 INSERT 失败会让整个事务 aborted ⇒ 读失败（[#387](https://github.com/magician336/lingdoc/issues/387)） → **动作**：把可降级的读类审计写移出业务事务 → **预期减少**：「普通读 fail-open」在生产方言上才真正成立 → **回归门槛**：隔离 PG 上「审计失败时读仍成功」的用例

### 10.4 本轮复评的证据与环境限制

| 项 | 结果 |
|---|---|
| 仓库级 Go 套件（本机全仓 123 包） | **PASS（除 12 条与本票无关的既有 Windows 失败）** —— 12 条全在 `internal/agent/tools`（6 条：MCP schema 的 `file://D:\` URI、读文件沙箱、shell stdin、skill 的可执行位与 pip/uv/ensurepip 恢复路径）与 `internal/application/service`（6 条：SQLite 临时目录被占用、skill 同类），在 `origin/main` 基线副本上**逐条原样复现**；本分支未触碰这两个包，它们也不 import 本次改动的包 |
| 登记处 | **PASS** —— `6 parallel work faces validated`（PF-3 补到 18 条路径后与其余五面零重叠） |
| 命名棘轮 | **PASS** —— `0/0/0` |
| `go test -race`（全仓本地） | **NOT RUN** —— 兄弟会话并行；但 CI 的 PR gate 已在 head 上对本次改动的两个包跑了 `go test -race`（`PROVIDERS: success`） |

**未覆盖风险**：本机全仓跑出的 5 条 Windows 失败说明「本地全仓绿」在 Windows 上不可得（CI 在 ubuntu 上跑同一套）；PG 端的普通读 fail-open 仍不成立（#387）；无种子规则的实际观测效果未验证；`AuditEvent` 既有 5 个不落库字段导致的读侧角色过滤缺口未修；CodeGraph 索引本轮未使用（工具不可用），取样以 `grep`/`read` 与可复现运行为准。
