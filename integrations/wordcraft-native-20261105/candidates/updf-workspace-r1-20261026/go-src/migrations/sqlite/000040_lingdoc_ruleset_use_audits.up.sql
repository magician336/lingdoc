CREATE TABLE lingdoc_ruleset_use_audits (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    actor_id TEXT NOT NULL,
    actor_role TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK (action IN ('check', 'preflight', 'release_snapshot', 'export')),
    ruleset_id TEXT NOT NULL,
    ruleset_version TEXT NOT NULL,
    ruleset_hash TEXT NOT NULL CHECK (length(ruleset_hash) = 64),
    effective_ruleset_hash TEXT NOT NULL CHECK (length(effective_ruleset_hash) = 64),
    template_id TEXT NOT NULL,
    template_version TEXT NOT NULL,
    template_copy_id TEXT NOT NULL DEFAULT '',
    template_copy_version INTEGER NOT NULL DEFAULT 0,
    project_version INTEGER NOT NULL DEFAULT 0,
    resource_id TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL,
    created_at DATETIME NOT NULL
);

CREATE INDEX idx_lingdoc_ruleset_use_audits_project
    ON lingdoc_ruleset_use_audits (tenant_id, project_id, created_at DESC, id DESC);
CREATE INDEX idx_lingdoc_ruleset_use_audits_version
    ON lingdoc_ruleset_use_audits (tenant_id, ruleset_id, ruleset_version, created_at DESC);
