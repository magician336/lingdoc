# 浏览器端到端检查（协议 §2 第 4 步 / §6 第 3 项）

**结论：PASS**（机器判定；仓内证据层口径 `HELD`）—— 真实浏览器 ＝ HTTP ＝ 刷新后读回，**10 项逐项一致、0 处不符**。

| 项 | 值 |
| --- | --- |
| 结论 | **PASS**（三方一致性 `status = PASS`，`consistent = true`，`checked = 10`，`mismatches = []`） |
| 执行时刻 | 2026-10-25T10:34:18Z |
| 绑定的 head | `7612f92c73649a1f7eeaf025e728de233195e76e`（＝ `origin/main`） |
| 后端 | **真实服务进程** `D:\wt\lingdoc-484-evidence\weknora-trackd-20261025-093259.exe`（pid 59008，source_head `c2345229`），API root `http://127.0.0.1:18087/api/v1/lingdoc`，**18087 全程未停未重启** |
| 前端 | **真实 Vite dev server**（`node node_modules/vite/bin/vite.js --port 5173 --strictPort`，worktree `D:\wt\lingdoc-beta-status`，head `7612f92c`），代理目标 `VITE_DEV_PROXY_TARGET=http://127.0.0.1:18087` |
| 浏览器 | **真实 Chrome/Edge（headless=new）＋ CDP**，走**真实登录表单**（邮箱 + 口令）拿到会话 |
| 身份 | 隔离环境 owner `trackd-4ec1487@weknora.local`（tenant 9，role owner），口令只从仓外凭据文件读入，**全程不打印、不写入证据** |
| 被检查的项目 | `0a6c35d0-9f5d-4a70-b9cf-3887179b8cd0`（**就是最终 F01 跑批 `f01-20261025T013341Z-5105ae32` 的那个项目**） |

## 实际执行的命令

```
python D:/wt/lingdoc-485-evidence/scripts/exp_633_browser_check.py
  # 内部依次调用（仓内执行器，脚本未改）：
  #   scripts/acceptance/beta_http_readback.py   的 read_items / model_gated_not_run
  #   node scripts/acceptance/beta_browser_cdp.mjs --url http://127.0.0.1:5173
  #   scripts/acceptance/beta_evidence_parity.py --http ... --browser ... --out parity.json
node D:/wt/lingdoc-485-evidence/scripts/exp_636_browser_shot.mjs http://127.0.0.1:5173 .../delivery-panel.png
```

## 三方快照

| 快照 | 时刻（**取自产物里的 `at` 字段**） | 比对项数 |
| --- | --- | --- |
| HTTP 侧 | 2026-10-25T10:34:07Z | 10 |
| 浏览器侧 | 2026-10-25T10:34:17Z | 10 |
| 刷新后读回 | 2026-10-25T10:34:17Z | 10 |

（三方比对本身的结论落在 `parity.json` 的 `at` ＝ `2026-10-25T10:34:18Z`。）

## 三方一致性结论

- 一致：**10 项全部逐字相同，0 处不符**（`parity.json` → `status = PASS`、`consistent = true`、`mismatches = []`）。

| 比对项 | HTTP 侧 | 浏览器侧 | 刷新后读回 |
| --- | --- | --- | --- |
| `browser_snapshot_digest_matches_http` | `1a61a9e98601` | `1a61a9e98601` | `1a61a9e98601` |
| `snapshot_id_matches` | `snapshot` | `snapshot` | `snapshot` |
| `project_version_matches` | `9` | `9` | `9` |
| `spec_revision_matches` | `1` | `1` | `1` |
| `chapters_in_snapshot_matches` | `2` | `2` | `2` |
| `sources_in_snapshot_matches` | `1` | `1` | `1` |
| `snapshot_check_status` | `passed` | `passed` | `passed` |
| `current_check_status` | `passed` | `passed` | `passed` |
| `browser_export_id_matches_http` | `export-1` | `export-1` | `export-1` |
| `browser_export_sha_matches_http` | `df5549b04426` | `df5549b04426` | `df5549b04426` |

- 浏览器侧**零异常**（`browser_exceptions` 为空）。
- 交付文件的摘要 `df5549b04426…` **就是 Word 人工检查所用的那一份**（`export-196b61e0….docx`，sha256 `df5549b044261b6c30463e63cd4e639f0e7f59d116e27e6e429e571a3865639a`）⇒ 浏览器里看到的交付文件与人工打开检查的文件是同一份。

## 本节的结论项

