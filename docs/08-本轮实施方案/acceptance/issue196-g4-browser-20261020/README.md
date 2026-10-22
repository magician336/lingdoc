# Issue #196 G4 浏览器链路证据（Edge/CDP）

## 这一份是什么

在隔离的 Edge 实例里，用真实 LingDoc 前端和真实后端跑完同一条链路的浏览器版本，并留下
「浏览器结果 == HTTP 结果 == 刷新后读回」的可核对证据。

- 验收代码 head：`d8229f42820ce8923886c195f1182b9b93ed5550`（tree `707cc0b78c5441b0fcbb5299b435c2eafb1cc6e7`）
- 浏览器：Microsoft Edge 155.0.4283.62，独立 profile，CDP 在 `127.0.0.1:9222`
- 前端：fixture 工作树的 Vite dev server（`127.0.0.1:5173`），`/api` 反向代理到后端
- 后端：当前 head 构建的服务进程（`127.0.0.1:8082`），隔离 PostgreSQL 库 `WeKnora_g4_20261020`
- 身份：本次运行自注册的隔离账号（`g4browser_81379614@example.com`，tenant 10005），密码不进证据

## 怎么跑出来的

1. `scripts/acceptance/governance_real_http.py` 的浏览器对偶：CDP 客户端用 Node 内置
   `WebSocket` 直连 `http://127.0.0.1:9222`，对每个步骤做真实 DOM 操作（`Input.insertText`
   打字、`element.click()` 点按钮），并用 `Network.requestWillBeSent/responseReceived`
   记录真实请求，用 `Page.captureScreenshot` 截图。
2. 链路：注册 → 登录 → 打开工作台 → 创建课题 → 填研究条件 → 立项 → 写正文（两章）→
   提交为正式版本 → 确认章节版本 → 交付检查 → 冻结当前内容 → 生成 DOCX → 刷新页面重读。

## 关键对象

| 对象 | 值 |
| --- | --- |
| 课题 | `f02b8196-655e-447e-828f-53ceb039bcda`（课题版本 7、研究条件版本 1） |
| 章节 | `87cfc688-1126-417f-ae8d-ba5c3dbc8917`（研究问题）、`80568cd4-69d0-47dc-8ac7-288a63e2a423`（研究方案） |
| 快照 | `snapshot-9e55c952f9c535375c94916b86f45615`，摘要 `9eb2c49a5abe…`，冻结时检查 `passed` |
| 导出产物 | `export-7c42a099afe40907813027298b271bbe`，文件 SHA256 `194c0a782cf3…` |

## 一致性断言（全部为真）

- 刷新页面后，浏览器交付面板显示的快照摘要 `9eb2c49a5abe…` 与同会话 HTTP 读回
  `GET /api/v1/lingdoc/projects/{pid}/releases` 的 `snapshot_digest` 一致。
- 浏览器显示的导出产物 `export-7` 与 HTTP 读回 `GET …/exports` 的 `id` 一致，
  文件 SHA256 `194c0a782cf3…` 也一致。
- 课题版本 7、章节 2 章、冻结时检查 `passed` 三项在两侧一致。

## 边界

- 这一份**不包含**真实模型调用：生成候选稿需要可检索来源与已授权资料，
  见 `issue196-g4-fail-20261020.json` 与矩阵 G4-MODEL-01 / G4-FAIL-01 的说明。
- 证据里没有令牌、口令、请求体或响应正文；`browser-evidence.json` 只保留方法、路径、状态码
  与对象标识。

## 文件

- `browser-evidence.json`：步骤、真实 LingDoc 请求清单、一致性断言（已脱敏）
- `01-register-result.png` … `12-after-reload.png`：按链路顺序的截图
