# LingDoc Beta 首版体验指南（操作者视角）

> 本文面向第一次使用 LingDoc（灵档）的负责人，不是面向开发者。
> 对应实例：2026-10-26 由「Beta 首版体验」会话启动的隔离运行实例，代码基线
> `origin/main = cff38ad3` + 采纳外键修复 `a0abebde`（分支 `beta-first-run`）。
> 实例已用真实模型完整跑通「导入材料 → 生成候选 → 采纳 → 冻结 → 导出 DOCX」全链路；
> 你要做的只是在同一实例上亲自再走一遍。
> 本实例的状态明细见文末「已知限制与当前状态」。

---

## A. 进入系统

### A.1 访问地址与账号

| 项目 | 值 |
| --- | --- |
| 前端（浏览器打开这个） | <http://localhost:5173/> |
| 后端 API（前端自动代理，无需直连） | <http://127.0.0.1:18120/api/v1> |
| 体验账号邮箱 | `beta-explorer@lingdoc.local` |
| 体验账号密码 | `BetaFirstRun#2026` |
| 角色 | 该实例的 Owner（可使用全部功能） |

> 安全说明：该账号只存在于本实例的隔离数据库中（`D:\wt\lingdoc-beta-firstrun\work\lingdoc-beta-firstrun.db`），不与主开发库（`WeKnora`）或任何验收库共享。若需要新账号，登录页点「创建账户」即可自助注册（本实例开放注册）。

### A.2 启动服务（如果已停止）

前置条件：Docker 容器 `WeKnora-redis-dev`（共享 Redis，口令取自主仓 `.env`）在运行；docreader 不强制（Markdown 材料不走它，但解析 PDF/DOCX 需要 `WeKnora-docreader-dev`）。

```bash
# 1) 启动后端（含数据库迁移、全部隔离配置）
bash D:/wt/lingdoc-beta-firstrun/start-server.sh
# 看到 "SERVER_READY http://127.0.0.1:18120" 即成功

# 2) 启动前端
cd D:/code_warehouse/Projects/lingdoc-beta-run/frontend
VITE_DEV_PROXY_TARGET=http://127.0.0.1:18120 npm run dev
# 看到 "Local: http://localhost:5173/" 即成功
```

### A.3 健康检查

```bash
# 后端存活与注册模式（应返回 {"success":true,...}）
curl -s http://127.0.0.1:18120/api/v1/auth/config

# 前端可达（返回 HTML 即正常）
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:5173/

# 日志查看（另开一个终端）
tail -f D:/wt/lingdoc-beta-firstrun/logs/server.log     # 后端应用日志
tail -f D:/wt/lingdoc-beta-firstrun/logs/server.stderr.log  # 队列/启动日志
```

### A.4 重启方法

先停止再启动，两步都用本实例自己的脚本（只碰端口 18120 上的进程，不影响其他会话的服务）：

```bash
bash D:/wt/lingdoc-beta-firstrun/stop-server.sh
bash D:/wt/lingdoc-beta-firstrun/start-server.sh
```

数据（课题、材料、候选、交付快照）全部保存在 SQLite 文件里，重启不丢失；登录状态因 JWT 失效需重新登录。**不要**动 `work/` 目录下的 `.db` 文件。

---

## B. 第一次体验（逐步）

以下按钮/字段名都是页面上真实的中文名，顺序即推荐路径。整条链走完约 15–25 分钟，其中等待模型的部分已标出。

### 步骤 1：登录

1. 浏览器打开 <http://localhost:5173/>，未登录会跳到「登录」页。
2. 填 **邮箱**：`beta-explorer@lingdoc.local`；**密码**：`BetaFirstRun#2026`。
3. 点 **「登录」**。成功后进入「知识库」页，右上角出现用户名 `beta-explorer`。

### 步骤 2：进入灵档工作台

1. 左侧菜单点 **「灵档项目」**。
2. 进入「灵档文稿」工作台（副题：写作 · 来源 · 核查 · 交付）。

### 步骤 3：选择（或创建）课题

