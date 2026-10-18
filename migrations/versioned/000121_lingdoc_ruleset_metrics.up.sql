CREATE TABLE lingdoc_ruleset_metrics (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(64) NOT NULL DEFAULT '',
    metric VARCHAR(48) NOT NULL,
    operation VARCHAR(64) NOT NULL,
    value BIGINT NOT NULL DEFAULT 0,
    failure_reason VARCHAR(64) NOT NULL DEFAULT '',
    capability VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_lingdoc_ruleset_metrics_scope
    ON lingdoc_ruleset_metrics(tenant_id, project_id, metric, created_at);
