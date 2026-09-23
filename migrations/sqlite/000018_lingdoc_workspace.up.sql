CREATE TABLE lingdoc_projects (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('draft', 'active')),
    project_version INTEGER NOT NULL CHECK (project_version >= 1),
    spec_revision INTEGER NOT NULL CHECK (spec_revision >= 0),
    spec_json TEXT NOT NULL DEFAULT '{}',
    template_id TEXT NOT NULL,
    template_version TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_lingdoc_projects_tenant ON lingdoc_projects (tenant_id, created_at DESC);

CREATE TABLE lingdoc_members (
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('owner', 'collaborator')),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX idx_lingdoc_members_user ON lingdoc_members (user_id, project_id);

CREATE TABLE lingdoc_chapters (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    section_id TEXT NOT NULL,
    title TEXT NOT NULL,
    current_version_id TEXT,
    UNIQUE (project_id, section_id)
);

CREATE TABLE lingdoc_chapter_versions (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    chapter_id TEXT NOT NULL REFERENCES lingdoc_chapters(id) ON DELETE CASCADE,
    parent_version_id TEXT,
    body_markdown TEXT NOT NULL,
    source_ids_json TEXT NOT NULL DEFAULT '[]',
    review_items_json TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_lingdoc_versions_chapter ON lingdoc_chapter_versions (chapter_id, created_at DESC);

CREATE TABLE lingdoc_operations (
    tenant_id INTEGER NOT NULL,
    user_id TEXT NOT NULL,
    operation TEXT NOT NULL,
    target TEXT NOT NULL,
    key TEXT NOT NULL,
    body_hash TEXT NOT NULL,
    response_json TEXT NOT NULL,
    response_code INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, user_id, operation, target, key)
);
