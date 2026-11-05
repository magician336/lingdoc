ALTER TABLE lingdoc_selected_rewrites DROP COLUMN evidence_constraints_json;
ALTER TABLE lingdoc_selected_rewrites DROP COLUMN template_basis_json;
CREATE TABLE lingdoc_change_sets_restore (
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

INSERT INTO lingdoc_change_sets_restore SELECT * FROM lingdoc_change_sets;
DROP TABLE lingdoc_change_sets;
ALTER TABLE lingdoc_change_sets_restore RENAME TO lingdoc_change_sets;
CREATE INDEX idx_lingdoc_change_sets_project ON lingdoc_change_sets (project_id, created_at DESC);

DROP TABLE lingdoc_template_governance_operations;
DROP TABLE lingdoc_rule_suggestion_runs;
DROP TABLE lingdoc_template_versions;
DROP TABLE lingdoc_template_profiles;

DROP TABLE lingdoc_g6_records;
DROP TABLE lingdoc_g3_records;
DROP TABLE lingdoc_g2_object_versions;
DROP TABLE lingdoc_g2_objects;
DROP TABLE lingdoc_g5_records;

ALTER TABLE lingdoc_selected_rewrites DROP COLUMN evidence_card_refs_json;