- 要看已完成的全流程成果：点 **「打开已有课题」** → 选 **「高寒光伏储能冬季运行优化研究」**（标注「已立项」）。该课题已完成两次真实生成、两次采纳、冻结与 DOCX 导出。
- 要从头体验：在「课题名称」输入框输入新名称，模板保持「两章演示模板 · 1 · 内部演示」，点 **「创建课题」**；然后点 **「研究条件 →」**，填 **研究主题**、**研究目标**，点 **「保存研究条件」**，再点 **「立项并创建章节」**，在确认对话框勾选 **「我已查看本次研究条件和变更，并确认据此立项。」** 后点 **「确认并立项」**。

### 步骤 4：上传研究材料

1. 左栏点 **「上传与项目资料 →」**。
2. 在「上传新材料」区点 **「选择文件」**，选一个文本材料（支持 md / txt / pdf / docx 等；演示用 md 最快）。
3. 在「上传文档确认」对话框点 **「确认上传并解析」**。
4. 等待解析：纯文本/Markdown 约 5–30 秒。材料行内状态依次为「等待处理 → 正在解析 → 处理完成（当前索引阶段已完成）」，可点 **「重新读取材料状态」** 手动刷新。
5. 状态就绪后点该材料行的 **「绑定到当前课题」**。
6. 系统弹出授权（模型使用许可）确认：填写原因后提交，一路确认。**不授权，后续生成会以 422「asset_not_authorized」拒绝**——这是设计行为（资料必须显式许可给模型用）。

### 步骤 5：生成候选稿（真实大模型调用）

1. 在章节编辑区下方找到 **「生成候选稿」** 区。
2. 勾选 **「本次允许使用的资料」** 里刚绑定的材料。
3. 在 **「写作要求」** 里写清要模型做什么（例如：「基于已绑定材料，提炼研究对象、核心约束与待评估策略，分三点作答，每点对应材料原文」）。
4. 点 **「生成候选稿」**。等待约 **10–40 秒**（真实调用 qwen3.8-flash；生成中按钮区会出现「刷新任务状态」）。
5. 成功标志：出现 **「候选稿预览（不会自动覆盖章节）」** 区块，注明「引用 N 条来源 · 待核事项 N 条」。

### 步骤 6：查看候选与来源证据

- 候选正文中的 `[[source:…]]` / `[来源 N]` 标记即可追溯来源。
- 点 **「查看引用 N 原文」** 逐条展开：每条显示定位（如「材料名 · 第 0 段」）、**原文摘录**与状态（「可以指回原文」）。
- 左栏 **「上传与项目资料 →」** 面板里还有 **「资料检索」**：输入检索问题点 **「检索」**，可单独验证命中（混合检索：向量 + 关键词双通道）。

### 步骤 7：采纳或编辑候选

1. 候选预览下点 **「采纳到本章」**。
2. 确认对话框会展示：候选正文、来源、待核事项、**「采纳确认」**（动作/写入目标/确认人/确认时刻/来源标注）与替换范围 diff。
3. 点 **「确认整章替换」**（空章节时按钮为「采纳候选」）。成功后按钮变「已采纳」，章节正文被替换，左栏章节名旁出现「已编辑」。
4. 若要留正式版本：点页头 **「提交为正式版本」**（工作副本 → 正式版本），随后可在「版本」里回看历史。
5. **重新确认当前版本**：交付前需要对每个章节做一次确认（下面步骤 9 会用到）。

### 步骤 8：验证已持久化

- 刷新页面（F5）→ 重新进入「灵档项目」→ 打开同一课题 → 章节正文仍在，即为持久化成功。
- 更严格：完全重启服务（A.4 两步）后再读一次，内容不丢（本实例已实测）。

### 步骤 9：交付（冻结 + 导出 DOCX）

1. 页头点 **「交付」** 打开交付抽屉。
2. 点 **「交付检查」**。若报阻断，按提示处理（常见两类：某章还没有正式版本 → 回编辑区「提交为正式版本」；确认记录不一致 → 章节上点 **「重新确认当前版本」**）。
3. 检查通过后点 **「冻结当前内容」**。若提示「正式变更尚未完成稳定化」，说明还有正式变更未闭环：
   - 打开左栏 **「正式变更与运行 →」**：先对当前变更点 **稳定化**；
   - 若显示影响任务阻断（BLOCK_RELEASE，材料授权类）：回交付抽屉再跑一次 **「交付检查」** 拿到新回执，然后到「正式变更与运行」里用当前回执完成 **Preflight 确认**，再点稳定化，状态会从 BLOCKED 变 STABILIZED。
