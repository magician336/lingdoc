## 补注（收口核验结果 + 一处机制表述更正）

**收口核验**：报告写完后对承重结论做了 42 条机器断言，**42 项通过 / 0 项失败**（`closure-verification.json`）。断言覆盖：两个 chat 账本逐字节未变、本轮 provider 请求数与状态、预算双上限（7,250/8,000 token、2,063,125/8,000,000 nano-CNY）、历史 403 行逐字保留且已结清、目标知识库 2 个 chunk 行逐字未变、章节 `source_ids` 逐字未变且被引用 chunk 全部存在、隔离知识库 2 个向量行维度 1024、`vec_embeddings_1024` 存在、检索命中 2 条且 `match_type=0`、服务端日志中的「keywords count: 0 / vector count: 2 / before fusion: vector=2, keyword=0」与两个余弦距离、6 个 worker 池的 `queue_redis_db=2 queue_db_source=queue-db-env`、Redis DB 1 未被本进程注册、两个队列库 pending task 均为 0、以及证据清单中每一件的 sha256。

**更正上一条评论中的一处机制表述**：重跑知识时，应用的 `DeleteChunksByKnowledgeID` 是 **GORM 软删除**（置 `deleted_at`）而非物理擦除。这个更正**不改变结论、反而使结论更强**：软删除之后应用层按 `deleted_at` 过滤，被章节引用的 chunk 在应用视角即已消失（章节 `source_ids` 悬空、`source-available` 规则翻转），所以「不重跑目标知识库的被引用知识」这一决策成立。

同时补一条**对本轮有利的实测事实**：隔离向量知识库前三轮尝试留下的 **6 个历史 chunk 行仍在库中**（`deleted_at` 已置、`index_status` 仍为 `failed`），即**历史失败痕迹没有被擦除**；本轮唯一的行级删除是应用自身的软删除，无任何物理删除，全轮零删除。

完整报告与全部证据（含 `closure-verification.json`）见 `D:\wt\lingdoc-485-evidence\embedding-run\flash-run\`，报告 `REPORT-embedding-flash-acceptance-20261026.md`。
