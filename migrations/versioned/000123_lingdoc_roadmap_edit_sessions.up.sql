-- G3 gap closure #250: versioned route-roadmap editing sessions and the
-- first-class RouteEdge relation.
--
-- The session table owns the "unfrozen editing session" concept: a project has
-- at most one open (unfrozen) session, enforced by the partial unique index
-- below, so two concurrent editors cannot both claim the roadmap.
--
-- The edge table is append-only per version: the row id is "<edge_id>/<version>",
-- mirroring how route node versions are keyed in lingdoc_g3_records, so the
-- version sequence of an edge is readable without a separate history table.
CREATE TABLE lingdoc_g3_roadmap_edit_sessions (
    id VARCHAR(36) PRIMARY KEY,
    project_id VARCHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL,
    baseline_json TEXT NOT NULL,
    node_proposals_json TEXT NOT NULL DEFAULT '[]',
    edge_proposals_json TEXT NOT NULL DEFAULT '[]',
    result_json TEXT NOT NULL DEFAULT '',
    actor_id VARCHAR(64) NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_lingdoc_g3_roadmap_edit_sessions_project
    ON lingdoc_g3_roadmap_edit_sessions(project_id, created_at DESC);

CREATE UNIQUE INDEX idx_lingdoc_g3_roadmap_edit_sessions_open
    ON lingdoc_g3_roadmap_edit_sessions(project_id) WHERE status = 'open';

CREATE TABLE lingdoc_g3_route_edges (
    id VARCHAR(191) PRIMARY KEY,
    project_id VARCHAR(64) NOT NULL,
    edge_id VARCHAR(64) NOT NULL,
    version BIGINT NOT NULL,
    source_id VARCHAR(64) NOT NULL,
    target_id VARCHAR(64) NOT NULL,
    relation VARCHAR(64) NOT NULL,
    context_revision BIGINT NOT NULL DEFAULT 0,
    actor_id VARCHAR(64) NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_lingdoc_g3_route_edges_project
    ON lingdoc_g3_route_edges(project_id, edge_id, version);
