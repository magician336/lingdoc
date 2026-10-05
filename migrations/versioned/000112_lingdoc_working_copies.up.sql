CREATE TABLE lingdoc_working_copies (
    project_id varchar(36) NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    chapter_id varchar(36) NOT NULL REFERENCES lingdoc_chapters(id) ON DELETE CASCADE,
    base_chapter_version_id varchar(36),
    spec_revision bigint NOT NULL CHECK (spec_revision >= 0),
    working_copy_revision bigint NOT NULL CHECK (working_copy_revision >= 1),
    body_markdown text NOT NULL DEFAULT '',
    source_ids_json text NOT NULL DEFAULT '[]',
    citation_usages_json TEXT NOT NULL DEFAULT '[]',
    review_items_json TEXT NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (project_id, chapter_id)
);

INSERT INTO lingdoc_working_copies (
    project_id, chapter_id, base_chapter_version_id, spec_revision,
    working_copy_revision, body_markdown, source_ids_json, citation_usages_json, review_items_json
)
SELECT c.project_id, c.id, c.current_version_id, p.spec_revision, 1,
       COALESCE(v.body_markdown, ''), COALESCE(v.source_ids_json, '[]'),
       COALESCE(v.citation_usages_json, '[]'), COALESCE(v.review_items_json, '[]')
FROM lingdoc_chapters AS c
JOIN lingdoc_projects AS p ON p.id = c.project_id
LEFT JOIN lingdoc_chapter_versions AS v
       ON v.id = c.current_version_id AND v.project_id = c.project_id AND v.chapter_id = c.id;

CREATE INDEX idx_lingdoc_working_copies_updated
    ON lingdoc_working_copies (project_id, updated_at DESC);
