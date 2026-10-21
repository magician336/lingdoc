-- Reverse the expand half of the candidate storage shape.
--
-- A candidate whose authorization is gone has no truthful representation in
-- the old two-value validity state. The rollback refuses to start when such a
-- candidate exists, so the state is never silently rewritten.

CREATE TEMP TABLE IF NOT EXISTS lingdoc_candidates_downgrade_guard (
    value INTEGER PRIMARY KEY CHECK (value = 1)
);
DELETE FROM lingdoc_candidates_downgrade_guard;
INSERT INTO lingdoc_candidates_downgrade_guard (value)
SELECT 0 WHERE EXISTS (SELECT 1 FROM lingdoc_candidates WHERE validity = 'not_authorized');
DELETE FROM lingdoc_candidates_downgrade_guard;
DROP TABLE lingdoc_candidates_downgrade_guard;

CREATE TABLE lingdoc_candidates_previous (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    chapter_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    body_markdown TEXT NOT NULL,
    source_ids_json TEXT NOT NULL DEFAULT '[]',
    basis_json TEXT NOT NULL,
    validity TEXT NOT NULL CHECK (validity IN ('fresh', 'stale')),
    review_items_json TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (project_id) REFERENCES lingdoc_projects(id),
    FOREIGN KEY (chapter_id) REFERENCES lingdoc_chapters(id)
);

INSERT INTO lingdoc_candidates_previous (
    id, project_id, chapter_id, run_id, body_markdown, source_ids_json,
    basis_json, validity, review_items_json, created_at
)
SELECT
    id, project_id, chapter_id, run_id, body_markdown, source_ids_json,
    basis_json, validity, review_items_json, created_at
FROM lingdoc_candidates;

-- The copy must not lose rows and must not invent rows.
CREATE TEMP TABLE IF NOT EXISTS lingdoc_candidates_downgrade_copy_guard (
    value INTEGER PRIMARY KEY CHECK (value = 1)
);
DELETE FROM lingdoc_candidates_downgrade_copy_guard;
INSERT INTO lingdoc_candidates_downgrade_copy_guard (value)
SELECT 0
WHERE (SELECT COUNT(*) FROM lingdoc_candidates) <> (SELECT COUNT(*) FROM lingdoc_candidates_previous);
INSERT INTO lingdoc_candidates_downgrade_copy_guard (value)
SELECT 0
WHERE EXISTS (
    SELECT 1 FROM lingdoc_candidates old
    WHERE NOT EXISTS (SELECT 1 FROM lingdoc_candidates_previous previous WHERE previous.id = old.id)
);
DELETE FROM lingdoc_candidates_downgrade_copy_guard;
DROP TABLE lingdoc_candidates_downgrade_copy_guard;

DROP TABLE lingdoc_candidates;
ALTER TABLE lingdoc_candidates_previous RENAME TO lingdoc_candidates;
CREATE INDEX idx_lingdoc_candidates_scope ON lingdoc_candidates(project_id, chapter_id);

-- The digest version column goes away with the rule it belongs to.
ALTER TABLE lingdoc_candidate_adoptions DROP COLUMN digest_version;