4. 冻结成功生成「快照 xxxx」卡片。点该卡片的 **「生成 DOCX」**（约 1–3 秒），完成后点 **「下载 DOCX」**，得到 `lingdoc-delivery-xxxxxxxx.docx`。
5. 用 Word/WPS 打开检查：应含两章正文、引用标记 [1][2]、**「引用资料」**（含每条来源的定位与原文摘录）与「待核事项与处理结论」。

### 步骤 10：查看交付历史

交付抽屉里 **「刷新交付历史」** 可列出全部快照与导出产物；每个快照都可以重新生成/下载 DOCX。

---

## C. 如何判断成功（关键操作对照表)

| 操作 | 页面应看到 | 对应 API 应返回 | 独立核验方法 |
| --- | --- | --- | --- |
| 登录 | 右上角用户名；跳转知识库页 | `POST /api/v1/auth/login` → 200，body 含 `token` | 浏览器 F12 Network 看该请求 200 |
| 材料解析+索引 | 材料行「处理完成（当前索引阶段已完成）」 | `GET /api/v1/knowledge/{id}` → `parse_status=completed`；`GET /api/v1/chunks/{id}` 每块 `index_status=ready` | 直接 curl 这两个端点 |
| 检索 | 「资料检索」返回命中列表 | `POST …/knowledge-bases/{id}/hybrid-search` → 200，命中带 `match_type`（0=向量，1=关键词） | 用「disable_vector_match / disable_keywords_match」分别验证两路通道 |
| 生成候选 | 「候选稿预览」出现 | `POST …/generations` → 202；`GET …/generations/{runId}` 轮询至 `succeeded` | 查账本（见下） |
| 采纳 | 按钮「已采纳」；正文替换 | `POST …/chapters/{id}/acceptances` → **201**；422/403 都说明前置条件不满足 | `GET …/chapters/{id}/working-copy` 读回正文与 `source_ids` |
| 正式版本 | 「版本」里出现新版本 | `POST …/working-copy/commit` → 201 | `GET …/chapters/{id}/versions` |
| 冻结 | 「快照 xxxx」卡片 | `POST …/releases` → **201**；422 `context_unstable` = 有变更未稳定化 | `GET …/releases` 列表 |
| 导出 | 「生成 DOCX」→ 产物行 | `POST …/releases/{id}/exports` → 202，`status=verified` + `file_sha256` | 下载后 `sha256sum` 与 `file_sha256` 比对；用 Word 打开 |

**账本独立核验（本实例专属）**：真实模型调用全部记录在隔离库的 `lingdoc_model_call_ledger` 表：

```bash
cd D:/wt/lingdoc-beta-firstrun
python -c "import sqlite3;con=sqlite3.connect('work/lingdoc-beta-firstrun.db');[print(r) for r in con.execute('SELECT kind,provider,model_id,run_mode,input_tokens,output_tokens,status,source_kind FROM lingdoc_model_call_ledger')]"
```

每个 `kind=call, run_mode=real` 的行就是一次真实 provider 调用（含 input/output tokens 与 status）。

**失败后如何恢复 / 是否产生额外费用**：

- 生成失败（422/503）：先看原因（资料未授权 / 模型不可用 / 预算类），修前置后重跑。**只有真正到达 provider 的调用才计费**；被闸门拒绝（授权、参数校验）不计费。以账本表里的行为准：有 `succeeded` 行 = 到达过 provider。
- 采纳 403：`confirmation.confirmed_by` 与登录身份不一致（换账号操作时常见），用当前登录账号重新确认。
- 冻结 422 `context_unstable`：不是错误，按步骤 9.3 走稳定化。
- 所有失败都会在页面通知区与 `logs/server.log` 里留痕，不会静默成功。

---

## D. 已知限制与当前状态（区分口径）

**真实运行并验证通过（本实例 2026-10-26 实测）**

