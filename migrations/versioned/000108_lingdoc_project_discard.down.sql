DROP INDEX IF EXISTS idx_lingdoc_projects_discarded_at;
ALTER TABLE lingdoc_projects DROP COLUMN IF EXISTS discarded_at;
