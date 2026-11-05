-- T13/T14: 交付快照与导出产物的持久化。
--
-- 两张表放同一份迁移，是因为产物表的 snapshot_id 指向快照表：分开写就有个中间
-- 状态——产物表建好了、它要指向的那张还没有。
CREATE TABLE lingdoc_release_snapshots (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    -- 冻结输入整份落成一列 JSON：它是**不可变**的，读回来只为了重算摘要与交给渲染器。
    -- 按字段拆列只会让「读回来的那份与冻结的那份不是同一个东西」多出 N 个发生的机会。
    frozen_input_json TEXT NOT NULL,
    snapshot_digest TEXT NOT NULL,
    check_json TEXT NOT NULL,
    -- is_current 必须落列：导出前要读它决定能不能导出。它存的是**冻结那一刻**的
    -- 判断，读出来就是历史事实；导出之所以安全，是因为它**另外**还要问一次此刻。
    is_current INTEGER NOT NULL,
    -- 不给 DEFAULT：这一列是记录含义的一部分（注入的时钟冻下的那个瞬间），
    -- 让它悄悄落成 CURRENT_TIMESTAMP，会把「写漏了」变成一个看不出错的值。
    created_at DATETIME NOT NULL,
    -- 动作记录并进同一张表。三列可空——Save 落下的快照不属于任何一次动作，
    -- 而 NULL 在 UNIQUE 索引里互不相等（SQLite 与 PostgreSQL 同一条），
    -- 所以这些行不会互相撞车。并表的理由是这个不变式：索引与实体在**同一行**上，
    -- 内存实现要靠一次加锁写入手工维持的「索引在、实体不在」半截状态，
    -- 在这里结构上不可能存在。
    action_actor_id TEXT,
    action_key TEXT,
    action_request_hash TEXT,
    UNIQUE (project_id, action_actor_id, action_key),
    FOREIGN KEY (project_id) REFERENCES lingdoc_projects(id)
);
CREATE INDEX idx_lingdoc_release_snapshots_project ON lingdoc_release_snapshots (project_id, created_at);

CREATE TABLE lingdoc_export_artifacts (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    snapshot_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('verified', 'failed')),
    file_sha256 TEXT NOT NULL DEFAULT '',
    failure_code TEXT NOT NULL DEFAULT '',
    -- 字节直接进这列：对象存储不在本轮范围，而「文件已校验通过」与「文件读得回来」
    -- 必须是同一件事。放到盘上的另一处，就多出一个「库里有记录、盘上没文件」的状态，
    -- 而下载会把它答成一句不可解释的失败。列表用**不带这列的投影**读。
    file_blob BLOB,
    created_at DATETIME NOT NULL,
    action_actor_id TEXT,
    action_key TEXT,
    action_request_hash TEXT,
    UNIQUE (project_id, action_actor_id, action_key),
    FOREIGN KEY (project_id) REFERENCES lingdoc_projects(id),
    FOREIGN KEY (snapshot_id) REFERENCES lingdoc_release_snapshots(id)
);
CREATE INDEX idx_lingdoc_export_artifacts_project ON lingdoc_export_artifacts (project_id, created_at);
