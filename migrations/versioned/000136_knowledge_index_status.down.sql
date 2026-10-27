-- Reverse of 000136: drop the row-level index rollup.
--
-- The column carries no data that is not derivable from chunks.index_status
-- (the backfill in the up script derived it from there), so dropping it loses
-- no independent facts. parse_status was never touched.
ALTER TABLE knowledges DROP COLUMN IF EXISTS index_status;
