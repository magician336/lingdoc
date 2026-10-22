-- 回退：去掉审计事件的目标版本与项目修订号两列（#327 的 down）。
--
-- 这两列只承载「此后新增」的事实，回退它们不会让任何一行变得无法表示：旧 schema 里
-- 没有这两个概念，行上的值随列一起消失，审计历史的其余部分逐字保留。
--
-- DROP COLUMN 要求 SQLite ≥ 3.35，且该列不得被索引、视图、触发器或约束引用；
-- 这两列都不在其中。仓内近期迁移（000046–000049）用同一种回退方式。
ALTER TABLE lingdoc_project_audits DROP COLUMN project_revision;
ALTER TABLE lingdoc_project_audits DROP COLUMN target_version;
