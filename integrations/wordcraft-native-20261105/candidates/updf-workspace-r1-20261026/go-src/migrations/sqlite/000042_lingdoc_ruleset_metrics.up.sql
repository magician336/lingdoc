CREATE TABLE lingdoc_ruleset_metrics (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    project_id TEXT NOT NULL DEFAULT '',
    metric TEXT NOT NULL,
    operation TEXT NOT NULL,
    value INTEGER NOT NULL DEFAULT 0,
    failure_reason TEXT NOT NULL DEFAULT '',
    capability TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL
);

CREATE INDEX idx_lingdoc_ruleset_metrics_scope
    ON lingdoc_ruleset_metrics(tenant_id, project_id, metric, created_at);
