DROP TABLE IF EXISTS lingdoc_candidate_adoptions;
DROP TABLE IF EXISTS lingdoc_chapter_confirmations;
DROP TABLE IF EXISTS lingdoc_candidates;
DROP INDEX IF EXISTS idx_lingdoc_chapter_versions_candidate;
ALTER TABLE lingdoc_chapter_versions
    DROP CONSTRAINT IF EXISTS fk_lingdoc_chapter_versions_candidate,
    DROP COLUMN IF EXISTS created_by,
    DROP COLUMN IF EXISTS confirmation_valid,
    DROP COLUMN IF EXISTS spec_revision,
    DROP COLUMN IF EXISTS candidate_id;
