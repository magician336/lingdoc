-- LingDoc evidence domain: which assets a project may use, and which revision
-- of each asset a reference to it means.
--
-- Tables follow 04-数据库设计与开发方案.md 4.2. Three of that table's columns
-- are deliberately absent -- external_ref, authorization_scope,
-- processing_run_ref -- because nothing in this task can write or read them
-- yet; they arrive with the task that first does. `title` goes the other way:
-- the design table omits it, but contracts/openapi.json marks it required on
-- Asset, so it is snapshotted from the knowledge at bind time. A rename in
-- WeKnora therefore does not follow through here (no event channel yet).
--
-- Empty string means "unknown" for knowledge_base_id/created_by, never NULL:
-- readers stay free of NULL handling. deleted_at stays nullable -- it is a
-- point in time, not an unknown value.
CREATE TABLE lingdoc_project_assets (
    id varchar(36) PRIMARY KEY,
    tenant_id bigint NOT NULL,
    project_id varchar(36) NOT NULL,
    origin varchar(32) NOT NULL DEFAULT 'weknora'
        CHECK (origin IN ('weknora', 'academic', 'conversation', 'upload')),
    knowledge_base_id varchar(36) NOT NULL DEFAULT '',
    knowledge_id varchar(36) NOT NULL,
    title varchar(255) NOT NULL DEFAULT '',
    asset_revision bigint NOT NULL DEFAULT 1 CHECK (asset_revision >= 1),
    processing_state varchar(32) NOT NULL DEFAULT 'pending'
        CHECK (processing_state IN ('pending', 'processing', 'ready', 'failed', 'replaced')),
    created_by varchar(36) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at timestamptz
);

-- One row per (project, knowledge): binding the same knowledge twice returns
-- the existing asset rather than a second one, and a concurrent duplicate
-- collides here instead of writing twice. It also blocks re-binding a
-- soft-deleted row, which is correct until unbind exists to define what
-- re-binding after an unbind should mean.
CREATE UNIQUE INDEX idx_lingdoc_project_assets_binding
    ON lingdoc_project_assets (project_id, knowledge_id);
CREATE INDEX idx_lingdoc_project_assets_scope
    ON lingdoc_project_assets (tenant_id, project_id);

CREATE TABLE lingdoc_asset_revisions (
    id varchar(36) PRIMARY KEY,
    asset_id varchar(36) NOT NULL REFERENCES lingdoc_project_assets(id) ON DELETE CASCADE,
    revision_no bigint NOT NULL CHECK (revision_no >= 1),
    weknora_knowledge_id varchar(36) NOT NULL DEFAULT '',
    content_hash varchar(64) NOT NULL DEFAULT '',
    status varchar(32) NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'superseded', 'failed', 'revoked')),
    metadata_json text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (asset_id, revision_no)
);

-- 不给 (asset_id, revision_no) 再建一条普通索引：上面那条 UNIQUE 就是同列的
-- btree，精确查某一版、按 asset_id 范围取某份资料的全部修订都走它；
-- 再建一条只是重复开销（模型侧也没有它，两处必须一致）。

-- Rows are never deleted, only superseded: a frozen chapter or a confirmed
-- evidence card has to be able to point back at the revision it was built on.
-- The current revision is the asset's asset_revision; no second pointer is
-- kept, so the two cannot drift.
--
-- Of the four statuses above, this task writes only `active` and
-- `superseded`. `revoked` is not written: whether a reader still has access is
-- answered by asking authorization at read time, never by a stored flag that
-- could go stale when access is granted again.
