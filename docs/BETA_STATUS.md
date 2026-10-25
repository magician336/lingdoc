# Beta 进度状态（唯一进度源）

> **本文件是 lingdoc Beta 实施的唯一进度源。** 每批任务结束后更新它；不要另建进度表。
> **基线**：`origin/main`（本文件合并后即最新基线；每次更新在本行下写明本文件对应的合并提交，避免基线行本身过期）。**审计报告**：`C:\Users\ASUS\AppData\Local\Temp\lingdoc-audit\00-beta-progress-audit.md`。
> **本轮同步基线（第六轮）**：见 **§19**（第十一批）。本轮把 #619 的裁定（A1 / B / C）落地（服务端根因 ＋ 契约侧稳定化链节点），给跑批器补上人工判定录入通道（#633），并把两条判定路径的三处口径残留收干净（#639 / #640）。**F01 契约节点数 28 → 31、checks 58 → 64**（新增三个稳定化节点）。本轮**没有**发起新的真实模型调用；稳定化链在隔离环境上以**零 provider 消耗**实测跑通（`stabilizeRevision` → `STABILIZED`、`prepareRelease` → 201）。⚠️ 本文件**本轮对应的合并提交**＝本 PR 的合并提交（由管理会话落基，**此处不臆造 SHA**）。
> **上一轮（第三轮）同步基线**：`origin/main` = `c14770f9`（PR #591 合并提交）。本轮新合入：**#587**（#578 两本账的「超限」判据收敛为**一份权威清单**，合并提交 `6ed1f3c4`）、**#588**（#545 许可提案不再把待授权资料计入本次操作的来源复核，合并提交 `4b73e8e7`）、**#589**（#576 前端租户角色中文标签收口为唯一来源，合并提交 `a9374cab`）、**#591**（登记处补登记 #576 的 6 条前端路径，PF-14 路径集 57 → 63）。**第二轮**同步（PR #585，合并提交 `24262070`）的基线是 `6b382132`；**第一轮**同步（PR #561，合并提交 `2a664c56`）的基线是 `a98138af`。⚠️ 本文件**本轮对应的合并提交**＝本 PR 的合并提交（由管理会话落基，**此处不臆造 SHA**）。
> **F01 契约基线（第三轮实测）**：`docs/08-本轮实施方案/contracts/workflow.json` = **28 节点 / 58 checks**（`version 0.5.0`，`mode = specification_not_executed_against_provider`）——在 `c14770f9` 的工作树上直接数出，且 `git diff 4b73e8e7..c14770f9 -- contracts/workflow.json` **为空** ⇒ 与最新一轮真实验收（Track-C）所用的契约**逐字相同**。
> **最新一轮真实验收 ＝ Track-D（不是 Track-A／Track-B／Track-C）**：2026-10-24 17:37，代码 `781295c7`（#590 合入后的 main）⇒ **PASS 11 / NOT RUN 16 / FAIL 1**（＝ 28）。⚠️ **判定口径以契约为准**：`workflow.json` 的 `retrieval_modes.meaning.vector_required` 原文写明「该节点确实需要向量检索：**在受控窗口里它是 NOT RUN**」⇒ **F01-07 的放行判定仍是 NOT RUN，不是 FAIL**。⚠️ 但 Track-D 改变了**事实**：它证明了**无 embedding 路线能真的跑通生成链**（全新 keyword-only KB ＋ 真实 Qwen chat 1 次 ＋ 候选稿产出），**并且**暴露出**一个新的真实 FAIL**（F01-11，契约请求体与服务端 HTTP 契约不一致）。逐条事实见 **§17**。**上一轮＝Track-C**（2026-10-24 12:44，代码 `4b73e8e7`）＝ PASS 11 / NOT RUN 17（**无 FAIL**）—— 详见 §16。
> **上一轮＝Track-C**：2026-10-24 12:44，代码 `4b73e8e7`（PR #588 合入后）⇒ **PASS 11 / NOT RUN 17**（＝ F01-07 **一个** ＋ F01-08…F01-23 **十六个**；**11 + 17 = 28 = 契约节点数**）。⚠️ **判定口径以契约为准**：`workflow.json` 的 `retrieval_modes.meaning.vector_required` 原文写明「该节点确实需要向量检索：**在受控窗口里它是 NOT RUN**」⇒ **F01-07 的放行判定是 NOT RUN，不是 FAIL**（它不是代码缺陷）。逐条事实见 **§16**。
> **状态枚举**：代码实现 / 集成（进 main）/ 测试通过 / 验收完成（真实跑过且结论 PASS）——**四维度分别记，不合并**。
> 另有 `NOT STARTED` / `IN PROGRESS` / `IMPLEMENTED_UNVERIFIED` / `INTEGRATED_UNVERIFIED` / `PASS` / `BLOCKED` / `UNKNOWN` / `DEFERRED` / `DEFERRED_MANUAL`。
> **`DEFERRED_MANUAL` 的含义（v1.1 增补，授权票 [#523](https://github.com/magician336/lingdoc/issues/523)）**：**人工前置事项尚未完成**（真人浏览器完整链、外部真实 KB 检索、多账号人工操作、负责人最终审批），但**代码侧条件已满足**，且**已有独立跟进票与写明的解除条件**。
> - **与 `NOT RUN` 的区别**：`NOT RUN` = 没跑**且没有**承接它的解除条件载体；`DEFERRED_MANUAL` = 没跑，**但有票、有解除条件、代码侧可继续**。二者都**不是** `PASS`。
> - **与 `BLOCKED` 的区别**：`BLOCKED` = 现在做不了（依赖缺、无法继续）；`DEFERRED_MANUAL` = 现在**没做**，但可做，且已有人承接。
> - **与 `DEFERRED` 的区别**：`DEFERRED` = 这项工作被**推迟**（范围问题）；`DEFERRED_MANUAL` = 这项**人工验收**没做（完成度问题）。
> - **门禁效果**：**不阻塞**独立开发与满足其他条件的代码合并；**阻塞** Beta 首版放行（验收协议 §6）——**代码合并不等于 Beta 验收通过**。
> - **必带件**：每条 `DEFERRED_MANUAL` 必须同时带**解除条件**与**跟进票号**（`#NNN`），缺任一项 ⇒ 守护测试（`scripts/ci/test_lingdoc_acceptance_status.py`）判失败。写法与判定口径见 [`docs/08-本轮实施方案/验收判定口径.md`](08-本轮实施方案/验收判定口径.md) §5.3。

---

## 1. Beta 首版放行链（必做，严格按依赖顺序）

| # | 任务 | 票 | 负责人 | 代码 | 集成 | 测试 | 验收 | 依赖 | 证据 | 下一步 |
|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 账本写入端修复：F01 生成路径也写真实账本行（`ModelCallLedgerSourceGenerationRun` 接上） | #486 | pf-10 | **PASS** | **PASS** | **PASS** | **PASS（受控真实调用已取证）** | — | PR #509 合并提交 `003efde8`；本地针对性测试 **8/8 PASS**（真表三例：走生成路径 ⇒ 恰好一行、不走 ⇒ 0 行、契约 `startGeneration` ⇒ 能按 `source_kind` 查到）；PR gate = success。**真实验收**：一次受控真实 Qwen chat 调用走生成路径 ⇒ DB `lingdoc_model_call_ledger` 新增**恰好 1 行**（`kind=call`／`source_kind=generation_run`／`run_mode=real`／`model_id=lingdoc-chat-qwen3.8-flash-iso`，行 id `3286e6a0-cd2a-4a07-9609-0f678ab72236`，input/output tokens 2051/519）⇒ 本行口径「生成路径写真实账本行」**已达成**（取证见 §14.2） | 已完成。⚠️ 该次验收发生在**受控 profile 的隔离路径**上 ⇒ **不等于** F01-07 契约节点通过、**不等于** Beta 放行通过；缺口 `GAP-MODEL-CREDENTIAL` **未解除**（§14.4）。⚠️ **本行的 PASS 来自 2026-10-23 的 v1 窗口那一行**（§14.2）—— 2026-10-24 的 #485 两轮实跑（Track-A 与第三轮 Track-C）**都没有**发起任何 provider 请求（**各 0 次**）⇒ **不**来自本轮 |
| 2 | #504 第一条：统一对偶两侧 base-url 口径 | #504（第一条） | pf-10 | **PASS** | **PASS**（PR #513，合并提交 `df6b271a`） | PASS（`scripts/ci` 全套 420 项 OK、F01 runner 回归 130 项 OK、登记处 11 面；管理会话另起进程实跑 4 条 base-url/api-root 守护断言全过、整个守护文件无回归） | NOT RUN | 1 | 单一权威 ＝ 契约 `contracts/openapi.json` 的 `servers[0].url` ＝ `http://localhost:4010/api/v1/lingdoc`；HTTP 对偶侧 15 处 `/api/v1/lingdoc` 字面量归零（实测 15 → 0）；守护测试从「两套相反约定各自钉死」改为**只钉死单一口径** ＋ 两条新断言（含「同一 base-url 喂两侧、同一判定」的回归门） | 已完成。真实 F01 跑通才算验收 |
| 3 | #494 资料标识参数化 | #494 | pf-10 | **PASS** | **PASS** | **PASS** | NOT RUN | — | PR #498 合并提交 `bbd461ac`；守卫 `test_lingdoc_beta_knowledge_id.py` | 已完成 |
| 4 | #504 第二条：解除条件改稳定标识 | #504（第二条） | pf-10 | **PASS** | **PASS** | **PASS** | NOT RUN | — | 同上；`acceptance/gaps.json` ＋ `test_lingdoc_beta_gap_ids.py`（9 项） | 已完成 |
| 5 | #499 断言能力补齐 ＋ **穷举校验** | #499 | pf-10 | **PASS**（PR #530 合并提交 `aa2edb12`；后续 PR #536 `2cdd6656`：求值分派不再落空——没有分支的算子一律报错 ＋ 守护守住「词表 ⊆ 求值分支」） | **PASS**（`aa2edb12`／`2cdd6656`） | PASS（`LingDoc PR gate` = pass、`F01 runner regression` = pass（**146/146**，本轮实测）） | NOT RUN | 3 | 审计：16/67 条缺 kind；kind 词表/校验/求值分三处且**无穷举检查**。**已落地**：`scripts/lingdoc_check_kinds.py`（单一权威 ＋ 穷举校验）＋ `scripts/ci/test_lingdoc_check_kinds.py` ＋ `run_f01.py` 增 5 个 kind ＋ `contracts/validate_artifacts.py` 改 229 行 | pf-10 实施（**已合并**）。⚠️ 两张缺口**仍未解除**：`GAP-PROVENANCE-FIELD`（OpenAPI 无 `provenance` 字段 ⇒ `provenance_real` 断言挂不上）与 `GAP-FAILURE-PATH-NODES`（4xx 节点仍未挂进 F01 序列 —— #501 只落地了形状）⇒ **解除条件**：由 OpenAPI 拥有面声明 `provenance` 字段；由 24 节点口径的拥有方先裁定序列可增节点，再挂 4xx 节点（跟进票 **#499**） |
| 6 | #501 先操作后断言（契约形状） | #501 | pf-10 | **PASS**（PR #574 合并提交 `06d37b69`：契约新增步骤级 `after_step` ＋ 同资源约束；执行器语义「前一步不成立 ⇒ 后继记 `BLOCKED`、不发请求、不求值断言」） | **PASS**（`06d37b69`） | PASS（`scripts.lingdoc_mock.test_workflow_twostep` **16/16 PASS**、F01 runner 回归 **146/146 PASS**，本轮实测；`contracts/validate_artifacts.py` 新增 `after_step` 静态校验 ＋ 4 条反例） | NOT RUN | 5 | 落地用例 **F24「成员撤权后同一资源不再放行」**（`contracts/scenarios.json`）；机器可读形态＝步骤字段 `after_step` ＋ 两步 `request.path_params` 键集与值表达式逐项相同；守护断言＝`scripts/lingdoc_mock/test_workflow_twostep.py` ＋ `validate_artifacts.py::check_same_resource` | 真实后端上的 F24 实跑 **NOT RUN**（本轮 provider 请求数必须为 0）；**资料级撤权**在 WeKnora 侧（`kbShares.RemoveShare`）、不在 OpenAPI 内 ⇒ 契约驱动不了，**仍记为未完成**；`GAP-FAILURE-PATH-NODES` **未解除**（跟进票 **#499**） |
| 7 | #484 材料接入与处理就绪（代码 ＋ 真实验收） | #484 | pf-10 | **PASS**（服务能力已交付：`internal/lingdoc/materiallingest/`，PR #515 → `2966045b`；编排模块 PR #528 → `ac5c869d`） | **PASS**（`2966045b`） | PASS（19 个测试全过、`-race` 过、lint 0 issues、`go build ./...` 过） | **PASS（F01-06 契约节点在隔离环境真实通过）** ⚠️ **但「材料合格」这层不成立**（见证据列） | 5（#499 只影响**断言能力**，不影响本行的**服务端能力**；本行的 PASS 指服务端能力 ＋ F01-06 节点） | 材料身份＝固定前缀＋文件 SHA-256（不含时间戳）⇒ 同一份材料恒等；处理状态取数落在 `evidence.ProcessingStateOf` → `evidence.AssetStateReady` 这条链 ⇒ 与 F01-06 响应里的「就绪」同口径。**实测**：隔离库 `weknora_iso` 上以 owner 身份跑 F01-01～F01-06 ⇒ **7/7 节点 PASS、10/10 checks passed**；F01-06 真实发出 `POST …/assets`（请求体只有 `knowledge_id`）⇒ 响应 `data.processing_state="ready"`；**本轮新增 chat = 0、embedding = 0**（详见 §13）。⚠️ **2026-10-24 #485 实跑的新事实**：F01-06 仍 PASS，但**绑定的材料本身不合格** —— 6 个分块里 2 段引用文本长度 **0**，另 2 段内容长度（477/397）**大于**其坐标跨度（399/328）⇒ 坐标档不可能逐字对上。**第三轮（Track-C，代码 `4b73e8e7`）实测的新事实**：**D1 不再卡 F01-06b** —— #588 修掉的是 **D2**（「授予权限」的操作把**待授权资料的分块**计入本次操作的来源复核），**不是** D1 ⇒ F01-06b/06c/06d/06e 本轮全部 PASS（§16.2）；**但 D1 本身仍未解除** ⇒ 仍需**来源复核**的 F01-09／F01-11／F01-12／F01-16／F01-17 这 5 个节点**本轮未执行** ⇒ **不得**把本行记成「材料合格」。解除路径＝PR **#590**（材料分块与坐标逐字自洽 ＋ 分块与 embedding 解耦），**该 PR 当前 OPEN、未合入 main** ⇒ 不计入「集成」维度 | 按 **#484** 的接入路径**重新接入**一份「分块内容与其坐标在原文上逐字一致」的材料；重跑前先做一次可复跑的前置比对：`GET /projects/{id}/sources/{chunk}` 的 `quoted_text` 与原文 `[start_at,end_at)` 切片逐字相等（不等 ⇒ 材料不合格，不进入 F01）。⚠️ 受控窗口内**无法**重新接入（缺陷 **D5**：无 embedding 模型时解析中止、给了 embedding 模型则被 profile 以 1007 拒；且 KB 更新接口**静默丢弃** `embedding_model_id`）⇒ 需先裁定「材料接入是否划到受控窗口之外」 |
| 8 | #485 在**真实后端**上跑完 F01 全流程（**契约现为 28 节点**，第一个里程碑） | #485 | pf-10 | **PASS**（契约 `workflow.json` ＋ 执行器 `run_f01.py` ＋ 后端生产链就位；本轮 **28/28 节点都有结论**） | **PASS**（`origin/main` = `c14770f9`） | PASS（F01 runner 回归 **146/146 PASS**、契约静态校验 **PASS**（`continuous_trace_steps=28`、`shape_checks=976`），**第三轮在 `c14770f9` 上实测**） | **最新一轮＝第六轮**（2026-10-25）：机器事实 **节点 31/31、机器断言 64 条中 passed 64、failed 0**；**契约判定＝NOT RUN**（唯一原因：4 条散文断言中 4 条尚无人工判定⇒ 提供方语义随之 not_verified；机器侧 0 failed、0 未求值）；**正式 Beta 放行结论＝不予放行**（逐条阻塞项与解除条件见 §19.8–§19.9）。—— **上一轮＝Track-D**（2026-10-24 17:37，代码 `781295c7`）：F01-01～F01-06e **11 个节点真实执行 PASS**（**材料闸门两阶段都跑且都合格**）；**F01-07 ＝ NOT RUN**（契约把它声明为 `retrieval: vector_required` ⇒ 受控窗口里它就是 NOT RUN；**本轮口径不变**）——但 Track-D 已证明**这条生成链在 keyword-only KB 上真的跑通了**：**真实 Qwen chat 1 次**（provider 上报 usage：1037/521/1558，`finish_reason=stop`）、**候选稿产出**、**embedding 请求 0 次**（五路交叉核对）；**F01-08～F01-10 在 Track-D 里真实执行 PASS**（`getGeneration` / `getCandidate` / `getSource` 全 200），但按放行口径它们挂在 F01-07 后面 ⇒ 仍记 **NOT RUN**；**F01-11 ＝ FAIL**（`acceptCandidate` HTTP 400 `invalid_request`；根因＝契约请求体缺服务端 `binding:"required"` 的 `confirmation` 载荷 ⇒ **票 #608**）；F01-12～F01-23 **NOT RUN**（挂在 F01-11 后面）。⚠️ 与上一轮相比**新增一个真实 FAIL**（此前 17 个 NOT RUN 里没有 FAIL）⇒ 放行结论**仍未通过**，但缺口从「授权边界」变成了「可修的契约缺陷」。—— 上一轮（Track-C）＝ **PASS 11 / NOT RUN 17**（无 FAIL），其明细如下 ⇒（2026-10-24 12:44，代码 `4b73e8e7`）：F01-01～F01-06e **11 个节点真实执行 PASS**（**15 条 checks 全过**；**F01-06b 由上一轮的 409 `stale_input` 变为 201**，**F01-06c/06d/06e 首次打通**）；**F01-07 ＝ NOT RUN**（契约把它声明为 `retrieval: vector_required` ⇒ 按 `retrieval_modes` 的口径，受控窗口里它就是 NOT RUN）＋ **F01-08～F01-23 共 16 个节点 NOT RUN**（挂在那次生成运行后面）⇒ 合计 **NOT RUN 17**（**11 + 17 = 28 = 契约节点数**）⇒ 协议 §6「主流程不能含 FAIL/BLOCKED/NOT RUN」⇒ **不通过**。⚠️ **两个视角要分开读**：**机器产物**（`track-c/f01-report.json`）把 F01-07 记为 `runner_status = "failed"` ＋ `failed_step = {id: F01-07}`（执行器视角：它**被尝试过**并返回 503 `dependency_unavailable`）；**契约判定**（放行口径）是 **NOT RUN**（根因＝**授权边界**，不是代码缺陷，见 §16.4）⇒ **不得**因为机器产物写了 `failed` 就把 F01-07 记成 FAIL。⚠️ **不得**把「F01-06b 通过」说成「F01 通过」；⚠️ F01-06b～06e 的**语义**断言是人工判定的（机器口径 `manual_assertions.status = not_run`，见 §16.3） | 1–7（#501 已合入 ⇒ 依赖解除；#545 已合入 ⇒ 许可节点在序列里） | **最新一轮（Track-C）证据目录**：`D:\wt\lingdoc-485-evidence\track-c\`（`TRACK-C-REPORT.md` 完整报告 ＋ `f01-report.json` 契约执行报告）；**服务端原始日志** `D:\wt\lingdoc-485-evidence\logs\server-next.stdout.log`（第 433–437 行＝F01-07 的检索→embedding→503 全链）；**embedding 被拒的原始证据** `D:\wt\lingdoc-485-evidence\logs\llm_debug\3724894d-490d-4599-a63c-9c31d491f625.log`（错误原文 `model is outside the acceptance budget profile`）。**上一轮（Track-A，代码 `b63aa79f`）的证据**仍在同一目录根下（`node-verdicts.md` 逐节点结论 ＋ `defects.md` ＋ `gaps.md` ＋ `ledger-status.md` ＋ `track-a/f01-report.json` ＋ `track-a-409-log.txt`），用于对比。**契约实测规模**：`steps = 28`（原 24 ＋ #545 的 4 个 model-use 许可节点 `F01-06b`～`F01-06e`）、**`checks = 58`**（原 50）⇒ 登记处注记已同步（PR #582 `e232bf0d`） | **D2 已解除**（PR #588 `4b73e8e7` 合入 ⇒ F01-06b 由 409 `stale_input` 变 **201**，06c/06d/06e 一路打通，许可读回 `model_use_allowed=true` 且钉在 `asset_revision=2`）⇒ **当前只剩一条阻塞**（且**不是**代码缺陷）：**F01-07 需要的 embedding 不在受控 profile 白名单内** ⇒ 受控 profile 在 **transport 之前**就拒了它（`model is outside the acceptance budget profile`）⇒ 真实 provider 请求 **0 次**。**契约判定：F01-07 ＝ NOT RUN**（`retrieval_modes` 口径；机器产物里它是 `failed_step` ⇒ 两个视角见 §16.3）—— **不得**把它记成 FAIL。**解除条件（唯一）**：把 embedding 纳入受控 profile 并**拆分 chat / embedding 额度**（#546 口径①；embedding 授权**当前未批**）⇒ 解除条件载体＝`scripts/lingdoc_mock/controlled_window_check.py` 的 `VECTOR_REQUIRED_UNBLOCK`（跟进票 **#546**／**#480**）。**仍未解除的两条材料侧缺陷**：**D1** 材料分块内容与坐标不自洽（⇒ **#484**；跟进 PR **#590** **未合**，不得记为已修）｜**D5** 受控窗口内无法重新接入材料 ＋ KB 更新接口静默丢弃 `embedding_model_id`（⇒ **#484**）。⚠️ **不得**改 `retrieval` 标签、**不得**关限制、**不得**伪造结果让 F01-07 变绿 |
| 9 | #486 真实模型选段改写（账本非 0 行＋switch 行） | #486 | 待派 | NOT STARTED | — | — | NOT RUN | 8 ＋ 人工前置 1 ＋ **受控 Qwen profile（PR #526 `dfa52944` ＋ #544 `232e0899`；v2 profile PR #583 `b63aa79f`）** | **账本非 0 行仍成立但只有历史那一行**：DB `lingdoc_model_call_ledger` 总行数 **1**（v1 窗口 2026-10-23 的调用，`source_kind=generation_run`／`run_mode=real`／`model_id=lingdoc-chat-qwen3.8-flash-iso`），**2026-10-24 的 #485 实跑新增 0 行**（provider 请求 **0** 次）。⚠️ **switch 行仍未产生**：该次调用没有模型切换，且 `RecordModelSwitch` 在 `origin/main` 上**只有接口定义与实现、没有生产调用点**（`internal/lingdoc/workspacecore/model_call_ledger.go:115`／`:185`，其余命中全在测试文件里）⇒ 本行口径「账本非 0 行**＋switch 行**」**只满足一半** ⇒ 按 §5 口径记 `NOT RUN`（**非** `DEFERRED_MANUAL`：代码侧条件不具备） | 人工前置 1 就位后派；执行前置三条：① 注册一行 `type=KnowledgeQA` 的 Qwen chat 行（`config/builtin_models.yaml` **不在仓内** ⇒ 本地 yaml 或直接注册；受控调用那次用的是隔离库行 `lingdoc-chat-qwen3.8-flash-iso`、`parameters.provider` 字面 `aliyun`）② 显式设置受控 profile（**不设 ⇒ 无次数上限、无费用口径**，见 §6 人工前置 1）③ 凭据：用正确的 `SYSTEM_AES_KEY` 或用该 key 重新注册模型行（**注意别把密钥明文落库**，见 §12.4 风险 3）。⚠️ **另需**：switch 行的写入路径（`RecordModelSwitch`）尚无生产调用点 ⇒ 需开票 |
| 10 | #487 隔离 PostgreSQL 那一轮 | #487 | 待派 | NOT STARTED | — | — | NOT RUN | 9 ＋ 人工前置 2 | **本轮已在隔离 PostgreSQL 上真跑 F01**（隔离库 `weknora_iso`、容器 `WeKnora-postgres-dev`、Redis DB 3、`AUTO_MIGRATE=false`）；但**「服务重启后的读回」未做**（本轮未执行 `scripts/acceptance/lingdoc_full_server_restart.py`）⇒ 缺口 `GAP-RESTART`（跟进票 **#480** 缺口 3） | 用 `--db-driver postgres --db-dsn <隔离库 DSN>` 跑重启读回（协议 §1 要求隔离 PostgreSQL）—— **解除条件**：重启后重读项目/资料/许可/账本四类行并留证（跟进票 **#480**） |
| 11 | #488 Word 人工检查 | #488 | 待派 | — | — | — | NOT RUN | 9 ＋ 人工前置 4 | 协议 §4：**程序库 round-trip 不能替代**。本轮 F01 未走到 F01-21…F01-23 ⇒ **没有 DOCX 可查** ⇒ 缺口 `GAP-WORD`（跟进票 **#480** 缺口 5） | F01 走到下载后，填写 `word-check.md` 的五个必填字段（Word 产品版本 / Windows 版本 / 原始 DOCX SHA-256 / 修改后 SHA-256 / 检查结果）—— **解除条件**：用真实 Microsoft Word 跑完「开/改/存/关/重开」并回填五个必填字段（跟进票 **#480**） |
| 12 | #489 第二人独立复现（**记录项，不阻塞**） | #489 | 待派 | — | — | — | NOT RUN（记录项，**未完成项**） | 9 | 口径裁定（#483）：**不是**首版门槛，但必须登记为未完成项；本轮**未复现**（`gh issue view 489` = OPEN）⇒ **不得**记为已通过 | 最后一批。**原因**：本轮没有第二名开发者执行独立复现。**解除条件**：由第二名开发者按 #489 的步骤独立复现 F01 契约并把证据绑到当前 head（跟进票 #489） |
| 13 | #490 放行结论表 ＋ 明确未放行项清单 | #490 | 待派 | NOT STARTED | — | — | — | 8–12 | — | 收口。**本轮 #485 已产出可直接入表的逐节点结论**：28 节点＝**7 PASS（真实执行）／1 BLOCKED（F01-06b，409 `stale_input`）／20 NOT RUN**，外加逐条缺口与解除条件（`D:\wt\lingdoc-485-evidence\node-verdicts.md` ＋ `gaps.md`） |

## 2. 规格与文档（并行面，不碰代码文件）

| 任务 | 票 | 负责人 | 状态 | 证据 / 备注 |
|---|---|---|---|---|
| 统一验收口径（11 个文件：18 号 §3/§8/§9、21-G6 草稿、G2/G4/G6 各线、**验收协议只加「= 契约当前 24 节点」的注明、门槛一字未动**、`G6-质量运行门禁.md` 23→24、GLOSSARY 与 16-术语表补「Beta 首版」范围词条、01 号蓝图） | #483 | pf-6 | **已合并**（PR #497，合并提交 `1e95a0e7`） | 逐条核实过：只改口径、不动门槛。**L6 术语表锁**由它持有 |
| 第二人复现口径统一（21-G6 草稿 / G2-spec.md:70 / G4-development-plan.md:130） | #483 扩展 | pf-6 | **PASS**（含在 #497 的 11 个文件内） | 规格清点 C-1 ⇒ 已改 21-G6 草稿 / G2-spec.md:70 / G4-development-plan.md:130 |
| Word / WPS 口径统一（18 号 §8 ＋ G6 全线） | #483 扩展 | pf-6 | **PASS**（含在 #497 的 11 个文件内） | 规格清点 C-2 ⇒ 已改 18 号 §8 ＋ G6 全线 |
| F01 节点数 23→24（`G6-质量运行门禁.md:27`） | #483 扩展 | pf-6 | **PASS**（含在 #497 的 11 个文件内） | 规格清点 C-3（实测契约 24 条）⇒ `G6-质量运行门禁.md:27` 已 23→24 |
| 「Beta」vs「Beta 首版」范围对象（16-术语表 §1、01 号蓝图 §3、GLOSSARY 缺词条） | #483 扩展 | pf-6 | **PASS**（含在 #497 的 11 个文件内） | 规格清点 C-5 / C-9 ⇒ 已改 16-术语表 §1、01 号蓝图 §3、GLOSSARY 补词条 |
| #502 spec 落盘副本**重做**（现副本与 #482 相似度 0.87，已过期） | #491 | pf-6 | **重做中** | ⚠️ **不得复用过期副本**；缺「4 类新表达力」决策、kind 列表仍 8 种（应 10）、权威表述相反（自称「以本文件为准」而 #482 说相反） |
| 非 Beta 延期清单（**只出清单，不批量关票/改标签**） | — | pf-6 | **已合并**（PR #507，合并提交 `6f0674b6`） | 72 张 open 票逐条判定，**55 张建议 DEFERRED**；清单首段写明「本清单只是清单，不执行任何变更」 |

## 3. 共享文件锁（一把锁同一时刻只有一个持有者）

| 锁 | 文件 | 当前持有者 | 排队中 | 规则 |
|---|---|---|---|---|
| **L1 登记处** | `scripts/lingdoc_parallel_surfaces.py` | **PF-14 / pf-10**（自 #498 合并起） | PF-7（#503 交面）、PF-9（#495 交面）、PF-11（#149）、PF-13（#248） | 重复声明会让 **main 加载失败**（loader 拒绝重名路径）⇒ 必须逐个合，每次合完其余重新落基 |
| **L2 路径图** | `docs/08-本轮实施方案/G1-G7-Spec-完成路径图.md` | **PF-14 / pf-10**（与 L1 绑定：交面必须同时改两处） | PF-7、PF-9、PF-11、PF-13 | 无机器守护 ⇒ 静默不同步风险 |
| **L3 契约产物** | `docs/08-本轮实施方案/contracts/openapi.json` | **PF-11 / #149**（先合者） | PF-13（#248 改借用） | — |
| **L4 保留路径** | `frontend/src/views/lingdoc/Workspace.vue` | **PF-11 / #149** | PF-13（#248） | **保留路径：任何面不得独占**；只如实记录改动事实 |
| **L5 生成路径** | `internal/lingdoc/workspace/generation.go` | **PF-2**（已交面） | **PF-14 借用**（账本修复必须改它：F01-07 走的就是它）；**PR #526**（`generation.go:405` 一行，已获管理会话改判批准，**已合入** `dfa52944`）；**票 #534**（`EnqueueGenerationAfter` 重试上限，**已落**：PR #554 `1df56366` —— 与正常路径共用同一个数字 `generation.GenerationTaskMaxRetry` = 3） | owner 仍是 PF-2；借用方在 summary 记一笔借用事实，**不得**声明进自己路径集 |
| **L6 术语表** | `GLOSSARY.md`、`docs/06-灵档产品开发规划/16-术语表.md` | **PF-6**（PR #497） | 任何要改术语的会话 | 全仓术语的唯一权威 ⇒ 同一时刻只允许一个会话改；改完即释放 |

⇒ **各面的「实现」可并行**（代码文件互不重叠）；**「交面」必须排队**，因为交面要同时改 L1＋L2。

## 4. 工作区 — 分支 — PR — 会话 对应表（只处理属于本任务的；其余 83 个 worktree 属其他 harness，一律不动）

| 会话 | 工作区 | 分支 | HEAD | 在做 | PR |
|---|---|---|---|---|---|
| 管理会话（本） | `.worktrees/feat-auto-20261020-ee557d31` | `codex/t3-workflow-wiring` ⚠️ **与 codex harness 共享** | `1837e6ea` | 协调 / 进度源 | — |
| pf-10 | `.worktrees/feat-auto-20261023-6a598156` | `feat/494-knowledge-id` | `5d340979` | 放行链代码链（#494→#504→#499→#501→#484→#485）＋ 账本修复 | #498 **已合并** |
| pf-6 | `.worktrees/feat-auto-20261020-1d2a548e` | `docs/491-beta-release-spec-doc` | `e0559b93` | 口径统一 + #502 重做 + 延期清单 | #502 / #497 |
| pf-1 | `.worktrees/feat-auto-20261020-ac02c1f4` | `feat/460-pf7-audit-chain` | `cbf07c1f` | PF-7（#460） | #503（落后 6 ⇒ 待落基） |
| 360 | `.worktrees/feat-auto-20261021-d60d835d` | `feat/pf9-owner-freeze-attribution` | `4805bb75` | PF-9（#461） | #495（落后 6 ⇒ 待落基） |
| #149 会话 | ⚠️ **不在** `.worktrees/feat-auto-*` ⇒ 实际在 `D:\tmp-pr149-r6`（`rebase/pr149-v6`） | `rebase/pr149-v6` | `020a1746` | PF-11（#129） | #149（61 文件，落后 6） |
| #248 会话 | `.worktrees/_redo248h`（`redo/pr248-v9`）；`.worktrees/feat-auto-20261020-c25d5fd2` 是**同分支的另一 worktree**（HEAD 停在 `5eab4127`） | `redo/pr248-v9` | `6530c619` | PF-13（#139） | #248（30 文件，BLOCKED） |
| ⚠️ 主仓库 | `D:\code_warehouse\Projects\lingdoc` | `main` | `a17eeaaf` | **只读保留**（落后 origin/main 66 个提交） | 有一笔**未提交**的登记处改动（PF-9 `available→claimed` ＋ 三条路径前缀修正）⇒ **与 PR #495 里的同一件事重复**；直接提交会回退大量内容 ⇒ 处置见审计 G.1 |

> ⚠️ **本表各 HEAD 取自本文件自身，未经本轮实测**（本轮未读任何别人的工作区）⇒ **以各会话自己的 summary 为准**；需要实测时用 `git -C <工作区> rev-parse HEAD` 逐个核对。

## 5. 已统一的验收口径（不可再分歧）

1. **第二人独立复现 = 记录项**，不是 Beta 首版硬门槛；但必须登记为未完成项（原因＋解除条件），不得记为已通过。依据：验收协议 §1/§6（专门定义「通过」、穷举列举）vs 18 号 §3/§8/§9（开发计划叙述）⇒ 以协议为准。
2. **真实 Word 检查以 Microsoft Word 为准**，WPS 不增加为硬门槛（协议 §4）。
3. **F01 按实际契约的节点数执行**（**第六轮实测：31 个节点**＝24 ＋ #545 的 4 个 model-use 许可节点 `F01-06b`～`F01-06e` ＋ #619 的 3 个稳定化节点 `F01-19a`～`F01-19c`（交付检查回执 → 闭环发布阻断任务 → 稳定化）；`checks = 64`）——⚠️ **本条的数字随契约演进**：第一轮口径统一（PR #497）时是 24（23 业务步 ＋ `F01-03b recordActivationRead`；文档里的「23」是同义拆法），#545 的许可节点合入后（PR #577 `88845c5d`）变为 28，登记处注记已同步（PR #582 `e232bf0d`）⇒ **以 `docs/08-本轮实施方案/contracts/workflow.json` 的当轮实测为准**，不要引用本文件里的历史数字。
4. **Beta 首版 = 验收协议定义的减量范围**；不等于完整 Beta 设计，也不等于完整 G1–G7。

## 6. 人工前置 6 项（当前状态）

| # | 项 | 状态 | 缺什么 | 是否阻塞 |
|---|---|---|---|---|
| 1 | Qwen 真实模型调用（`qwen3.8-flash` / OpenAI 兼容） | **DEFERRED_MANUAL**（⚠️ **本行记的是 #485 那一轮（2026-10-24）的事实**；最新一轮的真实调用事实见 §20.6 与 §20.11 第 6 项：模型账本**累计 4 行**（其中 **3 行属 10-24 更早的轮次**，**第 4 次**才落在最终跑批 `f01-20261025T013341Z-5105ae32` 那一轮）、额度 **4/4 用尽**，且**检索与向量相关的那部分仍 `NOT RUN`**）｜#485 那一轮在真实后端上实跑：**provider 请求 0 次、4 次额度一次未用**；F01-07 那一次**仍未发生** | **本轮实测事实（#485，2026-10-24）**：受控 profile ＝ **v2** `lingdoc-qwen-token-budget-20261024-v2`（PR #583 `b63aa79f`：调用数上限 **4**、累计预留上限 **90,000** token、单次 input ≤16,000 / output ≤4,096、获准模型仍**只有** `qwen3.8-flash`、v1 行为逐字未变）；**真实 provider 请求数 = 0**；**4 次额度一次未用**（累计预留 0/90,000）；**两本账本文件都不存在**（`qwen-beta.json` ＋ `.calls.json`）⇒ 窗口**零预留零消耗**；DB 账本 `lingdoc_model_call_ledger` 总行数仍 **1** —— 那一行是 **v1 窗口 2026-10-23 的历史行**（`source_kind=generation_run`／`run_mode=real`／2051/519 tokens，§14.2），**本轮新增 0 行**；环境检查 CHECK1–CHECK9 全 true ＋ `ALL_PASS=true`。**仍未做**（**第三轮后的唯一原因**）：① ~~**D2** 许可链断在 F01-06b（409 `stale_input`）~~ ⇒ **已解除**（PR #588 `4b73e8e7` 合入；**第三轮 Track-C 实测 F01-06b = 201**，06c/06d/06e 一路打通，许可读回 `model_use_allowed=true` 且钉在 `asset_revision=2`）② ~~Run B 实测：许可缺失时 `POST /generations` 返 **422 `asset_not_authorized`**~~ ⇒ **该前置已不成立**（许可已真实建立）③ **D3 仍在**：契约把 F01-07 声明为 `retrieval: vector_required`（`workflow.json` 实测只有 F01-07 带这个标签），而受控 profile 只放行 chat、**拒 embedding** ⇒ **第三轮 Track-C 实测 F01-07 返 503 `dependency_unavailable`**（`retryable=true`），embedding 请求在 **transport 之前**被受控 profile 拒掉（原文 `model is outside the acceptance budget profile`）⇒ 真实 provider 请求仍为 **0**。⇒ **契约判定：F01-07 ＝ NOT RUN**（`retrieval_modes` 的口径；机器产物里它是 `failed_step` —— 两个视角见 §16.3）⇒ **不是**代码缺陷。**解除条件（唯一）**：把 **embedding 纳入受控 profile 并拆分 chat / embedding 额度**（#546 口径①；**embedding 授权已批并已执行（2026-10-25）**：口径裁定与新 priced embedding profile 已落地（PR #668，合并提交 `79613798`），真实 provider 请求 **1 次**被 provider **403 `insufficient_quota`** 挡住 ⇒ **向量检索仍未验证**，详见 §20.13）⇒ 解除条件载体＝`scripts/lingdoc_mock/controlled_window_check.py` 的 `VECTOR_REQUIRED_UNBLOCK`；之后在受控 profile v2 下重跑 F01-07 ⇒ 账本出现 `source_kind=generation_run` 的真实行（跟进票 **#546**／**#480**；配套 **#484**（材料接入，**D1**／**D5** 仍未解除））。⚠️ **不得**改 `retrieval` 标签或关限制来让 F01-07 变绿。⚠️ **一次受控调用成功 ≠ F01-07 验收通过 ≠ Beta 放行通过**（§15.5）。⚠️ **不设** `LINGDOC_T10_ACCEPTANCE_PROFILE` ⇒ 空预算 ⇒ **没有调用次数上限、没有费用口径、没有预留/实付记账** ⇒ 「一次真实调用在预算内」**没有机器强制**；映射与坑不变：`LLM_MODEL_NAME=qwen3.8-flash` / `LLM_BASE_URL=https://llm-k57eqvdqacn4nov3.cn-beijing.maas.aliyuncs.com/compatible-mode/v1` / `LLM_API_KEY=<与 DASHSCOPE_API_KEY 同值>`；⛔ **不要写 `LLM_API_KEY=${DASHSCOPE_API_KEY}`**（`env_file` 原样传值、应用内插值只做一遍 ⇒ 会变成字面量）；⚠️ **设** `LINGDOC_T10_ACCEPTANCE_PROFILE` 会把 F01 的 chat 强制成 DeepSeek ⇒ 用 Qwen 时不要设 | **是**（卡 F01-07） |
| 2 | 隔离 PostgreSQL ＋ 重启读回 | **已解除**（本批前完成，见 §20.11 第 4 项；「重启读回」已做） | 隔离 PG 库 `weknora_iso_r7`（容器 `WeKnora-postgres-dev`）＋ 真实进程重启：项目/章节/来源绑定/下载哈希**逐字段相等**（A 轮 31 叶子、B 轮 36 叶子，**0 处不等**）；psql 直读证明行物理在 PG 内；**零 provider**（证据 `D:/wt/lingdoc-484-evidence/pg-r7/`）。⚠️ **章节级 `source_ids` 非空仍 `NOT RUN`**（需真实 WeKnora 解析 ⇒ 依赖 embedding 授权 **#546**），那一部分随 **§6 表第 5 行**（WeKnora 真实接入/处理/检索）与 §20.11 第 6 项阻塞 | 否（重启读回已解除；`source_ids` 随第 5/6 项） |
| 3 | 两章合成材料 ＋ `--material` SHA-256 | **材料已备好**（仓外 `lingdoc-prep/` ＋ 本轮 `synth-chapter-c.md` 2004 B）⚠️ 但**「同一套两章材料 ＋ 权威 SHA-256」未按协议登记** | 库中材料 `128b88fd…` 的 `file_hash=21c29a574dbbdb6ff156ebdc5071dafc` 已记录，但**原文文件已从盘上丢失**（`resources` 行指向的物理路径在所有 storage 根下都不存在）⇒ 无法给出「同一套两章材料」的权威哈希 ⇒ 缺口 `GAP-MATERIAL-HASH`（**#480** 缺口 4）＋ `GAP-D4`（**#484**）。⚠️ 该材料的分块内容与其坐标**不自洽**（缺陷 D1）⇒ 材料本身**不合格**，需按 #484 的接入路径重新接入（§1 第 7 行）。**解除条件**：原文文件在盘上后用 `--material <名称>=<sha256>` 登记本轮合成材料的哈希（跟进票 **#480**；材料重做见 **#484**） | **是**（卡 F01-06） |
| 4 | Word 人工检查（开/改/存/关/重开） | **DEFERRED_MANUAL**（**协议 §4 的机械动作与结构完整性已全部满足并 PASS，见 §20.9**；剩两项人眼确认） | 代码侧已具备：仓内 `docs/08-本轮实施方案/acceptance/beta-first/<sha>/<run>/word-check.md` 模板与五个必填字段的校验已就位。**DOCX 已落盘并已用真实 Microsoft Word 跑完「开/改/存/关/重开」**（`export-196b61e0….docx`，sha256 `df5549b0…`；修改后 `…word-modified.docx`，sha256 `3e336eeb…`；12 项全过；原始文件逐字节未变 ⇒ §20.9）⇒ **只差负责人本人的两项人眼确认**（「基本格式仍可用」的观感 ＋「3 条待核项保留理由表述是否恰当」）⇒ 缺口 `GAP-WORD`（**#480** 缺口 5）。**解除条件**：负责人打开修改后的文件看一眼版面，并把两项人眼确认写回 `word-check.md`（跟进票 **#480**） | **是**（**验收协议 §6** 第 5 项，见 §20.11） |
| 5 | WeKnora 真实接入 / 处理 / 检索 | NOT RUN（**接入 ✔、解析 ✔、词法检索命中 ✔**（2026-10-25，§20.13）；**「向量处理 ＋ 向量检索」仍未取得**） | 端点已定位：上传 `POST /api/v1/knowledge-bases/{id}/knowledge/file`（`routes_knowledge.go:73`、`handler/knowledge.go:254`，multipart 字段 `file`）；解析状态表 `knowledges.parse_status`＝`completed`（`types/knowledge.go:43-64`），`GET /api/v1/knowledge/:id`；检索 `POST /api/v1/knowledge-bases/{id}/hybrid-search`（`routes_knowledge.go:229`）。三个容器**本机全在跑**。⚠️ `env_facts.py` **已不在机器上**（要重写：幂等标识必须**与时间无关**＝固定标题前缀＋文件 SHA-256，否则每次造新知识、id 每次变；输出须按 starting_state 裁剪，否则 `load_state.py` 的 `preflight()` 在首个写请求前退 1）。**2026-10-24 #485 实跑的新事实（推翻了本行先前「docreader 解析接口从未真调过」的说法）**：新建关键词型 KB 后上传材料**成功**、docreader **真的解析出 2 个分块**（`Split document into 2 chunks`），但随后 `processChunks get embedding model failed`（`error_message = "Embedding model unavailable: model ID cannot be empty"`）⇒ **`parse_status=failed`、0 分块落库** ⇒ 「处理完成」与「检索命中」**仍未取得** ⇒ 缺口 **D5**（**#484**）＋ 受控 profile 拒 embedding（**#546**）—— ⚠️ **2026-10-25 更新（§20.13）**：受控 profile **已不再拒 embedding**（新 priced embedding profile，PR #668），**词法检索命中已取得**（目标 KB 2 条查询各命中 2 个分块、全部属目标材料、**0 次模型调用**），**章节级 `source_ids` 已逐条直接核实（8/8 PASS）**；**仍未取得的是向量处理与向量检索**（provider **403 `insufficient_quota`** ⇒ 0 向量，票 **#671**） | **是**（卡 F01-06） |
| 6 | 第二人独立复现 | NOT RUN（记录项；**已由负责人豁免 `WAIVED_BY_OWNER`**，见 §20.10） | **未完成项**（按 §5 口径 1：第二人独立复现**不是** Beta 首版硬门槛 ⇒ **不写** `DEFERRED_MANUAL`，但**必须**登记原因＋解除条件，**不得**记为已通过）。**原因**：本轮没有第二名开发者执行独立复现；**负责人已决定本次 Beta 不再执行、不再等待**（豁免标记 `WAIVED_BY_OWNER`，事实状态仍 `NOT_RUN`）。**解除条件**：由第二名开发者按 #489 的步骤独立复现 F01 契约并把证据绑到当前 head（跟进票 **#489**）。第二个开发者 | 否（不阻塞） |

## 7. 本批（第一批）完成汇报

- **本批实际完成**：审计报告交付；`#498`（#494 ＋ #504 第二条）**合并**（`bbd461ac`）；共享文件锁 L1–L4 建立；#502 判定为**需重做**（落盘副本过期）；Pf-10 / pf-6 / 材料准备 三路派单。
- **四维度状态（**当前值**，2026-10-24 **第三轮**按 `origin/main` = `c14770f9` 逐行复核）**。**统计口径：以 §1 放行链表的 13 行为准；四个维度分别统计，不合并成一个数字**（历史快照：第一批 2/2/2/0、第一轮 6/6/6/2、第二轮 8/8/8/2）：
  - **代码实现状态**：**PASS 8 项**（#486 账本写入端、#504 第一条、#494、#504 第二条、#499、#501、#484、#485）／`NOT STARTED` 3 项（#486 真实模型选段改写、#487、#490）／未开始 2 项（#488、#489）。
  - **集成状态（进 main）**：**PASS 8 项**（同上 8 项，均已在 `c14770f9` 上）／其余 5 项未开始。
  - **自动化测试状态**：**PASS 8 项**（同上 8 项）／其余 5 项未开始。**第三轮本人在 `c14770f9` 上实测**：F01 runner 回归 **146/146 PASS**（`python -m unittest discover -s scripts/lingdoc_mock -p 'test_workflow*.py'`）、契约静态校验 **PASS**（`continuous_trace_steps=28`、`shape_checks=976`）、`go test ./internal/models/acceptancebudget/...` **PASS**、三本账本一致性守护（#567/#578）**3 个测试全 PASS**、前端角色标签守护（#570/#576）**21/21 PASS**。
  - **真实验收状态**：**PASS 2 项**（#486 账本写入端 —— 受控真实调用写出 `source_kind=generation_run` 账本行，§14.2；#484 —— F01-06 契约节点真实通过）／**NOT RUN 1 项**（#485 —— **最新一轮＝Track-C**：**PASS 11 / NOT RUN 17**＝F01-07 **一个** ＋ F01-08…F01-23 **十六个**，**11 + 17 = 28**；F01-07 的 HTTP 503 与根因是**人工从服务端日志补齐**的（机器产物缺项，票 **#592**），而**契约判定是 NOT RUN**（`retrieval_modes` 口径），§16.2／§16.4）／**NOT RUN 9 项**（#504 第一条、#494、#504 第二条、#499、#501、#486 真实模型选段改写、#487、#488、#489）／未开始 1 项（#490）。
  - ⚠️ **真实验收 PASS 2 项 ≠ Beta 放行通过**：协议 §6 要求主流程无 FAIL/BLOCKED/NOT RUN/DEFERRED_MANUAL ⇒ **仍不达成** —— #485 主流程 **17 个节点 NOT RUN**（F01-07 一个 ＋ F01-08…F01-23 十六个），#489 仍是未完成项，#490 未开始。
  - ⚠️ **第三轮的唯一进展与唯一阻塞**：进展＝**D2 已解除**（F01-06b 由 409 变 201，06c/06d/06e 首次打通）；阻塞＝**F01-07 需要的 embedding 不在受控 profile 内**（在 transport 之前被拒，真实 provider 请求 **0** 次）⇒ **契约判定是 NOT RUN（授权边界，不是代码缺陷）**，解除条件见 §16.4。**「11 个节点通过」不等于「F01 通过」。**
- **剩余 Beta 必做项**：第 1–2 项（账本修复、base-url 口径）在做；第 5–13 项待做；人工前置 6 项待就位。
- **当前阻塞与风险**：① 账本写入端（P1）—— 不修则 #486 永远拿不到 PASS；② base-url 口径 —— 不修则浏览器对偶结构性不可跑；③ L1/L2 锁串行 ⇒ 每次合并后其余 PR 都要重新落基（门禁轮次 = PR 数）；④ 材料/WeKnora 解析链路风险最高（脚本丢失 ＋ 解析接口从未真调）；⑤ 主仓库旧 `main` 工作区有重复的未提交登记处改动。
- **下一批与并行**：代码链由 pf-10 串行推进（1→2→5→6→7→8）；文档链由 pf-6 并行（口径统一 → 延期清单 → #502 重做）；材料/Qwen 映射由准备会话并行；**环境侧（隔离 PG、WeKnora 解析最小验证）另派有边界的探测**。

## 8. 下一批之后的集成与验收顺序

1. 逐个核实 PR 的当前基线 / 差异 / 门禁（**不采信历史报告的 CLEAN 或 gate pass**）；每次只集成一个涉及共享文件的 PR；合并后重新核对其余 PR。
2. 放行链修复完 ⇒ 优先跑完 SQLite 的 24 节点 F01（#485）⇒ 第一个完整可验证里程碑。
3. 随后集中执行真实 Qwen / WeKnora / 隔离 PostgreSQL / Word 验收。
4. 第二人复现登记为未完成项，不阻塞首版。
5. 最后 #490 放行结论表，逐项 PASS / FAIL / NOT RUN / BLOCKED ＋ 解除条件。

## 9. 第二批进展（2026-10-23 17:00 后）

- **已合并**：#505（`docs/BETA_STATUS.md` 唯一进度源，合并提交 `76945d3a`）、#498（`bbd461ac`）。
- **已合并**：#497（口径统一 11 文件，合并提交 `1e95a0e7`）、#507（DEFERRED 清单，合并提交 `6f0674b6`）。
- **锁表增至 6 把**：新增 **L5**（`internal/lingdoc/workspace/generation.go`，owner=PF-2、PF-14 借用；账本修复必须改它）与 **L6**（`GLOSSARY.md` ＋ `16-术语表.md`，术语唯一权威，当前由 #497 持有）。
- **#503（PF-7）已按指示拆开**：20 → 18 个文件，**登记处与路径图已摘出** ⇒ 不占 L1/L2；交面另开票排队。
- **账本修复（放行链第 1 项）进行中**：pf-10 已定位到 `generation.go` 归 PF-2 的冲突并改为借用形态；修法＝**账本实例下沉到一处、所有适配器共用**（一个拥有者、多个调用方）。**硬要求已下达**：守护测试要分别断言「走生成路径 ⇒ 账本多一行」「不走 ⇒ 不多行」，且 F01 `startGeneration` 的调用能按 `source_kind` 查到。
- **环境侧已就绪的部分**：两章合成材料 ＋ SHA-256（可复用，勿另造）、Qwen 三行映射（注意 `${}` 插值坑）、WeKnora 三个端点与 `env_facts.py` 实现规格、隔离 PG 口径＝**独立库名不碰 dev 库**（还差：本轮专用隔离库、迁移到 134（dev 停在 122）、当前 head 二进制（仓外 exe 是 09-27 旧构建）、DSN、6 个 DB 环境变量）。
- **⚠️ 暂停待授权项（尚未执行）**：真实 Qwen 调用、任何不可逆操作、需要改验收门槛的改动。

## 10. 第三批进展（2026-10-23 18:00 后）

- **放行链第 1 项（账本写入端）已合并集成**：PR #509，合并提交 `003efde8`。四维度分别记：代码 **PASS** ／ 集成 **PASS** ／ 自动化测试 **PASS**（本地针对性 8/8 PASS ＋ PR gate success）／ 真实验收 **NOT RUN**（真实模型调用待授权，缺口 `GAP-MODEL-CREDENTIAL`）。⚠️ **本条的真实验收状态已被 §14 取代**：一次受控真实调用已发生 ⇒ 该行「真实验收」现记 **PASS**（§14.2）；缺口 `GAP-MODEL-CREDENTIAL` **未解除**（§14.4）。
- **修法与所有权**：账本实例下沉到 `SourceRuntime`（PF-14 新声明该路径），组合根统一注入，所有适配器共用**一个**写入点（`modelCallFacts` ＋ `recordModelCallRow`）；`internal/lingdoc/workspace/generation.go` 仍由 **PF-2** 持有（PF-14 借用，锁 L5，owner 未变）；生成运行 id 走 **context**（不动 `Input` 形状 ⇒ 幂等重放契约不受影响）。
- **放行链第 2 项（#504 第一条）已合并集成**：PR #513，合并提交 `df6b271a`。单一权威 ＝ 契约 `contracts/openapi.json` 的 `servers[0].url` ＝ `http://localhost:4010/api/v1/lingdoc`；HTTP 对偶侧 **15 处 `/api/v1/lingdoc` 字面量归零**（实测 15 → 0）；守护测试从「两套相反约定各自钉死」改为**只钉死单一口径** ＋ 两条新断言（含「同一 base-url 喂两侧、同一判定」的回归门）。`scripts/ci` 全套 420 项 OK、F01 runner 回归 130 项 OK、登记处 11 面；管理会话另起进程实跑 4 条 base-url/api-root 断言全过、整个 `test_lingdoc_beta_evidence.py` 无回归。
- **WeKnora 真实解析＋远程 embedding：已获用户正式授权，执行前检查已完成（实测，非引用旧报告）**
  - 材料复算 SHA-256 一致：`synth-chapter-a.md` ＝ `4b1572b57144…adf63dd`（4747 B）、`synth-chapter-b.md` ＝ `29e2d8f3c2ae…f14ef258`（4824 B）。
  - 分块策略：`config/config.yaml` → `chunk_size: 512`、`chunk_overlap: 50`、`split_markers: ["\n\n","\n","。"]`。
  - 费用依据（仓内已编码单价）：`acceptancebudget/ledger.go` → `QwenNanoCNYPerToken = 500` nano-CNY/token，模型 `qwen3.7-text-embedding` / `-flash`，`QwenBaseHostTail = .cn-beijing.maas.aliyuncs.com`、`QwenBasePath = /compatible-mode/v1`。
  - 密钥可见性：`DASHSCOPE_API_KEY`（len 115）、`DEEPSEEK_API_KEY`（len 35）存在；**全程只报存在性与长度，不打印、不落盘**。
  - ⚠️ **三个环境缺口（已交给执行会话处理）**：① WeKnora 主服务未在跑（8080/4010 closed，只有 postgres/redis/docreader 在跑）；② 向量库未在跑（qdrant/milvus 均无容器）⇒ 需独立 collection 名；③ `.env` 里**没有** `EMBEDDING_*` 配置 ⇒ 远程 embedding 模型需在 model 表注册。
  - 执行红线：每份材料**最多一次**真实处理流程；禁自动重试/并发重复提交/重复向量化/无边界轮询（状态轮询上限 12 次 × 20 秒）；失败即留脱敏证据并停下汇报；**独立数据库**（不碰 dev 库）；**不得发起任何 Qwen chat 调用**；不得用 Mock/预置状态/手工写库冒充真实验收。
  - **状态：NOT RUN（执行中）** ⇒ 结果到达后按 PASS / FAIL / BLOCKED_EXTERNAL / NOT RUN 分别登记，并写解除条件。**不得**把 WeKnora 单项通过等同于完整 F01 或 Beta 验收通过。
- **一次最小 Qwen chat 调用：授权已确认**（按原方案范围执行：走 LingDoc 真实生成路径、只跑 `startGeneration`、跑完立刻查账本确认新增一行 `source_kind=generation_run`）。**排在 #484 之后** —— 账本写入端已合入（`003efde8`），但 F01-07 的输入需要 #484 的材料接入能力。
- **#502（57-Spec 落盘副本）**：pf-6 已交重做版（逐字取 #482 当前正文），门禁已绿，待落基后合并。**验收冲突已裁定**：以「与发布副本逐字一致」为准，「不含会立刻过期的具体文件路径」**降为记录项、不作门禁**（根治在 #482 侧：下次更新把路径改成契约名/接口名）。
- **两处已纠正的自身判断**：① 「#504 的两条借用的记账缺失」是**误判**（截断输出所致）—— 核实后这两个文件本来就在 PF-10 块，PF-14 的 summary 已含该记账，登记处无重复声明；② 「#509 落基后 push 侧 job 全 skip」经查是**门禁的增量判定**（落基那次 push 的增量范围里没有 Go 文件），而 PR gate 按 `base...head` 判定 `run=True` 并给出 ruleset 要求的那个结论 ⇒ 不是逃逸。
- **门禁机制的一处查证（skip 不是逃逸）**：落基到最新 main 后，push 侧实质 job 全变 `skipped` —— 落基那次 push 的增量范围里只有 main 带进来的文档文件、**没有 Go 文件**，门禁的增量判定据此跳过；而 **PR gate 本体按 `base...head` 判定 `run=True`**（surface 正是本 PR 改的两个包），ruleset 要求的那一个结论由它给出。已用仓内决策模块本地复现该判定（`reason = contribution surface changed`、`run_code = True`、`run_targeted = True`、`job.app.verify: run = True`），并核实 `1bc43454` → `3b9bb70a` 的**唯一差异是 `docs/BETA_STATUS.md`（`.go` 差异 0）** ⇒ 被测代码树逐字节相同 ⇒ 上一轮的实测结论对当前 head 仍然成立。
- **#502 的验收冲突已裁定**：以「与发布副本 #482 **逐字一致**」为准；「不含会立刻过期的具体文件路径」**降为记录项、不作门禁** —— 仓内副本的第一职责是忠实反映 spec，副本与 spec 分叉会让「哪个是真的」不可判定。根治在 #482 侧（下次更新把具体路径改成契约名/接口名），已登记为残留项，**不阻塞首版**。
- **两处待补（已下达给 pf-10）**：① #513 的 PF-14 借用记账（`scripts/acceptance/beta_http_readback.py`、`scripts/ci/test_lingdoc_beta_evidence.py`，owner 仍是 PF-10，不声明进 paths）；② `internal/container/lingdoc.go` 的归属写准（owner 是 PF-7，#509 合入 main 后该路径已归还 ⇒ summary 里的「合入后归还」要改成事实状态）。⚠️ #503（PF-7）也改了该文件 ⇒ 落基会撞，冲突须解在机器可校验的一侧。
- **仍待授权（未执行）**：真实 Qwen 最小调用、WeKnora 远程 embedding、主仓库旧 main 处置、现场清理范围、票据降噪、真实验收机器归属。

## 11. 第四批收口（2026-10-23 18:40 后）

- **WeKnora 真实链路的完整结果与越界记录见 §10 的「WeKnora 真实解析 ＋ 远程 embedding：已完成」一段** —— 含两份材料的逐项实测（各 1 次上传、各 2 次 embedding 请求、分块 7/6、向量 1024 维、检索全中）、**12 次 provider 请求全部成功、5363 prompt tokens、0.000670375 CNY**、**单价口径更正（125 而非 500 nano-CNY/token）**、**4 次非预期 Qwen chat 调用的越界记录**（全部 400、零计费），以及「**Embedding 类型模型不得配为 `summary_model_id`**」的护栏建议。
- **F01-06 = PASS（仅此一项）**。**完整 F01 与 Beta 验收仍未通过** —— 生成/章节/Word 链路未覆盖。
- **#484 服务端能力**已交付（`internal/lingdoc/materiallingest/`）：19 个测试全过、`-race` 过、lint 0 issues、`go build ./...` 过；**待开 PR**（路径登记 `internal/lingdoc/materiallingest/` 由 PF-14 侧在登记处加）。

## 12. 第五批：资产链只读核查结论（含两处更正，2026-10-23 19:30 后）

### 12.1 两处更正（我先前的说法有错；⚠️ **本节的更正 1 已被 §13（2026-10-23 20:13 后）取代 —— 冲突处以 §13 为准**）

1. ⚠️ **本条已被 §13（2026-10-23 20:13 后）取代 —— 下面这段只对 19:30 那个时点成立，不可当作当前状态读。** §13.2（`:207-219`）记录 **7/7 节点 PASS、10/10 checks**，其中 **F01-06 `bindAsset` 真实发出 `POST /api/v1/lingdoc/projects/{projectId}/assets` ⇒ HTTP 201**、响应 `data.processing_state="ready"`；§13.3（`:225-240`）是请求与响应原文；§13.4（`:250-257`）的 before/after 表显示 `lingdoc_project_assets` 0→**1**、`lingdoc_asset_revisions` 0→**1**、`lingdoc_operations` 0→**4**。**原文保留**（留作当时判断的依据）：「**「F01-06 = PASS（仅此一项）」被高估了。** 已通过的只是 **WeKnora 侧的 5 条子断言**（材料处理完成 → `parse_status=completed` → 分块落库 → 远程向量落库 → 检索命中）。**契约节点 F01-06 本身从未执行** —— 它没有向 LingDoc 服务发过 `POST /api/v1/lingdoc/projects/{projectId}/assets`，也没有读过响应里的 `/data/processing_state`。**决定性反证**：隔离库 `weknora_iso` 里 `lingdoc_project_assets` / `lingdoc_asset_revisions` / `lingdoc_operations` / `lingdoc_projects` **四张表全是 0 行**。」
2. **上传被挡下的次数是 1 次，不是我先前说的「≥2 次」。** 逐次核对 `evidence/server.combined.redacted.log`：A-1（18:29:19）500 `unsupported file type`（**门在建 knowledge 行之前** ⇒ 0 次 provider 调用）、A-2（18:29:59）200、B-1（18:35:59）200 ⇒ **上传请求 3 次、真正进入处理 2 次、被门挡下 1 次**。

### 12.2 资产链的逐步调用关系（每跳都有代码证据）

- **F01-06 请求体**（契约 `steps[6]`）：`{"knowledge_id": "{{knowledge_id}}"}` ＋ `Idempotency-Key` 头 ＋ 路径参数 `projectId`；`expected_http: 201`；`checks: [{"path": "/data/processing_state", "equals": "ready"}]`。
  服务端 `internal/lingdoc/workspace/http.go:790-792` 接受的字段集**恰好是 `{knowledge_id}` 一个**，且 `decodeBody` 用 `DisallowUnknownFields()`（`http.go:290`）⇒ 多传字段即 400。**服务端与契约完全一致。**
- **`bindAsset` 是「创建 asset ＋ 建立项目↔知识绑定」**：`http.go:781` → `source_service.go:113` `BindAsset` → `source_service.go:123` 查知识 → `:141` `evidence.Bindings.BindIdempotent` → `internal/evidence/binding.go:270/300` `bindTx` ⇒ **INSERT** `lingdoc_project_assets` ＋ `lingdoc_asset_revisions` ＋ `lingdoc_operations`。**它要求 knowledge 事先存在**（否则 404，见 `assets_http_test.go:542`）。**没有第二个创建 asset 的端点。**
- **`processing_state` 读的是 WeKnora 自己的表**（不是 mock、不是 LingDoc 的状态表）：`evidence.ProcessingStateOf`（`internal/evidence/revision.go:83-98`，`completed → AssetStateReady`）← 入参来自 `source_service.go:144-147` ← `dbSourceCatalog.Knowledge`（`internal/lingdoc/workspace/adapters_gorm.go:40-54`，`SELECT * FROM knowledges`）。`evidence.KnowledgeSignalReader` 的**唯一生产实现**是 `adapters_gorm.go:72` `dbKnowledgeReader`（`:86-102` 读 `knowledges.parse_status`），装配点 `adapters_gorm.go:27-28`。**本仓 LingDoc 服务端与 WeKnora 是同一个二进制、同一个数据库**（`cmd/server` 打印 `DB_NAME=weknora_iso`）⇒ 读到的就是真实处理流水线写的状态。
- **knowledge ID → asset 的映射存在**：同一个 UUID 落成 `lingdoc_project_assets.knowledge_id` 与 `lingdoc_asset_revisions.weknora_knowledge_id`（`binding.go:237/254/282/287`）；asset ID 由服务端生成（`binding.go:234`）。反向解析 `evidence.Bindings.AssetForKnowledge`（`binding.go:202-215`）。
- **`internal/lingdoc/materiallingest/` 目前零生产调用点**（`git grep` 只命中包内与文档）⇒ 它补的是「服务端把本地材料文件变成 asset」这一段，**但 F01-06 不需要它**（F01-06 走的是「WeKnora 已处理完 → bindAsset 绑定」）。

### 12.3 真实处理是否具备安全执行条件

- **WeKnora 侧：已具备。** docreader/postgres/redis 在跑；隔离库 `weknora_iso` 迁移 0→132；向量库用 pgvector（`RETRIEVE_DRIVER=postgres`）⇒ **不需要 qdrant/milvus**；embedding 模型已注册且 `api_key` 已配置；acceptance 账本 `max_calls:0`、embedding 客户端 `maxRetries=0` ⇒ **无自动重试**；两份材料 `completed`/`enabled`、`embeddings` 13 行、`chunks` 13 行 ⇒ **不需要再上传、也不需要再向量化**。
- **LingDoc 侧：只缺「执行」。** 端点、字段集、状态取数链、`--knowledge-id` 注入机制（`run_f01.py:1336/1365`，守卫测试 9/9 PASS）、知识库读权限（owner 直通）**全部已具备**；缺的是：① 起服务（`DB_NAME=weknora_iso`，监听 `config/config.yaml:3` 的 8080）② 跑一次场景让 F01-02 建项目、F01-06 绑定。
- ⚠️ **实跑 F01 时必须用材料 B**（knowledge id `128b88fd-…`）：它所属的知识库 `summary_model_id` 为**空串** ⇒ 不派生摘要子任务 ⇒ **零 chat 风险**；材料 A 所属的知识库仍是「embedding 模型被配成 summary 模型」的危险配置。

### 12.4 额外模型调用风险

- **风险 1（已实际发生）**：`knowledge_post_process.go:208` `willSpawnSummary := len(textChunks)>0 && kb.SummaryModelID != ""` ＋ 当时的 `internal/application/service/model.go` `GetChatModel` **不校验 `model.Type`** ⇒ 根因是**两处**：① 知识库写入时未校验类型（**已由 PR #517 合入 `a5fb8841` 修掉**：按 `types.IsSummaryCapableModelType` 判定，建库与改库两条路径都拦）② **`GetChatModel` 的出口已由 PR #520 合入 fail-closed 守卫**（`internal/application/service/model.go:630-638`，按 `types.IsChatCapableModelType` 判定，放行 `{KnowledgeQA, VLLM}`）⇒ **残留的是各 `summary_model_id` 取值路径**：`knowledge_process.go:1677/1988/2264/2406`、`knowledge_auto_tag.go:214`（回落 `kb.SummaryModelID`）与 `wiki_config.synthesis_model_id` 等 ⇒「模型类型 × 用途」护栏票**已开（#533**，含 embedding 一侧的等价守卫）。
- **风险 2**：asynq `MaxRetry(3)` ⇒ 一次摘要失败放大成 **1+3=4 次** chat 请求（本次那 4 次正是如此）。acceptance 账本只覆盖 embedding，**不覆盖 chat 路径**；且 chat 路径要靠 acceptance profile 才有费用口径 —— profile 为空时 `internal/lingdoc/workspace/generation_acceptance_budget.go:91-96` 返回空预算 ⇒ 闸门整个不存在。⚠️ KB summary 那条路（`knowledge_post_process.go`）**本来就不经过 LingDoc 的 acceptance 预算** ⇒ 那 4 次越界调用在任何 profile 下都不会被账本拦下。
- **风险 3（当前未触发）**：F01-07 一旦执行会**真的**调 chat 模型，而隔离库模型行的 `api_key` 是用**已丢弃的一次性 `SYSTEM_AES_KEY`** 加密的 ⇒ 需要同一密钥或重新注册模型行。⚠️ **失败表象是 401，不是「凭据错」**：解密走宽松分支（`internal/types/model.go:347-365`）⇒ 密钥被**静默置空** ⇒ provider 收到空密钥；`CredentialDecryptError()` 截至 `origin/main=aa2edb12` **零生产消费**（PR #526 已在其分支补上 `GetChatModel` 的最小接线）⇒ 「没配 key」与「key 解不开」在证据里长得一样。⚠️ **重新注册模型行的安全前提**：`SYSTEM_AES_KEY` 未设或长度≠32 ⇒ `internal/types/model.go:302-318` 的加密分支整个跳过 ⇒ **明文密钥直接落进 `models.parameters`**（只有 `internal/runtime/startup.go:137-141` 一行警告）。护栏票 **#532**。

### 12.5 最小接线方案：F01-06 **不需要接线**（对比后不选新增端点）

| 方案 | 结论 |
|---|---|
| 新增「返回 WeKnora 处理状态」的端点 | **不需要，且有害** —— `processing_state` 已由既有 `bindAsset` 响应给出（`evidence/asset.go:17` ＋ `http.go:805`）；新增端点会重复信息、扩大契约面，还要改 `contracts/openapi.json`（L3，PF-11 持有） |
| 把状态读数放进现有 `listAssets` 响应 | **已存在** —— `http.go:768` → `source_service.go:90` → `RefreshAsset`（`binding.go:160`，按当前 `parse_status` 刷新已绑定 asset 的状态）；只是 F01-06 的断言读的是 **bind 响应**，`listAssets` 不参与 |
| 复用现有 `bindAsset` 的字段 | **已经是** —— 请求体只有 `knowledge_id`，响应体已有 `processing_state` |
| 接线 `materiallingest` | **可选**（它提供的是「服务端把本地文件变成知识」的能力，不改变 F01-06 的接口形状）；接线需：导出 `dbKnowledgeReader`（`adapters_gorm.go:72` 当前未导出、只内部接线）＋ 在 `internal/container/lingdoc.go`（**owner 是 PF-7** ⇒ 需排队）注入；**路径登记 `internal/lingdoc/materiallingest/` 由 PF-14 侧在登记处加**（L1） |

### 12.6 已合并的护栏与新增缺口

- **已合入 `a5fb8841`（PR #517）**：拒绝把非文本生成模型配成知识库 `summary_model_id`；判定按 `types.IsSummaryCapableModelType`（**模型类型**，不是名字）；建库（`knowledgebase.go`）与改库（`initialization.go` 的 `UpdateKBConfig`）两条路径都拦；正向用例证明 chat 模型未被误禁。
- **已合入 `cc40cb44`（PR #551，票 #533）**：「模型类型 × 用途」护栏 —— embedding 通道（`internal/application/service/model.go` 的 `GetEmbeddingModel`／`GetEmbeddingModelForTenant`）与连接自检都补上等价守卫；`types.IsEmbeddingCapableModelType` 已存在（`internal/types/model.go:87`）⇒ §13.7 第 3 条里「全仓不存在 `IsEmbeddingCapableModelType`」**已过时**。
- **已合入 `1df56366`（PR #554，票 #534）**：延迟重排路径的重试上限 —— `EnqueueGenerationAfter` 与正常路径共用**同一个**数字 `generation.GenerationTaskMaxRetry` = 3（`internal/lingdoc/workspace/generation.go:711` ＋ `internal/lingdoc/generation/tasks.go:39`）⇒ asynq 默认 25 的放大风险已闭合；守护 `internal/lingdoc/workspace/generation_retry_budget_test.go`。
- **已合入 `a622b8ac`（PR #553，票 #535 第一段）**：角色词表收敛 —— 一处定义、一处校验、审计列语义写清；**段二另开票**。
- **已合入 `dfa52944`（PR #526）＋ `232e0899`（PR #544）**：受控 Qwen profile（`lingdoc-qwen-token-budget-20261023-v1`，按 token 与调用次数设闸门）＋ 后续修复（钉死「无价格口径 ⇒ actual 恒 0」＋ 输入预检跟随 profile）。
- **已合入 `84f45806`（PR #548，票 #547）**：调用可追溯性 —— 候选行记下产出它的模型、provider 请求标识落进 LLM debug 日志 ⇒ §14.2 里「候选行 `model_id` 为空」这一观察在**本次调用之后**已闭合（该次调用的代码基线是 `dfa52944`，**不含** PR #548）。
- **第二轮已合入（2026-10-24）**：`06d37b69`（PR #574，票 **#501** 两步结构 `after_step` ＋ 同资源约束）｜`88845c5d`（PR #577，票 **#545** model-use 许可节点 `F01-06b…F01-06e`，F01 **24 → 28** 节点）｜`67c0c9de`（PR #580，票 **#546** 检索口径 ＋ 受控窗口按账本核验 embedding 行）｜`b63aa79f`（PR #583，**v2 受控 profile**：4 次 ＋ 累计 90,000，旧 profile 逐字不变）｜`4ba068d8` / `f7d974dd`（PR #562／#575，票 **#550** CI 排队机制 ＋ 判据脚本 `scripts/ci/lingdoc_gate_queue_state.py`）｜`8b380a01`（票 #542）｜`34d8a328`（票 #557）｜`596b4a99`（票 #558）｜`986f9143`（票 #564）｜`99c0417f`（票 #506，PF-7 交面）｜`de22b4a4`（票 #567）｜`7ba7d187`（票 #570）。
  ⚠️ **这些合入没有关闭任何 GAP**：#545 的许可节点已在契约里，但**真实后端上许可链仍断在 F01-06b**（§15.3 D2）；#546 的检索口径让 F01-07 按契约记 **NOT RUN**（§15.3 D3）；#501 只落地了形状，4xx 节点仍未挂进 F01 序列（`GAP-FAILURE-PATH-NODES` 未解除）。
- **新增缺口票（待开）**：① `GetChatModel` 及各 `summary_model_id` 取值路径的类型护栏 —— **出口部分已由 PR #520 落地**（`internal/application/service/model.go:630-638` fail-closed，按 `types.IsChatCapableModelType`），**残留是各取值路径** ② `wiki_config.synthesis_model_id` 等同形状兄弟字段 ③ `CopyKnowledgeBase`/`DuplicateKnowledgeBase` 绕过服务层会传播历史脏配置 ④ `internal/application/service` 从未在 CI 上以 `-race` 跑过（app.yml 是裸 `go test`，PR gate 的 `-race` 不含该包）⑤ 一张低优先级「测试可移植性」票（4 条 Windows-only 既有失败，已独立复核为环境性）。
- **已开票（本轮）**：⑧ **凭据解密失败显式化**（**#532**）：落库侧拒明文、`decryptModelAppSecret` 密文不外发、「解不开 / 没配 / 历来是明文」三态可区分。⑩ **switch 行的写入路径无生产调用点**（`RecordModelSwitch` 在 `origin/main` 上只有接口定义与实现，命中全在测试里）⇒ 放行链第 9 行「账本非 0 行**＋switch 行**」的后半截代码侧条件尚不具备（§1 第 9 行）。

### 12.7 6 个既有测试失败的独立复核结论

在 `origin/main` 干净检出（`2966045b`）上实跑 `go test -count=1 ./internal/application/service/...`：**恰好 6 个失败、同名、无第 7 个**；护栏分支跑同命令也是**恰好同样 6 个** ⇒ **diff 层 ＋ 行为层双重确认与护栏无关**。**归因被更正**：不是「Python 断言差异」，而是四条互不相同的平台机制（SQLite fixture 不关连接 ⇒ Windows `t.TempDir()` cleanup 必红；shebang 直执行 `.sh`；测试 fake 硬编码 `/bin/bash`；Windows 无 POSIX 权限位）。**这些失败不挡放行**：`internal/application/service` 只被 `app.yml`（ubuntu-latest）覆盖、**不被 `LingDoc PR gate` 覆盖**，且实证同一 commit 在 Linux CI 上 `Vet and test` = success。

## 13. F01-01～F01-06 隔离环境真实集成验证（2026-10-23 20:13 后）

### 13.1 执行环境（逐项显式设置并已核对）

| 项 | 实际值 |
|---|---|
| 代码 HEAD | `a5fb88410e5c8dce40b98c24bf53390dba52395f` |
| 工作目录 | `D:\wt\lingdoc-f01-preflight`（detached worktree，**未用主仓**；结束前 `git status --porcelain` 为空） |
| DB | `postgres` / `127.0.0.1:5432` / `postgres` / **`weknora_iso`**（dev 库 `WeKnora` 未写） |
| `RETRIEVE_DRIVER` / `AUTO_MIGRATE` / `AUTO_RECOVER_DIRTY` | `postgres` / **`false`** / `false` |
| Redis | `127.0.0.1:6379`，**`REDIS_DB=3`**，`REDIS_PREFIX=isof01:stream:`（该前缀只管 SSE，asynq 的键固定 `asynq:*`） |
| `STORAGE_TYPE` / `LOCAL_STORAGE_BASE_DIR` | `local` / `D:\wt\lingdoc-f01-data\files`（独立空目录） |
| `SERVER_PORT` / `GIN_MODE` | **8080** / `release` |
| 刻意**不设** | `LINGDOC_T10_ACCEPTANCE_PROFILE`、`DASHSCOPE_API_KEY`、`DEEPSEEK_API_KEY`、`BUILTIN_MODELS_CONFIG` |
| 凭据 | 隔离库 owner 账号（**走邀请注册业务路径**：`invite-links` → `register-by-invite`，零直接写库）；token 只存仓外受保护文件，**未打印、未提交、未进日志** |

**`builtin_models.yaml` 未被加载，两道独立保险**：① worktree 的 `config/` 只有 `.example`（主仓有 1533 B 真配置）② `container.go:955` 的 `LoadBuiltinModelsConfig` **只存在于 `AUTO_MIGRATE != "false"` 分支内**，本轮该分支不执行。启动日志无模型 UPSERT 记录，`models` 三行 `params_md5` 逐行不变、`api_key` 仍为 `enc:v1:` 密文。

### 13.2 逐节点结果：**7/7 PASS，10/10 checks passed**

`report.runner_status=completed`、`provider_semantics_status=verified`、`failures=<none>`。

| 节点 | HTTP | 关键响应 | checks |
|---|---|---|---|
| F01-01 `getTemplate` | 200 | `id=template-demo`、`ruleset_hash=ad814c1f…` | `/data/ruleset_hash` length 64 → passed |
| F01-02 `createProject` | 201 | **project id `770576b5-c7e4-45f5-9175-daefc8766a96`**、`status=draft`、`project_version=1` | `/data/template_id` == `template-demo` → passed |
| F01-03 `saveSpec` | 200 | `spec_revision=1`、`project_version=2` | spec 字段原样落库 → passed |
| F01-03b `recordActivationRead` | 201 | receipt `5fc5e474-…`、`actor_id=3a3c9b30…` | actor_id / project_version / spec_revision 三条 → 全 passed |
| F01-04 `activateProject` | 200 | `status=active`、`project_version=3`、`current_context_revision=1` | `/data/status` == `active` → passed |
| F01-05 `listChapters` | 200 | 两章 `cb53bea0…` / `ecd62e4d…` | 两条 `readback_equals`（`readback_after=F01-04`）→ 全 passed |
| **F01-06 `bindAsset`** | **201** | 见 §13.3 | **`/data/processing_state` == `ready` → passed** |

**裁剪副本先验证再运行**：`contracts/workflow.json` **只读**，24 步裁成 7 步（仓内被跟踪文件零改动）⇒ 执行器自身三道闸门先过（`_validate_readbacks` / `_check_base_url_scope` / `_preflight_required_variables`）。**依赖形状**：F01-06 只依赖 `project_id`（F01-02 capture）＋ `knowledge_id`（环境提供），与 F01-07+ 的 `question_id`/`question_version`/`method_id`/`method_version` **零交集** ⇒ 截断自洽；**F01-07 及之后未执行**。

### 13.3 F01-06 的请求 / 响应 / checks / 绑定证据

真实请求（服务端访问日志原文）：
```
POST /api/v1/lingdoc/projects/770576b5-c7e4-45f5-9175-daefc8766a96/assets
Idempotency-Key: f01-bind-0001
request_body = {"knowledge_id": "128b88fd-8389-4800-9851-92e9bf6fe19c"}
```
⇒ 请求体**只有** `knowledge_id` 一个字段；路径参数是 F01-02 capture 的 project id；幂等键被服务端记入 `lingdoc_operations.key`。

响应（HTTP 201）：
```json
{"data":{"id":"1ceba747-79e5-4b6c-a78a-252d1324085e",
         "project_id":"770576b5-c7e4-45f5-9175-daefc8766a96",
         "knowledge_id":"128b88fd-8389-4800-9851-92e9bf6fe19c",
         "title":"f0106-synth-chapter-b-29e2d8f3c2ae…md",
         "asset_revision":1,"processing_state":"ready"},
 "meta":{"refresh_required":false,"replayed":false}}
```
**checks 由执行器判定（不是事后查库）**：`path=/data/processing_state`、`operator=equals`、`expected="ready"`、`actual="ready"`、`found=true` ⇒ **passed**。

**库侧独立复核（与节点断言并存）**：`lingdoc_project_assets` 1 行（`id=1ceba747…`、`project_id=770576b5…`、`knowledge_id=128b88fd…`、`knowledge_base_id=ab69e3a1…`＝`lingdoc-beta-f01-06-b`、`processing_state=ready`、`asset_revision=1`、`created_by=3a3c9b30…`）；`lingdoc_asset_revisions` 1 行（`id=505379ca…`、`weknora_knowledge_id=128b88fd…`、`status=active`）。

### 13.4 执行前后的数据库差异

| 表 | before | after | 判定 |
|---|---|---|---|
| `lingdoc_projects` | 0 | **1** | 本次产生（`770576b5…`，status `active`，project_version 3，spec_revision 1） |
| `lingdoc_project_assets` | 0 | **1** | 本次产生（见 §13.3） |
| `lingdoc_asset_revisions` | 0 | **1** | 本次产生（见 §13.3） |
| `lingdoc_operations` | 0 | **4** | createProject 201 / saveSpec 200 / activateProject 200 / bindAsset 201，幂等键齐全 |
| `knowledges` | 2 | 2 | **未变**（`updated_at` 仍是 10:36:01.259933+00 与 10:31:52.202568+00；`parse_status`/`summary_status` 原样） |
| `embeddings` | 13 | 13 | **未变** |
| `chunks` | 13 | 13 | **未变** |
| `models` | 3 | 3 | **未变**（三行 `id/name/type/is_default/managed_by` 与 `params_md5` 逐行相同，`api_key` 仍为 `enc:v1:` 密文） |

⇒ **WeKnora 材料没有被重新处理**（knowleddings/embeddings/chunks 三表未变）**；模型配置没有被覆盖**。

### 13.5 本轮新增 chat / embedding 请求数：**0**（四条独立依据）

1. 服务端应用日志计数：`chat/completions` = **0**、`/embeddings` = **0**。
2. `llm_debug` 目录：本轮生成文件数 **0**（历史 12 个文件原样未动）。
3. acceptance 账本 `accept-iso.json.calls.json`：`requests=12`、`actual_nano_cny=670375`、`reserved_nano_cny=3042250`、`blocked=false`，**mtime 仍是 18:38:08**（早于本轮）⇒ 账本零写入。
4. Redis DB 3 事后复查：各队列 pending/active/scheduled/retry 仍全 **0**，归档死信仍是原来那 1 条。

**凭据泄漏扫描**：用 9 个真实密钥值（owner token、DB/Redis 密码、JWT_SECRET、两个厂商 key、3 条 api_key 密文）在 36 份证据文件里逐个搜索 ⇒ **0 命中**。

> ⚠️ **未确证**：本节是 **2026-10-23 20:13 那一次执行**的记录，**不是当前 head 的证据**（本轮未重跑 F01、未发起任何 provider 调用）⇒ 结论**对那一次运行**仍然有效，但**不可外推**到 `origin/main=aa2edb12` 之后的任何一次运行；要确证当前状态需**重跑一轮**并留下新的运行标识与证据目录。

### 13.6 偏离预期 / 如实登记的瑕疵

1. **stdout 日志被覆盖（操作失误）**：契约跑完后为「模型凭据解密探针」与「只读复读」又起了两次服务，复用了同一 stdout 路径 ⇒ 契约轮的 stdout 副本被覆盖。**独有信息未丢**：服务端 `LOG_PATH` 是追加写的，契约轮的完整访问日志（启动日志 ＋ 8 行含 request/response body 的 HTTP 交换行）完整保留，已切出为 `f01-contract-run-access-log.txt`；两个 GET 节点另做了事后只读复读（与执行时逐字段一致）。
2. **构建产物落在仓外**（`D:\wt\lingdoc-f01-data\server-preflight.exe`），以保证 worktree 零污染。
3. **并行的凭据会话服务仍在跑**（PID 73708，端口 18080，同一 `weknora_iso` ＋ Redis DB 3，且是活着的 asynq server）。它 housekeeping 关闭、各队列全空 ⇒ 无写入能力；20:05:56 它对本次 project 做了一次 **只读** `authorization.read`。**未去停它**（不是本会话的资源）；事后复核 `after == after2 == after3`（SHA-256 相同）⇒ 无任何改动；收尾时该进程已自行退出。
4. **额外一次非契约请求**：就绪探针 `GET /api/v1/auth/config`（200）。契约窗口内服务只对外提供了 8 个请求（1 探针 ＋ 7 节点）。
5. **两处事实修正（对本文件先前记载的更正）**：① `GetChatModel` 在非测试代码里的调用点是 **3 处**不是 2 处（`generation.go:442`、`agent_model.go:96`、`selected_rewrite_model.go:66`），第三处属改写域，同样不在 F01-01～06 路径上；② `LoadBuiltinModelsConfig` 位于 `AUTO_MIGRATE != "false"` 分支内 ⇒ 「不加载 builtin_models.yaml」有一道比「文件不存在」更强的保险。

### 13.7 F01-07 仍然存在的问题（只陈述，未做任何修改）

> ⚠️ **本节已被 §14 与 §15（2026-10-24）部分取代 —— 冲突处以 §15 为准**：下面第 1 条里「受控 Qwen 路径由 PR #526 交付 —— **未合并**」**已过时**（#526 已合入 `dfa52944`、后续修复 #544 `232e0899`、v2 profile #583 `b63aa79f`）；第 2 条里「隔离库 `models.api_key` 的真实形态**需重新实测**」**已有实测**（§15.1 起的 #485 实跑：CHECK3 `aes_key_bytes=32`、`stored_is_enc_v1=true`、`decrypt_ok=true`、`plaintext_len=115`、与注入的 `DASHSCOPE_API_KEY` 一致 ⇒ **凭据这半不再是缺口**）；第 3 条里「全仓不存在 `IsEmbeddingCapableModelType`」**已过时**（#533 已由 PR #551 合入 `cc40cb44`）；第 4 条里「`EnqueueGenerationAfter` 入队**未声明** `MaxRetry`」**已过时**（#534 已由 PR #554 合入 `1df56366`）。本节其余内容（第一次 POST 的 422 根因等）仍然成立。

1. **模型配置**：`models` 3 行里唯一能服务 chat 的是 `lingdoc-chat-deepseek`（KnowledgeQA、is_default=true、managed_by=yaml）；两个 Qwen 行（`qwen3.7-text-embedding-flash`、`text-embedding-v4`）都是 **`type=Embedding`** ⇒ **隔离库里没有可用的 Qwen chat 行**。⚠️ **「不设 `LINGDOC_T10_ACCEPTANCE_PROFILE`」不是零成本的替代方案**：`internal/lingdoc/workspace/generation_acceptance_budget.go:91-96` 在 profile 为空时直接返回空预算 ⇒ **没有调用次数上限、没有 CNY 上限、没有预留/实付记账** ⇒ 「一次真实调用在预算内」失去机器强制（**受控 Qwen 路径由 PR #526 交付 —— 未合并**：profile `lingdoc-qwen-token-budget-20261023-v1`，按 token 与调用次数设闸门，不以 CNY 费用为前置）。⚠️ **模型登记文件不在仓内**：仓内只有 `config/builtin_models.yaml.example`（其 `builtin_models: []`）⇒ Qwen chat 行要么写进本地 yaml 由启动装载，要么直接注册进 `models` 表。
2. **凭据解密**：三行 `api_key` 全为 `enc:v1:` 密文；用隔离环境的 `SYSTEM_AES_KEY` 做独立 AES-256-GCM 探针实测 **三行全部 auth tag mismatch**；主仓 `.env` 的 `SYSTEM_AES_KEY`/`SYSTEM_SIGNING_KEY` 长度为 0，也不是加密源。解密走宽松分支（**`internal/types/model.go:347-365`**）——失败时**静默置空**并打一行日志（`model.go:362-364`，原文是 `the credential is configured but unusable — this is NOT an unconfigured credential`），问题会推迟到真正发起模型调用时才暴露。另：`GET /api/v1/models` 的响应根本不返回 `parameters`/`api_key` ⇒ 凭据状态无法从 API 侧观测。⚠️ **未确证**：PR #526 的只读探针记「本机 `.env` 的 `SYSTEM_AES_KEY` 长度 0 ⇒ 写入端不加密 ⇒ 实测到的两行是**明文**（len=198 / len=35，**不是** `enc:v1:` 形态）」，与本条的「三行全为 `enc:v1:` 密文」**不一致** ⇒ 隔离库 `models.api_key` 的真实形态**需重新实测**（本轮未连库、未实测）。护栏票 **#532**（落库侧拒明文、`decryptModelAppSecret` 密文不外发、「解不开 / 没配 / 历来是明文」三态可区分）。
3. **模型类型校验**：守卫 `types.IsSummaryCapableModelType`（按 Type 判定）**已存在且已接线**（`knowledgebase.go:238`、`initialization.go:283`，PR #517 → `a5fb8841`）；`GetChatModel` 的**出口**也已 fail-closed（PR #520，`internal/application/service/model.go:630-638`，按 `types.IsChatCapableModelType` 判定，放行 `{KnowledgeQA, VLLM}`）。**残留的缺口换到了 embedding 一侧** —— 生成路径 `workspace/generation.go:442` 调的 `GetChatModel` 已会拒非文本生成模型，但 `internal/application/service/model.go:489`（`GetEmbeddingModel`）与 `:519`（`GetEmbeddingModelForTenant`）**不校验 `model.Type`**，`internal/models/embedding/embedder.go:66`（`ConfigFromModel`）/`:89`（`NewEmbedder`）也不看 Type，**全仓不存在 `IsEmbeddingCapableModelType`** ⇒ 把 chat 类型模型绑成 embedding 模型不会被拒 ⇒ 与 chat 侧**仍不对称**。护栏票 **#533**（同一张票还含连接自检绕过 chat 守卫那一段）。
4. **重试上限（正常路径已修，延迟重排路径仍有缺口）**：KB summary 任务 `asynq.MaxRetry(3)`；LingDoc 生成任务的**正常入队路径**已显式声明上限 —— `internal/lingdoc/generation/tasks.go:33` `generationTaskMaxRetry = 3`、`:49` `asynq.MaxRetry(generationTaskMaxRetry)`（PR #521），且 `taskFailureRetryable`（`:79-99`）按 `clienterror.Form.Retryable` 与上游状态码区分、`:144` 对永久性失败返 `asynq.SkipRetry` ⇒ **永久性 4xx 只试一次**（Redis 那条归档死信的上游 400 参数错误 `Field required: input.contents` 现在会直接归档，不再被一路重试到上限才进死信）。**残留**：`internal/lingdoc/workspace/generation.go` 的 `EnqueueGenerationAfter`（延迟重排路径，入队时**未声明 `MaxRetry`** ⇒ asynq 默认 25）⇒ 该路径上一次**可重试**失败（429/408/5xx/不带状态码的未知错误）会放大成**最多 25 次真实 provider 调用**。护栏票 **#534**。⚠️ `generation.go` 的行号会随 PR #526 合入位移（它在同文件改了 `generation.go:405` 一行、净增 7 行）⇒ **以落地时实际行号为准**。

### 13.8 边界声明

**F01-01～F01-06 全部 PASS 只代表这 7 个节点在本隔离环境通过；不代表完整 F01 通过，也不代表 Beta 验收通过。** F01-07 及之后一律未执行。#485 仍按**完整 24 节点契约**在 SQLite 上逐节点验收，前 7 节点的结果**不能冒充**完整 F01。

**证据**（仓外，含 MANIFEST 的 SHA-256 清单）：`D:\wt\lingdoc-f01-data\` —— `f01-contract-run-access-log.txt`、`f01-evidence.txt`、`f01-created-rows.txt`、`f01-report.json`、`before/after/after2/after3.txt`、`f01-01-readback.json`、`f01-05-readback.json`、`f01-07-open-issues.txt`、`EVIDENCE-NOTES.txt`、`MANIFEST.txt`。

## 14. 第六批：一次受控真实 Qwen chat 调用已取证（2026-10-24）

> **本节的事实来源**：`D:\wt\lingdoc-qwen-call\CALL-REPORT.md`（12 节 ＋ 28 份证据；证据目录 `D:\wt\lingdoc-qwen-call\evidence\`、账本 `D:\wt\lingdoc-qwen-call\ledger\qwen-round.json` ＋ 派生的 `qwen-round.json.calls.json`）。**本轮只做文档同步**：provider 请求数 = **0**（未跑 F01、未连真实后端、未发起任何真实模型调用）。

### 14.1 本轮基线与已合入的实现

**同步所依据的 head**：`origin/main` = `a98138af`（PR #556 合并提交）。

| PR | 合并提交 | 主题 |
|---|---|---|
| #509 | `003efde8` | #486 账本写入端（生成路径写真实账本行） |
| #513 | `df6b271a` | #504 第一条（对偶两侧 base-url 口径） |
| #498 | `bbd461ac` | #494 资料标识参数化 ＋ #504 第二条（解除条件改稳定标识） |
| #530 | `aa2edb12` | #499 断言能力补齐 ＋ 穷举校验 |
| #536 | `2cdd6656` | #499 求值分派不再落空 |
| #515 / #528 | `2966045b` / `ac5c869d` | #484 服务端能力 ＋ 离线可测编排模块 |
| **#526** | **`dfa52944`** | **受控 Qwen 执行路径（按 token 与调用次数设闸门）** |
| #544 | `232e0899` | #526 后续：钉死「无价格口径 ⇒ actual 恒 0」＋ 输入预检跟随 profile |
| #547 → PR #548 | `84f45806` | 调用可追溯性（候选行记下产出它的模型、provider 请求标识落进 debug 日志） |
| #543 → PR #552 | `af92acff` | 受控模式的错误文案按生效 profile 取模型名与上限 |
| #533 → PR #551 | `cc40cb44` | 模型类型 × 用途护栏（embedding 通道 ＋ 连接自检） |
| #535 第一段 → PR #553 | `a622b8ac` | 角色词表收敛（段二另开票） |
| #534 → PR #554 | `1df56366` | 延迟重排路径的重试上限 |

### 14.2 一次受控真实 Qwen chat 调用的取证事实

| 项 | 值 | 出处（CALL-REPORT） |
|---|---|---|
| 结论 | **已执行**：恰好 **1 次**受控的真实 Qwen chat 调用，**成功** | 表头 ＋ §1 |
| provider 请求总数 | **1**（服务日志 ＋ JSON 本地账 ＋ 派生总量账 ＋ DB 行，三方一致） | §1.1 / §1.6 |
| input / output / total tokens | **2051 / 519 / 2570** | §1.3 |
| usage 是否可核实 | **可核实**（`usage_status=reported`；`finish_reason=stop`） | §1.3 |
| 本地入口请求 ID | `X-Request-ID=e6b5a2b9-799f-4114-98d3-26ef351eb99e` | §1.1（`evidence/23-generation-post2.txt`） |
| generation run id（＝ DB 账本 `source_id`） | `427e9803-d166-427e-8078-921a30d6dd77` | §1.1 |
| **账本记录 ID** | DB 行 `3286e6a0-cd2a-4a07-9609-0f678ab72236`；JSON 本地账与派生总量账**没有 id 字段**，只有 `requests[0].number = 1` | §1.5 ＋ `ledger/qwen-round.json` |
| `model_id` | `lingdoc-chat-qwen3.8-flash-iso`（隔离库 `models` 行：`name=qwen3.8-flash`、`type=KnowledgeQA`、`source=remote`、`status=active`、`parameters.provider` 字面 `aliyun`） | §1.2 |
| `provider` | `aliyun` | §1.2 / §1.5 |
| `run_mode` | `real` | §1.5 |
| 是否走 generation run 路径 | **是**：DB 行 `kind=call` / `source_kind=generation_run` / `source_id` ＝ run id | §1.5 |
| 出网端点 | host `llm-k57eqvdqacn4nov3.cn-beijing.maas.aliyuncs.com` ＋ path `/compatible-mode/v1/chat/completions`（与获准白名单**逐字节相同**） | §1.2 |
| provider 侧请求 ID | **未核实**（百炼控制台本机不可访问；服务端未把 provider 响应头落进日志） | §1.1 |
| 费用字段 | **未核算 / 不适用**（JSON 账本 `cost_basis=not_applicable`）⇒ **不得**把 0 读成「免费」 | §6 |
| 派生候选稿 | `lingdoc_candidates.id = f38f0e74-fce8-4b44-9b32-8c809daf2076`（正文 479 字符、引用 3 个 source id） | §1.5 |
| 第一次 POST（对照） | **422** `asset_not_authorized` ⇒ **provider 请求数 0**（在授权判定层被拒，未出网） | §2 / §3 |
| 该次调用的代码基线 | `dfa52944`（PR #526 合并提交）⇒ **不含** PR #548 等后续修复 | §10（版本核对） |

> ⚠️ **本节记录的是 2026-10-23 的 v1 窗口那 1 次调用（历史行）**。2026-10-24 的 #485 真实后端实跑**没有**发起任何 provider 请求（**0 次**、v2 的 4 次额度一次未用、两本账本文件都不存在）⇒ DB 账本 `lingdoc_model_call_ledger` **总行数仍 1**、**本轮新增 0 行**（§15.4）。⇒ 本节的数字**不可**被读成「本轮又调用了」。

### 14.3 边界声明（不可省略）

**一次受控真实调用成功 ≠ F01-07 验收通过 ≠ Beta 放行通过。** 本次只跑了 `startGeneration` 及其后端模型调用；**F01-08～F01-23（取候选／取来源／采纳／存章／确认／交付／导出）未执行**；24 节点契约验收、SQLite 验收、人工验收的独立门槛**完全不受影响**（CALL-REPORT §7 / §12）。⇒ 本节只登记「一次受控调用的取证事实」，**不改任何放行结论**。

### 14.4 环境缺口：`GAP-MODEL-CREDENTIAL` **未解除**

- **缺口登记**（`docs/08-本轮实施方案/acceptance/gaps.json`，本轮**未改**该文件）：`GAP-MODEL-CREDENTIAL` → 票 **#480**、条目「缺口 2」、解除条件「给真实模型凭据后重跑本脚本」。
- **为什么一次真实调用 ≠ 缺口解除**：那次调用走的是**受控 profile 的隔离路径**（隔离库 `weknora_iso` ＋ 隔离 Redis DB 3 ＋ 临时把 KB 的 `vector_enabled` 置 false ＋ 临时插入 1 行 model-use 许可记录；**两处临时数据均已恢复并核验**）⇒ **不等于**「F01 主流程已能在受控 profile 下跑完 24 节点」，也**不等于**该缺口登记的解除条件（「给真实模型凭据后**重跑本脚本**」＝ `scripts/acceptance/beta_evidence.py`）被满足 —— 本轮**没有**以该脚本重跑。
- ⇒ **`GAP-MODEL-CREDENTIAL` 保持未解除**；放行链第 1 行新记的「真实验收 PASS」只覆盖**账本写入端这一条口径**（§1 第 1 行）。
- **第二轮更新（2026-10-24）**：该缺口登记的解除条件是「给真实模型凭据后**重跑本脚本**」⇒ 现在**凭据这半已经具备**（#485 实跑 CHECK3：`aes_key_bytes=32`、`stored_is_enc_v1=true`、`decrypt_ok=true`、`plaintext_len=115`、与注入的 `DASHSCOPE_API_KEY` 一致；端点与白名单 sha256 逐字节相同 ⇒ §15.1 的证据目录 `gaps.md` 明确写「真实模型凭据已具备 ⇒ 不再是缺口」），**但** `scripts/acceptance/beta_evidence.py` **本轮未重跑** ⇒ 登记的解除条件**仍未被满足** ⇒ **标识保持未解除**（`gaps.json` 本轮**未改**，仍绑定票 **#480**）。⚠️ 另注：本轮真实 provider 请求为 **0**，所以「凭据可用」只证明**能力**存在，**不证明**任何一次真实调用已由本轮发起。

### 14.5 本轮仍未解除 / 未运行的事实（NOT RUN 清单）

> ⚠️ **本表已被 §15.6 取代（2026-10-24 第二轮）** —— 冲突处以 §15.6 为准。具体变化：人工前置 1 的承接从「#545（配套 #501）」变为「#545 ＋ **#484** ＋ **#546**」（#501 已合入）；人工前置 2 的隔离 PG 这半**已具备**（本轮已在隔离 PostgreSQL 上真跑 F01），仍缺「重启读回」；**新增** #485 主流程本身的 1 处 BLOCKED ＋ 20 个 NOT RUN，以及 D1…D6 六条缺陷。下表按原文保留，供对照。

| 项 | 判定状态 | 承接 |
|---|---|---|
| #485 SQLite 跑完 24 节点 F01 | `NOT STARTED` | 等 #501 的两步契约形状 ＋ #545 的 model-use 许可节点 |
| #486 真实模型选段改写（**switch 行**那半截） | `NOT RUN`（代码侧条件尚不具备：`RecordModelSwitch` 无生产调用点） | 待开票 |
| #489 第二人独立复现（记录项） | `NOT RUN`（**未完成项**，**不是**首版门槛） | 解除条件 ＋ 跟进票 #489 |
| #490 放行结论表 | `NOT STARTED` | 收口 |
| 人工前置 1（Qwen 真实模型调用，**在 F01 主流程里**） | `DEFERRED_MANUAL` | 解除条件 ＋ 跟进票 #545（配套 #501） |
| 人工前置 2（隔离 PostgreSQL ＋ 重启读回） | `NOT RUN`（那次受控调用用过隔离库 `weknora_iso`，但**验收产物未产出**：未重跑 `beta_evidence.py --db-driver postgres`，未生成 `restart-readback.json`） | 缺口 `GAP-ISOLATED-PG`（票 #480） |
| 人工前置 4（Word 人工检查） | `DEFERRED_MANUAL` | 解除条件 ＋ 跟进票 #480（缺口 `GAP-WORD-CHECK`） |
| 人工前置 6（第二人独立复现） | `NOT RUN`（记录项，**未完成项**） | 解除条件 ＋ 跟进票 #489 |
| #527（DB 账本缺 `cost_basis` 列） | `NOT RUN`（票 OPEN） | 费用列**不得**单独引用 |

### 14.6 本轮未做的事（防止下一个人误读）

- **provider 请求数 = 0**：本轮只做文档同步 —— 未跑 F01、未连真实后端、未发起任何真实模型调用。
- **未改**：`docs/08-本轮实施方案/acceptance/gaps.json`（#501 实施线在动）、`scripts/lingdoc_parallel_surfaces.py` 与 `docs/08-本轮实施方案/G1-G7-Spec-完成路径图.md`（登记处 PR #556 在推）、`scripts/acceptance/beta_evidence.py`、`docs/08-本轮实施方案/contracts/**`、`scripts/lingdoc_mock/**`。
- **未**删除或清理任何 worktree、分支与他人未提交改动。
- 本文件的改动**只有一个文件**：`docs/BETA_STATUS.md`。

## 15. 第七批：#485 在真实后端上跑 F01 全流程（2026-10-24）—— **第二轮（Track-A／Track-B）**

> ⚠️ **本节是「第二轮」的历史记录，当前值以 §16 为准**（Track-C 把 #485 主流程从「7 PASS／1 BLOCKED／20 NOT RUN」推进到「**11 PASS／17 NOT RUN**」；§15.2／§15.3／§15.6 的结论**不得**再被读成当前状态）。本节原样保留，供对照。
> **本节的事实来源**：`D:\wt\lingdoc-485-evidence\` —— `README.md`（证据目录说明）、`node-verdicts.md`（**逐节点验收结论**）、`defects.md`（缺陷清单）、`gaps.md`（剩余 GAP ＋ 解除条件 ＋ 票号）、`ledger-status.md`（账本对账）、`runtime-manifest.json`、`track-a/`（Run A 执行器报告）、`track-b/`（Run B 变体）。代码 head ＝ `b63aa79f`（`origin/main`），隔离库 `weknora_iso` ＋ 隔离 Redis DB 3。
> ⚠️ **本轮 provider 请求数 = 0**（详见 §15.4）：不是省着用，是**发不出去**。

### 15.1 本轮基线与已合入的实现

**同步所依据的 head**：`origin/main` = `b63aa79f`。

| PR | 合并提交 | 主题 |
|---|---|---|
| #574 | `06d37b69` | **#501** 两步结构：契约步骤字段 `after_step` ＋ 同资源约束；执行器语义「前一步不成立 ⇒ 后继记 `BLOCKED`、不求值断言」；落地用例 F24 |
| #577 | `88845c5d` | **#545** model-use 许可节点：`F01-06b…F01-06e` 四个节点走生产链 `policy/proposals → assess → apply`；F01 节点数 **24 → 28** |
| #580 | `67c0c9de` | **#546** 检索口径：步骤字段 `retrieval`（`keyword_only`／`vector_required`）＋ 契约 `retrieval_modes` 段；受控窗口按**验收预算账本 ＋ 模型表类型**核验 embedding 行 |
| #582 | `e232bf0d` | 登记处 F01 节点数注记（steps 24 → 28、checks 50 → 58，以 main 实测为准） |
| #584 | `6b382132` | 登记处补登记 #583 profile v2 的 4 条路径（**PF-14 路径集 53 → 57**；PF-14 自 2026-10-23 认领时的 19 条起，多轮补登记到 57 条） |
| #583 | `b63aa79f` | **v2 受控 profile** `lingdoc-qwen-token-budget-20261024-v2`：4 次调用 ＋ 累计 90,000 token 预留闸门；旧 profile 行为逐字不变 |
| #562 ＋ #575 | `4ba068d8` / `f7d974dd` | **#550** CI 排队机制：`cancel-in-progress: false` ＋ 判据脚本 `scripts/ci/lingdoc_gate_queue_state.py`；第三形态（聚合 check-run 还不存在但 workflow run 仍在排队）判为 `queued` |
| #563 / #565 / #566 / #569 | `8b380a01` / `34d8a328` / `596b4a99` / `986f9143` | **#542**（受控 profile 下的输出上限与事后限值检查）／**#557**（审计列 `actor_role` 两处读侧兜底）／**#558**（rulesetstore 导出写入口补角色词表校验）／**#564**（`000001_agent.down.sql` 改 PostgreSQL 的 `UPDATE ... FROM`） |
| #571 / #572 / #573 | `99c0417f` / `de22b4a4` / `7ba7d187` | **#506**（PF-7 交面，登记处 status 改 done）／**#567**（generation 事后限值检查按 profile 取值）／**#570**（`PermissionEvidence.vue` 接入共享解析器） |
| #555 | `cbd449c1` | docs(#139)：登记 F01-07 已知缺口（#545）＋ 对齐 AC-04-05 与 PG 四行的判定口径 |

### 15.2 逐节点结论（28 个节点，真实后端）

**契约实测规模**：`steps = 28`（原 24 ＋ #545 的 4 个 model-use 许可节点 `F01-06b`～`F01-06e`）、**`checks = 58`**（原 50）—— 由 `docs/08-本轮实施方案/contracts/workflow.json` 在 `b63aa79f` 上直接量出。

| 节点 | 期望 | 实测 | 验收判定 |
|---|---|---|---|
| F01-01 `getTemplate` | 200 ＋ 64 位规则集哈希 | 200，1/1 checks 通过 | **PASS（真实执行）** |
| F01-02 `createProject` | 201 ＋ 绑定模板 | 201，1/1 通过 | **PASS（真实执行）** |
| F01-03 `saveSpec` | 200 ＋ 规格落库 | 200，1/1 通过 | **PASS（真实执行）** |
| F01-03b `recordActivationRead` | 201 ＋ actor/project_version/spec_revision | 201，3/3 通过 | **PASS（真实执行）** |
| F01-04 `activateProject` | 200 ＋ `status=active` | 200，1/1 通过 | **PASS（真实执行）** |
| F01-05 `listChapters` | 200 ＋ 两章骨架读回 | 200，2/2 通过 | **PASS（真实执行）** |
| F01-06 `bindAsset` | 201 ＋ `processing_state=ready` | 201，2/2 通过 | **PASS（真实执行）** |
| **F01-06b** `createChangeDraftPostPolicyProposal` | 201 ＋ `/data/status=draft` | **409 `stale_input`**（0 条 check 求值） | **BLOCKED（依赖缺失）／FAIL（契约声明 201、实得 409）—— 两种归因都成立，不替读者选** |
| F01-06c `assessChangeSet` | 200 ＋ 评估结论 | 未执行 | **NOT RUN**（上游 F01-06b 未成立 ⇒ 断言不得求值） |
| F01-06d `applyChangeSet` | 200 | 未执行 | **NOT RUN**（同上） |
| F01-06e `getProjectAssetPolicy` | 200 ＋ 许可读回 | 未执行 | **NOT RUN**（同上；旁证：独立探针 200，读回**默认**策略 `model_use_allowed=false`） |
| **F01-07** `startGeneration` | 202 ＋ 章节一致 | Run A：未执行；**Run B（24 节点变体）：422 `asset_not_authorized`** | **NOT RUN**（三重原因，见 §15.3 的 D2／D3） |
| F01-08…F01-23（16 个节点） | 各节点期望 | 未执行 | **NOT RUN**（上游未成立；其中 F01-09／F01-11／F01-12／F01-16／F01-17 另依赖 D1 的来源复核） |

**汇总**：**PASS（真实执行）7**（F01-01、F01-02、F01-03、F01-03b、F01-04、F01-05、F01-06；合计 **11 条 checks 全过**）／**BLOCKED 1**（F01-06b）／**NOT RUN 20**（F01-06c…F01-06e、F01-07…F01-23）／**缺结论 0**（28/28 都有结论）。
⇒ **协议 §6「主流程不能含 FAIL/BLOCKED/NOT RUN」⇒ 本轮 Beta 首版主流程不成立**（1 处 BLOCKED ＋ 20 个 NOT RUN）。

### 15.3 阻塞主链路的缺陷（按是否真阻塞 Beta 排序）

| # | 缺陷 | 机器事实 | 解除条件 ＋ 跟进票 |
|---|---|---|---|
| **D1** | 合成材料的分块内容与其坐标**不自洽** ⇒ 来源复核必然判「坐标不可信」 | 6 个分块里 2 段引用文本长度 **0**；另 2 段内容长度 **477／397** **大于**坐标跨度 **399／328**；重建原文时 793–818 重叠区字符**逐位冲突**；来源上下文接口自报 `"verbatim": false`；两个项目的来源锚点都是空 | 按 **#484** 的接入路径重新接入一份「分块内容与其坐标在原文上逐字一致」的材料；重跑前先做可复跑比对：`GET /projects/{id}/sources/{chunk}` 的 `quoted_text` 与原文 `[start_at,end_at)` 切片逐字相等（不等 ⇒ 材料不合格）。**跟进票 #484** |
| **D2** | 许可提案把**待授权资料的分块**计入本次操作的来源复核 ⇒ 授权与被授权**互为前提** | 提案请求体带的三个版本（`project_version=3`／`context_revision=1`／`asset_revision=1`）**与库里完全一致** ⇒ 不是请求过期；失败在**操作级来源复核**这一步；新建绑定的资料此刻只有默认策略（`model_use_allowed=false`）⇒ 「要授权」被「要先有授权」挡住 | 契约侧：F01-06b 之前补「来源锚点/复核准备」节点；**或**代码侧：政策提案这类「授予权限」的操作不应把待授权资料的分块计入该操作的来源复核。**跟进票 #545** |
| **D3** | F01-07 按契约声明就是 `vector_required` ⇒ 受控窗口内为 NOT RUN | 契约把 F01-07 声明为 `retrieval: vector_required`（#546 合入）；受控 profile 只放行 chat、拒 embedding ⇒ KB 处于部署现状时只读检索探针返回 `500 / code 1007`，且**两本账本文件仍不存在** ⇒ 拒绝发生在 transport 之前，**零 provider 请求** | 把 embedding 纳入受控 profile 并拆分 chat/embedding 额度（`scripts/lingdoc_mock/controlled_window_check.py` 的 `VECTOR_REQUIRED_UNBLOCK`）。**跟进票 #546／#480**。⚠️ 口径提示：当前契约里**没有任何** `keyword_only` 声明的节点 ⇒ 该可复跑核验在 F01 里没有承载节点 |
| **D4** | 材料原文文件不在任何 storage 根下 | `resources` 行指向的物理路径在所有 storage 根下都不存在（上轮会话的 storage 根已空）⇒ 任何需要「取回原文」的动作只能退化 | 材料接入后让跑验收的服务实例使用接入时的同一个 storage 根，并在证据里记录 `LOCAL_STORAGE_BASE_DIR` ＋ 文件 sha256。**跟进票 #484** |
| **D5** | 受控窗口内**无法重新接入材料** ⇒ D1 没法在窗口内自我修复 | 无 embedding 模型时解析在 `processChunks get embedding model failed` 处中止（`parse_status=failed`、**0 分块**）；给了 embedding 模型则被 profile 以 1007 拒；**KB 更新接口静默丢弃 `embedding_model_id`**（PUT 200 但字段仍为空） | 三选一：① 把「材料接入」划到受控窗口**之外**并单独授权 embedding ② 让分块落库与 embedding 解耦 ③ 给 KB 更新接口补上 `embedding_model_id`（本身可单独提票）。**跟进票 #484** |
| **D6** | #545 的缺口真实且当前不可绕过 | Run B（省掉 4 个许可节点的 24 节点变体）里 `POST /projects/{id}/generations` 返 **422 `asset_not_authorized`** ⇒ 许可记录缺失时生成在 provider 之前就被拒 | 修掉 D2 后 F01-07 的真实调用即可在窗口内发生（v2 限额足够：单次预留上界 16,000+4,096＝20,096，累计 4×20,096＝80,384 ≤ 90,000）。**跟进票 #545** |

### 15.4 账本事实（真实请求对账）

| 项 | 值 |
|---|---|
| **真实 provider 请求次数** | **0**（已发出/已响应/失败/重发 ＝ 0/0/0/0；非预期调用（DeepSeek／其它端点／embedding）＝ **0**） |
| 受控额度消耗 | **0 / 4 次**；累计预留 **0 / 90,000 token** |
| 两本账本（`qwen-beta.json` ／ `qwen-beta.json.calls.json`） | **均不存在** ⇒ 窗口**连预留都没发生过**（模型白名单检查发生在写账本之前） |
| DB 账本 `lingdoc_model_call_ledger` | 总行数 **1**，**本轮新增 0**；那一行是 **v1 窗口（2026-10-23）的历史行**（`source_kind=generation_run`／`model_id=lingdoc-chat-qwen3.8-flash-iso`／`provider=aliyun`／`run_mode=real`），本轮**未改动** |
| 为什么一次都没发生 | 不是额度不够，是**发不出去**：Run A 断在 F01-06b（409）；Run B 绕过许可链后 F01-07 被 422 拒（provider 之前） |
| 费用口径 | 不适用（`cost_basis = not_applicable`）⇒ **不得**把 0 读成「免费」（票 #527） |

### 15.5 边界声明（不可省略）

**一次受控真实调用成功 ≠ F01-07 验收通过 ≠ Beta 放行通过。** Run B 已经把这条钉死：许可缺失时生成**在 provider 之前**被 422 拒 ⇒ 「那一次成功」不能代替「F01-07 通过」（且 2026-10-23 那次 v1 调用发生在 #545 的门槛形成**之前**）。
⇒ 本节只登记「#485 这一轮的事实」，**不改任何放行结论**：协议 §6 要求主流程无 FAIL/BLOCKED/NOT RUN/DEFERRED_MANUAL，而本轮是 **1 处 BLOCKED ＋ 20 个 NOT RUN**。

### 15.6 本轮仍未解除 / 未运行的事实

| 项 | 现状与承接 |
|---|---|
| #485 主流程 | **BLOCKED 1 处（F01-06b）＋ NOT RUN 20** ⇒ Beta 首版**不通过**。解除条件见 §15.3 的 D1／D2／D5（跟进票 **#484**、**#545**） |
| F01-07 的真实调用 | **NOT RUN** —— D2（许可链断在 F01-06b）＋ D3（契约声明 `vector_required`）＋ D6（许可缺失 ⇒ 422）。解除条件 ＋ 跟进票 **#545**／**#546**／**#480** |
| `GAP-BROWSER`（浏览器端到端 / 三方一致性） | **NOT RUN**（本轮未起前端）。解除条件：起真实前端并用 `--frontend-url` 重跑浏览器侧，再跑三方一致性比对。跟进票 **#480** |
| `GAP-RESTART`（服务重启后的读回） | **NOT RUN**。解除条件：用 `--db-driver postgres --db-dsn <隔离库 DSN>` 跑重启读回。跟进票 **#480** |
| `GAP-WORD`（Word 人工检查） | **NOT RUN**（F01 未走到 F01-21…23 ⇒ 没有 DOCX）。解除条件：F01 走到下载后用真实 Microsoft Word 跑完并回填五个必填字段。跟进票 **#480** |
| `GAP-MATERIAL-HASH`（材料权威哈希） | **NOT RUN**（原文文件已丢失 ⇒ GAP-D4）。解除条件：原文在盘上后用 `--material <名称>=<sha256>` 登记。跟进票 **#480**／**#484** |
| `GAP-PROVENANCE`（候选 provenance 字段） | **NOT RUN** —— OpenAPI 的 Candidate 无 `provenance` 字段 ⇒ `provenance_real` 断言挂不上。跟进票 **#499** |
| `GAP-FAILURE-PATH`（F01 序列没有 4xx 失败节点） | **NOT RUN** —— #501 只落地了形状，4xx 节点仍未挂进 F01 序列。跟进票 **#499** |
| `GAP-REPRO`（第二人独立复现） | **NOT RUN**（记录项，**不是**首版门槛）。跟进票 **#489** |
| #490 放行结论表 | **NOT STARTED** —— 本轮已产出可直接入表的逐节点结论（§15.2） |

### 15.7 本轮未做的事（防止下一个人误读）

- **provider 请求数 = 0**：本轮只是**同步文档**，未跑 F01、未连真实后端、未发起任何真实模型调用。
- **未改**：`docs/08-本轮实施方案/acceptance/gaps.json`、`docs/08-本轮实施方案/Spec-139-模板目录验收矩阵.md`、`T139-验证报告.json`（PF-13 名下、另有线）、`scripts/lingdoc_parallel_surfaces.py` 与 `docs/08-本轮实施方案/G1-G7-Spec-完成路径图.md`（管理会话正在改）。
- **未**删除或清理任何 worktree、分支、`D:\wt\g3-t1..t10` 与历史证据；**未碰** PR #537。
- 本文件的改动**只有一个文件**：`docs/BETA_STATUS.md`。

## 16. 第八批（第三轮同步）：#588 合入后的 F01 全流程重跑 —— **Track-C**（2026-10-24 12:44）

> **本节的事实来源**：`D:\wt\lingdoc-485-evidence\track-c\TRACK-C-REPORT.md`（完整报告）＋ `D:\wt\lingdoc-485-evidence\track-c\f01-report.json`（**契约执行报告，机器产物**）＋ `D:\wt\lingdoc-485-evidence\logs\server-next.stdout.log`（服务端日志，**第 433–437 行**＝F01-07 的完整失败链）＋ `D:\wt\lingdoc-485-evidence\logs\llm_debug\3724894d-490d-4599-a63c-9c31d491f625.log`（embedding 被受控 profile 在 transport 之前拒绝的原始证据）。**上一轮（Track-A／Track-B）的证据**在 `D:\wt\lingdoc-485-evidence\` 根目录（`defects.md`／`gaps.md`／`node-verdicts.md`／`ledger-status.md`／`track-a/`／`track-b/`），本节末尾逐条给出对比。
> ⚠️ **本轮 provider 请求数 = 0**（§16.5）：不是省着用，是**在 transport 之前就被拒**。
> ⚠️ 本节**只登记事实，不改放行结论**。

### 16.1 本轮基线、环境与「为什么这一轮是单变量测量」

| 项 | 值 | 出处 |
|---|---|---|
| 同步所依据的 head | `origin/main` = `c14770f9` | 第三轮实测 `git rev-parse origin/main` |
| **真实验收所依据的代码 sha** | `4b73e8e7da3270147aa8b2bce689d2816a88833c`（PR #588 合并提交） | `track-c/TRACK-C-REPORT.md` 抬头 ＋ `gh pr view 588` |
| 契约 | `docs/08-本轮实施方案/contracts/workflow.json` = **28 节点 / 58 checks**（`version 0.5.0`，`mode = specification_not_executed_against_provider`） | 在 `c14770f9` 的工作树上直接数出；且 `git diff 4b73e8e7..c14770f9 -- contracts/workflow.json` **为空** ⇒ 与本轮所用契约**逐字相同** |
| 二进制 | `D:\wt\lingdoc-485-evidence\bin\weknora-4b73e8e7.exe` | 证据目录 |
| 环境 | 隔离 PostgreSQL `weknora_iso` ＋ 隔离 Redis DB 3 ＋ profile `lingdoc-qwen-token-budget-20261024-v2` ＋ 32 字节 `SYSTEM_AES_KEY` ＋ `AUTO_MIGRATE=false` | 与 #485 那一轮**逐项相同** |
| 材料 | `128b88fd-8389-4800-9851-92e9bf6fe19c` | 与 #485 那一轮**同一条** |
| owner 身份 | 新身份 `f01-485-989a8030@weknora.local`（user_id `3cbb7fff-5808-4710-9ae1-9c0ae25b643f`，role=owner） | 必须换新身份，否则契约复用同一 Idempotency-Key 会**重放**上一轮的项目而不是新建一个 |
| 本轮项目 | `397904db-5367-4c07-a9b6-299bce50681b` | `server-next.stdout.log` 的请求路径 |
| **单变量** | 相对 #485 那一轮，**唯一变化是代码**（PR #588）；数据库／Redis／profile／账本路径／材料／契约**全部相同** ⇒ **F01-06b 的结果变化只能归因于 #588** | `TRACK-C-REPORT.md`「为什么这一轮是单变量测量」 |

### 16.2 逐节点结论：**PASS 11 / NOT RUN 17**

**机器口径**（`track-c/f01-report.json`，**执行器视角**）：`total_steps = 28`、`completed_steps = 11`、`runner_status = "failed"`、`failed_step.id = "F01-07"`、`steps[]` 恰好 11 条。
**契约判定**（**放行口径**）：`workflow.json` 的 `retrieval_modes.meaning.vector_required` 原文 —— 「该节点确实需要向量检索：**在受控窗口里它是 NOT RUN**，解除条件见 `scripts/lingdoc_mock/controlled_window_check.py` 的 `VECTOR_REQUIRED_UNBLOCK`」⇒ **F01-07 的判定是 NOT RUN，不是 FAIL**。**两个视角都真实，但不是同一层**（详见 §16.3）。

| 节点 | 期望 | 实测 | 验收判定 |
|---|---|---|---|
| F01-01 `getTemplate` | 200 ＋ 64 位规则集哈希 | 200，1/1 checks 通过 | **PASS（真实执行）** |
| F01-02 `createProject` | 201 ＋ 绑定模板 | 201，1/1 通过 | **PASS（真实执行）** |
| F01-03 `saveSpec` | 200 ＋ 规格落库 | 200，1/1 通过 | **PASS（真实执行）** |
| F01-03b `recordActivationRead` | 201 ＋ actor/project_version/spec_revision | 201，3/3 通过 | **PASS（真实执行）** |
| F01-04 `activateProject` | 200 ＋ `status=active` | 200，1/1 通过 | **PASS（真实执行）** |
| F01-05 `listChapters` | 200 ＋ 两章骨架读回 | 200，2/2 通过 | **PASS（真实执行）** |
| F01-06 `bindAsset` | 201 ＋ `processing_state=ready` ＋ 字段集 | 201，2/2 通过 | **PASS（真实执行）** |
| **F01-06b** `createChangeDraftPostPolicyProposal` | 201 ＋ `/data/status=draft` | **201**（上一轮是 **409 `stale_input`**），1/1 通过 | **PASS（真实执行）** —— **本轮最硬的进展** |
| **F01-06c** `assessChangeSet` | 200 ＋ `/data/status=assessed` | **200**（上一轮未到达），1/1 通过 | **PASS（真实执行）** |
| **F01-06d** `applyChangeSet` | 200 | **200**（上一轮未到达），0 条 check | **PASS（真实执行）** |
| **F01-06e** `getProjectAssetPolicy` | 200 ＋ 许可读回 | **200**，2/2 通过（`model_use_allowed=true`、`version_bound=[2]`）（上一轮未到达） | **PASS（真实执行）** |
| **F01-07** `startGeneration` | 202 ＋ 章节一致 | **机器视角（执行器）**：**被尝试过**，HTTP **503 `dependency_unavailable`**（`retryable=true`）⇒ 机器产物记 `runner_status = "failed"` ＋ `failed_step = {id: "F01-07", operation_id: "startGeneration"}`（⚠️ 该产物**没有** 503／`dependency_unavailable`／`failures` 键 ⇒ 状态码与根因是**人工从服务端日志补齐的**，票 **#592**）；**契约视角（放行）**：该节点声明 `retrieval: vector_required` ⇒ 按 `retrieval_modes` 的口径，受控窗口里它就是 NOT RUN | **NOT RUN**（契约／放行口径）—— ⚠️ **不是 FAIL**：记成 FAIL 会把「解除条件＝把 embedding 纳入受控 profile ＋ 拆分 chat/embedding 额度」误读成**代码缺陷**，而根因是**授权边界**。解除条件见 §16.4 |
| F01-08 … F01-23（**16 个节点**） | 各节点期望 | 未执行 | **NOT RUN**（挂在 F01-07 那次生成运行后面） |

**汇总（契约／放行口径）**：**PASS（真实执行）11**（F01-01…F01-06e，合计 **15 条 checks 全过**）／**NOT RUN 17**（＝ F01-07 **一个** ＋ F01-08…F01-23 **十六个**）／**缺结论 0**（28/28 都有结论）⇒ **11 + 17 = 28 = 契约节点数**。
⇒ **协议 §6「主流程不能含 FAIL/BLOCKED/NOT RUN」⇒ 本轮 Beta 首版主流程仍不成立。**

**对比上一轮（Track-A，代码 `b63aa79f`）**：PASS **7 → 11**（＋4 ＝ F01-06b／06c／06d／06e）；BLOCKED **1 → 0**（F01-06b 由 409 `stale_input` 转 PASS）；**NOT RUN 20 → 17**（上一轮的 20 ＝ 06c／06d／06e ＋ F01-07…F01-23；本轮的 17 ＝ F01-07 ＋ F01-08…F01-23 ⇒ **F01-07 的判定两轮都是 NOT RUN**）。

### 16.3 三个层次必须分开读：**机器产物（执行器视角）／契约判定（放行口径）／人工判定（语义断言）**

| 层 | 谁说的 | 本轮是什么 | 能用来做什么／**不能**用来做什么 |
|---|---|---|---|
| **① 机器产物（执行器视角）** | `track-c/f01-report.json` | `total_steps=28`、`completed_steps=11`、`steps[]` 11 条、`runner_status="failed"`、`failed_step={id:"F01-07"}`、`checks[]` 15 条全 `passed`、`manual_assertions[].status="not_run"`、`verification_scope=http_smoke_only`、`provider_semantics_status=not_verified` | 能证明「**哪些节点被执行过、HTTP 状态、机器 check 的求值结果**」；**不能**用来定放行结论 —— 它把 F01-07 记成 `failed`，且**没有** 503／`dependency_unavailable`／`failures` 键（缺口见 §16.7 的 **#592**） |
| **② 契约判定（放行口径）** | `workflow.json` 的 `retrieval_modes` ＋ 验收协议 §6 | F01-07 声明 `retrieval: vector_required` ⇒ **在受控窗口里它就是 NOT RUN** | 这才是**放行结论**用的口径 ⇒ **PASS 11 / NOT RUN 17** |
| **③ 人工判定（语义断言）** | 执行会话逐条核对契约语义 | F01-06b／06c／06d／06e 各 1 条 `assertions`（共 **4 条**）**人工判为通过** | 只覆盖这 4 条的**语义**层；**不得**读成机器验证 |

- **机器求值的 checks**：契约 58 条 checks 中，**本轮只求值了 15 条**（覆盖已执行的 11 个节点），**15 条全部 `status: "passed"`**；**其余 43 条未求值**（上游未成立 ⇒ 不得求值）⇒ **不得**把「15/15 全过」读成「58 条全过」。
- ⇒ **F01-06b～06e 的 HTTP 层与机器 check 层是机器证据；它们的语义层是人工判断。** **不得**把「11 PASS」读成「F01 通过」，也**不得**把人工判定的 4 条读成机器验证。
- ⇒ **不得**因为机器产物写了 `runner_status="failed"` 就把 F01-07 记成 **FAIL**（放行口径是 NOT RUN）；反过来也**不得**因为契约判它是 NOT RUN 就说「服务端什么都没发生」—— **503 确实发生了**，只是它的**判定**是 NOT RUN。

### 16.4 F01-07 的根因（机器证据）与**唯一**解除条件

> **判定口径提醒**：F01-07 的**放行判定是 NOT RUN**（§16.2／§16.3）—— 本节记的是「**它为什么没跑成**」与「**怎样才能跑**」，**不是**一个代码缺陷的诊断。它被尝试过、返回了 503（机器产物里记为 `failed_step`），但按契约 `retrieval_modes` 的口径，确需向量检索的节点在受控窗口里就是 NOT RUN。

服务端日志顺序（`logs/server-next.stdout.log` 第 433–437 行，同一请求 id `3724894d-490d-4599-a63c-9c31d491f625`）：

```
knowledgebase_search.go:142[HybridSearch] | knowledge base IDs: [ab69e3a1-…], query text: 基于指定材料起草研究问题，待核实项单…
model.go:197[GetModelByID]     | Model found, name: qwen3.7-text-embedding-flash, status: active
model.go:541[GetEmbeddingModel] | Embedding model initialized successfully
ERROR POST /api/v1/lingdoc/projects/397904db-…/generations → 503 {"error":{"code":"dependency_unavailable","message":"依赖暂时不可用。","retryable":true}}
```

`logs/llm_debug/3724894d-490d-4599-a63c-9c31d491f625.log`（同一次请求）：

```
================ Embedding ================
Model: qwen3.7-text-embedding-flash
---------- Error ----------
model is outside the acceptance budget profile
```

⇒ **embedding 请求在受控 profile 的边界上被拒（transport 之前）**，不是「发出去被上游拒绝」。这与契约把 F01-07 声明为 `retrieval: vector_required` 一致（`workflow.json` 实测：**只有 F01-07 带 `retrieval` 标签**），也与 #546 的口径①一致。

**解除条件（唯一）**：把 **embedding 纳入受控 profile 并拆分 chat / embedding 额度**（#546 口径①；**embedding 授权已批并已执行（2026-10-25）**：口径裁定与新 priced embedding profile 已落地（PR #668，合并提交 `79613798`），真实 provider 请求 **1 次**被 provider **403 `insufficient_quota`** 挡住 ⇒ **向量检索仍未验证**，详见 §20.13）⇒ 解除条件载体＝`scripts/lingdoc_mock/controlled_window_check.py` 的 `VECTOR_REQUIRED_UNBLOCK`（跟进票 **#546**／**#480**）。
⚠️ **不能做的事**（明确记下）：不得改 `retrieval` 标签、不得关限制、不得伪造结果让 F01-07 变绿；**也不得**把「F01-06b 通过」说成「F01 通过」；**也不得**把 F01-07 记成 FAIL —— 那是授权边界，不是代码缺陷。

⚠️ **机器产物的缺口（必须记下，见 §16.7 的 #592）**：上面这段 503 状态码与根因**全部是人工从服务端日志补齐的** —— `track-c/f01-report.json` 里**没有 `failures` 键、没有 503、没有 `dependency_unavailable`**，只有 `{"failed_step": {"id": "F01-07", "operation_id": "startGeneration"}}` ⇒ **机器产物本身无法区分「503 被策略拒绝」与「503 依赖挂了」**。⇒ **在 #592 修好之前，任何只读机器产物的下游消费者都会把 F01-07 的真实原因丢掉。**

### 16.5 账本事实：真实 provider 请求 **0** 次

| 项 | 值 | 出处 |
|---|---|---|
| 真实 provider 请求次数 | **0** | 下列三条独立依据 |
| 文件式账本 `D:\wt\lingdoc-485-ledger\qwen-beta.json` | **不存在** | 第三轮实测 `Test-Path` = False；该目录下只有 `zz-write-probe-not-a-ledger.json` ＋ `.calls.json` ＋ `.lock` 三个**探针**文件 |
| 文件式账本 `D:\wt\lingdoc-485-ledger\qwen-beta.json.calls.json`（派生总量账；`ledger.go:210`＝`ledgerPath + ".calls.json"`，Track-C 报告里简写为 `.calls.json`） | **不存在** | 同上 |
| DB 账本 `lingdoc_model_call_ledger` | 本轮**新增 0 行** | `TRACK-C-REPORT.md`「调用安全」表：表内那 1 行的 `created_at` 是 2026-10-23 17:38:49，属 **v1 profile 时代**的旧调用（`project_id=770576b5` ≠ 本轮项目 `397904db`） |
| 4 次真实 Qwen chat 额度 | **一次未用**（0 / 4 次；累计预留 0 / 90,000 token） | `TRACK-C-REPORT.md`；窗口**零预留零消耗** |
| 费用口径 | 不适用（`cost_basis = not_applicable`）⇒ **不得**把 0 读成「免费」（票 **#527**） | §14.2 |

### 16.6 口径更正（管理会话第一轮报告把 F01-07 记成 FAIL，与契约口径不符）

**本节记录一次口径更正**（更正由管理会话做出，**报告文件本体已被更正**）：

- **更正 1（判定口径）**：管理会话的第一轮报告把 F01-07 记为 **FAIL**（「受控授权边界，不是代码缺陷」），并把 `F01-08 … F01-23` 写成「**17 个**节点」，总结行写「**PASS 11 / FAIL 1 / NOT RUN 17**」⇒ 11 + 1 + 17 = **29 > 28**。
- **依据（契约原文）**：`docs/08-本轮实施方案/contracts/workflow.json` 的 `retrieval_modes.meaning.vector_required` —— 「该节点确实需要向量检索：**在受控窗口里它是 NOT RUN**，解除条件见 `scripts/lingdoc_mock/controlled_window_check.py` 的 `VECTOR_REQUIRED_UNBLOCK`」⇒ **F01-07 的放行判定是 NOT RUN，不是 FAIL**。
- **更正 2（节点计数）**：契约实测 **28** 个 step，其中 `F01-08…F01-23` 是 **16** 个（不是 17）。⇒ **正确口径：PASS 11 / NOT RUN 17**（＝ F01-07 **一个** ＋ F01-08…F01-23 **十六个**；**11 + 17 = 28 = 契约节点数**）。上一轮「多算的 1 个」是因为把 F01-07 同时算进了 FAIL 和 NOT RUN。
- **为什么这个更正重要（不是措辞问题）**：记成 FAIL 会让「解除条件 ＝ 把 embedding 纳入受控 profile ＋ 拆分 chat/embedding 额度」看起来像**代码缺陷**（像是 #546 的实现 bug），而它的根因是**授权边界** ⇒ 会把后续施工面引向错误的修复方向。用户对 #546 的裁定同样是「确需向量检索的节点**如实记录 NOT RUN 及解除条件**」。
- **本文件已与票面口径一致**：`#485` 票面上的更正评论（同一条更正，已写明「记成 FAIL 会让解除条件看起来像代码缺陷」）与本节**同源**。
- **证据文件的现状**：`D:\wt\lingdoc-485-evidence\track-c\TRACK-C-REPORT.md` **本体已被管理会话更正**（F01-07 行＝「**NOT RUN**（受控授权边界，不是代码缺陷）」；`F01-08 … F01-23` 行＝「**16** 个节点」；总结行＝「**PASS 11 / NOT RUN 17**」）⇒ **本文件按更正后的口径记，证据文件与本文件不再矛盾。**
- ⚠️ **不要倒过来照抄机器产物**：`track-c/f01-report.json` 是**执行器视角**（它把 F01-07 记成 `failed_step`）⇒ **机器产物不能单独用来定放行口径**（三层区分见 §16.3）。

### 16.7 剩余 GAP 与解除条件（第三轮后）

| 项 | 现状 | 解除条件 ＋ 跟进票 |
|---|---|---|
| #485 主流程 | **NOT RUN 17**（F01-07 **一个** ＋ F01-08…F01-23 **十六个**；**11 + 17 = 28**）⇒ Beta 首版**不通过** | 见 §16.4（embedding 授权）＋ 下面的 D1／D5 |
| **D2**（许可提案把**待授权资料**计入本次操作的来源复核 ⇒ 授权与被授权互为前提） | **已解除**（PR #588 `4b73e8e7`；Track-C 实测 F01-06b = 201，06c/06d/06e 一路打通） | — |
| **D1**（材料分块内容与其坐标**不自洽**） | **未解除**。本轮不再卡 F01-06b（那是 **D2**，不是 D1）⇒ 但仍需**来源复核**的 F01-09／F01-11／F01-12／F01-16／F01-17 **尚未执行** ⇒ **不得**记为「材料合格」 | 按 **#484** 的接入路径重新接入「分块内容与其坐标在原文上**逐字一致**」的材料；重跑前先做可复跑比对：`GET /projects/{id}/sources/{chunk}` 的 `quoted_text` 与原文 `[start_at,end_at)` 切片**逐字相等**。跟进票 **#484**；跟进 PR **#590**（**当前 OPEN、未合入 main**） |
| **D5**（受控窗口内**无法重新接入材料** ＋ KB 更新接口**静默丢弃** `embedding_model_id`） | **未解除** | ① 把「材料接入」划到受控窗口**之外**并单独授权 embedding ② 让**分块落库与 embedding 解耦**（PR #590 的方向）③ 给 KB 更新接口补上 `embedding_model_id`（可单独提票）。跟进票 **#484** |
| `GAP-RESTART`（服务重启后的读回） | **NOT RUN**。PR #586（`bc3637cf`）已合入「隔离 PG 的服务重启读回 ＋ 浏览器/HTTP/刷新后读回三方一致性」（`scripts/acceptance/lingdoc_full_server_restart.py`），**但本轮未重跑 F01 ⇒ 未产出新的运行证据**（目录里那份 `restart-readback.json` 的时间戳早于本轮）⇒ 缺口**未解除** | 用 `--db-driver postgres --db-dsn <隔离库 DSN>` 跑重启读回，重启后重读项目/资料/许可/账本四类行并留证。跟进票 **#480** |
| `GAP-BROWSER`（浏览器端到端 / 三方一致性） | **NOT RUN**（本轮未起前端） | 起真实前端并用 `--frontend-url` 重跑浏览器侧，再跑三方一致性比对。跟进票 **#480** |
| `GAP-WORD`（Word 人工检查） | **NOT RUN**（F01 未走到 F01-21…23 ⇒ 没有 DOCX） | F01 走到下载后用真实 Microsoft Word 跑完「开/改/存/关/重开」并回填 `word-check.md` 的五个必填字段。跟进票 **#480** |
| `GAP-MATERIAL-HASH`（材料权威哈希） | **NOT RUN**（原文文件已丢失 ⇒ GAP-D4） | 原文在盘上后用 `--material <名称>=<sha256>` 登记。跟进票 **#480**／**#484** |
| `GAP-PROVENANCE`（候选 provenance 字段） | **NOT RUN** —— OpenAPI 的 Candidate 无 `provenance` 字段 ⇒ `provenance_real` 断言挂不上 | 由 **OpenAPI 拥有面**声明 `provenance` 字段 ⇒ 该断言才挂得上。跟进票 **#499** |
| `GAP-FAILURE-PATH`（F01 序列没有 4xx 失败节点） | **NOT RUN** —— #501 只落地了形状，4xx 节点仍未挂进 F01 序列 | 由 28 节点口径的拥有方先裁定序列可增节点，再把 4xx 节点挂进 F01 序列。跟进票 **#499** |
| `GAP-REPRO`（第二人独立复现） | **NOT RUN**（**记录项**，**不是**首版门槛） | 由第二名开发者按 #489 的步骤独立复现 F01 契约，并把证据绑到当前 head。跟进票 **#489** |
| #490 放行结论表 | **NOT STARTED** —— 本轮已产出可直接入表的逐节点结论（§16.2） | — |
| **机器产物缺失败原因**（`track-c/f01-report.json` **没有** `failures` 键、**没有** 503、**没有** `dependency_unavailable`，只有 `{"failed_step": {"id": "F01-07", "operation_id": "startGeneration"}}`） | ⚠️ **未解除**（本轮**实测发现**）⇒ **F01-07 的 503 状态码与根因（`model is outside the acceptance budget profile`）全部是人工从服务端日志补齐的** —— 机器产物本身**无法区分「503 被策略拒绝」与「503 依赖挂了」**。⇒ **在修好之前，任何只读机器产物的下游消费者都会把 F01-07 的真实原因丢掉**（执行器在 `stop_on_mismatch=True` 的抛出路径上没有任何持久化；`stop_on_mismatch=False` 分支才写 `failures`） | 跟进票 **#592**（「放行判定不得产生假 PASS：失败必须进机器产物 ＋ 人工断言未判定⇒NOT RUN ＋ 没跑到的 check 必须生成 NOT_RUN ＋ 受控窗口核验接入放行链」，**OPEN**） |
| `docs/08-本轮实施方案/contracts/validation-result.json`（契约静态校验产物） | ⚠️ **已过期**（本轮**实测发现**）—— 仓内提交的那份仍写 `continuous_trace_steps = 24`／`shape_checks = 957`／`scenarios = 34`，而 `c14770f9` 上实跑 `validate_artifacts.py` 得 **28 / 976 / 35** ⇒ 这是**产物过期**，**不是**契约变了（契约本身实测就是 28 节点 / 58 checks）。成因：CI 是在 `scratch` 副本上跑该脚本、**不回写**仓内文件（`.github/workflows/lingdoc-review.yml:112-118`），仓内这份是**手工提交的快照**，最后一次更新在 `fa9f85f3`（#494 时代） | 本轮**未改**该文件（契约产物不在本轮范围内）⇒ 需由**契约产物拥有面**重跑并提交该产物。**票号待管理会话回填**（本轮先记为「已过期，实跑为 28/976，待补票」）。**不得**据此把「契约节点数」读成 24 |
| **#568**（`000001_agent.down.sql` 的关联键 `m.id = k.id` 大概率本就写错） | ⚠️ **未解除**（**票面残留，本节补登记**）—— `models.id` 与 `knowledges.id` 是各自独立生成的随机 UUID 主键，两表**没有**声明式关联 ⇒ 该语句改的是**id 撞车**的那一行、漏掉**真正引用了模型**的那一行 ⇒ ① **#564 的语法修复数据影响为零**（它本来就一行都匹配不到）② 但也意味着 **「000001 的 down 从来没有真正撤销过 up 侧那次 `models.tenant_id` 回填」** ⇒ **「迁移可回滚」在数据语义这一层仍然是假的** | 语义修正（正确的关联键应是「引用方 → 被引用方」，如 `k.embedding_model_id = m.id`）由 **#568** 承接；本轮**未**改该迁移文件。**不得**因为 #564 已关就认为「迁移可回滚」已成立 |
| **#578 的矩阵 / T139 回填**（受控预算账本判据的口径变更） | ⚠️ **待办**（票面残留）—— #578 的实现侧义务（「把改变了判定的情形如实列出」）**已完成**（PR #587 描述的「对既有已落账数据的影响」一节），但**验收矩阵与 `T139-验证报告.json` 的回填归管理会话**（#578 边界明文：实施侧**不得**改这两个文件）。本轮**实测**：该矩阵与报告的验收点**全部是 #139 域内**（`US-01…US-15`、`TD-01…`、`AC-04-03`、`AC-04-05`），**没有**一行是「受控预算账本的判据」⇒ 本票在这两份文件里**没有可回填的行** | 由**管理会话**确认是否需要回填、以及是否需要为「受控预算账本」另立一份口径表。跟进票 **#578**（已关，残留待办） |

### 16.8 本轮未做的事（防止下一个人误读）

- **provider 请求数 = 0**：本轮只做**文档同步**，未发起任何真实模型调用或真实外部请求。
- **未改**：`docs/08-本轮实施方案/acceptance/gaps.json`、`docs/08-本轮实施方案/Spec-139-模板目录验收矩阵.md`、`docs/08-本轮实施方案/T139-验证报告.json`、`docs/08-本轮实施方案/contracts/**`、`scripts/lingdoc_parallel_surfaces.py` 与 `docs/08-本轮实施方案/G1-G7-Spec-完成路径图.md`（路径登记由管理会话统一做）、`scripts/lingdoc_mock/**`（**含 PR #590 正在改的 `run_f01.py`**）。
  - ⚠️ **一处需要说清的副作用**：本轮为取「契约静态校验」数字，**运行过** `docs/08-本轮实施方案/contracts/validate_artifacts.py`；该脚本**会就地重写**同目录的 `validation-result.json` ⇒ 那个文件一度被改动，**已恢复为 HEAD 的内容**，**不在本 PR 的改动里**（见 §16.7 末行记录的「产物过期」观察）。**`contracts/workflow.json` 本身一字未改。**
- **未合并任何 PR、未用 `--admin`、未删任何文件/分支/容器/卷。**
- 本文件的改动**只有一个文件**：`docs/BETA_STATUS.md`。


---

## 17. 第九批（第四轮同步）：#590 合入后的**决定性技术实验** —— **Track-D**（2026-10-24 17:37）

**这一批与前几批不同**：它不是又一次 F01 跑批，而是**先证明「无 embedding 路线能不能支撑 Beta 的生成链」**，再据此决定契约要不要改。

**环境**：隔离 SQLite（`D:\wt\lingdoc-484-evidence\data\lingdoc-484-final.db`）、独立存储根、私有 miniredis、`RETRIEVE_DRIVER=sqlite`；
服务与材料闸门二进制**由 `origin/main` = `781295c7` 重建**（tag `sqlite_fts5`；清单 `D:\wt\lingdoc-484-evidence\track-d-binary-manifest.json`，含两个二进制的 sha256）。
⚠️ **账本不删**：#590 的启动脚本会在启动时删账本（那等于凭空多出一份调用授权）⇒ Track-D 的启动脚本**只报告账本状态**。

### 17.1 结论

| 维度 | 结论 |
|---|---|
| **代码实现** | PASS —— #590（分块与 embedding 解耦、材料闸门两阶段、KB 更新的 `embedding_model_id` 持久化）已在 main；本轮在真实运行里全部生效 |
| **集成** | PASS —— 二进制来自合并后的 main（`781295c7`），登记处已补登记 #590 的 30 条无主路径（PR #607，PF-14 65 → 95） |
| **自动化测试** | PASS —— `LingDoc PR gate` 在 #590／#604／#607 的当前 head 全绿；`scripts/ci` 的 `ciscripts` 步骤**无条件**运行（登记处守护每次都在 CI 里跑） |
| **真实验收** | **七项零 embedding 前置全部 PASS**（`zero-embedding-verdict.json` 的 `overall: PASS`）；**真实 provider 请求：chat 1 次（授权 4 次，余 3 次）／embedding 0 次**；F01 走到 **15/28**，止于 F01-11（**新增一个真实 FAIL**，票 #608） |

### 17.2 七项零 embedding 前置（逐项证据见 `zero-embedding-verdict.json`）

1. 材料已解析 PASS｜2. 分块已落库 PASS｜3. 分块正文 == 坐标跨度 PASS（`[0,489)`／`[475,902)`，逐字一致）｜
4. 关键词检索能取回材料 PASS（3 条查询各 2 条命中；**探针前后两本账本都不存在**）｜
5. **生成请求构造正确 PASS**（provider 调试日志：chat 请求块 1 个、**embedding 请求块 0 个**；模型是已核准的 `qwen3.8-flash`；端点是白名单内唯一端点；请求里 `CompletionBudget=4096` ＝ profile 的输出上限）｜
6. chat 模型选中已核准行 PASS（全库 1 行、8 项事实逐字核对全 True）｜
7. **零 embedding** PASS（a. 数据库模型调用账本 1 行且是聊天行、`embedding_rows=0`｜b. 两本 JSON 账本无 embedding 行｜c. `lite_embeddings` 24 行但 `dimension>0` **0 行**｜d. provider 日志无 Embedding 块｜e. 应用日志无 embedding 请求）

**应用日志里的第二个独立证据**（接入阶段）：`embedding model unavailable for KB …; the vector pipeline is skipped, the keyword pipeline is not` ⇒ **#590 的解耦在真实运行里落地**。

### 17.3 真实调用与三本账

- 请求：模型 `qwen3.8-flash`、端点 `https://llm-k57eqvdqacn4nov3.cn-beijing.maas.aliyuncs.com/compatible-mode/v1/chat/completions`、
  `RequestID=34235362-9739-900d-b647-4968d3292409`、`RequestFingerprint=sha256:6f8cdaeb…94a5b`、
  `Temperature=0.20 / CompletionBudget=4096 / ResponseFormat=json_object`、耗时 7.651s。
- 请求体**只带关键词检索取回的两段材料**（逐字引文 ＋ `locator`），**没有任何 embedding 相关字段**。
- 回答：合法 JSON（`body_markdown` 每条事实都带 `[[source:…]]`、`source_ids`、`review_items` 3 条）。
- 账本①窗口账本 `qwen-beta-sqlite.json`：1 条请求，`status=response_received`、`usage_status=reported`、
  `estimated_input_token_upper_bound=4849`、`input/output/total = 1037/521/1558`、`finish_reason=stop`；
  `reserved_tokens=8945`、`actual_tokens=1558`、`calls_max=4`、`blocked=false`。
- 账本②共享账本 `.calls.json`：同一条调用，`max_calls=4`、`max_concurrency=1`。
- 账本③数据库 `lingdoc_model_call_ledger`：1 行（`kind=call`／`provider=aliyun`／`model_id=lingdoc-chat-qwen3.8-flash-iso`／
  `run_mode=real`／`input_tokens=1037`／`output_tokens=521`／`status=succeeded`／`source_kind=generation_run`，
  `source_id` = 生成运行 `f4135915-…`）。
  ⚠️ **更正（2026-10-24，独立审查发现）**：本节初稿把这一行读成了「`source_kind=real`／`context=generation_run`」——
  **两处都错**：真实列名是 **`run_mode=real`** 与 **`source_kind=generation_run`**（`source_id` 才是生成运行 id），
  表里**没有 `context` 列**。事实本身（1 行、聊天调用、usage 1037/521）不变，只是字段读法更正。
- ⇒ **预留（8945）、provider 实报 usage（1558）、累计剩余额度（90000−8945）三者分开记录**，互不冒充。

### 17.4 这一批暴露的两个问题（已开票）

| 票 | 级别 | 问题 | 影响 |
|---|---|---|---|
| **#608** | **P1（阻塞放行链）** | F01 契约的 `acceptCandidate` 请求体**缺服务端 `binding:"required"` 的 `confirmation` 载荷**（代码注释另写明 `expected_working_copy_revision`／`expected_body_hash`「两者缺一不可」）⇒ 永远 400 `invalid_request` | **F01-11…F01-23 到不了**；候选稿已真实产出却采纳不了 |
| **#609** | **P1（健壮性）** | `types.ModelParameters.Scan` 只认 `[]byte`，拿到 `string` 就**静默返回 nil**；SQLite 的 `models.parameters` DDL 是 `TEXT` ⇒ 非 GORM 写入的行读回来 `provider/端点/凭据` 全空，而列表仍显示为已配置 ⇒ 失败点在远处、形态像配置问题 | 任何非 GORM 写入的模型行在 SQLite 部署里都是哑行；`internal/types/` 下另有约 20 处同模式 |

**第一次尝试（保留为证据）**：同样的序列，模型行被写成 TEXT ⇒ 生成阶段 `generation_model_mismatch`、**provider 请求 0 次**
（`f01-report-attempt1-model-mismatch.json`）⇒ 这就是 #609 的现场复现。

### 17.5 口径与下一步（不改门槛、不改标签）

- **F01 的放行判定口径不变**：F01-07 仍按契约声明 `vector_required` 记 **NOT RUN**。Track-D 证明的是「这条链**能**跑」，**不是**「契约已经改了」。
- **下一步（按 #546 口径②的 A 路线准备）**：① **先修 #608**（否则 F01-11 之后到不了）；② **正式契约变更**：`workflow.json` 的 F01-07 `retrieval` 由 `vector_required` 改为 `keyword_only`，并同步 `controlled_window_check.py` 的 `VECTOR_REQUIRED_UNBLOCK` 文本 —— 这是**要走 Spec → 验收矩阵 → 独立审查 → PR 的正式变更**，**不是**在本轮里改标签；③ **零 embedding 守护**落到放行链上真被跑；④ 修 **#609**。
- **授权现状**：embedding 请求 **0 次**（未授权）；真实 chat **1 次 / 4 次**（余 3 次）。**不因本轮成功就扩大外部调用授权。**

### 17.6 证据索引

- 报告：`D:\wt\lingdoc-485-evidence\track-d-experiment\TRACK-D-REPORT.md`
- 轮次目录：`D:\wt\lingdoc-485-evidence\track-d-experiment\run-20261024-781295c7\`
  （`zero-embedding-verdict.json`／`f01-report.json`／`f01-report-attempt1-model-mismatch.json`／`gate-stage1.json`／`keyword-probe.json`／`kb-readback.json`／`ingest-readback.json`／`f01-run-meta*.json`）
- provider 请求原文：`D:\wt\lingdoc-484-evidence\logs\llm_debug_trackd\20261024_173737.438.log`
- 两本账本：`D:\wt\lingdoc-484-evidence\ledger\qwen-beta-sqlite.json` ＋ `.calls.json`
- 二进制清单：`D:\wt\lingdoc-484-evidence\track-d-binary-manifest.json`

---

## 18. 第十批（第五轮同步）：三条缺陷线合入 ＋ F01-07 检索方式口径变更

**基线**：`origin/main` = `96d9320ed2c958b0ec5054e2e75a341a13a91b12`。本批把 Track-D 实验暴露的三个问题与一件口径变更落进 `main`：**#608**（F01-11 采纳节点）、**#609**（模型参数扫描）、**#605**（`client/**` 门禁覆盖）、**#546 口径②**（F01-07 的检索方式声明）。

| 项 | 票 | PR | 合并提交 | 它修的是什么 | 证据 |
|---|---|---|---|---|---|
| 候选采纳契约 | #608 | #612 | `{sha612}` | 契约的 F01-11／F01-12 请求体与服务端 HTTP 契约对不上（缺必填的 `confirmation` ＋ 工作副本声明）⇒ 采纳节点永远 400 | 真 handler ＋ 真 SQLite ＋ 契约那份请求体：旧请求体 400/零版本、契约现在发的那份 201/恰好 1 个版本；缺载荷 400＋零版本、过期修订 409 `version_conflict`、哈希不符 409 `working_copy_dirty`、同键重投 201 `replayed` 且仍 1 个版本 |
| 模型参数扫描 | #609 | #613 | `{sha613}` | `ModelParameters.Scan` 只认 `[]byte`，拿到驱动交回的 `string` 就**静默返回 nil** ⇒ 非 GORM 写入的模型行在 SQLite 里是哑行（provider/端点/凭据全空） | 共享读取器 `internal/types/json_column.go`：收 `[]byte` 与 `string`，其余类型**报错**（`ErrUnsupportedJSONColumnValue`）；驱动的载体类型**实测**（SQLite 生产驱动 ＋ 纯 Go 驱动 ＋ PostgreSQL）；base-vs-head 复现：回退后 TEXT 写入的行读回 `provider=""` |
| `client/**` 门禁覆盖 | #605 | #611 | `{sha611}` | `client/` 是独立 Go 模块，根模块的构建显式排除它 ⇒ 只改 `client/` 的 PR 没有任何 job 编译/测试它 | 新增 `.github/workflows/client.yml`（`go build` ＋ `go vet` ＋ `go test`）；门禁面清单新增 `client` 面（`verifies=True`）；两个对照变更的真实判定输出：只改 `client/` ⇒ client job 跑、app job 不跑、coverage `verified`；只改 `docs/` ⇒ 两者都不跑 |
| F01-07 检索方式 | #546 | #615 | `{sha615}` | 契约把 F01-07 声明为 `vector_required` ⇒ 受控窗口里它恒 NOT RUN。Track-D 已证明**无 embedding 路线能真的跑通生成链** ⇒ 改为 `keyword_only`（口径②） | 声明前后对照（同一套真实产物，唯一变量是那一个字段）：窗口两次都 **PASS**，F01-07 由 **NOT RUN** 变为**可判定**；放行链层面（`beta_evidence.controlled_window_part`）判为 `held`；独立审查第一轮 **PARTIAL**（6 点）→ 复审 **PASS** |

### 18.1 F01 当前逐节点状态

| 口径 | 结果 | 说明 |
|---|---|---|
| **契约判定（放行口径）** | 见 `docs/08-本轮实施方案/Spec-Beta-F01-07-检索方式口径变更.md` §4 | F01-07 的声明已由 `vector_required` 改为 `keyword_only`；**改声明之前的实验事实不动** |
| **机器产物（执行器视角）** | failed｜15/28 节点｜26 条 checks 全过｜材料闸门两阶段：['content', 'full'] | 失败节点：{"id": "F01-11", "operation_id": "acceptCandidate"} |
| **下一轮** | 待前置合入后重跑 | 前置：隔离环境（18087，服务在跑）、材料与 KB 可复用（`7c104e1b`／`c1197b70`）、授权 2/4 次（余 2 次）、累计预留 17890/90000 |

### 18.2 真实调用与账本对账（截至本轮）

- **真实 chat 请求：2 次**（授权 4 次，余 2 次）；**embedding 请求：0 次**（未授权，也没有发生）。
- 窗口账本 `qwen-beta-sqlite.json` ＋ 共享账本 `.calls.json`：各记同一条调用，`status=response_received`、`usage_status=reported`、`input/output/total = 1037/521/1558`、`finish_reason=stop`。
- 数据库账本 `lingdoc_model_call_ledger`：2 行（`kind=call`／`model_id=lingdoc-chat-qwen3.8-flash-iso`／`run_mode=real`／`source_kind=generation_run`／`input_tokens=1037`／`output_tokens=521`／`status=succeeded`）。
- ⇒ **预留（17890）／provider 实报 usage（3026）／累计剩余额度**三者分开记录，互不冒充。

### 18.3 尚未验证的与下一步

- **尚未验证**：合并后 head 上的门禁证据；下一轮完整 F01 的逐节点结论（材料闸门两阶段 ＋ 采纳节点 ＋ 放行链的逐节点判定）；`acceptance/beta-first/` 目录的实际落盘内容；**向量开启的知识库**上的检索行为（无这样的 KB 可实测）。
- **下一步**：① 重建隔离环境二进制（合并后 main）并重跑一轮完整 F01（用掉剩余额度里的 1 次）；② 出 #490 的放行结论表；③ #546 口径②的变更要在放行链上真的被执行（`controlled_window_check.py` 已被放行链接入，但「窗口 PASS 只等于账本里没有 embedding 行、不蕴含没有尝试过 embedding」这条口径强度限制要一起交代）。

## 19. 第十一批（第六轮同步）：#619 裁定落地 ＋ 判定层三处收口 ＋ 人工判定录入通道

**基线**：`origin/main` = `c2345229`（本文件合并后的 main）。本批把「交付稳定化」这条线一次收口，并把两条判定路径的口径残留清干净。

**本批的核心事实**：#619 的服务端根因（许可类变更集 apply 自己推进了被评估对象的版本 ⇒ 正式变更**结构上不可能稳定化**）已按已获批准的裁定 **A1 / B / C** 实施；F01 契约补上了**真实缺失**的稳定化链节点；跑批器补上了**人工判定录入通道**。三者合起来，`prepareRelease` 的 422 `context_unstable` 这条阻塞**在真实后端上被解除**（见 §19.4 的零额度实测）。

### 19.1 已合入的实现（逐项：票 → PR → 合并提交 → 它修的是什么）

| 项 | 票 | PR | 合并提交 | 它修的是什么 |
|---|---|---|---|---|
| 候选采纳的 review 决策必须**一一对应** | #618 | #620 | `f404fd02` | 契约与服务端对「采纳要覆盖全部待核项」的理解不一致 ⇒ 采纳节点永远 422 |
| F01-16 的处置断言说清「一一对应」 | #621 | #624 | `1aee3f21` | 新增 check kind `one_to_one`（ID 集合相等、顺序无关、两侧无重复）⇒ 断言不再依赖未经契约保证的数组顺序 |
| 放行判定层加固（任务四） | — | #625 | `8bffdc4d` | 第二条判定路径也读人工断言与提供方语义 ⇒ 两条路径同口径 |
| 6 条 `one_to_one` 自测进 CI ＋ 非空口径统一 | #626 | #637 | `6b394ca7` | 自测原本只有手动 `--self-test` 会跑，任何 CI job 都不收它 |
| **未求值的 checks 不得被当作通过** | #632 | #638 | `655dde3f` | 差集非空 ⇒ NOT RUN；结果集合对不上 ⇒ BLOCKED |
| **人工判定录入通道** | #633 | #643 | `3d7ee440` | 跑批器恒把散文断言记成 `not_run` ⇒ G6-01 对真实证据**永不可能 PASS** |
| 校验器非空口径统一 ＋ 未求值断言独立表示 | #639 / #640 | #644 | `2e5ce854` | 两个裁判对同一个值给出不同答案；未求值条目被伪装成「失败」 |
| **#619 服务端根因（A1 ＋ B ＋ C）** | #619 | #645 | `68aa3fd6` | 许可类变更集 apply 自己推进了被评估对象的版本 ⇒ 稳定化**结构上不可能**；并落真实审计记录 |
| **#619 契约侧（稳定化链节点 ＋ `receipt_id` ＋ 场景）** | #619 | #646 | `bb0081a5` | 契约里**根本没有**稳定化节点 ⇒ 交付链无法表达「回执 → 闭环 → 稳定化 → 冻结」 |
| 登记处补记 #626 的跨面借用 | #626 | #642 | `9738ea81` | 两侧各记一条，owner 不变 |

### 19.2 #619 的裁定与实施（A1 / B / C）

| 裁定 | 内容 | 实施落点 |
|---|---|---|
| **A1** | **仅**豁免「当前变更集自身造成**且可证明**」的预期版本推进；外部漂移 / 并发修改 / 非预期变化 / 无法证明来源 ⇒ **仍须阻断**；比较必须**精确相等**（不是 `>=`） | `internal/lingdoc/workspacecore/impact_self_attributed_advance.go`：声明来自**变更集自己持久化的语义计划**（`semantic_deltas[].expected_version`），apply 前后的乐观锁（版本 ＋ 内容哈希）保证**只有 apply 自己能产生 `ExpectedVersion + 1`**；读不到变更集 ⇒ 无豁免（fail closed） |
| **B** | `ProjectAsset` 在稳定化检查中**只要求版本仍有效**，不额外要求复核确认；**其它对象**的确认要求**一行未改** | 同文件：判据两半 ——（a）修订号必须是评估时那版或**恰好**是本变更集声明的目标版；（b）**许可钉住的内容指纹**必须仍是资料当前内容（内容被换 ⇒ 指纹变 ⇒ 许可失效） |
| **C** | 授权 Owner 可经 API 完成稳定化，但须落**真实审计**，且**无法证明的事实记为「无法证明」** | `internal/lingdoc/workspacecore/stabilization_audit.go`：审计行与稳定化**同事务**落 `lingdoc_project_audits`（action `revision.stabilize`）；渠道分两层 —— `class` 是服务端**能证明**的凭据类别，`ui_origin` / `automation` **恒为 `unprovable`**；`verdict_source = machine_evaluation`（不冒充人工判定）；审计字段缺失 ⇒ **拒绝稳定化**（硬闸门） |

**规范核对（无冲突）**：`G3-pcc-change-coordination.md` §10.1 第 3 条枚举的对象集**不含** ProjectAsset，第 4 条把 ProjectAsset/SourceAnchor 单列为「版本仍有效」⇒ 与 B 逐字对应；§4.2 动作表里 `APPLIED` 的可执行动作含「稳定化」⇒ 一致。

### 19.3 三层判定必须分开读（本批把这条钉进了机器）

| 层 | 谁判 | 本批的落点 |
|---|---|---|
| **机器执行事实** | 执行器 | `run_f01.py` 的报告：每步 HTTP 码、每条 check 的求值结果、**每条散文断言的判定状态与出处**（`status` / `verdict` / `authority` / `actor` / `authorization` / `channel` / `channel_verifiability` / `occurred_at` / `evidence_ref` / `audit_id` / `run_id`） |
| **契约验收结果** | 契约 + 判定层 | `checks[]` 的求值结果；**未求值的断言有独立的取值与形状**（`not_run` ＋ `evaluated: false`），不再伪装成 `failed` |
| **人工判定结果** | 人（经审计留痕） | **权威性三档**：`server_attested`（服务端审计记录逐字段相等）/ `claimed`（无从核验）/ `absent`（缺字段 ⇒ **整条拒绝**）。**只有「权威 ∧ passed」能让散文断言被满足**；`held` / `rejected` 判了但没过 ⇒ 不得放行 |
| **正式 Beta 放行结论** | 协议 §6 | 主流程无 FAIL / BLOCKED / NOT RUN / DEFERRED_MANUAL 才算通过；`DEFERRED_MANUAL` **不阻塞**代码合并但**阻塞**放行 |

⚠️ **口径提醒**：`held` / `rejected` 在放行层显示为 `NOT RUN`（现有档位只有 `PASS/FAIL/BLOCKED/NOT RUN`），红线的实质要求（「不得让放行通过」）**已满足**；把「判了但没过」单独做成 `FAIL` 需要改两条判定路径的档位集，是另走票的施工。

### 19.4 零额度实测：稳定化链在真实后端上跑通了（**本批最硬的一条证据**）

**为什么先做这一步**：最后一次真实 chat 额度只剩 1 次；在把它花掉之前，必须先证明「稳定化真的能成」。稳定化链（`checkCurrent` → `acknowledgeImpactPreflight` → `stabilizeRevision` → `prepareRelease`）**不调用模型**，所以可以**零额度**先验。

**实测（隔离环境 18087，服务端二进制由 #619 分支重建，DB 账本前后都是 3 行 ⇒ 零 provider 消耗）**：

| 步 | 端点 | 结果 |
|---|---|---|
| 1 | `getProject` | 200，`project_version=10`、`current_context_revision=2` |
| 2 | `checkCurrent` | 200，`receipt_id=5077fd65-…`、`status=passed` |
| 3 | `acknowledgeImpactPreflight` | 200，`closed_task_ids` 非空、`proof_id` 回显该回执 |
| 4 | `stabilizeRevision` | **200、`status=STABILIZED`、`blockers=[]`** ← **A1 ＋ B 生效** |
| 5 | `prepareRelease` | **201**（冻结快照产出）← **F01-19 的阻塞解除** |

⚠️ **同一条链上的一个真实陷阱（已写入运维脚本）**：稳定化会**复核闭环证据是否仍属当前版本**。若闭环证据是**旧批次**留下的、而项目版本已经前进，`stabilizeRevision` 会返回 **409 `version_conflict`**（不是 422）。⇒ 契约里**交付检查回执必须与闭环、稳定化相邻**（F01-19a → 19b → 19c 三个节点紧挨着），中间不能有推进项目版本的步骤。

### 19.5 契约侧补齐（节点数 28 → 31，checks 58 → 64）

| 节点 | operation | 断言 |
|---|---|---|
| **F01-19a** | `checkCurrent` | `/data/status` = `passed`；`/data/receipt_id` 非空 |
| **F01-19b** | `acknowledgeImpactPreflight` | `/data/closed_task_ids` 非空；`/data/proof_id` = **上一步捕获的 `receipt_id`**（不是幂等键、不是冻结快照 id） |
| **F01-19c** | `stabilizeRevision` | `/data/status` = `STABILIZED`；`/data/blockers` 长度 = 0 |

- `CheckResult` 补 `receipt_id`（服务端真实返回，而 schema 是 `additionalProperties: false` ⇒ 改之前真实响应**违反契约**）。**不列入 `required`**：`CheckResult` 同时描述冻结快照里的 check，而那里服务端不给 receipt（依据：`internal/lingdoc/delivery/snapshot.go:154-161`、`internal/lingdoc/workspace/full_workflow_guard.go:197-201`）。
- `ObjectVersionRef.kind` 枚举补 **`DeliveryPreflight`** —— 服务端真的在发这个 kind（`impact_preflight.go:97`、`impact_source_operation.go:503`，实测 `listImpactTasks` 的 `closure_evidence[0].kind` 就是它），枚举里没有 ⇒ **真实响应违反契约**。这是本批顺带修掉的**既有缺口**。
- `scenarios.json` 35 → **37** 条：补 F34「影响任务未闭环 ⇒ 稳定化被阻断（`impact_task_open`）」与 F35「回执闭环后仍因受影响对象版本被替换而阻断（`impact_object_stale`）」。
- `checkCurrent` **不带 `Idempotency-Key`**：服务端明确说明该操作不落快照、不需要幂等键（`internal/lingdoc/workspace/delivery_http.go:114-115`），OpenAPI 也没声明 ⇒ 契约不得发它没声明的头。

### 19.6 真实调用与账本对账（截至本批）

- **真实 chat 请求：3 次**（授权 4 次，**余 1 次**）；**embedding 请求：0 次**（未授权，也没有发生）。
- 两本 JSON 账本记录的是**同一批**调用 ⇒ 计数取**较大值**（3），**不是相加**（相加会把同一事件数两次）；DB 账本 `lingdoc_model_call_ledger` **3 行**是独立见证。
- `actual_tokens = 4594`（provider 实报）、`reserved_tokens = 26835`（**预留**，不是消耗）、`blocked = false`。
- `lite_embeddings` 24 行、**维度全为 0** ⇒ 没有向量落库 ⇒ 与「embedding 0 次」一致。

### 19.7 最终一轮：31 节点 / 64 checks 的完整 F01 实跑

- **运行批次**：`f01-20261025T013341Z-5105ae32`
- **代码 head**：`c23452293396b0f303e85a9c5aea4f8bd52802af`
- **runner_status** = **completed**；完成节点 **31 / 31**
- **机器断言**：64 条，passed **64**、failed **0**
- **材料闸门两阶段**：`['content', 'full']`
- **runner 退出码** = 0；耗时 12 秒

**真实调用与账本对账（这一轮）**

- 账本行数：跑前 **3** → 跑后 **4**（两本 JSON 账本记录**同一批**调用 ⇒ 取较大值；DB 账本 `lingdoc_model_call_ledger` 是独立见证）
- 零 embedding 判定：**PASS**（产物 `zeroemb-verdict.json`）

**逐节点结论**

逐节点表见 `D:\wt\lingdoc-485-evidence\track-d-experiment\run-final-20261025-093259-c2345229\f01-per-node.md`（执行状态、HTTP 结果、checks 求值计数、人工判定状态、证据与审计）。

### 19.8 本轮的放行结论

**四层分列的结论表**（机器执行事实／契约验收结果／人工判定结果／正式放行结论）见本节末尾。

# 最终 Beta 放行结论表（机器事实／契约结果／人工判定／放行结论 四层分列）

- 运行批次：`f01-20261025T013341Z-5105ae32`
- 契约：`workflow.json` = **31 节点 / 64 checks**
- 代码 head：`(见 f01-run-meta.json)`

## 一、机器执行事实（执行器视角的事实，不含任何判定）

- `runner_status` = **completed**
- 节点：完成 **31 / 31**
- 机器断言：**64 条**，passed **64**、failed **0**
- 材料闸门两阶段：`['content', 'full']`

## 二、契约验收结果（放行口径判定，两条路径分别给出）

| 路径 | 判定 | 依据 |
|---|---|---|
| 第二条判定路径（`main_path_evidence`） | **NOT RUN** | failed 0、unevaluated 0、未判定散文断言 4、语义 not_verified |
| 主路径（`beta_evidence`） | **NOT RUN** | 逐条 outcome 100 条 |

## 三、人工判定结果（逐条，判定与可证明性分开）

| 节点 | 状态 | 判定 | 权威性 | 渠道 | 渠道可证明性 | 审计标识 |
|---|---|---|---|---|---|---|
| `F01-06b` | `not_run` | `—` | `—` | `—` | `—` | `—` |
| `F01-06c` | `not_run` | `—` | `—` | `—` | `—` | `—` |
| `F01-06d` | `not_run` | `—` | `—` | `—` | `—` | `—` |
| `F01-06e` | `not_run` | `—` | `—` | `—` | `—` | `—` |

## 四、正式 Beta 放行结论

**不予放行**。阻塞项逐条如下（每条都带解除条件）：

| # | 阻塞项 | 事实 | 解除条件 |
|---|---|---|---|
| 1 | 人工判定未完成 | 4 条契约散文断言尚无人工判定（其中 4 条未判定） | 用 #633 的录入通道写一条判定（绑定本批次 run_id ＋ 逐字引文），并由服务端落审计记录使其可核验；权威性核验口径见跟进票 |
| 2 | 提供方语义未核验 | provider_semantics_status = 'not_verified' | 散文断言全部被权威判定为 passed 且机器断言全过后才会变为 verified |

> **口径提醒**：机器断言全过 **不等于** Beta 放行通过；放行判定按验收协议 §6，主流程不得含 FAIL / BLOCKED / NOT RUN / DEFERRED_MANUAL。`DEFERRED_MANUAL` 不阻塞代码合并，但**阻塞** Beta 首版放行。

### 19.9 本轮之后仍未解除的事项（逐条带解除条件）

| # | 事项 | 性质 | 解除条件 |
|---|---|---|---|
| 1 | 4 条契约散文断言（F01-06b～06e）的人工判定 | **人工前置**（#633 的录入通道已就位；缺的是判定本身 ＋ 服务端可核验的审计记录） | 跟进票 **#649**：服务端补一类「人工判定」审计记录后，写 4 条判定（绑定本批次 `run_id` ＋ 逐字引文）并在**同一批次**上重跑（生成节点带固定 `Idempotency-Key` ⇒ **重放、不产生新的 provider 请求**） |
| 2 | Word 人工检查（开/改/存/关/重开） | **DEFERRED_MANUAL** | 本轮**已产出**可检查的 DOCX（导出产物 `export-196b61e0de1242e6247ddb6bf2e91747`，status = `verified`，`file_sha256 = df5549b044261b6c30463e63cd4e639f0e7f59d116e27e6e429e571a3865639a`，快照 `snapshot-09c6230b71d5024ed19e155e840f8635`；跑批器下载并校验了它但**未落盘**）⇒ 用 `GET /projects/{projectId}/exports/{exportId}` 下载到盘上，用真实 Microsoft Word 操作后填回 `acceptance/beta-first/<sha>/<run>/word-check.md` 的五个必填字段（跟进票 **#480**） |
| 3 | 第二人独立复现 | **记录项**（不阻塞首版） | 由第二名开发者按 #489 的步骤独立复现并把证据绑到当前 head（跟进票 **#489**） |
| 4 | 隔离 PostgreSQL 的「重启后读回」 | **NOT RUN**（本轮用隔离 SQLite） | 用 `--db-driver postgres --db-dsn <隔离库 DSN>` 跑重启读回（跟进票 **#480**） |
| 5 | 向量开启的知识库上的检索行为 | **未授权**（embedding 授权为 0 次） | 把 embedding 纳入受控 profile 并拆分 chat / embedding 额度（跟进票 **#546**） |
| 6 | #619 服务端的三项残留（声明口径、三条承重测试、API 密钥主体授权边界、存量变更集） | **已登记** | 跟进票 **#647** |
## 20. 第十二批（第七轮同步）：#649 服务端权威人工判定落地 ＋ 判定层自证补强 ＋ 一处契约缺陷收口

### 20.1 本批合入的实现（逐项：票 → PR → 合并提交 → 它修的是什么）

| 票 | PR | 合并提交 | 它修的是什么 |
|---|---|---|---|
| #651 | **#652** | `d4cc1b09` | 生成运行的**幂等重放在生产装配下必然 503**（`validateReplayInput` 是唯一仍硬要求 `s.Currentness != nil` 的地方，而生产走 `NewServiceWithAccessPolicy` 设 `Access` 不设 `Currentness`）。同时补上**复核点名的 5 处钉子**（M4 终局集合的 failed/interrupted 两支、M6 重放路径的来源校验、M7 存储输入的证据校验、HTTP 接缝用例、「不写候选」断言）—— 这 5 处在补测前**删掉仍全仓全绿**。 |
| #647 | **#653** | `ef0fbdd0` | #619 的三条**承重属性**与稳定化授权边界的仓内自带测试（7 条包级 ＋ 3 条 HTTP 级）。**判定式一行未改**。 |
| #655 | **#658** | `b1c8b272` | 来源漂移的重放被答成 **500 `internal_error`（retryable=true）** ⇒ 改为 **409 `source_stale`（retryable=false）**。500 的承诺把调用方引向重试，而重试救不了漂移（捕获时的那份快照不会自己变回去）—— 在额度只剩 1 次的窗口里，这是**会烧额度的方向**。 |
| #633 | **#654** | `5ed7ae06` | 跑批器的**零额度重判路径**：只重算人工判定层，机器事实**逐字节保留**、`run_id` 不变、语义判据**复用跑批器那一条**（不写第二份）。 |
| #654 | **#657** | `fd55737e` | 登记处补记 #654 的三条新路径到 PF-14。 |
| #649 | **#659** | `540f77e4` | **服务端落下权威的人工判定审计记录**（领域层）：`actor`/`authorization`/`channel`/`occurred_at`/`audit_id` **全部由服务端推导**（入参只有 5 个调用方字段 ⇒ 这 5 个派生事实**物理上无处可填**）。审计行落 `lingdoc_project_audits`（action = `manual.judgement`，`details` **顶层扁平**）；判定结果落 `lingdoc_g5_records`（kind = `manual_judgement`，id = `sha256(run_id + "\x00" + assertion_id)`）；**同一写事务**，先判定结果后审计行，任一失败整体回滚；**不需要迁移**。 |
| #649 | **#660** | （见 20.2） | HTTP 接缝（两个端点 ＋ 窄端口）、组合根显式交接、契约、跑批器读端认得服务端信封。 |
| #649 | 登记处补记 | （见 20.2） | 9 条路径的归属（6 条新增 ＋ 4 条跨面单向移交的记录）。 |

### 20.2 #649：人工判定为什么此前**永远**不可能被核验为权威

跑批器的核验判据是**逐字段相等**（`scripts/lingdoc_mock/manual_decision_audit.py` 的 `verify_against_audit`）：审计源里要有一条以 `audit_id` 为键、**顶层**带齐 7 个字段（`actor`/`authorization`/`channel`/`occurred_at`/`assertion_id`/`run_id`/`evidence_ref`）的记录，且每个字段与判定记录**完全相等**。

⇒ **服务端此前没有一类记录能提供这 7 个字段** ⇒ 判定只能停在 `claimed` ⇒ `provider_semantics_status` 恒 `not_verified` ⇒ 两条放行路径都判 NOT RUN。**这就是 19.8 里那两个阻塞项的根因。**

本批把它拆成两条线落地（领域层 ＋ HTTP 侧），并由**管理会话裁定了 5 项口径**（记录如下，避免后续再翻旧账）：

| # | 裁定 | 依据 |
|---|---|---|
| 1 | 导出记录里 flat `channel` = **`unverified_channel`** | 服务端能证明的只有**凭据类别**；UI 点击与脚本重放**同属 `user_session` 凭据** ⇒ 结构上无法区分 ⇒ 按「不可证明的一律 `unprovable`」⇒ **宁可低估不可高估**。 |
| 2 | `occurred_at` 必须**由服务端存字符串、导出时原样回吐** | 若走「DB 里的 `time.Time` 再格式化」，精度/时区会在往返里漂，**字节相等就断了**。 |
| 3 | 渠道两层语义放**附加键**（`channel_class`/`channel_ui_origin`/`channel_automation`） | 跑批器只比 7 个具名字段、忽略额外键 ⇒ 附加键**不影响**相等判定；后两者**恒为 `unprovable`**。 |
| 4 | 路由注册接缝 = `internal/router/router.go`（**无主**路径），**不改** `http.go` | `http.go` 属 PF-2 且会破两条路由总数断言；`router.go` 不在任何面的路径集里 ⇒ 改它不越界。 |
| 5 | 跑批器读端**允许拆信封**，但**只准拆** | 导出端点返回信封而 `_read_records` 只吃数组/JSONL ⇒ 不拆则判定**静默**停在 `claimed`。**判定语义（`verify_against_audit`/`audit_index`/`resolve_authority`/`AUDIT_FIELDS`）一字未改**，且补了守护：拆信封后**仍缺字段/值不等仍不成立**。 |

**跨语言端到端已跑通**（真实服务端导出信封 → 真实跑批器）：

```
records the runner read: 1 | audit_index size: 1
verify_against_audit: True      authority: server_attested      decision_status: passed
export quote == write-side quote: True
ingest: server_attested passed verified
```

⇒ **19.9 的第 1 条（人工判定未完成）的「服务端可核验的审计记录」这一半已解除**；剩下的仍是**判定本身**（人工前置，见 20.5）。

### 20.3 判定层的三处补强（#652 的 5 处钉子 ＋ #653 的三条承重属性 ＋ #655 的错误形态）

- **#652**：复核在原 head 上发现 M4/M6/M7 三处**删掉仍全仓全绿**（重放路径**没有第二道来源防线**）⇒ 补测后每处都有会转红的用例。**管理会话独立重做**两处变异：删来源校验 ⇒ **恰好** `TestReplayStillValidatesTheSources` 转红（其余 9 条仍绿）；删证据校验 ⇒ **恰好** `TestReplayStillValidatesTheStoredEvidence` 转红；文件逐字节还原、工作树干净。
- **#653**：9 处独立反向变异，8 处决定性转红；**MUT3/MUT5/MUT9 在 merge-base `db582027` 与 HEAD 两端全部复现** ⇒ 这三条性质**并入前确实未被钉住**。另有一处**存活变异体**（业务键的 `run_id` 维度零覆盖）⇒ 已补测试并由管理会话独立重做该变异，**恰好** `TestManualJudgementKeepsOneAssertionOfTwoBatchesApart` 转红。
- **#658**：管理会话独立重做三处变异（去 Form / `Retryable:true` / `Status:500`）⇒ 全部让三条 HTTP 级用例转红；契约与实现**机械交叉核对 4/4 全匹配**。

### 20.4 A-2 负向验证：F01-06c 现在有了**负向**证据（结论 PASS）

F01-06c 原文：「变更集评估后才允许应用；服务端的合法前态只有 assessed。」此前只有**正向**证据（审计序列里 `change_set.assess` 先于 `project.change_set.apply`）。A-2 用**唯一一次**负向 apply 补上了负向证据：

- 前置状态（psql 直读）：变更集 `status='draft'`、`applied_at` 空；`g3_records` 里 `kind='assessment'` **0 条**；`status='applied'` **0 行**；无 apply 审计；无 `applyChangeSet` 幂等行；服务端 `GET …/impact-assessment` **自身返回 404**。
- 负向 apply：**HTTP 404 `not_found`**（`retryable=false`）。
- 归因：`service_facade.go:2144` → `typed_contracts.go:624-633` → `impact_source_operation.go:169` → `:389-392`（取评估记录失败时**原样透传** `ErrNotFound`）；服务端 SQL 日志时序佐证（`lingdoc_g3_records kind='assessment'` → record not found → 404），且 `lingdoc_change_sets` 全程无 not-found ⇒ 变更集**被找到了**。
- 阳性对照（同二进制／同库／同端口／同权限，唯一差异＝**评估记录的有无**）⇒ **HTTP 200**、`status=applied`。
- 副作用：44 张 `lingdoc_%` 表 ＋ `models` 逐表行数与内容指纹前后对比 ⇒ 除 **1 条 `authorization.manage` 审计**外**零差异**。
- 成本：`LLM_DEBUG_LOG` 0 文件、`models` 0 行、账本 0 行 ⇒ **provider 请求 0 次、额度消耗 0**。

**两项口径缺口（交票主裁决，本轮不代判）**：

| # | 缺口 | 事实 | 处置 |
|---|---|---|---|
| L1 | 拒绝是 **404 `not_found`** 而非状态机对应的 **422 `invalid_state`** | 调用方**无法区分**「变更集不存在」与「变更集未评估」⇒ 该门禁在**可观测口径上偏弱**（不影响「不能被应用」这一事实） | **已开独立票 #656**（含最小复现、根因行号、5 条验收矩阵） |
| L2 | 「合法前态只有 `assessed`」对应的状态机检查（`ChangeSetStatusCanMove("apply", status)`）在 HTTP 路径上**未被执行到** | 拒绝发生在更早的来源解码阶段 ⇒ 该后半句目前只有**静态**证据（契约表、DB CHECK 约束、既有测试），**未被本次实验直接实测** | 如实标注；补它需要先修 L1（见 #656） |

**F01-06c 的读法（供票主裁决）**：读作「**未评估的变更集不得被应用**」⇒ A-2 **支持**，可判 `passed`；读作「**服务端以状态非法（422 `invalid_state`）拒绝，且合法前态集合恰为 `{assessed}`**」⇒ A-2 **仅部分支持**（拒绝确实发生且归因确凿，但错误码是 404，且状态机检查从未被执行）⇒ 该读法下应记 **PARTIAL**。**本轮未改动任何人工判定记录。**

证据目录：`D:/wt/lingdoc-485-evidence/a2-negative/`（索引 `REPORT.md`；15 份 JSON/日志/脚本带 sha256）。

### 20.5 本批之后仍未解除的事项（逐条带解除条件）

> **本节的状态词以本节为准**：§6「人工前置 6 项（当前状态）」中**本批动过的三行**（第 2 行重启读回、第 4 行 Word、第 6 行第二人独立复现）已同步到本批现状；**第 1 行**（Qwen 真实模型调用）仍以 **#485 那一轮**的事实为主体（其最新一轮事实见 §20.6 与 §20.11 第 6 项，已在该行就地标注）；**第 3、5 行**保留 **#485 那一轮**的快照（未就地标注最新一轮）。§19.9 是**第六轮的历史记录**，其中 Word 行描述的是**当时**的状态（「还没有 DOCX 可查」）⇒ **一律不改写历史** —— 现状读 §20.5 与 §20.11。

| # | 事项 | 性质 | 解除条件 |
|---|---|---|---|
| 1 | 4 条契约散文断言（F01-06b～06e）的**人工判定** | **已解除**（本批完成） | **已由票主本人作出判定并经正式服务端接口落审计记录**（4×201，`audit_id` 见 §20.7；`authority=server_attested`），再经 **#654 的零额度重判路径**接入既有跑批报告 ⇒ 两条放行路径**实际**判 PASS（§20.6）。⚠️ 剩余的**工程**约束：额度已 4/4 用尽 ⇒ **不能**再带新判定重跑 F01 主流程，只能走零额度重判（已如此做）。⚠️ **F01-06c 的 422 行为仍未验证**（见第 6 条与 #656）。 |
| 2 | Word 人工检查（开/改/存/关/重开） | **DEFERRED_MANUAL**（**协议 §4 的机械动作与结构完整性已全部满足并 PASS，见 §20.9**；剩两项人眼确认） | **解除条件**：负责人打开修改后的文件 `export-196b61e0….word-modified.docx`（sha256 `3e336eeb…`）确认「基本格式仍可用」，并给出「3 条待核项保留理由表述是否恰当」的结论，写回 `word-check.md`（跟进票 **#480**）。⚠️ 「DOCX 生成成功」**不等于**「Word 检查通过」；「机械动作已做」也**不等于**「人眼确认已做」。 |
| 3 | 第二人独立复现 | **记录项**（不阻塞首版） | 由**第二名真人**开发者按 #489 的步骤独立复现（干净工作树 ＋ 新身份 ＋ 新隔离数据库 ＋ 独立报告）并把证据绑到当前 head。⚠️ AI 会话**不计入**「另一位开发者」。 |
| 4 | 隔离 PostgreSQL 的「重启后读回」 | **已解除**（本批前完成） | 已用隔离 PG 库 `weknora_iso_r7` ＋ 真实进程重启完成：项目/章节/来源绑定/下载哈希**逐字段相等**（A 轮 31 叶子、B 轮 36 叶子，**0 处不等**）；psql 直读证明行物理在 PG 内；**零 provider**。证据 `D:/wt/lingdoc-484-evidence/pg-r7/`。⚠️ **章节级 `source_ids` 非空仍 NOT RUN**（需真实 WeKnora 解析 ⇒ 依赖 embedding 授权）。 |
| 5 | 向量开启的知识库上的检索行为 | **未授权**（embedding 授权为 0 次） | 把 embedding 纳入受控 profile 并拆分 chat / embedding 额度（跟进票 **#546**；等待票主批复范围：模型、预计调用数、费用、与 chat 分开记账、材料、验证方法、风险）。 |
| 6 | F01-06c 的**负向拒绝形态** | **已登记**（票 #656） | apply 未评估的变更集现在答 **404 `not_found`** 而非 422 `invalid_state` ⇒ 调用方分不清「不存在」与「未评估」。修法：把「评估记录不存在」从 `ErrNotFound` 改成可区分的领域错误，并让状态机检查在来源解码**之前**执行。 |
| 7 | #619 服务端的三项残留 | **已登记** | 跟进票 **#647**：① 声明口径；② 三条承重测试（**本批 #653 已补上**）；③ API 密钥主体授权边界（**已登记为已知承重耦合**：Owner 判定必须读项目成员记录里的 owner 身份，不得改读 `Actor.Role`）；④ 存量变更集（**不迁移不回填**）。 |

### 20.6 四层结论的更新（相对 19.8）

**相对第六轮，本批**：

- **第一层（机器执行事实）**：**未变** —— 最近一次真实跑批仍是 `f01-20261025T013341Z-5105ae32`（31/31 节点、64/64 机器断言）。本批**未发起任何真实 provider 调用**（额度 4/4 用尽）。
- **第二层（契约验收结果）**：**已由 NOT RUN 变为 PASS**。用 #654 的**零额度重判路径**把四条人工判定接入既有跑批报告后，两条判定路径**实际**判 PASS（`main_path_evidence` = PASS、`beta_evidence` = PASS，`provider_semantics_status` = `verified`；outcome 数 105 → 97，8 条 `not_run` 全部消除）。**机器事实逐字节不变**（9 个机器事实键逐字节相等，顶层键集与顺序一致），`run_id` 不变，**新增 provider 请求 = 0、新增账本行 = 0**。
- **第三层（人工判定结果）**：4 条**已由 `not_run` 变为 `passed`**，且**全部取得服务端权威审计证明**（`authority=server_attested`、`channel_verifiability=verified`）。四条各自的 `audit_id`、`actor`、`authorization`、`occurred_at`、`evidence_ref` 见 §20.7。⚠️ **渠道本身仍不可证明**：服务端能证明的只有凭据类别（`user_session`），**不能**证明「本人现场操作」⇒ `channel` 恒为 `unverified_channel`、`ui_origin`/`automation` 恒 `unprovable`。
- **第四层（正式 Beta 放行结论）**：**仍不予放行**，但阻塞项已从四项减到两项。**① Word 人工检查（`DEFERRED_MANUAL`）** —— 协议 §4 的机械动作与结构完整性已 PASS（§20.9），**两项人眼确认尚未做出**；**② 真实依赖均已验证**（`NOT RUN`）—— 向量检索行为与章节级 `source_ids`，依赖 #546 的 embedding 授权。⚠️ **本节（第 20.6）与 §20.11 的口径差异已对齐**：**浏览器检查已在本轮由 `NOT RUN` 变为 `PASS`**（§20.12），**第二人独立复现已由负责人豁免**（`WAIVED_BY_OWNER`，§20.10）⇒ 这两项**都不再是**剩余放行条件。

> **口径提醒（不变）**：机器断言全过 **不等于** Beta 放行通过；放行判定按验收协议 §6，主流程不得含 FAIL / BLOCKED / NOT RUN / DEFERRED_MANUAL。`DEFERRED_MANUAL` 不阻塞代码合并，但**阻塞** Beta 首版放行。
> **本批新增的一条口径**：`#649` 落地后，**「判定能否被核验为权威」已不再是阻塞项**；**「有没有判定」才是**。这两件事在 19.8 里是同一条阻塞项（#1），现在必须分开读。
> **再一条**（本批新事）：**四条判定被记录 ＋ 契约验收两条路径 PASS ≠ 整个 Beta 自动放行**。放行结论仍按协议 §6 逐条判。

### 20.7 四条人工判定的服务端审计记录（**权威证明已取得**）

执行方式：**正式服务端 HTTP 接口**（`POST /api/v1/lingdoc/projects/{projectId}/manual-judgements`），不是写库、不是手工插审计行。判定服务跑在**独立端口 18095**（二进制 source_head `ac6ee9a5`，含 #659 领域层 ＋ #660 HTTP 侧）；**18087 上那轮服务全程未停未动**。

| 断言 | verdict | `audit_id` | `actor` | `channel` | `occurred_at` | `evidence_ref` |
|---|---|---|---|---|---|---|
| `F01-06b` | `passed` | `9bdeffbc-aff5-4d64-a4f2-4460da4b645f` | `619a1613…`（认证到的 owner） | `unverified_channel` | `2026-10-25T09:10:49Z` | `run:<run_id>/human-action-sheet.md#A-1` |
| `F01-06c` | `passed` | `9d180db7-7bcc-4fa2-98b3-a8e3bf8b92c5` | 同上 | 同上 | `2026-10-25T09:13:01Z` | `run:<run_id>/a2-negative/REPORT.md` |
| `F01-06d` | `passed` | `5e6c2b90-c06b-4ea5-8043-70b03dda4d6f` | 同上 | 同上 | `2026-10-25T09:13:01Z` | `run:<run_id>/human-action-sheet.md#A-3` |
| `F01-06e` | `passed` | `c6d9afdc-32d7-4786-9132-2f04bdb8b0c2` | 同上 | 同上 | `2026-10-25T09:13:01Z` | `run:<run_id>/human-action-sheet.md#A-4` |

- **授权事实**（服务端推导，四条相同）：`method=project_membership_capability_and_owner_check;capability=manage;project_owner=true;human_decision_gate=passed_not_a_machine_principal` ⇒ 读的是**项目成员记录**里的 owner 身份，**不是** `Actor.Role`。
- **判定来源**：`verdict_source = human_judgement`（**人工**，不是机器求值升级）。
- **逐字段读回**：每条 15 项检查全过（verdict／契约原文逐字／run_id／evidence_ref／actor／channel 三项／occurred_at／audit_id／authorization 两项／7 字段顶层／恰 12 键）；**审计行 details 与判定记录 payload 的 7 个字段逐字段相等**（28/28 项）。
- **F01-06e 的原始 HTTP 响应体已补证**：从**最终批次服务端访问日志第 429 行**取得 `GET …/assets/cac1fd1f…/policy` 的**响应原文**（`status_code=200`，body sha256 `d19ff67c…`；12 项字段检查全过，`model_use_allowed=true`、`asset_revision=2`、`source_version_hash=33998f49…`），**不是**从数据库重建的。

⚠️ **保留的证据限制（未淡化）**：① **F01-06c 的状态机合法前态与 422 行为仍未直接验证** —— A-2 的负向拒绝发生在**来源解码阶段**，错误码是 **404 `not_found`** 而非 422 `invalid_state`（调用方分不清「不存在」与「未评估」）⇒ **已开票 #656**；② **F01-06d 没有机器 checks**，其 `content_hash` 两版**相同**，且**不替代** F01-06e 的指纹绑定核验；③ **F01-06e 的理由必须保留为「同意」**，不得改写成用户曾逐项表达的理由；④ 服务端**不能**校验 `assertion_id` 是否是契约散文节点（契约是 docs 产物），逐字核验由跑批器按 #633 承担。

### 20.8 本批留下的一处覆盖缺口（**已闭合**）

#660 的独立复核发现：**真实 router 的注册分支没有回归测试** —— 把 `internal/router/router.go:331` 的注册分支废掉后，PR 自带的 `router` ＋ `container` 两个套件**仍全绿**（退出码 0），而生产 router 已不再服务这两个端点。复核用**自己写的**测试证明了生产接线是对的（真实 router ＋ 真实组合根 ⇒ 201/200）。

⇒ **PR #664 已补上这条守卫并合入 main**（合并提交 `7612f92c`）：新增 `internal/router/lingdoc_manual_judgement_registration_test.go`（**只新增**，零既有文件改动），两条用例都经 **`router.NewRouter` ＋ dig 真实装配**（不再直接调 `workspace.RegisterManualJudgementRoutes`）⇒ 端口存在 ⇒ 两条生产路径在 `engine.Routes()` 里；端口为 nil ⇒ 不在。既有路由总数断言 **79 / 54** 均未变。

独立复核 **PASS**：4 处独立变异全部干净转红（M1 废掉注册分支 ⇒ `TestNewRouterServesTheManualJudgementRoutesWhenThePortIsComposed` 唯一转红；M2 去掉 `optional:"true"` ⇒ `TestNewRouterOmitsTheManualJudgementRoutesWhenThePortIsAbsent` 唯一转红；M3 路径字面量改一字符、M4 `POST`→`PUT` ⇒ 两条同时转红），逐字节还原、sha256 与基线一致、工作树干净。门禁 **21 行 0 fail / 0 pending**（`LingDoc PR gate` pass 1m38s、`integration` pass 23m2s）。

### 20.9 Word 人工检查（协议 §4）：**已用真实的 Microsoft Word 执行**

| 项 | 值 |
| --- | --- |
| 被检查的文件 | `export-196b61e0de1242e6247ddb6bf2e91747.docx`（3176 字节） |
| 原始 SHA-256 | `df5549b044261b6c30463e63cd4e639f0e7f59d116e27e6e429e571a3865639a`（与本轮 F01-23 下载产物的 `file_sha256` 一致） |
| 修改后文件 SHA-256 | `3e336eeb73254690b2c7d4301a022f703fe5fdeb4a7f165a79d9a87e0070982c`（16138 字节，由 **Word 自己**保存产生） |
| Word 产品版本 | **Microsoft Word 16.0（build 16.0.20430）** |
| Windows 版本 | 10.0.26200 |
| 机器校验 | `beta_evidence.word_check(...)` ⇒ **status = `held`**（`held` ＝ 这条期望成立；`overall_result` 的阻塞集合是 `{failed, blocked, not_run}`，不含 `held`） |

> ⚠️ **机器层的 `held` 与放行口径的状态词不在同一层**：机器层（`beta_evidence.word_check`）只回答「五个必填字段**是否存在且非占位**、结论词**是否不含「不通过」**」⇒ `held`；⚠️ 它**不比较**表单里填的 SHA-256 与文档实际哈希（代码注释就写着「the executor checks the fields, never the document's content」）——「两份 DOCX 哈希与表单所填是否相等」是**本轮另行核对**的事实，见本节表格的 SHA-256 行。**放行判定按协议 §6 逐项核对**，而协议 §5 规定「人工前置项尚未完成就是 `DEFERRED_MANUAL`，**不是** `PASS`」⇒ 本项在 §6 门槛表里的状态词是 **`DEFERRED_MANUAL`**（**验收协议 §6** 第 5 项，见 §20.11），**仍然阻塞放行**。**机器层不阻塞 ≠ 放行层不阻塞。**

**执行的正是协议 §4 的整段动作**（**真实 Microsoft Word 的 COM 自动化，不是程序库 roundtrip**）：打开下载的文件 → 修改**第 6 段正文**（`正文` 样式：`使用合成记录检验软件流程，结果仅用于内部演示。` → 追加「（Word 人工检查：已修改此处正文。）」）→ `SaveAs2` **另存为新文件** → 关闭整个 Word 实例 → **重新起一个 Word 实例重新打开** → 逐项检查。

| # | 检查项 | 结果 |
| --- | --- | --- |
| 1 | 修改保留（关掉 Word 后重新打开仍在） | **通过** |
| 2 | 逐段对比：12 段**恰好 1 段不同**（被改的那段），其余 11 段逐字相同 | **通过** |
| 3 | 标题层级不变（`标题` ×1、`副标题` ×1、`标题 1` ×4 仍在原位；大纲级别 10/1 未变） | **通过** |
| 4 | 引用仍在：`[1] synth-chapter-c.md`、`定位：synth-chapter-c.md · 第 0 段`、引用的原文 | **通过** |
| 5 | 正文里的行内引用标记 `包含100条虚构记录 [1]。` 仍在 | **通过** |
| 6 | 附录仍列出 **3 条**待核项，各带「处理：保留警示」「理由：仅作内部演示，保留该项并随文件明确列出」 | **通过** |
| 7 | 内部演示标识仍在：副标题 `仅供内部演示；并非正式申报材料` ＋ 正文 `不把它视为真实科研结果` | **通过** |
| 8 | 文档仍可正常渲染：页数 1、词数 280（275 ＋ 插入的 5 个词） | **通过** |

**原始文件逐字节未变**（检查前后 sha256 相同）—— 修改只落在另存的新文件上。

⚠️ **两项人眼确认尚未做出**（如实标注，**不**记为已通过）：**版面观感**（行距、页边距、字体在屏幕上看起来是否可接受）与**「3 条待核项的保留理由表述是否恰当」**。这两项本质上是人的观察，脚本只能证明结构没坏、证明不了观感。⇒ 记录：**机械性动作与结构完整性全部成立；人眼确认待负责人完成**。

证据：`D:\wt\lingdoc-485-evidence\track-d-experiment\run-final-20261025-093259-c2345229\word-check.md`。

### 20.10 #489 第二人独立复现：**`WAIVED_BY_OWNER`**（事实状态仍 `NOT_RUN`）

**项目负责人决定**：本次 Beta 首版收尾**不再执行**第二人独立复现，**不再等待**真人复现。

| 字段 | 值 |
| --- | --- |
| **豁免标记** | **`WAIVED_BY_OWNER`** —— 由项目负责人明确决定跳过本次独立复现 |
| **事实状态** | **`NOT_RUN`** —— 独立复现**尚未执行** |

⚠️ **没有**伪造任何复现记录、复现证据或 `PASS` 结果；**本票的 4 条验收标准全部保持未勾选**。`NOT_RUN` 与 `WAIVED_BY_OWNER` 是**两件事**：前者说「没做」，后者说「不再要求做」。

**放行判定机制未做任何改动** —— 本项**本来就不在** Beta 首版的放行判定里：验收协议 §1「**一位执行者**完成整套验收，不要求第二位开发者独立复现」；§6 的总门槛逐项列举为「F01 原 23 个业务步骤及当前必需前置节点、业务语义/读回断言、**浏览器检查**、**重启恢复**和 **Word 检查**」（§6 原文括注为「即契约当前的 24 个节点」；**§3 的口径为 31 个节点**，§20.11 的表格按 31 记）—— **不含**第二人独立复现；机器侧 `beta_evidence.overall_result` 只看 F01 契约的 outcomes ＋ `provider_semantics_status`，`manual_decision_audit.AUDIT_FIELDS` 里也没有这一项 ⇒ 本项**不进**任何放行判定的输入。**本票没有任何「阻塞条件」需要被移除** —— 它自建票起就是「记录项，不阻塞首版」。

**残余风险（如实披露）**：① **单点执行者风险** —— 本次放行**没有**「第二位开发者用独立环境独立复现」的证据背书，任何执行错误只会被同一人发现；② 豁免的是「本次 Beta」，若日后要恢复该项的证据效力，仍需**真实执行**一次，**本次豁免不能当作那次执行的替代**，也**不能**被引用为「已复现」；③ 本豁免**仅适用于 #489**，**不连带**任何其它项。

票面记录：https://github.com/magician336/lingdoc/issues/489#issuecomment-6396424884

### 20.11 按协议 §6 逐项核对的最终 Beta 放行结论

协议 §6 的总门槛原文：「**只有一位执行者**完成 F01 原23个业务步骤及当前必需前置节点（即契约当前的 24 个节点）、业务语义/读回断言、浏览器检查、重启恢复和 Word 检查，且真实依赖均已验证，才标记 Beta 首版通过。**主流程不能含 `FAIL`、`BLOCKED`、`NOT RUN` 或 `DEFERRED_MANUAL`。**」

| # | §6 门槛项 | 本轮状态 | 证据 |
| --- | --- | --- | --- |
| 1 | F01 原 23 个业务步骤 ＋ 当前必需前置节点（契约当前 **31** 个节点） | **PASS** | 31/31 节点执行，HTTP 状态与契约逐条一致；`f01-report.json` |
| 2 | 业务语义/读回断言 | **PASS** | 64/64 机器断言（`checks`）；**外加**四条散文断言由服务端权威审计证明 ⇒ `provider_semantics_status` = `verified`（§20.7） |
| 3 | **浏览器检查**（协议 §2 第 4 步） | **PASS**（**PASS 覆盖三方一致性 10 项**；§2 第 4 步的「**候选取消**」交互面本轮未执行，见 §20.12 的覆盖边界） | §20.12：**真实浏览器 ＝ HTTP ＝ 刷新后读回，10 项逐项一致、0 处不符**（`parity.json` → `status = PASS`、`consistent = true`、`checked = 10`、`mismatches = []`）；真实前端（Vite dev server ＋ headless Chrome ＋ 真实登录表单）对**最终 F01 项目**渲染出交付快照，摘要 `df5549b0…` 就是 Word 检查所用那一份。**零 provider、零库改动**（前后状态文件 sha256 相同） |
| 4 | **重启恢复** | **PASS** | 隔离 PG 库 `weknora_iso_r7` ＋ 真实进程重启：项目/章节/来源绑定/下载哈希**逐字段相等**（A 轮 31 叶子、B 轮 36 叶子，**0 处不等**）；psql 直读证明行物理在 PG 内；**零 provider** |
| 5 | **Word 检查** | **`DEFERRED_MANUAL`**（协议 §4 的**机械动作与结构完整性已全部满足并 PASS**；**两项人眼确认尚未做出**） | §20.9：真实 Microsoft Word 16.0 打开→改一处正文→另存→关闭→重开→逐项检查，**12 项全过**（§20.9 摘录其中 8 项，完整 12 项见 `word-check.md`）；原始文件逐字节未变。**解除条件**：负责人打开修改后的文件确认「基本格式仍可用」，并给出「3 条待核项保留理由表述是否恰当」的结论，写回 `word-check.md`（跟进票 **#480**） |
| 6 | 真实依赖均已验证 | **`NOT RUN`**（**部分闭合**：章节级 `source_ids` 与**词法**检索命中已**直接核实**；**向量检索行为仍未验证**） | §20.13：**授权已批并已执行**（2026-10-25）。① **章节级 `source_ids` 逐条直接核实 PASS**（8/8）：最终项目两个当前版本的 `source_ids` 逐条解析到 chunk ⇒ 落在目标 KB `7c104e1b…` 的目标材料内，**坐标档引文与材料原文逐字一致**（坐标系＝**字符偏移**，不是字节偏移），正文 `citation_usages_json` 与版本记录一致（`embedding-run/source_ids-verification.json`）。② **词法检索命中 PASS**（10/10）：目标 KB 2 条查询各命中 2 个分块、全部属目标材料，**chat 两本账本 sha256 检索前后逐字节相同 ⇒ 0 次模型调用**（`embedding-run/retrieval-source-consistency.json`）。③ **向量检索行为仍未验证**：口径裁定已落地（PR #668 新 priced embedding profile，合并提交 `79613798`），真实 provider 请求 **1 次**即被 provider **403 `insufficient_quota`**（账号「仅免费层」额度耗尽）拒绝 ⇒ **0 向量**、F01-07 仍 `NOT RUN`；账本 **1 行**且 `blocked=true`（一次 4xx 即冻结，且该 profile **无核对/解冻通道** ⇒ 票 **#671**）。④ 另立票 **#670**：前两轮「0 次出网」的根因是 **asynq 队列跨实例无 namespace 隔离**，任务被 chat profile 的实例取走 |
| — | 人工判定（四条散文断言） | **PASS**（已取得服务端权威审计证明） | §20.7：4×201、`authority=server_attested`、`channel_verifiability=verified` |
| — | 第二人独立复现（**非 §6 门槛项**） | **`WAIVED_BY_OWNER`** ／ 事实 `NOT_RUN` | §20.10：负责人豁免；**不连带**任何其它项 |

**最终结论：Beta 首版【仍不予放行】。**

- **契约验收两条路径已实际判 PASS**（`main_path_evidence` = PASS、`beta_evidence` = PASS、`provider_semantics_status` = `verified`）—— 这是系统自身的规则跑出来的，**不是**人工宣称的。
- **第 3 项（浏览器检查）本轮已从 `NOT RUN` 变为 `PASS`**（§20.12）⇒ §6 六项门槛里现在还剩两项未闭合：**第 5 项（Word 检查）＝ `DEFERRED_MANUAL`**（协议 §4 的机械动作与结构完整性已 PASS，但**两项人眼确认尚未做出**）与**第 6 项（真实依赖均已验证）＝ `NOT RUN`**（章节级 `source_ids` 与**词法**检索命中已直接核实，**向量检索仍未验证**，§20.13） ⇒ 主流程仍含 `DEFERRED_MANUAL` 与 `NOT RUN` ⇒ **不满足「主流程不能含 `NOT RUN` / `DEFERRED_MANUAL`」** ⇒ **不予放行**。
- **#489 的豁免不改变这个结论**：它只覆盖「第二人独立复现」这一项，而该项**本来就不在** §6 门槛里。**豁免不连带**embedding 验收与 Word 的人眼确认。
- **解除放行的最小剩余动作**：① **向量检索行为**（embedding 授权**已批并已执行**，§20.13；现在卡在 **provider 侧额度**（403 `insufficient_quota`）＋ **#671**（该 profile 一次 4xx 即冻结且**无解冻通道**）⇒ 需要负责人先解掉额度，并在解冻通道落地后用**新窗口**重跑）；② 完成 Word 检查的两项人眼确认（版面观感、待核项保留理由的表述）。这两项**各自独立**，任何一项未完成都仍不放行。

> **口径提醒（不变）**：机器断言全过 **不等于** Beta 放行通过；放行判定按验收协议 §6 逐项核对，**主流程不得含 FAIL / BLOCKED / NOT RUN / DEFERRED_MANUAL**。

### 20.12 浏览器端到端检查（协议 §2 第 4 步）：**已真跑，三方一致**

**这一项此前一直是 `NOT RUN`**（本轮之前**从未**执行过）：没有真实前端、没有 CDP 浏览器、没有三方比对产物。本轮**实际执行了**，结论 **PASS**。

#### 环境（全部是真实依赖，无 Mock、无假模型）

| 项 | 值 |
| --- | --- |
| 绑定的 head | `7612f92c73649a1f7eeaf025e728de233195e76e`（＝ `origin/main`） |
| 后端 | **真实服务进程** `weknora-trackd-20261025-093259.exe`（pid 59008，source_head `c2345229`），API root `http://127.0.0.1:18087/api/v1/lingdoc`；**18087 全程未停未重启**，18095 也未动 |
| 前端 | **真实 Vite dev server**（`node node_modules/vite/bin/vite.js --port 5173 --strictPort`，worktree `D:\wt\lingdoc-beta-status`），代理目标 `VITE_DEV_PROXY_TARGET=http://127.0.0.1:18087` |
| 浏览器 | **真实 Chrome/Edge（headless=new）＋ CDP**，走**真实登录表单**（邮箱 + 口令）拿到会话 |
| 身份 | 隔离环境 owner `trackd-4ec1487@weknora.local`（tenant 9，role owner）；口令只从仓外凭据文件读入，**全程不打印、不写入证据**；手写文件用完即删（同仓内编排器的处置） |
| 被检查的项目 | `0a6c35d0-9f5d-4a70-b9cf-3887179b8cd0` —— **就是最终 F01 跑批 `f01-20261025T013341Z-5105ae32` 的那个项目**（不是新建的空项目） |

#### 三方比对结果

| 快照 | 时刻（**取自产物里的 `at` 字段**） | 比对项数 |
| --- | --- | --- |
| HTTP 侧 | 2026-10-25T10:34:07Z | 10 |
| 浏览器侧 | 2026-10-25T10:34:17Z | 10 |
| 刷新后读回 | 2026-10-25T10:34:17Z | 10 |

（三方比对本身的结论落在 `parity.json` 的 `at` ＝ `2026-10-25T10:34:18Z`。）

**10 项逐项一致、0 处不符**（`parity.json` → `status = PASS`、`consistent = true`、`checked = 10`、`mismatches = []`）：快照摘要 `1a61a9e98601`、快照 id、课题版本 `9`、研究条件版本 `1`、章节 `2`、资料 `1`、冻结时检查 `passed`、当前检查 `passed`、交付文件 id `export-1`、交付文件摘要 `df5549b04426`。浏览器侧**零 JS 异常**。

- 交付文件摘要 `df5549b04426…` **就是 Word 人工检查所用的那一份**（`export-196b61e0….docx`，sha256 `df5549b044261b6c30463e63cd4e639f0e7f59d116e27e6e429e571a3865639a`）⇒ 浏览器里看到的交付文件与人工打开检查的文件是**同一份**。
- 仓内证据层（`scripts/acceptance/beta_evidence.py`）对这三项的口径是 **HELD**（＝「这一项成立」；`overall_result` 的阻塞集合 `{failed, blocked, not_run}` 不含 `held`）。

#### 零 provider、零副作用（前后对照）

| 项 | 前 | 后 |
| --- | --- | --- |
| `lingdoc_model_call_ledger` 行数 | 4 | **4** |
| LLM 调试目录文件数 | 4 | **4** |
| JSON 账本 `qwen-beta-sqlite.json` sha256 | `74e86abc…` | **`74e86abc…`**（mtime 也未变） |
| JSON 账本 `…json.calls.json` sha256 | `dd39a0cb…` | **`dd39a0cb…`** |
| `lingdoc_release_snapshots` 行数 | 2 | **2** |
| `lingdoc_export_artifacts` 行数 | 1 | **1** |
| 最终项目 `project_version` | 9 | **9** |

⇒ **最强的单条证据**：前后状态文件 `state-before.json` 与 `state-after.json` 的 sha256 **完全相同**（`27df80f11333c286…`）。

#### 哪些动作走 UI、哪些走 API（协议 §2 第 4 步要求注明）

- **走 UI（本轮真实浏览器）**：真实登录表单提交 ⇒ 进入真实前端的灵档工作台 ⇒ 页面真实渲染出该课题（研究条件版本 1 · 课题版本 9 · 课题成员 1 人 · 两章正文）⇒ 打开「交付」面板读取冻结快照/检查结论/交付文件 ⇒ **刷新页面后再读一次**（10 项与刷新前逐字相同）。
- **走 API（F01 跑批内执行，UI 未逐一点击）**：项目与资料（F01-02/06/10）、**候选预览**（F01-09）、**采纳**（F01-11/12）、**章节编辑与确认**（F01-13～17）、**冻结**（F01-19a～20）、**导出与下载**（F01-21～23，DOCX 已下载并做了 Word 人工检查）。
- **未执行**：**候选取消** —— F01 契约里没有取消节点；浏览器对偶把「候选采纳/取消的读回」列为**模型门控**面（54-Spec 决策 9）⇒ 这两条在 `parity.json` 里记 `not_run`，但按执行器自身的设计**不降级**「三方一致性」这一结论项。作为**覆盖边界**如实登记。

#### 本轮的两处执行方式偏离（如实登记）

1. **取令牌用登录而不是注册**：仓内执行器 `beta_http_readback.py` 的 `register_login()` **总是先注册**，对已存在的邮箱答 **400** 并当成致命错误 ⇒ `--email` 的「复用已有账号」路径**实际不可用**；而最终 F01 项目属于一个**已存在**的 owner 账号，新注册账号会落在别的租户、根本读不回该项目。本轮改为用**同一个生产端点** `/api/v1/auth/login` 取令牌，之后**所有比对逻辑仍是执行器自己的未改动代码**。⚠️ 这是**执行器的一处缺陷**，本轮**未修**（本轮不施工），建议另开票。
2. **不跑 `prepare_delivery()`**：F01 已冻结 `snapshot-09c6230b…` 并导出 `export-196b61e0…`（Word 检查过的那一份）。再冻一份会让导出与快照脱钩、并把 F01 快照的 `is_current` 翻掉 ⇒ **削弱**本轮其余证据之间的连贯性。交付检查本身**仍被真实调用**（`read_items()` 会 `POST /projects/{id}/checks`），而该调用是只读的（实测 `project_version` 前后都是 9、结论 `passed`、1 条 warning＝保留待核项）。

#### 证据位置

- 证据文件：`D:\wt\lingdoc-485-evidence\browser-check.md`
- 产物目录：`D:\wt\lingdoc-485-evidence\browser-run-20261025\`（`parity.json`、`http-readback.json`、`browser-readback.json`、`state-before.json`、`state-after.json`、`delivery-panel.png`（真实前端「交付」面板截图，人工可复核）、`run-summary.json`、`evidence-index.json`）
- 复核脚本：`D:\wt\lingdoc-485-evidence\scripts\exp_633_browser_check.py`（幂等）、`exp_636_browser_shot.mjs`
- ⚠️ 产物目录里另有一份 `http-readback.log`，内容是「NOT RUN: HTTP side produced no comparable items (http://127.0.0.1:18087/api/v1/lingdoc)」—— 那是**一次失败的执行**留下的残留日志，**不是**结论那一次（结论那一次的 HTTP 侧走的是内联路径，不写 `.log`）。⚠️ **单凭这一行无法区分失败原因**（仓内执行器在「没产出可比对项」与「RunnerError」两条路径上打印的是同一句；日志里的 base-url 是**正确的** API 根，不能读成「base-url 写错」）。那次失败的真实原因当时写在 `http-readback.json` 的 `not_run[]` 里（`POST /api/v1/auth/register returned 400 (code=1000)`，即下面「执行方式偏离」第 ① 条），而该 json 已被后来成功那一次覆盖。它**零删除**保留在此，仅作痕迹；判定以 `parity.json` 与 `http-readback.json` 为准。
- 跟进票 **#480** 的「浏览器端到端 / 三方一致性实测」缺口，**本项已解除**。

### 20.13 #546 授权 embedding 的执行轮（2026-10-25）：**请求真的出网了，但 provider 403**；并查明了前两轮「0 次出网」的真正根因

**这一轮把 #546 的授权执行掉了**，也第一次真正触到了 provider 边界。结论先说：**向量检索行为仍未验证**（provider 侧额度阻塞）⇒ §6 第 6 项**仍是 `NOT RUN`**；但**章节级 `source_ids` 与词法检索命中已直接核实**（不是推断），并且**查明了此前两轮「0 次出网」的真正根因** —— 它不在模型行、不在端点、也不在 provider 路由上。

#### 口径裁定与实现（已合入 `main`）

- **选「把 embedding 纳入受控」**，但**不改共享计价层**，而是**新开一个 profile**：PR **#668**（合并提交 `79613798`）新增 `lingdoc-qwen-embedding-budget-20261025`（`maxCalls=4`、累计预留上限 `8000`、单次 input ≤`16000`、output ≤`0`、自带计价基准 `CostBasisQwenEmbeddingNanoCNY`、允许模型 `qwen3.7-text-embedding` / `qwen3.7-text-embedding-flash`）。
- 该 profile **必须自带 chat 闸门**（`ProfileApprovedChatModel` ＋ 聊天路径在预留前拒绝）：否则聊天路径会按「profile 批准的那个模型」去预留 embedding 模型、而白名单又放行 ⇒ 真 chat 会出网，并把 embedding 窗口打成 `limit_violation` ＋冻结。既有 profile 的行为**逐字未变**。
- 授权数字**按字面值钉住**（常量 4→5、8000→9000 原本全绿 ⇒ 新增字面值守护用例）。
- **6 轮独立复核**全部收口（12 项发现：1 阻断 ＋ 2 需修正 ＋ 9 提示）；门禁 `LingDoc PR gate` pass、`integration` pass。

#### 此前两轮「embedding 阶段失败、0 次出网」的根因：asynq 队列**跨实例无 namespace 隔离**（新票 **#670**）

| 项 | 实测事实 |
| --- | --- |
| 代码事实 | `internal/router/task.go:72-93` 的 `getAsynqRedisClientOpt()` 只读 `REDIS_ADDR`/`REDIS_PASSWORD`/`REDIS_DB`，**不设 asynq namespace** ⇒ **`REDIS_PREFIX` 隔离不了任务队列** |
| 环境事实 | 18096（embedding profile）与 18087（chat profile）都连 `127.0.0.1:6381` / **Redis DB 1**，只有 key 前缀不同 ⇒ 同一条队列 |
| 现象 | 18096 上 `POST /api/v1/knowledge/:id/reparse` 排的任务**两次都被 18087 取走**：失败原文在 **`server-trackd.stdout.log`**（不在 18096 的日志里）；18096 的 `llm_debug_embedding/` **0 文件**（**该计数截至 21:30 那两轮**；隔离队列后的第三轮在该目录留下了 1 个文件）；embedding 账本（`.calls.json`）**当时不存在** ⇒ **真实 provider 请求 0 次** |
| 错误身份 | `model is outside the acceptance budget profile`＝chat profile 的 `allowedModels` 只有 `qwen3.8-flash` ⇒ embedding 模型在 **transport 之前**被拒（`ledger.go:230` 的 `ProfileAllowsModel`） |
| 阶段记录 | `knowledge_processing_spans`：`embedding` 阶段 `failed`、`error_detail=model is outside the acceptance budget profile`、`duration_ms` ＝ **3042**（21:02 那次）／**3013**（21:30 那次）＝ 5 次尝试之间的 4 次退避（200/400/800/1600 ms）之和 ⇒ 每次都**立即失败** |
| 被排除的方向 | 隔离库的 embedding 模型行**与 chat 行同端点同密文**（`base_url`＝`https://llm-k57eqvdqacn4nov3.cn-beijing.maas.aliyuncs.com/compatible-mode/v1`，`api_key` 密文**逐字节相同**）；profile 白名单与端点白名单**实测全部通过** ⇒ 先前怀疑的「模型行/端点/provider 路由」三个方向均被实测排除 |
| 环境侧处置 | 18096 改用 **Redis DB 2**（实测原为 0 键）⇒ 队列隔离生效（DB 2 里只有它自己的 6 个 worker 注册，DB 1 里没有它）。**18087／18095 全程未停未重启** |

⇒ 该缺陷的真实危害是**跨实例的受控预算会互相串**（任务在错误的 profile 下执行 ⇒ 看起来像产品缺陷，其实是队列串台），且失败信息出现在**错误的服务的日志里** ⇒ 排查方向被带偏。已立票 **#670**（namespace 隔离 / payload 带执行者 / 两者都做，验收要求含「A 排的任务必须由 A 执行、两边账本不得互串」）。

#### 隔离队列后的真实调用：provider **403 `insufficient_quota`**

```
OpenAIEmbedder EmbedBatch API error: Http Status 403 Forbidden, Response Body:
{"error":{"message":"Free quota exhausted. To continue accessing the model on a paid basis, please add funds or disable the \"use free tier only\" mode in the management console.","id":"6d319ec6-…","type":"insufficient_quota","code":"insufficient_quota"}}
```

- provider **凭据有效、端点可达、请求体被接受**；拒绝理由是**账号侧「仅免费层」额度耗尽**（**不是** 401 鉴权失败、**不是** 404 model_not_found、**不是** 400 参数错）。
- **账本行数 ＝ 实际调用数 ＝ 1**：embedding 账本 1 行，`status=http_error`、`input_upper_bound=3085`、`actual_nano_cny=1542500`（＝3085×500 nano-CNY，计价正确）、`cost_basis=qwen_embedding_nano_cny_2026_10_25`。
- 索引器随后的 4 次重试**全部在 transport 之前**被账本挡下（`model acceptance budget requires reconciliation`）⇒ **provider 只收到 1 个请求**，额度没有被继续消耗（这一点是**对的**）。
- **向量没有生成**：`lite_embeddings` 0 行、无 `vec_<dim>` 表、chunk `index_status=failed`、阶段记录 `error_code=VECTORSTORE_WRITE_FAILED`。**没有任何一处显示假成功。**

#### 由此暴露的第二处产品缺口：该 profile **没有核对/解冻通道**（新票 **#671**）

- `Reservation.Unknown("http_error")` ⇒ `ledger.Blocked = true` ⇒ 之后所有 `Reserve` 返回 `ErrBlocked`（`model acceptance budget requires reconciliation`）。
- 唯一能写 `Reconciliation` 的操作者 `ReconcileEmbeddingHTTPError` 的第一道闸门是 `reconcile.go:62` 的 `b.profile != CostOnlyProfile` ⇒ **本 profile 用不了**；`reconcile.go:201` / `ledger.go:644` 也规定只有 `CostOnlyProfile` 的账本才允许带 `Reconciliation`。
- ⇒ 授权给的「≤4 次」在第 1 次撞上 4xx 后**再也用不掉**，且不能正规结清（手改账本文件属于篡改记账，不允许）。

#### 不依赖 embedding 的两项**已直接核实**（PASS，且不是推断）

**（a）章节级 `source_ids` 逐条直接核实 —— 8/8 PASS**（`D:\wt\lingdoc-485-evidence\embedding-run\source_ids-verification.json`）

- 最终项目 `0a6c35d0-9f5d-4a70-b9cf-3887179b8cd0` 的两个**当前版本**：研究问题章 `72d16400…` 声明 `source_ids=["1e849f5f-83e7-4138-a39f-d1aa1928054a"]`；研究方案章 `1a090fd0…` 声明 `[]`（该章正文 23 字、无引用）。
- 逐条解析到 chunk ⇒ 落在**目标 KB `7c104e1b…` 的目标材料** `c1197b70…` 内；**坐标档的引文与材料原文逐字一致**。
- ⚠️ **坐标系是字符（rune）偏移，不是字节偏移**（材料是 UTF-8 中文：2004 字节 / 902 字符；两个 chunk 在**字符口径下逐字相等、字节口径下不等**）。此前把这一项记成「坐标档对不上」是**比对单位用错**，不是材料缺陷。
- 章节正文的 `citation_usages_json` 与版本记录的 `source_ids_json` **一致**（正文里就带着 `[[source:1e849f5f-…]]` 标记）；**无任何章节 `source_id` 指向本轮的隔离 KB**。

**（b）目标 KB 的检索命中与来源一致性 —— 10/10 PASS**（`D:\wt\lingdoc-485-evidence\embedding-run\retrieval-source-consistency.json`）

- 目标 KB 是 keyword-only（`vector_enabled=false`、`embedding_model_id` 为空）⇒ 检索走 FTS5 关键词引擎，**不产生任何模型调用**：chat 两本账本 sha256 检索前后**逐字节相同**（`dd39a0cb…` → `dd39a0cb…`）。
- 2 条查询（`PC-08 样品的抗压强度是多少` / `这份合成材料的文档编号和合成日期是什么`）各命中 **2 个分块**，全部是目标材料的 chunk（`91a28f35…` score 10.99、`1e849f5f…` score 7.90；`1e849f5f…` 11.46、`91a28f35…` 4.46），命中里包含章节声明的那个 source。
- ⚠️ **口径说明**：这是**词法检索**，**不是**向量检索 —— 向量检索仍需 embedding，而 embedding 仍被 provider 403 挡着。

#### 隔离口径（对授权的一处**缩小**，已如实登记）

授权原文的「知识库范围＝仅目标 KB `7c104e1b…` 的重新索引」在执行上被调整为「**新建隔离 vector-enabled KB 同材料**」，理由是技术事实：重跑目标 KB 的处理会**删旧 chunk、换 chunk id**，使章节的 `source_ids` 悬空并伤及 F01 的证据链。隔离 KB `895c0a35…`（同租户、同材料 `synth-chapter-c.md`、sha256 `001ba50c…`）承担了向量化；**目标 KB 与其 2 个 chunk 全程未被触碰**（`index_status=ready`、未删除）。

#### 对账（执行后快照 `embedding-run\after-state-attempt3.json`）

| 项 | 执行前 | 执行后 |
| --- | --- | --- |
| chat 账本 `qwen-beta-sqlite.json` sha256 | `74e86abc…` | **`74e86abc…`（逐字节未变，mtime 也未变）** |
| chat 账本 `.calls.json` sha256 | `dd39a0cb…` | **`dd39a0cb…`（逐字节未变）** |
| embedding 账本（**只有 `.calls.json` 一本**：当前代码只在 `…+.calls.json` 上记账，`ledger.go:206-210` 的 `NewFromEnv` 会给 env 基础路径拼上后缀；基础名文件 `qwen-embedding-budget.json` **从不存在** —— chat 之所以有两本，是因为 `qwen-beta-sqlite.json` 是**旧 v1 schema** 的历史遗留） | **不存在** | **存在，1 行**、`blocked=true` |
| DB `lingdoc_model_call_ledger` 行数 | 4（全 chat） | **4**（embedding 路径**不写**该表 ⇒ 独立文件账本是它唯一的账本） |
| 隔离 KB 向量行 | 0 | **0** |
| 目标 KB chunk 行 | 2（`ready`） | **2（`ready`，未动）** |
| 章节 `source_ids` | 研究问题 `[1e849f5f…]`／研究方案 `[]` | **逐字未变** |

#### 结论与**需要负责人裁定**的一项

- **§6 第 6 项「真实依赖均已验证」仍是 `NOT RUN`**：向量检索行为**没有**验证（provider 403 ⇒ 0 向量）；F01-07 仍 `NOT RUN`。**没有把任何未达成的项改判 PASS。**
- **需要负责人决定的一件事**：provider 侧的「仅免费层」额度耗尽不在代码侧、也不在授权侧。可选项：① 在百炼控制台充值或关闭「仅免费层」模式后，用**新授权窗口**重跑（⚠️ **但 #671 未解前，撞到 4xx 依旧会废掉整个窗口**）；② 授权另一条端点/凭据（超出当前授权范围，需重新批复）；③ 接受 §6 第 6 项在本轮维持 `NOT RUN`。
- 与 **Word 检查的两项人眼确认**（§6 第 5 项，`DEFERRED_MANUAL`）**各自独立**，任何一项未完成都仍不放行。

#### 证据（本轮全部在仓外，**未删任何东西**）

- `D:\wt\lingdoc-485-evidence\embedding-run\`：`before-state.json`（整轮前置）、`after-state-attempt3.json`、`provider-403-response.txt`（provider 原文，sha256 `2915D119…`）、`source_ids-verification.json`、`retrieval-source-consistency.json`、`g2_attempt3_result.json`、`g1_add_embedding_model_row.py`、`g2_retry_vector.py`、`verify_chapter_source_ids.py`、`verify_retrieval_sources.py`、`check_coordinate_unit.py`、`inspect_redis.py`、`capture_before_attempt3.py`、`dump_model_params.py`、`dump_stage_failure.py`、`start-embedding-service.ps1`（含 DB 2 隔离的理由注释）、`stop-embedding-service.ps1`、`extract-provider-403.ps1`、`ticket-asynq-queue-isolation.md`、`ticket-embedding-profile-no-reconcile.md`
- 服务日志：`D:\wt\lingdoc-484-evidence\logs\server-embedding-20261025-215322.log`（本次真实调用与 403 原文）、`server-trackd.stdout.log`（**证明前两轮的任务被 18087 抢走**的日志）
- 隔离环境：DB `D:\wt\lingdoc-484-evidence\data\lingdoc-484-final.db`；embedding 账本 `ledger\qwen-embedding-budget.json.calls.json`（**1 行**；基础名文件从未生成）与 chat 两本 `ledger\qwen-beta-sqlite.json` ＋ `.calls.json`（**未变**）
- 新立票：**#670**（asynq 队列跨实例无 namespace 隔离）、**#671**（新 priced embedding profile 无核对/解冻通道）；票 **#546** 已回写本轮事实。
