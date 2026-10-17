DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM lingdoc_change_sets WHERE status = 'satisfied') THEN
        RAISE EXCEPTION 'cannot roll back satisfied ChangeSets without losing their terminal meaning';
    END IF;
END;
$$;

ALTER TABLE lingdoc_change_sets
    DROP CONSTRAINT IF EXISTS lingdoc_change_sets_status_check;

ALTER TABLE lingdoc_change_sets
    ADD CONSTRAINT lingdoc_change_sets_status_check
    CHECK (status IN ('draft', 'assessed', 'applied', 'rejected', 'stale', 'stabilized'));
