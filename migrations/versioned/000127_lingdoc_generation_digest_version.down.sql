-- Reverse the digest version of the generation run.
--
-- The column goes away with its values: a rollback cannot tell which rule
-- produced which digest any more, so the rows that exist after it are read as
-- "written by the rule that had no version at all".

ALTER TABLE lingdoc_generation_runs
    DROP COLUMN IF EXISTS digest_version;
