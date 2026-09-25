DROP TABLE IF EXISTS lingdoc_candidate_adoptions;
DROP TABLE IF EXISTS lingdoc_chapter_confirmations;
DROP TABLE IF EXISTS lingdoc_candidates;
DROP INDEX IF EXISTS idx_lingdoc_chapter_versions_candidate;
ALTER TABLE lingdoc_chapter_versions DROP COLUMN created_by;
ALTER TABLE lingdoc_chapter_versions DROP COLUMN confirmation_valid;
ALTER TABLE lingdoc_chapter_versions DROP COLUMN spec_revision;
ALTER TABLE lingdoc_chapter_versions DROP COLUMN candidate_id;
