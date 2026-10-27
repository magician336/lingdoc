# 11. 审计失败告警走自己的事务

**状态**：已采纳（2026-10-22，由 Issue [#327](https://github.com/magician336/lingdoc/issues/327)（施工并行面 PF-3）与 Spec [#130](https://github.com/magician336/lingdoc/issues/130) 产生）

**修订关系**：本决策**不修订** [ADR 0010](0010-audit-action-format-and-read-sides.md)（审计动作分类的两种形状）。它决定**审计失败告警的事务归属**、**触发边界**与**接线点**。

## 背景

G5 Spec 要求：「普通读取的审计写入失败采用 fail-open 并告警」（[29-Spec](../06-灵档产品开发规划/29-Spec-G5审计与权限可见性.md):9,32），运维负责人要能看到「临时可观测性丢失」（同文件 :24）。

现状是：读类审计写在**调用方事务内**（`internal/lingdoc/workspacecore/service_facade.go:452` 的 `recordAuditInTransaction`，GORM 分支走 `tx.(auditTransaction).RecordAudit`），失败时记一条日志，调用点用 `_ =` 丢弃错误（`service_facade.go:535,546`、`typed_contracts.go:419`）。告警侧则完全空着——`applyRuntimeAlerts`（`internal/lingdoc/workspacecore/runtime_control.go:471`）的唯一调用方是 `runtime_trace.go:71`，只由运行事件驱动。

于是要回答三个问题：**告警写在哪个事务里**、**哪些审计失败算「普通读」**、**接到告警管线的哪个入口**。

## 决策

### 决策一：告警写在**自己的事务**里，永不写在被报告的那个事务里

业务事务可能正因这次失败而回滚。告警写在它内部，会在最需要它的时候跟着一起消失——失败可观测性在最需要的时候归零，比没有告警更危险。

此外 SQLite 用单连接：业务事务还开着的时候另开事务会死锁，直到上下文超时（这个约束已写在 `service_facade.go:449-451` 的既有注释里）。「同事务写告警」在 SQLite 上是行不通的，不只是不划算。

代价是告警与业务读**不再原子**：读可能成功而告警缺失。这是有意的取舍——可观测性丢失绝不能反过来变成业务失败。

### 决策二：只有**读类审计**的失败发告警；写路径一律不发

区分点在**调用方**，不在底层。`recordAuditInTransaction` 同时服务两种语义：授权通过时（`internal/lingdoc/operationcore/operationcore.go:458-463`）写路径要 fail-closed，失败要让业务一起失败；授权拒绝时（同文件 :442,449）审计失败只记日志。

把这个区分埋进底层函数，等于让底层猜调用方的策略——那是信息泄漏。读类与写类因此各走各的接缝：读类走一个**显式吞掉错误并发布告警**的包装器，写类继续走原来那个返回错误的路径。

关键写审计失败**不**发这类告警：它本身就是一次 500 错误，已经可见；再发一条告警只是噪音，而且会让人误以为「项目还在正常跑，只是审计坏了」。

### 决策三：接线点是 `RecordRuntimeOperationContext`，且**必须显式给出类别**

告警事件复用 G6 的运行事件管线（`RuntimeEvent` → `applyRuntimeAlerts` → `RuntimeAlertRule` 匹配 → `RuntimeAlert`），**不另建告警系统**：`"audit"` 早已在 `validRuntimeEvent` 的类别白名单里（`runtime_control.go:337`），事件与告警立刻能被 `GET /runtime/events` 与 `GET /runtime/alerts` 看见。

但**类别必须显式设置**：`RecordRuntimeOperationContext` 的自动推断分支（`runtime_trace.go:40-60`）只认 changeset / impact_task / stabilization / preflight / snapshot / download / export，**产不出 `"audit"`**。因此接线时用 `WithRuntimeTrace` 显式带上 `Category: "audit"`，让该函数继续负责运行事件的组装与落库，而不是在别处重复实现一份 `RuntimeEvent` 的构造。

**绝不接到 `RecordRuntimeEvent`（reported 来源，`runtime_control.go:358-380`）**：它只写事件记录、**不调用** `applyRuntimeAlerts`，接在那里会让「人工上报的事件」具备触发规则的能力，直接破坏既有守护 `TestG6ReportedEventsCannotTriggerAutomaticRuntimePause`（`workspacecore/impact_g6_extended_test.go:218`）。

### 决策四：告警只带**有界错误码**，不带原始错误文本

运行事件的 `ErrorCode` 要过 `runtimeSafeCode`（`runtime_control.go:334`，`^[a-zA-Z0-9_.:/-]{1,128}$`）。原始错误文本（含空格与中文）过不了这个校验，因此告警里只放固定码（如 `audit_write_failed`），**原始错误留在日志里**。这同时满足「告警不含研究正文与敏感内容」的约束。

## 后果

- **告警事件只能带项目作用域的标识**：规则匹配读的是项目作用域的 `alert_rule`（`runtime_control.go:473`），没有 `project_id` 就匹配不到任何规则。租户级授权失败（如列项目）在生产路径上本就不写审计行，因此不存在这个问题。
- **没有规则就没有告警**：全仓没有任何种子规则，`applyRuntimeAlerts` 在无匹配规则时静默跳过。运维必须显式配置规则（如 `Signal="audit/failed"`），这一点必须写进交付说明，否则「接了线但没告警」会被误读成接线失败。
- **规则若配成 `pause` 或 `block_export`，审计 sink 故障会暂停项目**（`runtime_control.go:486-500`）。这是值班侧的处置选择，不是缺陷；但文档必须警示，建议默认 `notify`。
- **`applyRuntimeAlerts` 的首行是裸类型断言** `tx.(runtimeTransaction)`（`runtime_control.go:472`）。新增调用点必须自己保证事务实现了该端口。
- **告警自身失败只记日志**，绝不阻塞那一次读。
- 本决策的接口契约与不变量见 `docs/08-本轮实施方案/PF-3-审计告警接线与AuditEvent字段.md` §3。
- **相关但独立**：PostgreSQL 上「读类审计失败会让读一并失败」（事务 aborted ⇒ 提交回滚）这个缺口**不在**本决策范围内，另见 [#387](https://github.com/magician336/lingdoc/issues/387)；该缺口已由 [ADR 0015](0015-read-audit-outside-the-read-transaction.md) 处置（读类审计移到业务读事务之外）。
