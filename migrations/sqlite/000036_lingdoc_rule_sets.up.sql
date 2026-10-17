CREATE TABLE lingdoc_rule_sets (
    tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    ruleset_id TEXT NOT NULL,
    version TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('draft', 'published', 'withdrawn', 'recalled')),
    ruleset_hash TEXT NOT NULL DEFAULT '',
    bindings_json TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, ruleset_id, version),
    CHECK (tenant_id > 0),
    CHECK (length(ruleset_id) BETWEEN 1 AND 64),
    CHECK (length(version) BETWEEN 1 AND 64),
    CHECK (length(created_by) BETWEEN 1 AND 64),
    CHECK (ruleset_hash = '' OR length(ruleset_hash) = 64)
);

CREATE INDEX idx_lingdoc_rule_sets_history
    ON lingdoc_rule_sets (tenant_id, ruleset_id, created_at DESC, version DESC);
