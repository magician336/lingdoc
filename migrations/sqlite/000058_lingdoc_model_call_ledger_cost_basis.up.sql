-- #527：模型调用账本的金额列补上语义列（sqlite 侧，与 versioned 000136 同一语义）。
--
-- reserved_nano_usd / actual_nano_usd 的 0 有两种读法——「这次调用免费」和
-- 「这次调用没有核算金额」——表里没有任何一列能区分两者。受控 Qwen profile
-- 没有价格基础（acceptancebudget 的 CostBasisNotApplicable），它在两列写 0，
-- 单读 DB 行会把「未核算」读成「免费」，违反「未核算 ≠ 0」的记账纪律。
--
-- cost_basis 承载金额列的口径，词表归 acceptancebudget 所有（JSON 账本已在用
-- 同一组值），写入端（Go 模块）携带该包的值，这里不写 CHECK、不抄第二份词表
-- （55-Spec 决策 5 的先例）。SQLite 的 ALTER TABLE ... ADD COLUMN 保留旧行，
-- 旧行该列为 NULL：无法判定它们当时的口径，回填任何「已核算」值都会让旧行
-- 看起来像核算过。NULL 读作「口径未知」，单独引用旧行的金额列不得作为成本结论。

ALTER TABLE lingdoc_model_call_ledger
    ADD COLUMN cost_basis TEXT;
