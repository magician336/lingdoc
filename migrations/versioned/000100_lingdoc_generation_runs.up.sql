CREATE TABLE IF NOT EXISTS lingdoc_generation_runs (
    id UUID PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    actor_id VARCHAR(128) NOT NULL,
    project_id UUID NOT NULL,
    chapter_id UUID NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'interrupted')),
    candidate_id UUID,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    error_message VARCHAR(500) NOT NULL DEFAULT '',
    retryable BOOLEAN NOT NULL DEFAULT FALSE,
    input_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_lingdoc_generation_idempotency UNIQUE (tenant_id, actor_id, project_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_lingdoc_generation_project ON lingdoc_generation_runs (tenant_id, project_id, created_at);
CREATE INDEX IF NOT EXISTS idx_lingdoc_generation_chapter ON lingdoc_generation_runs (tenant_id, project_id, chapter_id, created_at);
CREATE INDEX IF NOT EXISTS idx_lingdoc_generation_status ON lingdoc_generation_runs (status, created_at);
