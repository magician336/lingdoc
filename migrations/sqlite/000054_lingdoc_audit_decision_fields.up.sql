-- 审计事件的决策事实成为一等列（#415 / PF-7）。
--
-- `AuditEvent` 结构体一直声明着 TenantID / UserID / Role / Capability / Decision
-- 五个字段，但它们只被塞进 details_json，**没有自己的列**。后果是读侧的角色过滤
-- 实际只靠 `actor_id`：`AuditEvents` 读回时 `UserID` 恒空，于是
-- `ListAuditEvents` 里那条 `item.UserID == actor.UserID` 永远不成立
-- （Spec #130 的 User Story 3/4/5 因此是空的）。ADR 0012（决策三）已把这件事
-- 记成「历史欠账」，本迁移把它还掉。
--
-- 为什么是列而不是 details 里的键：一条审计要能重建「谁、在哪个租户、以什么角色、
-- 申请了什么能力、判定是什么」——这五个值必须可查询、可索引、可约束。塞进
-- details_json 会让它们退化成需要解析 JSON 才能取出的字符串。
--
-- 历史行不回改：新列对既有行取默认值，表示「这条记录产生时没有这个概念」，
-- 而不是「角色为空」「判定为空」。审计历史不静默重写。
--
-- 与 PostgreSQL 侧（migrations/versioned/000133_lingdoc_audit_decision_fields.up.sql）
-- 表达同一条规则：同名不同编号，沿用 000046 / 000125 的先例。两方言的列集合一致
-- （两边的新列都由 ADD COLUMN 追加在最后）。
--
-- capability 是**无界**列，与 action 列不同：action 存的是分类（`authorization.<种类>`，
-- 有界），而 capability 是 `种类:资源`，资源由调用方给出、无界（000046 的迁移注释
-- 记着这件事）。把它放进有界列就是让 000046 修掉的那个溢出缺陷原样回来。
ALTER TABLE lingdoc_project_audits ADD COLUMN tenant_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE lingdoc_project_audits ADD COLUMN user_id TEXT NOT NULL DEFAULT '';
ALTER TABLE lingdoc_project_audits ADD COLUMN role TEXT NOT NULL DEFAULT '';
ALTER TABLE lingdoc_project_audits ADD COLUMN capability TEXT NOT NULL DEFAULT '';
ALTER TABLE lingdoc_project_audits ADD COLUMN decision TEXT NOT NULL DEFAULT '';
