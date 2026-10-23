# #129 G5 角色、Scope 与成员生命周期验收矩阵

## 当前修订状态（2026-10-22，成员上下文切出后重绑）

GitHub PR #149 当前候选头为 `1166c525fb6d23696059e2b62f03aab926227244`，包含最新 main `fede87238875219b78baf76f21f220b405ded355`（PR #374 / #268 已合入：成员与 Owner 上下文切出到 `internal/lingdoc/membership`；PR #377 随后更正了 47.15 方案文档的计数，本分支已并入）。PR 保持 Draft/open，未合并。当前候选的 9 项 GitHub Actions 均已成功；其中带隔离 PostgreSQL 17 的后端 closeout、重复 API 契约与 SQLite/PostgreSQL RuleSet HTTP→导出持久化验证，是在代码树逐字相同的上一头 `c1c9d882` 上跑完的（该次推送只多一个方案文档更正，门禁按本次推送自身的差异跳过了 Interface Integration 的重 job），覆盖面差异见 V7。

事务归 membership 上下文（#268 切出后）：邀请接受须在事务内重验项目状态；角色、资料/章节授权、撤权恢复和审计失败均不得部分写入。正式入口继续使用 ProjectCollaborationPanel。

| 本轮核对项 | 当前结果 | 当前证据 | 状态 |
| --- | --- | --- | --- |
| main 窄口整合 | 已适配必需批量授权端口、G2 adapter 与声明式错误；最新 main 的 Spec-48 文档与 #268 上下文切出已包含在 PR 树中 | PR 头 `1166c52`；main `fede8723` | PASS |
| 归档/弃用项目接受邀请 | 预检和事务写入均拒绝；成员、邀请和项目版本不部分改变 | `internal/lingdoc/membership/g5_invitation_lifecycle_test.go` 定向回归，已在本地跑过；归档状态不在存储词表内，竞态改用生产可达的弃用态构造（见「本轮结构适配」） | PASS |
| Admin＋Author、空 Scope | 治理管理员身份不扩大写者职责；空 Scope 不授予章节/资料写权限 | 合成双账号真实 UI；接受后权限说明可见 | PASS（邀请/接受 UI） |
| 章节资料权限 | 章节 ID 不再授权任意资料；当前正式章节绑定及精确 Scope 决定可读，Researcher 需显式资料范围 | `TestG5ResourceBatchAlignsChapterAuthorAndResearcherAssetScopes`（`internal/lingdoc/membership/g5_resource_scope_test.go`） | PASS |
| 撤权后显式恢复 | 移除/重入不恢复已撤销 Scope；恢复仅使用当前显式批准范围 | 当前头专项回归 `scope-restoration-current-head` | PASS |
| 邀请幂等/版本/审计 | 当前邀请版本和项目版本受事务约束；审计失败不部分保存 | `internal/lingdoc/membership/g5_invitation_lifecycle_test.go` 定向回归（幂等/版本/审计恢复、归档竞态三段） | PASS（已列路径） |
| 当前浏览器完整门禁 | 邀请、本人接受、空 Scope UI 已跑；立项与暂停分别停在产品要求的真人确认框，未代勾选或提交 | `pr149-current-review/member-human-confirmation.png`；项目仍为 Draft | BLOCKED（必须人工） |
| 当前候选 CI 与独立复审 | 当前头 9/9 Actions 成功；magician336 独立复审和用户审阅 PR 摘要仍待办 | GitHub PR #149 头 `1166c52`；Run IDs 39684683445–39684684298（上一头 `c1c9d882` 的 Run IDs 39682004255–39682004444 同样 9/9） | CI PASS / 人工待办 |
| 真实外部 KB | 不在本轮合成账号/隔离数据库范围内 | 未配置真实资料服务凭据 | NOT RUN（外部环境） |

PR 描述/摘要仍未更新（本轮新版草稿已备好，等待用户审阅），遵守用户要求先给本人过目。PASS 仅绑定本节列出的自动化与 UI 子路径；真实外部 KB、两项产品真人确认、用户摘要审批及 magician336 独立复审仍未完成。

## 本轮结构适配（#268 成员与 Owner 上下文切出后）

本轮把分支合到最新 main：先合入 `0e60f57b`（PR #374 / #268 已合入，成员与 Owner 上下文切出到 `internal/lingdoc/membership`），随后合入 `fede8723`（PR #377 更正 47.15 方案文档的计数，本分支未改该文档，合入以免 PR 差异显示回退）。

