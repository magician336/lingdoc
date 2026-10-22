-- 审计事件的目标版本与项目修订号成为一等列（#327）。
--
-- 这两列在 Spec 里是 append-only 审计事件的一等字段，不是 details_json 里的附注：
-- 一条审计要能重建「当时是哪个版本、当时的上下文修订是多少」，这两个值必须可查询、
-- 可索引、可约束。塞进 details_json 会让它们退化成需要解析 JSON 才能取出的字符串。
--
-- 历史行不回改：新列对既有行取默认值（空串 / 0），表示「这条记录产生时没有这个概念」，
-- 而不是「版本为空」「修订为零」。审计历史不静默重写。
--
-- 与 PostgreSQL 侧（migrations/versioned/000131_lingdoc_audit_version_fields.up.sql）
-- 表达同一条规则：同名不同编号，沿用 000046 / 000125 的先例。两方言的列集合与列顺序一致
-- （两边的新列都由 ADD COLUMN 追加在 created_at 之后）。
ALTER TABLE lingdoc_project_audits ADD COLUMN target_version TEXT NOT NULL DEFAULT '';
ALTER TABLE lingdoc_project_audits ADD COLUMN project_revision INTEGER NOT NULL DEFAULT 0;
