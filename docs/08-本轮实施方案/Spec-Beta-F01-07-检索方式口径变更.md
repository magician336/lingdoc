# Spec-Beta-F01-07：F01-07 的检索方式口径变更（`vector_required` → `keyword_only`）

> **票**：[#546](https://github.com/magician336/lingdoc/issues/546)（受控 Qwen profile 拒绝 embedding 与需要检索的生成路径互斥：F01 检索节点撞 1007，需裁定口径）｜**上游**：[#482](https://github.com/magician336/lingdoc/issues/482)（Beta 首版放行验收）｜**执行面**：PF-14（Beta 首版放行执行）｜**口径**：#546 的候选②
> **变更基线（被改的那棵树的基线）**：`origin/main` = `1c543c7d`
> **实现 head（§4 验收矩阵的 PASS 谓词绑在它上面）**：`3f081e53`（每次 push 后更新此行；合并后为合并提交）
> ⚠️ **两者的区别**：A1–A3 这类「要求靠这次改动才成立」的判据，在**变更基线**上**不成立**（那里契约仍声明 `vector_required`、三态守护还不存在）⇒ 它们的 PASS 只能绑在**实现 head**上；把它们记成「在 `1c543c7d` 上 PASS」就是把一个还没发生的改动说成了已经验证过的。
> **状态**：**已裁定 ＋ 已独立审查（PARTIAL，6 点已改）＋ 待合入**（契约变更与守护同步在同一个 PR；审查结论见 §7）

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
| `docs/08-本轮实施方案/contracts/workflow.json` | ① F01-07 的 `retrieval` 字段：`vector_required` → `keyword_only`；② `retrieval_modes.meaning.keyword_only` 的文本补上判据（窗口 PASS 才可判定）**并加上范围限定**（向量开关是部署属性）**与证据强度边界**（账本里没有 embedding 行 ≠ 没有尝试过 embedding）；③ `retrieval_modes.meaning.vector_required` 的文本保留该模式的定义，但写清「本契约现在没有节点声明它」 | PF-10（跨面借用，owner 不变） |
| `scripts/lingdoc_mock/controlled_window_check.py` | ① `VECTOR_REQUIRED_UNBLOCK` 的文本：该模式仍在词表里，解除条件改成「若将来某节点确实需要向量检索 ⇒ 把 embedding 纳入受控 profile 并拆分 chat/embedding 额度」（并去掉已过时的「MaxCalls=1」——profile 是版本化的，v1 是 1 次、v2 是 4 次）；② **`check()` 的返回形状新增 `nodes` 键**（逐节点判定）——命令行的 `main()` 早就报它了，单账本形态却没有，而新的三态守护要读它 | PF-14（自有） |
| `scripts/ci/test_lingdoc_controlled_window.py` | 该文件基线里**有 1 条**钉住旧事实的守护（`test_the_contract_declares_how_the_f01_generation_may_retrieve`），改成新事实；**新增**「`keyword_only` 节点的三态守护」（窗口 PASS ⇒ 判定、窗口 FAIL ⇒ 失败、窗口 NOT RUN ⇒ NOT RUN） | PF-14（自有） |
| `scripts/ci/test_lingdoc_beta_evidence.py` | ① `declared == [("F01-07", "keyword_only")]`；② **`test_a_vector_required_node_is_never_recorded_as_passed` 改成**夹具契约**驱动**（它原来钉的是真实契约的声明 —— 真实契约一变，这条守护就会因为「真实契约不再声明 vector_required」而失去它要守的语义）；③ **新增**「窗口 FAIL ⇒ 该节点 FAILED」「窗口 NOT RUN ⇒ 该节点 NOT_RUN」 | PF-10（跨面借用，owner 不变） |
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
- 账本③数据库 `lingdoc_model_call_ledger`：1 行（`kind=call`／`provider=aliyun`／`model_id=lingdoc-chat-qwen3.8-flash-iso`／**`run_mode=real`**／`input_tokens=1037`／`output_tokens=521`／`status=succeeded`／**`source_kind=generation_run`**，`source_id` ＝ 生成运行 `f4135915-…`）。
  ⚠️ **字段读法的更正**（2026-10-24 独立审查发现）：本文件初稿把这一行写成「`source_kind=real`／`context=generation_run`」—— **两处都错**：真实列名是 `run_mode` 与 `source_kind`（`source_id` 才是生成运行 id），表里**没有 `context` 列**。事实本身（1 行、聊天调用、usage 1037/521）不变。
- ⇒ **预留（8945）、provider 实报 usage（1558）、累计剩余额度（90000−8945）三者分开记录**，互不冒充。

### 3.3 声明的前后对照（**零 provider 消耗**：只读已经发生的事）

同一套真实产物（两本账本 ＋ 隔离 SQLite），**唯一变量是 F01-07 的声明**：

| | 窗口判定 | F01-07 |
|---|---|---|
| **before**（`vector_required`，main 上的现状） | **PASS**（2 条调用行里没有 embedding 行；交叉核对 available；`window_fact_complete=true`） | **NOT_RUN** |
| **after**（`keyword_only`，只改这一个字段的副本） | **PASS**（同上） | **PASS** |

after 的原话：「受控窗口的 2 条调用行里没有 embedding 行；该节点声明 keyword_only，可以在受控窗口里被判定。」

⇒ **窗口本身早就是 PASS 的**；F01-07 之所以不被判定，只因为它自己声明了 `vector_required`。产物：`f01-07-declaration-before-after.json`。
（`window_fact_complete=true` 出自**命令行** `main()`（`controlled_window_check.py:421`，含义是「两本账本都读到了」），不是 `judge_window` 的返回键 —— 引用时要说清是哪一层。）

⚠️ **这条对照能证明什么、不能证明什么**：
- **能**：窗口本身的机器事实是「两本账本可读 ＋ 没有 embedding 调用行」；只改声明这一个字段就能让 F01-07 从 NOT_RUN 变成可判定。
- **不能**：它**不**证明「这个节点没有尝试过 embedding」。**受控 profile 在 transport 之前拒绝的 embedding 尝试不落账本行**（`internal/models/acceptancebudget/ledger.go`：模型不在白名单时直接返回，不写任何行）⇒ 「账本里没有 embedding 行」**不蕴含**「没有尝试过 embedding」。
  ⇒ 本轮「零 embedding」的结论**不是**由账本单独支撑的，而是由五路证据里**独立于账本**的那两路支撑的：**provider 调试日志**（1 个文件、0 个 Embedding 请求块）与应用日志（0 条 embedding 请求命中）。放行链未来对 `keyword_only` 节点给出的 `held` 只有账本这一个证据源 ⇒ 这是**口径本身的残留强度**，已写进契约的 `meaning.keyword_only` 与 §6 的边界。

### 3.4 放行链自己的判定（不是模块的自检）

§3.3 走的是 `controlled_window_check` 模块。**放行链那一层**（`scripts/acceptance/beta_evidence.py` 的 `controlled_window_part`）也用同一套真实产物判了一遍 —— 调的是**它自己的函数**，不是一份复制：

```
=== window verdict ===
status  = PASS
reason  = 受控窗口的 2 条调用行里没有 embedding 行。
requests= 2  embedding_rows= []
cross_check = {"available": true, "reason": "部署的模型调用账本里有 0 个 embedding 模型的调用。", "embedding_models": []}

=== per-node outcomes (the release chain's own) ===
held       受控窗口：embedding 调用核验（受控窗口内不得有 embedding 调用行）
           evidence: 受控窗口的 2 条调用行里没有 embedding 行。
held       受控窗口 · F01-07 startGeneration（keyword_only）
           evidence: 受控窗口的 2 条调用行里没有 embedding 行；该节点声明 keyword_only，可以在受控窗口里被判定。
not_run    受控窗口 · F01-08 getGeneration（undeclared）
           reason: 契约没有为这个节点声明检索方式（undeclared）：它不参与受控窗口的 embedding 判定，所以本轮没有判定它。
（其余 **27** 个节点同为 undeclared ⇒ not_run，见 §6 的边界说明）
```

⇒ **F01-07 在放行链这一层被判为 `held`**，窗口级结论也是 `held`，交叉核对可用且**部署账本里 0 个 embedding 模型**。
产物：`release-chain-f01-07-verdict.json`（本节脚本 **0 次 provider 请求**，只读已经发生的事）。

⚠️ **这条 `held` 是窗口级事实，不是「这个节点没有尝试过 embedding」**：
- 受控窗口的机器事实只有一条：**两本账本可读 ＋ 没有 embedding 调用行**。被受控 profile 在 transport 之前拒绝的 embedding 尝试**不落账本行**（`internal/models/acceptancebudget/ledger.go`：模型不在白名单时直接返回、不写行）⇒ 「没有 embedding 行」**不蕴含**「没有尝试过 embedding」。
- 本轮「零 embedding」是由**独立于账本**的两路证据成立的：provider 调试日志（1 个文件、0 个 Embedding 请求块）＋ 应用日志（0 条命中）。
- 放行链的 `controlled_window_part` **不看跑批报告**（它的签名里没有）⇒ **F01-07 根本没执行也会产出一条 `held`**。这不是本次变更新引入的（改动前这条永远是 NOT RUN），是「`keyword_only` 第一次让这条结论可以被给出」带来的；**总判定不受影响**（NOT RUN 优先于 held），但**逐节点条目会多出一条 `passed[]`** ⇒ 读结论表的人必须知道这条 `held` 的来源是**窗口**，不是**跑批**。

---

## 4. 验收矩阵

口径 v1：**一行 = 一条可独立判定的要求**；PASS 谓词 = **一条可 grep 的断言名 ＋ 一条可复现命令 ＋ 绑定的当前 SHA**。状态枚举：`PASS` / `FAIL` / `NOT RUN` / `DEFERRED_MANUAL`（带 `ticket` ＋ 解除条件）。

| # | 要求 | 断言 / 可复现命令 | 状态 | 证据 |
|---|---|---|---|---|
| A1 | F01-07 在契约里声明 `keyword_only` | `test_the_contract_declares_how_the_f01_generation_may_retrieve`（`scripts/ci/test_lingdoc_controlled_window.py`） | **PASS（绑在实现 head 上）** | 契约字段 ＋ 守护断言。⚠️ 这条要求**在变更基线 `1c543c7d` 上不成立**（那里仍声明 `vector_required`）⇒ PASS 只能记在实现 head 上 |
| A2 | 放行链引用的契约声明里**恰好一个**节点声明检索方式，且是 F01-07 | `test_the_contract_declares_the_retrieval_mode_of_exactly_one_node`（`scripts/ci/test_lingdoc_beta_evidence.py`） | **PASS（绑在实现 head 上）** | 守护断言（同上：变更基线上不成立） |
| A3 | **`keyword_only` 节点只有在窗口 PASS 时才被判定为 held** | 新增守护 `test_a_keyword_only_node_follows_the_window_verdict`（窗口 PASS ⇒ 该节点 held；窗口 FAIL ⇒ failed；窗口 NOT RUN ⇒ not_run） | **PASS（绑在实现 head 上）** | 守护断言（CI）。⚠️ 这条守护**在变更基线上还不存在** ⇒ 同上 |
| A4 | 受控窗口里**没有 embedding 调用行** ⇒ 窗口 PASS | `test_a_window_without_embedding_calls_passes` | **PASS** | 守护断言 ＋ 真实两本账本（§3.3） |
| A5 | 受控窗口里**有** embedding 调用行 ⇒ **整个窗口判失败**（不是「检索通过」） | `test_a_window_with_an_embedding_call_fails_as_a_whole` ＋ `test_a_controlled_window_with_an_embedding_call_fails_as_a_whole` | **PASS** | 守护断言 |
| A5b | **放行链自己**（`beta_evidence.controlled_window_part`，不是模块自检）把 F01-07 判为 `held`，窗口级结论也是 `held`，交叉核对可用且 0 个 embedding 模型 | `python D:\wt\lingdoc-485-evidence\scripts\exp_f0107_release_chain_verdict.py`（真实两本账本 ＋ 隔离 SQLite ＋ 只改一个字段的契约副本；**0 次 provider 请求**） | **PASS** | `release-chain-f01-07-verdict.json`（**绑定实现 head `3f081e53`**）。⚠️ **这条 `held` 是窗口级事实**：放行链不看跑批报告 ⇒ F01-07 根本没执行也会产出它；且「账本里没有 embedding 行」**不蕴含**「没有尝试过 embedding」（被白名单拒绝的尝试不落行，§3.3/§3.4 已展开） |
| A6 | 窗口账本**读不到** ⇒ NOT RUN，**永不** PASS | `test_a_window_that_cannot_be_read_is_not_run_and_never_pass` ＋ `test_a_window_with_only_one_of_its_two_ledgers_is_not_run` | **PASS** | 守护断言 |
| A7 | 没有模型表 ⇒ NOT RUN（「读不到」不等于「没有」） | `test_a_window_without_the_model_table_is_not_run` | **PASS** | 守护断言 |
| A8 | 声明了没人知道的检索方式 ⇒ **按名字拒绝**，不得静默跳过 | `test_a_misspelled_declaration_is_refused_by_name` | **PASS** | 守护断言 |
| A9 | 真实 F01 序列里 F01-07 能被判定（不只是单测） | 下一轮完整 F01 跑批后，读放行链产出的逐节点结论 | **NOT RUN** | 解除条件：#608 合入后重跑一轮完整 F01（材料 ＋ 两本账本 ＋ 隔离环境），产出新的 `acceptance/beta-first/` 证据目录；**本轮不消耗真实调用**（用既有账本与既有材料重放，只有 F01-07 需要新的生成时才用剩下 3 次额度中的 1 次） |
| A10 | F01 序列在 F01-07 之后的节点（08…23）全部跑到底 | 同上 | **NOT RUN** | 解除条件：**#608**（F01-11 采纳节点）合入并复核后重跑 |
| A11 | **真实实验的证据与原始口径不被改写** | 本文件 §3 记录的是 Track-D **改声明之前**的事实（窗口 PASS、F01-07 NOT_RUN）；Track-D 的 F01 报告原样保留（`f01-report.json` ＋ 第一次尝试的 `f01-report-attempt1-model-mismatch.json`） | **PASS** | 证据目录原样；历史结果未被追溯修改 |
| A12 | **不放宽 embedding 拒绝规则** | `internal/models/acceptancebudget/` 与 embedding 拒绝路径**一字未改**（本次 PR 不碰这些文件） | **PASS** | 本 PR 的改动文件清单 |
| A13 | **不永久改既有知识库的向量开关** | Track-D 用的是**新建**的 keyword-only KB；实测隔离库里 12 个 KB **全部是当天新建**、**无一被翻转**（`indexing_strategy` 与 `updated_at` 均未被本 PR 或实验改动） | **PASS** | §3 环境说明 ＋ 实验脚本只创建、不翻转 |

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
- **不改** `docs/BETA_STATUS.md` 里的**历史口径**：那一文件有 10 处以上写着「F01-07 ＝ NOT RUN（契约声明 `vector_required`）」，那是 **Track-D 当时的事实**，本变更**不改写它**；它由**后续的状态同步 PR**（下一轮）追加新章节来说明「口径已变、事实未变」。本次只在自己的分支上更正了一处**字段读法错误**（§3.2 的账本行），并在原处标注了更正。
- **实现事实的边界（必须写清，否则声明会被读成比它强的东西）**：
  - 「该节点不依赖向量语义召回」是**节点级声明**，但**实现里由知识库的部署配置决定**（`internal/application/service/knowledgebase_search.go` 的 `buildRetrievalParams`：有没有 embedding 取决于知识库的 `IsVectorEnabled()`；生成链的输入解析走 `HybridSearch`）。⇒ 声明的效力**依赖本轮所用的知识库配置**，不是契约能单方面保证的事实。
  - 仓库自己的反证就在 `docs/BETA_STATUS.md`（部署现状下检索探针返回 500/1007，而**两本账本仍不存在**）⇒ 被拒绝的 embedding 尝试在账本里**不留痕**。
  - ⇒ 所以本契约的 `meaning.keyword_only` 里同时写了两句：**判据是窗口的机器事实**，以及**「没有 embedding 行」不蕴含「没有尝试过 embedding」**。

---

## 7. 独立审查

**结论：PARTIAL**（裁定本身站得住，事实基础真实可复现；但有 6 处必须先改的点）。独立 verifier 会话，只读取证、逐条核实，报告在 `D:\wt\lingdoc-485-evidence\review-f01-07\REVIEW-f01-07-spec.md`（34 KB，**未改动任何项目文件**）。

| # | 审查发现 | 处置 |
|---|---|---|
| 1 | **A1/A2/A3 在变更基线 `1c543c7d` 上不成立**（那里契约仍声明 `vector_required`、三态守护还不存在），却标了 PASS | §4 三行改成「PASS（绑在实现 head 上）」并在证据栏写明「在变更基线上不成立」；头部新增「变更基线 vs 实现 head」两行 |
| 2 | §2 漏了两处必改：`check()` 的返回形状新增 `nodes` 键；`test_a_vector_required_node_is_never_recorded_as_passed` 必须改成**夹具驱动** | §2 已补这两行；旧事实守护共 3 条、分布在两个文件（`test_lingdoc_controlled_window.py` 1 条 ＋ `test_lingdoc_beta_evidence.py` 2 条），§2 已按文件分列 |
| 3 | §3.2 把账本③读成「`source_kind=real`／`context=generation_run`」——**两处都错**，真实列名是 `run_mode`／`source_kind`，表里没有 `context` 列 | §3.2 已更正并标注更正；**同一处错误也存在于 Track-D 报告与 `docs/BETA_STATUS.md` §17**，两处已一并更正（见 §8 证据索引的更正说明） |
| 4 | `keyword_only` 的释义缺**部署配置**的范围限定 | 契约的 `meaning.keyword_only` 已加范围限定；§1/§6 已补实现事实边界 |
| 5 | §3.4 与 A5b 的免责只防了「范围外推」，没防「**证据强度外推**」（窗口 PASS ≠ 没有 embedding 尝试） | §3.3/§3.4/A5b 三处都补了这条；契约释义里也写了 |
| 6 | `docs/BETA_STATUS.md` 会因此陈旧（10 处以上） | §6 写明：**不改写历史口径**，由后续状态同步 PR 追加新章节说明「口径已变、事实未变」 |

**审查还独立复现了**：§3.3 的「只改一个字段」（去掉 step 级 `retrieval` 后副本与基线**逐字相同**）、两次窗口判定均 PASS、A5b 的放行链层判定、§3 的全部数字（除上面第 3 条的字段读法）、四条边界（「不放宽拒绝规则」「不改既有 KB 开关」「不改词表」「不改其余 27 个 undeclared」）**全部成立**。

**审查明确指出的仍未验证项**：未发真实 provider 请求；#608 未合入；**没有向量开启的 KB 可供实测**（⇒ 「keyword-only 路线成立」这件事本身在真实环境里的证据来自 Track-D 的**新建** keyword-only KB，没有「向量开启时对比」的实测）；`acceptance/beta-first/` 目录尚不存在（逐节点 `passed[]` 那条是代码路径推导）；未跑 `scripts/ci` 全量发现、未跑 `go test`。

**审查提出的环境副作用**：§3.4 的脚本原先经 `beta_evidence.read_model_types` **读写**打开隔离库，于是那对 `-wal`/`-shm` 旁文件的 mtime 被改了一次（**主库内容与行数未变**，复审实测：主库 sha256/大小/mtime 全同、`integrity_check=ok`、models=1／ledger=1／chunks=24／knowledges=12／KB=12 行数不变）⇒ **已把脚本改成自带只读读类型函数**（`mode=ro`），复审实测跑前跑后主库与旁文件的 sha256/大小/mtime **全部未变**。那两个旁文件是 WAL 模式的正常产物（服务端自己也在用），**没有也不该由证据脚本删除**。

> ⚠️ **审查结论为 PASS 之前，本变更不得合入 main**；当前结论是 **PARTIAL**，上面 6 点已全部处置 ⇒ 处置后需**复审**（复审只针对这 6 点），复审通过才合入。

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
- 独立审查报告：`D:\wt\lingdoc-485-evidence\review-f01-07\REVIEW-f01-07-spec.md`
- 票面通报：[#485](https://github.com/magician336/lingdoc/issues/485)（实验通报）、[#490](https://github.com/magician336/lingdoc/issues/490)（对放行结论表的影响）

### 8.1 一处更正（独立审查发现，三处同步更正）

**模型调用账本那一行的字段读法**：本文件初稿、Track-D 报告（`TRACK-D-REPORT.md` §3.4）与 `docs/BETA_STATUS.md` §17 **三处**都把它写成「`source_kind=real`／`context=generation_run`」—— **两处都错**：

| 写法 | 真相 |
|---|---|
| `source_kind=real` | 真实列名是 **`run_mode=real`** |
| `context=generation_run` | 真实列名是 **`source_kind=generation_run`**（`source_id` 才是生成运行 id）；表里**没有 `context` 列** |

**事实本身不变**（1 行、`kind=call`、`model_id=lingdoc-chat-qwen3.8-flash-iso`、`input_tokens=1037`、`output_tokens=521`、`status=succeeded`）—— 错的只是字段读法。三处已同步更正；`docs/BETA_STATUS.md` 的更正就地标注，**没有改写 Track-D 的实验事实**。
