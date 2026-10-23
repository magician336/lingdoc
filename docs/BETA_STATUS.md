# Beta 进度状态（唯一进度源）

> **本文件是 lingdoc Beta 实施的唯一进度源。** 每批任务结束后更新它；不要另建进度表。
> **基线**：`origin/main = bbd461ac`（PR #498 合并后）。**审计报告**：`C:\Users\ASUS\AppData\Local\Temp\lingdoc-audit\00-beta-progress-audit.md`。
> **状态枚举**：代码实现 / 集成（进 main）/ 测试通过 / 验收完成（真实跑过且结论 PASS）——**四维度分别记，不合并**。
> 另有 `NOT STARTED` / `IN PROGRESS` / `IMPLEMENTED_UNVERIFIED` / `INTEGRATED_UNVERIFIED` / `PASS` / `BLOCKED` / `UNKNOWN` / `DEFERRED`。

---

## 1. Beta 首版放行链（必做，严格按依赖顺序）

| # | 任务 | 票 | 负责人 | 代码 | 集成 | 测试 | 验收 | 依赖 | 证据 | 下一步 |
|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 账本写入端修复：F01 生成路径也写真实账本行（`ModelCallLedgerSourceGenerationRun` 接上） | 新票（管理会话开） | pf-10 | NOT STARTED | — | — | — | — | 审计 P1 / D1：写表方是 `agent_model.go:recordModelCall`（Agent 轮次）；F01-07 走 `generation.go:generationHostModel` 不写表 | pf-10 开票并实施 |
| 2 | #504 第一条：统一对偶两侧 base-url 口径 | #504（第一条） | pf-10 | IN PROGRESS | — | — | — | — | 体检新候选 A：F01 要 API 根 / HTTP 侧要服务器根，15 处字面量；守护测试保护了矛盾 | 口径已裁定＝契约 `servers[0].url` 为单一权威 |
| 3 | #494 资料标识参数化 | #494 | pf-10 | **PASS** | **PASS** | **PASS** | NOT RUN | — | PR #498 合并提交 `bbd461ac`；守卫 `test_lingdoc_beta_knowledge_id.py` | 已完成 |
| 4 | #504 第二条：解除条件改稳定标识 | #504（第二条） | pf-10 | **PASS** | **PASS** | **PASS** | NOT RUN | — | 同上；`acceptance/gaps.json` ＋ `test_lingdoc_beta_gap_ids.py`（9 项） | 已完成 |
| 5 | #499 断言能力补齐 ＋ **穷举校验** | #499 | pf-10 | NOT STARTED | — | — | — | 3 | 审计：16/67 条缺 kind；kind 词表/校验/求值分三处且**无穷举检查** | pf-10 实施 |
| 6 | #501 先操作后断言（契约形状） | #501 | pf-10 | NOT STARTED | — | — | — | 5 | 审计：契约线性单遍，无「二次操作＋事后断言」结构 | pf-10 实施 |
| 7 | #484 材料接入与处理就绪（代码） | #484 | pf-10 | NOT STARTED | — | — | — | 5 | 「处理就绪」**已存在且已被 F01-06 断言**（`evidence/revision.go:ProcessingStateOf`、`evidence/asset.go:AssetStateReady`）⇒ 缺的是**产生**它的那一步 | pf-10 实施 |
| 8 | #485 SQLite 跑完 24 节点 F01（**第一个里程碑**） | #485 | pf-10 | NOT STARTED | — | — | — | 1–7 | 协议 §6：主流程不能含 FAIL/BLOCKED/NOT RUN | pf-10 实施 |
| 9 | #486 真实模型选段改写（账本非 0 行＋switch 行） | #486 | 待派 | NOT STARTED | — | — | — | 8 ＋ 人工前置 1 | **账本未修前不得标 PASS** | 人工前置 1 就位后派 |
| 10 | #487 隔离 PostgreSQL 那一轮 | #487 | 待派 | NOT STARTED | — | — | — | 9 ＋ 人工前置 2 | sqlite 那半已 PASS（`restart-readback.json` held） | 人工前置 2 就位后派 |
| 11 | #488 Word 人工检查 | #488 | 待派 | — | — | — | — | 9 ＋ 人工前置 4 | 协议 §4：**程序库 round-trip 不能替代** | 人工前置 4 就位后派 |
| 12 | #489 第二人独立复现（**记录项，不阻塞**） | #489 | 待派 | — | — | — | — | 9 | 口径裁定（#483）：**不是**首版门槛，但必须登记为未完成项 | 最后一批 |
| 13 | #490 放行结论表 ＋ 明确未放行项清单 | #490 | 待派 | NOT STARTED | — | — | — | 8–12 | — | 收口 |

