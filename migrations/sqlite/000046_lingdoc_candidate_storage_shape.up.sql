-- PF-2/1b: the candidate storage shape and the idempotency digest version.
--
-- SQLite cannot change a CHECK constraint in place, so the candidates table
-- is rebuilt: the new table carries the four new columns and the widened
-- validity state, the rows are copied, and the copy is checked -- row count
-- and every primary key -- before the old table is dropped.

CREATE TABLE lingdoc_candidates_next (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    chapter_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    body_markdown TEXT NOT NULL,
    source_ids_json TEXT NOT NULL DEFAULT '[]',
    basis_json TEXT NOT NULL,
    validity TEXT NOT NULL CHECK (validity IN ('fresh', 'stale', 'not_authorized')),
    review_items_json TEXT NOT NULL DEFAULT '[]',
    writing_action TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '' CHECK (origin IN ('', 'human', 'ai_generated', 'ai_assisted_human')),
    run_mode TEXT NOT NULL DEFAULT '' CHECK (run_mode IN ('', 'real', 'mock', 'real_api_fake_model')),
    model_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (project_id) REFERENCES lingdoc_projects(id),
    FOREIGN KEY (chapter_id) REFERENCES lingdoc_chapters(id)
);

INSERT INTO lingdoc_candidates_next (
    id, project_id, chapter_id, run_id, body_markdown, source_ids_json,
    basis_json, validity, review_items_json, created_at
)
SELECT
    id, project_id, chapter_id, run_id, body_markdown, source_ids_json,
    basis_json, validity, review_items_json, created_at
FROM lingdoc_candidates;

-- The copy must not lose rows and must not invent rows. Inserting a value
-- the guard forbids fails the migration before the old table is touched.
CREATE TEMP TABLE IF NOT EXISTS lingdoc_candidates_upgrade_guard (
    value INTEGER PRIMARY KEY CHECK (value = 1)
);
DELETE FROM lingdoc_candidates_upgrade_guard;
INSERT INTO lingdoc_candidates_upgrade_guard (value)
SELECT 0
WHERE (SELECT COUNT(*) FROM lingdoc_candidates) <> (SELECT COUNT(*) FROM lingdoc_candidates_next);
-- Every copied row must still be reachable by its primary key.
INSERT INTO lingdoc_candidates_upgrade_guard (value)
SELECT 0
WHERE EXISTS (
    SELECT 1 FROM lingdoc_candidates old
    WHERE NOT EXISTS (SELECT 1 FROM lingdoc_candidates_next next WHERE next.id = old.id)
);
DELETE FROM lingdoc_candidates_upgrade_guard;
DROP TABLE lingdoc_candidates_upgrade_guard;

DROP TABLE lingdoc_candidates;
ALTER TABLE lingdoc_candidates_next RENAME TO lingdoc_candidates;
CREATE INDEX idx_lingdoc_candidates_scope ON lingdoc_candidates(project_id, chapter_id);

-- The writing action has no check constraint on purpose: the action
-- vocabulary belongs to the writingaction package and grows with it, so a
-- check here would have to be edited every time an action is added.

-- The idempotency rows learn which digest rule produced their digest. The
-- rows that exist today were written by the rule that had no version prefix
-- at all, which is version 1.
ALTER TABLE lingdoc_candidate_adoptions ADD COLUMN digest_version INTEGER NOT NULL DEFAULT 1;
