# 最终 Embedding 验收报告（Flash 模型 · 2026-10-26）

> 结论先行：**本轮真实 Embedding 验收达成**。在负责人授权的硬预算内共发出 **2 次** provider 请求（上限 2），累计账本预留 **7,250 / 8,000 token**（上限 8,000），累计预留费用 **2,063,125 / 8,000,000 nano-CNY ＝ ¥0.002063125**（上限 ¥0.008），**Chat 请求 0 次**，**未授权探测/测试请求 0 次**。
> 2 个 chunk 全部写入真实向量（维度 **1024**），一次真实向量检索命中 2 条，**排序与分数完全来自向量通道**（关键词通道 0 命中）。
> §6 第 6 项（向量检索）此前为 `NOT RUN`、向量行 0；**本轮已具备申请改判 `PASS` 的完整证据**，是否改判由负责人裁定（本报告不自行改判）。

---

## 1. 授权与边界（本轮实际用量）

| 项 | 上限 | 实际 | 判定 |
| --- | --- | --- | --- |
| Provider 请求尝试次数 | ≤ 2 | **2**（账本第 2、3 行） | PASS |
| 累计计费 Token | ≤ 8,000 | **675**（provider 报告：648 + 27）；账本累计**预留** 7,250 | PASS |
| 累计费用 | ≤ ¥0.008（8,000,000 nano-CNY） | 累计**预留** 2,063,125 nano-CNY ＝ **¥0.002063125**；provider 实报用量折算 84,375 nano-CNY ＝ ¥0.000084375 | PASS |
| Chat 请求 | 0 | **0** | PASS |
| 未授权探测/测试请求 | 0 | **0**（本地链路测试用假 transport，零网络） | PASS |

本轮**未**执行且不在授权内：自行充值、改计费模式、关闭「仅免费层」、切换账号/凭据/端点、扩大预算、调用 Chat。额度不足时的处置路径未被触发——本轮 provider 未返回 403。

预算闸门是**机器强制**的，不是自我约束：embedding profile `lingdoc-qwen-embedding-budget-20261025` 的 `cumulative_token_max=8000`、`max_reserve_nano_cny=8,000,000`（＝¥0.008）、`max_calls=4`，窗口当前 `blocked=false`，剩余 750 token 预留额度与 5,936,875 nano-CNY 预留额度仍在窗口内可用。

---

## 2. 执行前强制核验（16 项，逐项实测结论）