- 冲突面：以 `058637c`（本轮合并前分支所含的 main）为共同祖先，分支改动 55 个文件、main 改动 71 个文件，交集 9 个文件。实际合并产生 3 处冲突：`workspacecore/g5_lifecycle.go`（成员与 Owner 操作已搬走）、`workspacecore/g5_auth_review_regression_test.go`（main 删除并搬进 membership）、`workspacecore/service_facade_test.go`（main 搬走 5 项成员用例）。`workspacecore/g5_policy.go` 与 `internal/router/lingdoc_integration_test.go` 自动合并，已逐处核对未丢断言。
- 处置：`workspacecore/g5_lifecycle.go` 取 main 侧（成员操作已不在该文件）；本 PR 新增的两处「已停止活动的项目拒绝接受」检查迁到 `internal/lingdoc/membership/lifecycle.go`（预检与写入路径各一处）；`g5_auth_review_regression_test.go` 接受 main 的删除，本 PR 新增的 2 项授权用例迁到 `internal/lingdoc/membership/g5_resource_scope_test.go`；`service_facade_test.go` 取 main 侧后追加本 PR 的批量审计用例；`internal/router/lingdoc_integration_test.go` 的全流程路由口改装配成员与 Owner 服务。
- 断言与产品行为未变：成员与 Owner 操作改问成员与 Owner 服务，工作区操作仍问聚合体，断言值一字未改。
- 「已停止活动」的表达变化：`archived` 不在存储词表内（SQLite 与 PG 的 `lingdoc_projects.status` 只允许 draft/active），成员上下文也无法在工作区模块之外替换事务（工作区的记录口断言生产事务类型），因此归档竞态改用生产可达的弃用态（`discarded_at`）构造同一竞态；`archived` 这一分支由新的领域谓词 `core.Project.Archived()` 承载，并有 `TestProjectArchivedRecognizesBothForms` 直接覆盖两种形态。语义不变：预检与写入路径都拒绝已停止活动的项目。
- 本轮未新增数据库 migration，未改动 OpenAPI 契约与前端消费面。

## 历史验收记录（不代表当前 head）

