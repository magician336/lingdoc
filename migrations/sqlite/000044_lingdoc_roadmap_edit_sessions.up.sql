-- G3 gap closure #250: versioned route-roadmap editing sessions and the
-- first-class RouteEdge relation (SQLite dialect).
--
-- SQLite stores timestamps as DATETIME and integers as INTEGER; the partial
-- unique index is supported, so the "one open session per project" rule is
-- enforced by the schema on both dialects.
CREATE TABLE lingdoc_g3_roadmap_edit_sessions (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    status TEXT NOT NULL,
    baseline_json TEXT NOT NULL,
    node_proposals_json TEXT NOT NULL DEFAULT '[]',
    edge_proposals_json TEXT NOT NULL DEFAULT '[]',
    result_json TEXT NOT NULL DEFAULT '',
    actor_id TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE INDEX idx_lingdoc_g3_roadmap_edit_sessions_project
    ON lingdoc_g3_roadmap_edit_sessions(project_id, created_at DESC);

CREATE UNIQUE INDEX idx_lingdoc_g3_roadmap_edit_sessions_open
    ON lingdoc_g3_roadmap_edit_sessions(project_id) WHERE status = 'open';

CREATE TABLE lingdoc_g3_route_edges (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    edge_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    source_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    relation TEXT NOT NULL,
    context_revision INTEGER NOT NULL DEFAULT 0,
    actor_id TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL
);

CREATE INDEX idx_lingdoc_g3_route_edges_project
    ON lingdoc_g3_route_edges(project_id, edge_id, version);
