-- PF-2/9: the language review report.
--
-- A language review does not produce a candidate: it produces a report -- a
-- list of findings the user previews, decides on, and then applies through a
-- separate entry point. The report therefore lives in its own table, with its
-- own baseline (the working copy it was made against), its own report version
-- and its own authorization state.

CREATE TABLE lingdoc_review_reports (
    id varchar(36) PRIMARY KEY,
    tenant_id bigint NOT NULL,
    actor_id varchar(64) NOT NULL,
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    chapter_id varchar(36) NOT NULL REFERENCES lingdoc_chapters(id) ON DELETE CASCADE,
    idempotency_key varchar(128) NOT NULL,
    request_hash varchar(64) NOT NULL,
    state varchar(16) NOT NULL CHECK (state IN ('ready', 'applied', 'rejected')),
    report_version varchar(64) NOT NULL,
    scope varchar(16) NOT NULL CHECK (scope IN ('chapter', 'section')),
    start_utf16 integer NOT NULL CHECK (start_utf16 >= 0),
    end_utf16 integer NOT NULL CHECK (end_utf16 > start_utf16),
    selected_text text NOT NULL,
    base_chapter_version_id varchar(36),
    spec_revision bigint NOT NULL CHECK (spec_revision >= 0),
    working_copy_revision bigint NOT NULL CHECK (working_copy_revision >= 1),
    body_hash varchar(64) NOT NULL,
    findings_json text NOT NULL DEFAULT '[]',
    source_ids_json text NOT NULL DEFAULT '[]',
    authorized_sources_json text NOT NULL DEFAULT '[]',
    evidence_card_refs_json text NOT NULL DEFAULT '[]',
    evidence_constraints_json text NOT NULL DEFAULT '[]',
    template_basis_json text NOT NULL DEFAULT '{}',
    writing_action varchar(64) NOT NULL DEFAULT '',
    origin varchar(32) NOT NULL DEFAULT '' CHECK (origin IN ('', 'human', 'ai_generated', 'ai_assisted_human')),
    run_mode varchar(32) NOT NULL DEFAULT '' CHECK (run_mode IN ('', 'real', 'mock', 'real_api_fake_model')),
    authorization_state varchar(16) NOT NULL DEFAULT ''
        CHECK (authorization_state IN ('', 'fresh', 'stale', 'not_authorized')),
    error_code varchar(64) NOT NULL DEFAULT '',
    decisions_json text NOT NULL DEFAULT '[]',
    applied_key varchar(128) NOT NULL DEFAULT '',
    applied_response_json text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, actor_id, project_id, chapter_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_lingdoc_review_reports_chapter
    ON lingdoc_review_reports (tenant_id, project_id, chapter_id, created_at DESC);
