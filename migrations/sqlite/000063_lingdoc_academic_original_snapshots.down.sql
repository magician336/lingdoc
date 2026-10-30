-- Preserve snapshot bytes: operators must explicitly migrate or remove them
-- before downgrading. The migration runner executes this script transactionally.
CREATE TEMP TABLE academic_original_snapshot_downgrade_guard (n INTEGER NOT NULL CHECK (n = 0));
INSERT INTO academic_original_snapshot_downgrade_guard SELECT COUNT(*) FROM lingdoc_academic_original_snapshots;
DROP TABLE academic_original_snapshot_downgrade_guard;
DROP TRIGGER lingdoc_academic_original_snapshots_no_replace;
DROP TRIGGER lingdoc_academic_original_snapshots_no_update;
DROP TABLE lingdoc_academic_original_snapshots;
