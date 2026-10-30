CREATE TEMP TABLE IF NOT EXISTS claim_evidence_downgrade_guard (value INTEGER NOT NULL CHECK (value = 0));
DELETE FROM claim_evidence_downgrade_guard;
INSERT INTO claim_evidence_downgrade_guard (value)
SELECT CASE WHEN EXISTS (SELECT 1 FROM lingdoc_candidates WHERE claim_evidence_json NOT IN ('[]', 'null')) OR EXISTS (SELECT 1 FROM lingdoc_selected_rewrites WHERE claim_evidence_json NOT IN ('[]', 'null')) OR EXISTS (SELECT 1 FROM lingdoc_claim_provenance) THEN 1 ELSE 0 END;
DROP TABLE claim_evidence_downgrade_guard;
DROP TABLE lingdoc_claim_provenance;
ALTER TABLE lingdoc_candidates DROP COLUMN claim_evidence_json;
ALTER TABLE lingdoc_selected_rewrites DROP COLUMN claim_evidence_json;
