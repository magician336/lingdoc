CREATE TABLE IF NOT EXISTS chunk_pdf_page (
    chunk_id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_id TEXT NOT NULL,
    page INTEGER NOT NULL CHECK (page > 0),
    text_sha256 TEXT NOT NULL
);
