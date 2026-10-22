# 12. 审计的版本字段用独立列，不塞进 details

**状态**：已采纳（2026-10-22，由 Issue [#327](https://github.com/magician336/lingdoc/issues/327)（施工并行面 PF-3）与 Spec [#130](https://github.com/magician336/lingdoc/issues/130) 产生）

**修订关系**：本决策**不修订** [ADR 0010](0010-audit-action-format-and-read-sides.md)。它决定 `AuditEvent` 的**持久化形状**。

## 背景

Spec 要求审计事件带「target reference, target version, scope evaluation, project revision, request/correlation IDs, reason code, policy version and source service」（[29-Spec](../06-灵档产品开发规划/29-Spec-G5审计与权限可见性.md):28），用户故事要求 allow/deny 事件能重建一次决定（同文件 :18）。

现状有一个必须先说清的事实：**`AuditEvent` 有 13 个字段，GORM 路径只落库 7 列**。`lingdoc_project_audits` 表只有 `id, project_id, actor_id, action, target, details_json, created_at`（当前生效定义见 `migrations/sqlite/000046_lingdoc_audit_action_bounds.up.sql:18-26` 与 `migrations/versioned/000106_lingdoc_draft_provenance.up.sql:18-27`）。`TenantID`/`UserID`/`Role`/`Capability`/`Decision` **根本不落库**，`Reason` 被塞进 `details_json` 的 `reason` 键（`internal/lingdoc/workspacecore/service.go:409-424`）；读回同样只还原 7 个字段（同文件 `:426-443`，构造在 `:440`）。

也就是说，「加字段」这个动作在 `AuditEvent` 上已经发生过一次，而且做成了**声明与持久化不一致**的样子。本轮要加的 `target_version` 与 `project_revision` 面临同一个岔路口。

## 决策

### 决策一：`target_version` 与 `project_revision` 用**独立列**

两种方言各加两列（`migrations/sqlite/000052_lingdoc_audit_version_fields.*`、`migrations/versioned/000131_lingdoc_audit_version_fields.*`），`auditEventRow` 同步加两个字段，写侧写、读侧读。

理由：Spec 把它们写成 append-only 审计事件的一等字段，不是附注。一条审计要能重建「当时是哪个版本、当时的上下文修订是多少」，这两个值必须是**可查询、可索引、可约束**的一等事实。塞进 `details_json` 会让它们退化成需要解析 JSON 才能取出的字符串——查不了、约束不了、类型也保不住。

> 迁移编号：`sqlite` 下一个可用是 `000052`、`versioned` 下一个可用是 `000131`（两方言偏移量固定为 79）。编号会被上游合并抢占，提交前必须重核；被占则改号并**同 PR 改登记处**。

### 决策二：**不回改**历史行

迁移只加列，历史行的两个新列取默认值（`target_version` 空串、`project_revision` 为 `0`）。审计是追加式、不可变的；回填历史行等于用今天的事实改写当时的记录。**空值表示「这条记录产生时没有这个概念」**，不是「值为零」。

### 决策三：既有那 5 个不落库字段**本轮不补**

`TenantID`/`UserID`/`Role`/`Capability`/`Decision` 不落库是**历史欠账**，不是设计选择。本轮不补，是因为补它们会牵动读侧的角色过滤（`ListAuditEvents` 的过滤实际只靠 `item.ActorID`，因为 `item.UserID` 在 GORM 路径上恒空——`service.go:440` 不还原它），那是另一张票的工程量。

但**必须显式记账**：读者不能从「审计事件有 decision 和 capability 字段」推出「审计事件落库了 decision 和 capability」。这条欠账写进交付说明与本 ADR 的后果段。

## 后果

- **两种方言的列集合必须一致**：`sqlite/000052` 与 `versioned/000131` 表达同一条规则，同名不同编号（沿用 `000046`/`000125` 的先例）。
- **`internal/lingdoc/membership/fixture_test.go:253` 有一份镜像的 `auditEventRow`**：它独立定义了行模型，改列定义时必须同侧同步，否则两侧对同一张表的认知会漂移。
- **`details_json` 仍是既有 revision 语义的落点**：变更集场景下的 `base_context_revision`/`target_context_revision` 仍留在 details 里，它们是变更集的一对参照值，与本决策新增的「目标版本」「项目修订号」不是同一对概念（见 `GLOSSARY.md` 的「目标版本」「项目修订号」）。
- 本决策不解决 `AuditEvent` 声明 13 字段、落库 7 字段的整体不一致；那一条留给专门的票。
