-- 归档是项目生命周期的一个终态，不是「丢弃」的另一种说法。
--
-- 丢弃是 draft 项目的软删除标记（discarded_at），归档是 active 项目的
-- status = archived：领域文档把两件事分开，存储层也分开。归档后项目不再
-- 接受变更，所以这个状态是终态。
ALTER TABLE lingdoc_projects
    DROP CONSTRAINT IF EXISTS lingdoc_projects_status_check;

ALTER TABLE lingdoc_projects
    ADD CONSTRAINT lingdoc_projects_status_check
    CHECK (status IN ('draft', 'active', 'archived'));
