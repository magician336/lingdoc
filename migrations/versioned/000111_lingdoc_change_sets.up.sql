CREATE TABLE lingdoc_change_sets (
    id varchar(36) PRIMARY KEY,
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    created_by varchar(64) NOT NULL,
    reason text NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('draft', 'assessed', 'applied', 'rejected', 'stale')),
    base_context_revision bigint NOT NULL,
    target_context_revision bigint,
    base_spec_revision bigint NOT NULL,
    target_spec_revision bigint,
    fields_json text NOT NULL DEFAULT '[]',
    impacts_json text NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    applied_at timestamptz
);
CREATE INDEX idx_lingdoc_change_sets_project ON lingdoc_change_sets (project_id, created_at DESC);
