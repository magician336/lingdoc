CREATE TABLE IF NOT EXISTS lingdoc_selected_rewrites (
    id varchar(36) PRIMARY KEY,
    tenant_id bigint NOT NULL,
    actor_id varchar(64) NOT NULL,
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    chapter_id varchar(36) NOT NULL REFERENCES lingdoc_chapters(id) ON DELETE CASCADE,
    idempotency_key varchar(128) NOT NULL,
    request_hash varchar(64) NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('queued', 'ready', 'failed', 'stale')),
    run_mode varchar(32) NOT NULL CHECK (run_mode IN ('mock', 'real_api_fake_model', 'real')),
    base_chapter_version_id varchar(36),
    spec_revision bigint NOT NULL CHECK (spec_revision >= 0),
    working_copy_revision bigint NOT NULL CHECK (working_copy_revision >= 1),
    start_utf16 integer NOT NULL CHECK (start_utf16 >= 0),
    end_utf16 integer NOT NULL CHECK (end_utf16 > start_utf16),
    selected_text text NOT NULL,
    instruction text NOT NULL,
    replacement_markdown text NOT NULL DEFAULT '',
    source_ids_json text NOT NULL DEFAULT '[]',
    authorized_sources_json text NOT NULL DEFAULT '[]',
    review_items_json text NOT NULL DEFAULT '[]',
    error_code varchar(64) NOT NULL DEFAULT '',
    applied_key varchar(128) NOT NULL DEFAULT '',
    applied_response_json text NOT NULL DEFAULT '',
    apply_pending_key varchar(128) NOT NULL DEFAULT '',
    apply_pending_input_json text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, actor_id, project_id, chapter_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_lingdoc_selected_rewrites_chapter
    ON lingdoc_selected_rewrites (tenant_id, project_id, chapter_id, created_at DESC);
