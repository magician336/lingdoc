CREATE TABLE lingdoc_selected_rewrites (
    id TEXT PRIMARY KEY NOT NULL,
    tenant_id INTEGER NOT NULL,
    actor_id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    chapter_id TEXT NOT NULL REFERENCES lingdoc_chapters(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'ready', 'failed', 'stale')),
    run_mode TEXT NOT NULL CHECK (run_mode IN ('mock', 'real_api_fake_model', 'real')),
    base_chapter_version_id TEXT,
    spec_revision INTEGER NOT NULL CHECK (spec_revision >= 0),
    working_copy_revision INTEGER NOT NULL CHECK (working_copy_revision >= 1),
    start_utf16 INTEGER NOT NULL CHECK (start_utf16 >= 0),
    end_utf16 INTEGER NOT NULL CHECK (end_utf16 > start_utf16),
    selected_text TEXT NOT NULL,
    instruction TEXT NOT NULL,
    replacement_markdown TEXT NOT NULL DEFAULT '',
    source_ids_json TEXT NOT NULL DEFAULT '[]',
    authorized_sources_json TEXT NOT NULL DEFAULT '[]',
    review_items_json TEXT NOT NULL DEFAULT '[]',
    error_code TEXT NOT NULL DEFAULT '',
    applied_key TEXT NOT NULL DEFAULT '',
    applied_response_json TEXT NOT NULL DEFAULT '',
    apply_pending_key TEXT NOT NULL DEFAULT '',
    apply_pending_input_json TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, actor_id, project_id, chapter_id, idempotency_key)
);

CREATE INDEX idx_lingdoc_selected_rewrites_chapter
    ON lingdoc_selected_rewrites (tenant_id, project_id, chapter_id, created_at DESC);