| # | 核验项 | 实测结论 | 证据 |
| --- | --- | --- | --- |
| 1 | 代码 head / 票面 | `origin/main=d76ff6a7`（含 #672/#673/#674）；工作树 HEAD=32b22cf9，**HEAD tree 与 origin/main tree 完全相同**（`349b1d99…`），工作树干净；#670/#671 已关闭，#546/#675 开放 | `build_and_start_embedding.ps1` 输出 |
| 2 | embedding 实际模型 ID | 模型行 `lingdoc-embed-qwen3.7-text-embedding-iso` 的 `name`/`display_name` 已为 **`qwen3.7-text-embedding-flash`**；provider 请求体 `model` 字段逐字为该值（账本第 2、3 行 `model` 字段） | `model-row-fix.json`、`phase-a-result.json` |
| 3 | 模型→Provider 传递链 | `models.parameters` → `types.ModelParameters` → `ConfigFromModel` → `OpenAIEmbedder.modelName` → 请求体 `model`；端点守卫 `acceptanceEmbeddingEndpointAllowed` 接受该端点；**2 个 chunk 落在 1 次批量请求内** | `local-chain-test-transcript.txt`（本地零网络） |
| 4 | chat 仍为 qwen3.8-flash | chat 模型行逐字未变（name=qwen3.8-flash、type 不变、parameters 逐字相同）；embedding profile 的允许模型集只有两个 embedding 模型；**DB 账本行数 4 → 4**（全部 `model_id=lingdoc-chat-qwen3.8-flash-iso`），embedding 路径不写 DB 账本 | `model-row-fix.json`、`before-after-comparison.json` |
| 5 | endpoint / 地域 / 模型匹配 | `https://llm-k57eqvdqacn4nov3.cn-beijing.maas.aliyuncs.com/compatible-mode/v1`（https、主机后缀、精确路径、无 userinfo/端口/query/fragment）；实际请求目标 `/compatible-mode/v1/embeddings` | `local-chain-test-transcript.txt`、`retrieval-log-evidence.txt` |
| 6 | `LINGDOC_ASYNQ_REDIS_DB` 实际生效值 | 启动日志 6 个 worker 池**全部**为 `queue_redis_db=2 queue_db_source=queue-db-env`（即来自队列自身设置，而非回落到 `REDIS_DB`）；Redis DB 2 新增 6 条 pid 68840 的 server 注册，DB 1 未受影响 | `startup-log-queue-identity.txt` |
| 7 | enqueue / 6 pool / inspector 队列一致性 | enqueue 客户端与 6 个 worker 池共用 `getAsynqRedisClientOpt()`（单一真相源）；inspector 改读队列自己的库（#670）；**DB 1 与 DB 2 的 pending task 均为 0**，无遗留任务、无跨 profile 消费 | `before-after-comparison.json`、`startup-log-queue-identity.txt` |
| 8 | 向量维度与 vec 表/索引/检索兼容 | provider 返回维度 **1024**；新建 vec0 虚表 `vec_embeddings_1024`（含影子表）；`lite_embeddings` 新增 2 行 `dimension=1024`；检索时 `query_dim=1024` 命中 2 行 | `phase-a-result.json`、`retrieval-log-evidence.txt` |
| 9 | #675 冻结风险 | 本轮 2 次调用**均以 `response_received` 结束且 usage 为 provider 实报**，未落入 `outcome_unknown` / `usage_unknown` / `in_flight` / `cancel_reconciliation_required` / `limit_violation`；窗口未被冻结（`blocked=false`）。风险仍在：若将来出现传输失败或 provider 不报 usage，该窗口仍会永久冻结（#675 未解） | `phase-a-result.json`、`phase-b-result.json` |
| 10 | 材料 sha256 / 2 chunk 归属 | 材料 `synth-chapter-c.md` sha256 = **`001ba50ca631bf8ce6487038cbd91247d8a775eb8fb3e39f22227af613ae06d2`**；被向量化的 2 个 chunk 与目标知识库 2 个被引用 chunk 的内容 sha256、坐标（0-489 / 475-902）、`context_header`、knowledge title 前缀**逐字一致** | `probe_material_and_chunks.py` 输出、`pre-phase-snapshot.json` |
| 11 | 章节绑定与 source_ids | 9 条章节版本的 `source_ids_json` **前后逐字一致**；被引用的 2 个 chunk 仍存在（`chapter_cited_chunks_all_exist=true`）。**source_ids 8/8 PASS 的既有证据完整保留** | `before-after-comparison.json` |
| 12 | chat 账本基线 | `qwen-beta-sqlite.json` sha256 `74e86abc…`、`qwen-beta-sqlite.json.calls.json` sha256 `dd39a0cb…`，**前后逐字节未变** | `before-after-comparison.json` |
| 13 | embedding 账本基线 | 执行前 1 行 / `blocked=true` / sha256 `238ca5bf…`；执行后 3 行 / `blocked=false` / sha256 `a9cf6cda…`；窗口累计预留 7,250 token、2,063,125 nano-CNY | `post-phase-snapshot.json` |
| 14 | DB 账本行数类型 | `lingdoc_model_call_ledger` 4 行，**全部** `kind=call`、`run_mode=real`、`source_kind=generation_run`、`model_id=lingdoc-chat-qwen3.8-flash-iso`；本轮前后均为 4 行，**embedding 调用不写该表** | `post-phase-snapshot.json` |
| 15 | 向量行与索引状态 | 执行前：隔离向量 KB 向量行 **0**，其 6 个历史 chunk 全部 `index_status=failed`（**软删除保留**，未擦除）；执行后：向量行 **2**，2 个新 chunk `index_status=ready`；目标知识库向量行 2 → 2（关键词行，维度 0，未动） | `before-after-comparison.json`、`closure-verification.json` |
| 16 | 后台任务重试/遗留/跨 profile 消费 | 两个队列库的 pending task 均为 0；本轮任务由 pid 68840（embedding profile）在 DB 2 消费；chat profile 实例（pid 59008/80248）在 DB 1，未参与；embedding profile 的允许模型集不含 chat 模型，任何 chat 调用在出网前即被拒 | `before-after-comparison.json`、启动日志 |

---

## 3. 最小修复与本地验证（零网络）

**修复**：隔离环境中 embedding 模型行的 `name` / `display_name` 从 `qwen3.7-text-embedding` 改为授权模型 `qwen3.7-text-embedding-flash`。

