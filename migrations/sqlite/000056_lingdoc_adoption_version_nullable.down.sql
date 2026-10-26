-- Reverse of 000056: restore chapter_version_id NOT NULL.
--
-- Only safe when the table holds no NULL chapter_version_id (every recorded
-- adoption points at a real chapter version). Rows with NULL would violate
-- the restored constraint, so they abort the migration explicitly instead of
-- being silently rewritten.

CREATE TABLE lingdoc_candidate_adoptions_prev (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    chapter_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    chapter_version_id TEXT NOT NULL,
    response_json TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    digest_version INTEGER NOT NULL DEFAULT 1,
    UNIQUE (project_id, chapter_id, actor_id, idempotency_key),
    FOREIGN KEY (project_id) REFERENCES lingdoc_projects(id),
    FOREIGN KEY (chapter_id) REFERENCES lingdoc_chapters(id),
    FOREIGN KEY (chapter_version_id) REFERENCES lingdoc_chapter_versions(id)
);

SELECT CASE
    WHEN EXISTS (SELECT 1 FROM lingdoc_candidate_adoptions WHERE chapter_version_id IS NULL)
    THEN RAISE(ABORT, 'cannot restore NOT NULL: NULL chapter_version_id rows exist')
END;

INSERT INTO lingdoc_candidate_adoptions_prev (
    id, project_id, chapter_id, actor_id, idempotency_key,
    request_hash, chapter_version_id, response_json, created_at, digest_version
)
SELECT
    id, project_id, chapter_id, actor_id, idempotency_key,
    request_hash, chapter_version_id, response_json, created_at, digest_version
FROM lingdoc_candidate_adoptions;

DROP TABLE lingdoc_candidate_adoptions;
ALTER TABLE lingdoc_candidate_adoptions_prev RENAME TO lingdoc_candidate_adoptions;
