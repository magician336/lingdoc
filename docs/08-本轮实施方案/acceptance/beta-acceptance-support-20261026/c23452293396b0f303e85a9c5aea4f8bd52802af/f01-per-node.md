# F01 逐节点事实（自动提取，未含人工判定）

- 跑批报告：`D:\wt\lingdoc-485-evidence\track-d-experiment\run-final-20261025-093259-c2345229\f01-report.json`
- 契约：`D:\wt\lingdoc-final-main\docs\08-本轮实施方案\contracts\workflow.json`
- runner_status = **completed**，completed/total = **31/31**
- 契约节点数 = **31**，契约声明 checks = **64**，本轮实际求值 checks = **64**（passed 64 / failed 0）
- 节点：executed **31**，运行失败 **0**，NOT RUN **0**
- verification_scope = `http_smoke_only`，provider_semantics_status = `not_verified`
- 材料闸门阶段：`["content", "full"]`

| 节点 | operation | 期望 HTTP | 实际 HTTP | 运行事实 | 契约判定 | checks 声明/求值/通过 |
| --- | --- | --- | --- | --- | --- | --- |
| F01-01 | getTemplate | 200 | 200 | executed | PASS | 1 / 1 / 1 |
| F01-02 | createProject | 201 | 201 | executed | PASS | 1 / 1 / 1 |
| F01-03 | saveSpec | 200 | 200 | executed | PASS | 1 / 1 / 1 |
| F01-03b | recordActivationRead | 201 | 201 | executed | PASS | 3 / 3 / 3 |
| F01-04 | activateProject | 200 | 200 | executed | PASS | 1 / 1 / 1 |
| F01-05 | listChapters | 200 | 200 | executed | PASS | 2 / 2 / 2 |
| F01-06 | bindAsset | 201 | 201 | executed | PASS | 2 / 2 / 2 |
| F01-06b | createChangeDraftPostPolicyProposal | 201 | 201 | executed | PASS | 1 / 1 / 1 |
| F01-06c | assessChangeSet | 200 | 200 | executed | PASS | 1 / 1 / 1 |
| F01-06d | applyChangeSet | 200 | 200 | executed | PASS | 0 / 0 / 0 |
| F01-06e | getProjectAssetPolicy | 200 | 200 | executed | PASS | 2 / 2 / 2 |
| F01-07 | startGeneration | 202 | 202 | executed | PASS | 1 / 1 / 1 |
| F01-08 | getGeneration | 200 | 200 | executed | PASS | 2 / 2 / 2 |
| F01-09 | getCandidate | 200 | 200 | executed | PASS | 5 / 5 / 5 |
| F01-10 | getSource | 200 | 200 | executed | PASS | 3 / 3 / 3 |
| F01-11 | acceptCandidate | 201 | 201 | executed | PASS | 2 / 2 / 2 |
| F01-12 | acceptCandidate | 201 | 201 | executed | PASS | 2 / 2 / 2 |
| F01-13 | saveChapter | 201 | 201 | executed | PASS | 2 / 2 / 2 |
| F01-14 | saveChapter | 201 | 201 | executed | PASS | 2 / 2 / 2 |
| F01-15 | saveChapter | 201 | 201 | executed | PASS | 1 / 1 / 1 |
| F01-16 | confirmChapter | 201 | 201 | executed | PASS | 3 / 3 / 3 |
| F01-17 | confirmChapter | 201 | 201 | executed | PASS | 2 / 2 / 2 |
| F01-18 | getProject | 200 | 200 | executed | PASS | 2 / 2 / 2 |
| F01-19a | checkCurrent | 200 | 200 | executed | PASS | 2 / 2 / 2 |
| F01-19b | acknowledgeImpactPreflight | 200 | 200 | executed | PASS | 2 / 2 / 2 |
| F01-19c | stabilizeRevision | 200 | 200 | executed | PASS | 2 / 2 / 2 |
| F01-19 | prepareRelease | 201 | 201 | executed | PASS | 10 / 10 / 10 |
| F01-20 | prepareRelease | 201 | 201 | executed | PASS | 1 / 1 / 1 |
| F01-21 | startExport | 202 | 202 | executed | PASS | 1 / 1 / 1 |
| F01-22 | getExport | 200 | 200 | executed | PASS | 3 / 3 / 3 |
| F01-23 | downloadExport | 200 | 200 | executed | PASS | 1 / 1 / 1 |

