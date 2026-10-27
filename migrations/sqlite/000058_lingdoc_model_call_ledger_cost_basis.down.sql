-- 回退 #527 的语义列：删列即删掉这一段口径事实，没有行被改写。
-- 列删掉后，金额列的 0 回到两种读法无法区分的状态（回到 #527 修复前）。

ALTER TABLE lingdoc_model_call_ledger
    DROP COLUMN cost_basis;
