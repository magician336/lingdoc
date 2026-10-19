# PR #247 body 片段

> **本票不写 PR body。** 以下是给父会话的现成片段，合并到 draft PR #247 的 body 即可。
> 片段里的 SHA / head 必须换成**写入那一刻的当前 head**（本片段记录的是证据执行时的 head）。

---

## 关联

- Spec：**#235 G3 验收台——把 G3 验收矩阵变成可执行的真实链路页面**
- 本票：**#245 G3 验收台-9：验收台自身的验收矩阵与交付**
- 缺口清单：**#246**（保持 open；未关闭前，AC-G3-03 / 08 / 14 / 15 / 16 与 G3.7 不判 `PASS`）

## 实施文档

- `docs/08-本轮实施方案/G3-演示验收台-20261020.md` —— 页面对应关系、跑法、判据口径、**验收台自身的八列验收矩阵（23 行）**、验证命令与结果、已知边界与遗留、**审查后修复（第 9 节）**。

## 当前 head 的证据

| 证据 | 文件 | head |
|---|---|---|
| 本票证据（验收台自身矩阵 + 验证记录 + 审查修复清单） | `docs/08-本轮实施方案/G3-验收台-9-验收台自身验收矩阵与交付证据-20261020.json` | 前端 `078f5d2` ／ 后端 `7f5fd8c` |
| 真实链路 | `docs/08-本轮实施方案/G3-验收台-2-真实链路证据-20261020.json` | 后端 `7f5fd8c` |
| 变更集生命周期与并发保护 | `docs/08-本轮实施方案/G3-验收台-3-变更集生命周期证据-20261020.json` | 后端 `7f5fd8c` |
| 依赖图与影响任务 | `docs/08-本轮实施方案/G3-验收台-4-依赖图与影响任务证据-20261020.json` | 后端 `7f5fd8c` |
| 稳定化与交付闭环 | `docs/08-本轮实施方案/G3-验收台-5-稳定化与交付闭环证据-20261020.json` | 后端 `7f5fd8c` |
| 阻断与恢复路径 | `docs/08-本轮实施方案/G3-验收台-6-阻断与恢复路径证据-20261020.json` | 后端 `7f5fd8c` |
| 真实模型验收项 | `docs/08-本轮实施方案/G3-验收台-7-真实模型验收证据-20261020.json` | 后端 `7f5fd8c` |
| **整链证据**（7 段真实运行逐段记账 / 每行判定只取判定它的那一次运行 / 23 行 / 11 条缺口） | `docs/08-本轮实施方案/G3-验收台-8-整链证据-20261020.json` | 后端 `7f5fd8c` |
| **浏览器实跑**（真实模式逐段点击 + 导出回执） | `docs/08-本轮实施方案/G3-验收台-9-浏览器实跑证据-20261020.json` | 前端 `643a3fc`+`5e6e82f` ／ 后端 `7f5fd8c` |

> 证据落 `docs/08-本轮实施方案/`，**不写进 Issue**（`docs/agents/issue-tracker.md:54-56`）。
> 结论绑定上表的 head；head 变化后这些结论整体失效。

## 本票提交

- `0c00d85` — `docs(#245)`：实施文档、PR body 片段、本票证据 JSON（验收台自身 23 行矩阵）
- `438dc0b` — `chore(#245)`：重跑整条真实链路，刷新 G3 验收台 2-8 的证据 JSON
- `88ad461` / `dabade4` — `docs(#245)`：补记本票提交记录、修正快照单测的断言计数
- `5e6e82f` — `feat(#245)`：导出给出**可见回执** + 下载触发加固（由浏览器实跑暴露）
- `b928a1a` — `fix(#245)`：**审查后修复**（判定卡状态口径、AC-G3-04 合取项、徽标口径、**整链证据累计**、`buildChainEvidenceExport`、node `--chain` 模式）
- `078f5d2` — `docs(#245)`：补记审查后修复与整链口径，更新证据与状态表

> `5e6e82f` 与 `b928a1a` 触碰了 `frontend/src/**` 与 `frontend/scripts/**`；其余提交只动 `docs/08-本轮实施方案/` 下的文档与证据 JSON。
> 写入 body 时请把上表的 SHA 换成 **PR #247 合并后那一刻的 head**。

## 审查后修复（独立审查 VERDICT=FAIL → 修复后复验通过）