复核日期：2026-10-21。PR [#149](https://github.com/magician336/lingdoc/pull/149) / Issue [#129](https://github.com/magician336/lingdoc/issues/129)。主线基线为 `3b5b835580972cebaba9fcc64b1f8d11df9c6e87`。

已执行测试的产品源码/测试基线：`e5e4ec81d6b426511a0b490f984cac0497bd93ff`，tree `f10383e8bd447851d5cb953940476dfecf1b2c07`。Spec 为 [26-Spec-G5角色Scope与成员生命周期.md](../06-灵档产品开发规划/26-Spec-G5角色Scope与成员生命周期.md)，blob `8f0cd5f2dbe796cacc0914b2808cfe52c246c552`。近期整合候选 `dee8aabdad6925f485e7e71ff569ab7bb38557d5`（tree `59e814d478dac3117cfb0c173f0adc2fc21f4b79`）只同步 #288 的 CI/文档/注释增量，未改变 #129 产品源码或测试；该 head 的 9 项 GitHub Actions 全部 PASS。矩阵本次同步自身属于证据文档，提交后会触发新 head CI；上述 PASS 仅绑定 `dee8aab`。

正式入口沿用 ProjectCollaborationPanel；没有恢复旧 ProjectMembers 入口。资料 Scope 由后端 ResourceRef / 前端 resourceScope.ts 构造；一次资源批量授权使用一个稳定读事务和聚合审计。项目中未绑定的资料与其他项目资料收敛为 not_found；项目内有绑定但超出职能 Scope 的资料为 not_authorized。章节作者只可沿当前明确章节使用资料，空 Researcher Scope 始终为空集。

Workspace 的实际检索消费者把当前章节传给 API；OpenAPI 的 RetrieveSources 同步声明可选 chapter_id。服务端重验当前章节归属和成员 Scope，字段本身不授予权限；无章节的调用不自动获得章节权限。

状态枚举：TODO、PASS、PASS (follow-up)、N/A、WARNING、NOT RUN、BLOCKED。PASS 仅表示列出的自动化路径通过，不表示真实外部资料服务、真人浏览器或负责人最终验收通过。

| 验收项 | 用户可见入口 | 领域不变量/事务边界 | 成功结果 | 失败/权限/恢复 | 持久化与并发 | 当前候选证据 | 状态 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| US1 成员与能力列表 | 正式协作面板、项目读取 | 当前租户/项目成员为准；治理与职能分开 | 当前角色、范围和能力可读 | 非成员拒绝；未知状态显式保留 | 项目/身份更新不得被迟回包覆盖 | V1/V2/V4；23 项真实 SFC 状态/渲染回归 | PASS |
| US2 同租户 active 邀请 | 邀请面板、HTTP | 邀请不能创建租户身份 | 当前同租户有效成员收到邀请 | 跨租户、inactive、缺失用户拒绝 | 版本、邀请、审计与回执原子写 | V1/V2 的真实 Gin/JWT/持久化路由 | PASS |
| US3 治理与职能双轴 | 单人成员编辑 | Owner 唯一；Admin 不自动获得职能 | 合法组合准确保存 | 越权赋权/未知角色拒绝 | 当前项目版本 CAS | V1/V2/V4；角色/范围序列化测试 | PASS |
| US4 章节范围 | 章节 Scope 选择器、写入 | Author 仅可操作获配章节；空范围为空集 | 多选与实际授权一致 | 越权章节拒绝 | 服务端逐次重读当前权限 | V1/V2；typed batch、章节 Author/G2 回归 | PASS |
| US5 资料范围 | retrieval、G2、来源读取 | canonical asset scope；调用前批量校验 | 仅授权资料进入网关/检索 | 缺授权器报 permission_schema_unavailable；拒绝时无摘录 | 一个资源批量事务、聚合审计 | V1；scopeErr 聚合拒绝且 Gateway 调用为 0 | PASS |
| US6 邀请详情 | 收件箱与详情 | 明示当前版本、角色、Scope、期限 | 用户可核对将接受权限 | 过期/已接受/关闭邀请不可操作 | 接受重验当前邀请 | V2/V4 | PASS |
| US7 重发/旧版本 | 重发与接受端点 | 新邀请版本替代旧版本 | 当前版本可接受 | 旧版本 409；同键重试不重加成员 | 邀请版本与幂等键共同约束 | V2 当前真实路由 | PASS |
| US8 暂停/移除 | 成员状态操作 | 理由、版本、当前管理权必需 | 状态即时影响下一请求 | 被暂停/移除者不可读写 | 生命周期变更与审计原子写 | V1/V2 | PASS |
| US9 租户重加入 | 显式恢复入口 | tenant rejoin 不覆盖项目侧决定 | suspended_by_tenant 待显式恢复 | 项目侧 suspended/removed 保持原状态 | 每次重读租户和项目状态 | V2：两类暂停及项目移除均经真实租户 adapter | PASS |
| US10 已撤销 Scope | 移除/恢复/再次访问 | 不恢复已撤销范围 | 合法显式恢复才重新允许 | 项目移除后租户重加入仍 removed 且 Scope 为空 | 不回放旧 token/旧版本授权 | V1/V2；移除清 Scope、租户重加入不复活 | PASS |
| US11 状态可理解 | 正式面板、抽屉 | inactive/suspended/pending 不冒充 active | 实际状态与可执行动作一致 | 未知状态/未确认响应保留草稿 | 项目/身份 epoch 防旧响应覆盖 | V4 的真实 SFC 23/23 | PASS |
| US12 Observer 只读 | 项目页与 API | 可读不意味着可研究/写入/确认/导出 | 合法范围只读查看 | 所有未授权写路径拒绝 | 服务端是最终权限边界 | V1/V2/V4 | PASS |
| US13 版本与审计 | 成员写入/授权决策 | 失败即拒绝；拒绝决策也记录 | 操作者/理由/范围可追溯 | AuthorizeRequest 审计失败返回 audit_write_failed；批量审计失败同样拒绝 | 授权批量与成员写事务回归 | V1；typed audit、聚合审计及 PG 审计列宽回归 | PASS |
| US14 到期/重复接受 | 邀请接受 API | 当前版本唯一；到期先于成功回放 | 有效接受同键重试幂等 | 到期拒绝不改变待接受成员 | 版本与回执不部分写 | V2；到期记录为直接改库构造态，不是真实时钟推进 | PASS |
| HTTP/G2 允许集与拒绝集 | retrieval、任务创建、来源读取 | 章节上下文随任务和实际页面请求传递，不能凭资源串扩权 | 当前章节 Author / 指定资料 Researcher 可用 | 空 Scope、撤权、错误章节拒绝 | 当前成员/资源状态重验 | V1/V6；G2 Chapter scope、batch/HTTP/T09 与前端消费者契约 | PASS |
| JWT 唯一与持久化 | 登录、重加入后的新 token | 同秒签发使用唯一 jti；持久化失败不返回凭证 | 新 token 与已撤销 token 分离 | access/refresh 写入失败正确报错 | 当前实现对第二次写失败有补偿撤销 | V2/V3；真实路由连续 20 次无 401/404 漂移 | PASS |
| SQLite/PG 迁移与审计宽度 | 仓库适配器 | 使用生产迁移，审计 action 不截断业务码 | PG 当前迁移与读回通过 | 阻断规则 dismiss 的长 action 正确存储 | 每轮生成独立 PG schema 并清理 | V1，PostgreSQL 17；不把它称为 PG 全成员 HTTP 验收 | PASS |
| 当前候选真人浏览器 | 实际登录后的成员操作 | 当前构建与实际服务响应一致 | 操作截图/录屏和恢复路径 | 多账号撤权、断网恢复 | 浏览器与服务共同读回 | 本轮只有实际 SFC 渲染/处理器测试；当前候选浏览器 NOT RUN | NOT RUN |
| 当前 PR 候选 GitHub CI | 原 PR 分支 | 自动化结果仅绑定执行 SHA | 9 项 workflow 通过 | 不替代真人浏览器、外部 KB 或人工审查 | `1166c52` 的 Actions 全部成功 | V7；9/9 workflow PASS | PASS |
| 独立代码复审 / 负责人最终验收 | GitHub PR #149 | 评审人复核当前范围与证据 | 负责人确认后再合并 | 旧 head 的 CHANGES_REQUESTED 不等于当前 head 已通过 | reviewer magician336 已请求；新 head 的最终结论待出 | 仍待 magician336 对当前候选独立复审 | NOT RUN |
| 真实外部 KB | 实际资料服务与检索 | 合成检索与真实外部服务分开 | 外部 KB 真检索 | 外部凭据/课题授权另行提供 | 本轮没有连接真实 KB | 真实 KB 搜索与课题试写未纳入本轮 | NOT RUN |

## 本轮执行记录

- **V1（历史候选，本轮未在本机复跑）**：Docker Go 1.26.8，设置指向一次性 PostgreSQL 17 的 LINGDOC_TEST_POSTGRES_DSN，运行 `go test -p=2 -count=1 -timeout=15m -mod=readonly ./internal/lingdoc/workspace ./internal/lingdoc/workspacecore ./internal/router`。结果分别为 PASS（283.505s / 95.223s / 5.477s）。生产 PG migration/repository/audit-width 用例实际执行；路由主体使用隔离 SQLite。本轮候选的 PG 证据以 CI 的 Interface Integration（带一次性 PG service）为准，不把历史候选的 PG 结果当作当前头的证据。
- **V8（本轮候选 `1166c52`）**：本机 go1.27.1 windows/amd64，`go test -count=1 ./internal/lingdoc/... ./internal/router/...` — PASS（workspace 156.267s、workspacecore 38.161s、membership 6.610s、router 1.520s，其余包 ok）；前端 `npm test -- --test-reporter=dot` 全部通过、`npm run type-check` PASS；`gofmt -l` 对改动的 Go 包无输出；`git diff --check` 通过。本机未连 PostgreSQL，PG 专项见 V7 的 CI 结论。
- **V2**：`go test -count=20 -timeout=10m -mod=readonly -run '^TestLingDocRealAPIThroughMainRegistration$' ./internal/router` — PASS（20.091s），20 次真实 Gin/JWT/持久化请求；搜索服务是替身。
- **V3**：`go test -json -count=1 -timeout=10m -mod=readonly ./internal/application/service` — PASS，失败测试数 0。初次整包曾因代理 fake-IP 将公开域名解析为 198.18.* 而触发 SSRF 拒绝；使用加密 DNS 核实的公开 A 记录，仅为临时容器设置 example.com / api.e2b.app host 映射后重跑通过。没有修改或关闭 SSRF 防护。原失败日志与最终日志分开留存。
- **V4**：前端定向回归：projectMembers 7/7、collaborationState 的真实 Vue SFC 23/23、sourceRetrievalContract 3/3；完整 `npm test -- --test-reporter=dot`、`npm run type-check`、`npm run build` 在最后消费者修订后重新运行，全部 PASS。构建从 D 盘真实目录执行（2m34s），保留仓库既有大 chunk 警告。曾失败的 SFC 装载器已改为编译真实 resourceScope 模块，再复跑整套通过。
- **V5**：gofmt 与 `git diff --check` 通过。`1166c525fb6d23696059e2b62f03aab926227244` 的 PR Actions 9/9 PASS；其中 automated gate 不等于浏览器或负责人审批。原始脱敏测试日志保留在本机本轮 outputs 目录；本矩阵提供 SHA、命令和结果供复现，不把本机路径当公开 CI artifact。
- **V7**：GitHub 当前整合候选 `1166c525fb6d23696059e2b62f03aab926227244` 的 Go Lint、Frontend、LingDoc Mock、App、Minimal Acceptance Summary、F01 Runner、LingDoc Review、Interface Integration、Workspace Concurrency 共 9 项全部 success。须区分两头的实际覆盖面：`1166c52` 相对上一头只多了 main 的一个方案文档更正，Interface Integration 的 `integration` 与 `frontend` 两个 job 因此被门禁按本次推送自身的差异跳过（逐 job 的 step 结果为 skipped）；带一次性 PostgreSQL 17 的后端 closeout、顺序 API 契约复跑与 SQLite/PG RuleSet HTTP→导出持久化验证，是在代码面完全相同的上一头 `c1c9d882`（Run ID 39682004444）上跑完并成功的。`git diff --name-only c1c9d882 1166c52 -- '*.go' '*.ts' '*.vue' '*.js' '*.json' '*.sql'` 为空，两个头的被测代码树逐字一致，因此该 PG 证据绑定当前代码状态。不覆盖当前浏览器/外部 KB/独立审批。
- **V6**：最后消费者补丁未改变后端实现；HTTP 提供方新增 chapter_id 转发断言后，`go test -count=1 -timeout=5m -mod=readonly -run '^TestHandlerDelegatesSourceUseCaseAndMapsDeniedDetails$' ./internal/lingdoc/workspace` — PASS（2.120s）。真实前端 API wrapper 的带章节/不带章节请求体、实际 Workspace 调用及 OpenAPI 可选字段约束 3/3 PASS。

## 当前门禁与剩余边界

当前 PR head `1166c52` 的 9/9 GitHub Actions 已通过。真实浏览器已完成邀请、本人接受与空 Scope 核对；立项与暂停操作分别停在必须由项目负责人勾选的确认框，尚未代为提交。真实外部 KB 搜索和 magician336 独立复审仍未完成；不自动关闭 #129、合并或部署。本轮成员上下文切出后，成员与 Owner 操作已迁到 `internal/lingdoc/membership`，本 PR 的 G5 产品行为与断言未变，只是调用归属随上下文迁移。本次矩阵同步只更新验收证据，不改 PR 描述；任何 PR 摘要/正文更新仍须先经用户审阅。

## 结构健康度复评

阶段：PR 修订、合并前诊断；对象是 #129 影响范围内的 H_L / F_R 投影。以下分数沿用先前结构卡绑定的源码基线 `e5e4ec8`、CI 候选 `dee8aab` 与当时 Spec；当前 PR head 已到 `1166c52`，本次没有重新评分。取样范围仍为 workspacecore 角色/Scope 与 typed contracts、SourceService/G2、宿主 user token 服务、协作面板/状态测试、契约与本矩阵。

**历史 H_L=65.6/100，F_R=65.6/100，置信度 B**（可持续开发，但存在明确治理债务；不是对当前头重新评分）。CodeGraph 未发现可用入口，NOT RUN；当前 9/9 CI 已通过，但真人操作仍有两个人工确认点，故不据自动化结果宣告验收完成。无同范围正式 Spec 阶段 20 指标卡，ΔH_L/ΔF_R=N/A；此前近似分数与全局暂估不用于算差值。这是结构诊断，不能解释为功能完成率或合并许可。

| 维度 | H_L 加权分 | F_R 加权分 |
| --- | ---: | ---: |
| 复杂性控制 /20 | 10.6 | 10.6 |
| 模块深度与边界 /20 | 12.0 | 12.0 |
| 演化式设计 /15 | 10.0 | 10.0 |
| 战略执行 /15 | 10.0 | 10.0 |
| 一致性与显然性 /15 | 12.0 | 12.0 |
| 证据、反馈与恢复 /15 | 11.0 | 11.0 |

各指标为 0–5 分；加权规则来自[项目健康度评分协议](../项目健康度评分协议.md)。下表证据索引 V1–V7 见上；各项置信度 B、与上次差值 N/A。

| 维度 | 指标（权重） | H_L | F_R | 证据/结构不足 | 重构方向 |
| --- | --- | ---: | ---: | --- | --- |
| 复杂性控制 | 变化放大 (8) | 3 | 3 | V1/V2；SourceService→G2→core | 收敛 ResearchScope 的构造归属 |
| 复杂性控制 | 认知负荷 (7) | 2 | 2 | ResourceRef 与两轴角色/Scope | 将章节/资料授权上下文集中到服务端值对象 |
| 复杂性控制 | 特例与信息泄漏 (5) | 3 | 3 | V1；404/403/422 契约 | 保持对象不存在与范围拒绝分层 |
| 模块深度与边界 | 接口成本/隐藏收益 (7) | 3 | 3 | 批量授权端口与 G2 adapter | 让入口消费完整决策而非拼策略 |
| 模块深度与边界 | 信息隐藏 (6) | 3 | 3 | 来源存在性/Scope/实时授权分层 | 避免调用方重复理解绑定和授权 |
| 模块深度与边界 | 层次错位 (4) | 3 | 3 | HTTP 只渲染 ClientForm | 沿用领域自述错误形态 |
| 模块深度与边界 | 耦合与透传 (3) | 3 | 3 | Go/TS ResourceRef 仍需协调 | 保持 Go/TS 漂移契约检查 |
| 演化式设计 | 结构不同的候选方案 (5) | 3 | 3 | 沿用当前面板，未建第二套 | 后续先比较边界方案再扩展 |
| 演化式设计 | 增量与回退 (5) | 4 | 4 | 原 PR 祖先/旧 token 回归 | 保持单 Spec、正常快进更新 |
| 演化式设计 | 持续重构 (5) | 3 | 3 | 批量授权与审计失败修订 | 继续减少兼容透传 |
| 战略执行 | 设计投资与债务偿还 (6) | 3 | 3 | 宿主 token 两行写入仍需补偿 | 将 token pair 持久化收进单事务端口 |
| 战略执行 | 路线与依赖一致性 (5) | 4 | 4 | Spec #129 与当前 main | 不恢复旧成员入口 |
| 战略执行 | 变更局部性 (4) | 3 | 3 | 认证/Source/G2/前端消费者跨边界修订 | 认证修订只保留最小必要范围 |
| 一致性与显然性 | 领域术语/文档 (5) | 4 | 4 | 术语表 Scope 定义 | 保持空 Scope 为空集 |
| 一致性与显然性 | 契约与非显然注释 (3) | 4 | 4 | ClientForm；批量事务注释 | 新错误必须自带对外形态 |
| 一致性与显然性 | 代码语义一致性 (4) | 4 | 4 | V1/V2/V3 失败与恢复一致 | 不以能力摘要代替执行鉴权 |
| 一致性与显然性 | 自动化约束 (3) | 4 | 4 | ResourceRef/HTTP/SFC 契约测试 | 保留跨入口与回滚约束 |
| 证据、反馈与恢复 | 未知发现 (5) | 4 | 4 | T09/JWT/DNS 反例被定位 | 保留原始失败与修复证据 |
| 证据、反馈与恢复 | 失败/权限/恢复测试 (5) | 4 | 4 | V1/V2/V3/V4 | 增加真实服务与浏览器覆盖 |
| 证据、反馈与恢复 | CI/观测/回退 (5) | 3 | 3 | V5/V7；CI 通过，浏览器待跑 | 新 head CI 通过后再作 Ready 决定 |

优先级按变化放大收益排序：

1. SourceService / G2 / core 的 ResearchScope 构造仍需共同理解章节与资料边界；将可信任务/章节上下文收进单一服务端值对象，减少入口传递策略知识。回归门槛为现有跨入口允许集/拒绝集与单事务聚合审计。
2. 宿主 token pair 当前仍由两次持久化与补偿撤销组成；引入原子 token-pair repository 操作，减少认证失败恢复规则外露。回归门槛为同秒唯一性、两次写失败、租户撤权和重复签发。
3. Go/TS 资源键与成员生命周期投影仍有跨语言协调成本；保留唯一构造入口及漂移约束，后续只从真实消费者收窄 DTO。认知负荷为 2/5，需要持续治理；没有低于 2/5 的关键指标。
