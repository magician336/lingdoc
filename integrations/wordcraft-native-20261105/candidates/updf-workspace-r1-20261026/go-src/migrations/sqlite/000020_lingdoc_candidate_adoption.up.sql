-- T11 candidate adoption extends the T08 workspace tables created by 000018.
-- Keep chapter history append-only while recording the candidate and actor
-- that produced each adopted version.
ALTER TABLE lingdoc_chapter_versions ADD COLUMN candidate_id TEXT;
ALTER TABLE lingdoc_chapter_versions ADD COLUMN spec_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE lingdoc_chapter_versions ADD COLUMN confirmation_valid INTEGER NOT NULL DEFAULT 0 CHECK (confirmation_valid IN (0, 1));
ALTER TABLE lingdoc_chapter_versions ADD COLUMN created_by TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_lingdoc_chapter_versions_candidate
    ON lingdoc_chapter_versions(candidate_id);

CREATE TABLE lingdoc_candidates (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    chapter_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    body_markdown TEXT NOT NULL,
    source_ids_json TEXT NOT NULL DEFAULT '[]',
    basis_json TEXT NOT NULL,
    validity TEXT NOT NULL CHECK (validity IN ('fresh', 'stale')),
    review_items_json TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (project_id) REFERENCES lingdoc_projects(id),
    FOREIGN KEY (chapter_id) REFERENCES lingdoc_chapters(id)
);
CREATE INDEX idx_lingdoc_candidates_scope ON lingdoc_candidates(project_id, chapter_id);

CREATE TABLE lingdoc_chapter_confirmations (
    id TEXT PRIMARY KEY,
    chapter_id TEXT NOT NULL,
    chapter_version_id TEXT NOT NULL,
    valid INTEGER NOT NULL DEFAULT 0 CHECK (valid IN (0, 1)),
    details_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (chapter_id) REFERENCES lingdoc_chapters(id),
    FOREIGN KEY (chapter_version_id) REFERENCES lingdoc_chapter_versions(id)
);
CREATE INDEX idx_lingdoc_confirmations_version ON lingdoc_chapter_confirmations(chapter_version_id);

CREATE TABLE lingdoc_candidate_adoptions (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    chapter_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    chapter_version_id TEXT NOT NULL,
    response_json TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (project_id, chapter_id, actor_id, idempotency_key),
    FOREIGN KEY (project_id) REFERENCES lingdoc_projects(id),
    FOREIGN KEY (chapter_id) REFERENCES lingdoc_chapters(id),
    FOREIGN KEY (chapter_version_id) REFERENCES lingdoc_chapter_versions(id)
);
