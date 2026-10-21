-- 审计动作分类列在两种方言上都强制有界（#276）。
--
-- PostgreSQL 侧这一列已经是 varchar(64)：写入时就拒超长值（SQLSTATE 22001），
-- #253 那个「合法请求得到 500」的缺陷在 PG 上现形。SQLite 侧此前是无界 TEXT，
-- 同一类缺陷在开发机上完全看不见（SQLite 迁移 000046 现在补上了同样的边界）。
--
-- 这里再加一条同名显式约束，是为了让两种方言把**同一条规则写在同一个名字下**：
-- 读 schema 就能看到边界，不必知道哪个方言用类型、哪个方言用表约束。
-- varchar(64) 已经保证没有历史行违反这条约束，所以这条 ADD CONSTRAINT 不会失败。
--
-- 规则用字符数而不是字节数（length 与 varchar 的计数口径一致）：两种方言都在字符
-- 意义上界住分类值。
ALTER TABLE lingdoc_project_audits
    ADD CONSTRAINT ck_lingdoc_project_audits_action_len CHECK (length(action) <= 64);
