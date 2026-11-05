CREATE TABLE lingdoc_projects (
    id varchar(36) PRIMARY KEY,
    tenant_id bigint NOT NULL,
    name varchar(120) NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('draft', 'active')),
    project_version bigint NOT NULL CHECK (project_version >= 1),
    spec_revision bigint NOT NULL CHECK (spec_revision >= 0),
    spec_json text NOT NULL DEFAULT '{}',
    template_id varchar(80) NOT NULL,
    template_version varchar(40) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_lingdoc_projects_tenant ON lingdoc_projects (tenant_id, created_at DESC);

CREATE TABLE lingdoc_members (
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    user_id varchar(64) NOT NULL,
    role varchar(20) NOT NULL CHECK (role IN ('owner', 'collaborator')),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX idx_lingdoc_members_user ON lingdoc_members (user_id, project_id);

CREATE TABLE lingdoc_chapters (
    id varchar(36) PRIMARY KEY,
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    section_id varchar(80) NOT NULL,
    title varchar(120) NOT NULL,
    current_version_id varchar(36),
    UNIQUE (project_id, section_id)
);

CREATE TABLE lingdoc_chapter_versions (
    id varchar(36) PRIMARY KEY,
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    chapter_id varchar(36) NOT NULL REFERENCES lingdoc_chapters(id) ON DELETE CASCADE,
    parent_version_id varchar(36),
    body_markdown text NOT NULL,
    source_ids_json text NOT NULL DEFAULT '[]',
    review_items_json text NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_lingdoc_versions_chapter ON lingdoc_chapter_versions (chapter_id, created_at DESC);

CREATE TABLE lingdoc_operations (
    tenant_id bigint NOT NULL,
    user_id varchar(64) NOT NULL,
    operation varchar(40) NOT NULL,
    target varchar(100) NOT NULL,
    key varchar(128) NOT NULL,
    body_hash varchar(64) NOT NULL,
    response_json text NOT NULL,
    response_code integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, user_id, operation, target, key)
);