| 结论项 | 状态 | 证据 |
| --- | --- | --- |
| 浏览器对偶：HTTP 侧快照 | **held** | HTTP 侧在 2026-10-25T10:34:07Z 从真实后端读回 **10** 个比对项 |
| 浏览器对偶：浏览器侧快照 | **held** | 浏览器侧在 2026-10-25T10:34:17Z 从真实前端读回 **10** 个比对项 |
| 三方一致性：浏览器 ＝ HTTP ＝ 刷新后读回 | **held** | 比对 **10** 项，不一致 **0** 项，结论 **PASS** |

> `held` 是仓内证据层（`scripts/acceptance/beta_evidence.py`）对「这一项成立」的记号；`overall_result` 的阻塞集合是 `{failed, blocked, not_run}`，**不含** `held`。

## 哪些动作走 UI、哪些走 API（协议 §2 第 4 步要求注明）

**本轮在真实浏览器里执行的（UI）**：

- 真实登录表单填写与提交（邮箱 + 口令）⇒ 拿到 `weknora_token` 会话；
- 进入真实前端的灵档工作台 `/platform/lingdoc`，页面**真实渲染**出该课题（研究条件版本 1 · 课题版本 9 · 课题成员 1 人 · 两章正文）；
- 打开「交付」面板，**读取**冻结快照、冻结时检查结论、当前检查结论、交付文件 id 与摘要；
- **刷新页面后再次读取**，10 项与刷新前逐字相同（reload 稳定性）。

**由 F01 跑批经正式 API 执行的（UI 未逐一点击）**：

| 交互 | 对应 F01 节点 | 本轮状态 |
| --- | --- | --- |
| 项目 / 资料 | F01-02 / F01-06 / F01-10 | ✅ 已执行（API） |
| 候选预览 | F01-09 `getCandidate` | ✅ 已执行（API） |
| 采纳 | F01-11 / F01-12 `acceptCandidate` | ✅ 已执行（API） |
| 章节编辑 / 确认 | F01-13～F01-15 `saveChapter` / F01-16～17 `confirmChapter` | ✅ 已执行（API） |
| 冻结 | F01-19a～F01-19c / F01-20 | ✅ 已执行（API） |
| 导出 / 下载 | F01-21 `startExport` / F01-22 `getExport` / F01-23 `downloadExport` | ✅ 已执行（API）；DOCX 已下载并做了 Word 人工检查 |
| **候选取消** | — | ❌ **本轮未执行**：F01 契约里没有取消节点，浏览器对偶把「候选采纳/取消的读回」列为**模型门控**面 |

## 未运行项

| 未运行项 | 原因 | 解除条件 |
| --- | --- | --- |
| `browser_candidate_adoption_state_matches` | 候选采纳的读回需要一份**真实生成的候选稿**；本面不跑真实模型（54-Spec 决策 9）。**注意**：候选采纳本身已在 F01-11/F01-12 经 API 真实执行（见上表）。 | 在浏览器对偶里跑真实模型生成候选稿后重跑本节 |
| `browser_candidate_cancelled_state_matches` | 同上；且 **候选取消**这一交互本轮既未走 API 也未在 UI 点击。 | 同上；并在 UI 里实际点击一次候选取消 |

⚠️ 这两条按**执行器自身的设计**（54-Spec 决策 9）属模型门控面，**不降级**「三方一致性」这一结论项（`beta_evidence.py` 只看 `parity.consistent`），但作为**覆盖边界**如实登记。

## 副作用与成本核验（前后对照）

| 项 | 前 | 后 | 结论 |
| --- | --- | --- | --- |
| `lingdoc_model_call_ledger` 行数 | 4 | **4** | 无新增 provider 调用 |
| LLM 调试目录文件数 | 4 | **4** | 无新增模型调用痕迹 |
| JSON 账本 `qwen-beta-sqlite.json` sha256 | `74e86abcad63e3d1…` | **`74e86abcad63e3d1…`** | 逐字节未变（mtime 也未变） |
| JSON 账本 `…json.calls.json` sha256 | `dd39a0cb4bfa2c31…` | **`dd39a0cb4bfa2c31…`** | 逐字节未变 |
| `lingdoc_release_snapshots` 行数 | 2 | **2** | 未新增冻结快照 |
| `lingdoc_export_artifacts` 行数 | 1 | **1** | 未新增导出 |
| 最终项目 `project_version` | 9 | **9** | 工作区未移动 |

> **最强的单条证据**：`state-before.json` 与 `state-after.json` 的 sha256 **完全相同**（`27df80f11333c286…`）⇒ 前后的库状态与账本状态**逐字节一致**。

