ALTER TABLE lingdoc_projects
    ADD COLUMN delivery_status varchar(32) NOT NULL DEFAULT 'NOT_READY';
ALTER TABLE lingdoc_projects
    ADD COLUMN baseline_confirmation_id varchar(36);
