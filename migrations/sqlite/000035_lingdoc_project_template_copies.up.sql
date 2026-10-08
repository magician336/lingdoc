ALTER TABLE lingdoc_projects ADD COLUMN template_copy_version INTEGER NOT NULL DEFAULT 0;

CREATE TABLE lingdoc_project_template_copies (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    version INTEGER NOT NULL CHECK (version > 0),
    source_template_id TEXT NOT NULL,
    source_template_version TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('draft', 'bound', 'superseded', 'discarded')),
    content_hash TEXT NOT NULL,
    ruleset_hash TEXT NOT NULL,
    definition_json TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    UNIQUE (project_id, version)
);

CREATE INDEX idx_lingdoc_project_template_copies_history
    ON lingdoc_project_template_copies (project_id, version DESC);
