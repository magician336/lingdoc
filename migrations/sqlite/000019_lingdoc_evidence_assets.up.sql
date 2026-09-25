-- LingDoc evidence domain: which assets a project may use, and which revision
-- of each asset a reference to it means. Mirrors the versioned migration
-- 000098; keep the two in step.
--
-- Empty string means "unknown" for knowledge_base_id/created_by, never NULL:
-- readers stay free of NULL handling. deleted_at stays nullable -- it is a
-- point in time, not an unknown value.
CREATE TABLE lingdoc_project_assets (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    project_id TEXT NOT NULL,
    origin TEXT NOT NULL DEFAULT 'weknora'
        CHECK (origin IN ('weknora', 'academic', 'conversation', 'upload')),
    knowledge_base_id TEXT NOT NULL DEFAULT '',
    knowledge_id TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    asset_revision INTEGER NOT NULL DEFAULT 1 CHECK (asset_revision >= 1),
    processing_state TEXT NOT NULL DEFAULT 'pending'
        CHECK (processing_state IN ('pending', 'processing', 'ready', 'failed', 'replaced')),
    created_by TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

-- One row per (project, knowledge): binding the same knowledge twice returns
-- the existing asset rather than a second one, and a concurrent duplicate
-- collides here instead of writing twice.
CREATE UNIQUE INDEX idx_lingdoc_project_assets_binding
    ON lingdoc_project_assets (project_id, knowledge_id);
CREATE INDEX idx_lingdoc_project_assets_scope
    ON lingdoc_project_assets (tenant_id, project_id);

CREATE TABLE lingdoc_asset_revisions (
    id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL REFERENCES lingdoc_project_assets(id) ON DELETE CASCADE,
    revision_no INTEGER NOT NULL CHECK (revision_no >= 1),
    weknora_knowledge_id TEXT NOT NULL DEFAULT '',
    content_hash TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'superseded', 'failed', 'revoked')),
    metadata_json TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (asset_id, revision_no)
);

-- 不给 (asset_id, revision_no) 再建一条普通索引：上面那条 UNIQUE 就是同列的
-- btree，精确查某一版、按 asset_id 范围取某份资料的全部修订都走它；
-- 再建一条只是重复开销（模型侧也没有它，两处必须一致）。
