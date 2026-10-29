CREATE TABLE IF NOT EXISTS chunk_origin_sha256 (
    chunk_id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_id TEXT NOT NULL,
    sha256 TEXT NOT NULL
);
