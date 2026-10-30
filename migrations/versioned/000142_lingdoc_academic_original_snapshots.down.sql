-- Lock out new inserts while checking that no snapshot bytes will be lost.
LOCK TABLE lingdoc_academic_original_snapshots IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM lingdoc_academic_original_snapshots) THEN
        RAISE EXCEPTION 'cannot downgrade while academic original snapshots exist';
    END IF;
END $$;
DROP TRIGGER lingdoc_academic_original_snapshots_no_update ON lingdoc_academic_original_snapshots;
DROP FUNCTION lingdoc_academic_original_snapshots_reject_update();
DROP TABLE lingdoc_academic_original_snapshots;
