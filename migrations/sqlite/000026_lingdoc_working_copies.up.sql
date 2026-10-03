CREATE TABLE lingdoc_working_copies (
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    chapter_id TEXT NOT NULL REFERENCES lingdoc_chapters(id) ON DELETE CASCADE,
    base_chapter_version_id TEXT,
    spec_revision INTEGER NOT NULL CHECK (spec_revision >= 0),
    working_copy_revision INTEGER NOT NULL CHECK (working_copy_revision >= 1),
    body_markdown TEXT NOT NULL DEFAULT '',
    source_ids_json TEXT NOT NULL DEFAULT '[]',
    review_items_json TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (project_id, chapter_id)
);

INSERT INTO lingdoc_working_copies (
    project_id, chapter_id, base_chapter_version_id, spec_revision,
    working_copy_revision, body_markdown, source_ids_json, review_items_json
)
SELECT c.project_id, c.id, c.current_version_id, p.spec_revision, 1,
       COALESCE(v.body_markdown, ''), COALESCE(v.source_ids_json, '[]'),
       COALESCE(v.review_items_json, '[]')
FROM lingdoc_chapters AS c
JOIN lingdoc_projects AS p ON p.id = c.project_id
LEFT JOIN lingdoc_chapter_versions AS v
       ON v.id = c.current_version_id AND v.project_id = c.project_id AND v.chapter_id = c.id;

CREATE INDEX idx_lingdoc_working_copies_updated
    ON lingdoc_working_copies (project_id, updated_at DESC);
