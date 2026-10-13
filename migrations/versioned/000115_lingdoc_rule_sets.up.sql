CREATE TABLE lingdoc_rule_sets (
    tenant_id bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    ruleset_id varchar(64) NOT NULL,
    version varchar(64) NOT NULL,
    state varchar(16) NOT NULL CHECK (state IN ('draft', 'published', 'withdrawn', 'recalled')),
    ruleset_hash varchar(64) NOT NULL DEFAULT '',
    bindings_json text NOT NULL,
    created_by varchar(64) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, ruleset_id, version),
    CHECK (tenant_id > 0),
    CHECK (length(ruleset_id) BETWEEN 1 AND 64),
    CHECK (length(version) BETWEEN 1 AND 64),
    CHECK (length(created_by) BETWEEN 1 AND 64),
    CHECK (ruleset_hash = '' OR length(ruleset_hash) = 64)
);

CREATE INDEX idx_lingdoc_rule_sets_history
    ON lingdoc_rule_sets (tenant_id, ruleset_id, created_at DESC, version DESC);

