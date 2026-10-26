-- Reverse of 000135: restore chapter_version_id NOT NULL.
-- Fails explicitly when NULL rows exist instead of silently rewriting them.

SELECT CASE
    WHEN EXISTS (SELECT 1 FROM lingdoc_candidate_adoptions WHERE chapter_version_id IS NULL)
    THEN (SELECT 1/0)
    ELSE (SELECT 1)
END;

ALTER TABLE lingdoc_candidate_adoptions
    ALTER COLUMN chapter_version_id SET NOT NULL;
