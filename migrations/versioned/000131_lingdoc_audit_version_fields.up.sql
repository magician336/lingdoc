-- 审计事件的目标版本与项目修订号成为一等列（#327）。
--
-- 与 SQLite 侧（migrations/sqlite/000052_lingdoc_audit_version_fields.up.sql）表达同一条规则：
-- 同名不同编号，沿用 000046 / 000125 的先例。两方言的列集合与列顺序一致
-- （两边的新列都由 ADD COLUMN 追加在 created_at 之后）。
--
-- 历史行不回改：ADD COLUMN 带默认值，既有行取空串与 0，表示「这条记录产生时没有这个概念」，
-- 而不是「版本为空」「修订为零」。审计历史不静默重写。
ALTER TABLE lingdoc_project_audits
    ADD COLUMN target_version varchar(128) NOT NULL DEFAULT '';
ALTER TABLE lingdoc_project_audits
    ADD COLUMN project_revision bigint NOT NULL DEFAULT 0;
