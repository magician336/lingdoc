-- 回退把模型调用账本整张表删掉。
--
-- 账本记的是「发生过什么」，不是配置：删表就是删掉这段历史，没有可回退的
-- 形状迁移要做（这张表从建立起就是当前形状，也没有改过 CHECK 约束）。

DROP INDEX IF EXISTS idx_lingdoc_model_call_mode;
DROP INDEX IF EXISTS idx_lingdoc_model_call_project;
DROP INDEX IF EXISTS idx_lingdoc_model_call_source;
DROP TABLE IF EXISTS lingdoc_model_call_ledger;
