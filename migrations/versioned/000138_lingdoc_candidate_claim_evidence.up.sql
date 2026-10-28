ALTER TABLE lingdoc_candidates ADD COLUMN claim_evidence_json text NOT NULL DEFAULT '[]';
ALTER TABLE lingdoc_selected_rewrites ADD COLUMN claim_evidence_json text NOT NULL DEFAULT '[]';
CREATE TABLE lingdoc_claim_provenance (
    id varchar(36) PRIMARY KEY,
    project_id varchar(36) NOT NULL,
    chapter_id varchar(36) NOT NULL,
    chapter_version_id varchar(36) NOT NULL DEFAULT '',
    origin_kind varchar(24) NOT NULL,
    candidate_id varchar(36) NOT NULL,
    run_id varchar(128) NOT NULL DEFAULT '',
    working_copy_revision bigint NOT NULL,
    start_utf16 integer NOT NULL,
    end_utf16 integer NOT NULL,
    body_hash varchar(64) NOT NULL,
    status varchar(24) NOT NULL,
    created_at timestamp NOT NULL,
    UNIQUE (project_id, chapter_id, chapter_version_id, origin_kind, candidate_id, working_copy_revision)
);
CREATE INDEX idx_lingdoc_claim_provenance_scope ON lingdoc_claim_provenance(project_id, chapter_id);