## 本轮的两处执行方式偏离（如实登记）

1. **取令牌用登录，不是注册**。仓内执行器 `beta_http_readback.py` 的 `register_login()` **总是先注册**，对已存在的邮箱答 **400** 并当作致命错误 ⇒ 它无法复用已存在的账号（`--email` 的帮助文本写着「Reuse this account」）。而最终 F01 项目属于一个**已存在**的 owner 账号，新注册的账号会落在另一个租户、根本读不回该项目。
   ⇒ 本轮改为：用**同一个生产端点** `/api/v1/auth/login` 登录取得令牌，之后**所有比对逻辑仍是执行器自己的未改动代码**（`read_items` / `model_gated_not_run` / `beta_browser_cdp.mjs` / `beta_evidence_parity.py`）。
   ⇒ 这是**执行器的一处缺陷**（`--email` 复用路径不可用），**未在本轮修**（本轮不施工）；建议另开票修 `register_login` 对「邮箱已存在」的容忍。
2. **不跑 `prepare_delivery()`**。F01 跑批已经冻结了 `snapshot-09c6230b…` 并导出了 `export-196b61e0…`（就是 Word 检查过的那一份）。再冻一份会产生第二个快照与第二个导出：导出与快照的对应关系会脱钩，且会把 F01 快照的 `is_current` 翻掉 ⇒ **削弱**本轮其余证据（Word 检查、重启读回）之间的连贯性。
   ⇒ 交付检查本身**仍然被真实调用**：`read_items()` 会 `POST /projects/{id}/checks` 取 `current_check_status`，而该调用是只读的（实测 `project_version` 前后都是 9、交付结论 `passed`、1 条 warning＝保留待核项）。

## 证据索引

目录：`D:\wt\lingdoc-485-evidence\browser-run-20261025\`

| 文件 | sha256（前 16 位） | 说明 |
| --- | --- | --- |
| `parity.json` | `e9aef04a0c00997d` | 三方比对结论（PASS / 10 项 / 0 不符） |
| `http-readback.json` | `6ecb63fd41f350df` | HTTP 侧快照（10 项 ＋ 2 条 not_run） |
| `browser-readback.json` | `769fbd4c0fbf0a9c` | 浏览器侧快照（含刷新后读回） |
| `state-before.json` | `27df80f11333c286` | 执行前的库状态 ＋ 账本状态 |
| `state-after.json` | `27df80f11333c286` | 执行后的库状态 ＋ 账本状态（**与前逐字节相同**） |
| `delivery-panel.png` | `955b889a63f3b07e` | 真实前端「交付」面板截图（人工可复核） |
| `run-summary.json` | `e78bf10832c807fe` | 本轮退出码与前后计数的汇总 |
| `evidence-index.json` | — | 全部产物 ＋ 脚本的 sha256 清单 |

⚠️ 目录里另有一份 `http-readback.log`，内容是「NOT RUN: HTTP side produced no comparable items (http://127.0.0.1:18087/api/v1/lingdoc)」—— 那是**一次失败的执行**留下的残留日志（**不是**结论那一次：结论那一次的 HTTP 侧走的是内联路径，不写 `.log`）。⚠️ **单凭这一行无法区分失败原因**：仓内执行器在「没产出可比对项」与「RunnerError」两条路径上打印的是**同一句**；日志里打印的 base-url 是**正确的** API 根，所以不能把它读成「base-url 写错」。那次失败的真实原因当时写在 `http-readback.json` 的 `not_run[]` 里（`POST /api/v1/auth/register returned 400 (code=1000)`，见 §20.12 的「执行方式偏离」），而该 json 已被后来成功那一次覆盖。按零删除守则这条日志**保留**在此，仅作痕迹；**判定以 `parity.json` 与 `http-readback.json` 为准**。

复核方法：`python D:/wt/lingdoc-485-evidence/scripts/exp_633_browser_check.py`（幂等：它先落 before 快照，再跑三步，再落 after 快照）。

## 截图里能直接看到什么

`delivery-panel.png` 是真实前端的「交付」弹窗，页面上写着：**快照 snapshot（当前内容）**、**冻结于项目版本 9 · 研究条件第 1 版 · 章节 2 章 · 资料 1 份 · 摘要 1a61a9e98601**、**冻结时的交付检查：通过**、**冻结时间 2026/10/25 09:33:53**、**DOCX 已生成，可在本快照下点击"下载 DOCX"**、**export-1 … 摘要 df5549b04426** ⇒ 与上表 HTTP 侧的 10 项**逐字对应**。
