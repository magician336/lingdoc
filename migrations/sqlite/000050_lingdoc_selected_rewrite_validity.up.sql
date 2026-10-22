-- PF-2/9: the selected rewrite candidate's adoptability state becomes the
-- candidate layer's validity enumeration.
--
-- The rewrite side used to carry a bare status word list of its own
-- ('queued'/'ready'/'failed'/'stale'). That was the second validity
-- vocabulary in the repository: the candidate layer already owns the three
-- states that answer "can this still be adopted", and the two paths have to
-- say the same thing in the same words.
--
-- SQLite cannot change a CHECK constraint in place, so the table is rebuilt:
-- the new table carries the validity column, the rows are copied with their
-- old status word translated, and the copy is checked -- row count and every
-- primary key -- before the old table is dropped.

CREATE TABLE lingdoc_selected_rewrites_next (
    id TEXT PRIMARY KEY NOT NULL,
    tenant_id INTEGER NOT NULL,
    actor_id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    chapter_id TEXT NOT NULL REFERENCES lingdoc_chapters(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    validity TEXT NOT NULL DEFAULT '' CHECK (validity IN ('', 'fresh', 'stale', 'not_authorized')),
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

-- 'ready' becomes 'fresh' and 'stale' stays 'stale'; 'queued' and 'failed'
-- have no adoptable result, so they carry the empty state and keep their
-- reason in error_code.
INSERT INTO lingdoc_selected_rewrites_next (
    id, tenant_id, actor_id, project_id, chapter_id, idempotency_key,
    request_hash, validity, run_mode, base_chapter_version_id, spec_revision,
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
    CASE status WHEN 'ready' THEN 'fresh' WHEN 'stale' THEN 'stale' ELSE '' END,
    run_mode, base_chapter_version_id, spec_revision, working_copy_revision,
    start_utf16, end_utf16, selected_text, instruction, replacement_markdown,
    source_ids_json, authorized_sources_json, review_items_json,
    evidence_card_refs_json, template_basis_json, evidence_constraints_json,
    writing_action, origin, error_code, applied_key, applied_response_json,
    apply_pending_key, apply_pending_input_json, created_at, updated_at
FROM lingdoc_selected_rewrites;

-- The copy must not lose rows and must not invent rows. Inserting a value
-- the guard forbids fails the migration before the old table is touched.
CREATE TEMP TABLE IF NOT EXISTS lingdoc_selected_rewrites_upgrade_guard (
    value INTEGER PRIMARY KEY CHECK (value = 1)
);
DELETE FROM lingdoc_selected_rewrites_upgrade_guard;
INSERT INTO lingdoc_selected_rewrites_upgrade_guard (value)
SELECT 0
WHERE (SELECT COUNT(*) FROM lingdoc_selected_rewrites) <> (SELECT COUNT(*) FROM lingdoc_selected_rewrites_next);
-- Every copied row must still be reachable by its primary key.
INSERT INTO lingdoc_selected_rewrites_upgrade_guard (value)
SELECT 0
WHERE EXISTS (
    SELECT 1 FROM lingdoc_selected_rewrites old
    WHERE NOT EXISTS (SELECT 1 FROM lingdoc_selected_rewrites_next next WHERE next.id = old.id)
);
-- The idempotency key is the row's business identity: a copy that let two
-- rows share one key would let a replay return the wrong candidate.
INSERT INTO lingdoc_selected_rewrites_upgrade_guard (value)
SELECT 0
WHERE EXISTS (
    SELECT 1 FROM lingdoc_selected_rewrites_next
    GROUP BY tenant_id, actor_id, project_id, chapter_id, idempotency_key
    HAVING COUNT(*) > 1
);
DELETE FROM lingdoc_selected_rewrites_upgrade_guard;
DROP TABLE lingdoc_selected_rewrites_upgrade_guard;

DROP TABLE lingdoc_selected_rewrites;
ALTER TABLE lingdoc_selected_rewrites_next RENAME TO lingdoc_selected_rewrites;
CREATE INDEX idx_lingdoc_selected_rewrites_chapter
    ON lingdoc_selected_rewrites (tenant_id, project_id, chapter_id, created_at DESC);
CREATE INDEX idx_lingdoc_selected_rewrites_validity
    ON lingdoc_selected_rewrites (project_id, validity);
