ALTER TABLE lingdoc_change_sets DROP CONSTRAINT lingdoc_change_sets_status_check;
ALTER TABLE lingdoc_change_sets ADD CONSTRAINT lingdoc_change_sets_status_check CHECK (status IN ('draft','assessed','applied','rejected','stale','stabilized'));


CREATE TABLE lingdoc_template_profiles (
 tenant_id BIGINT NOT NULL, template_id VARCHAR(80) NOT NULL, name TEXT NOT NULL,
 revision BIGINT NOT NULL CHECK(revision > 0), created_by VARCHAR(64) NOT NULL,
 created_at TIMESTAMPTZ NOT NULL, PRIMARY KEY(tenant_id, template_id)
);
CREATE TABLE lingdoc_template_versions (
 tenant_id BIGINT NOT NULL, template_id VARCHAR(80) NOT NULL, version VARCHAR(64) NOT NULL,
 revision BIGINT NOT NULL CHECK(revision > 0),
 state VARCHAR(16) NOT NULL CHECK(state IN ('draft','published','withdrawn','recalled')),
 definition_json TEXT NOT NULL, ruleset_json TEXT NOT NULL,
 binding_policies_json TEXT NOT NULL DEFAULT '[]', policy_hash VARCHAR(64) NOT NULL DEFAULT '',
 source_type VARCHAR(32) NOT NULL CHECK(source_type IN ('lingdoc_builtin','organization_input','external_reference','project_copy')),
 scope TEXT NOT NULL, source_refs_json TEXT NOT NULL, curator TEXT NOT NULL,
 content_hash VARCHAR(64) NOT NULL, ruleset_hash VARCHAR(64) NOT NULL,
 published_at TIMESTAMPTZ, recall_json TEXT NOT NULL DEFAULT 'null',
 created_by VARCHAR(64) NOT NULL, created_at TIMESTAMPTZ NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL, PRIMARY KEY(tenant_id, template_id, version),
 FOREIGN KEY(tenant_id, template_id) REFERENCES lingdoc_template_profiles(tenant_id, template_id)
);
CREATE INDEX idx_lingdoc_template_catalog ON lingdoc_template_versions(tenant_id,state,scope,created_at DESC);
CREATE TABLE lingdoc_rule_suggestion_runs (
 tenant_id BIGINT NOT NULL, id VARCHAR(36) NOT NULL, project_id VARCHAR(36) NOT NULL,
 actor_id VARCHAR(64) NOT NULL, revision BIGINT NOT NULL CHECK(revision > 0),
 status VARCHAR(16) NOT NULL CHECK(status IN ('running','suggestion','failed','stale','accepted','rejected')),
 scope_json TEXT NOT NULL, output_json TEXT NOT NULL DEFAULT 'null',
 model_id TEXT NOT NULL DEFAULT '', run_mode TEXT NOT NULL DEFAULT '',
 prompt_version TEXT NOT NULL, tool_names_json TEXT NOT NULL DEFAULT '[]',
 input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0,
 duration_ms BIGINT NOT NULL DEFAULT 0, error_code TEXT NOT NULL DEFAULT '',
 error_message TEXT NOT NULL DEFAULT '', decision_reason TEXT NOT NULL DEFAULT '',
 draft_ruleset_json TEXT NOT NULL DEFAULT 'null', created_at TIMESTAMPTZ NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL, PRIMARY KEY(tenant_id,id)
);
CREATE INDEX idx_lingdoc_rule_suggestions ON lingdoc_rule_suggestion_runs(tenant_id,project_id,created_at DESC);
CREATE TABLE lingdoc_template_governance_operations (
 tenant_id BIGINT NOT NULL, actor_id VARCHAR(64) NOT NULL, operation VARCHAR(48) NOT NULL,
 target VARCHAR(180) NOT NULL, idempotency_key VARCHAR(128) NOT NULL,
 request_hash VARCHAR(64) NOT NULL, result_json TEXT NOT NULL,
 basis_json TEXT NOT NULL DEFAULT '{}', created_at TIMESTAMPTZ NOT NULL,
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
