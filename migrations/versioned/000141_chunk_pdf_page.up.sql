CREATE TABLE IF NOT EXISTS chunk_pdf_page (
    chunk_id varchar(36) PRIMARY KEY,
    tenant_id bigint NOT NULL,
    knowledge_id varchar(36) NOT NULL,
    page integer NOT NULL CHECK (page > 0),
    text_sha256 varchar(64) NOT NULL
);
