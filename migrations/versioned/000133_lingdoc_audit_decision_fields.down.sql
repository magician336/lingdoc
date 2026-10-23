-- 回退：去掉审计事件的五个决策列（#415 的 down）。
--
-- 与 SQLite 侧（migrations/sqlite/000054_lingdoc_audit_decision_fields.down.sql）表达同一条
-- 规则：同名不同编号。
--
-- 这五列只承载「此后新增」的事实，回退它们不会让任何一行变得无法表示：旧 schema
-- 里没有这些概念，行上的值随列一起消失；这些事实的旧载体（details_json 里的
-- tenant_id / role / capability / decision 键）由写入方一直保留。审计历史不静默重写。
ALTER TABLE lingdoc_project_audits
    DROP COLUMN IF EXISTS decision;
ALTER TABLE lingdoc_project_audits
    DROP COLUMN IF EXISTS capability;
ALTER TABLE lingdoc_project_audits
    DROP COLUMN IF EXISTS role;
ALTER TABLE lingdoc_project_audits
    DROP COLUMN IF EXISTS user_id;
ALTER TABLE lingdoc_project_audits
    DROP COLUMN IF EXISTS tenant_id;
