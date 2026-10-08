ALTER TABLE lingdoc_projects ADD COLUMN template_copy_version bigint NOT NULL DEFAULT 0;

CREATE TABLE lingdoc_project_template_copies (
    id varchar(36) PRIMARY KEY,
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    version bigint NOT NULL CHECK (version > 0),
    source_template_id varchar(80) NOT NULL,
    source_template_version varchar(40) NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('draft', 'bound', 'superseded', 'discarded')),
    content_hash varchar(64) NOT NULL,
    ruleset_hash varchar(64) NOT NULL,
    definition_json text NOT NULL,
    created_by varchar(64) NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (project_id, version)
);

CREATE INDEX idx_lingdoc_project_template_copies_history
    ON lingdoc_project_template_copies (project_id, version DESC);
