ALTER TABLE lingdoc_projects
    ADD COLUMN spec_metadata_json text NOT NULL DEFAULT '{}';

CREATE TABLE lingdoc_draft_candidates (
    id varchar(36) PRIMARY KEY,
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    kind varchar(32) NOT NULL,
    title varchar(255) NOT NULL,
    content text NOT NULL,
    level varchar(32) NOT NULL CHECK (level IN ('candidate_evidence', 'background', 'discovery')),
    based_on_context_revision bigint NOT NULL DEFAULT 0 CHECK (based_on_context_revision >= 0),
    provenance_json text NOT NULL DEFAULT '{}',
    created_by varchar(64) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_lingdoc_draft_candidates_project ON lingdoc_draft_candidates(project_id, created_at, id);

CREATE TABLE lingdoc_project_audits (
    id varchar(36) PRIMARY KEY,
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    actor_id varchar(64) NOT NULL,
    action varchar(64) NOT NULL,
    target varchar(128) NOT NULL,
    details_json text NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_lingdoc_project_audits_project ON lingdoc_project_audits(project_id, created_at, id);
