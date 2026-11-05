ALTER TABLE lingdoc_projects
    ADD COLUMN current_context_revision bigint NOT NULL DEFAULT 0;

ALTER TABLE lingdoc_projects
    ADD CONSTRAINT lingdoc_projects_context_revision_check
    CHECK (current_context_revision >= 0);

UPDATE lingdoc_projects
SET current_context_revision = 1
WHERE status = 'active' AND current_context_revision = 0;
