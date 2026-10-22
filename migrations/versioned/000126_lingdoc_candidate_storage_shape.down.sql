-- Reverse the expand half of the candidate storage shape.
--
-- A candidate whose authorization is gone has no truthful representation in
-- the old two-value validity state. Re-adding the old check therefore fails
-- on such a row, which refuses the rollback instead of silently rewriting
-- the state.

ALTER TABLE lingdoc_candidates
    DROP CONSTRAINT IF EXISTS lingdoc_candidates_run_mode_check;
ALTER TABLE lingdoc_candidates
    DROP CONSTRAINT IF EXISTS lingdoc_candidates_origin_check;
ALTER TABLE lingdoc_candidates
    DROP CONSTRAINT IF EXISTS lingdoc_candidates_validity_check;
ALTER TABLE lingdoc_candidates
    ADD CONSTRAINT lingdoc_candidates_validity_check
    CHECK (validity IN ('fresh', 'stale'));

ALTER TABLE lingdoc_candidates
    DROP COLUMN IF EXISTS writing_action;
ALTER TABLE lingdoc_candidates
    DROP COLUMN IF EXISTS origin;
ALTER TABLE lingdoc_candidates
    DROP COLUMN IF EXISTS run_mode;
ALTER TABLE lingdoc_candidates
    DROP COLUMN IF EXISTS model_id;

ALTER TABLE lingdoc_candidate_adoptions
    DROP COLUMN IF EXISTS digest_version;
