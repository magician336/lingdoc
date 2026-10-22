-- PF-2/9: the language review report.
--
-- A language review does not produce a candidate: it produces a report -- a
-- list of findings the user previews, decides on, and then applies through a
-- separate entry point. The report therefore lives in its own table, with its
-- own baseline (the working copy it was made against), its own report version
-- and its own authorization state.

CREATE TABLE lingdoc_review_reports (
    id TEXT PRIMARY KEY NOT NULL,
    tenant_id INTEGER NOT NULL,
    actor_id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    chapter_id TEXT NOT NULL REFERENCES lingdoc_chapters(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('ready', 'applied', 'rejected')),
    report_version TEXT NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('chapter', 'section')),
    start_utf16 INTEGER NOT NULL CHECK (start_utf16 >= 0),
    end_utf16 INTEGER NOT NULL CHECK (end_utf16 > start_utf16),
    selected_text TEXT NOT NULL,
    base_chapter_version_id TEXT,
    spec_revision INTEGER NOT NULL CHECK (spec_revision >= 0),
    working_copy_revision INTEGER NOT NULL CHECK (working_copy_revision >= 1),
    body_hash TEXT NOT NULL,
    findings_json TEXT NOT NULL DEFAULT '[]',
    source_ids_json TEXT NOT NULL DEFAULT '[]',
    authorized_sources_json TEXT NOT NULL DEFAULT '[]',
    evidence_card_refs_json TEXT NOT NULL DEFAULT '[]',
    evidence_constraints_json TEXT NOT NULL DEFAULT '[]',
    template_basis_json TEXT NOT NULL DEFAULT '{}',
    writing_action TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '' CHECK (origin IN ('', 'human', 'ai_generated', 'ai_assisted_human')),
    run_mode TEXT NOT NULL DEFAULT '' CHECK (run_mode IN ('', 'real', 'mock', 'real_api_fake_model')),
    authorization_state TEXT NOT NULL DEFAULT ''
        CHECK (authorization_state IN ('', 'fresh', 'stale', 'not_authorized')),
    error_code TEXT NOT NULL DEFAULT '',
    decisions_json TEXT NOT NULL DEFAULT '[]',
    applied_key TEXT NOT NULL DEFAULT '',
    applied_response_json TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, actor_id, project_id, chapter_id, idempotency_key)
);

CREATE INDEX idx_lingdoc_review_reports_chapter
    ON lingdoc_review_reports (tenant_id, project_id, chapter_id, created_at DESC);
