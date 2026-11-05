ALTER TABLE lingdoc_chapter_versions
    ADD COLUMN citation_usages_json TEXT NOT NULL DEFAULT '[]';
