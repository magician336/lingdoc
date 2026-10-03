CREATE TABLE lingdoc_owner_transfers (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    from_user_id TEXT NOT NULL,
    to_user_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'cancelled')),
    expected_project_version INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    accepted_at DATETIME,
    CHECK (from_user_id <> to_user_id)
);
CREATE INDEX idx_lingdoc_owner_transfers_project ON lingdoc_owner_transfers(project_id, status, created_at);
CREATE UNIQUE INDEX idx_lingdoc_owner_transfers_pending ON lingdoc_owner_transfers(project_id) WHERE status = 'pending';
