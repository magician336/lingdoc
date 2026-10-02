-- Expand-contract sidecar for LingDoc project capability and scope data.
-- The legacy lingdoc_members(role) table remains readable during migration.
CREATE TABLE IF NOT EXISTS lingdoc_member_permissions (
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    governance_role TEXT NOT NULL CHECK (governance_role IN ('owner', 'admin', 'member')),
    function_roles_json TEXT NOT NULL DEFAULT '[]',
    function_scopes_json TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'invited', 'suspended', 'removed', 'suspended_by_tenant')),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_lingdoc_member_permissions_user
    ON lingdoc_member_permissions (user_id, project_id);
