CREATE TABLE lingdoc_academic_original_snapshots (
    tenant_id INTEGER NOT NULL CHECK (tenant_id > 0),
    project_id TEXT NOT NULL CHECK (length(project_id) > 0),
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
    size INTEGER NOT NULL CHECK (size > 0 AND size <= 8388608),
    content BLOB NOT NULL CHECK (length(content) = size),
    PRIMARY KEY (tenant_id, project_id, sha256)
);

CREATE TRIGGER lingdoc_academic_original_snapshots_no_update
BEFORE UPDATE ON lingdoc_academic_original_snapshots
BEGIN
    SELECT RAISE(ABORT, 'academic original snapshots cannot be updated');
END;

-- SQLite REPLACE deletes and reinserts without firing UPDATE triggers.
-- Permit an identical insert for deduplication, but never replace its bytes.
CREATE TRIGGER lingdoc_academic_original_snapshots_no_replace
BEFORE INSERT ON lingdoc_academic_original_snapshots
WHEN EXISTS (
    SELECT 1 FROM lingdoc_academic_original_snapshots
    WHERE tenant_id = NEW.tenant_id AND project_id = NEW.project_id AND sha256 = NEW.sha256
      AND (size != NEW.size OR content != NEW.content)
)
BEGIN
    SELECT RAISE(ABORT, 'academic original snapshots cannot be replaced');
END;