## 人工断言（契约自带，runner 一律记 not_verified）

- `F01-06b`（status=not_run）：把「允许用这份资料做模型生成」写成一条显式提案，绑定该资料当前的修订号与项目的上下文修订号；版本不符即冲突，不猜版本。
- `F01-06c`（status=not_run）：变更集评估后才允许应用；服务端的合法前态只有 assessed。
- `F01-06d`（status=not_run）：应用这一步才真正写下 model-use 许可记录（对象 kind = project_asset_policy），并把资料推进到新一版修订。
- `F01-06e`（status=not_run）：把许可读回来看：它带 model_use_allowed=true，且钉在这一版资料修订上。

## 账本与额度（机器读出）

- profile = `lingdoc-qwen-token-budget-20261024-v2`，model = `qwen3.8-flash`
- calls_max = **4**，reserved_tokens = **35780**，actual_tokens = **6150**，cumulative_token_max = **90000**，blocked = **False**
- 账本请求行数 = **4**（两本账本各 4 行；同一窗口的两本账本记录同一次调用）
  - #1 model=qwen3.8-flash status=response_received input=None output=None total=None finish=stop
  - #2 model=qwen3.8-flash status=response_received input=None output=None total=None finish=stop
  - #3 model=qwen3.8-flash status=response_received input=None output=None total=None finish=stop
  - #4 model=qwen3.8-flash status=response_received input=None output=None total=None finish=stop

## 零 embedding 证据（五源交叉核验，机器读出）

- 总体判定 = **PASS**
  - PASS `db_ledger_new_rows_are_chat_calls` :: before=3 after=4 expected_new=1 (the chat calls this round made); a chat call DOES add a row, so the check is 'the new rows are the chat calls', not 'no new row'
  - PASS `db_ledger_no_embedding_model` :: embedding models in ledger = []
  - PASS `vector_table_unchanged` :: total 24 -> 24; dim>0 0 -> 0
  - PASS `json_ledger_no_embedding_row:qwen-beta-sqlite.json` :: embedding_rows=0 rows=4 models=['qwen3.8-flash'] statuses=['response_received'] reserved_tokens=35780 actual_tokens=6150 calls_max=4 blocked=False
  - PASS `json_ledger_no_embedding_row:qwen-beta-sqlite.json.calls.json` :: embedding_rows=0 rows=4 models=['qwen3.8-flash'] statuses=['response_received'] reserved_tokens=35780 actual_tokens=6150 calls_max=None blocked=False
  - PASS `provider_log_no_embedding_block` :: hits=[] files=4
  - PASS `app_log_no_embedding_request` :: embedding request paths=[]; (loose substring hits, mostly field names in read-back bodies: 4)

## 判定口径（来自 docs/08-本轮实施方案/验收判定口径.md v1.1）

- **三视角分离**：契约判定（节点声明的 expected_http vs 实际）／执行器结果（runner 求值的 checks）／人工验收结论（按验收矩阵逐条核实并接受）。三者不互相替代。
- **判定状态四值**：`PASS` / `FAIL` / `NOT RUN` / `DEFERRED_MANUAL`（矩阵登记状态）。
- **运行事实**：`status` 字段（`not_run` / `passed` / `failed`），与 `matrix_status` 是两个字段，**不许互相顶替**。
- **`held`**：证据生产器（`scripts/acceptance/beta_evidence.py`）的五态之一（held / failed / blocked / not_run / PASS）。它表示「这条期望被保留、不下结论」，不是「通过」。
- **`retained_warning` 不是节点判定状态**：它是**待核项处置维度**的取值（`disposition` 字段），含义是「保留该待核警告，不阻断放行，但必须连同原文与理由冻结并出现在交付文件附录」。它与 PASS/FAIL/NOT RUN **不在同一维度**，不能并列成状态。
- **静默 SKIP 一律判 FAIL**；只有显式登记的未运行才允许记 `NOT RUN`。
- `DEFERRED_MANUAL` 不阻塞代码合并，但**阻塞 Beta 放行**。
