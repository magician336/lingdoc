DROP TABLE IF EXISTS lingdoc_draft_candidates;
DROP TABLE IF EXISTS lingdoc_project_audits;
ALTER TABLE lingdoc_projects DROP COLUMN IF EXISTS spec_metadata_json;
