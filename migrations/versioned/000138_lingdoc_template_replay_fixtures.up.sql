-- #769：回放夹具登记地基（Spec #768 切片一）。与 sqlite 链 000059 同一语义；
-- 类型按既有方言约定改写：tenant_id INTEGER→BIGINT、文本列 TEXT→VARCHAR(宽度)
-- 或 TEXT（JSON 输入）、时间 DATETIME→TIMESTAMPTZ。
--
-- 租户隔离的 append-only 回放夹具表：主键把每条夹具钉在登记时试放过的那个
-- 版本身份（content_hash + policy_hash）上；草稿重存产生新内容摘要后，旧行
-- 自然失活（读取与 Verify 只认当前 pin 匹配的行），没有任何改/删路径——
-- 「改」= 对当前 pin 重新登记。内置 demo 夹具保持代码定义，不经本表。
--
-- VARCHAR 宽度与 gorm 声明（templategovernance.fixtureRow 的 size: 标签）
-- 一一对应，由表形状守护双门比对（PostgreSQL 侧强制长度相等）。

CREATE TABLE lingdoc_template_replay_fixtures (
 tenant_id BIGINT NOT NULL, template_id VARCHAR(80) NOT NULL, version VARCHAR(64) NOT NULL,
 content_hash VARCHAR(64) NOT NULL, fixture_id VARCHAR(80) NOT NULL,
 ruleset_id VARCHAR(80) NOT NULL, ruleset_version VARCHAR(64) NOT NULL, ruleset_hash VARCHAR(64) NOT NULL,
 policy_hash VARCHAR(64) NOT NULL, rule_id VARCHAR(80) NOT NULL, scene VARCHAR(32) NOT NULL,
 polarity VARCHAR(16) NOT NULL CHECK(polarity IN ('positive','negative')),
 target_ref VARCHAR(180) NOT NULL, input_json TEXT NOT NULL, expected_status VARCHAR(16) NOT NULL,
 reviewed_by VARCHAR(64) NOT NULL, review_reference VARCHAR(255) NOT NULL, reviewed_at TIMESTAMPTZ NOT NULL,
 created_by VARCHAR(64) NOT NULL, created_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(tenant_id, template_id, version, content_hash, fixture_id)
);
CREATE INDEX idx_lingdoc_template_replay_fixtures ON lingdoc_template_replay_fixtures(tenant_id,fixture_id);
