CREATE TABLE lingdoc_change_sets_next (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    created_by TEXT NOT NULL,
    reason TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('draft', 'assessed', 'applied', 'rejected', 'stale', 'stabilized')),
    base_context_revision INTEGER NOT NULL,
    target_context_revision INTEGER,
    base_spec_revision INTEGER NOT NULL,
    target_spec_revision INTEGER,
    fields_json TEXT NOT NULL DEFAULT '[]',
    impacts_json TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    applied_at DATETIME
);

INSERT INTO lingdoc_change_sets_next SELECT * FROM lingdoc_change_sets;
DROP TABLE lingdoc_change_sets;
ALTER TABLE lingdoc_change_sets_next RENAME TO lingdoc_change_sets;
CREATE INDEX idx_lingdoc_change_sets_project ON lingdoc_change_sets (project_id, created_at DESC);


CREATE TABLE lingdoc_template_profiles (
 tenant_id INTEGER NOT NULL, template_id TEXT NOT NULL, name TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision > 0), created_by TEXT NOT NULL,
 created_at DATETIME NOT NULL, PRIMARY KEY(tenant_id, template_id)
);
CREATE TABLE lingdoc_template_versions (
 tenant_id INTEGER NOT NULL, template_id TEXT NOT NULL, version TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision > 0),
 state TEXT NOT NULL CHECK(state IN ('draft','published','withdrawn','recalled')),
 definition_json TEXT NOT NULL, ruleset_json TEXT NOT NULL,
 binding_policies_json TEXT NOT NULL DEFAULT '[]', policy_hash TEXT NOT NULL DEFAULT '',
 source_type TEXT NOT NULL CHECK(source_type IN ('lingdoc_builtin','organization_input','external_reference','project_copy')),
 scope TEXT NOT NULL, source_refs_json TEXT NOT NULL, curator TEXT NOT NULL,
 content_hash TEXT NOT NULL, ruleset_hash TEXT NOT NULL, published_at DATETIME,
 recall_json TEXT NOT NULL DEFAULT 'null', created_by TEXT NOT NULL,
 created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 PRIMARY KEY(tenant_id, template_id, version),
 FOREIGN KEY(tenant_id, template_id) REFERENCES lingdoc_template_profiles(tenant_id, template_id)
);
CREATE INDEX idx_lingdoc_template_catalog ON lingdoc_template_versions(tenant_id,state,scope,created_at DESC);
CREATE TABLE lingdoc_rule_suggestion_runs (
 tenant_id INTEGER NOT NULL, id TEXT NOT NULL, project_id TEXT NOT NULL,
 actor_id TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision > 0),
 status TEXT NOT NULL CHECK(status IN ('running','suggestion','failed','stale','accepted','rejected')),
 scope_json TEXT NOT NULL, output_json TEXT NOT NULL DEFAULT 'null',
 model_id TEXT NOT NULL DEFAULT '', run_mode TEXT NOT NULL DEFAULT '',
 prompt_version TEXT NOT NULL, tool_names_json TEXT NOT NULL DEFAULT '[]',
 input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0,
 duration_ms INTEGER NOT NULL DEFAULT 0, error_code TEXT NOT NULL DEFAULT '',
 error_message TEXT NOT NULL DEFAULT '', decision_reason TEXT NOT NULL DEFAULT '',
 draft_ruleset_json TEXT NOT NULL DEFAULT 'null', created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL, PRIMARY KEY(tenant_id,id)
);
CREATE INDEX idx_lingdoc_rule_suggestions ON lingdoc_rule_suggestion_runs(tenant_id,project_id,created_at DESC);
CREATE TABLE lingdoc_template_governance_operations (
 tenant_id INTEGER NOT NULL, actor_id TEXT NOT NULL, operation TEXT NOT NULL,
 target TEXT NOT NULL, idempotency_key TEXT NOT NULL, request_hash TEXT NOT NULL,
 result_json TEXT NOT NULL, basis_json TEXT NOT NULL DEFAULT '{}',
 created_at DATETIME NOT NULL,
 PRIMARY KEY(tenant_id,actor_id,operation,target,idempotency_key)
);

CREATE TABLE lingdoc_g5_records (project_id TEXT NOT NULL,kind TEXT NOT NULL,id TEXT NOT NULL,version BIGINT NOT NULL CHECK(version>0),payload_json TEXT NOT NULL,PRIMARY KEY(project_id,kind,id));
CREATE TABLE lingdoc_g2_objects (project_id TEXT NOT NULL,kind TEXT NOT NULL,id TEXT NOT NULL,version BIGINT NOT NULL CHECK(version>0),payload_json TEXT NOT NULL,PRIMARY KEY(project_id,kind,id));
CREATE TABLE lingdoc_g2_object_versions (project_id TEXT NOT NULL,kind TEXT NOT NULL,id TEXT NOT NULL,version BIGINT NOT NULL CHECK(version>0),payload_json TEXT NOT NULL,PRIMARY KEY(project_id,kind,id,version));
CREATE TABLE lingdoc_g3_records (id TEXT PRIMARY KEY,project_id TEXT NOT NULL,kind TEXT NOT NULL,record_key TEXT NOT NULL,version BIGINT NOT NULL CHECK(version>0),body_json TEXT NOT NULL,created_at TIMESTAMP NOT NULL,updated_at TIMESTAMP NOT NULL,UNIQUE(project_id,kind,record_key));
CREATE INDEX idx_lingdoc_g3_project ON lingdoc_g3_records(project_id,kind,updated_at);
CREATE TABLE lingdoc_g6_records (id TEXT PRIMARY KEY,tenant_id BIGINT NOT NULL,project_id TEXT NOT NULL,kind TEXT NOT NULL,record_key TEXT NOT NULL,version BIGINT NOT NULL CHECK(version>0),body_json TEXT NOT NULL,created_at TIMESTAMP NOT NULL,updated_at TIMESTAMP NOT NULL,UNIQUE(tenant_id,project_id,kind,record_key));
CREATE INDEX idx_lingdoc_g6_project ON lingdoc_g6_records(tenant_id,project_id,kind,updated_at);

ALTER TABLE lingdoc_selected_rewrites ADD COLUMN evidence_card_refs_json TEXT NOT NULL DEFAULT '[]';

ALTER TABLE lingdoc_selected_rewrites ADD COLUMN template_basis_json TEXT NOT NULL DEFAULT '{}';

ALTER TABLE lingdoc_selected_rewrites ADD COLUMN evidence_constraints_json TEXT NOT NULL DEFAULT '[]';
