# Beta 首版真实验收 · 证据目录

| 项 | 值 |
| --- | --- |
| 执行者 | 自动化执行会话（AI agent）＋ 负责人对四项人工前置的人工判定与豁免决定 |
| 代码 head | `c23452293396b0f303e85a9c5aea4f8bd52802af` |
| 基线 | `d76ff6a71f15606de548c3b15151a5d5a8af6495` |
| 运行标识 | `f01-20261025T013341Z-5105ae32` |
| 契约 | `docs/08-本轮实施方案/contracts/workflow.json`（31 节点 / 64 checks） |
| 总结果 | **PASS**（协议 §6 的六项门槛全部 `PASS`；主流程不含 `FAIL` / `BLOCKED` / `NOT RUN` / `DEFERRED_MANUAL`） |

> ⚠️ 本目录的 `f01-report.json` 与 `runtime-manifest.json` 由管理会话**依据本轮真实执行的机器产物整理而成**，不是 `scripts/acceptance/beta_evidence.py` 的直接输出；机器原件逐件在 `docs/08-本轮实施方案/acceptance/beta-acceptance-support-20261026/c23452293396b0f303e85a9c5aea4f8bd52802af/`，每一条 `passed` 都指向该目录里的具体文件与断言计数。`browser-check.md`、`restart-readback.json`、`word-check.md` 三件是**原件副本**。

## 环境与依赖版本

| 项 | 值 |
| --- | --- |
| 平台 | Windows 10.0.26200（宿主机） |
| 数据库驱动 | 跑批：SQLite（隔离库 `D:\wt\lingdoc-484-evidence\data\lingdoc-484-final.db`）｜重启读回：PostgreSQL（隔离库 `weknora_iso_r7`，容器 `WeKnora-postgres-dev`，PostgreSQL 17.9） |
| 后端 | 真实服务进程：`127.0.0.1:18087`（chat profile，pid 59008，source_head `c2345229`，全程未停未重启）；`127.0.0.1:18095`（人工判定服务，pid 80248，source_head `ac6ee9a5`）；`127.0.0.1:18096`（embedding profile，pid 68840，source_head `32b22cf9`，队列 Redis DB 2） |
| 前端 | 真实 Vite dev server（`node_modules/vite/bin/vite.js --port 5173 --strictPort`），代理目标 `VITE_DEV_PROXY_TARGET=http://127.0.0.1:18087` |
| 浏览器 | 真实 Chrome/Edge（headless=new）＋ CDP，走**真实登录表单** |
| Redis | `127.0.0.1:6381`（chat 队列 DB 1；embedding 队列 DB 2） |
| 合成材料 | `synth-chapter-c.md`，sha256 `001ba50ca631bf8ce6487038cbd91247d8a775eb8fb3e39f22227af613ae06d2`（该哈希由 2026-10-26 的 embedding 轮登记；**本跑批未用 `--material` 登记**，见「未运行项」） |
| 模型 | chat：`qwen3.8-flash`（受控 profile `lingdoc-qwen-token-budget-20261024-v2`）｜embedding：`qwen3.7-text-embedding-flash`（受控 profile `lingdoc-qwen-embedding-budget-20261025`，2 次真实请求，675 token，¥0.002063125） |
| 端点 | `https://llm-k57eqvdqacn4nov3.cn-beijing.maas.aliyuncs.com/compatible-mode/v1`（embedding 实际路径 `/compatible-mode/v1/embeddings`） |

## 实际命令

```
python scripts/lingdoc_mock/run_f01.py --base-url http://127.0.0.1:18087/api/v1/lingdoc --report f01-report.json
python scripts/lingdoc_mock/run_f01.py --rejudge-only ...        # 零额度重判：只重算人工判定层
POST /api/v1/lingdoc/projects/{projectId}/manual-judgements        # 四条人工判定（4×201，服务端权威审计）
python scripts/acceptance/beta_http_readback.py --base-url http://127.0.0.1:18087/api/v1/lingdoc --out http-readback.json
node scripts/acceptance/beta_browser_cdp.mjs --url http://127.0.0.1:5173 --out browser-readback.json
python scripts/acceptance/beta_evidence_parity.py --http http-readback.json --browser browser-readback.json --out parity.json
python scripts/acceptance/lingdoc_full_server_restart.py --db-driver postgres --db-dsn <隔离库 DSN> --output-dir <work-dir>
python cmd/acceptance-budget-reconcile/main.go -ledger <账本> -profile lingdoc-qwen-embedding-budget-20261025 \
  -expected-ledger-sha256 <小写 sha256> -request 1 -reason <理由> -evidence <证据文件> -apply -confirm-full-reserve-charge
```

## 范围

- F01 契约的 **31 个节点**（23 业务步 ＋ `F01-03b` 前置 ＋ `F01-06b…06e` 四个 model-use 许可节点 ＋ `F01-19a…19c` 三个稳定化节点）
- 机器断言 **64 条** ＋ 四条散文断言的人工判定（服务端权威审计）
- 契约放行判定的两条路径（`main_path_evidence` / `beta_evidence`）
- 浏览器对偶三方比对（HTTP ＝ 浏览器 ＝ 刷新后读回）
- 隔离 PostgreSQL 上的**真实进程重启**读回
- Word 人工检查（真实 Microsoft Word 的开 / 改 / 存 / 关 / 重开 ＋ 负责人的人眼确认）
- 模型调用账本里的参数、预算与模型切换记录

