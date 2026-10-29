DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM lingdoc_candidates WHERE claim_evidence_json NOT IN ('[]', 'null')) OR EXISTS (SELECT 1 FROM lingdoc_selected_rewrites WHERE claim_evidence_json NOT IN ('[]', 'null')) OR EXISTS (SELECT 1 FROM lingdoc_claim_provenance) THEN
        RAISE EXCEPTION 'cannot drop nonempty claim evidence';
    END IF;
END $$;
DROP TABLE lingdoc_claim_provenance;
ALTER TABLE lingdoc_candidates DROP COLUMN claim_evidence_json;
ALTER TABLE lingdoc_selected_rewrites DROP COLUMN claim_evidence_json;
