-- T13/T14: 交付快照与导出产物的持久化，与 000024_lingdoc_delivery_stores 同形。
--
-- 两张表放同一份迁移，是因为产物表的 snapshot_id 指向快照表：分开写就有个中间
-- 状态——产物表建好了、它要指向的那张还没有。
--
-- 列宽：本轮的 ID 是带前缀的不透明串（snapshot-<32 hex> / export-<32 hex>，41 字符），
-- 所以这四列一律 varchar(64)，不是 varchar(36)。
CREATE TABLE lingdoc_release_snapshots (
    id varchar(64) PRIMARY KEY,
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id),
    frozen_input_json text NOT NULL,
    snapshot_digest varchar(64) NOT NULL,
    check_json text NOT NULL,
    is_current boolean NOT NULL,
    created_at timestamptz NOT NULL,
    action_actor_id varchar(128),
    action_key varchar(128),
    action_request_hash varchar(64),
    CONSTRAINT uq_lingdoc_release_snapshot_action UNIQUE (project_id, action_actor_id, action_key)
);
CREATE INDEX idx_lingdoc_release_snapshots_project
    ON lingdoc_release_snapshots(project_id, created_at);

CREATE TABLE lingdoc_export_artifacts (
    id varchar(64) PRIMARY KEY,
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id),
    snapshot_id varchar(64) NOT NULL REFERENCES lingdoc_release_snapshots(id),
    status varchar(16) NOT NULL CHECK (status IN ('verified', 'failed')),
    file_sha256 varchar(64) NOT NULL DEFAULT '',
    failure_code varchar(64) NOT NULL DEFAULT '',
    file_blob bytea,
    created_at timestamptz NOT NULL,
    action_actor_id varchar(128),
    action_key varchar(128),
    action_request_hash varchar(64),
    CONSTRAINT uq_lingdoc_export_artifact_action UNIQUE (project_id, action_actor_id, action_key)
);
CREATE INDEX idx_lingdoc_export_artifacts_project
    ON lingdoc_export_artifacts(project_id, created_at);
