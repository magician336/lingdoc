-- Beta first-run defect fix: a working-copy-only adoption on a chapter that
-- has no version yet writes the idempotency row with chapter_version_id = ''
-- (recordAdoptionIdempotency). The column is NOT NULL and carries a FOREIGN
-- KEY to lingdoc_chapter_versions(id), so the empty string violates the FK
-- and the whole adoption transaction rolls back with an opaque
-- "FOREIGN KEY constraint failed" -> HTTP 500. Adopting a candidate into an
-- empty chapter is the normal first adoption, so the path was effectively
-- broken on the production schema.
--
-- The honest value for "this adoption produced no chapter version" is NULL:
-- SQLite FK constraints treat NULL as satisfied, so the existing FK stays as
-- it is. SQLite cannot ALTER a column nullability in place, so the table is
-- rebuilt: new table with a nullable column, rows copied, copy checked, old
-- table dropped -- the same pattern as 000047.

CREATE TABLE lingdoc_candidate_adoptions_next (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    chapter_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    chapter_version_id TEXT,
    response_json TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    digest_version INTEGER NOT NULL DEFAULT 1,
    UNIQUE (project_id, chapter_id, actor_id, idempotency_key),
    FOREIGN KEY (project_id) REFERENCES lingdoc_projects(id),
    FOREIGN KEY (chapter_id) REFERENCES lingdoc_chapters(id),
    FOREIGN KEY (chapter_version_id) REFERENCES lingdoc_chapter_versions(id)
);

INSERT INTO lingdoc_candidate_adoptions_next (
    id, project_id, chapter_id, actor_id, idempotency_key,
    request_hash, chapter_version_id, response_json, created_at, digest_version
)
SELECT
    id, project_id, chapter_id, actor_id, idempotency_key,
    request_hash, chapter_version_id, response_json, created_at, digest_version
FROM lingdoc_candidate_adoptions;

-- The copy must be complete before the old table is dropped. RAISE() only
-- exists inside trigger programs, so the abort is expressed as a constraint
-- violation instead: when the counts disagree this INSERT writes a NULL
-- project_id, which fails NOT NULL and fails the migration.
INSERT INTO lingdoc_candidate_adoptions_next (id)
SELECT 'row-count-mismatch-abort'
WHERE (SELECT COUNT(*) FROM lingdoc_candidate_adoptions)
   <> (SELECT COUNT(*) FROM lingdoc_candidate_adoptions_next);

DROP TABLE lingdoc_candidate_adoptions;
ALTER TABLE lingdoc_candidate_adoptions_next RENAME TO lingdoc_candidate_adoptions;
