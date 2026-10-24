# Spec-Beta-F01-07：F01-07 的检索方式口径变更（`vector_required` → `keyword_only`）

> **票**：[#546](https://github.com/magician336/lingdoc/issues/546)（受控 Qwen profile 拒绝 embedding 与需要检索的生成路径互斥：F01 检索节点撞 1007，需裁定口径）｜**上游**：[#482](https://github.com/magician336/lingdoc/issues/482)（Beta 首版放行验收）｜**执行面**：PF-14（Beta 首版放行执行）｜**口径**：#546 的候选②
> **本轮绑定基线**：`origin/main` = `1c543c7d`
> **状态**：**已裁定，待合入**（契约变更与守护同步在同一个 PR；独立审查见 §7）

---

## 1. 裁定

**F01-07（`startGeneration`）在 F01 契约里声明 `retrieval: keyword_only`。**

即 #546 的**候选②**：「契约支持声明『本节点只走关键词检索』并由执行器核验其确实不产生 embedding 请求」。

**理由（三条，每条都有证据）**：

1. **Beta 首版验收的生成节点不需要向量语义召回。** F01-07 的产物是「基于绑定资料的章节候选稿」，资料来源由**资料绑定与来源引文**给出（契约 F01-06 `bindAsset` ＋ F01-10 `getSource`），不是由向量召回给出。Track-D 在一个**全新 keyword-only 知识库**（`vector_enabled=false`、`embedding_model_id=''`、`summary_model_id=''`）上真跑了一遍：检索走关键词通道、生成走真实 Qwen chat、**候选稿产出、每条事实都带 `[[source:…]]` 标记**（§3）。
2. **受控窗口本来就是为「不调用 embedding」设计的。** 受控 profile 只放行 chat 模型；embedding 在 **transport 之前**被确定性拒绝（#546 记录：`model is outside the acceptance budget profile`）。把 F01-07 留在 `vector_required`，等于要求受控窗口永远无法判定这一节点 —— 而实验已经证明**这条链不需要 embedding 就能跑通**。
3. **改声明不等于放宽闸门。** embedding 的拒绝规则、模型白名单、账本口径**一字不动**。受控窗口的判定仍然是：**窗口里出现 embedding 行 ⇒ 整个窗口判失败**；两本账本读不到 ⇒ NOT RUN（不得推断为零调用或 PASS）。

### 明确不做的四件事

| 不做 | 为什么 |
|---|---|
| **不放宽 embedding 拒绝规则** | 那是 #526 经独立复核 PASS 的设计意图之一（防 embedding 偷跑出网） |
| **不把 embedding 纳入受控 profile** | 不需要了；#546 的候选③（纳入 embedding）随之**不采用** |
| **不永久改既有知识库的向量开关** | `indexing_strategy` 的向量开关是**部署/环境属性**，不是受控 profile 的一部分（#546 的边界）。Track-D 用的是**新建的** keyword-only 知识库，既有 KB 的开关一字未动 |
| **不在跑批过程中改标签、不追溯修改历史结果** | Track-D 那一轮的 F01-07 判定仍是 **NOT RUN**（见 §4 第 3 行）；本变更只改**以后**的判定口径 |

---

## 2. 契约变更的精确内容（逐文件）

| 文件 | 改什么 | 归属 |
|---|---|---|
| `docs/08-本轮实施方案/contracts/workflow.json` | ① F01-07 的 `retrieval` 字段：`vector_required` → `keyword_only`；② `retrieval_modes.meaning.keyword_only` 的文本补上判据（窗口 PASS 才可判定）；③ `retrieval_modes.meaning.vector_required` 的文本保留该模式的定义，但写清「本契约现在没有节点声明它」 | PF-10（跨面借用，owner 不变） |
| `scripts/lingdoc_mock/controlled_window_check.py` | `VECTOR_REQUIRED_UNBLOCK` 的文本：该模式仍在词表里，解除条件改成「若将来某节点确实需要向量检索 ⇒ 把 embedding 纳入受控 profile 并拆分 chat/embedding 额度」 | PF-14（自有） |
| `scripts/ci/test_lingdoc_controlled_window.py` | 两条钉住旧事实的守护改成新事实；**新增**「`keyword_only` 节点的三态守护」 | PF-14（自有） |
| `scripts/ci/test_lingdoc_beta_evidence.py` | `declared == [("F01-07", "keyword_only")]`；**新增**「窗口 FAIL ⇒ 该节点 FAIL」「窗口 NOT RUN ⇒ 该节点 NOT RUN」 | PF-10（跨面借用，owner 不变） |
| `docs/08-本轮实施方案/Spec-Beta-F01-07-检索方式口径变更.md` | 本文件（裁定记录 ＋ 验收矩阵 ＋ 证据索引） | PF-14（自有，新增路径） |

**不改**：`retrieval_modes.values`（词表）、F01-07 之外的任何节点的 `retrieval`、F01-08…F01-23 的 `undeclared` 状态、生成/来源/验收的其它契约、任何验收门槛。

---

## 3. 事实基础（真实环境，不是推理）

**环境**：隔离 SQLite（`D:\wt\lingdoc-484-evidence\data\lingdoc-484-final.db`）、独立存储根、私有 miniredis、`RETRIEVE_DRIVER=sqlite`；服务与材料闸门二进制**由合并后的 main 重建**（`781295c7`，sha256 见 `track-d-binary-manifest.json`）。

**受控 profile**：`lingdoc-qwen-token-budget-20261024-v2`（4 次调用 / 单次输出上限 4096 token / 累计预留上限 90000 / 并发 1）。

### 3.1 七项零 embedding 前置：**全部 PASS**

| # | 前置 | 判定 | 证据 |
|---|---|---|---|
| 1 | 材料已解析 | PASS | `parse_status=completed` |
| 2 | 分块已落库 | PASS | 2 段分块 |
| 3 | 分块正文 == 坐标跨度 | PASS | `[0,489)`／`[475,902)`，逐字一致 |
| 4 | 关键词检索能取回材料 | PASS | 3 条查询各 2 条命中；**探针前后两本账本都不存在** |
| 5 | **生成请求构造正确** | PASS | provider 调试日志：**chat 请求块 1 个、embedding 请求块 0 个**；模型是已核准的 `qwen3.8-flash`；端点是白名单内唯一端点；`CompletionBudget=4096` |
| 6 | chat 模型选中已核准行 | PASS | 全库 1 行、8 项事实逐字核对全 True |
| 7 | **零 embedding** | PASS | a. DB 账本 1 行且是聊天行｜b. 两本 JSON 账本无 embedding 行｜c. `lite_embeddings` 24 行但 `dimension>0` **0 行**｜d. provider 日志无 Embedding 块｜e. 应用日志无 embedding 请求 |

**第二个独立证据**（服务端接入阶段原话）：`the vector pipeline is skipped, the keyword pipeline is not` ⇒ #590 的解耦在真实运行里落地。

### 3.2 真实调用与三本账（chat 1 次 / 授权 4 次；embedding 0 次）

- 请求：`qwen3.8-flash` @ `https://llm-k57eqvdqacn4nov3.cn-beijing.maas.aliyuncs.com/compatible-mode/v1/chat/completions`、
  `RequestID=34235362-9739-900d-b647-4968d3292409`、`RequestFingerprint=sha256:6f8cdaeb…94a5b`、`Temperature=0.20 / CompletionBudget=4096 / ResponseFormat=json_object`、耗时 7.651s。
- 请求体**只带关键词检索取回的两段材料**（逐字引文 ＋ `locator`），**没有任何 embedding 相关字段**。
- 回答：合法 JSON；`body_markdown` 每条事实带 `[[source:…]]`；`source_ids` 两段分块；`review_items` 3 条。
- 账本①窗口账本：1 条请求，`status=response_received`、`usage_status=reported`、`input/output/total=1037/521/1558`、`finish_reason=stop`、`reserved_tokens=8945`、`actual_tokens=1558`、`calls_max=4`、`blocked=false`。
- 账本②共享账本 `.calls.json`：同一条调用。
- 账本③数据库 `lingdoc_model_call_ledger`：1 行（`kind=call`／`provider=aliyun`／`model_id=lingdoc-chat-qwen3.8-flash-iso`／`source_kind=real`／`input_tokens=1037`／`output_tokens=521`／`status=succeeded`／`context=generation_run`）。
- ⇒ **预留（8945）、provider 实报 usage（1558）、累计剩余额度（90000−8945）三者分开记录**，互不冒充。

### 3.3 声明的前后对照（**零 provider 消耗**：只读已经发生的事）

同一套真实产物（两本账本 ＋ 隔离 SQLite），**唯一变量是 F01-07 的声明**：

| | 窗口判定 | F01-07 |
|---|---|---|
| **before**（`vector_required`，main 上的现状） | **PASS**（2 条调用行里没有 embedding 行；交叉核对 available；`window_fact_complete=true`） | **NOT_RUN** |
| **after**（`keyword_only`，只改这一个字段的副本） | **PASS**（同上） | **PASS** |

after 的原话：「受控窗口的 2 条调用行里没有 embedding 行；该节点声明 keyword_only，可以在受控窗口里被判定。」

⇒ **窗口本身早就是 PASS 的**；F01-07 之所以不被判定，只因为它自己声明了 `vector_required`。产物：`f01-07-declaration-before-after.json`。

---

## 4. 验收矩阵

口径 v1：**一行 = 一条可独立判定的要求**；PASS 谓词 = **一条可 grep 的断言名 ＋ 一条可复现命令 ＋ 绑定的当前 SHA**。状态枚举：`PASS` / `FAIL` / `NOT RUN` / `DEFERRED_MANUAL`（带 `ticket` ＋ 解除条件）。

| # | 要求 | 断言 / 可复现命令 | 状态 | 证据 |
|---|---|---|---|---|
| A1 | F01-07 在契约里声明 `keyword_only` | `test_the_contract_declares_how_the_f01_generation_may_retrieve`（`scripts/ci/test_lingdoc_controlled_window.py`） | **PASS** | 契约字段 ＋ 守护断言 |
| A2 | 放行链引用的契约声明里**恰好一个**节点声明检索方式，且是 F01-07 | `test_the_contract_declares_the_retrieval_mode_of_exactly_one_node`（`scripts/ci/test_lingdoc_beta_evidence.py`） | **PASS** | 守护断言 |
| A3 | **`keyword_only` 节点只有在窗口 PASS 时才被判定为 held** | 新增守护（窗口 PASS ⇒ 该节点 held；窗口 FAIL ⇒ failed；窗口 NOT RUN ⇒ not_run） | **PASS** | 守护断言（CI） |
| A4 | 受控窗口里**没有 embedding 调用行** ⇒ 窗口 PASS | `test_a_window_without_embedding_calls_passes` | **PASS** | 守护断言 ＋ 真实两本账本（§3.3） |
| A5 | 受控窗口里**有** embedding 调用行 ⇒ **整个窗口判失败**（不是「检索通过」） | `test_a_window_with_an_embedding_call_fails_as_a_whole` ＋ `test_a_controlled_window_with_an_embedding_call_fails_as_a_whole` | **PASS** | 守护断言 |
| A6 | 窗口账本**读不到** ⇒ NOT RUN，**永不** PASS | `test_a_window_that_cannot_be_read_is_not_run_and_never_pass` ＋ `test_a_window_with_only_one_of_its_two_ledgers_is_not_run` | **PASS** | 守护断言 |
| A7 | 没有模型表 ⇒ NOT RUN（「读不到」不等于「没有」） | `test_a_window_without_the_model_table_is_not_run` | **PASS** | 守护断言 |
| A8 | 声明了没人知道的检索方式 ⇒ **按名字拒绝**，不得静默跳过 | `test_a_misspelled_declaration_is_refused_by_name` | **PASS** | 守护断言 |
| A9 | 真实 F01 序列里 F01-07 能被判定（不只是单测） | 下一轮完整 F01 跑批后，读放行链产出的逐节点结论 | **NOT RUN** | 解除条件：#608 合入后重跑一轮完整 F01（材料 ＋ 两本账本 ＋ 隔离环境），产出新的 `acceptance/beta-first/` 证据目录；**本轮不消耗真实调用**（用既有账本与既有材料重放，只有 F01-07 需要新的生成时才用剩下 3 次额度中的 1 次） |
| A10 | F01 序列在 F01-07 之后的节点（08…23）全部跑到底 | 同上 | **NOT RUN** | 解除条件：**#608**（F01-11 采纳节点）合入并复核后重跑 |
| A11 | **真实实验的证据与原始口径不被改写** | 本文件 §3 记录的是 Track-D **改声明之前**的事实（窗口 PASS、F01-07 NOT_RUN）；Track-D 的 F01 报告原样保留（`f01-report.json` ＋ 第一次尝试的 `f01-report-attempt1-model-mismatch.json`） | **PASS** | 证据目录原样；历史结果未被追溯修改 |
| A12 | **不放宽 embedding 拒绝规则** | `internal/models/acceptancebudget/` 与 embedding 拒绝路径**一字未改**（本次 PR 不碰这些文件） | **PASS** | 本 PR 的改动文件清单 |
| A13 | **不永久改既有知识库的向量开关** | Track-D 用的是**新建**的 keyword-only KB；既有 9 个 KB 的 `indexing_strategy` 未被本 PR 或实验改动 | **PASS** | §3 环境说明 ＋ 实验脚本只创建、不翻转 |

**本变更自身没有 `DEFERRED_MANUAL` 行**（人工前置项是 F01 链自己的，不是本变更的）。

---

## 5. 放行链实际执行的那条零 embedding 守护

放行链 = `scripts/acceptance/beta_evidence.py`。它**已经**把受控窗口判定接进总判定：

- `controlled_window_part(...)` 调 `judge_window(...)`，产出**一条窗口级结论** ＋ **每个声明节点一条结论**；
- 两本账本**一起**交给它（`--controlled-window-ledger` ＋ `--controlled-window-calls-ledger`，后者默认取前者 ＋ `.calls.json`）⇒ 半可读的窗口**永远不能被认证**；
- 部署自己的模型调用账本作为**交叉核对**（「账本里记成 embedding 调用的那个模型就是 embedding 模型，不管模型表怎么说」）；
- 窗口结论与逐节点结论都进 `outcomes`，`overall_result(outcomes, provider_semantics_status(f01_report))` 把它们并入**总判定**。

本变更给这条链补的是**「`keyword_only` 节点的判定三态」**这条此前不存在的守护（A3）：窗口 FAIL 时该节点必须 FAIL，窗口 NOT RUN 时必须 NOT RUN —— 否则「改成 keyword_only」会变成一条绕过失败的口子。

---

## 6. 边界（这次明确不做）

- **不改** `retrieval_modes.values` 词表（`keyword_only` / `vector_required` 两个取值都在）。
- **不改** F01-08…F01-23 的 `undeclared` 状态。它们现在是「没声明 ⇒ 不参与受控窗口的 embedding 判定」，**不是**「已被判定」；本变更不扩大范围去给它们逐个声明检索方式（那是另一件事，若要做的后果是：F01-09 的 `getCandidate` 读的是候选稿、F01-13…F01-17 写章节，都不产生检索，但**逐个声明需要各自的事实依据**，不能顺手填）。
- **不改**任何验收门槛、不改 `DEFERRED_MANUAL` 的语义、不动 `gaps.json`（缺口登记表里没有与 F01-07/embedding 解除条件相关的条目：8 条 GAP 全部是 #480 / #499 的，与本变更无关）。
- **不改**生成、来源、许可、验收的其它契约（#608 那条线单独一个 PR）。

---

## 7. 独立审查

- 审查对象：本 Spec ＋ 契约改动 ＋ 守护改动 ＋ §4 的验收矩阵。
- 审查方式：**独立 verifier 会话**，只读取证、逐条核实（A1–A13 每行是否真的由断言与命令支撑；§3 的每个数字是否来自真实产物；「不放宽 embedding 拒绝规则」「不改既有 KB 向量开关」两条边界是否真的成立）。
- 审查结论与逐条发现记在本节，并同步到 [#546](https://github.com/magician336/lingdoc/issues/546) 票面。

> ⚠️ **审查未完成前，本变更不得合入 main**；审查结论为 PASS 之前，本 Spec 的状态是「已裁定，待合入」，**不是**「已通过」。

---

## 8. 证据索引

- Track-D 报告：`D:\wt\lingdoc-485-evidence\track-d-experiment\TRACK-D-REPORT.md`
- 轮次目录：`D:\wt\lingdoc-485-evidence\track-d-experiment\run-20261024-781295c7\`
  - `zero-embedding-verdict.json`（七项前置逐项判定，`overall: PASS`）
  - `f01-report.json`（Track-D 的 F01 跑批：15/28，止于 F01-11）
  - `f01-report-attempt1-model-mismatch.json`（第一次尝试：0 次 provider 请求）
  - **`f01-07-declaration-before-after.json`**（§3.3 的声明前后对照）
  - `gate-stage1.json`／`keyword-probe.json`／`kb-readback.json`／`ingest-readback.json`
- provider 请求原文：`D:\wt\lingdoc-484-evidence\logs\llm_debug_trackd\20261024_173737.438.log`
- 两本账本：`D:\wt\lingdoc-484-evidence\ledger\qwen-beta-sqlite.json` ＋ `.calls.json`
- 二进制清单（sha256）：`D:\wt\lingdoc-484-evidence\track-d-binary-manifest.json`
- 票面通报：[#485](https://github.com/magician336/lingdoc/issues/485)（实验通报）、[#490](https://github.com/magician336/lingdoc/issues/490)（对放行结论表的影响）
