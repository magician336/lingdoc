# Issue #40：接口解耦与验收

本次重构保持既有 HTTP 行为和 `lingdoc_*` 表，不将 T01–T16 全产品接真或正式申报验收算作本 Issue 的成果。提交入口为 PR #43；当前提交的执行结果以 PR 的 Testing 表及对应 Actions 日志为准。独立审查、合并与产品验收是不同步骤。

## 分层与替换

- `workspace/contracts.go`：项目/章节和来源应用接口、领域参数及错误。路由注册的 Gin 类型单独位于 `routes.go`，不进入应用接口。
- `workspacecore.Service`：权限、输入校验、预期版本、幂等重放、模板激活和手工编辑待核项继承；不导入 GORM。`Repository.Transaction`/`Transaction` 只提供领域值的存储原语。
- `workspacecore.GORMRepository`：SQL、row、CAS、事务隔离、SQLite 锁的有界重试。操作结果与领域写入原子提交；不在 adapter 里重写另一套业务规则。
- `workspace.SourceService`：先项目授权，再绑定/KB 授权、来源解析和版本复核。handler 只解码、取身份、调用应用接口、映射响应。
- `container.NewLingDocWorkspaceHandler`：唯一生产工作区装配点，连接 GORM、WeKnora 资料/KB 权限和来源 adapter。
- `delivery.ReleaseApplication`/`ExportApplication`：检查、冻结、动态当前性、导出与下载边界。`container.NewLingDocDelivery` 注册到主容器，默认连接 `DOCXRenderer`；存储、当前性和包含来源的访问检查必须由消费方显式提供。缺失时请求该服务会失败，不自动注入内存存储或放行权限。

交付装配不是新增交付 HTTP 实现。已有 release/export 原型此前未挂主路由，本次也不凭空增加未完成的 prepare/export/download 端点。未来运输层直接消费上述端口，持久化提供方须实现 `SnapshotStore`/`ExportStore`，不得用此处测试内存实现冒充生产落库。

## 权限与事务契约

现有 13 条 workspace 路由全部经主线 `registerLingDocWorkspaceRoutes` 注册：读操作（含 retrieval POST）最低 Viewer，写操作最低 Contributor；API Key 都要求 FullAccess。FullAccess 不代替项目成员或来源权限。owner 才能管理成员；不应可见的项目统一隐藏为 not_found。

写入顺序为当前租户成员/项目能力授权 → 按租户、操作者、操作、目标、键读取历史结果 → 同键不同 body 冲突/同 body 精确重放 → 仅新操作校验预期版本 → 条件写入及历史结果同事务提交。撤权后旧键不得取回旧正文。SQL 写竞争只能改变预期版本；spec 锁重试重跑整个事务，复用原键、遵守调用方取消与总预算。

手工章节保存仍保持原实现的来源 fail-closed 边界：带引用或当前章已有引用时拒绝未经来源校验的写入。本次不悄悄放宽该限制，也不声称补齐引用编辑功能。来源层空范围不扩大到全库，部分拒绝不能静默当全部成功。

冻结 digest 不包含动态 is_current；blocked 快照不得导出，渲染失败不得下载。导出创建和下载均重新检查访问；访问 adapter 应覆盖当前来源授权，而不只是项目成员。

## 契约与迁移兼容性

HTTP 请求、响应字段、错误码、URL、前端 adapter 和表结构均未改变。对照文件保持原内容：`contracts/openapi.json`、`scenarios.json`、`workflow.json` 及 `frontend/src/dev/lingdoc-mock` 请求适配。静态 validator 和前端消费测试用来检查这些不变边界，不靠复制一份新 schema 宣称同步。

生产迁移 SQLite `000018_lingdoc_workspace`/`000019_lingdoc_evidence_assets`、Postgres `000097_lingdoc_workspace`/`000098_lingdoc_evidence_assets` 不改写。SQLite API 测试执行原 workspace/evidence migration；Postgres repository 测试执行原 workspace migration，使用独立生成 schema 并在测试后清理该 schema，不触碰业务数据库。认证支持表的测试 AutoMigrate 与 LingDoc 生产 migration 验证分开。

## 分开执行、分开记录

| 层次 | 执行入口与证据 | 验证边界 |
| --- | --- | --- |
| 应用/主线路由 | `go test -race -count=1 -v ./internal/lingdoc/... ./internal/router ./internal/container ./internal/evidence`；LingDoc Interface Integration 的 Real API and database contracts 步骤 | 真实 handler、Service、GORM 存储、JWT 签发/校验、token/key 存储、Auth 和主线注册/角色/API-key 自检；成功创建/读回、冲突、重放、非成员、撤权、资料未就绪 |
| SQLite | workspacecore 持久化/新连接读回，主线真实 API fixture；Workspace Concurrency workflow | 原生产 migration、CAS、撤权前重放保护、手工编辑待核项、独立写者 100 次、锁/幂等回归 10 次 |
| Postgres | `LINGDOC_TEST_POSTGRES_DSN='host=... port=... user=... password=... dbname=... sslmode=disable' go test -race -v -run '^TestPostgresMigrationAndRepositoryContract$' ./internal/lingdoc/workspacecore` | 独立 PostgreSQL 17 测试容器、生产 migration、创建/重放/版本冲突/不可变章、新连接读回/撤权；变量未设明确 SKIP，不算 PASS |
| 前端 | `cd frontend` 后 `npm ci --no-audit --no-fund`、`npm test`、`npm run type-check`、`npm run build` | Linux CI 全量消费者回归；Windows 中 POSIX 路径测试失败单独记录，不作为业务通过；未作浏览器人工走查 |
| 静态契约 | `python -X utf8 docs/08-本轮实施方案/contracts/validate_artifacts.py`；LingDoc Review gate | 25 个操作、56 schema、F01/F02–F22 固定样例与负例；不是运行中的 HTTP、数据库、模型或 DOCX 人工验收 |

真实 API fixture 的 tenant 信息、跨租户 share 查询和外部知识检索结果是明确的合成提供方：它们不是上线组织分享数据库或真实检索模型。工作区/evidence 数据、token/key/成员与请求路径是真实现。测试覆盖当前 share 撤销后拒绝绑定重放/检索，FullAccess key 也不能绕过项目成员。没有真实模型费用或私密资料。

## 可替换性示例

`service_facade_test.go` 的非 SQL、有状态 adapter 执行同一 `Service`，无需改 handler 或规则；验证版本冲突、同键 body 冲突、响应丢失重试、撤权、来源边界及提交失败回滚。它不复制一套 Service 行为进 fake。

`delivery/renderer_docx_test.go` 将同一导出规则分别配 DOCX adapter 和替代 renderer，验证放行、blocked 和撤权下载规则不变；`container/lingdoc_test.go` 验证接口装配与缺失保护端口拒绝。真实 DOCX adapter 只读冻结值，并将已有待核处置传给 T05 renderer。测试替代字节不是实际 DOCX；真正 DOCX 的 ZIP 结构单独核验。

本 Issue 不替代 T05 的第二人 Word/WPS 打开、编辑、保存记录，也不宣称完整业务 Docker F01、真实模型或生产交付存储已完成。回退通过撤销本重构 PR 的代码提交，不删业务表、不重写历史版本。
