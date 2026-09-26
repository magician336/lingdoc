ALTER TABLE lingdoc_generation_runs
    ADD COLUMN claim_token TEXT NOT NULL DEFAULT '';

ALTER TABLE lingdoc_generation_runs
    ADD COLUMN cancel_requested BOOLEAN NOT NULL DEFAULT FALSE;