- 只改这两列与描述；**`parameters` 逐字未变**（凭据密文、端点、provider、`embedding_parameters` 全部保持），凭据密文前缀仍为 `enc:v1:`，全程未出现明文；
- 行 id 未变 ⇒ 知识库的 `embedding_model_id` 引用与上一轮记录保持一致；
- chat 模型行**未读入写路径**，其 name/type/parameters 逐字未变；
- 不是「只改 .env 就宣称成功」：模型名走 DB 行 → 应用配置 → provider 请求体，链路有本地测试与账本双重佐证。

**本地验证**（`probes/zz_flash_chain_local_test.go`，`package embedding`，跑完即移出工作树，工作树已恢复干净）：

- 直接读隔离库的真实模型行与真实 chunk 行，构造 `types.Model` → `ConfigFromModel`；
- 断言端点守卫接受该端点、embedding profile 接受 flash 且拒绝 `qwen3.8-flash`、chat profile 拒绝 flash；
- 用**假 transport**（零网络）跑 `BatchEmbed`：1 次请求承载 2 个 chunk，请求体 `model=qwen3.7-text-embedding-flash`，URL 为 `/compatible-mode/v1/embeddings`；
- 账本（临时文件）记录 1 行：`status=response_received`、`bound=3085`、`reserved=385,625`（＝3085×125，flash 单价）、`actual=875`（＝7×125）；
- 断言本轮三次预留的累计 = 3085（上一轮已结清）+ 3085（批量）+ 1080（query）= **7250 ≤ 8000**；
- 结论：**PASS**（`local-chain-test-transcript.txt`，`gofmt -l` 无输出、`go vet` 无输出、`go test` PASS）。

---

## 4. 冻结窗口的正规恢复（纯本地账本操作）

上一轮 403 使窗口永久冻结。本轮**没有手改账本、没有切换 acceptance profile、没有绕过预算保护**，而是走刚落库的 #671/#674 恢复通道：

1. 先把冻结账本**原字节复制**到证据目录（sha256 `238ca5bf91331e7761ff529ae712c306f3daac5d1782d72d33e5fe51bb6903ac`，与线上账本逐字节相同）；
2. 生成操作者证据文件（绑定该精确快照、精确请求号、403 状态与 403 原始响应 sha256 `2915d119…`）；
3. 执行 `acceptance-budget-reconcile -profile lingdoc-qwen-embedding-budget-20261025 -apply -confirm-full-reserve-charge`（绑定精确 sha256）；
4. 结果：`blocked=true → false`，**失败请求逐字保留**（仍为 `http_error`、仍为 `usage_status=unavailable`、仍按预留上界计费 1,542,500 nano-CNY，**未退款**），仅追加一条 `embedding_http_error_full_reserve_v1` 记录（含 `charge_basis=full_reserved_upper_bound_not_provider_usage`、`provider_token_usage=unknown`）。

> 过程中的一次真实拦截：首次执行被拒（`model acceptance budget unavailable`），原因是 PowerShell 的 `Get-FileHash` 输出**大写** sha256，而闸门要求小写十六进制。闸门在写文件之前即拒绝对应请求，**账本未被改动**；改用证据文件中的小写摘要后通过。这同时验证了「快照绑定」确实生效。

---

## 5. 阶段 A：一次批量向量化 2 个 chunk

- 入口：正式应用链路 `POST /knowledge/{id}/reparse`（重新解析 → 索引阶段 → embedding 阶段），未绕预算、未绕队列、未绕账本；
- provider 请求：**1 次**（账本第 2 行），输入 2 个 chunk；
- 账本第 2 行：`model=qwen3.7-text-embedding-flash`、`status=response_received`、预留上界 3085 token、provider 实报 **648 token**、`usage_status=reported`、预留 385,625 nano-CNY、实际 **81,000** nano-CNY（648×125）；
- 结果：2 个 chunk `index_status` 由 `failed` 变为 **`ready`**；`lite_embeddings` 新增 2 行（`dimension=1024`）；新建 `vec_embeddings_1024` 向量表；
- 前三轮尝试留下的 **6 个历史 chunk 行仍在库中**（`deleted_at` 已置、GORM 软删除，`index_status` 仍为 `failed`）⇒ **历史失败痕迹未被擦除**（本轮唯一的行级删除是应用自身的软删除，无任何物理删除）；
- 批量语义：账本只增 1 行 ⇒ 2 个 chunk 由**一次** provider 请求覆盖（`BATCH_EMBED_SIZE` 默认 5 > 2，落在同一子批）。

