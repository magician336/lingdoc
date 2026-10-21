-- PF-2/2a: the selected rewrite candidate learns the action that produced it.
--
-- SQLite can add a column with a check constraint in place: the constraint is
-- evaluated per row and the table is not rebuilt, so the rows that exist today
-- keep saying exactly what they said before.

ALTER TABLE lingdoc_selected_rewrites
    ADD COLUMN writing_action TEXT NOT NULL DEFAULT '';

ALTER TABLE lingdoc_selected_rewrites
    ADD COLUMN origin TEXT NOT NULL DEFAULT ''
    CHECK (origin IN ('', 'human', 'ai_generated', 'ai_assisted_human'));

-- The writing action has no check constraint on purpose: the action
-- vocabulary belongs to the writingaction package and grows with it, so a
-- check here would have to be edited every time an action is added.
