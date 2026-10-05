ALTER TABLE lingdoc_projects
    ADD COLUMN discarded_at timestamptz;

CREATE INDEX idx_lingdoc_projects_discarded_at
    ON lingdoc_projects(tenant_id, discarded_at);
