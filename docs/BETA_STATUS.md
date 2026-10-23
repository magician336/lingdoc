# Beta 进度状态（唯一进度源）

> **本文件是 lingdoc Beta 实施的唯一进度源。** 每批任务结束后更新它；不要另建进度表。
> **基线**：`origin/main`（本文件合并后即最新基线；每次更新在本行下写明本文件对应的合并提交，避免基线行本身过期）。**审计报告**：`C:\Users\ASUS\AppData\Local\Temp\lingdoc-audit\00-beta-progress-audit.md`。
> **状态枚举**：代码实现 / 集成（进 main）/ 测试通过 / 验收完成（真实跑过且结论 PASS）——**四维度分别记，不合并**。
> 另有 `NOT STARTED` / `IN PROGRESS` / `IMPLEMENTED_UNVERIFIED` / `INTEGRATED_UNVERIFIED` / `PASS` / `BLOCKED` / `UNKNOWN` / `DEFERRED`。

---

## 1. Beta 首版放行链（必做，严格按依赖顺序）

| # | 任务 | 票 | 负责人 | 代码 | 集成 | 测试 | 验收 | 依赖 | 证据 | 下一步 |
|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 账本写入端修复：F01 生成路径也写真实账本行（`ModelCallLedgerSourceGenerationRun` 接上） | #486 | pf-10 | **PASS** | **PASS** | **PASS** | NOT RUN | — | PR #509 合并提交 `003efde8`；本地针对性测试 **8/8 PASS**（真表三例：走生成路径 ⇒ 恰好一行、不走 ⇒ 0 行、契约 `startGeneration` ⇒ 能按 `source_kind` 查到）；PR gate = success | 已完成。真实模型调用仍待授权（缺口 `GAP-MODEL-CREDENTIAL`） |
| 2 | #504 第一条：统一对偶两侧 base-url 口径 | #504（第一条） | pf-10 | **PASS** | **PASS**（PR #513，合并提交 `df6b271a`） | PASS（`scripts/ci` 全套 420 项 OK、F01 runner 回归 130 项 OK、登记处 11 面；管理会话另起进程实跑 4 条 base-url/api-root 守护断言全过、整个守护文件无回归） | NOT RUN | 1 | 单一权威 ＝ 契约 `contracts/openapi.json` 的 `servers[0].url` ＝ `http://localhost:4010/api/v1/lingdoc`；HTTP 对偶侧 15 处 `/api/v1/lingdoc` 字面量归零（实测 15 → 0）；守护测试从「两套相反约定各自钉死」改为**只钉死单一口径** ＋ 两条新断言（含「同一 base-url 喂两侧、同一判定」的回归门） | 已完成。真实 F01 跑通才算验收 |
| 3 | #494 资料标识参数化 | #494 | pf-10 | **PASS** | **PASS** | **PASS** | NOT RUN | — | PR #498 合并提交 `bbd461ac`；守卫 `test_lingdoc_beta_knowledge_id.py` | 已完成 |
| 4 | #504 第二条：解除条件改稳定标识 | #504（第二条） | pf-10 | **PASS** | **PASS** | **PASS** | NOT RUN | — | 同上；`acceptance/gaps.json` ＋ `test_lingdoc_beta_gap_ids.py`（9 项） | 已完成 |
| 5 | #499 断言能力补齐 ＋ **穷举校验** | #499 | pf-10 | NOT STARTED | — | — | — | 3 | 审计：16/67 条缺 kind；kind 词表/校验/求值分三处且**无穷举检查** | pf-10 实施 |
| 6 | #501 先操作后断言（契约形状） | #501 | pf-10 | NOT STARTED | — | — | — | 5 | 审计：契约线性单遍，无「二次操作＋事后断言」结构 | pf-10 实施 |
| 7 | #484 材料接入与处理就绪（代码 ＋ 真实验收） | #484 | pf-10 | **PASS**（服务能力已交付：`internal/lingdoc/materiallingest/`） | 待合（PR 未开） | PASS（19 个测试全过、`-race` 过、lint 0 issues、`go build ./...` 过） | **PASS**（WeKnora 真实链路，见 §10） | 5 | 材料身份＝固定前缀＋文件 SHA-256（不含时间戳）⇒ 同一份材料恒等；处理状态取数落在 `evidence.ProcessingStateOf` → `evidence.AssetStateReady` 这条链 ⇒ 与 F01-06 响应里的「就绪」同口径 | 代码进 main ＋ 由 PF-14 侧登记路径后即可接入验收路径（F01 用现有 `--knowledge-id` 注入，无需新端点） |
| 8 | #485 SQLite 跑完 24 节点 F01（**第一个里程碑**） | #485 | pf-10 | NOT STARTED | — | — | — | 1–7 | 协议 §6：主流程不能含 FAIL/BLOCKED/NOT RUN | pf-10 实施 |
| 9 | #486 真实模型选段改写（账本非 0 行＋switch 行） | #486 | 待派 | NOT STARTED | — | — | — | 8 ＋ 人工前置 1 | **账本未修前不得标 PASS** | 人工前置 1 就位后派 |
| 10 | #487 隔离 PostgreSQL 那一轮 | #487 | 待派 | NOT STARTED | — | — | — | 9 ＋ 人工前置 2 | sqlite 那半已 PASS（`restart-readback.json` held） | 人工前置 2 就位后派 |
| 11 | #488 Word 人工检查 | #488 | 待派 | — | — | — | — | 9 ＋ 人工前置 4 | 协议 §4：**程序库 round-trip 不能替代** | 人工前置 4 就位后派 |
| 12 | #489 第二人独立复现（**记录项，不阻塞**） | #489 | 待派 | — | — | — | — | 9 | 口径裁定（#483）：**不是**首版门槛，但必须登记为未完成项 | 最后一批 |
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
| **L5 生成路径** | `internal/lingdoc/workspace/generation.go` | **PF-2**（已交面） | **PF-14 借用**（账本修复必须改它：F01-07 走的就是它） | owner 仍是 PF-2；借用方在 summary 记一笔借用事实，**不得**声明进自己路径集 |
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

