CREATE TABLE lingdoc_change_sets_next (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    created_by TEXT NOT NULL,
    reason TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('assessed', 'applied', 'rejected', 'stale', 'satisfied')),
    base_context_revision INTEGER NOT NULL,
    target_context_revision INTEGER,
    base_spec_revision INTEGER NOT NULL,
    target_spec_revision INTEGER,
    fields_json TEXT NOT NULL DEFAULT '[]',
    impacts_json TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    applied_at DATETIME
);

INSERT INTO lingdoc_change_sets_next (
    id, project_id, created_by, reason, status, base_context_revision,
    target_context_revision, base_spec_revision, target_spec_revision,
    fields_json, impacts_json, created_at, applied_at
)
SELECT
    id, project_id, created_by, reason, status, base_context_revision,
    target_context_revision, base_spec_revision, target_spec_revision,
    fields_json, impacts_json, created_at, applied_at
FROM lingdoc_change_sets;

DROP TABLE lingdoc_change_sets;
ALTER TABLE lingdoc_change_sets_next RENAME TO lingdoc_change_sets;
CREATE INDEX idx_lingdoc_change_sets_project ON lingdoc_change_sets (project_id, created_at DESC);