---

## 6. 阶段 B：一次 query 向量化 + 一次真实向量检索

- 入口：正式检索入口 `POST /knowledge-bases/{kb}/hybrid-search`；
- query：`mesoporous oxide thin film porosity compressive strength`（英文改写，**其词元在索引文本中一次也不出现** ⇒ 关键词通道必然 0 命中，这正是「排序只能来自向量通道」的设计）；
- provider 请求：**1 次**（账本第 3 行），`model=qwen3.7-text-embedding-flash`、`status=response_received`、预留上界 1080 token、provider 实报 **27 token**、预留 135,000 nano-CNY、实际 **3,375** nano-CNY；
- 命中：**2 条**，均 `match_type=0`（`MatchTypeEmbedding`，向量匹配）：

| 位次 | chunk_id | 余弦距离 | 余弦相似度（score） | match_type |
| --- | --- | --- | --- | --- |
| 1 | `46833d0a-6d5c-4a61-9e32-6eef20799a60` | 0.4011 | **0.5989** | 0 = embedding |
| 2 | `3c7f6524-1f74-413c-931b-38d990e3f0ea` | 0.5446 | **0.4554** | 0 = embedding |

**排序依据（服务端日志逐行）**：

```
[SQLite] vectorRetrieve: query_dim=1024, threshold=0.0000, matched=2 rows
[SQLite] vectorRetrieve: #1 chunk_id=46833d0a-…, distance=0.4011, score=0.5989
[SQLite] vectorRetrieve: #2 chunk_id=3c7f6524-…, distance=0.5446, score=0.4554
Retrieval results, engine: sqlite, retriever: keywords, count: 0
Retrieval results, engine: sqlite, retriever: vector,   count: 2
HybridSearch | Result count before fusion: vector=2, keyword=0
fuseOrDeduplicate | Result count after deduplication: 2
```

关键词通道实跑并返回 **0** 条，向量通道返回 **2** 条；`fuseOrDeduplicate` 因此走**纯向量分支**（保留 embedding 原始分数并按分数降序），最终返回顺序与分数**完全来自向量通道**。这不是「词法命中」也不是「请求已发出」，而是可复核的真实向量检索。

---

## 7. 向量维度、向量行、索引状态与检索兼容性

- provider 返回维度 **1024**；模型行 `embedding_parameters.dimension=0`（即「不覆盖、由 provider 决定」），请求体**不带** `dimensions` 参数 ⇒ 维度取 provider 原生值，避免依赖「可覆盖」假设；
- `vec_embeddings_1024`（sqlite-vec 的 vec0 虚表）及其影子表已创建；`lite_embeddings` 中该知识库的 2 行 `dimension=1024`；
- 检索侧 `query_dim=1024` 与索引侧维度一致 ⇒ 命中 2 行，无维度漂移告警（`validateQueryEmbeddingDimension` 在期望维度为 0 时不误判）。

---

## 8. 队列隔离与失败恢复核验

- **队列隔离**：embedding 实例（pid 68840）6 个 worker 池全部 `queue_redis_db=2 queue_db_source=queue-db-env`；Redis DB 2 新增该 pid 的 6 条 server 注册；DB 1（chat profile 实例所在库）注册数不变、pending task 为 0 ⇒ embedding 任务不可能被 chat profile 消费，反之亦然；
- **失败恢复**：上一轮的 403 通过 #671/#674 的正规通道恢复，窗口从「永久冻结」变为可用；本轮 2 次调用均正常结束，窗口 `blocked=false`；
- **#675 未解**：非 `http_error` 的结局（`outcome_unknown` / `usage_unknown` / `in_flight` / `cancel_reconciliation_required` / `limit_violation`）仍会永久冻结且无恢复通道，chat profile 的同类缺口也在。该票保持开放，本轮未触发该风险。

---

## 9. 前后对比（同一环境、同一快照口径）

