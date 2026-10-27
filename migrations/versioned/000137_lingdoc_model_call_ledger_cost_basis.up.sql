-- #527：模型调用账本的金额列补上语义列。
--
-- reserved_nano_usd / actual_nano_usd 的 0 有两种读法——「这次调用免费」和
-- 「这次调用没有核算金额」——表里没有任何一列能区分两者。受控 Qwen profile
-- 没有价格基础（acceptancebudget 的 CostBasisNotApplicable），它在两列写 0，
-- 单读 DB 行会把「未核算」读成「免费」，违反「未核算 ≠ 0」的记账纪律。
--
-- cost_basis 承载金额列的口径，词表归 acceptancebudget 所有（JSON 账本已在用
-- 同一组值）：not_applicable＝金额未核算；deepseek_peak_usd_2026_09_29 与
-- qwen_embedding_nano_cny_2026_10_25＝按该价格口径核算。这里不写 CHECK：
-- 在迁移里再抄一份词表就是第二份同值词表（55-Spec 决策 5 的先例），由写入端
-- （Go 模块）携带 acceptancebudget 的值。
--
-- 旧行（本列加入前写入的）保持 NULL：无法判定它们当时的口径，回填任何
-- 「已核算」值都会让旧行看起来像核算过。NULL 读作「口径未知」，单独引用
-- 旧行的金额列不得作为成本结论。

ALTER TABLE lingdoc_model_call_ledger
    ADD COLUMN cost_basis VARCHAR(32);
