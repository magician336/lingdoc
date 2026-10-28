-- #769：回放夹具登记地基（Spec #768 切片一）。
--
-- 租户隔离的 append-only 回放夹具表：主键把每条夹具钉在登记时试放过的那个
-- 版本身份（content_hash + policy_hash）上；草稿重存产生新内容摘要后，旧行
-- 自然失活（读取与 Verify 只认当前 pin 匹配的行），没有任何改/删路径——
-- 「改」= 对当前 pin 重新登记。内置 demo 夹具保持代码定义，不经本表。
--
-- 列长度拼写在 sqlite 链为 TEXT（SQLite 不强制宽度），versioned（PG）链的
-- VARCHAR 宽度与 gorm 声明（templategovernance.fixtureRow 的 size: 标签）
-- 一一对应，由表形状守护双门比对。

CREATE TABLE lingdoc_template_replay_fixtures (
 tenant_id INTEGER NOT NULL, template_id TEXT NOT NULL, version TEXT NOT NULL,
 content_hash TEXT NOT NULL, fixture_id TEXT NOT NULL,
 ruleset_id TEXT NOT NULL, ruleset_version TEXT NOT NULL, ruleset_hash TEXT NOT NULL,
 policy_hash TEXT NOT NULL, rule_id TEXT NOT NULL, scene TEXT NOT NULL,
 polarity TEXT NOT NULL CHECK(polarity IN ('positive','negative')),
 target_ref TEXT NOT NULL, input_json TEXT NOT NULL, expected_status TEXT NOT NULL,
 reviewed_by TEXT NOT NULL, review_reference TEXT NOT NULL, reviewed_at DATETIME NOT NULL,
 created_by TEXT NOT NULL, created_at DATETIME NOT NULL,
 PRIMARY KEY(tenant_id, template_id, version, content_hash, fixture_id)
);
CREATE INDEX idx_lingdoc_template_replay_fixtures ON lingdoc_template_replay_fixtures(tenant_id,fixture_id);
