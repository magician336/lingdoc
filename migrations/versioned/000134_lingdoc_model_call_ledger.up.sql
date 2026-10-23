-- 55-Spec 模型调用账本（#453）：一次模型调用一行，一次模型切换一行。
--
-- 与 SQLite 侧同一张表、同一组列名、同一语义；类型按既有方言约定改写：
-- id TEXT→UUID、tenant_id INTEGER→BIGINT、JSON 文本→JSONB、时间 DATETIME→TIMESTAMPTZ。
--
-- kind 的取值只有两个，写死在 CHECK 里：它是这张表的形状，不是会长的词表。
-- run_mode 故意不加 CHECK：那一列的词表归 candidateadoption 所有，在这里再抄
-- 一份就是第二份同值词表（55-Spec 决策 5）。
--
-- switched_from_model_id / switch_reason / affected_steps_json 只对 kind='switch'
-- 有意义，这个不变式由写入端（Go 模块）守，不写成 CHECK。

CREATE TABLE IF NOT EXISTS lingdoc_model_call_ledger (
    id UUID PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    project_id VARCHAR(36),
    kind VARCHAR(16) NOT NULL CHECK (kind IN ('call', 'switch')),
    provider VARCHAR(64),
    model_id VARCHAR(128),
    run_mode VARCHAR(32),
    parameters_json JSONB,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    reserved_nano_usd BIGINT NOT NULL DEFAULT 0,
    actual_nano_usd BIGINT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL,
    error_code VARCHAR(64),
    source_kind VARCHAR(32),
    source_id VARCHAR(64),
    switched_from_model_id VARCHAR(128),
    switch_reason VARCHAR(500),
    affected_steps_json JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_lingdoc_model_call_source
    ON lingdoc_model_call_ledger(tenant_id, source_kind, source_id, created_at);
CREATE INDEX IF NOT EXISTS idx_lingdoc_model_call_project
    ON lingdoc_model_call_ledger(tenant_id, project_id, kind, created_at);
CREATE INDEX IF NOT EXISTS idx_lingdoc_model_call_mode
    ON lingdoc_model_call_ledger(tenant_id, run_mode, created_at);
