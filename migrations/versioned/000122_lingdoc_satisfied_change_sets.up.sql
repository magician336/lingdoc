ALTER TABLE lingdoc_change_sets
    DROP CONSTRAINT IF EXISTS lingdoc_change_sets_status_check;

ALTER TABLE lingdoc_change_sets
    ADD CONSTRAINT lingdoc_change_sets_status_check
    CHECK (status IN ('draft', 'assessed', 'applied', 'rejected', 'stale', 'stabilized', 'satisfied'));
