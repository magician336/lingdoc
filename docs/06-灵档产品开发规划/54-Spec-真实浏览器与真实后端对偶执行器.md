# Spec：真实浏览器 × 真实后端对偶执行器

> 发布副本：[Issue #452](https://github.com/magician336/lingdoc/issues/452)。
> 定稿依据：[#442](https://github.com/magician336/lingdoc/issues/442) §2.1（编号 **54**）｜研究底稿：`research/new-spec-candidates` @ `9ae5d9fb`（只推分支、未合入 `main`）。
> 施工并行面：**待切**——按 [#441](https://github.com/magician336/lingdoc/issues/441) §0.2，本 Spec 的仓内文档写出来之后，它的施工面才能切。
> 下游：[#451](https://github.com/magician336/lingdoc/issues/451)（53-Spec Beta 首版验收执行器与证据目录——证据目录里要有 `browser-check.md`，**浏览器对偶得先能跑**）。
> 体例参照：`docs/08-本轮实施方案/acceptance/issue196-g4-browser-20261020/`（G4 的浏览器证据）。

## Problem Statement

协议 §2 第 4 步（L18）要求：「在**真实浏览器**核对项目、资料、候选预览与取消、采纳、章节编辑/确认、冻结和下载交互；注明哪些动作通过 UI 执行，哪些通过 API 执行。**只展示成品不能称为整个页面流程通过。**」

这件事现在拿不出证据。

**仓内没有任何 CDP 脚本驱动真实 LingDoc 后端。** 全仓真正用 CDP 驱动的只有 6 个文件：

| 文件 | 行数 | 驱动什么 |
|---|---|---|
| `scripts/lingdoc_mock/browser_smoke.mjs` | 93 | **静态 mock 页**（`http://127.0.0.1:5173/lingdoc-mock.html`）+ mock server，断言 api origin 固定为 `http://127.0.0.1:4010`；文件自述「不连真实后端」 |
| `frontend/tests/workbench-layout.browser.mjs` | 394 | 三栏工作台布局回归，**合成 API** + 真实 SFC 编译挂载 |
| `frontend/tests/ui-safety.browser.mjs` | 303 | 历史 #109 plaintext-editor 夹具，合成 API provider（文件自述「不是当前的连续编辑器验收执行器」） |
| `frontend/tests/editor-slice.browser.mjs` | 286 | 编辑器小切片实机：真实 Edge 无头 + CDP 原生鼠标/键盘/文本输入 |
| `frontend/tests/conditions-safety.browser.mjs` | 345 | 真实 Vue/Tiptap 组件 + **合成 API** |
| `scripts/test_browserskill_extension.mjs` | — | 浏览器扩展测试，非业务链路 |

⇒ **五分之一在真实浏览器里跑，五分之四在合成 API 上跑**，**没有一个连真实后端**。

**G4 那次是真跑过的，但驱动代码从未进仓。** `docs/08-本轮实施方案/acceptance/issue196-g4-browser-20261020/README.md` 完整记录了那次运行：隔离 Edge 实例、独立 profile、CDP `127.0.0.1:9222`、前端 Vite `127.0.0.1:5173`、后端 `127.0.0.1:8082` + 隔离 PG 库；链路上到「注册 → 登录 → 工作台 → 创建课题 → 研究条件 → 立项 → 两章正文 → 提交正式版本 → 确认章节版本 → 交付检查 → 冻结 → DOCX → 刷新重读」；并且留下了**五条一致性断言**（L32–L38）。但 `git log --all --diff-filter=A` 查不到任何浏览器驱动脚本被加入过——**证据在仓内，代码不在**。

后果具体：**凡是需要「界面显示的 == 接口返回的」这一条证据的地方**（上下文包面板、告警规则读回、权限读出口的冻结原因、交付快照与导出哈希），现在都只能靠截图与口述。截图不能被机器判，所以这些验收点写不成断言。

## Solution

用 CDP 驱动**真实前端**对**真实后端**跑完整链路，产出「**浏览器 == HTTP == 刷新后读回**」的**一致性证据**。

三段要相等：浏览器里读到的、HTTP 直接读到的、刷新页面后再读到的——三者一致才说明前端没有在客户端拼影子。

## User Stories

1. As a 验收者, I want the browser check to drive the real backend, so that the result says something about the product rather than about a mock.
2. As a 验收者, I want to see that the browser and the API agree, so that a UI that shows a different scope from the server is caught.
3. As a 验收者, I want to see that a page reload does not change what is shown, so that a client-side shadow is caught.
4. As a 验收者, I want the evidence to record which actions were performed through the UI and which through the API, so that 「只展示成品」 is not passed off as a page flow.
5. As a QA 负责人, I want a deliberately injected inconsistency to turn the corresponding assertion red, so that the assertion is known to be able to fail.
6. As a QA 负责人, I want the same evidence shape as the G4 browser evidence, so that a reader already knows how to read it.
7. As a QA 负责人, I want no tokens, passwords, request bodies or response bodies in the evidence, so that committing evidence does not commit secrets.
8. As a developer, I want the browser-runner and the HTTP-runner to be separate executables, so that the comparison is between two independent observations rather than one process asserting itself.
9. As a developer, I want the existing synthetic-API browser tests left alone, so that adding a real-backend runner does not change what they prove.
10. As a developer, I want the runner to know which port the backend actually listens on, so that it does not silently talk to the wrong process.
11. As a 审阅者, I want the evidence to carry the head it was produced against, so that it can be bound to a build.
12. As a 审阅者, I want the browser evidence to live beside the other acceptance evidence, so that one directory holds a round's evidence.

## Implementation Decisions

1. **沿用 G4 的形态：两次运行 + 事后一致性比对，不做同进程对偶。** G4 那次是「HTTP 侧一次运行 + 浏览器侧一次运行 + 事后比对」，仓内**没有同进程对偶的先例**。同进程要同时起后端 + 前端 + CDP 并让两边状态对齐，复杂度与失败面都大得多；而 G4 的证据体例（`browser-evidence.json` 的 `consistency_assertions`）**本来就是「比对两个来源」的结构**。⇒ 沿用它，不发明新形态。

2. **新增一个编排器，底层是两个独立执行器。** 编排器负责「起环境 → 跑 HTTP 侧 → 跑浏览器侧 → 比对 → 产证据」；两个执行器各自产出一份**可比对**的快照。理由：**G4 的浏览器驱动代码从未进过仓**——能复用的是**断言形状与证据体例**，不是代码。所以本 Spec 要**从零写编排器与两个执行器**，不能指望「照着 G4 改」。

3. **「真实后端」是本 Spec 与现有五个 browser 用例的根本区别，因此要分开命名、分开断言。** 现有五个都是**合成 API**；本 Spec 的浏览器侧必须打**真实后端**。⇒ 编排器与产物**不复用**现有 browser 用例的入口与断言名，避免「合成 API 的 PASS」被读成「真实后端的 PASS」。

4. **一致性断言沿用 G4 的五条形状，并扩到本轮覆盖的范围。** G4 的五条是（`browser-evidence.json` 的 `consistency_assertions`）：`browser_snapshot_digest_matches_http`、`browser_export_id_matches_http`、`browser_export_sha_matches_http`、`project_version_matches`、`snapshot_check_status`。本 Spec 保留这五条**作为形状的先例**，并按 F01 覆盖面增补（正文版本、冻结检查、候选采纳与取消的读回一致）。

5. **「浏览器 == HTTP == 刷新后读回」是三方两两相等，不是两方。** 三个快照：浏览器侧读到的、HTTP 侧读到的、刷新后浏览器再读到的。⇒ 断言数是**两两比较**，不是三个数相等。

6. **端口必须显式处理，不能假设 `APP_PORT` 生效。** `docs/08-本轮实施方案/T15-真实服务启动.md:101` 明写「**端口不受 `APP_PORT` 控制**。监听地址来自 `config/config.yaml` 的 `server.port`（当前 `8080`）」。⇒ 编排器要**读实际配置**或**显式改配置**，并在 `runtime-manifest.json` 里记录后端实际端口与前端 dev server 端口。前端侧固定用 `frontend/vite.config.ts:131` 的 5173 与 `:134-136` 的 `DEV_PROXY_TARGET` 代理链（`VITE_DEV_PROXY_TARGET || FRONTEND_BACKEND_URL || 'http://localhost:8080'`）。

7. **没有「同时起前后端」的单条命令——编排器要自己起。** `Makefile` 的 `dev-start` 只启动**依赖服务**（其自述「仅启动依赖服务」），后端 `dev-app` 与前端 `dev-frontend` 要分别执行。⇒ 编排器自己负责拉起两个进程并管理其生命周期（停止、清理）。

8. **证据边界照 G4：不写令牌、口令、请求体与响应正文。** G4 的 README L40–L45 明确记了这条边界。⇒ 本 Spec 的产物沿用，并加一条守护（出现敏感形态即红）。

9. **不跑真实模型。** G4 那次也不含真实模型调用（README L40）。本 Spec 断言的是**浏览器与后端的一致性**，不是模型输出。⇒ 真实模型链路是 [55-Spec](https://github.com/magician336/lingdoc/issues/453) 与 Beta 协议 §2 的事。

10. **产物落 `docs/08-本轮实施方案/acceptance/` 下。** 与 G4 浏览器证据、T16 closeout 同级；具体目录名由 53-Spec 的证据目录规则决定（`beta-first/<完整SHA>/<运行标识>/`）。本 Spec 产出 `browser-check.md` 与 `browser-evidence.json` 两件。

11. **不引入前端测试框架的 browser 入口。** `frontend/package.json` 的 `test` 是 `tsx --test`，**不跑** `.browser.mjs`（它们要手动 `node tests/xxx.browser.mjs`）。⇒ 本 Spec 的执行器是独立可执行脚本，不改 `npm test` 的语义。

## Testing Decisions

好的测试断言的是**「这个执行器能抓住什么」**——尤其是「它能不能变红」。

- **主接缝 —— 编排器的一次完整运行**（在合成环境上：真实后端 + 真实前端 + 真实浏览器，模型用 fake）：
  - 产出 `browser-check.md` 与 `browser-evidence.json`；
  - 三方快照两两相等：浏览器 == HTTP、浏览器 == 刷新后、HTTP == 刷新后；
  - 证据里记录了 head、浏览器版本与 profile、后端实际端口、隔离库标识；
  - 证据里**注明哪些动作经 UI 执行、哪些经 API 执行**（协议 §2 第 4 步的要求）。
- **负例 —— 断言必须能红**（这是本 Spec 最要紧的一条）：
  - **故意注入一处不一致**（例如让前端显示的快照摘要与服务端返回的差一位）⇒ 对应的一致性断言**变红**；
  - 注入后证据产物**仍然写出来**并标明该断言失败（证据的价值就在于记录失败）；
  - 没有「因为跑不通就跳过断言」的退路。
- **回归**：现有五个 `.browser.mjs` 用例**行为不变**（它们证明的是合成 API 上的东西，不因本 Spec 改变）；`scripts/lingdoc_mock/browser_smoke.mjs` 的断言与输出形状不变。
- **脱敏守护**：产物里出现令牌、口令、请求体或响应正文 ⇒ 红。
- **模式**：**跑** fake 模型模式；**不跑**真实模型调用、真实 WeKnora、真实 DOCX 渲染（那是 Beta 协议 §2 的整条链，由 53-Spec 编排）。
- **先例**：G4 的 `docs/08-本轮实施方案/acceptance/issue196-g4-browser-20261020/` 是**断言形状与证据体例**的先例；`frontend/tests/editor-slice.browser.mjs` 是「真实 Edge 无头 + CDP 原生输入」的**代码**先例；`scripts/lingdoc_mock/browser_smoke.mjs` 是「spawn 无头 Chrome → 读 `DevToolsActivePort` → `GET /json/list` 取 `webSocketDebuggerUrl` → Node 内置 `WebSocket` 直连 CDP」这套**连接**手法的先例。

## Out of Scope

- **浏览器端的功能测试覆盖**——这是**一致性对偶**，不是 E2E 覆盖。功能覆盖是各面板自己的事。
- **视觉回归与像素级比对**。
- **移动端浏览器**。
- **真实模型调用与真实 WeKnora**——见决策 9。
- **真实 DOCX 渲染与 Word 打开**——归 [53-Spec](https://github.com/magician336/lingdoc/issues/451) 与 Beta 协议 §4。
- **把本执行器并入 `npm test`**——见决策 11。
- **改写现有五个 `.browser.mjs` 用例**。
- **前端路由与面板的改造**——归 [52-Spec](https://github.com/magician336/lingdoc/issues/450)。

## Further Notes

- 本 Spec 由 [#442](https://github.com/magician336/lingdoc/issues/442) 定稿编号 **54**。**筛选三条标准**：① 验收点能写成机器可判的断言 ✅（G4 先例同款）② 至少解锁一张下游票（#53、#120 的浏览器检查、#384）✅ ③ 不与现有 G 线 Spec 重叠 ✅。
- **依赖边**：本 Spec **无上游**；**下游是 #53** ⇒ 原生边 `#451 blocked_by #452`（已加）。边档：验收未满足档。
- **G4 的代码不可考**——这是本 Spec 的一条硬事实，写在决策 2 里：能复用的是**断言形状与证据体例**，不是驱动代码。别指望「照着 G4 改」。
- **一条已存在的形态先例值得记**：仓内 `scripts/acceptance/governance_real_http.py:382` 把 G4 的 `G4-BROWSER-01` 记为 `NOT RUN`，理由「No Edge/CDP endpoint was supplied to this HTTP runner.」——**HTTP 侧执行器知道自己没跑浏览器侧**。本 Spec 的编排器要继承这份诚实：谁没跑，证据里就得写没跑。
