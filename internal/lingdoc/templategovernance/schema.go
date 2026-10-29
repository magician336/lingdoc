package templategovernance

import "time"

// SQLiteDDL and PostgreSQLDDL are supplied to the application's versioned
// migration runner. NewService never auto-migrates a production database.
const SQLiteDDL = `
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
CREATE TABLE lingdoc_template_replay_fixtures (
 tenant_id INTEGER NOT NULL, template_id TEXT NOT NULL, version TEXT NOT NULL,
 content_hash TEXT NOT NULL, fixture_id TEXT NOT NULL,
 ruleset_id TEXT NOT NULL, ruleset_version TEXT NOT NULL, ruleset_hash TEXT NOT NULL,
 policy_hash TEXT NOT NULL, rule_id TEXT NOT NULL, scene TEXT NOT NULL,
 polarity TEXT NOT NULL CHECK(polarity IN ('positive','negative')),
 target_ref TEXT NOT NULL, input_json TEXT NOT NULL, expected_status TEXT NOT NULL,
 reviewed_by TEXT NOT NULL, review_reference TEXT NOT NULL, reviewed_at DATETIME NOT NULL,
 created_by TEXT NOT NULL, created_at DATETIME NOT NULL,
 PRIMARY KEY(tenant_id, template_id, version, content_hash, fixture_id)
);
CREATE INDEX idx_lingdoc_template_replay_fixtures ON lingdoc_template_replay_fixtures(tenant_id,fixture_id);
`

const PostgreSQLDDL = `
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
CREATE TABLE lingdoc_template_replay_fixtures (
 tenant_id BIGINT NOT NULL, template_id VARCHAR(80) NOT NULL, version VARCHAR(64) NOT NULL,
 content_hash VARCHAR(64) NOT NULL, fixture_id VARCHAR(80) NOT NULL,
 ruleset_id VARCHAR(80) NOT NULL, ruleset_version VARCHAR(64) NOT NULL, ruleset_hash VARCHAR(64) NOT NULL,
 policy_hash VARCHAR(64) NOT NULL, rule_id VARCHAR(80) NOT NULL, scene VARCHAR(32) NOT NULL,
 polarity VARCHAR(16) NOT NULL CHECK(polarity IN ('positive','negative')),
 target_ref VARCHAR(180) NOT NULL, input_json TEXT NOT NULL, expected_status VARCHAR(16) NOT NULL,
 reviewed_by VARCHAR(64) NOT NULL, review_reference VARCHAR(255) NOT NULL, reviewed_at TIMESTAMPTZ NOT NULL,
 created_by VARCHAR(64) NOT NULL, created_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(tenant_id, template_id, version, content_hash, fixture_id)
);
CREATE INDEX idx_lingdoc_template_replay_fixtures ON lingdoc_template_replay_fixtures(tenant_id,fixture_id);
`
const DropDDL = `
DROP TABLE lingdoc_template_governance_operations;
DROP TABLE lingdoc_template_replay_fixtures;
DROP TABLE lingdoc_rule_suggestion_runs;
DROP TABLE lingdoc_template_versions;
DROP TABLE lingdoc_template_profiles;
`

type profileRow struct {
	TenantID   uint64 `gorm:"primaryKey;autoIncrement:false"`
	TemplateID string `gorm:"primaryKey"`
	Name       string
	Revision   int64
	CreatedBy  string
	CreatedAt  time.Time
}

func (profileRow) TableName() string { return "lingdoc_template_profiles" }

type versionRow struct {
	BindingPoliciesJSON string `gorm:"column:binding_policies_json"`
	PolicyHash          string
	TenantID            uint64 `gorm:"primaryKey;autoIncrement:false"`
	TemplateID          string `gorm:"primaryKey"`
	Version             string `gorm:"primaryKey"`
	Revision            int64
	State               string
	DefinitionJSON      string `gorm:"column:definition_json"`
	RulesetJSON         string `gorm:"column:ruleset_json"`
	SourceType          string
	Scope               string
	SourceRefsJSON      string `gorm:"column:source_refs_json"`
	Curator             string
	ContentHash         string
	RulesetHash         string
	PublishedAt         *time.Time
	RecallJSON          string `gorm:"column:recall_json"`
	CreatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (versionRow) TableName() string { return "lingdoc_template_versions" }

type suggestionRow struct {
	TenantID         uint64 `gorm:"primaryKey;autoIncrement:false"`
	ID               string `gorm:"primaryKey"`
	ProjectID        string
	ActorID          string
	Revision         int64
	Status           string
	ScopeJSON        string `gorm:"column:scope_json"`
	OutputJSON       string `gorm:"column:output_json"`
	ModelID          string
	RunMode          string
	PromptVersion    string
	ToolNamesJSON    string `gorm:"column:tool_names_json"`
	InputTokens      int
	OutputTokens     int
	DurationMS       int64
	ErrorCode        string
	ErrorMessage     string
	DecisionReason   string
	DraftRulesetJSON string `gorm:"column:draft_ruleset_json"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (suggestionRow) TableName() string { return "lingdoc_rule_suggestion_runs" }

type operationRow struct {
	TenantID       uint64 `gorm:"primaryKey;autoIncrement:false"`
	ActorID        string `gorm:"primaryKey"`
	Operation      string `gorm:"primaryKey"`
	Target         string `gorm:"primaryKey"`
	IdempotencyKey string `gorm:"primaryKey"`
	RequestHash    string
	ResultJSON     string `gorm:"column:result_json"`
	BasisJSON      string `gorm:"column:basis_json"`
	CreatedAt      time.Time
}

func (operationRow) TableName() string { return "lingdoc_template_governance_operations" }

// fixtureRow is one append-only tenant replay fixture. The primary key pins
// the fixture to the exact version identity (content hash) it was trialed
// against; a re-saved draft gets a new content hash and the old rows simply
// stop answering the current pin. There is no update or delete path: "changing"
// a fixture means registering it again against the current pin.
//
// Length declarations use the single `gorm:"size:N"` spelling (the #713
// lesson: gorm parses Size only from `size:`, never from `type:`), and the
// numbers match the VARCHAR widths of both migration chains exactly.
type fixtureRow struct {
	TenantID        uint64 `gorm:"primaryKey;autoIncrement:false"`
	TemplateID      string `gorm:"primaryKey;size:80"`
	Version         string `gorm:"primaryKey;size:64"`
	ContentHash     string `gorm:"primaryKey;size:64"`
	FixtureID       string `gorm:"primaryKey;size:80"`
	RulesetID       string `gorm:"size:80"`
	RulesetVersion  string `gorm:"size:64"`
	RulesetHash     string `gorm:"size:64"`
	PolicyHash      string `gorm:"size:64"`
	RuleID          string `gorm:"size:80"`
	Scene           string `gorm:"size:32"`
	Polarity        string `gorm:"size:16"`
	TargetRef       string `gorm:"size:180"`
	InputJSON       string `gorm:"column:input_json"`
	ExpectedStatus  string `gorm:"size:16"`
	ReviewedBy      string `gorm:"size:64"`
	ReviewReference string `gorm:"size:255"`
	ReviewedAt      time.Time
	CreatedBy       string `gorm:"size:64"`
	CreatedAt       time.Time
}

func (fixtureRow) TableName() string { return "lingdoc_template_replay_fixtures" }
