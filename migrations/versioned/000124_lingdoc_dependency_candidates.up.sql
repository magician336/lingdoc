-- G3 gap closure #252: AI-proposed dependency candidates.
--
-- A candidate is a proposal, not a dependency edge. The edges live in
-- lingdoc_g3_records; the candidates live here so that an undecided proposal
-- can never be read as part of the formal dependency graph.
--
-- The batch owns the idempotency key of the proposal run. The key is unique
-- per (project, actor): the same actor may never produce two batches with
-- one key, and two actors may use the same key without colliding.
CREATE TABLE lingdoc_g3_dependency_candidate_batches (
    id VARCHAR(36) PRIMARY KEY,
    project_id VARCHAR(64) NOT NULL,
    actor_id VARCHAR(64) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    scope_json TEXT NOT NULL DEFAULT '{}',
    status VARCHAR(16) NOT NULL,
    suppressed_json TEXT NOT NULL DEFAULT '[]',
    content_hash VARCHAR(64) NOT NULL DEFAULT '',
    project_version BIGINT NOT NULL DEFAULT 0,
    context_revision BIGINT NOT NULL DEFAULT 0,
    candidate_count INTEGER NOT NULL DEFAULT 0,
    suppressed_count INTEGER NOT NULL DEFAULT 0,
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (project_id, actor_id, idempotency_key)
);

CREATE INDEX idx_lingdoc_g3_dependency_candidate_batches_project
    ON lingdoc_g3_dependency_candidate_batches(project_id, created_at DESC);

-- The candidate carries the object versions it was raised against, the
-- excerpt that triggered it and the proposed rationale, so that a human can
-- check the proposal before deciding.
CREATE TABLE lingdoc_g3_dependency_candidates (
    id VARCHAR(36) PRIMARY KEY,
    project_id VARCHAR(64) NOT NULL,
    batch_id VARCHAR(36) NOT NULL,
    source_kind VARCHAR(32) NOT NULL,
    source_id VARCHAR(128) NOT NULL,
    source_version VARCHAR(128) NOT NULL,
    target_kind VARCHAR(32) NOT NULL,
    target_id VARCHAR(128) NOT NULL,
    target_version VARCHAR(128) NOT NULL,
    relation VARCHAR(128) NOT NULL,
    action VARCHAR(64) NOT NULL,
    evidence TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    status VARCHAR(16) NOT NULL,
    decision VARCHAR(16) NOT NULL DEFAULT '',
    decision_reason TEXT NOT NULL DEFAULT '',
    decided_by VARCHAR(64) NOT NULL DEFAULT '',
    decided_at TIMESTAMPTZ,
    edge_id VARCHAR(36) NOT NULL DEFAULT '',
    superseded_relation VARCHAR(128) NOT NULL DEFAULT '',
    superseded_action VARCHAR(64) NOT NULL DEFAULT '',
    project_version BIGINT NOT NULL DEFAULT 0,
    context_revision BIGINT NOT NULL DEFAULT 0,
    model_run_id VARCHAR(128) NOT NULL DEFAULT '',
    actor_id VARCHAR(64) NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_lingdoc_g3_dependency_candidates_batch
    ON lingdoc_g3_dependency_candidates(project_id, batch_id);

CREATE INDEX idx_lingdoc_g3_dependency_candidates_project
    ON lingdoc_g3_dependency_candidates(project_id, status, project_version);
