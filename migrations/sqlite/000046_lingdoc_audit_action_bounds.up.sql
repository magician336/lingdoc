-- 审计动作分类列在两种方言上都强制有界（#276）。
--
-- PostgreSQL 侧这一列一直是 varchar(64)：超长动作在 PG 上被拒写（SQLSTATE 22001），
-- #253 那个「合法请求得到 500」的缺陷在 PG 上现形。SQLite 不强制 VARCHAR 长度，
-- 这一列此前是无界 TEXT —— 同样的超长动作被静静写进去，缺陷在开发机上完全看不见。
-- 本迁移让两种方言对同一条规则表达一致。
--
-- SQLite 改列定义只能重建表：建新表 → 搬数据 → 删旧表 → 改名 → 重建索引。
--
-- 搬数据不是原样复制。旧写入方（#253 修复前，aa9bc394 的父提交）把完整能力写进
-- 分类列（`authorization.<种类>:<资源>`，最长 73 字符），这类行**必须**先规范化成
-- `authorization.<种类>` 才装得进有界的列。完整能力没有丢：旧写入方本来也把它写进
-- 了 details_json 的 `capability` 字段，这里原样保留。
--
-- 无法规范化的超长行（不是旧授权格式）会让本迁移失败并把库标成 dirty。那是刻意的：
-- 审计历史不静默截断。要处置的结论与建议见
-- `docs/08-本轮实施方案/T276-验证报告.json`。
CREATE TABLE lingdoc_project_audits_next (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES lingdoc_projects(id) ON DELETE CASCADE,
    actor_id TEXT NOT NULL,
    action TEXT NOT NULL CHECK (length(action) <= 64),
    target TEXT NOT NULL,
    details_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO lingdoc_project_audits_next (
    id, project_id, actor_id, action, target, details_json, created_at
)
SELECT
    id, project_id, actor_id,
    CASE
        WHEN length(action) > 64 AND action LIKE 'authorization.%:%'
            THEN substr(action, 1, instr(action, ':') - 1)
        ELSE action
    END,
    target, details_json, created_at
FROM lingdoc_project_audits;

DROP TABLE lingdoc_project_audits;
ALTER TABLE lingdoc_project_audits_next RENAME TO lingdoc_project_audits;
CREATE INDEX idx_lingdoc_project_audits_project ON lingdoc_project_audits(project_id, created_at, id);
