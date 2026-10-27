-- Reverse of 000135: restore chapter_version_id NOT NULL.
--
-- PostgreSQL enforces the precondition itself: when NULL rows exist the
-- ALTER fails with "column contains null values" and nothing changes. No
-- rows are rewritten or dropped silently.

ALTER TABLE lingdoc_candidate_adoptions
    ALTER COLUMN chapter_version_id SET NOT NULL;
