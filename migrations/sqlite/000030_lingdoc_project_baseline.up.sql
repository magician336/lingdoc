ALTER TABLE lingdoc_projects ADD COLUMN delivery_status TEXT NOT NULL DEFAULT 'NOT_READY';
ALTER TABLE lingdoc_projects ADD COLUMN baseline_confirmation_id TEXT;
