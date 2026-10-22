-- 回退把归档这个终态从 status 的取值集合里拿掉。
--
-- 已归档的项目无法表示成旧的两个状态：丢弃是另一件事（discarded_at），归档
-- 没有等价的旧状态。这一步因此拒绝带着已归档的项目回退，而不是把它们悄悄
-- 改写成别的东西——那会让「项目已结束」这个事实消失。
--
-- 守卫表用旧的状态集合做一次写入检查：只要库里还有已归档的行，这一步就失败，
-- 迁移停在脏状态上等人处置。
CREATE TABLE lingdoc_projects_status_guard (
    status TEXT NOT NULL CHECK (status IN ('draft', 'active'))
);
INSERT INTO lingdoc_projects_status_guard (status)
SELECT status FROM lingdoc_projects;
DROP TABLE lingdoc_projects_status_guard;

CREATE TABLE lingdoc_projects_previous (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('draft', 'active')),
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

INSERT INTO lingdoc_projects_previous (
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
ALTER TABLE lingdoc_projects_previous RENAME TO lingdoc_projects;
CREATE INDEX idx_lingdoc_projects_tenant ON lingdoc_projects (tenant_id, created_at DESC);
CREATE INDEX idx_lingdoc_projects_discarded_at ON lingdoc_projects(tenant_id, discarded_at);
