# G5. 完整权限、治理与审计

> 父计划：[Beta 六项缺口开发计划](../18-Beta六项缺口开发计划.md)
> 权限基线：[权限设计与复用 WeKnora](../08-权限设计与复用WeKnora.md)
> 前置假设：T01–T16 已完成并通过各自验收。本文件是后续开发计划，不表示功能当前已实现。

## 1. 目标和基本判断

G5 将固定测试成员升级为可解释、可撤销、可恢复、可审计的租户—项目—资源三层权限闭环。它不重新设计登录、用户和租户，而是在 WeKnora 的身份、租户成员、知识库共享和下载鉴权之上增加 LingDoc 的项目成员、职能能力和资源范围。

权限判断必须回答四个问题：

1. 这个调用者是不是当前租户的有效成员？
2. 他在这个项目中具有什么治理角色和职能能力？
3. 目标章节、资料、知识库、证据或交付文件是否在他的可用范围内？
4. 该操作是否还满足 Owner、对象版本、Preflight 和当前状态等领域条件？

统一判定式为：

\`\`\`text
EffectivePermission
  = TenantIdentity
  ∩ ProjectMembershipAndCapability
  ∩ ResourceScope
  ∩ DomainStateAndVersionConditions
\`\`\`

任一层拒绝，整体拒绝。前端隐藏按钮不是权限控制；每个领域入口、异步任务、文件预览和下载都必须重新判定。

## 2. 范围和边界

### 2.1 本轮交付

- 复用 WeKnora 的 User、TenantMember、TenantRole、成员状态、租户切换和知识库授权；
- LingDoc 的 ProjectMember、GovernanceRole、FunctionRole、capability 和 function scope；
- 项目、章节、资料、证据、路线图、模板、交付快照和 ExportArtifact 的访问边界；
- 成员邀请、接受、角色叠加、scope 授予/回收、暂停、移除和恢复；
- Owner 转让、租户停用传播、\`owner_unavailable\` 和恢复入口；
- 项目与 WeKnora 知识库绑定、ProjectAsset 导入和资料授权；
- allow/deny 审计、角色变更审计、版本条件审计和最小审计查询界面；
- 撤权后的正文、证据、缓存、文件预览和下载再鉴权。

### 2.2 明确不负责

G5 不负责：

- 新建第二套登录、用户、租户或组织体系；
- 用数字等级比较所有角色；治理角色和职能角色是两条正交轴；
- 把共享知识库权限当作项目成员资格；
- 通过 Admin 身份自动获得学术确认、ChangeSet 应用或正式导出；
- Beta 阶段的临时授权、受限服务账号、敏感材料水印、组织审批流；
- 让 API Key、Agent 或后台任务绕过项目成员和资源 scope；
- 解决研究内容是否正确、证据是否充分或模板规则是否合理。

## 3. 权限对象和角色模型

### 3.1 TenantIdentity

租户身份直接来自 WeKnora。只有当前租户中处于 \`active\` 状态的成员才能执行项目操作；\`invited\`、\`suspended\`、\`removed\` 或租户已停用的身份不能通过项目 API。

租户角色只提供能力上限，不自动授予某个项目的内容权限：

| WeKnora 身份 | G5 作用 |
|---|---|
| Owner/Admin | 可执行租户范围内允许的管理动作，仍需满足项目成员和领域条件 |
| Contributor | 可创建项目或参与被授予的项目能力，不自动获得其他项目编辑权 |
| Viewer | 只能在被项目成员和资源读取范围允许时查看 |

项目的 \`tenant_id\` 必须由数据库中的 Project 归属确定，不能采用请求体中的租户字段。所有章节、资料、证据、ChangeSet、快照和导出物都必须能回溯到同一租户。

### 3.2 ProjectMember

ProjectMember 至少包含：

\`\`\`text
project_id
user_id
governance_role: owner | admin | member
function_roles: [author | researcher | reviewer | observer]
function_scopes: { role -> [scope] }
status: active | invited | suspended | removed | suspended_by_tenant
invited_by / accepted_at / created_at / updated_at
\`\`\`

项目治理轴互斥且必须有一个：

| 治理角色 | 定位 | 典型权限 |
|---|---|---|
| \`owner\` | 项目最终责任人、唯一 Owner | 全部项目能力、ChangeSet 应用、导出、归档和转让 |
| \`admin\` | 项目运营和协作管理 | 成员、配置、任务、审计和允许范围内的问题处置 |
| \`member\` | 项目参与者底座 | 由职能轴授予具体内容能力 |

职能轴可以多选，不是全序：

| 职能角色 | 定位 |
|---|---|
| \`author\` | 编辑被授权章节、关联对象、创建内容变更 |
| \`researcher\` | 检索、资料导入申请、证据候选和调研任务 |
| \`reviewer\` | 审阅并确认被授予范围内的证据、内容或问题 |
| \`observer\` | 查看获授权内容和审计摘要 |

关键不变量：

1. Owner 不携带职能轴；\`governance_role=owner\` 时 \`function_roles\` 和 \`function_scopes\` 必须为空，Owner 的能力来自治理角色全集。
2. 非 Owner 未填写 scope 表示尚未指派作用域，不表示“全部”；其写作用域为空集，默认拒绝。
3. Admin 与职能角色可以叠加，能力取允许的并集，但不能突破租户上限或资源授权。
4. scope 主要限制写操作；普通章节阅读由项目成员权限决定，资料和知识库阅读还要经过资源授权。
5. 可以允许 \`author + reviewer\` 自写自审；系统通过对象版本、确认范围、理由和审计保证可追溯，不强制职责分离。

### 3.3 Capability

初始 capability 按领域分组：

\`\`\`text
项目       project.read / project.settings.edit / project.members.manage / project.archive
资料       asset.read / asset.import / asset.delete / asset.download
调研       research.search / research.fetch
证据       evidence.candidate.create / evidence.confirm
内容       content.read / content.edit / content.comment / content.confirm
模板       template.copy / template.edit / template.bind / template.upgrade.apply
变更       changeset.create / changeset.assess / changeset.apply
交付       delivery.preflight / delivery.issue.dismiss / delivery.export
路线       route.edit / route.confirm
任务       task.read / task.assign / task.manage
审计       audit.read
\`\`\`

能力不是最终结论。每个 capability 还必须通过目标资源 scope、项目状态、对象版本、Owner 条件和操作前置条件。

## 4. 权限判定链

### 4.1 标准判定顺序

所有领域入口按以下顺序判定：

1. 解析调用者：会话用户、API Key principal 或受支持的 Agent 身份；
2. 检查租户 active 状态和项目真实归属；
3. 加载当前 ProjectMember，检查项目成员状态；
4. 将治理角色和职能角色展开为 capability；
5. 检查目标对象、章节、资料、知识库或文件 scope；
6. 检查 Owner、项目状态、Context Revision、模板/规则版本和 Preflight 条件；
7. 记录 allow/deny 审计并返回最小必要结果。

无法确定调用者、成员关系、资源授权或对象版本时，必须 fail closed；不能把“上游服务暂时不可用”解释成允许。

### 4.2 允许与拒绝的最小返回

对有权用户，可以返回完成操作所需的对象和理由。对无权用户，若存在性本身敏感，统一返回“不存在或无权访问”；不能泄露项目名称、章节标题、资料标题、SourceAnchor 文本或文件大小。审计系统内部仍记录真实拒绝原因。

### 4.3 API Key、Agent 和异步任务

API Key 必须绑定声明的知识库/项目 scope，不能绕过 ProjectMember。共享 Agent 只提供被授予的读取能力，不提供资料导入、证据确认、ChangeSet 应用或导出能力。异步任务执行时重新检查提交者能力、项目状态、目标版本和资料授权；任务创建时有权不代表任务完成时仍有权。

## 5. 角色与能力边界

下表是能力上限，实际结果仍与 scope 和领域条件取交集：

| capability | Owner | Admin | Author | Researcher | Reviewer | Observer |
|---|---:|---:|---:|---:|---:|---:|
| \`project.read\` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| \`project.settings.edit\` | ✓ | ✓ | — | — | — | — |
| \`project.members.manage\` | ✓ | ✓ | — | — | — | — |
| \`project.archive\` | ✓ | — | — | — | — | — |
| \`asset.read\` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| \`asset.import\` | ✓ | ✓ | — | ✓ | — | — |
| \`asset.delete\` | ✓ | ✓ | — | — | — | — |
| \`research.search/fetch\` | ✓ | — | — | ✓ | — | — |
| \`evidence.candidate.create\` | ✓ | — | ✓ | ✓ | — | — |
| \`evidence.confirm\` | ✓ | — | — | — | ✓ | — |
| \`content.read\` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| \`content.comment\` | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| \`content.edit\` | ✓ | — | ✓ | — | — | — |
| \`content.confirm\` | ✓ | — | — | — | ✓ | — |
| \`template.copy/edit\` | ✓ | ✓ | — | — | — | — |
| \`template.upgrade.apply\` | ✓ | — | — | — | — | — |
| \`changeset.create/assess\` | ✓ | — | ✓ | — | — | — |
| \`changeset.apply\` | ✓ | — | — | — | — | — |
| \`delivery.preflight\` | ✓ | ✓ | ✓ | — | — | — |
| \`delivery.export\` | ✓ | — | — | — | — | — |
| \`route.edit\` | ✓ | — | ✓ | — | — | — |
| \`task.read\` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| \`task.assign/manage\` | ✓ | ✓ | — | — | — | — |
| \`audit.read\` | ✓ | ✓ | — | — | — | — |

问题处置有额外限制：Admin 可以处理 warning/info 的误报或允许范围内的风险；Reviewer 可以在目标 scope 内 dismiss blocking 误报，但不能 waive；blocking 问题没有 waive 入口；正式导出只由 Owner 执行。

## 6. Scope 语义

### 6.1 Scope 类型

G5 最小支持：

- \`chapter_scope\`：Ch1、Ch2 或章节集合；
- \`asset_scope\`：指定 ProjectAsset、SourceAnchor 或资料集合；
- \`delivery_scope\`：指定 ReleaseSnapshot、RouteNode 或交付项；
- \`knowledge_base_scope\`：允许访问的 WeKnora 知识库及其分享级别；
- \`template_scope\`：项目副本和模板版本的编辑/绑定范围。

Scope 必须引用稳定对象和版本范围。对象删除、项目归档、模板升级或资料授权变化时，旧 scope 保留历史，但当前判定重新计算。

### 6.2 写入范围与读取范围

职能 scope 限制写操作、确认和处置操作。章节之间的普通阅读不按段落建立 ACL；但资料、知识库、来源摘录和文件下载必须经过 ProjectAsset/SourcePolicy/WeKnora 授权。这样既避免把协作拆成过细 ACL，也不把资料授权误当作项目写权。

### 6.3 scope 变更

新增、收窄或回收 scope 都必须：检查操作者的 \`project.members.manage\`、记录旧值/新值、说明生效时间和影响对象，并让后续读取/下载立即重新判定。scope 回收不删除历史审计，但撤权者只能看到最小状态信息。

## 7. 成员生命周期

### 7.1 邀请与接受

Owner 或 Admin 邀请租户内用户时，必须指定治理角色（不能直接邀请 Owner）、可选职能角色和初始 scope。邀请状态为 \`invited\`，未接受前不能读项目内容。接受时重新检查租户 active 状态、项目未归档/丢弃和邀请未过期，并以幂等键建立 active 成员。

### 7.2 追加职能和 scope

追加职能、扩大或收窄 scope 是权限变更，不是普通资料编辑。操作者必须有成员管理能力；Reviewer 不得自授确认范围；所有变更写入审计并立即影响下一次请求。

### 7.3 暂停、移除和恢复

- \`suspended\`：项目侧暂时冻结，保留历史关系，不得读写项目内容；
- \`removed\`：项目侧清退，不改变 WeKnora 租户身份；
- \`suspended_by_tenant\`：由租户成员状态失效传导，租户恢复后是否回到 active 由项目侧恢复策略决定；
- 恢复必须重新检查当前租户身份、项目状态、原 scope 和资源授权，不能因历史成员关系自动恢复已撤回的权限。

移除或暂停后，正文、证据、资料标题、缓存和下载均要重新鉴权；已开始的异步任务返回 \`STALE/NOT_AUTHORIZED\`，不能继续写入正式对象。

## 8. Owner 生命周期与 owner_unavailable

### 8.1 Owner 不变量

每个未丢弃项目必须始终有且只有一个持久 Owner 记录。Owner 停用不删除 \`owner_user_id\`，而是派生 \`owner_unavailable=true\`。不能通过普通成员编辑制造双 Owner 或零 Owner。

### 8.2 Owner 专属操作

以下操作必须由当前可用 Owner 执行：立项确认、active 模板升级应用、ChangeSet 应用、正式导出、项目归档和所有权转让。Admin 可以管理成员和配置，但不能替 Owner 执行这些动作。

### 8.3 owner_unavailable

Owner 的租户身份失效时：

- 项目保留原 \`draft\`、\`active\` 或 \`archived\` 状态，不增加第四种 Project 状态；
- 日常读取、已授权协作和必要的恢复入口可以继续；
- \`changeset.apply\`、\`template.upgrade.apply\`、\`delivery.export\`、\`project.archive\` 和立项确认冻结；
- 界面显示冻结原因、当前 Owner、可执行的恢复/转让入口；
- 审计记录状态传播和每次被拒绝的 Owner 专属操作。

### 8.4 转让和恢复

普通转让与 owner_unavailable 恢复都使用 G1 的条件写入入口。一次事务必须检查：当前 \`predecessor_id\`、候选 \`successor_id\` 的 active 租户身份、项目状态、\`project_version\`、转让权限和唯一 Owner 约束。并发竞争失败时整个操作回滚，不产生双 Owner、零 Owner 或半套角色变化。

受让人变为 Owner 后，原有职能角色和 scope 清空，Owner 能力来自治理轴全集；原 Owner 降级为 Admin/Member 后按新的职能和 scope 重新授权，不能自动保留原 Owner 全集。

## 9. WeKnora 知识库与资料授权

### 9.1 项目—知识库绑定

项目绑定知识库时保存 Project、Tenant、KnowledgeBase、绑定者、绑定版本和允许用途。绑定关系不把项目变成知识库，也不把知识库 viewer 自动升级为项目成员。

### 9.2 读取与导入

读取 WeKnora 内容必须同时通过项目成员、知识库分享/API Key scope、SourcePolicy 和 ProjectAsset 授权。Researcher 可以提出导入申请或建立候选，但正式导入、删除、证据确认仍按 capability 和对象状态检查。

### 9.3 上游返回子集

若 WeKnora 或其他上游服务只返回请求对象的允许子集，G5 必须比较请求集合和返回集合：未返回对象不能被当作成功；需要返回给用户的结果必须明确标记被过滤的数量或统一隐藏存在性。不能因为上游接口返回 200 就放行全部请求。

## 10. 关键动作的权限接入

| 领域动作 | 必需能力 | 额外条件 |
|---|---|---|
| G1 创建项目 | 租户 active + 项目创建资格 | 建立唯一 Owner |
| G1 草稿保存 | \`content.edit\`/\`template.edit\` | 项目仍为 draft，scope 覆盖 |
| G1 立项确认 | Owner | 必要字段、模板迁移和 Owner 可用 |
| G2 资料导入 | \`asset.import\` | ProjectAsset scope、KB/SourcePolicy 允许 |
| G2 证据确认 | \`evidence.confirm\` | 目标 scope、SourceAnchor 有效、版本匹配 |
| G3 创建/评估 ChangeSet | \`changeset.create/assess\` | active、基线和对象版本有效 |
| G3 应用 ChangeSet | Owner + \`changeset.apply\` | current revision、Owner 可用、无并发过期 |
| G4 编辑项目副本 | \`template.edit\` | draft 或通过 ChangeSet |
| G4 应用模板升级 | Owner + \`template.upgrade.apply\` | G3 评估和迁移通过 |
| G4 处置问题 | 对应 \`delivery.issue.dismiss\` | 严重度、目标 scope、理由和版本 |
| G6 Preflight | \`delivery.preflight\` | 当前快照和规则版本可读 |
| G6 正式导出 | Owner + \`delivery.export\` | Preflight 通过，下载再鉴权 |

任何领域服务收到没有能力的请求，都不得依赖调用方自行过滤字段；必须拒绝写入并记录 deny 审计。

## 11. 历史读取、缓存和下载

历史对象分三层：

1. **非敏感元数据**：ID、时间、状态和 hash；按当前项目成员权限显示；
2. **正文、来源摘录、证据详情和确认理由**：重新检查当前项目和资料授权；
3. **ExportArtifact、预览文件和下载流**：每次打开/下载都重新检查项目、资料、文件和交付授权。

撤权后，缓存、预签名 URL、浏览器继续下载、异步预览和历史快照都不能成为旁路。撤权者最多收到 \`access_revoked\` 或最小 \`access-status\`；不可变审计可以保留，但不得包含研究原文、密钥或可复用下载地址。

回放旧模板或旧 revision 只改变业务版本，不降低当前访问权限；“有权查看历史版本”不等于“拥有历史来源正文”。

## 12. 审计模型

### 12.1 事件字段

每次 allow/deny、角色变更、scope 变更、Owner 转让、资料绑定、确认、导出和下载至少记录：

\`\`\`text
event_id, timestamp, actor_principal, tenant_id, project_id
action, decision, capability, target_ref, target_version
scope_evaluation, project_revision, request_id, correlation_id
reason_code, policy_version, source_service
\`\`\`

审计是追加式不可变记录。失败、拒绝和上游授权不完整同样要记录；记录中不写研究正文、SourceAnchor 原文、文件内容、API Key 或访问令牌。

### 12.2 审计查询

Owner/Admin 可以查看项目成员、权限变化、拒绝原因和交付操作；普通成员只能查看与自己相关的操作及项目允许的审计摘要。查询结果也要经过当前项目和租户权限，不能因拥有 \`audit.read\` 泄露另一个项目的资源。

### 12.3 审计与业务状态分离

审计事件不能由前端伪造，也不能反向修改 Project、ProjectMember、ChangeSet 或 ExportArtifact。业务状态写入失败时，必须记录拒绝/失败；审计写入失败时，敏感业务动作默认失败闭合，不能“先放行、后补日志”。

## 13. 交付切片

| 切片 | 交付物 | 完成判据 |
|---|---|---|
| G5.1 身份与判定器 | TenantIdentity、EffectivePermission、fail-closed 链 | 租户、项目、资源和领域条件逐层相交 |
| G5.2 角色与 capability | 双轴角色、能力矩阵、快捷组合 | Owner 唯一；Admin 不获得学术确认或导出 |
| G5.3 Scope | 章节、资料、KB、模板、交付 scope | 未指派 scope 对非 Owner 为空集，回收立即生效 |
| G5.4 成员生命周期 | 邀请、接受、追加、暂停、移除、恢复 | 过期/撤权成员不能读写或下载 |
| G5.5 Owner 恢复 | 转让、owner_unavailable、并发保护 | 不产生双 Owner/零 Owner；专属能力正确冻结 |
| G5.6 WeKnora 交接 | KB 绑定、SourcePolicy、上游子集比较 | KB 权限不能替代项目成员，返回子集可识别 |
| G5.7 资源与下载 | ProjectAsset、证据、缓存、ExportArtifact 再鉴权 | 撤权后正文和文件不能旁路获取 |
| G5.8 审计 | allow/deny、权限变更、查询和保密字段 | 审计不可变、可追溯且不含正文 |

## 14. 与其他能力线交接

### 交给 G1

- 创建项目时建立唯一 Owner 和初始成员；
- 草稿保存、模板副本编辑、立项确认的 capability 和 scope；
- Owner 不可用时冻结立项确认的条件。

### 交给 G2

- ProjectAsset、知识库、SourceAnchor、EvidenceCard 的读取和写入判定；
- 资料撤权、来源失效和异步重检的再鉴权；
- 调研 Agent 只能使用授权资料，不能替用户确认正式证据。

### 交给 G3

- ChangeSet 创建、评估、应用、任务分派和稳定化的权限矩阵；
- Owner 可用、当前 revision、目标对象 scope 和并发条件；
- 撤权或成员状态变化导致的任务失效与审计。

### 交给 G4

- 模板复制、编辑、绑定、升级和 ValidationIssue 处置权限；
- Reviewer/Owner 的 blocking 误报裁定边界；
- 历史模板、规则问题和 Preflight 的分层读取。

### 交给 G6

- 跨租户、跨项目、撤权、Owner 停用、并发转让、缓存下载和审计 fixture；
- allow/deny 比例、越权尝试、撤权后泄露数、Owner 恢复耗时和审计完整性指标。

## 15. 验收场景

### AC-G5-01：三层相交拒绝

用户是当前租户成员，但不是项目成员；即使携带有效知识库 viewer 权限，访问项目正文仍返回“不存在或无权访问”，并写入 deny 审计。

### AC-G5-02：未指派 scope

成员拥有 \`Author\`，但未分配章节 scope。用户可以按项目读取边界查看内容，不能编辑任何章节，不能以空 scope 推断为全集。

### AC-G5-03：角色叠加边界

Admin + Author 可以管理成员、编辑被授权章节和处理允许的 warning，但不能确认 blocking 误报、应用 ChangeSet 或正式导出。

### AC-G5-04：资料与项目权限分离

用户是项目成员但没有绑定知识库或 ProjectAsset 的读取授权。项目元数据可读，资料正文、SourceAnchor 和检索结果不可读。

### AC-G5-05：撤权后再鉴权

用户已经打开过资料或获得预览 URL，随后 scope 被回收。再次查看正文、刷新预览、下载文件和异步任务写入都被拒绝；缓存不泄露内容。

### AC-G5-06：Owner 停用

租户停用 Owner 后，项目保留原状态并标记 \`owner_unavailable\`；普通协作按现有权限继续，立项确认、ChangeSet 应用、模板升级、导出和归档被冻结。

### AC-G5-07：并发转让

两个管理员同时发起 Owner 转让。只有满足当前 \`project_version\` 的一个事务成功，另一个失败；系统始终只有一个 Owner，不产生半套 scope 变化。

### AC-G5-08：Owner 接管清空职能轴

受让人原来是 Reviewer。转让成功后，受让人变为 Owner 且职能轴清空；原 Owner 降级后不自动保留全集能力，需重新授予职能和 scope。

### AC-G5-09：历史读取分层

撤权用户仍可能看到不可敏感的历史 ID/hash（若项目策略允许），但不能读取正文、来源摘录、确认理由或下载 ExportArtifact；审计记录保留且不含原文。

### AC-G5-10：上游返回子集

用户请求多个知识库资料，上游只返回部分允许对象。系统不把调用当作全部成功，并明确记录过滤结果；未授权对象不能进入 EvidenceCard 候选。

### AC-G5-11：异步结果陈旧

任务创建时用户有权限，运行期间成员被移除或资料 scope 被回收。任务结果只能保存为未授权/陈旧失败，不能写入当前证据、章节或快照。

### AC-G5-12：审计完整性

允许、拒绝、角色变更、scope 回收、Owner 转让和下载均可按项目、操作者、目标版本和原因查询；审计内容不含研究正文、密钥和可复用下载地址。

## 16. 验收与指标

发布前必须验证：角色矩阵、scope 交集、跨租户和跨项目访问、知识库绑定、撤权后的正文/缓存/下载、Owner 并发转让、异步任务再鉴权和审计不可变性。

上线后至少统计：allow/deny 数、越权尝试数、撤权后访问阻断数、下载再鉴权失败数、Owner 恢复时长、角色/scope 变更数、异步未授权写入拦截数和审计写入失败数。指标用于发现权限漏洞和操作阻塞，不用于推断用户的研究质量。

## 17. 不纳入 G5

本阶段不做临时授权、服务账号、敏感材料水印、组织审批流、新租户系统和逐段文本 ACL。G5 的完成标准是：每个操作都能沿租户身份、项目能力、资源 scope 和领域条件得到可解释的 allow/deny 结果；成员撤权立即生效；Owner 失效可恢复；历史与下载不会绕过当前权限；所有关键决定有不可变审计。

---
