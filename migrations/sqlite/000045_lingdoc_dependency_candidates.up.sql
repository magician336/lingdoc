-- G3 gap closure #252: AI-proposed dependency candidates (SQLite dialect).
--
-- A candidate is a proposal, not a dependency edge. The edges live in
-- lingdoc_g3_records; the candidates live here so that an undecided proposal
-- can never be read as part of the formal dependency graph.
--
-- The batch owns the idempotency key of the proposal run. The key is unique
-- per (project, actor): the same actor may never produce two batches with
-- one key, and two actors may use the same key without colliding.
CREATE TABLE lingdoc_g3_dependency_candidate_batches (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    scope_json TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL,
    suppressed_json TEXT NOT NULL DEFAULT '[]',
    content_hash TEXT NOT NULL DEFAULT '',
    project_version INTEGER NOT NULL DEFAULT 0,
    context_revision INTEGER NOT NULL DEFAULT 0,
    candidate_count INTEGER NOT NULL DEFAULT 0,
    suppressed_count INTEGER NOT NULL DEFAULT 0,
    reason TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (project_id, actor_id, idempotency_key)
);

CREATE INDEX idx_lingdoc_g3_dependency_candidate_batches_project
    ON lingdoc_g3_dependency_candidate_batches(project_id, created_at DESC);

-- The candidate carries the object versions it was raised against, the
-- excerpt that triggered it and the proposed rationale, so that a human can
-- check the proposal before deciding.
CREATE TABLE lingdoc_g3_dependency_candidates (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    batch_id TEXT NOT NULL,
    source_kind TEXT NOT NULL,
    source_id TEXT NOT NULL,
    source_version TEXT NOT NULL,
    target_kind TEXT NOT NULL,
    target_id TEXT NOT NULL,
    target_version TEXT NOT NULL,
    relation TEXT NOT NULL,
    action TEXT NOT NULL,
    evidence TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    confidence REAL NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    decision TEXT NOT NULL DEFAULT '',
    decision_reason TEXT NOT NULL DEFAULT '',
    decided_by TEXT NOT NULL DEFAULT '',
    decided_at DATETIME,
    edge_id TEXT NOT NULL DEFAULT '',
    superseded_relation TEXT NOT NULL DEFAULT '',
    superseded_action TEXT NOT NULL DEFAULT '',
    project_version INTEGER NOT NULL DEFAULT 0,
    context_revision INTEGER NOT NULL DEFAULT 0,
    model_run_id TEXT NOT NULL DEFAULT '',
    actor_id TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE INDEX idx_lingdoc_g3_dependency_candidates_batch
    ON lingdoc_g3_dependency_candidates(project_id, batch_id);

CREATE INDEX idx_lingdoc_g3_dependency_candidates_project
    ON lingdoc_g3_dependency_candidates(project_id, status, project_version);
