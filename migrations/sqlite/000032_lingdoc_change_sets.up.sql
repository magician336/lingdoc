CREATE TABLE lingdoc_change_sets (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    created_by TEXT NOT NULL,
    reason TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('assessed', 'applied', 'rejected', 'stale')),
    base_context_revision INTEGER NOT NULL,
    target_context_revision INTEGER,
    base_spec_revision INTEGER NOT NULL,
    target_spec_revision INTEGER,
    fields_json TEXT NOT NULL DEFAULT '[]',
    impacts_json TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    applied_at DATETIME
);
CREATE INDEX idx_lingdoc_change_sets_project ON lingdoc_change_sets (project_id, created_at DESC);
