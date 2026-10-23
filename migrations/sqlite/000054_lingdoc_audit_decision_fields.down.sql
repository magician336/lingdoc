-- 回退：去掉审计事件的五个决策列（#415 的 down）。
--
-- 这五列只承载「此后新增」的事实，回退它们不会让任何一行变得无法表示：旧 schema
-- 里没有这些概念，行上的值随列一起消失；这些事实的**旧载体**（details_json 里的
-- tenant_id / role / capability / decision 键）由写入方一直保留，回退后审计历史的
-- 其余部分逐字保留。审计历史不静默重写。
--
-- DROP COLUMN 要求 SQLite ≥ 3.35，且该列不得被索引、视图、触发器或约束引用；
-- 这五列都不在其中。仓内近期迁移（000046–000052）用同一种回退方式。
ALTER TABLE lingdoc_project_audits DROP COLUMN decision;
ALTER TABLE lingdoc_project_audits DROP COLUMN capability;
ALTER TABLE lingdoc_project_audits DROP COLUMN role;
ALTER TABLE lingdoc_project_audits DROP COLUMN user_id;
ALTER TABLE lingdoc_project_audits DROP COLUMN tenant_id;