| 类别 | 问题 | 修法 |
|---|---|---|
| 状态口径 | 规则表里 AC-G3-01/02/04/11 能判 `PASS` 却没有 `verdictId`，判定卡与规则表不同源 | 补齐 `verdictId` + 结构性守护断言 |
| 状态口径 | 判定卡自带 `status` 时汇总体仍按规则表状态写 | 以判定卡状态为准；判定不成立一律不升格 |
| 状态口径 | **AC-G3-04** 判据是合取式，判定卡只判了前半段就写成立 | 判定卡补「服务端给出未受影响理由」合取项；未给出时 `holds=false` + `PASS (follow-up)` + #246 |
| 状态口径 | 判定卡徽标写死 `holds ? PASS : NOT RUN`，把「已判定但有缺口」显示成「没跑」 | 徽标以判定卡自带状态为准（`verdictStatus` / `verdictCardClass`） |
| 状态口径 | G3.6 记 `PASS`，但 `RETRIEVE_CANDIDATES` 类闭环证据本机不可实跑 | G3.6 → `WARNING` + #246 |
| 状态口径 | G3.7 基线是 `TODO`（尚未判定），但这一行本轮不打算判定 | G3.7 基线 → `NOT RUN` |
| 证据口径 | 「导出整链证据 JSON」实际只导出最后一次运行 | 会话累计清单（`g3demo_run_history`，上限 12 段，同 runId 去重，坏数据读成空数组）；矩阵投影支持多次运行 |
| 证据口径 | 跨段累计若把不同项目的步骤揉在一起会串味 | 判定**仍在单次运行内评估**，只在汇总层合并；每行标注「来自运行 `<runId>`」 |
| 证据口径 | 导出体没有整链形态 | `buildChainEvidenceExport`：`runs[]` 逐段记账、`verdicts` 带 `run_id`、head 不一致或任一段 stale → 整体标过期、判定不成立却写 `PASS` → 退回矩阵原状态 |
| 证据口径 | node 侧与页面侧可能走样 | `run-evidence.ts --chain`：**同一个构造函数** + **同一个投影纯函数** |

## 本票的验证命令与结果

| 命令 | 结果 |
|---|---|
| `npm test`（`frontend/`） | exit 0 —— `tests 1460 / pass 1459 / fail 0 / skipped 1 / todo 0`（审查后修复后重跑） |
| `npm run type-check` | exit 0 —— `vue-tsc --build` 无报错 |
| `npm run build` | exit 0 —— `✓ built in 33.65s`（仅既有 chunk 告警） |
| `npm run check-i18n` | exit 0 —— `tests 13 / pass 13 / fail 0` |
| `node C:\Users\ASUS\.minimax\g3-demo-spec\verify-criteria.mjs` | exit 0 —— 判据条目总数 23 / 逐字命中 23 / 未命中 0 |
| `npx tsx scripts/g3demo/run-evidence.ts --live` | exit 0 —— 实时链路 62 步 / 4 条判定，后端 head `7f5fd8c` |
| `npx tsx scripts/g3demo/run-{chain,changeset,dependencies,stabilization,blocked,model}.ts` | 六条全部 exit 0，后端 head `7f5fd8c` |
| `npx tsx scripts/g3demo/run-evidence.ts --chain`（整链口径） | exit 0 —— 7 段真实运行逐段记账 / 23 行 / 11 条缺口 / head `7f5fd8c` |
| **浏览器实跑**（内置浏览器 / 真实后端 `127.0.0.1:8080`） | 逐段点击通过：自建账号 + 各自新建隔离项目；变更集 62 步（含非 Owner 探测）→ AC-G3-01/02 `PASS`；稳定化 60+ 步 → G3.1 `PASS`；阻断恢复 82 步；模型段 3 次真实调用（`lingdoc-chat-deepseek` real，4343.8 / 2324.3 / 3042.3 ms）→ 三行 `WARNING`；导出回执可见；**console 0 error** |
| **浏览器实跑复验**（审查后修复后） | 累计清单恢复生效；AC-G3-04 徽标由 `NOT RUN` 修正为 `PASS (follow-up)`；变更集段新运行 `g3r-mvft6a2i-e45612cb` / 隔离项目 `82d2bd9c-c323-4336-bcaf-d1c97022146b` / head `7f5fd8c` |

## 状态说明

**PR 仍为 Draft。** 理由：

1. **产品矩阵未闭环** —— G3.2 / G3.3 / G3.4 / G3.5 本轮未判定（`TODO`），AC-G3-03 / 08 / 16 为 `BLOCKED`（缺口 #246），G3.7 为 `NOT RUN`（缺口 #246）。按 `docs/agents/development-rules.md:84`，矩阵出现 `TODO` / `NOT RUN` / `BLOCKED` 时 PR 不能放行。
2. **依赖真实模型的结论**（AC-G3-06 / 14 / 15 模型侧）状态一律记 `WARNING`，不写 `PASS`。
3. **受管宿主不落盘浏览器下载** —— 对照实验里普通 `<a download>` 同样不落盘，属宿主环境限制；页面已改为给出**可见导出回执**，落盘证据以 node 脚本为准。（不影响放行，仅作边界说明。）
