# Issue #40：接口解耦与验收

本次重构保持既有 HTTP 行为和 `lingdoc_*` 表，不将 T01–T16 全产品接真或正式申报验收算作本 Issue 的成果。提交入口为 PR #43；当前提交的执行结果以 PR 的 Testing 表及对应 Actions 日志为准。独立审查、合并与产品验收是不同步骤。

## 分层与替换

- `workspace/contracts.go`：项目/章节和来源应用接口、领域参数及错误。路由注册的 Gin 类型单独位于 `routes.go`，不进入应用接口。
- `workspacecore.Service`：权限、输入校验、预期版本、幂等重放、模板激活和手工编辑待核项继承；不导入 GORM。`Repository.Transaction`/`Transaction` 只提供领域值的存储原语。
- `workspacecore.GORMRepository`：SQL、row、CAS、事务隔离、SQLite 锁的有界重试。操作结果与领域写入原子提交；不在 adapter 里重写另一套业务规则。
- `workspace.SourceService`：先项目授权，再绑定/KB 授权、来源解析和版本复核。handler 只解码、取身份、调用应用接口、映射响应。
- `container.NewLingDocWorkspaceHandler`：唯一生产工作区装配点，连接 GORM、WeKnora 资料/KB 权限和来源 adapter。
- `workspace.ReleaseApplication`/`ExportApplication` 为带身份的交付应用边界。主容器保留 main 的持久化快照/产物库、文件校验、当前性和来源访问检查；内部 `delivery` 端口不能替代外层授权。`NewLingDocDelivery` 的替代装配入口同样必须提供这些保护，不自动注入内存存储或放行权限。

本次保留最新 main 已有的生成、采纳、确认、检查、冻结、导出和下载入口，不删除 T16 的集成能力，也不增加新产品功能。生产持久化不能用测试内存实现冒充。

## 权限与事务契约

工作区 14 条路由及生成/采纳/确认/交付合计 30 条，全部经主线 LingDoc 注册器声明权限：读操作（含 retrieval/checks POST）最低 Viewer，写操作最低 Contributor；API Key 都要求 FullAccess。FullAccess 不代替项目成员或来源权限。owner 才能管理成员；不应可见的项目统一隐藏为 not_found。

写入顺序为当前租户成员/项目能力授权 → 章节请求来源复核（适用时）→ 按租户、操作者、操作、目标、键读取历史结果 → 同键不同 body 冲突/同 body 精确重放 → 仅新操作校验预期版本 → 条件写入及历史结果同事务提交。撤权后旧键不得取回旧正文。SQL 写竞争只能改变预期版本；spec 锁重试重跑整个事务，复用原键、遵守调用方取消与总预算。

手工章节保存保留最新 main 的引用编辑能力：项目授权之后、历史幂等结果读取之前，复核请求声明的来源；来源依赖缺失不能放行带引用写入，撤权后旧键不能绕过复核。章节读取、生成上下文和确认当前性保留主线行为。来源层空范围不扩大到全库，部分拒绝不能静默当全部成功。

冻结 digest 不包含动态 is_current；blocked 快照不得导出，渲染失败不得下载。导出创建和下载均重新检查访问；访问 adapter 应覆盖当前来源授权，而不只是项目成员。

## 契约与迁移兼容性

HTTP 请求、响应字段、错误码、URL、前端 adapter 和表结构均未改变。对照文件保持原内容：`contracts/openapi.json`、`scenarios.json`、`workflow.json` 及 `frontend/src/dev/lingdoc-mock` 请求适配。静态 validator 和前端消费测试用来检查这些不变边界，不靠复制一份新 schema 宣称同步。

生产迁移 SQLite `000018`–`000024`、Postgres `000097`–`000103` 不改写。数据库契约测试执行这些完整原始 SQL 批次（不按分号拆分），使用独立 SQLite 文件及独立 PostgreSQL schema；Postgres 测试后清理该 schema，不触碰业务数据库。主线真实 API fixture 执行 workspace/evidence migration；认证支持表的测试 AutoMigrate 与完整 LingDoc 生产 migration 验证分开。

## 分开执行、分开记录

