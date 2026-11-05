CREATE TABLE lingdoc_member_permissions (
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    user_id varchar(64) NOT NULL,
    governance_role varchar(16) NOT NULL CHECK (governance_role IN ('owner', 'admin', 'member')),
    function_roles_json text NOT NULL DEFAULT '[]',
    function_scopes_json text NOT NULL DEFAULT '{}',
    status varchar(24) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'invited', 'suspended', 'removed', 'suspended_by_tenant')),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX idx_lingdoc_member_permissions_user ON lingdoc_member_permissions (user_id, project_id);
