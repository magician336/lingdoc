-- Reverse of 000057 (sqlite): drop the row-level index rollup.
-- Same rationale as postgres 000136 down: the column holds nothing that is not
-- derivable from chunks.index_status.
ALTER TABLE knowledges DROP COLUMN index_status;
