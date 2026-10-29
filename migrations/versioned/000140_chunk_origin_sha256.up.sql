CREATE TABLE IF NOT EXISTS chunk_origin_sha256 (
    chunk_id varchar(36) PRIMARY KEY,
    tenant_id bigint NOT NULL,
    knowledge_id varchar(36) NOT NULL,
    sha256 varchar(64) NOT NULL
);
