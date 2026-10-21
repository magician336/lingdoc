# 10. 审计动作分类的两种形状：写侧只写新格式，读侧按动作种类匹配

**状态**：已采纳（2026-10-22，由 Issue [#276](https://github.com/magician336/lingdoc/issues/276) 与 [#282](https://github.com/magician336/lingdoc/issues/282) 产生；两票是 [#253](https://github.com/magician336/lingdoc/issues/253) 的遗留项）

**修订关系**：本决策**不修订** [ADR 0003](0003-impact-task-severity-and-disposition.md)（影响任务严重度与处置）、[ADR 0008](0008-writing-action-vocabulary-and-adoption-model.md)（写作动作词表）。它只决定**审计动作列的形状契约**与**读侧匹配规则**。

## 背景

`lingdoc_project_audits.action` 是**分类列**：它写「发生了什么」，不写「发生在哪个对象上」。分类列在两种方言上都有边界——PostgreSQL 侧一直是 `varchar(64)`。

历史里存在**两种形状**：

| 形状 | 例子 | 长度 | 何时写入 |
| --- | --- | --- | --- |
| 旧形状 | `authorization.issue-dismiss-blocking:<uuid>` | 73 | #253 修复**之前**的写入方（`aa9bc394` 的父提交）把完整能力写进了分类列 |
| 新形状 | `authorization.issue-dismiss-blocking` | 32 | #253 之后（`internal/lingdoc/workspacecore/service_facade.go:424-427` 的 `auditAuthorizationAction`） |

#253 修掉的是「旧形状把资源写进分类列」这件事。旧形状的最坏情况是 73 字符：**PostgreSQL 的 `varchar(64)` 拒写**（SQLSTATE 22001），于是一个合法的处置请求得到未映射的 500。SQLite 侧当时是无界 `TEXT`，同样的写入被静静接受——**缺陷在开发机上完全看不见**。这是 [#276](https://github.com/magician336/lingdoc/issues/276) 的由来。

形状变了之后，「哪些读侧会漏读旧形状的行」就成了一个必须回答的问题，而不是一句「已知无影响」。

## 事实一：写侧只有一个点产出授权动作

| 写点 | 产出 | 带资源吗 |
| --- | --- | --- |
| `internal/lingdoc/workspacecore/service_facade.go:468` | `auditAuthorizationAction(capability)` = `"authorization." + 种类` | **不带**（种类最长 36 字符） |
| `internal/lingdoc/candidateadoption/candidate_adoption_store.go:287` | `project.change_set.review`（字面量） | 不带 |

其余动作族（`project.*`、`project_spec.*`、`member.*`、`validation_issue.*`、`semantic.*`、`impact_task.*`、`dependency.*`、`roadmap.*`、`trial.*`、`g2.*`、`workspace.*`、`machine.read`、`delivery.export`、`preflight.compute` 等）都是字面量，或「前缀 + 枚举受限的片段」——片段取值由入参校验限定（`internal/lingdoc/workspacecore/impact_service.go:31-33` 的 `validImpactAction` 等），因此都不带资源、都在 64 字符内。

**结论**：仓里没有第二个能写出「动作 + 资源」的写点。旧形状是历史，不是现状。

## 事实二：读侧只有一条查询，而且不按动作过滤

| 读点 | 过滤条件 | action 处理 |
| --- | --- | --- |
| `internal/lingdoc/workspacecore/service.go:426-443` `gormTransaction.AuditEvents` | 只按 `project_id` 过滤（`:428`） | **原样透传**（`:440`） |
| `internal/lingdoc/workspacecore/service_facade.go:2588-2613` `ListAuditEvents` | 委托上一者；只按 actor 过滤（非 owner/admin，`:2601-2609`） | 原样透传 |

- HTTP 出口：`GET /projects/:projectId/audit`（`internal/lingdoc/workspace/http.go:144` 注册，`:421-432` 处理）。
- **导出路径不读这张表**：影响任务导出、证据导出、机器可读视图都不消费它。`AuditSink.Record` 那条通道写的是另一张表 `audit_logs`（`internal/container/lingdoc.go:211-239`）。
- 仓内唯一按动作串匹配的**生产**逻辑有两处：`service_facade.go:2757`（重复处置检查）与 `:2904`（`latestValidationIssueDispositions`）。两者匹配的都是 `validation_issue.disposition`——一个**从不带资源**的动作族，因此与形状变化无关。

## 决策

### 决策一：读侧按**动作种类**匹配，不按完整动作串匹配

**动作种类** = 动作串第一个 `:` 之前的部分。

- 「`action == "authorization.issue-dismiss-blocking"`」这种等值匹配会漏掉旧形状的行。
- 「`action LIKE "authorization.issue-dismiss-blocking%"`」或「先取种类再比较」不会漏。
- 本决策**不要求改生产代码**：事实二表明生产读侧没有按授权族做等值匹配，两处等值匹配属于另一个动作族。它把「已知无影响」升级成**有保证**：守护测试 `internal/lingdoc/workspacecore/audit_action_format_test.go` 钉住「两种形状的行都从读侧回来，且 action 原样透传」，任何新增的**按完整动作串过滤授权族**的读侧都会让这条守护失败。

### 决策二：写侧只写新形状

已由 #253 落地。守护：`internal/lingdoc/workspacecore/audit_action_width_test.go`（宽度与「不带资源」）与本轮的 `TestAuditWriterEmitsTheCurrentShapeOnly`（形状与「资源留在 details」）。

### 决策三：历史行保留，不改写

- 旧形状行**保留**。完整能力一直在 `details_json.capability` 里（旧写入方也写了），所以分类列的形状不是唯一的事实来源。
- **超过 64 字符的旧形状行**（`authorization.<种类>:<uuid>`，73 字符）按方言分两种情况：
  - **PostgreSQL**：从未存在——`varchar(64)` 拒写。这正是 #253 的缺陷面。
  - **SQLite**：可能存在（无界列静默接受）。→ #276 的迁移把它们**原地规范化**成新形状（资源仍在 details 里，不丢），再给该列加上与 PostgreSQL 等价的边界。

### 决策四：分类列的边界由两种方言共同表达（#276）

| 方言 | 表达方式 |
| --- | --- |
| SQLite | 表重建 + `CHECK (length(action) <= 64)`（SQLite 不强制 VARCHAR 长度） |
| PostgreSQL | `varchar(64)` + 同名显式约束 `ck_lingdoc_project_audits_action_len` |

规则按**字符数**计（`length` 与 `varchar` 的计数口径一致），两种方言都在字符意义上界住分类值。

**边界含 64（`<= 64`），不是 `< 64`**。这不是设计偏好，是实测事实：本机 PostgreSQL 的真实审计表里**有 11 行恰好 64 字符**的旧形状行（`authorization.issue-resolve:<uuid>`），用 `< 64` 会把这 11 行历史拒掉。

**旧形状行真实存在，不是假设**：同一张表里 **535 行带 `:<资源>` 后缀**（长度 56 / 58 / 64）。所以「新旧格式并存」是事实，读侧按动作种类匹配的规则不是空话。

## 后果

- **消费者**（任何读审计流的前端、导出或脚本）要按动作种类匹配，不要按完整动作串等值匹配。HTTP 响应里的 `action` 字段仍然是原样的分类串。
- **写侧**新增动作族时，分类值必须短于 64 字符且不带资源；资源进 `details_json`。
- **盘点**：两种方言现存行的动作长度分布与超长行清单见 `docs/08-本轮实施方案/T276-验证报告.json`。SQLite 侧的超长行由 #276 的迁移原地规范化；无法规范化的超长行会让迁移失败并把库标成 dirty——审计历史不静默截断。
