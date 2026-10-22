-- PF-2/9 rollback: the selected rewrite candidate goes back to the bare
-- status word list.
--
-- The rollback rebuilds the table the same way the upgrade did, so the rows
-- that exist today keep saying exactly what they said before: 'fresh'
-- becomes 'ready', 'stale' stays 'stale', and a candidate without an
-- adoptable result goes back to 'queued'. The 'not_authorized' state has no
-- word of its own in the old list, so it rolls back to 'stale' -- the old
-- shape could not say "the authorization is gone" apart from "the inputs
-- changed".

CREATE TABLE lingdoc_selected_rewrites_restore (
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
    evidence_card_refs_json TEXT NOT NULL DEFAULT '[]',
    template_basis_json TEXT NOT NULL DEFAULT '{}',
    evidence_constraints_json TEXT NOT NULL DEFAULT '[]',
    writing_action TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '' CHECK (origin IN ('', 'human', 'ai_generated', 'ai_assisted_human')),
    error_code TEXT NOT NULL DEFAULT '',
    applied_key TEXT NOT NULL DEFAULT '',
    applied_response_json TEXT NOT NULL DEFAULT '',
    apply_pending_key TEXT NOT NULL DEFAULT '',
    apply_pending_input_json TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, actor_id, project_id, chapter_id, idempotency_key)
);

INSERT INTO lingdoc_selected_rewrites_restore (
    id, tenant_id, actor_id, project_id, chapter_id, idempotency_key,
    request_hash, status, run_mode, base_chapter_version_id, spec_revision,
    working_copy_revision, start_utf16, end_utf16, selected_text, instruction,
    replacement_markdown, source_ids_json, authorized_sources_json,
    review_items_json, evidence_card_refs_json, template_basis_json,
    evidence_constraints_json, writing_action, origin, error_code, applied_key,
    applied_response_json, apply_pending_key, apply_pending_input_json,
    created_at, updated_at
)
SELECT
    id, tenant_id, actor_id, project_id, chapter_id, idempotency_key,
    request_hash,
    CASE validity
        WHEN 'fresh' THEN 'ready'
        WHEN 'stale' THEN 'stale'
        WHEN 'not_authorized' THEN 'stale'
        ELSE 'queued'
    END,
    run_mode, base_chapter_version_id, spec_revision, working_copy_revision,
    start_utf16, end_utf16, selected_text, instruction, replacement_markdown,
    source_ids_json, authorized_sources_json, review_items_json,
    evidence_card_refs_json, template_basis_json, evidence_constraints_json,
    writing_action, origin, error_code, applied_key, applied_response_json,
    apply_pending_key, apply_pending_input_json, created_at, updated_at
FROM lingdoc_selected_rewrites;

DROP TABLE lingdoc_selected_rewrites;
ALTER TABLE lingdoc_selected_rewrites_restore RENAME TO lingdoc_selected_rewrites;
CREATE INDEX idx_lingdoc_selected_rewrites_chapter
    ON lingdoc_selected_rewrites (tenant_id, project_id, chapter_id, created_at DESC);
