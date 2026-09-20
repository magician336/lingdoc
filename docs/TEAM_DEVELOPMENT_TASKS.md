---
title: 国家级课题申报智能体开发人员 PR 实施计划
aliases:
  - 开发团队 PR 计划
  - 课题申报智能体开发计划
tags:
  - weknora
  - agent
  - 课题申报
  - pr-plan
status: active
related:
  - "顶层开发计划（PROJECT_TOP_LEVEL_DEVELOPMENT_PLAN.md）"
  - "技术实施附件（DEVELOPMENT_PLAN.md）"
  - "模板与规则系统设计（06-灵档产品开发规划/11-模板与规则系统设计.md）"
---

# 国家级课题申报智能体：开发人员 PR 实施计划

> 产品依据：[顶层开发计划](PROJECT_TOP_LEVEL_DEVELOPMENT_PLAN.md)。工程约束与现状证据：[技术实施附件](DEVELOPMENT_PLAN.md)。  
> 使用方式：**每一行对应一个可独立合并的 PR**。本计划不分配人员、不含工期；开发者领取一行后，应能明确改什么、依赖什么、如何验证。  
> 实施基调：先完成“已有项目/团队知识驱动”的闭环；高质量外部学术检索是后续的受控补充，不是通用网页搜索。  
> 模板与规则系统的系统契约见 [模板与规则系统设计](06-灵档产品开发规划/11-模板与规则系统设计.md)；阶段 3 中涉及规则求值、问题处置与导出放行的 PR 以该文为准。

> **PR 合并规则**
>
> 一个 PR 只改变一个模块或一个模块的完整纵向切片。它必须包含测试与必要文档；接口未稳定时，先合并契约 PR，再合并实现与界面 PR。禁止在一个 PR 中同时引入新的项目模型、多个文献平台和完整写作工作流。

## 目录

