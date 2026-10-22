-- 归档是项目生命周期的一个终态，不是「丢弃」的另一种说法。
--
-- 丢弃是 draft 项目的软删除标记（discarded_at），归档是 active 项目的
-- status = archived：领域文档把两件事分开，存储层也分开。归档后项目不再
-- 接受变更，所以这个状态是终态，回退不会把它改写成别的东西。
--
-- SQLite 改 CHECK 约束只能重建表：建新表 → 搬数据 → 删旧表 → 改名 → 重建索引。
-- 子表（成员、章节、审计……）的外键仍然指向 lingdoc_projects，重建不改动它们。
CREATE TABLE lingdoc_projects_next (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('draft', 'active', 'archived')),
    project_version INTEGER NOT NULL CHECK (project_version >= 1),
    spec_revision INTEGER NOT NULL CHECK (spec_revision >= 0),
    spec_json TEXT NOT NULL DEFAULT '{}',
    template_id TEXT NOT NULL,
    template_version TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    current_context_revision INTEGER NOT NULL DEFAULT 0,
    spec_metadata_json TEXT NOT NULL DEFAULT '{}',
    discarded_at DATETIME,
    delivery_status TEXT NOT NULL DEFAULT 'NOT_READY',
    baseline_confirmation_id TEXT,
    template_copy_version INTEGER NOT NULL DEFAULT 0
);

INSERT INTO lingdoc_projects_next (
    id, tenant_id, name, status, project_version, spec_revision, spec_json,
    template_id, template_version, created_at, current_context_revision,
    spec_metadata_json, discarded_at, delivery_status, baseline_confirmation_id,
    template_copy_version
)
SELECT
    id, tenant_id, name, status, project_version, spec_revision, spec_json,
    template_id, template_version, created_at, current_context_revision,
    spec_metadata_json, discarded_at, delivery_status, baseline_confirmation_id,
    template_copy_version
FROM lingdoc_projects;

DROP TABLE lingdoc_projects;
ALTER TABLE lingdoc_projects_next RENAME TO lingdoc_projects;
CREATE INDEX idx_lingdoc_projects_tenant ON lingdoc_projects (tenant_id, created_at DESC);
CREATE INDEX idx_lingdoc_projects_discarded_at ON lingdoc_projects(tenant_id, discarded_at);
