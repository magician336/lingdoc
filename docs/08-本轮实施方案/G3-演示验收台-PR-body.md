# PR #247 body 片段（供父会话在合并后写入）

> **本票不写 PR body。** 以下是给父会话的现成片段，合并到 draft PR #247 的 body 即可。
> 片段里的 SHA / head 必须换成**写入那一刻的当前 head**（本片段记录的是证据执行时的 head）。

---

## 关联

- Spec：**#235 G3 验收台——把 G3 验收矩阵变成可执行的真实链路页面**
- 本票：**#245 G3 验收台-9：验收台自身的验收矩阵与交付**
- 缺口清单：**#246**（保持 open；未关闭前，AC-G3-03 / 08 / 14 / 15 / 16 与 G3.7 不判 `PASS`）

## 实施文档

- `docs/08-本轮实施方案/G3-演示验收台-20261020.md` —— 页面对应关系、跑法、判据口径、**验收台自身的八列验收矩阵（23 行）**、验证命令与结果、已知边界与遗留。

## 当前 head 的证据

| 证据 | 文件 | head |
|---|---|---|
| 本票证据（验收台自身矩阵 + 验证记录） | `docs/08-本轮实施方案/G3-验收台-9-验收台自身验收矩阵与交付证据-20261020.json` | 前端 `643a3fc`（本票提交前）／后端 `7f5fd8c` |
| 真实链路 | `docs/08-本轮实施方案/G3-验收台-2-真实链路证据-20261020.json` | 后端 `7f5fd8c` |
| 变更集生命周期与并发保护 | `docs/08-本轮实施方案/G3-验收台-3-变更集生命周期证据-20261020.json` | 后端 `7f5fd8c` |
| 依赖图与影响任务 | `docs/08-本轮实施方案/G3-验收台-4-依赖图与影响任务证据-20261020.json` | 后端 `7f5fd8c` |
| 稳定化与交付闭环 | `docs/08-本轮实施方案/G3-验收台-5-稳定化与交付闭环证据-20261020.json` | 后端 `7f5fd8c` |
| 阻断与恢复路径 | `docs/08-本轮实施方案/G3-验收台-6-阻断与恢复路径证据-20261020.json` | 后端 `7f5fd8c` |
| 真实模型验收项 | `docs/08-本轮实施方案/G3-验收台-7-真实模型验收证据-20261020.json` | 后端 `7f5fd8c` |
| 整链汇总（7 次运行 / 384 步 / 23 行 / 11 条缺口） | `docs/08-本轮实施方案/G3-验收台-8-整链证据-20261020.json` | 后端 `7f5fd8c` |

> 证据落 `docs/08-本轮实施方案/`，**不写进 Issue**（`docs/agents/issue-tracker.md:54-56`）。
> 结论绑定上表的 head；head 变化后这些结论整体失效。

## 本票的验证命令与结果

| 命令 | 结果 |
|---|---|
| `npm test`（`frontend/`） | exit 0 —— `tests 1451 / pass 1450 / fail 0 / skipped 1 / todo 0` |
| `npm run type-check` | exit 0 —— `vue-tsc --build` 无报错 |
| `npm run build` | exit 0 —— `✓ built in 33.65s` |
| `npm run check-i18n` | exit 0 —— `tests 13 / pass 13 / fail 0` |
| `node C:\Users\ASUS\.minimax\g3-demo-spec\verify-criteria.mjs` | exit 0 —— 判据条目总数 23 / 逐字命中 23 / 未命中 0 |
| `npx tsx scripts/g3demo/run-evidence.ts --live` | exit 0 —— 实时链路 62 步 / 4 条判定，后端 head `7f5fd8c` |
| `npx tsx scripts/g3demo/run-{chain,changeset,dependencies,stabilization,blocked,model}.ts` | 六条全部 exit 0，后端 head `7f5fd8c` |
| `npx tsx scripts/g3demo/run-evidence.ts`（汇总） | exit 0 —— 7 次运行 / 384 步 / 23 行 / 11 条缺口 |

## 状态说明

**PR 仍为 Draft。** 理由：

1. **页面真实模式在浏览器里的交互未被自动验证** —— 仓库无 Playwright / Puppeteer，本轮不新增浏览器 runner。node 脚本实跑证明的是**执行器**在真实后端上的行为，不能替代浏览器里的点击与渲染验收。**需人工浏览器验收；PR 在父会话与用户完成人工验收前保持 Draft。**（矩阵 CON-16 记 `WARNING`）
2. **产品矩阵未闭环** —— G3.2 / G3.3 / G3.4 / G3.5 本轮未判定（`TODO`），AC-G3-03 / 08 / 16 为 `BLOCKED`（缺口 #246），G3.7 为 `NOT RUN`（缺口 #246）。按 `docs/agents/development-rules.md:84`，矩阵出现 `TODO` / `NOT RUN` / `BLOCKED` 时 PR 不能放行。
3. **依赖真实模型的结论**（AC-G3-06 / 14 / 15 模型侧）状态一律记 `WARNING`，不写 `PASS`。
