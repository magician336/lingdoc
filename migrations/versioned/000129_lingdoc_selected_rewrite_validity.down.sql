-- PF-2/9 rollback: the selected rewrite candidate goes back to the bare
-- status word list.
--
-- The rows keep their state: 'fresh' becomes 'ready', 'stale' stays
-- 'stale', and a candidate without an adoptable result goes back to
-- 'queued'. The 'not_authorized' state has no word of its own in the old
-- list, so it rolls back to 'stale' -- the old shape could not say "the
-- authorization is gone" apart from "the inputs changed".

ALTER TABLE lingdoc_selected_rewrites
    ADD COLUMN status varchar(16) NOT NULL DEFAULT 'queued';

UPDATE lingdoc_selected_rewrites
SET status = CASE validity
    WHEN 'fresh' THEN 'ready'
    WHEN 'stale' THEN 'stale'
    WHEN 'not_authorized' THEN 'stale'
    ELSE 'queued'
END;

ALTER TABLE lingdoc_selected_rewrites
    DROP CONSTRAINT IF EXISTS lingdoc_selected_rewrites_validity_check;
ALTER TABLE lingdoc_selected_rewrites
    DROP COLUMN validity;

ALTER TABLE lingdoc_selected_rewrites
    ADD CONSTRAINT lingdoc_selected_rewrites_status_check
    CHECK (status IN ('queued', 'ready', 'failed', 'stale'));

DROP INDEX IF EXISTS idx_lingdoc_selected_rewrites_validity;
