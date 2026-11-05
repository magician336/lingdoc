ALTER TABLE lingdoc_projects
    DROP CONSTRAINT IF EXISTS lingdoc_projects_context_revision_check;
ALTER TABLE lingdoc_projects
    DROP COLUMN IF EXISTS current_context_revision;
