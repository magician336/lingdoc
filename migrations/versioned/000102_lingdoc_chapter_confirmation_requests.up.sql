-- T12: persist confirmation request idempotency independently from candidate adoption.
CREATE TABLE lingdoc_chapter_confirmation_requests (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    chapter_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    response_json TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_lingdoc_confirmation_request UNIQUE (project_id, chapter_id, actor_id, idempotency_key),
    FOREIGN KEY (project_id) REFERENCES lingdoc_projects(id),
    FOREIGN KEY (chapter_id) REFERENCES lingdoc_chapters(id)
);
