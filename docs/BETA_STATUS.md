# Beta 进度状态（唯一进度源）

> **本文件是 lingdoc Beta 实施的唯一进度源。** 每批任务结束后更新它；不要另建进度表。
> **基线**：`origin/main`（本文件合并后即最新基线；每次更新在本行下写明本文件对应的合并提交，避免基线行本身过期）。**审计报告**：`C:\Users\ASUS\AppData\Local\Temp\lingdoc-audit\00-beta-progress-audit.md`。
> **本轮同步基线**：`origin/main` = `a98138af`（PR #556 合并提交；本文件本轮更新按此 head 逐项核实）。⚠️ 本文件**本轮对应的合并提交**＝本 PR 的合并提交（由管理会话落基，**此处不臆造 SHA**）。
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
| 1 | 账本写入端修复：F01 生成路径也写真实账本行（`ModelCallLedgerSourceGenerationRun` 接上） | #486 | pf-10 | **PASS** | **PASS** | **PASS** | **PASS（受控真实调用已取证）** | — | PR #509 合并提交 `003efde8`；本地针对性测试 **8/8 PASS**（真表三例：走生成路径 ⇒ 恰好一行、不走 ⇒ 0 行、契约 `startGeneration` ⇒ 能按 `source_kind` 查到）；PR gate = success。**真实验收**：一次受控真实 Qwen chat 调用走生成路径 ⇒ DB `lingdoc_model_call_ledger` 新增**恰好 1 行**（`kind=call`／`source_kind=generation_run`／`run_mode=real`／`model_id=lingdoc-chat-qwen3.8-flash-iso`，行 id `3286e6a0-cd2a-4a07-9609-0f678ab72236`，input/output tokens 2051/519）⇒ 本行口径「生成路径写真实账本行」**已达成**（取证见 §14.2） | 已完成。⚠️ 该次验收发生在**受控 profile 的隔离路径**上 ⇒ **不等于** F01-07 契约节点通过、**不等于** Beta 放行通过；缺口 `GAP-MODEL-CREDENTIAL` **未解除**（§14.4） |
| 2 | #504 第一条：统一对偶两侧 base-url 口径 | #504（第一条） | pf-10 | **PASS** | **PASS**（PR #513，合并提交 `df6b271a`） | PASS（`scripts/ci` 全套 420 项 OK、F01 runner 回归 130 项 OK、登记处 11 面；管理会话另起进程实跑 4 条 base-url/api-root 守护断言全过、整个守护文件无回归） | NOT RUN | 1 | 单一权威 ＝ 契约 `contracts/openapi.json` 的 `servers[0].url` ＝ `http://localhost:4010/api/v1/lingdoc`；HTTP 对偶侧 15 处 `/api/v1/lingdoc` 字面量归零（实测 15 → 0）；守护测试从「两套相反约定各自钉死」改为**只钉死单一口径** ＋ 两条新断言（含「同一 base-url 喂两侧、同一判定」的回归门） | 已完成。真实 F01 跑通才算验收 |
| 3 | #494 资料标识参数化 | #494 | pf-10 | **PASS** | **PASS** | **PASS** | NOT RUN | — | PR #498 合并提交 `bbd461ac`；守卫 `test_lingdoc_beta_knowledge_id.py` | 已完成 |
| 4 | #504 第二条：解除条件改稳定标识 | #504（第二条） | pf-10 | **PASS** | **PASS** | **PASS** | NOT RUN | — | 同上；`acceptance/gaps.json` ＋ `test_lingdoc_beta_gap_ids.py`（9 项） | 已完成 |
| 5 | #499 断言能力补齐 ＋ **穷举校验** | #499 | pf-10 | **PASS**（PR #530 合并提交 `aa2edb12`） | **PASS**（`aa2edb12`） | PASS（`LingDoc PR gate` = pass、`F01 runner regression` = pass） | NOT RUN | 3 | 审计：16/67 条缺 kind；kind 词表/校验/求值分三处且**无穷举检查**。**已落地**：`scripts/lingdoc_check_kinds.py`（单一权威 ＋ 穷举校验）＋ `scripts/ci/test_lingdoc_check_kinds.py` ＋ `run_f01.py` 增 5 个 kind ＋ `contracts/validate_artifacts.py` 改 229 行 | pf-10 实施（**已合并**；余下的是它与 #501 的串行） |
| 6 | #501 先操作后断言（契约形状） | #501 | pf-10 | NOT STARTED | — | — | — | 5 | 审计：契约线性单遍，无「二次操作＋事后断言」结构 | pf-10 实施。⚠️ **未确证**：pf-10 的实际进展以该会话的 summary 为准（本轮未读任何别人的工作区）；**#501 与 #499 改同一批文件 ⇒ 必须串行**（#499 已随 PR #530 合入）。⚠️ **本轮实测**：`origin/main`（`a98138af`）上**没有** #501 的代码（`gh pr list` 无对应 PR）⇒ 四维度仍按 `NOT STARTED` 记 |
| 7 | #484 材料接入与处理就绪（代码 ＋ 真实验收） | #484 | pf-10 | **PASS**（服务能力已交付：`internal/lingdoc/materiallingest/`，PR #515 → `2966045b`） | **PASS**（`2966045b`） | PASS（19 个测试全过、`-race` 过、lint 0 issues、`go build ./...` 过） | **PASS（F01-06 契约节点在隔离环境真实通过）** | 5（#499 只影响**断言能力**，不影响本行的**服务端能力**；本行的 PASS 指服务端能力 ＋ F01-06 节点） | 材料身份＝固定前缀＋文件 SHA-256（不含时间戳）⇒ 同一份材料恒等；处理状态取数落在 `evidence.ProcessingStateOf` → `evidence.AssetStateReady` 这条链 ⇒ 与 F01-06 响应里的「就绪」同口径。**实测**：隔离库 `weknora_iso` 上以 owner 身份跑 F01-01～F01-06 ⇒ **7/7 节点 PASS、10/10 checks passed**；F01-06 真实发出 `POST …/assets`（请求体只有 `knowledge_id`）⇒ 响应 `data.processing_state="ready"`；**本轮新增 chat = 0、embedding = 0**（详见 §13） | 真实材料侧的**检索命中**证据（人工前置 5 尚 NOT RUN）＋ 与 Qwen chat 模型行 / 预算口径对齐（见第 9 行） |
| 8 | #485 SQLite 跑完 24 节点 F01（**第一个里程碑**） | #485 | pf-10 | NOT STARTED | — | — | — | 1–7 | 协议 §6：主流程不能含 FAIL/BLOCKED/NOT RUN | pf-10 实施。**本行仍 `NOT STARTED`** —— 开工前置：① **#501** 的两步契约形状 ② **#545** 的 model-use 同意记录（F01-07 在真实链路上缺它 ⇒ 422 `asset_not_authorized`，票 OPEN） |
| 9 | #486 真实模型选段改写（账本非 0 行＋switch 行） | #486 | 待派 | NOT STARTED | — | — | NOT RUN | 8 ＋ 人工前置 1 ＋ **受控 Qwen profile（PR #526 已合入 `dfa52944`；后续修复 PR #544 `232e0899`）** | **账本非 0 行已达成**（一次受控真实调用 ⇒ `kind=call` 行 1 行、2051/519 tokens，§14.2）；⚠️ **switch 行未产生**：该次调用没有模型切换，且 `RecordModelSwitch` 在 `origin/main` 上**只有接口定义与实现、没有生产调用点**（`internal/lingdoc/workspacecore/model_call_ledger.go:115`／`:185`，其余命中全在测试文件里）⇒ 本行口径「账本非 0 行**＋switch 行**」**只满足一半** ⇒ 按 §5 口径记 `NOT RUN`（**非** `DEFERRED_MANUAL`：代码侧条件不具备） | 人工前置 1 就位后派；执行前置三条：① 注册一行 `type=KnowledgeQA` 的 Qwen chat 行（`config/builtin_models.yaml` **不在仓内** ⇒ 本地 yaml 或直接注册；受控调用那次用的是隔离库行 `lingdoc-chat-qwen3.8-flash-iso`、`parameters.provider` 字面 `aliyun`）② 显式设置受控 profile（**不设 ⇒ 无次数上限、无费用口径**，见 §6 人工前置 1）③ 凭据：用正确的 `SYSTEM_AES_KEY` 或用该 key 重新注册模型行（**注意别把密钥明文落库**，见 §12.4 风险 3）。⚠️ **另需**：switch 行的写入路径（`RecordModelSwitch`）尚无生产调用点 ⇒ 需开票 |
| 10 | #487 隔离 PostgreSQL 那一轮 | #487 | 待派 | NOT STARTED | — | — | — | 9 ＋ 人工前置 2 | sqlite 那半已 PASS（`restart-readback.json` held） | 人工前置 2 就位后派 |
| 11 | #488 Word 人工检查 | #488 | 待派 | — | — | — | — | 9 ＋ 人工前置 4 | 协议 §4：**程序库 round-trip 不能替代** | 人工前置 4 就位后派 |
| 12 | #489 第二人独立复现（**记录项，不阻塞**） | #489 | 待派 | — | — | — | NOT RUN（记录项，**未完成项**） | 9 | 口径裁定（#483）：**不是**首版门槛，但必须登记为未完成项；本轮**未复现**（`gh issue view 489` = OPEN）⇒ **不得**记为已通过 | 最后一批。**原因**：本轮没有第二名开发者执行独立复现。**解除条件**：由第二名开发者按 #489 的步骤独立复现 F01 契约并把证据绑到当前 head（跟进票 #489） |
| 13 | #490 放行结论表 ＋ 明确未放行项清单 | #490 | 待派 | NOT STARTED | — | — | — | 8–12 | — | 收口 |

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
3. **F01 按实际契约的 24 个节点执行**（23 业务步 ＋ `F01-03b recordActivationRead`）；文档里的「23」是同义拆法，但 `G6-质量运行门禁.md:27` 按 23 字面表述 ⇒ 要改。
4. **Beta 首版 = 验收协议定义的减量范围**；不等于完整 Beta 设计，也不等于完整 G1–G7。