- 登录/注册、租户与成员（Owner）。
- 材料上传（Markdown）→ 解析 → 分块（坐标逐字自洽）→ 真实 Embedding（`qwen3.7-text-embedding-flash`，1024 维，chunk `index_status=ready`）。
- 混合检索双通道：向量通道（match_type=0，含真实查询向量化）与关键词通道（match_type=1，BM25/FTS5）分别独立验证。
- 真实聊天生成 ×2（`qwen3.8-flash`，run_mode=real，账本 2,965 入 / 850 出 token，均 succeeded），候选带可追溯来源。
- 采纳（含显式 confirmation）→ 工作副本 → 正式版本 → 章节确认；重启后读回一致。
- 交付检查 → Preflight 闭环 BLOCK_RELEASE 影响任务 → Owner 稳定化（STABILIZED）→ 冻结（快照 2 章）→ DOCX 导出（sha256 一致，含正文/引用/来源原文/待核结论）。
- 浏览器 UI：登录、知识库列表、灵档工作台、课题/章节/编辑器/交付入口全部真实渲染（有截图证据）。

**已修复并随本实例验证的缺陷**

- 空章节首次采纳必然 500（`lingdoc_candidate_adoptions.chapter_version_id` 外键违约）。修复＝迁移 sqlite 000056 / postgres 000135 + 指针化写入，提交 `a0abebde`（分支 `beta-first-run`，**尚未合入 main**）。
- 实例构建必须带 `sqlite_fts5` Go 构建标签，否则关键词通道建表失败（`no such table: lite_embeddings_fts`）。`start-server.sh` 启动前请按 A.2 的命令构建。

**自动化测试通过、但尚未在真实运行环境验证**

- F01 契约 31 节点中的部分节点（以 `docs/BETA_STATUS.md` §22 的口径为准）；本实例的通过不代表契约放行口径改变。

**真实 Provider 调用成功、业务结果未逐条验证**

- 检索命中分数排序的领域质量（只验证了「来自真实向量通道」与排序稳定性，未做相关性评审）。

**受限于本次范围的 NOT RUN**

- PDF/DOCX/图片材料的真实解析（docreader 容器在运行但本次只用了 Markdown）。
- 多成员协作、撤权（F24 类）场景。
- 微信小程序、mcp-server、Lite 单文件版。
- 第二人独立复现（票 #489 口径）。

**只能演示、尚未完成的功能**

- 「演示模板状态暂不可读」提示（工作台顶部黄条）：模板治理目录读取在演示模板上返回不可读，不影响主链；点「重试」偶发可恢复。
- 已归档的乱码课题「高寒…（乱码）」会出现在课题列表里：那是本次会话早期因 Windows curl 命令行编码问题产生的废弃项目（已归档，不污染数据），可忽略。

---

## E. 停止与恢复

### 停止（只处理本实例，不影响其他会话）

```bash
# 停后端（按端口 18120 定位，带归属校验）
bash D:/wt/lingdoc-beta-firstrun/stop-server.sh

# 停前端（Vite 在前台终端的，直接 Ctrl+C；后台的按 PID）
# Windows: 找到 5173 端口属主并结束
netstat -ano | findstr :5173
taskkill /PID <上面查到的PID> /F
```

### 启动

见 A.2（两条命令）。

### 数据与证据位置（只读）

| 内容 | 路径 |
| --- | --- |
| 隔离数据库 | `D:\wt\lingdoc-beta-firstrun\work\lingdoc-beta-firstrun.db` |
| 上传的原始材料 | `D:\wt\lingdoc-beta-firstrun\files\`、`D:\wt\lingdoc-beta-firstrun\materials\` |
| 本次全部 API 证据（请求/响应 JSON） | `D:\wt\lingdoc-beta-firstrun\evidence\` |
| 导出的交付文档 | `D:\wt\lingdoc-beta-firstrun\evidence\lingdoc-delivery.docx` |
| 后端日志 | `D:\wt\lingdoc-beta-firstrun\logs\server.log`（应用）、`server.stderr.log`（队列/启动） |

### 故障排查速查

| 症状 | 先看哪里 |
| --- | --- |
| 前端打不开 | Vite 终端输出；5173 是否被占用 |
| 登录后接口全 401 | 后端是否重启过而前端缓存了旧 token → 退出重登 |
| 生成一直「刷新任务状态」 | `logs/server.log` 搜 `generation`；provider 侧问题会在 run 的 `error` 字段里 |
| 检索报 FTS5 no such table | 二进制构建缺 `sqlite_fts5` 标签，按 A.2 重新构建 |
| 上传解析卡住 | `logs/server.stderr.log` 里 asynq 队列是否在跑；docreader 仅 PDF/DOCX 需要 |