| 层次 | 执行入口与证据 | 验证边界 |
| --- | --- | --- |
| 应用/主线路由 | `go test -race -count=1 -v ./internal/lingdoc/... ./internal/router ./internal/container ./internal/evidence`；LingDoc Interface Integration 的 Real API and database contracts 步骤 | 真实 handler、Service、GORM 存储、JWT 签发/校验、token/key 存储、Auth 和主线注册/角色/API-key 自检；成功创建/读回、冲突、重放、非成员、撤权、资料未就绪 |
| SQLite | workspacecore 持久化/新连接读回，主线真实 API fixture；Workspace Concurrency workflow | 原生产 migration、CAS、撤权前重放保护、手工编辑待核项、独立写者 100 次、锁/幂等回归 10 次 |
| Postgres | `LINGDOC_TEST_POSTGRES_DSN='host=... port=... user=... password=... dbname=... sslmode=disable' go test -race -v -run '^TestPostgresMigrationAndRepositoryContract$' ./internal/lingdoc/workspacecore` | 独立 PostgreSQL 17 测试容器、生产 migration、创建/重放/版本冲突/不可变章、新连接读回/撤权；变量未设明确 SKIP，不算 PASS |
| 前端 | `cd frontend` 后 `npm ci --no-audit --no-fund`、`npm test`、`npm run type-check`、`npm run build` | Linux CI 全量消费者回归；Windows 中 POSIX 路径测试失败单独记录，不作为业务通过；未作浏览器人工走查 |
| 静态契约 | `python -X utf8 docs/08-本轮实施方案/contracts/validate_artifacts.py`；LingDoc Review gate | 28 个路径、31 个操作、65 schema、23 个场景；不是运行中的 HTTP、数据库、模型或 DOCX 人工验收 |

真实 API fixture 的 tenant 信息、跨租户 share 查询和外部知识检索结果是明确的合成提供方：它们不是上线组织分享数据库或真实检索模型。工作区/evidence 数据、token/key/成员与请求路径是真实现。测试覆盖当前 share 撤销后拒绝绑定重放/检索，FullAccess key 也不能绕过项目成员。没有真实模型费用或私密资料。

## 可替换性示例

`service_facade_test.go` 的非 SQL、有状态 adapter 执行同一 `Service`，无需改 handler 或规则；验证版本冲突、同键 body 冲突、响应丢失重试、撤权、来源边界及提交失败回滚。它不复制一套 Service 行为进 fake。

`delivery/renderer_docx_test.go` 将同一导出规则分别配 DOCX adapter 和替代 renderer，验证放行、blocked 和撤权下载规则不变；`container/lingdoc_test.go` 验证接口装配与缺失保护端口拒绝。真实 DOCX adapter 只读冻结值，并将已有待核处置传给 T05 renderer。测试替代字节不是实际 DOCX；真正 DOCX 的 ZIP 结构单独核验。

本 Issue 不替代 T05 的第二人 Word/WPS 打开、编辑、保存记录，也不宣称完整业务 Docker F01、真实模型或生产交付存储已完成。回退通过撤销本重构 PR 的代码提交，不删业务表、不重写历史版本。

## 最新 main 同步的本地检查记录

2026-10-02：合并适配基于 PR head `37e1df2c1a4724f96839ae62a4061a551dbb3c34` 与 main `eaa02e9b744b588eb80c4f60c150aff120a8eab3`，不直接写入 main。

- PASS：Windows CGO 下全部 LingDoc 领域包、workspace、router、evidence；主线真实 API 连续 20 次执行。
- PASS：独立 Docker PostgreSQL 17 的完整生产迁移和 repository 契约；SQLite 完整生产迁移批次。未设 DSN 的 SKIP 不计入这个 PASS。
- PASS：前端全量测试重跑 947 个，945 通过、2 个 Windows POSIX 场景跳过、0 失败（本次显式启动 HTTP fixture）；类型检查与生产构建通过；专用本机 HTTP/Axios 测试 7/7、无跳过。
- PASS：静态契约检查及 diff 空白检查。
- PASS：使用 `golang:1.26-bookworm`、CGO、D 盘依赖/编译缓存和可写临时源码副本执行 `go test -race -p 2 -count=1 ./internal/lingdoc/... ./internal/router ./internal/container ./internal/evidence`，全部包通过，退出码 0。运行中实际设置独立 PostgreSQL 17 DSN；没有把数据库缺省 SKIP 算作 PASS。
- 环境失败（单独保留，不计 PASS）：Windows container 包受 DuckDB 原生库链接兼容性影响；第一次 Linux 汇总受缺少 SQLite 开发头文件及只读测试报告目录影响。最终 Linux 运行提供锁定 go-sqlite3 依赖的头文件，并在隔离可写副本运行需要写报告的旧测试，以上问题不再阻止最终汇总。

新工作区、生成、采纳和交付 HTTP 测试通过非数据库的应用接口替代实现验证边界；生产容器显式提供来源/确认/生成和交付保护端口，生成 worker 不再依赖 HTTP handler。机器可读结果见 `Issue40-验证报告.json`。只更新 PR #43 分支；远端当前提交的 Actions 结果应另看 PR，旧 PR CI 不代替本次验证。独立审查、完整产品接真和人工 Word/WPS 验收仍是后续步骤。
