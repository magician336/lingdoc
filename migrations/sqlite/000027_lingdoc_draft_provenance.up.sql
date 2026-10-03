ALTER TABLE lingdoc_projects ADD COLUMN spec_metadata_json TEXT NOT NULL DEFAULT '{}';
CREATE TABLE lingdoc_draft_candidates (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    level TEXT NOT NULL CHECK (level IN ('candidate_evidence', 'background', 'discovery')),
    based_on_context_revision INTEGER NOT NULL DEFAULT 0 CHECK (based_on_context_revision >= 0),
    provenance_json TEXT NOT NULL DEFAULT '{}',
    created_by TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_lingdoc_draft_candidates_project ON lingdoc_draft_candidates(project_id, created_at, id);
CREATE TABLE lingdoc_project_audits (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    actor_id TEXT NOT NULL,
    action TEXT NOT NULL,
    target TEXT NOT NULL,
    details_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_lingdoc_project_audits_project ON lingdoc_project_audits(project_id, created_at, id);
