CREATE TABLE lingdoc_generation_runs (
    id TEXT PRIMARY KEY NOT NULL,
    tenant_id INTEGER NOT NULL,
    actor_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    chapter_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'interrupted')),
    candidate_id TEXT,
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    retryable BOOLEAN NOT NULL DEFAULT FALSE,
    input_json TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, actor_id, project_id, idempotency_key)
);

CREATE INDEX idx_lingdoc_generation_project ON lingdoc_generation_runs (tenant_id, project_id, created_at);
CREATE INDEX idx_lingdoc_generation_chapter ON lingdoc_generation_runs (tenant_id, project_id, chapter_id, created_at);
CREATE INDEX idx_lingdoc_generation_status ON lingdoc_generation_runs (status, created_at);