| 事实 | 执行前 | 执行后 | 判定 |
| --- | --- | --- | --- |
| chat 账本 `qwen-beta-sqlite.json` | sha256 `74e86abc…` | sha256 `74e86abc…` | **逐字节未变** |
| chat 账本 `…calls.json` | sha256 `dd39a0cb…` | sha256 `dd39a0cb…` | **逐字节未变** |
| embedding 账本 | 1 行、`blocked=true`、sha256 `238ca5bf…` | 3 行、`blocked=false`、sha256 `a9cf6cda…` | 变化符合预期（1 行结清 + 2 行新调用） |
| 账本累计预留 token / 费用 | 3,085 / 1,542,500 | **7,250 / 2,063,125**（上限 8,000 / 8,000,000） | 在授权与 profile 双重上限内 |
| 隔离向量 KB 向量行 | **0** | **2**（dimension=1024） | 由 0 到 2 |
| 隔离向量 KB chunk `index_status` | 6 行历史 `failed`（未软删除时可见） | 6 行历史 `failed`（**软删除保留**）+ 2 行 `ready`（未删除） | 失败→就绪；历史失败痕迹保留 |
| 目标知识库 chunk 行（2 行，含 id/坐标/内容 sha/index_status） | — | — | **前后逐字一致** |
| 目标知识库向量行 | 2（关键词行，维度 0） | 2（未动） | 未受影响 |
| 章节 `source_ids`（9 条） | — | — | **前后逐字一致**；被引用 chunk 全部存在 |
| `lingdoc_model_call_ledger` 行数 | 4（全 chat） | 4（全 chat） | embedding 不写该表 |
| 向量表 | 仅 `vector_stores` | 增 `vec_embeddings_1024*` | 与维度一致 |
| 队列 pending task（DB 1 / DB 2） | 0 / 0 | 0 / 0 | 无遗留、无跨 profile 消费 |

---

## 10. 未执行项、边界声明与残留风险

**未执行（不在授权内或不在本轮范围）**

1. 未充值、未改计费模式、未关闭「仅免费层」、未切换账号/凭据/端点；本轮 provider 未返回 403，故该处置路径未被触发。
2. 未调用 Chat（0 次）；embedding profile 本身也不允许 chat 模型出网。
3. 未发出任何探测/测试请求；阶段 A 的**第一条**真实请求直接用于目标 chunk 的向量化（未做「先探一下模型」）。
4. 未重跑**目标知识库**的被引用知识（`c1197b70-…`）：重跑会**软删除**其 2 个 chunk（GORM 软删除后应用层按 `deleted_at` 过滤 ⇒ 章节引用的 chunk 在应用视角消失）并以新 uuid 重建，使章节 `source_ids` 悬空并翻转 `source-available` 规则。因此向量化在**同租户、同材料、同 chunking 配置**的隔离向量知识库中进行——其 2 个 chunk 与目标知识库 2 个被引用 chunk 的**内容 sha256、坐标、title 前缀逐字一致**（见第 2 节第 10 项），向量即目标材料的向量。**此为上一轮已登记的偏离，本轮沿用并补齐了等价性证据。**
5. 未修改任何验收门槛、未把任何 `NOT RUN` 改判为 `PASS`、未关闭 #546 或 #675、未改动 Beta 正式放行结论。
6. 未删除任何文件、分支、容器卷或证据；本轮的诊断探针只**移出**工作树到证据目录。

**§6 第 6 项（向量检索）是否具备申请改 `PASS` 的证据**

- **具备**：真实 provider 向量（Flash，1024 维）已写入向量库；真实向量检索返回 2 条命中；排序与分数可逐行溯源到向量通道（关键词通道实跑 0 命中）；账本逐字记录了 2 次请求的用途、状态、token 与费用；前后对比证明既有证据（source_ids 8/8、词法 10/10、403 原文）全部保留。
- **仍需负责人裁定**：(a) 是否据此把 §6 第 6 项改判 `PASS`；(b) 是否接受第 10 节第 4 项的「向量化在隔离向量知识库进行」这一偏离（或要求另立一轮在目标知识库本体上做向量索引）；(c) 是否关闭 #546（其原始命题「受控 Qwen profile 拒绝 embedding 与需要检索的生成路径互斥」在 embedding profile 下已不复现，但 chat profile 的互斥面未验证）。

**残留风险**

