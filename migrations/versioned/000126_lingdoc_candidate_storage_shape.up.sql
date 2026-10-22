-- PF-2/1b: the candidate storage shape and the idempotency digest version.
--
-- This is the expand half of a wide refactor. The new columns and the new
-- validity state live next to the old shape, and nothing in the running
-- production path writes them yet, so the rows that exist today keep saying
-- exactly what they said before.

ALTER TABLE lingdoc_candidates
    ADD COLUMN writing_action varchar(64) NOT NULL DEFAULT '',
    ADD COLUMN origin varchar(32) NOT NULL DEFAULT '',
    ADD COLUMN run_mode varchar(32) NOT NULL DEFAULT '',
    ADD COLUMN model_id varchar(128) NOT NULL DEFAULT '';

-- The validity state gains the "authorization is gone" reason. It is a
-- separate state from "the inputs changed" because the two call for
-- different recovery steps: run the action again, or restore the
-- authorization and run it again.
ALTER TABLE lingdoc_candidates
    DROP CONSTRAINT IF EXISTS lingdoc_candidates_validity_check;
ALTER TABLE lingdoc_candidates
    ADD CONSTRAINT lingdoc_candidates_validity_check
    CHECK (validity IN ('fresh', 'stale', 'not_authorized'));

-- The origin and the run mode are closed sets. The empty value is what a row
-- written before this migration carries, and what a candidate that has not
-- been attributed yet carries.
ALTER TABLE lingdoc_candidates
    ADD CONSTRAINT lingdoc_candidates_origin_check
    CHECK (origin IN ('', 'human', 'ai_generated', 'ai_assisted_human'));
ALTER TABLE lingdoc_candidates
    ADD CONSTRAINT lingdoc_candidates_run_mode_check
    CHECK (run_mode IN ('', 'real', 'mock', 'real_api_fake_model'));

-- The writing action has no check constraint on purpose: the action
-- vocabulary belongs to the writingaction package and grows with it, so a
-- check here would have to be edited every time an action is added.

-- The idempotency rows learn which digest rule produced their digest. The
-- rows that exist today were written by the rule that had no version prefix
-- at all, which is version 1.
ALTER TABLE lingdoc_candidate_adoptions
    ADD COLUMN digest_version integer NOT NULL DEFAULT 1;
