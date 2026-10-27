-- #599 (sqlite side): make "parsed but not indexed" visible on the knowledge row.
--
-- Same semantics as the postgres migration 000136: knowledges.index_status is the
-- row-level rollup of the per-chunk chunks.index_status — pending | ready | failed;
-- "failed" is non-fatal (parse succeeded, chunks on disk, index missing; a re-parse
-- rebuilds it) and parse_status is untouched.
--
-- Backfill derives the rollup from existing chunk evidence, same rules as 000136.

ALTER TABLE knowledges ADD COLUMN index_status VARCHAR(16) NOT NULL DEFAULT 'pending';

UPDATE knowledges
SET index_status = 'failed'
WHERE EXISTS (
    SELECT 1 FROM chunks c
    WHERE c.knowledge_id = knowledges.id
      AND c.deleted_at IS NULL
      AND c.index_status = 'failed'
);

UPDATE knowledges
SET index_status = 'ready'
WHERE index_status = 'pending'
  AND EXISTS (
    SELECT 1 FROM chunks c
    WHERE c.knowledge_id = knowledges.id
      AND c.deleted_at IS NULL
  )
  AND NOT EXISTS (
    SELECT 1 FROM chunks c
    WHERE c.knowledge_id = knowledges.id
      AND c.deleted_at IS NULL
      AND c.index_status = 'failed'
);
