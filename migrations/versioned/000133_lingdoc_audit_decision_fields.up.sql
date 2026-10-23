-- 审计事件的决策事实成为一等列（#415 / PF-7）。
--
-- 与 SQLite 侧（migrations/sqlite/000054_lingdoc_audit_decision_fields.up.sql）表达同一条规则：
-- 同名不同编号，沿用 000046 / 000125 的先例。两方言的列集合一致（两边的新列都由
-- ADD COLUMN 追加在最后）。
--
-- 五个字段（tenant_id / user_id / role / capability / decision）此前只存在于
-- details_json，读侧的角色过滤因此只靠 actor_id（见 ADR 0012 决策三记的欠账）。
-- 有了自己的列，一条审计才能在不解析 JSON 的前提下重建「谁、在哪个租户、以什么
-- 角色、申请了什么能力、判定是什么」。
--
-- 历史行不回改：ADD COLUMN 带默认值，既有行取默认值，表示「这条记录产生时没有
-- 这个概念」，而不是「角色为空」「判定为空」。审计历史不静默重写。
--
-- capability 是无界 text，与 action 列不同：action 存的是有界的分类
-- （`authorization.<种类>`），capability 是 `种类:资源`，资源由调用方给出、无界。
ALTER TABLE lingdoc_project_audits
    ADD COLUMN tenant_id bigint NOT NULL DEFAULT 0;
ALTER TABLE lingdoc_project_audits
    ADD COLUMN user_id varchar(64) NOT NULL DEFAULT '';
ALTER TABLE lingdoc_project_audits
    ADD COLUMN role varchar(32) NOT NULL DEFAULT '';
ALTER TABLE lingdoc_project_audits
    ADD COLUMN capability text NOT NULL DEFAULT '';
ALTER TABLE lingdoc_project_audits
    ADD COLUMN decision varchar(32) NOT NULL DEFAULT '';