## 6. 人工前置 6 项（当前状态）

| # | 项 | 状态 | 缺什么 | 是否阻塞 |
|---|---|---|---|---|
| 1 | Qwen 真实模型调用（`qwen3.8-flash` / OpenAI 兼容） | **DEFERRED_MANUAL**（一次受控真实调用已取证并已登记；F01 主流程里那一次**仍未跑**） | **实测事实（已发生）**：一次受控真实 Qwen chat 调用已执行并取证 —— provider 请求总数 **1**；input/output/total tokens **2051 / 519 / 2570**（`usage_status=reported` ⇒ **可核实**）；`finish_reason=stop`；本地入口请求 ID `e6b5a2b9-799f-4114-98d3-26ef351eb99e`；generation run id（＝ DB 账本 `source_id`）`427e9803-d166-427e-8078-921a30d6dd77`；DB 账本行 id `3286e6a0-cd2a-4a07-9609-0f678ab72236`（`kind=call`／`source_kind=generation_run`／`run_mode=real`／`model_id=lingdoc-chat-qwen3.8-flash-iso`／`provider=aliyun`）⇒ **走的是 generation run 路径**；provider 侧请求 ID **未核实**；**费用＝未核算/不适用**（JSON 账本 `cost_basis=not_applicable`；DB 账本表 21 列**无 `cost_basis`** ⇒ 其 `reserved/actual=0` **不得**单独引用成「免费」，票 #527）。**仍未做**：F01 主流程里那一次真实调用 —— ① 受控 profile 与旧路径检索不兼容（profile 刻意禁 embedding，而旧路径检索要算查询向量）② F01-07 缺**显式 model-use 同意记录**（隔离库 `lingdoc_g2_objects` 0 行 ⇒ 默认 policy 的 `ModelUseAllowed=false` ⇒ 422 `asset_not_authorized`，票 **#545** OPEN）。**解除条件**：按 #545 的口径在 F01-06 与 F01-07 之间补出显式 model-use 许可（或让 `bindAsset` 在既有语义下产出该许可），并在受控 profile `lingdoc-qwen-token-budget-20261023-v1` 下重跑 F01-07 ⇒ 账本出现 `source_kind=generation_run` 的真实行（跟进票 **#545**；配套 **#501** 的两步契约形状）。⚠️ **一次受控调用成功 ≠ F01-07 验收通过 ≠ Beta 放行通过**（§14.3）。映射与注意事项不变：`LLM_MODEL_NAME=qwen3.8-flash` / `LLM_BASE_URL=https://llm-k57eqvdqacn4nov3.cn-beijing.maas.aliyuncs.com/compatible-mode/v1` / `LLM_API_KEY=<与 DASHSCOPE_API_KEY 同值>`；⛔ **不要写 `LLM_API_KEY=${DASHSCOPE_API_KEY}`**（`env_file` 原样传值、应用内插值只做一遍 ⇒ 会变成字面量）。`LLM_*` 在 `.env.example` 的 **D2 段**（非 D1），Go 代码不直接读，经 `config/builtin_models.yaml` 占位符生效。⚠️ **设 `LINGDOC_T10_ACCEPTANCE_PROFILE` 会把 F01 的 chat 强制成 DeepSeek** ⇒ 用 Qwen 时不要设；⚠️ **「不设」的代价要写明**：profile 为空时 `internal/lingdoc/workspace/generation_acceptance_budget.go:91-96` 返回空预算 ⇒ **没有调用次数上限、没有 CNY 上限、没有预留/实付记账** ⇒ 「一次真实调用在预算内」**没有机器强制** ⇒ **二选一已裁定为 ①**：受控 Qwen profile 已由 PR #526 合入（`dfa52944`）、后续修复 PR #544（`232e0899`） | **是**（卡 F01-07） |
| 2 | 隔离 PostgreSQL ＋ 重启读回 | NOT RUN（sqlite 那半已 PASS） | 隔离 PG 实例 | 否（后置） |
| 3 | 两章合成材料 ＋ `--material` SHA-256 | **材料已备好**（仓外 `lingdoc-prep/`） | `synth-chapter-a`（SB-2031 薄膜，SHA-256 `4b1572b57144…`，4747 B）＋ `synth-chapter-b`（SB-4472 电解质，`29e2d8f3c2ae…`，4824 B），各 3 处可核对引用点；字节规范 UTF-8 无 BOM / 仅 LF / 末尾恰好一个换行；有 `recompute_hashes.py` 可复算 ⇒ **同一套材料必须复用，勿另造** | **是**（卡 F01-06） |
| 4 | Word 人工检查（开/改/存/关/重开） | **DEFERRED_MANUAL** | 代码侧已具备：仓内 `docs/08-本轮实施方案/acceptance/beta-first/<sha>/<run>/word-check.md` 模板与五个必填字段的校验已就位（现存 3 份**未填**模板，字段仍是 `<待填>`）⇒ **只差真人用 Microsoft Word 操作**。**解除条件**：按 `word-check.md` 填完五个必填字段并用 `--word-check <该文件>` 重跑证据脚本（跟进票 **#480**，缺口标识 `GAP-WORD-CHECK`）。Microsoft Word（Windows 宿主机）；主仓库已有 `_bp_check/SOURCE_snapshot.docx` ＋ 21 页渲染 | 否 |
| 5 | WeKnora 真实接入 / 处理 / 检索 | NOT RUN，**本机可能恒 BLOCKED** | 端点已定位：上传 `POST /api/v1/knowledge-bases/{id}/knowledge/file`（`routes_knowledge.go:73`、`handler/knowledge.go:254`，multipart 字段 `file`）；解析状态表 `knowledges.parse_status`＝`completed`（`types/knowledge.go:43-64`），`GET /api/v1/knowledge/:id`；检索 `POST /api/v1/knowledge-bases/{id}/hybrid-search`（`routes_knowledge.go:229`）。三个容器**本机全在跑**。⚠️ `env_facts.py` **已不在机器上**（要重写：幂等标识必须**与时间无关**＝固定标题前缀＋文件 SHA-256，否则每次造新知识、id 每次变；输出须按 starting_state 裁剪，否则 `load_state.py` 的 `preflight()` 在首个写请求前退 1）；⚠️ **docreader 解析接口从未真调过**；⚠️ **真实解析必经 remote embedding ⇒ 真实验证会触发真实外部调用 ⇒ 须先修账本并取得授权** | **是**（卡 F01-06） |
| 6 | 第二人独立复现 | NOT RUN（记录项） | **未完成项**（按 §5 口径 1：第二人独立复现**不是** Beta 首版硬门槛 ⇒ **不写** `DEFERRED_MANUAL`，但**必须**登记原因＋解除条件，**不得**记为已通过）。**原因**：本轮没有第二名开发者执行独立复现。**解除条件**：由第二名开发者按 #489 的步骤独立复现 F01 契约并把证据绑到当前 head（跟进票 **#489**）。第二个开发者 | 否（不阻塞） |