1. **#675 未解**：任何非 `http_error` 的失败结局仍会永久冻结窗口，且 chat profile 的同类缺口仍在（票 #675 开放）。
2. **账号额度**：本轮 2 次调用成功，仅说明当前额度足以覆盖约 675 token 的调用；额度耗尽时窗口会再次冻结（可经 #671/#674 恢复，但每次都需人工结清）。
3. **偏离（见上第 10 节第 4 项）**：目标知识库 2 个被引用 chunk 本身仍**没有**向量行（其 `lite_embeddings` 行是关键词行，维度 0）；若验收口径要求「被引用 chunk 本身带向量」，则需要另一轮在目标知识库内以「单 chunk 重索引」方式实现——代价是每 chunk 1 次 provider 请求（2 chunk = 2 次），超出本轮 2 次上限。
4. `internal/stream/factory.go` 与 `internal/container/container.go:807-812` 仍各自手搓 Redis 连接参数（上一轮已登记为提示级问题），本轮未处理。

---

## 11. 证据清单（可逐件复核）

**收口核验**：报告写完后运行 `closure_verification.py`，把本报告的 **42 条承重结论逐条机器断言**（账本与快照 sha256、本轮请求次数与状态、预算双上限、目标 chunk 逐字未变、`source_ids` 未变、向量维度与 vec 表、检索命中与 `match_type`、服务端日志里的通道计数与两个余弦距离、队列身份与注册归属、证据清单 sha256），结果 **42 项通过 / 0 项失败**（`closure-verification.json`）。核验过程中确实查出并更正了两处：历史 chunk 行是**软删除**而非擦除（本报告表述已更正，且该事实对「不删历史失败记录」更有利），以及队列身份行落在 **stderr** 日志（证据副本已补齐）。

全部位于 `D:\wt\lingdoc-485-evidence\embedding-run\flash-run\`：

| 文件 | 内容 | sha256（前 16 位） |
| --- | --- | --- |
| `pre-phase-snapshot.json` | 执行前全量快照（账本、模型、KB、chunk、向量行、队列） | `420084de2235ecb5` |
| `post-phase-snapshot.json` | 执行后同口径快照 | `11b05ee727be6aa6` |
| `before-after-comparison.json` | 前后逐字段对比 | `773bf156edde57bd` |
| `model-row-fix.json` | 模型行最小修复的前后事实 | `a99074e9a4c52fa8` |
| `local-chain-test-transcript.txt` | 本地零网络链路测试转录（PASS） | `7465f8f2d0a7042c` |
| `probes/zz_flash_chain_local_test.go` | 该本地测试源码（已移出工作树） | `98461e56b684ee89` |
| `reconcile-evidence.json` | 窗口恢复的操作者证据 | `e7e86d7075dcbbed` |
| `ledger-before-reconcile.calls.json` | 恢复前的账本原字节副本 | `238ca5bf91331e77` |
| `acceptance-budget-reconcile.exe` | 本轮构建的恢复工具（head 32b22cf9） | `e42c1e40b54080c8` |
| `phase-a-result.json` | 阶段 A 前后状态与账本 | `90fbf317e52609f6` |
| `phase-b-result.json` | 阶段 B 原始检索响应与账本 | `e79a1e54f996de69` |
| `retrieval-log-evidence.txt` | 检索阶段服务端日志摘录（排序依据） | `422c7305be2e6139` |
| `server-embedding-20261026-075556.log` | 嵌入实例完整日志副本 | `5a2b77a312b739a2` |
| `startup-log-queue-identity.txt` | 启动日志中的队列身份（6 池） | `3efeaf451ad3f0fc` |
| `artifact-manifest.json` | 本清单（全部证据件与其 sha256） | 见文件 |
| `closure-verification.json` | 收口核验：**42 项断言全部通过（0 失败）**，逐条断言本报告的承重结论 | — |

外部账本（未改动/已改变）：

| 文件 | sha256 | 状态 |
| --- | --- | --- |
| `ledger\qwen-beta-sqlite.json` | `74e86abcad63e3d1…` | 逐字节未变 |
| `ledger\qwen-beta-sqlite.json.calls.json` | `dd39a0cb4bfa2c31…` | 逐字节未变 |
| `ledger\qwen-embedding-budget.json.calls.json` | `a9cf6cda345ab20b…` | 3 行（1 行结清 + 2 行新调用） |
| `embedding-run\provider-403-response.txt` | `2915d119c8314714…` | 上一轮 403 原文，保留 |

**凭据处理**：全程未输出明文 API Key；模型行中的 `api_key` 始终为 `enc:v1:` 密文；报告与证据文件中只有密文 sha256 前 12 位指纹。启动脚本读取 `.env` 的 JWT 密钥仅注入子进程环境变量，未打印（上一轮曾因变量名复用误打印，本轮脚本已改名并复核）。
