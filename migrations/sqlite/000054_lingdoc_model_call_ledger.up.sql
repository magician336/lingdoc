-- 55-Spec 模型调用账本（#453）：一次模型调用一行，一次模型切换一行。
--
-- 列形状沿用 lingdoc_rule_suggestion_runs（本仓已验证的「一次模型调用一行」先例），
-- 并在其上扩展 provider、参数与预算三组列。两行种放在一张表里、用 kind 区分：
-- 模型切换与它引发的后续调用是同一件事的两面，拆成两张表就得跨表 join 才能回答
-- 「这次调用用的模型为什么换了」。
--
-- kind 的取值只有两个，写死在 CHECK 里：它是这张表的形状，不是会长的词表。
-- run_mode 故意不加 CHECK：那一列的词表归 candidateadoption 所有，在这里再抄
-- 一份就是第二份同值词表（55-Spec 决策 5）。
--
-- switched_from_model_id / switch_reason / affected_steps_json 只对 kind='switch'
-- 有意义，这个不变式由写入端（Go 模块）守，不写成 CHECK：写成 CHECK 就得把「空」
-- 的两种表示（NULL 与 ''）都算进去，约束会比它要守的规则本身还复杂。
--
-- 其余列的可空性按表契约：只有 tenant_id、kind、status、created_at 是 NOT NULL，
-- 写入端一律写实际值（不写 NULL），读库的一侧不必处理 NULL。

CREATE TABLE lingdoc_model_call_ledger (
    id TEXT PRIMARY KEY NOT NULL,
    tenant_id INTEGER NOT NULL,
    project_id TEXT,
    kind TEXT NOT NULL CHECK (kind IN ('call', 'switch')),
    provider TEXT,
    model_id TEXT,
    run_mode TEXT,
    parameters_json TEXT,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    reserved_nano_usd INTEGER NOT NULL DEFAULT 0,
    actual_nano_usd INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    error_code TEXT,
    source_kind TEXT,
    source_id TEXT,
    switched_from_model_id TEXT,
    switch_reason TEXT,
    affected_steps_json TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_lingdoc_model_call_source
    ON lingdoc_model_call_ledger(tenant_id, source_kind, source_id, created_at);
CREATE INDEX idx_lingdoc_model_call_project
    ON lingdoc_model_call_ledger(tenant_id, project_id, kind, created_at);
CREATE INDEX idx_lingdoc_model_call_mode
    ON lingdoc_model_call_ledger(tenant_id, run_mode, created_at);
