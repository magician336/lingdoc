ALTER TABLE lingdoc_generation_runs
    ADD COLUMN claim_token VARCHAR(36) NOT NULL DEFAULT '';

ALTER TABLE lingdoc_generation_runs
    ADD COLUMN cancel_requested BOOLEAN NOT NULL DEFAULT FALSE;