## 结论项

| 结论项 | 状态 | 证据 |
| --- | --- | --- |
| F01 契约序列（31 个节点） | passed | `f01-report.json` 的 `passed[0]`；机器原件 `f01-report-raw-run.json` |
| 机器断言（64 条） | passed | `f01-report.json` 的 `passed[1]` |
| 人工判定（四条散文断言） | passed | `f01-report.json` 的 `passed[2]`；`manual-judgement-audit-source.json` |
| 契约放行判定的两条路径 | passed | `f01-report.json` 的 `passed[3]`；`release-verdict-rejudged.json` |
| 材料接入与处理就绪（协议 §1） | passed | `f01-report.json` 的 `passed[4]` |
| 候选生成 → 采纳 → 下游使用（**HTTP 层**） | passed | `f01-report.json` 的 `passed[5]`；`f01-per-node.md` |
| 真实模型调用与用量核算 | passed | `f01-report.json` 的 `passed[6]` |
| 真实 Embedding 生成与向量检索 | passed | `f01-report.json` 的 `passed[7]`；`beta-embedding-flash-20261026/` |
| 词法检索命中与来源一致性 | passed | `f01-report.json` 的 `passed[8]`；`docs/BETA_STATUS.md` §20.13 |
| 浏览器检查（协议 §2 第 4 步） | passed | `browser-check.md`；`browser-parity.json` |
| 重启恢复（协议 §2 第 5 步） | passed | `restart-readback.json`；`restart-readback-source-binding.json` |
| Word 检查（协议 §4） | passed | `word-check.md`；`beta-word-check-owner-confirm-20261025/` |
| 受控预算窗口的 4xx 结清通道 | passed | `f01-report.json` 的 `passed[12]`；`reconcile-evidence.json` |

## 未运行项与已知限制

主流程之外、**未执行**的事项逐条列在 `f01-report.json` 的 `known_limits`（每条带原因与解除条件），覆盖边界列在 `coverage_boundaries`：

| 未运行项 / 限制 | 原因 | 解除条件 |
| --- | --- | --- |
| **候选采纳的持久化只到 HTTP 层** | 采纳结果从未经独立 GET 读回、从未比对数据库物理行、从未在跨重启场景核对该项目；跨重启证据跑的是另一个项目（Beta 452 parity）；契约自身把本轮范围标为 `verification_scope = http_smoke_only` | 在契约里补一条采纳后的独立 GET 读回断言，并在隔离 PostgreSQL 上对同一个 F01 项目做跨重启比对 |
| **F01-11 的 intent 文本说『项目版本增加到 5（由 F01-18 的读回核对）』，而 F01-18 只断言 spec_revision 与 status** | intent 的陈述强于它覆盖的断言（与 #621 同类）；判定以断言为准 | 按 #621 的形态修契约：补 project_version 断言，或把 intent 改写成它真正覆盖的内容 |
| F01-06c 的 422 `invalid_state` 行为 | 负向 apply 实测拒绝在来源解码阶段，错误码是 404 `not_found`；状态机检查未被执行到 | 票 #656 |
| 模型切换的账本行（`kind=switch`） | `RecordModelSwitch` 没有生产调用点（产品里没有「按项目切换模型」的动作） | 需要先有该产品动作；票 #480 的 `AC-BETA-10` 因此不满足 |
| 浏览器「候选取消」交互 | F01 契约里没有取消节点 | 在契约里增加取消节点并实跑 |
| Word 机器渲染产物（PDF / 截图） | Word COM 三条导出通道均挂起，LibreOffice 未安装 | 另装渲染通道 |
| 第二人独立复现 | 负责人决定本次不再执行，正式豁免 `WAIVED_BY_OWNER`（事实状态仍 `NOT_RUN`） | 票 #489 |
| 合成材料哈希未按 `--material` 登记 | 本跑批未登记；哈希来自 2026-10-26 的 embedding 轮 | 票 #480 |
| 向量化在隔离向量知识库完成 | 重跑目标知识库会软删除其被引用 chunk、使章节 `source_ids` 悬空（负责人已接受该偏离） | 若要求目标库本体向量化，需新授权的一轮 |
| F01 跑批在 Windows + 隔离 SQLite 上执行 | 协议 §1 写的是 WSL2 + 隔离 PostgreSQL；重启读回那一项确实在隔离 PG 上完成 | 若要求全流程在 PG 上跑，需在隔离 PG 环境重跑一次 F01 |

## 这份目录说明什么

- 六件产物来自**同一轮验收**（F01 跑批 `f01-20261025T013341Z-5105ae32`）与它所引用的后续取证轮；**逐件的 head 已在 `runtime-manifest.json` 里登记**（浏览器前端 head `7612f92c…`；embedding 轮 head `d76ff6a7…`）—— 不得把它们读成「同一次、同一 head 的执行」。
- 状态三态按协议 §5：`BLOCKED`（主流程缺依赖或无法继续）、`FAIL`（业务行为违反断言）、`NOT RUN`（没有执行）。总结果由主流程决定，不用平均覆盖。
- 产物已脱敏：令牌、密钥、请求体与响应体不写入本目录（协议 §6）；独立的复核脚本对本目录六件产物逐件做过泄漏形状检查，结果为空。
- 当前权威状态与放行结论见 `docs/BETA_STATUS.md` §22；本目录是它的证据支撑。
