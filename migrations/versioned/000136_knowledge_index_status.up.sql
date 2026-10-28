-- #599: make "parsed but not indexed" visible on the knowledge row itself.
--
-- knowledges.index_status is the row-level rollup of the per-chunk chunks.index_status
-- ("is this content reflected in the retrieval index?"): pending | ready | failed.
-- "failed" is a non-fatal marker — the parse succeeded and the chunks are on disk,
-- only the index is missing; a re-parse rebuilds it. It deliberately does NOT touch
-- parse_status, which stays the sole parse-outcome signal.
--
-- Backfill: derive the rollup from the chunk evidence that already exists, so the
-- historical rows (e.g. the soft-deleted-then-retained chunks recorded in
-- BETA_STATUS §22.1) surface on the row without a re-parse:
--   any live chunk failed            -> failed
--   live chunks exist, none failed   -> ready
--   no live chunks                   -> keeps the column default 'pending'

ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS index_status VARCHAR(16) NOT NULL DEFAULT 'pending';

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
