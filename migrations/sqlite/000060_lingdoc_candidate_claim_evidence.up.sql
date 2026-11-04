ALTER TABLE lingdoc_candidates ADD COLUMN claim_evidence_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE lingdoc_selected_rewrites ADD COLUMN claim_evidence_json TEXT NOT NULL DEFAULT '[]';
CREATE TABLE lingdoc_claim_provenance (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    chapter_id TEXT NOT NULL,
    chapter_version_id TEXT NOT NULL DEFAULT '',
    origin_kind TEXT NOT NULL,
    candidate_id TEXT NOT NULL,
    run_id TEXT NOT NULL DEFAULT '',
    working_copy_revision INTEGER NOT NULL,
    start_utf16 INTEGER NOT NULL,
    end_utf16 INTEGER NOT NULL,
    body_hash TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    UNIQUE (project_id, chapter_id, chapter_version_id, origin_kind, candidate_id, working_copy_revision)
);
CREATE INDEX idx_lingdoc_claim_provenance_scope ON lingdoc_claim_provenance(project_id, chapter_id);
