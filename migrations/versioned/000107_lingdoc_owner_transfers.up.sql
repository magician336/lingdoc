CREATE TABLE lingdoc_owner_transfers (
    id varchar(36) PRIMARY KEY,
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    from_user_id varchar(64) NOT NULL,
    to_user_id varchar(64) NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('pending', 'accepted', 'cancelled')),
    expected_project_version bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    accepted_at timestamptz,
    CHECK (from_user_id <> to_user_id)
);
CREATE INDEX idx_lingdoc_owner_transfers_project ON lingdoc_owner_transfers(project_id, status, created_at);
CREATE UNIQUE INDEX idx_lingdoc_owner_transfers_pending ON lingdoc_owner_transfers(project_id) WHERE status = 'pending';
