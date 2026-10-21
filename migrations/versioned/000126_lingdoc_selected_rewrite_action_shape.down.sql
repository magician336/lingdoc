-- Reverse the action shape of the selected rewrite candidate.
--
-- The two columns go away with the rows that carry them. PostgreSQL drops a
-- column with its values, so a rollback also drops the attribution of the
-- candidates produced on the action path.

ALTER TABLE lingdoc_selected_rewrites
    DROP CONSTRAINT IF EXISTS lingdoc_selected_rewrites_origin_check;

ALTER TABLE lingdoc_selected_rewrites
    DROP COLUMN IF EXISTS writing_action;
ALTER TABLE lingdoc_selected_rewrites
    DROP COLUMN IF EXISTS origin;
