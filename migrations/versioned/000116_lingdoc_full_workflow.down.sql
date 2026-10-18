ALTER TABLE lingdoc_selected_rewrites DROP COLUMN evidence_constraints_json;
ALTER TABLE lingdoc_selected_rewrites DROP COLUMN template_basis_json;
ALTER TABLE lingdoc_change_sets DROP CONSTRAINT lingdoc_change_sets_status_check;
ALTER TABLE lingdoc_change_sets ADD CONSTRAINT lingdoc_change_sets_status_check CHECK (status IN ('assessed','applied','rejected','stale'));

DROP TABLE lingdoc_template_governance_operations;
DROP TABLE lingdoc_rule_suggestion_runs;
DROP TABLE lingdoc_template_versions;
DROP TABLE lingdoc_template_profiles;

DROP TABLE lingdoc_g6_records;
DROP TABLE lingdoc_g3_records;
DROP TABLE lingdoc_g2_object_versions;
DROP TABLE lingdoc_g2_objects;
DROP TABLE lingdoc_g5_records;

ALTER TABLE lingdoc_selected_rewrites DROP COLUMN evidence_card_refs_json;