## 2. 规格与文档（并行面，不碰代码文件）

| 任务 | 票 | 负责人 | 状态 | 证据 / 备注 |
|---|---|---|---|---|
| 第二人复现口径统一（18 号 §3/§8/§9） | #483 | pf-6 | IN PROGRESS（PR #497） | 已改 3 处；PR 落后 main 6 个提交 ⇒ 需落基 |
| 第二人复现口径统一（21-G6 草稿 / G2-spec.md:70 / G4-development-plan.md:130） | #483 扩展 | pf-6 | NOT STARTED | 规格清点 C-1 |
| Word / WPS 口径统一（18 号 §8 ＋ G6 全线） | #483 扩展 | pf-6 | NOT STARTED | 规格清点 C-2 |
| F01 节点数 23→24（`G6-质量运行门禁.md:27`） | #483 扩展 | pf-6 | NOT STARTED | 规格清点 C-3（实测契约 24 条） |
| 「Beta」vs「Beta 首版」范围对象（16-术语表 §1、01 号蓝图 §3、GLOSSARY 缺词条） | #483 扩展 | pf-6 | NOT STARTED | 规格清点 C-5 / C-9 |
| #502 spec 落盘副本**重做**（现副本与 #482 相似度 0.87，已过期） | #491 | pf-6 | NOT STARTED | 缺「4 类新表达力」决策；kind 列表仍 8 种（应 10）；权威表述相反 |
| 非 Beta 延期清单（用 `DEFERRED`，**不批量关票/改标签**） | — | pf-6 | NOT STARTED | 规格清点 E 节：22 项 DEFERRED ＋ 8 项不可延期 |

## 3. 共享文件锁（一把锁同一时刻只有一个持有者）

| 锁 | 文件 | 当前持有者 | 排队中 | 规则 |
|---|---|---|---|---|
| **L1 登记处** | `scripts/lingdoc_parallel_surfaces.py` | **PF-14 / pf-10**（自 #498 合并起） | PF-7（#503 交面）、PF-9（#495 交面）、PF-11（#149）、PF-13（#248） | 重复声明会让 **main 加载失败**（loader 拒绝重名路径）⇒ 必须逐个合，每次合完其余重新落基 |
| **L2 路径图** | `docs/08-本轮实施方案/G1-G7-Spec-完成路径图.md` | **PF-14 / pf-10**（与 L1 绑定：交面必须同时改两处） | PF-7、PF-9、PF-11、PF-13 | 无机器守护 ⇒ 静默不同步风险 |
| **L3 契约产物** | `docs/08-本轮实施方案/contracts/openapi.json` | **PF-11 / #149**（先合者） | PF-13（#248 改借用） | — |
| **L4 保留路径** | `frontend/src/views/lingdoc/Workspace.vue` | **PF-11 / #149** | PF-13（#248） | **保留路径：任何面不得独占**；只如实记录改动事实 |

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

## 5. 已统一的验收口径（不可再分歧）

1. **第二人独立复现 = 记录项**，不是 Beta 首版硬门槛；但必须登记为未完成项（原因＋解除条件），不得记为已通过。依据：验收协议 §1/§6（专门定义「通过」、穷举列举）vs 18 号 §3/§8/§9（开发计划叙述）⇒ 以协议为准。
2. **真实 Word 检查以 Microsoft Word 为准**，WPS 不增加为硬门槛（协议 §4）。
3. **F01 按实际契约的 24 个节点执行**（23 业务步 ＋ `F01-03b recordActivationRead`）；文档里的「23」是同义拆法，但 `G6-质量运行门禁.md:27` 按 23 字面表述 ⇒ 要改。
4. **Beta 首版 = 验收协议定义的减量范围**；不等于完整 Beta 设计，也不等于完整 G1–G7。

