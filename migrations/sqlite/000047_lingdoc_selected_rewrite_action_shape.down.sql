-- Reverse the action shape of the selected rewrite candidate.
--
-- SQLite keeps the table and drops the two columns with their values, so a
-- rollback also drops the attribution of the candidates produced on the action
-- path.

ALTER TABLE lingdoc_selected_rewrites DROP COLUMN writing_action;
ALTER TABLE lingdoc_selected_rewrites DROP COLUMN origin;