## 7. 本批（第一批）完成汇报

- **本批实际完成**：审计报告交付；`#498`（#494 ＋ #504 第二条）**合并**（`bbd461ac`）；共享文件锁 L1–L4 建立；#502 判定为**需重做**（落盘副本过期）；Pf-10 / pf-6 / 材料准备 三路派单。
- **四维度状态（**当前值**，2026-10-24 按 `origin/main` = `a98138af` 逐行复核；第一批当时的快照为 2/2/2/0）**：放行链代码实现 **PASS 6 项**（#486 账本写入端、#504 第一条、#494、#504 第二条、#499、#484）／集成 **PASS 6 项**／测试通过 **PASS 6 项**／**验收完成 2 项**（① #486 账本写入端 —— 一次受控真实调用写出 `source_kind=generation_run` 账本行，§14.2；② #484 —— F01-06 契约节点在隔离环境真实通过）。⚠️ **验收完成 2 项 ≠ Beta 放行通过**：协议 §6 要求主流程无 FAIL/BLOCKED/NOT RUN/DEFERRED_MANUAL ⇒ **仍不达成**（#485 仍 `NOT STARTED`、#489 仍是未完成项、#490 未开始）。
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

> ⚠️ **本节已被 §14（2026-10-24）部分取代 —— 冲突处以 §14 为准**：下面第 1 条里「受控 Qwen 路径由 PR #526 交付 —— **未合并**」**已过时**（#526 已合入 `dfa52944`、后续修复 #544 `232e0899`）；第 3 条里「全仓不存在 `IsEmbeddingCapableModelType`」**已过时**（#533 已由 PR #551 合入 `cc40cb44`）；第 4 条里「`EnqueueGenerationAfter` 入队**未声明** `MaxRetry`」**已过时**（#534 已由 PR #554 合入 `1df56366`）。本节其余内容（第一次 POST 的 422 根因、凭据解密的宽松分支等）仍然成立。

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

