-- 回退：把审计动作分类列变回无界 TEXT（#276 的 down）。
--
-- 不需要拒绝回退：旧 schema 的无界列能表示新 schema 的每一行，语义更宽而不是更窄，
-- 回退不会让任何一行变得无法表示。
--
-- 审计历史不改写：规范化过的分类值不会回写成旧格式。完整能力一直在 details_json 的
-- `capability` 字段里，分类列回退成无界也不需要把它补回去。
CREATE TABLE lingdoc_project_audits_previous (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    actor_id TEXT NOT NULL,
    action TEXT NOT NULL,
    target TEXT NOT NULL,
    details_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO lingdoc_project_audits_previous (
    id, project_id, actor_id, action, target, details_json, created_at
)
SELECT
    id, project_id, actor_id, action, target, details_json, created_at
FROM lingdoc_project_audits;

DROP TABLE lingdoc_project_audits;
ALTER TABLE lingdoc_project_audits_previous RENAME TO lingdoc_project_audits;
CREATE INDEX idx_lingdoc_project_audits_project ON lingdoc_project_audits(project_id, created_at, id);
