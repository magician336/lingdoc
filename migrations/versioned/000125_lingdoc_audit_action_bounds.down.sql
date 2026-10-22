-- 回退：去掉 PostgreSQL 侧的显式分类列边界约束（#276 的 down）。
--
-- 列类型 varchar(64) 不变——它是 PostgreSQL 侧一直存在的边界。回退只去掉本轮为了让
-- 两种方言把同一条规则写在同一个名字下而加的那条显式约束。
ALTER TABLE lingdoc_project_audits
    DROP CONSTRAINT IF EXISTS ck_lingdoc_project_audits_action_len;