### 14.3 边界声明（不可省略）

**一次受控真实调用成功 ≠ F01-07 验收通过 ≠ Beta 放行通过。** 本次只跑了 `startGeneration` 及其后端模型调用；**F01-08～F01-23（取候选／取来源／采纳／存章／确认／交付／导出）未执行**；24 节点契约验收、SQLite 验收、人工验收的独立门槛**完全不受影响**（CALL-REPORT §7 / §12）。⇒ 本节只登记「一次受控调用的取证事实」，**不改任何放行结论**。

### 14.4 环境缺口：`GAP-MODEL-CREDENTIAL` **未解除**

- **缺口登记**（`docs/08-本轮实施方案/acceptance/gaps.json`，本轮**未改**该文件）：`GAP-MODEL-CREDENTIAL` → 票 **#480**、条目「缺口 2」、解除条件「给真实模型凭据后重跑本脚本」。
- **为什么一次真实调用 ≠ 缺口解除**：那次调用走的是**受控 profile 的隔离路径**（隔离库 `weknora_iso` ＋ 隔离 Redis DB 3 ＋ 临时把 KB 的 `vector_enabled` 置 false ＋ 临时插入 1 行 model-use 许可记录；**两处临时数据均已恢复并核验**）⇒ **不等于**「F01 主流程已能在受控 profile 下跑完 24 节点」，也**不等于**该缺口登记的解除条件（「给真实模型凭据后**重跑本脚本**」＝ `scripts/acceptance/beta_evidence.py`）被满足 —— 本轮**没有**以该脚本重跑。
- ⇒ **`GAP-MODEL-CREDENTIAL` 保持未解除**；放行链第 1 行新记的「真实验收 PASS」只覆盖**账本写入端这一条口径**（§1 第 1 行）。

### 14.5 本轮仍未解除 / 未运行的事实（NOT RUN 清单）

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