## 5. 已统一的验收口径（不可再分歧）

1. **第二人独立复现 = 记录项**，不是 Beta 首版硬门槛；但必须登记为未完成项（原因＋解除条件），不得记为已通过。依据：验收协议 §1/§6（专门定义「通过」、穷举列举）vs 18 号 §3/§8/§9（开发计划叙述）⇒ 以协议为准。
2. **真实 Word 检查以 Microsoft Word 为准**，WPS 不增加为硬门槛（协议 §4）。
3. **F01 按实际契约的 24 个节点执行**（23 业务步 ＋ `F01-03b recordActivationRead`）；文档里的「23」是同义拆法，但 `G6-质量运行门禁.md:27` 按 23 字面表述 ⇒ 要改。
4. **Beta 首版 = 验收协议定义的减量范围**；不等于完整 Beta 设计，也不等于完整 G1–G7。

## 6. 人工前置 6 项（当前状态）

| # | 项 | 状态 | 缺什么 | 是否阻塞 |
|---|---|---|---|---|
| 1 | Qwen 真实模型调用（`qwen3.8-flash` / OpenAI 兼容） | NOT RUN（Beta 链账本 0 行） | 映射已定：`LLM_MODEL_NAME=qwen3.8-flash` / `LLM_BASE_URL=https://llm-k57eqvdqacn4nov3.cn-beijing.maas.aliyuncs.com/compatible-mode/v1` / `LLM_API_KEY=<与 DASHSCOPE_API_KEY 同值>`。⛔ **不要写 `LLM_API_KEY=${DASHSCOPE_API_KEY}`**（`env_file` 原样传值、应用内插值只做一遍 ⇒ 会变成字面量）。`LLM_*` 在 `.env.example` 的 **D2 段**（非 D1），Go 代码不直接读，经 `config/builtin_models.yaml` 占位符生效。⚠️ **设 `LINGDOC_T10_ACCEPTANCE_PROFILE` 会把 F01 的 chat 强制成 DeepSeek** ⇒ 用 Qwen 时不要设。⚠️ **账本写入端修好前不得标 PASS，且暂不发起真实调用**（待账本修复后单独申请） | **是**（卡 F01-07） |
| 2 | 隔离 PostgreSQL ＋ 重启读回 | NOT RUN（sqlite 那半已 PASS） | 隔离 PG 实例 | 否（后置） |
| 3 | 两章合成材料 ＋ `--material` SHA-256 | **材料已备好**（仓外 `lingdoc-prep/`） | `synth-chapter-a`（SB-2031 薄膜，SHA-256 `4b1572b57144…`，4747 B）＋ `synth-chapter-b`（SB-4472 电解质，`29e2d8f3c2ae…`，4824 B），各 3 处可核对引用点；字节规范 UTF-8 无 BOM / 仅 LF / 末尾恰好一个换行；有 `recompute_hashes.py` 可复算 ⇒ **同一套材料必须复用，勿另造** | **是**（卡 F01-06） |
| 4 | Word 人工检查（开/改/存/关/重开） | NOT RUN | Microsoft Word（Windows 宿主机）；主仓库已有 `_bp_check/SOURCE_snapshot.docx` ＋ 21 页渲染 | 否 |
| 5 | WeKnora 真实接入 / 处理 / 检索 | NOT RUN，**本机可能恒 BLOCKED** | 端点已定位：上传 `POST /api/v1/knowledge-bases/{id}/knowledge/file`（`routes_knowledge.go:73`、`handler/knowledge.go:254`，multipart 字段 `file`）；解析状态表 `knowledges.parse_status`＝`completed`（`types/knowledge.go:43-64`），`GET /api/v1/knowledge/:id`；检索 `POST /api/v1/knowledge-bases/{id}/hybrid-search`（`routes_knowledge.go:229`）。三个容器**本机全在跑**。⚠️ `env_facts.py` **已不在机器上**（要重写：幂等标识必须**与时间无关**＝固定标题前缀＋文件 SHA-256，否则每次造新知识、id 每次变；输出须按 starting_state 裁剪，否则 `load_state.py` 的 `preflight()` 在首个写请求前退 1）；⚠️ **docreader 解析接口从未真调过**；⚠️ **真实解析必经 remote embedding ⇒ 真实验证会触发真实外部调用 ⇒ 须先修账本并取得授权** | **是**（卡 F01-06） |
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

