-- Reverse the digest version of the generation run.
--
-- SQLite keeps the table and drops the column with its values: a rollback
-- cannot tell which rule produced which digest any more.

ALTER TABLE lingdoc_generation_runs DROP COLUMN digest_version;
