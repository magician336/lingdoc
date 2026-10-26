-- Beta first-run defect fix, postgres side of sqlite 000056.
--
-- A working-copy-only adoption on a chapter with no version yet writes the
-- idempotency row with chapter_version_id = ''. The column is NOT NULL with a
-- FOREIGN KEY to lingdoc_chapter_versions(id), so the empty string fails the
-- FK and the adoption transaction rolls back (HTTP 500). "No chapter version"
-- is a real state of a working-copy adoption, so the column becomes nullable;
-- NULL passes the FK trivially, so the constraint itself stays.

ALTER TABLE lingdoc_candidate_adoptions
    ALTER COLUMN chapter_version_id DROP NOT NULL;