## 9. 第二批进展（2026-10-23 17:00 后）

- **已合并**：#505（`docs/BETA_STATUS.md` 唯一进度源，合并提交 `76945d3a`）、#498（`bbd461ac`）。
- **已合并**：#497（口径统一 11 文件，合并提交 `1e95a0e7`）、#507（DEFERRED 清单，合并提交 `6f0674b6`）。
- **锁表增至 6 把**：新增 **L5**（`internal/lingdoc/workspace/generation.go`，owner=PF-2、PF-14 借用；账本修复必须改它）与 **L6**（`GLOSSARY.md` ＋ `16-术语表.md`，术语唯一权威，当前由 #497 持有）。
- **#503（PF-7）已按指示拆开**：20 → 18 个文件，**登记处与路径图已摘出** ⇒ 不占 L1/L2；交面另开票排队。
- **账本修复（放行链第 1 项）进行中**：pf-10 已定位到 `generation.go` 归 PF-2 的冲突并改为借用形态；修法＝**账本实例下沉到一处、所有适配器共用**（一个拥有者、多个调用方）。**硬要求已下达**：守护测试要分别断言「走生成路径 ⇒ 账本多一行」「不走 ⇒ 不多行」，且 F01 `startGeneration` 的调用能按 `source_kind` 查到。
- **环境侧已就绪的部分**：两章合成材料 ＋ SHA-256（可复用，勿另造）、Qwen 三行映射（注意 `${}` 插值坑）、WeKnora 三个端点与 `env_facts.py` 实现规格、隔离 PG 口径＝**独立库名不碰 dev 库**（还差：本轮专用隔离库、迁移到 134（dev 停在 122）、当前 head 二进制（仓外 exe 是 09-27 旧构建）、DSN、6 个 DB 环境变量）。
- **⚠️ 暂停待授权项（尚未执行）**：真实 Qwen 调用、任何不可逆操作、需要改验收门槛的改动。

## 10. 第三批进展（2026-10-23 18:00 后）

- **放行链第 1 项（账本写入端）已合并集成**：PR #509，合并提交 `003efde8`。四维度分别记：代码 **PASS** ／ 集成 **PASS** ／ 自动化测试 **PASS**（本地针对性 8/8 PASS ＋ PR gate success）／ 真实验收 **NOT RUN**（真实模型调用待授权，缺口 `GAP-MODEL-CREDENTIAL`）。
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
