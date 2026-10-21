-- PF-2/2a: the selected rewrite candidate learns the action that produced it.
--
-- The two columns are empty for the rows that exist today and for the
-- candidates produced on the legacy path: the legacy path is driven by a free
-- text instruction and records no action.

ALTER TABLE lingdoc_selected_rewrites
    ADD COLUMN writing_action varchar(64) NOT NULL DEFAULT '',
    ADD COLUMN origin varchar(32) NOT NULL DEFAULT '';

-- The origin is a closed set. The empty value is what a row written before
-- this migration carries, and what a candidate that has not been attributed
-- yet carries.
ALTER TABLE lingdoc_selected_rewrites
    ADD CONSTRAINT lingdoc_selected_rewrites_origin_check
    CHECK (origin IN ('', 'human', 'ai_generated', 'ai_assisted_human'));

-- The writing action has no check constraint on purpose: the action
-- vocabulary belongs to the writingaction package and grows with it, so a
-- check here would have to be edited every time an action is added.
