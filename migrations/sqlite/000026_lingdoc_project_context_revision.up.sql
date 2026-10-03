ALTER TABLE lingdoc_projects ADD COLUMN current_context_revision INTEGER NOT NULL DEFAULT 0;
UPDATE lingdoc_projects SET current_context_revision = 1 WHERE status = 'active' AND current_context_revision = 0;
