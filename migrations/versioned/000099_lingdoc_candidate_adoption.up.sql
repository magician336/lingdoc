-- T11 candidate adoption extends the T08 workspace tables created by 000097.
ALTER TABLE lingdoc_chapter_versions
    ADD COLUMN candidate_id varchar(36),
    ADD COLUMN spec_revision bigint NOT NULL DEFAULT 0,
    ADD COLUMN confirmation_valid boolean NOT NULL DEFAULT false,
    ADD COLUMN created_by varchar(128) NOT NULL DEFAULT '';

CREATE INDEX idx_lingdoc_chapter_versions_candidate
    ON lingdoc_chapter_versions(candidate_id);

CREATE TABLE lingdoc_candidates (
    id varchar(36) PRIMARY KEY,
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id),
    chapter_id varchar(36) NOT NULL REFERENCES lingdoc_chapters(id),
    run_id varchar(128) NOT NULL,
    body_markdown text NOT NULL,
    source_ids_json text NOT NULL DEFAULT '[]',
    basis_json text NOT NULL,
    validity varchar(16) NOT NULL CHECK (validity IN ('fresh', 'stale')),
    review_items_json text NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_lingdoc_candidates_scope
    ON lingdoc_candidates(project_id, chapter_id);

ALTER TABLE lingdoc_chapter_versions
    ADD CONSTRAINT fk_lingdoc_chapter_versions_candidate
    FOREIGN KEY (candidate_id) REFERENCES lingdoc_candidates(id);

CREATE TABLE lingdoc_chapter_confirmations (
    id varchar(36) PRIMARY KEY,
    chapter_id varchar(36) NOT NULL REFERENCES lingdoc_chapters(id),
    chapter_version_id varchar(36) NOT NULL REFERENCES lingdoc_chapter_versions(id),
    valid boolean NOT NULL DEFAULT false,
    details_json text NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_lingdoc_confirmations_version
    ON lingdoc_chapter_confirmations(chapter_version_id);

CREATE TABLE lingdoc_candidate_adoptions (
    id varchar(36) PRIMARY KEY,
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id),
    chapter_id varchar(36) NOT NULL REFERENCES lingdoc_chapters(id),
    actor_id varchar(128) NOT NULL,
    idempotency_key varchar(128) NOT NULL,
    request_hash varchar(64) NOT NULL,
    chapter_version_id varchar(36) NOT NULL REFERENCES lingdoc_chapter_versions(id),
    response_json text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (project_id, chapter_id, actor_id, idempotency_key)
);