## 6. 人工前置 6 项（当前状态）

| # | 项 | 状态 | 缺什么 | 是否阻塞 |
|---|---|---|---|---|
| 1 | Qwen 真实模型调用（`qwen3.8-flash` / OpenAI 兼容） | NOT RUN（Beta 链账本 0 行） | 仓内变量名是 `LLM_*`（`LLM_PROVIDER=openai` 已是默认），需显式映射 `DASHSCOPE_API_KEY` → `LLM_API_KEY`；⚠️ **账本写入端修好前不得标 PASS** | **是**（卡 F01-07） |
| 2 | 隔离 PostgreSQL ＋ 重启读回 | NOT RUN（sqlite 那半已 PASS） | 隔离 PG 实例 | 否（后置） |
| 3 | 两章合成材料 ＋ `--material` SHA-256 | NOT RUN（`materials=[]`） | 材料与哈希（准备中） | **是**（卡 F01-06） |
| 4 | Word 人工检查（开/改/存/关/重开） | NOT RUN | Microsoft Word（Windows 宿主机）；主仓库已有 `_bp_check/SOURCE_snapshot.docx` ＋ 21 页渲染 | 否 |
| 5 | WeKnora 真实接入 / 处理 / 检索 | NOT RUN，**本机可能恒 BLOCKED** | 仓外一次性脚本 `env_facts.py` **已不在机器上**；**docreader 解析接口从未真调过** | **是**（卡 F01-06） |
| 6 | 第二人独立复现 | NOT RUN（记录项） | 第二个开发者 | 否（不阻塞） |

## 7. 本批（第一批）完成汇报

- **本批实际完成**：审计报告交付；`#498`（#494 ＋ #504 第二条）**合并**（`bbd461ac`）；共享文件锁 L1–L4 建立；#502 判定为**需重做**（落盘副本过期）；Pf-10 / pf-6 / 材料准备 三路派单。
- **四维度状态**：放行链代码实现 **PASS 2 项**（#494、#504 第二条）／集成 **PASS 2 项**／测试通过 **PASS 2 项**（门禁 gate=pass）／**验收完成 0 项**（协议 §6 要求主流程无 FAIL/BLOCKED/NOT RUN ⇒ 三轮均未达成）。
- **剩余 Beta 必做项**：第 1–2 项（账本修复、base-url 口径）在做；第 5–13 项待做；人工前置 6 项待就位。
- **当前阻塞与风险**：① 账本写入端（P1）—— 不修则 #486 永远拿不到 PASS；② base-url 口径 —— 不修则浏览器对偶结构性不可跑；③ L1/L2 锁串行 ⇒ 每次合并后其余 PR 都要重新落基（门禁轮次 = PR 数）；④ 材料/WeKnora 解析链路风险最高（脚本丢失 ＋ 解析接口从未真调）；⑤ 主仓库旧 `main` 工作区有重复的未提交登记处改动。
- **下一批与并行**：代码链由 pf-10 串行推进（1→2→5→6→7→8）；文档链由 pf-6 并行（口径统一 → 延期清单 → #502 重做）；材料/Qwen 映射由准备会话并行；**环境侧（隔离 PG、WeKnora 解析最小验证）另派有边界的探测**。

## 8. 下一批之后的集成与验收顺序

1. 逐个核实 PR 的当前基线 / 差异 / 门禁（**不采信历史报告的 CLEAN 或 gate pass**）；每次只集成一个涉及共享文件的 PR；合并后重新核对其余 PR。
2. 放行链修复完 ⇒ 优先跑完 SQLite 的 24 节点 F01（#485）⇒ 第一个完整可验证里程碑。
3. 随后集中执行真实 Qwen / WeKnora / 隔离 PostgreSQL / Word 验收。
4. 第二人复现登记为未完成项，不阻塞首版。
5. 最后 #490 放行结论表，逐项 PASS / FAIL / NOT RUN / BLOCKED ＋ 解除条件。
