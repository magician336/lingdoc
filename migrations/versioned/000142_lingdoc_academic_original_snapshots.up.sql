CREATE TABLE lingdoc_academic_original_snapshots (
    tenant_id bigint NOT NULL CHECK (tenant_id > 0),
    project_id text NOT NULL CHECK (length(project_id) > 0),
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size bigint NOT NULL CHECK (size > 0 AND size <= 8388608),
    content bytea NOT NULL CHECK (octet_length(content) = size),
    PRIMARY KEY (tenant_id, project_id, sha256)
);

CREATE FUNCTION lingdoc_academic_original_snapshots_reject_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'academic original snapshots cannot be updated';
END;
$$;
CREATE TRIGGER lingdoc_academic_original_snapshots_no_update
BEFORE UPDATE ON lingdoc_academic_original_snapshots
FOR EACH ROW EXECUTE FUNCTION lingdoc_academic_original_snapshots_reject_update();
