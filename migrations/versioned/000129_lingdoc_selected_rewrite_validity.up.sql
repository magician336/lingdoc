-- PF-2/9: the selected rewrite candidate's adoptability state becomes the
-- candidate layer's validity enumeration.
--
-- The rewrite side used to carry a bare status word list of its own
-- ('queued'/'ready'/'failed'/'stale'). That was the second validity
-- vocabulary in the repository: the candidate layer already owns the three
-- states that answer "can this still be adopted", and the two paths have to
-- say the same thing in the same words.
--
-- The rows that exist today are copied over with their old status word
-- translated, so no candidate loses its state: 'ready' becomes 'fresh' and
-- 'stale' stays 'stale'. 'queued' and 'failed' have no adoptable result, so
-- they carry the empty state and keep their reason in error_code.

ALTER TABLE lingdoc_selected_rewrites
    ADD COLUMN validity varchar(16) NOT NULL DEFAULT '';

UPDATE lingdoc_selected_rewrites
SET validity = CASE status
    WHEN 'ready' THEN 'fresh'
    WHEN 'stale' THEN 'stale'
    ELSE ''
END;

ALTER TABLE lingdoc_selected_rewrites
    DROP CONSTRAINT IF EXISTS lingdoc_selected_rewrites_status_check;
ALTER TABLE lingdoc_selected_rewrites
    DROP COLUMN status;

-- The validity is a closed set, and the empty value is what a row without an
-- adoptable result carries.
ALTER TABLE lingdoc_selected_rewrites
    ADD CONSTRAINT lingdoc_selected_rewrites_validity_check
    CHECK (validity IN ('', 'fresh', 'stale', 'not_authorized'));

CREATE INDEX IF NOT EXISTS idx_lingdoc_selected_rewrites_validity
    ON lingdoc_selected_rewrites (project_id, validity);
