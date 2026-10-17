CREATE TABLE lingdoc_ruleset_use_audits (
    id varchar(36) PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    actor_id varchar(64) NOT NULL,
    actor_role varchar(32) NOT NULL,
    project_id varchar(64) NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    action varchar(32) NOT NULL CHECK (action IN ('check', 'preflight', 'release_snapshot', 'export')),
    ruleset_id varchar(64) NOT NULL,
    ruleset_version varchar(64) NOT NULL,
    ruleset_hash varchar(64) NOT NULL CHECK (length(ruleset_hash) = 64),
    effective_ruleset_hash varchar(64) NOT NULL CHECK (length(effective_ruleset_hash) = 64),
    template_id varchar(80) NOT NULL,
    template_version varchar(40) NOT NULL,
    template_copy_id varchar(64) NOT NULL DEFAULT '',
    template_copy_version bigint NOT NULL DEFAULT 0,
    project_version bigint NOT NULL DEFAULT 0,
    resource_id varchar(80) NOT NULL DEFAULT '',
    outcome varchar(24) NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE INDEX idx_lingdoc_ruleset_use_audits_project
    ON lingdoc_ruleset_use_audits (tenant_id, project_id, created_at DESC, id DESC);
CREATE INDEX idx_lingdoc_ruleset_use_audits_version
    ON lingdoc_ruleset_use_audits (tenant_id, ruleset_id, ruleset_version, created_at DESC);