- [0. PR 编写与验证约定](#0-pr-编写与验证约定)
- [阶段 0：领域契约与工程基线](#阶段-0领域契约与工程基线)
- [阶段 1：项目知识工作区与最小写作闭环](#阶段-1项目知识工作区与最小写作闭环)
- [阶段 2：受控高质量学术检索](#阶段-2受控高质量学术检索)
- [阶段 3：申报协作、审校与材料包](#阶段-3申报协作审校与材料包)
- [阶段 4：生产治理与机构接入](#阶段-4生产治理与机构接入)
- [附录：现有代码 seam](#附录现有代码-seam)

## 0. PR 编写与验证约定

### 0.1 每个 PR 描述必须包含

```text
目标：解决哪一个用户可见问题。
模块：只影响哪个模块及其 interface。
改动范围：新增/修改的目录或文件。
依赖：必须已合并的 PR ID。
契约：请求、响应、事件、数据不变量或 UI 状态。
验证：自动化命令、fixture 与人工演示步骤。
回退：feature flag、兼容迁移或可关闭入口。
```

### 0.2 通用合并门槛

- [ ] PR 不改变其他模块的内部存储；跨模块只经已声明的 interface 调用。
- [ ] 所有项目、资料、证据、会话和输出都带 tenant/project 权限检查；无权限不得返回存在性信息。
- [ ] 自动生成内容包含资料依据或明确的“待核实/知识不足”状态。
- [ ] 后端 PR 至少执行相关包测试；跨模块或迁移 PR 执行 `go test ./...`。
- [ ] 前端 PR 至少执行 `npm run type-check`、`npm test`、`npm run build`（在 `WeKnora/frontend/`）。
- [ ] 涉及路由/模型的 PR 更新 OpenAPI/接口说明、最小请求样例和错误语义。
- [ ] 涉及数据库的 PR 有前向兼容迁移与本地升级/回退说明；不修改或删除既有用户数据。

### 0.3 建议的 PR 标签

`domain-contract`、`backend`、`frontend`、`migration`、`integration`、`security`、`test`、`ops`。标签只用于检索，不代表人员或优先级。

## 阶段 0：领域契约与工程基线

目标：在动核心代码前固定“项目知识优先”的领域模型、质量规则和验收样本，使后续 PR 共享同一语言。

| PR ID / 建议标题 | 模块与改动范围 | 依赖 | interface / 完成标准 | 验证 |
|---|---|---|---|---|
| PR-000 `docs: define research-project domain contracts` | 新增 `docs/plans/research-project-domain.md`；更新本笔记链接 | 无 | 定义 `Project`、`ProjectMember`、`ProjectAsset`、`EvidenceCard`、`ResearchClaim`、`WritingTask`、`ChapterVersion` 的责任、关系、权限与删除语义；同时固定 `TemplateProfile` 与 `ValidationIssue` 的身份：问题身份为「规则标识 + 目标对象 + 对象版本」，跨写作期检查与导出前检查两个级别。 | 文档评审：任一任务都能说明其 project、资料范围、引用与确认状态；任一问题都能回溯到具体规则与对象版本。 |
| PR-001 `test: add proposal-domain fixtures and acceptance cases` | 新增 `testdata/proposal/`、相关 Go fixture/helper | PR-000 | 提供三类脱敏课题样本；覆盖有依据、知识不足、错引、跨项目越权、敏感资料五类期望。 | `go test ./...`；fixture 可被后续检索、Agent、API 测试复用。 |
| PR-002 `docs: define academic-source policy contract` | 新增 `docs/plans/academic-source-policy.md` | PR-000 | 明确来源目录、授权状态、质量规则版本、允许字段、访问限额、引用和停用语义；不指定未经授权的平台。 | 文档评审：能判断一个来源为何可接入/为何被拒绝。 |
| PR-003 `chore: add proposal feature flags and test configuration` | `config/`、`.env.example`、feature flag helper、测试配置 | PR-000 | 增加项目工作区、学术检索、申报协作等独立开关；默认不改变现有 WeKnora 行为。 | 启停开关的配置测试；`go test ./...`。 |

## 阶段 1：项目知识工作区与最小写作闭环

目标：用户能将已有项目资料置入工作区，基于这些资料检索、查看证据、生成初稿并保存版本；此阶段不依赖外部学术平台。

### 1.1 项目与资产模块

| PR ID / 建议标题 | 模块与改动范围 | 依赖 | interface / 完成标准 | 验证 |
|---|---|---|---|---|
| PR-101 `feat(project): add project workspace domain and migration` | 新增 `internal/types/research_project.go`、`internal/application/repository/research_project*.go`、`migrations/*research_project*` | PR-000, PR-003 | 提供创建、读取、归档 project 的小 interface；所有记录有 tenant、创建者、状态和审计字段。 | repository 测试；全量 `go test ./...`；迁移从空库和已有库均可执行。 |
| PR-102 `feat(project): add member and project-access enforcement` | project member repository/service；`internal/middleware/` 或现有 access seam | PR-101 | `ProjectAccess` 统一决定成员能否查看/编辑项目资产；禁止 handler 各自拼权限条件。 | 正反权限测试：跨 tenant、跨 project、只读成员、已归档项目。 |
| PR-103 `feat(project): expose workspace REST contract` | `internal/handler/`、`internal/router/`、OpenAPI 文档、handler tests | PR-101, PR-102 | 提供 project 创建/列表/详情/成员管理的 `/api/v1` 契约；错误遵循既有认证/RBAC 语义。 | handler 测试；OpenAPI 生成/契约测试；curl 样例。 |
| PR-104 `feat(project): link existing knowledge assets to project` | 新增 project-knowledge 关联 repository/service；复用 knowledge handler/service | PR-101, PR-102 | 资料以“引用/关联”进入 project，不复制原始文件或重建索引；保留 knowledge/document 版本和项目可见性。 | 用已有知识库创建关联；检查未授权资料无法关联或枚举。 |
| PR-105 `feat(project): add evidence card domain` | `internal/types/evidence_card.go`、repository/service、迁移 | PR-101, PR-104 | 证据卡保存 source、原文位置、摘要、适用主张、确认状态和版本；不保存脱离来源的孤立结论。 | CRUD/权限/版本测试；从项目资料创建证据卡的集成测试。 |

### 1.2 已有知识检索与引用模块

| PR ID / 建议标题 | 模块与改动范围 | 依赖 | interface / 完成标准 | 验证 |
|---|---|---|---|---|
| PR-106 `feat(project-rag): add project-scoped retrieval adapter` | 新增 `internal/application/service/projectrag/`；适配 `session_knowledge_qa.go` 与 `retriever/` | PR-104, PR-105 | `SearchProjectKnowledge(projectID, query, options)` 只检索项目关联资料，复用既有 chunk/rerank/citation，不创建第二条索引写入链路。 | fixture 检索测试：命中当前项目资料；无项目资料时返回知识不足。 |
| PR-107 `feat(project-rag): persist evidence-backed answer metadata` | project answer/evidence repository；会话/消息 metadata adapter | PR-106 | 回答记录使用的 evidence card/chunk/version 与知识层次；回答可从项目页面重新定位证据。 | 回答→证据卡→原文位置的集成测试。 |
| PR-108 `feat(api): add project knowledge query endpoint and SSE events` | chat/project handler、`routes_chat.go`、SSE event model、OpenAPI | PR-106, PR-107 | 请求必须指定 project；事件含 `project_id`、引用与 `knowledge_insufficient` 状态；沿用既有 SSE stop/continue 语义。 | handler/SSE replay 测试；断线后重连能恢复引用。 |
| PR-109 `feat(web): add project workspace shell` | `frontend/src/views/`、`frontend/src/api/`、router、i18n | PR-103 | 实现项目列表、项目主页、成员与资料入口；不新增独立认证或状态系统。 | 前端 typecheck/test/build；手动演示创建、打开和无权限访问。 |
| PR-110 `feat(web): show project assets and evidence cards` | project workspace 组件、API client | PR-104, PR-105, PR-109 | 用户可查看关联资料、处理/可用状态、证据卡、来源与确认状态。 | 组件测试；用 fixture 项目演示证据跳转。 |
| PR-111 `feat(web): add evidence-backed project chat` | 复用 `useChatStreamHandler.ts`，新增 project chat view/API adapter | PR-108, PR-109 | 回答区显示引用、知识层次、知识不足；不得把无证据回答伪装为项目事实。 | 前端流式测试；浏览器断线重连与引用渲染验证。 |

### 1.3 首批写作任务纵向切片

| PR ID / 建议标题 | 模块与改动范围 | 依赖 | interface / 完成标准 | 验证 |
|---|---|---|---|---|
| PR-112 `feat(task): add writing-task contract and persistence` | 新增 `internal/types/writing_task.go`、service/repository、迁移 | PR-101, PR-105 | 任务包含 project、类型、资料范围、输出约束、待解决问题、状态与结果版本；首批只支持只读资料任务。 | 状态/权限/资料范围测试。 |
| PR-113 `feat(agent): add project-knowledge writing task adapter` | `internal/agent/`、工具 registry、projectrag adapter | PR-106, PR-112 | 为“文献综述提纲、研究问题、章节初稿”提供受控任务卡；Agent 只能读取指定 project/evidence。 | Agent 集成测试：无资料时拒绝编造；有资料时返回带依据草稿。 |
| PR-114 `feat(web): add writing task create-review-save flow` | project view、任务卡 UI、草稿编辑/保存 UI | PR-111, PR-112, PR-113 | 用户选择资料和约束后创建任务，查看依据、编辑结果并保存为项目草稿版本。 | 前端 E2E：资料→任务→草稿→人工修改→保存。 |
| PR-115 `test(e2e): verify existing-knowledge-first proposal slice` | 后端集成测试、前端 E2E、`testdata/proposal/` | PR-114 | 验证端到端路径与“已有知识优先、无依据明确提示、无权限不泄露”的产品承诺。 | `go test ./...`；前端 CI 命令；E2E 报告随 PR 附上。 |

> **阶段 1 合并闸门**
>
> 仅用已有资料，试点用户已能完成“项目工作区 → 证据支持问答 → 初稿 → 人工编辑保存”。在此之前不得合并任何面向普通网页或多平台的学术检索 PR。

## 阶段 2：受控高质量学术检索

目标：在知识不足、用户请求扩展或需要核验时，以可配置规则检索一个已授权来源；该模块是对项目知识的补充。

| PR ID / 建议标题 | 模块与改动范围 | 依赖 | interface / 完成标准 | 验证 |
|---|---|---|---|---|
| PR-201 `feat(academic-source): add source catalog domain` | 新增 `internal/application/service/academicsource/`、types/repository/migration | PR-002, PR-003 | `AcademicSource` 包含许可状态、可用字段、配额、启停与审计信息；所有调用先通过目录解析。 | catalog CRUD/权限/停用测试。 |
| PR-202 `feat(academic-source): add quality-rule versioning` | quality rule types/repository/service | PR-201 | 规则按机构/学科版本化，支持期刊/会议质量、年份、语种、收录和同行评审条件；每次查询记录 rule version。 | 规则匹配/版本回放测试。 |
| PR-203 `feat(academic-source): add provider interface and fake adapter` | provider interface、fake adapter、contract tests | PR-201 | Provider 只暴露检索与元数据读取的最小 interface；fake 用于 CI，不依赖真实凭证。 | provider contract tests；不配置凭证时测试可稳定通过。 |
| PR-204 `feat(academic-source): integrate first authorized provider` | 新增单一 provider adapter、配置/secret reference | PR-203 | 仅接入已确认许可的平台；返回 metadata/摘要/授权全文链接，不绕过订阅、付费或平台限制。 | 沙箱/测试账号集成测试；限额、无权限、超时和停用来源测试。 |
| PR-205 `feat(academic-search): add policy-enforced search service` | `internal/application/service/academicsearch/`、质量过滤、审计 adapter | PR-202, PR-204 | `SearchAcademic(project, query, ruleVersion)` 只接受 catalog 来源，过滤后返回来源、质量标签、可用性、检索时间；无命中不能回退到普通网页。 | source/quality/权限/无命中/平台异常测试。 |
| PR-206 `feat(agent): add academic-search task tool` | Agent tool registry、task card adapter、approval/policy seam | PR-205, PR-113 | 仅当资料范围声明可补充外部证据时可调用；结果必须标为外部文献，不能覆盖既有项目事实。 | Agent 选择/禁用/结果标记测试。 |
| PR-207 `feat(project-rag): add internal-first evidence composition` | projectrag adapter、evidence composition model | PR-106, PR-205 | 答案先组织项目/团队证据，外部结果作为补充/核验层；输出清楚区分各层证据。 | 混合 fixture：验证内部证据优先级与引用归属。 |
| PR-208 `feat(project): import selected academic results as evidence cards` | project/evidence service、学术结果导入 handler | PR-105, PR-205 | 用户明确选择后才导入；记录检索式、筛选、来源、规则版本、授权范围和导入时间。 | 导入/撤销/重复导入/无权导入测试。 |
| PR-209 `feat(web): add controlled academic search and import UI` | project workspace 检索页、source/quality 状态组件 | PR-205, PR-208 | 提供学科/规则筛选、来源/质量标签、无全文提示和导入证据卡入口；不展示泛网页入口。 | 组件测试；fixture/假 adapter 演示。 |
| PR-210 `test(e2e): verify internal-first academic enrichment` | E2E、fixture、平台 adapter contract suite | PR-206, PR-207, PR-209 | 验证已有知识先被使用、外部资料明确标注、无质量命中不降级、平台异常不破坏项目问答。 | 后端/前端 CI；E2E 报告。 |

> **阶段 2 合并闸门**
>
> 只能从一个已授权来源完成可追溯的检索、筛选与导入。新增第二个平台必须复用 PR-203 的 provider interface，并以独立 PR 合并。

## 阶段 3：申报协作、审校与材料包

目标：把任务卡与聊天结果沉淀为多人共同维护的申报材料，而不是散落的生成文本。

| PR ID / 建议标题                                                   | 模块与改动范围                                            | 依赖                     | interface / 完成标准                                    | 验证                                |
| -------------------------------------------------------------- | -------------------------------------------------- | ---------------------- | --------------------------------------------------- | --------------------------------- |
| PR-301 `feat(template): add proposal-template domain`          | template types/repository/service、迁移、管理 handler    | PR-101                 | 模板描述章节结构、必要字段、材料清单和规则集；**规则集版本化并记录哈希**，每条规则带稳定标识、判定方（可复算归规则 / 语义归模型）与严重度。模板可版本化，项目固定使用一个已发布版本。**本 PR 只定义规则，不求值、不处置问题**。            | 模板版本/规则集哈希回放测试；换模板时同名字段按名保留、不静默清空的测试；权限/项目绑定测试。                   |
| PR-302 `feat(chapter): add project chapter and version domain` | chapter types/repository/service、迁移                | PR-101, PR-301         | 章节有内容、引用、状态、作者和版本谱系；支持创建、读取、比较、回退。                  | 版本不可变/回退/权限测试。                    |
| PR-303 `feat(web): add proposal outline and chapter editor`    | workspace 前端、chapter API client、编辑器组件              | PR-302                 | 用户从项目模板生成章节树，编辑并保存版本；界面显示证据/任务关联。                   | 前端 typecheck/test/build；手动版本比较演示。 |
| PR-304 `feat(collaboration): add comments and project tasks`   | comment/task types/repository/service、前端列表         | PR-302                 | 批注和待办绑定章节或证据卡；包含状态、创建者、处理记录，不存为无归属聊天文本。             | CRUD/权限/状态转换测试。                   |
| PR-305 `feat(agent): add proposal writing task cards`          | task adapter、Agent prompt/template、chapter adapter | PR-113, PR-301, PR-302 | 为研究基础、文献综述、研究设计、创新点、摘要提供明确输入/资料范围/输出约束；只生成候选章节版本。   | 每类任务 fixture 评测；验证不改写未授权/锁定内容。    |
| PR-306 `feat(agent): add evidence and consistency reviewer`    | review service、Agent tool/prompt、issue model       | PR-302, PR-305         | 定义统一问题实体的求值与处置：规则类与模型类问题产出同一结构，含规则标识、判定方、严重度、目标对象与对象版本；身份为「规则标识 + 目标对象 + 对象版本」，与求值级别无关。实现两种处置语义——**误报删除对任一严重度开放（含阻断级），豁免只对警告与提示级开放**；删除跨求值级别生效，目标对象换版本后问题重新出现，不得成为永久静音。模型类规则默认从警告级起步。检查研究问题—文献—方法—创新点—预期成果的一致性、引用缺失与待核实项；输出 issue，不自动通过。 | 有意构造矛盾/错引 fixture 的评测测试；规则类与模型类问题同列表渲染、同处置入口的测试；误报删除跨级别继承与换版本后复现测试。 |
| PR-307 `feat(agent): add constrained polishing task`           | polishing task adapter、diff model                  | PR-302, PR-305         | 支持保留观点、不改事实、术语一致、可选结构重排；每次输出可比较 diff 与引用变化。         | 文本 diff/保留约束/引用不丢失测试。             |
| PR-308 `feat(material-pack): build confirmed proposal package` | material pack service/handler、导出 adapter           | PR-301, PR-302, PR-304, PR-306 | 仅收集负责人确认的章节；汇总附件、未闭环 issue、版本和资料依据。同时实现**导出前检查**：把 PR-301 的规则集在冻结快照上重跑一遍（复用 PR-306 的求值器，不得另写一套），只有无阻断问题才允许固定快照并导出；阻断级问题不接受豁免。 | 已确认/未确认/权限受限材料的打包测试；规则集在快照上重跑与写作期结论一致的测试；误报删除跨级别继承、阻断级不可豁免的测试。 |
| PR-309 `feat(web): add review and final-confirmation flow`     | 审校面板、issue 列表、材料包预览/确认页                            | PR-306, PR-307, PR-308 | 导出前展示未确认主张、缺失引用、权限限制与模板缺口，要求负责人确认。                  | 前端 E2E：创建 issue→修复→确认→导出预览。       |
| PR-310 `test(e2e): complete proposal collaboration scenario`   | E2E、三类试点 fixture、用户路径文档                            | PR-309                 | 从资料到材料包验证协作、版本、错误恢复、权限变更与证据追溯。                      | 完整测试报告；真实/脱敏试点演示记录。               |

## 阶段 4：生产治理与机构接入

目标：在核心课题工作流被验证后，补齐可恢复执行、机构治理、观测、安全与发布能力。RuoYi 仅通过受控管理接口接入，不参与研究者主路径。

| PR ID / 建议标题                                                               | 模块与改动范围                                                                         | 依赖                                             | interface / 完成标准                                                  | 验证                                           |
| -------------------------------------------------------------------------- | ------------------------------------------------------------------------------- | ---------------------------------------------- | ----------------------------------------------------------------- | -------------------------------------------- |
| PR-401 `feat(agent-run): persist long-running task state`                  | 新增 `internal/application/service/agentrun/`、repository/migration、worker adapter | PR-305                                         | 长任务保存状态、步骤、取消请求与幂等键；不依赖仅 Redis 的事件回放作为唯一状态。                       | worker 中断/接管/取消/重复请求测试。                      |
| PR-402 `feat(agent-run): add durable event replay and recovery`            | event store/outbox、SSE adapter、前端恢复 adapter                                     | PR-401                                         | SSE 断线可按序重放；执行故障在安全边界恢复；工具副作用不重复。                                 | 断线、重连、worker kill、重复投递集成测试。                  |
| PR-403 `feat(identity): add institution OIDC and organization mapping`     | auth/middleware、organization mapping、前端登录 adapter                               | PR-102                                         | 将机构身份映射到 tenant/project 权限；不引入第二套用户真相。                            | 登录、邀请、离职禁用、跨组织越权测试。                          |
| PR-404 `feat(admin): add private admin BFF contract`                       | `internal/api/admin/`、私网路由、审计事件                                                 | PR-403                                         | 管理动作经私网、短期服务身份和审计完成；不直接暴露数据库/worker。                              | service identity/错误 audience/审计字段测试。         |
| PR-405 `feat(ruoyi): integrate root governance console`                    | 独立 RuoYi adapter/页面、Admin BFF client                                            | PR-404                                         | 支持组织、规则、来源、使用量与审计治理；管理端不默认读取研究原文。                                 | BFF 契约测试；权限/脱敏 UI 验收。                        |
| PR-406 `feat(observability): add proposal workflow telemetry`              | OTel/Langfuse instrumentation、metrics/dashboard definitions                     | PR-115, PR-210, PR-310                         | 按 request/project/task/source 观察成功率、时延、Token、失败与引用质量；日志不含敏感原文/密钥。 | metrics schema 测试；trace 从 UI 请求关联到 Agent/检索。 |
| PR-407 `feat(ops): split runtime roles and production health checks`       | Docker/Helm、API/worker role config、`/livez`/`/readyz`                           | PR-401                                         | API、文档 worker、Agent worker 可独立运行/扩缩容；就绪检查验证必要依赖。                  | Helm template/lint；容器 smoke test；优雅终止测试。     |
| PR-408 `feat(security): enforce secrets, egress and data-lifecycle policy` | secret reference、NetworkPolicy、外部来源 egress、删除/导出审计                              | PR-204, PR-403                                 | 密钥不进入 Git/日志/模型上下文；学术来源出网受控；资料删除/导出可追踪。                           | secret scan、越权/SSRF/删除流程测试。                  |
| PR-409 `ci: add proposal PR quality gates`                                 | `.github/workflows/`、测试脚本、OpenAPI/SDK/RAG eval gates                            | PR-115, PR-210, PR-310                         | 每类 PR 自动执行对应 Go、前端、契约、fixture 评测、安全/镜像/Helm 检查。                   | 在一个样例 PR 上验证失败门禁确实阻止合并。                      |
| PR-410 `test: run production readiness and recovery suite`                 | 压测/故障注入脚本、runbook、验收报告                                                          | PR-402, PR-405, PR-406, PR-407, PR-408, PR-409 | 覆盖并发问答、资料摄取、平台异常、任务恢复、越权、备份/回退和试点上线流程。                            | 预发演练记录与可复现报告。                                |

> **阶段 4 合并闸门**
>
> 机构管理员可治理而不直接接触研究原文；研究任务可解释、可取消、可恢复；部署、回退、告警和安全验证均有自动化或可复现的 runbook。

## 附录：现有代码 seam

下表只用于帮助开发者定位复用点；新增模块不应复制这些实现的内部逻辑。

| 需要实现的能力 | 优先复用/适配的现有位置 | 计划中的新模块 |
|---|---|---|
| 文档摄取、处理、分块和索引 | `WeKnora/internal/application/service/knowledge_create.go`、`knowledge_process.go`、`knowledge_post_process.go` | project-knowledge 关联；不重建摄取/索引。 |
| 混合 RAG、rerank、引用 | `WeKnora/internal/application/service/session_knowledge_qa.go`、`chat_pipeline/`、`retriever/` | `projectrag`：只添加 project 范围和证据组合。 |
| ReAct、工具调用、MCP、Sandbox | `WeKnora/internal/agent/engine.go`、`internal/agent/tools/`、`mcp_tool.go` | 写作任务/学术检索 task adapter；不新建第二套 Agent engine。 |
| 会话 SSE、断线回放 | `WeKnora/internal/router/routes_chat.go`、`internal/handler/session/stream.go`、`internal/stream/`、`frontend/src/composables/useChatStreamHandler.ts` | project SSE 元数据与持久运行恢复 adapter。 |
| 认证、租户和 RBAC | `WeKnora/internal/middleware/auth.go`、`access.go`、`internal/router/rbac.go` | project access 与机构身份 mapping。 |
| 前端工作台 | `WeKnora/frontend/src/views/chat/`、`frontend/src/api/` | project workspace、证据、任务卡、章节、审校界面。 |
| 部署与持续集成 | `WeKnora/helm/`、`docker-compose.yml`、`.github/workflows/` | 分角色运行、生产健康检查、质量门禁。 |

