# 灵档权限设计与 WeKnora 复用方案

## 1. 设计结论

灵档不再造一套“用户—组织—登录”体系，而是在 WeKnora 现有权限模型之上增加**项目级成员与能力**。权限判断采用三层相交模型：

```text
有效权限 = 租户身份上限 ∩ 项目成员能力 ∩ 资源/凭证范围
```

- **租户身份层**：直接复用 WeKnora 的 User、TenantMember、TenantRole、成员状态、租户切换和邀请机制。
- **资源访问层**：直接复用 WeKnora 的 Knowledge Base share、OrgMemberRole、`ResolveKB`、`KBAccess`、API Key 知识库 scope 和 Agent 共享只读规则。
- **项目操作层**：LingDoc 新增 ProjectMember 与 capability；它只收紧权限，不能突破租户角色、知识库共享或 API Key scope。

项目不是知识库的别名。项目负责课题、章节、主张、证据候选、ChangeSet 和交付；知识库仍由 WeKnora 负责解析、检索、引用和 Agent 能力。

## 2. 可直接复用的 WeKnora 能力

| WeKnora 现有设计 | 灵档复用方式 | 不应改变的约束 |
|---|---|---|
| User、登录/OIDC、会话 | 作为灵档唯一身份来源 | 不新增 LingDoc 用户表或第二套登录 |
| TenantMember、TenantRole | 作为项目所在租户和权限上限 | 仅 active 成员可操作；invited/suspended 不能执行项目操作 |
| Owner/Admin/Contributor/Viewer | 控制租户级动作和项目创建资格 | Contributor 不自动获得其他项目的编辑权 |
| `IsTenantAccessible`、`RequireRole` | 每个项目 API 的第一道检查 | 缺少租户或成员关系时 fail closed |
| KB share、OrgMemberRole | 控制项目可用知识库的资源权限 | KB 分享不能替代项目成员资格 |
| `ResolveKB`、`KBAccess` | 解析调用者、Agent、KB scope 和分享权限 | 不把共享 KB 的 viewer 权限升级为写入权限 |
| API Key principal 与 KB scope | 约束 Agent、自动化和集成调用 | API Key 不得绕过项目成员检查；默认拒绝未声明 scope |
| Agent 共享只读路径 | 允许特定 Agent 使用项目授权的资料 | shared-agent 只提供 viewer 能力，不提供导入、确认或发布权限 |
| 审计 denied operation 机制 | 记录权限拒绝、越权尝试和人工确认 | 审计事件不允许由前端伪造 |

## 3. LingDoc 新增的最小领域模型

### 3.1 Project

`Project` 至少包含：`id`、`tenant_id`、`owner_user_id`、`name`、`status`、`template_profile_id`、`current_context_revision`、`created_by`、`created_at`、`archived_at`、`discarded_at`。模板与版本字段的语义见 [`03-数据模型与接口契约`](03-数据模型与接口契约.md) 第 2 节。

`status: draft | active | archived`。`draft` 是建项期状态（`current_context_revision = 0`，见 [`07-建项阶段设计`](07-建项阶段设计.md)），立项确认后转 `active`；`archived` 仅 Owner 可置（见第 4 节）。`owner_unavailable` **不是独立的 `status` 值**，而是由 `owner_user_id` 指向者当前租户成员状态派生出的只读标记：项目保持原 `status`（`draft` 或 `active`）、未归档、日常协作照常，只因租户侧导致 Owner 失效（见 8.3），Owner 独有的 `changeset.apply`、`delivery.export` 与 `project.archive` 被冻结；解除方式为恢复 Owner 的租户身份，或按第 4 节转让所有权。

`discarded_at` 也不是独立的 `status` 值，而是**只对 `draft` 项目有效**的软删除标记：丢弃表示这段构想未成立，归档（`status = archived`）表示项目已结束，两者语义不同、不可共用。丢弃后项目默认不出现在项目列表，不可写入、不可立项确认，但保留审计与可恢复入口；`active` 项目只能归档，不能丢弃。

`tenant_id` 是项目的权威租户归属。所有项目、章节、材料、主张、ChangeSet、交付记录都必须通过项目归属回溯到同一租户；禁止用请求中的 `tenant_id` 覆盖数据库归属。

### 3.2 ProjectMember 与双轴角色模型

```text
ProjectMember
- project_id
- user_id
- governance_role: owner | admin | member          # 治理轴，必选一
- function_roles: [author | researcher | reviewer | observer]   # 职能轴，可多选
- function_scopes: { <function_role>: [scope] }    # 限定章节/材料/交付版本范围
                        # 未填 ≠ 不限：对 governance_role ≠ owner 的行，未填表示「尚未指派」，
                        #   该职能的写作用域为空集（默认拒绝）。见下方规则 1
                        # 不变量：governance_role = owner 的行，上述两轴字段恒为空
                        # 原因见下方规则 1：Owner 的能力与 scope 来自治理轴全集，不来自职能轴
- status: active | invited | suspended | removed | suspended_by_tenant
                        # removed 为项目侧主动清退，不改变租户身份（见 8.4）
                        # suspended_by_tenant 由租户身份失效传导而来（见 8.3）
- invited_by
- created_at / updated_at
```

项目角色拆成两个**正交**维度，而不是一条全序等级。

**治理轴（必选一，互斥）**——决定项目身份与治理权：

| 治理角色 | 典型人员 | 定位 |
|---|---|---|
| Project Owner | 课题负责人 | **权限顶点**，拥有全部 capability；项目最终责任人，唯一可归档项目与转让所有权 |
| Project Admin | 项目助理 | 项目运营：成员、配置、任务分派、审计；**不做学术确认、不放行交付** |
| Member | 全体参与者 | 参与者底座，本身不含内容能力，由职能轴赋予 |

**职能轴（可多选，叠加）**——决定能对内容做什么：

| 职能角色 | 典型人员 | 定位 |
|---|---|---|
| Author | 章节负责人 | 生成/编辑草稿、关联证据、创建 ChangeSet、编辑技术路线 |
| Researcher | 研究助理 | 调研、运行 Research Agent、提交导入申请、创建证据候选 |
| Reviewer | 指导教师/评审人 | 审阅并确认被分配的内容或证据；不直接改稿 |
| Observer | 观察者 | 只读授权内容与审计摘要（未指定职能时的默认值） |

**五条关键规则**：

1. **Owner 是唯一顶点，且不携带职能轴**：它自动拥有全部 capability，不参与职能轴选择——`governance_role = owner` 的行 `function_roles` 与 `function_scopes` 恒为空，这是一条可校验的不变量，不是默认值。界面显示"全部"是因为治理角色是 `Owner`，而不是因为职能轴被填满；它的能力集是其他所有成员的授权上限。Owner 被降级为 `Admin` / `Member` 时职能轴仍为空，按"未指定职能"处理即 `Observer`（见第 4 节转让规则）。由此有三条推论：
    - **Owner 的 scope 就是 Owner 的 scope**：它不再受转让前那些章节/材料/交付版本 scope 的约束——`function_scopes` 为空即全集能力，不做 scope 过滤；
    - **「空即全集」只是 Owner 的读法，不是通用规则**：对 `governance_role ≠ owner` 的行，`function_scopes` 未填表示该职能**尚未指派作用域**，写作用域为空集（默认拒绝）。草稿期授予的职能角色因此天然不带章节写权，章节 scope 在立项确认、章节生成之后再指派（见 [`07-建项阶段设计`](07-建项阶段设计.md) 第 5.4 节）。若把“未填 = 不限”当作通用规则，草稿期随手授予的一个 `Author` 会在立项后自动获得全部章节的写权——这与最小授权正好相反；
    - **转让会清空受让人的职能轴**：受让人转让前持有的受限职能授权不保留，其能力全部来自 Owner 全集；被降级时按上句处理，需要时由新任 Owner 或 `Admin` 重新授予。
2. **职能可叠加，且互不全序**：`Author + Reviewer` 表示既能写稿也能确认；`Reviewer` 不会因为“更受信任”而获得 `Author` 的改稿能力。
3. **治理与学术分离**：`Project Admin` 能管人管配置，但拿不到 `evidence.confirm`、`content.confirm`、`changeset.apply`、`delivery.export`——“能管理项目”不等于“能做学术裁定”。
4. **scope 只约束写操作**：职能 scope 限定的是“能改哪里”，不是“能看哪里”。`Author` scope 为 Ch1–Ch2 的成员仍可阅读全部章节——章节之间高度关联，限制读会让协作无法进行。读取边界由知识库共享级别和资料授权范围管辖，不由职能 scope 管辖。Beta 不为每段文本单独建立 ACL；敏感内容另按第 11 节的生产增强项处理。
5. **允许自写自审**：同一人可以确认自己撰写的内容，`Author + Reviewer` 是其负责章节的正常配置。内容确认不设职责分离要求——确认绑定对象版本、核查范围与理由，可追溯性由审计保证，而不是靠“必须换一个人点确认”。

**五种预设组合**（原有五角色即双轴模型的固定组合，其中”课题负责人”就是 `Owner`；除「课题负责人」外的四种是邀请弹窗里的一键快捷项，「课题负责人」仅作展示、不能在邀请中授予，五种都不是可选范围的上限）：

| 预设 | 原称呼 | 双轴表达 |
|---|---|---|
| 课题负责人 | Project Owner | `Owner` |
| 章节负责人 | Editor | `Member + Author` |
| 研究助理 | Researcher | `Member + Researcher` |
| 评审人 | Reviewer | `Member + Reviewer` |
| 观察者 | Viewer | `Member + Observer` |

预设之外的组合用于表达原五角色覆盖不到的人，例如科研秘书：`Admin + Author`（管理模板、分派任务、检查材料完整性、协作章节，但不做学术确认）。组合在数据层与界面同等可用，不存在“模型支持、界面表达不出”的角色。

唯一的例外是治理角色 `Owner`，有两处：它**不由邀请产生**——邀请弹窗的治理下拉只到 `Admin`，新任 `Owner` 必须走转让流程（见第 4 节），否则邀请一次就会同时存在两个 Owner；它**也不携带职能轴**——转让为 `Owner` 时受让人原有职能轴清空，其能力一律来自全集（见上文规则 1）。

实现上**始终以 capability 集合为准**，不比较数字等级。`Project Owner` 的顶点地位由“它是全集”体现，而不是由某个数字等级体现；这既保证了 Owner 最高权限，又不把其余角色压成一条全序线。

### 3.3 Capability 与角色映射矩阵

建议初始能力集合如下（按域分组）：

```text
项目治理   project.read / project.settings.edit / project.members.manage / project.archive
资料       asset.read / asset.import / asset.delete
调研       research.search / research.fetch
证据       evidence.candidate.create / evidence.confirm
内容       content.read / content.edit / content.comment / content.confirm
变更       changeset.create / changeset.apply
交付       delivery.preflight / delivery.issue.dismiss / delivery.export
路线       route.edit
协作       task.read / task.assign / task.manage
审计       audit.read
```

完整映射矩阵（`Author` 等职能列表示在 `Member` 底座之上叠加的能力；`Admin` 可与任意职能叠加，叠加后取并集）：

| capability                  | Owner | Admin | Member+Author | Member+Researcher | Member+Reviewer | Member+Observer |
| --------------------------- | ----- | ----- | ------------- | ----------------- | --------------- | --------------- |
| `project.read`              | ✓     | ✓     | ✓             | ✓                 | ✓               | ✓               |
| `project.settings.edit`     | ✓     | ✓     | —             | —                 | —               | —               |
| `project.members.manage`    | ✓     | ✓     | —             | —                 | —               | —               |
| `project.archive`           | ✓     | —     | —             | —                 | —               | —               |
| `asset.read`                | ✓     | ✓     | ✓             | ✓                 | ✓               | ✓               |
| `asset.import`              | ✓     | ✓     | —             | ✓                 | —               | —               |
| `asset.delete`              | ✓     | ✓     | —             | —                 | —               | —               |
| `research.search`           | ✓     | —     | —             | ✓                 | —               | —               |
| `research.fetch`            | ✓     | —     | —             | ✓                 | —               | —               |
| `evidence.candidate.create` | ✓     | —     | ✓             | ✓                 | —               | —               |
| `evidence.confirm`          | ✓     | —     | —             | —                 | ✓               | —               |
| `content.read`              | ✓     | ✓     | ✓             | ✓                 | ✓               | ✓               |
| `content.edit`              | ✓     | —     | ✓             | —                 | —               | —               |
| `content.comment`           | ✓     | ✓     | ✓             | ✓                 | ✓               | —               |
| `content.confirm`           | ✓     | —     | —             | —                 | ✓               | —               |
| `changeset.create`          | ✓     | —     | ✓             | —                 | —               | —               |
| `changeset.apply`           | ✓     | —     | —             | —                 | —               | —               |
| `delivery.preflight`        | ✓     | ✓     | ✓             | —                 | —               | —               |
| `delivery.issue.dismiss`（警告/提示级） | ✓     | ✓     | ✓             | —                 | ✓               | —               |
| `delivery.issue.dismiss`（阻断级）      | ✓     | —     | —             | —                 | ✓               | —               |
| `delivery.export`           | ✓     | —     | —             | —                 | —               | —               |
| `route.edit`                | ✓     | —     | ✓             | —                 | —               | —               |
| `task.read`                 | ✓     | ✓     | ✓             | ✓                 | ✓               | ✓               |
| `task.assign`               | ✓     | ✓     | —             | —                 | —               | —               |
| `task.manage`               | ✓     | ✓     | —             | —                 | —               | —               |
| `audit.read`                | ✓     | ✓     | ✓             | ✓                 | ✓               | ✓               |

五点需要特别注意：

- **`changeset.apply` 与 `delivery.export` 仅 Owner 持有**：这两项分别是“改变项目语义”和“放行正式交付”，属于项目最终责任，不下放给 Admin。Owner 是该项目的唯一责任人，这两项由其独立判断并留审计，不设第二签。
- **“删除误报”与“豁免”是两件事，阻断级只允许前者，且它是一次学理裁定**：豁免是承认问题真实存在但本次接受，只对警告/提示级开放，权限随 `delivery.preflight`；删除（`delivery.issue.dismiss`）是判定规则或模型判断有误、这条问题本就不该存在，对任一严重度开放——AI 会产生幻觉，用户必须能推翻它。但删除一条阻断项的效果等于“这条阻断不存在”，它判断的是“证据够不够、论证成不成立”，与 `evidence.confirm` / `content.confirm` 同性质，因此同样归 `Owner` 与 `Member + Reviewer`，不给 `Admin`（见上文治理与学术分离）。它也不能随手做：必须填写理由，记录规则 ID、判定方与目标版本，进入导出审计，且只对当前目标版本有效——目标对象换版本后问题会在下次求值时重新出现（这个作用域跨检查级别、也跨判定方，见 [`03-数据模型与接口契约`](03-数据模型与接口契约.md) 第 5 节）。`Member + Reviewer` 持有删除阻断级的权力却不在 `delivery.preflight` 列，并不矛盾：复核中心本就把“阻断交付”列在队列里（见 [`09-用户界面与交互规划`](09-用户界面与交互规划.md) 第 11 节），复核人裁决的是别人跑出来的问题，不必自己发起预检。
- **`evidence.confirm` 与 `content.confirm` 作用于不同对象，不可互换**：前者确认的是 `EvidenceCard`——“这段原文确实支持该主张，支持范围与限制的描述准确”（A 域来源真相），确认后该证据卡才能作为章节的已确认依据，其 scope 按**证据卡所服务的主张与章节**判定；后者确认的是 `ChapterVersion` / `ResearchClaimVersion`——“这段文字表述准确、结论未超出其所引证据能支持的范围”（B 域项目真相），确认后该版本才能进入 `ReleaseSnapshot`，其 scope 按**章节或主张本身**判定。矩阵中两行恰好同列（都只归 `Owner` 与 `Member + Reviewer`），但流程上内容确认以其所引证据已有确认记录或明确标为待核为前提，且确认内容的人不必重做证据确认；自写自审对两者同样适用。
- **`content.comment` 不授予 Observer**：评论是写入动作，观察者只读。
- **`task.read` 与 `audit.read` 全角色可见**：任务中心和审计摘要是协作可见性的一部分，不构成写入能力。

章节、材料集合、交付版本可以进一步增加 `scope`（例如“仅负责章节”），但 **scope 只限定写操作**：读取边界由知识库共享级别和资料授权范围决定，不由职能 scope 决定。Beta 不应为每一段文本单独建立 ACL。

**未指派即空集**：`function_scopes` 中某项职能未填时，该职能的写作用域是空集，不是全集（`Owner` 除外，它不参与职能轴）。这是默认拒绝在职责范围内的应用，也是「先给角色、后给范围」这一真实协作顺序成立的前提。

### 3.4 ProjectAsset 与知识库绑定

`ProjectAsset` 是 LingDoc 对项目资料的引用边界。对已有 WeKnora 资料只建立关联，不复制原始文件或重建索引；对获准导入的外部来源/对话片段，才通过 WeKnora 既有处理链创建受控知识资产，再写入项目关联。每条绑定至少记录 `project_id`、`knowledge_id`/来源引用、`authorization_scope`、`asset_revision` 和状态。项目成员先通过 LingDoc capability，再由 WeKnora `ResolveKB`/`KBAccess` 检查知识库共享权限；任一检查失败都不能读取或写入该资料。

## 4. 推荐的权限矩阵

| 操作 | 租户最低角色 | 项目能力 | 资源条件 |
|---|---|---|---|
| 创建记录（草稿项目） | Contributor | 无（创建者成为 `Owner`） | 当前租户 active；项目以 `draft` + `Context Revision 0` 存在 |
| 草拟研究条件（草稿期） | Viewer | 职能角色 `Author`（`changeset.create`） | 草稿期不产生 `ChangeSet`；确认权仍在 `Owner` |
| 立项确认 | Contributor | `changeset.apply`（仅 `Owner`） | 草稿期确认页须显示两类差异（他人改动、仍是 AI 填写且未经人工改动）；通过后生成章节骨架与 `Context Revision 1` |
| 邀请/移除项目成员 | Contributor | `project.members.manage`（治理角色为 `Owner` 或 `Admin`） | 被邀请人须已是同一租户的 active 成员；跨租户的合并邀请另需租户管理权（见 8.2） |
| 绑定知识库 | Contributor | `project.settings.edit`（`Owner` 或 `Admin`） | 通过 `ResolveKB`，至少 KB viewer；跨租户需显式分享 |
| 分派/转派任务 | Viewer | `task.assign` / `task.manage`（`Owner` 或 `Admin`） | 任务须指向项目内对象和有效 Revision |
| 上传/导入项目资料 | Contributor | `asset.import` | KB editor/write scope；学术来源需通过 ImportCandidate 和来源目录确认 |
| 运行 Research Agent | Viewer | 职能角色 `Researcher`（`asset.read`、`research.search`、`research.fetch`） | AcademicSource policy、Agent scope、KB scope 均通过 |
| 生成草稿 | Viewer | 职能角色 `Author`（`content.edit`） | 所有引用 KB 可读；输出写入项目版本 |
| 确认主张/证据 | Viewer | 职能角色 `Reviewer`（`evidence.confirm` / `content.confirm`） | 允许自写自审；确认须绑定对象版本与核查范围 |
| 创建 ChangeSet | Viewer | 职能角色 `Author`（`changeset.create`） | 须绑定当前 `base_revision`；创建不等于应用 |
| 应用 ChangeSet | Contributor | `changeset.apply`（仅 `Owner`） | 须绑定 `base_revision`；已被后续变更超越的陈旧 ChangeSet 拒绝应用 |
| 运行模板检查/交付预检 | Viewer | `delivery.preflight` | 模板检查只需项目已立并有章节骨架，预检需快照可构造；两者都不改变项目状态 |
| 删除误报问题 | Viewer | `delivery.issue.dismiss`——警告/提示级：`Owner`、`Admin`、`Member + Author` 或 `Member + Reviewer`；阻断级：仅 `Owner` 或 `Member + Reviewer` | 问题须仍为 `OPEN`；须填写理由并记录规则、判定方与目标版本；删除只对当前规则与目标版本有效，跨检查级别也跨判定方；目标对象换版本后问题重新出现 |
| 放行导出 | Viewer | `delivery.export`（仅 `Owner`） | 仅导出已确认版本；未解决阻断项拒绝放行且不接受豁免——阻断项只能被修复、关闭或判定为误报删除，不能被跳过；仅警告/提示级问题可豁免，豁免权限随 `delivery.preflight`（见 3.3），导出时由 `Owner` 记录理由与风险清单 |
| 丢弃草稿 | Contributor | `project.archive`（仅 `Owner`） | 仅对 `draft` 项目有效：写软删除标记 `discarded_at`，**不改 `status`**；草稿期不新增 capability |
| 归档项目 | Contributor | `project.archive`（仅 `Owner`） | 仅对 `active` 项目有效：置 `status = archived`；存在未解决阻断项时先提示，但不强制；归档不改动租户成员状态（见 8.4）——`active` 项目不能丢弃，`draft` 项目不能归档 |
| 查看审计 | Viewer | `audit.read` | 仅可见项目范围；平台审计仍归 WeKnora/RuoYi 管理 |

“租户最低角色”只是上限/前置条件，不是最终授权。最终结果必须同时满足租户、项目、资源三层检查。

### Owner 唯一性与所有权转让

`Project.owner_user_id` 是项目的**唯一** Owner，与 `ProjectMember` 中 `governance_role = owner` 的那一行必须始终一致，以 `owner_user_id` 为权威字段。

这条约束不是形式要求：`changeset.apply` 和 `delivery.export` 仅 Owner 持有，项目一旦失去 Owner，就再没有人能应用变更或放行交付，项目会卡死在当前 Revision。

| 规则               | 说明                                                                                                                                          |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| 不能直接移除或降级 Owner  | 把 Owner 降为 `Admin`/`Member`，或从成员列表移除，都必须先完成所有权转让                                                                                            |
| 转让必须是原子的         | 新 Owner 上任与旧 Owner 降级在同一次事务内完成，不允许出现“零 Owner”或“双 Owner”的中间态                                                                                 |
| 转让清空职能轴          | 升为 `Owner` 时受让人原职能轴清空——其能力改由 Owner 全集提供，不再受转让前的章节/材料/交付版本 scope 约束；降级方保持原状：回到 `Admin` / `Member`，职能轴为转让前内容，原 Owner 职能轴本就为空，降级后按"未指定职能"处理即 `Observer`。Owner 行职能轴恒为空（见 3.2 规则 1） |
| 转让须经受让人确认        | 未确认前所有权保持原状，避免把最终责任强加给尚未同意的人                                                                                                                |
| 仅 Owner 可发起转让    | 租户 `Admin` 可协助成员管理，但不能自行取得项目所有权；平台管理员同样不能                                                                                                   |
| `Owner` 不由邀请产生   | 邀请弹窗的治理角色只能授予 `Admin` / `Member`；新任 `Owner` 必须走转让流程，否则会破坏“有且只有一个 Owner”                                                                     |
| Owner 不可用时另设发起路径 | `owner_unavailable` 下 Owner 已被停用、无法自行发起，由租户 `Owner` 或平台管理员**指定受让人并发起**，仍须受让人确认后生效——发起权旁路不等于可以自行取得所有权                                        |
| Owner 离场前必须先转让   | Owner 不能自行退出项目；租户层要 suspended 其成员身份时，应先触发项目所有权转让                                                                                            |
| 归档不解除约束          | `project.archive` 仅 Owner 可执行；归档后 Owner 关系保留用于审计                                                                                            |

WeKnora 租户层采用“至少保留一个 Owner”的 last-owner 保护（降级或移除最后一个 Owner 返回 409）；项目层更严格——**有且只有一个 Owner**。

转让是 `owner_unavailable` 的逃生路径，不是常规操作：租户侧停用 Owner 之前应先确认其可恢复；确已无法恢复时，项目停留在 `owner_unavailable`（见 8.3），再按上表“Owner 不可用时另设发起路径”由租户 `Owner` 或平台管理员指定受让人发起转让来解除。

## 5. 请求链路与后端落点

每个 LingDoc API 按下面顺序执行，不允许只在前端隐藏按钮：

```text
1. 认证：复用 WeKnora JWT/OIDC/API Key caller
2. 租户：复用 IsTenantAccessible + active TenantMember + RequireRole
3. 项目：按 project_id 查询并校验 project.tenant_id == caller.tenant_id
4. 项目成员：校验 ProjectMember.status == active
5. 能力：检查目标 capability 与 scope（章节/材料/交付版本）
6. 资源：对每个 KB 调用 ResolveKB/KBAccess，检查分享角色和 API Key scope
7. Agent：检查 Agent 来源租户、调用者身份和工具 scope
8. 审计：记录 allow/deny、角色、资源、ChangeSet/版本和原因
```

建议新增独立的 `internal/application/access/project`（或等价领域包），保持与 WeKnora 现有 `access/knowledgebase` 分离：

- `ResolveProject`：解析项目归属和当前成员；
- `ProjectPermissions`：请求内缓存成员与 capability 判断，缓存错误但不跨请求缓存；
- `CheckProjectCapability`：执行能力及 scope 检查；
- `ProjectKBScope`：把项目允许的 KB 集合交给 `ResolveKB`，不复制 KB 的分享逻辑；
- `ProjectAudit`：统一记录项目授权决策和人工确认。

已有 `RequireRole`、`IsTenantAccessible`、`ResolveKB`、`KBAccess` 不应被项目代码重写或绕过。

## 6. 调研智能体和知识沉淀的权限

Research Agent 必须把“查找”和“写入/确认”拆开：

1. `research.search`：在允许的网络、项目 KB 和当前会话中检索，产生候选结果。
2. `research.fetch`：获取用户选中的批准学术来源或文献，形成 AcademicSource/ConversationExcerpt。
3. `asset.import`：把候选转换为 ImportCandidate，写入项目待确认队列；不能直接成为正式 EvidenceCard。
4. `evidence.confirm`：由 Owner/Reviewer 等人工角色确认来源、适用范围和引用位置。

AI 多轮回答的导入同样必须经过用户选择、范围确认和项目权限检查。搜索结果、模型回答和自动摘要永远不是已确认证据。

如果项目绑定的 KB 是共享资源：

- KB viewer 可检索和引用，但不能上传、删除、改解析配置；
- KB editor 可按 WeKnora 规则写入 KB，但仍需要项目 `asset.import`；
- KB share 绝不能赋予项目成员 `content.confirm`、`changeset.apply` 或 `delivery.export`。

## 7. API Key、服务调用与跨租户

- 人类调用沿用 WeKnora caller identity；不要用“系统用户”代替实际操作者。
- API Key 必须同时具备 WeKnora 的 KB scope 和 LingDoc 的 project scope；没有 project scope 时默认拒绝。
- API Key 不能调用成员管理、人工确认、ChangeSet 应用、交付发布等高风险动作，除非未来增加明确的受限服务能力，并由人工确认触发。
- 跨租户访问只允许 WeKnora 已有的 cross-tenant superuser/platform API-key 机制；LingDoc 项目 API 不自行扩大跨租户权限。
- Agent source tenant、caller tenant、project tenant 不一致时拒绝；不得因为 Agent 能访问某个 KB 就让它访问整个项目。

## 8. 管理面的结合与状态联动

LingDoc 不在前端重造租户管理，而是把 WeKnora 的成员与权限能力**嵌进**项目工作流：界面是一个入口，真相源各归其主，状态通过检查与事件对齐。

### 8.1 分工边界

| 管理对象 | 真相源 | LingDoc 能做什么 |
|---|---|---|
| 租户成员、租户角色、租户邀请 | WeKnora | 可发起（调用 WeKnora API），不可另存 |
| 知识库创建、KB 分享级别 | WeKnora | 可发起绑定/解绑，分享级别仍走 WeKnora |
| 项目成员、治理/职能角色、职能 scope | LingDoc | 完整管理 |
| 项目—KB 绑定、资料授权范围 | LingDoc | 完整管理 |
| 模板发布、配额、组织治理 | RuoYi root | 只读引用，经 Admin API |

原则是：**LingDoc 可以发起租户层操作，但绝不持有租户层的真相**。租户成员表、租户角色、KB 分享记录始终只有 WeKnora 一份。

### 8.2 成员添加：一个入口，按对方状态分支

在项目成员页邀请同事时，按对方当前状态走不同路径：

| 对方状态 | 行为 |
|---|---|
| 已是本租户 active 成员 | 直接创建 `ProjectMember`（`invited`），授权到项目 |
| 已注册、但不在本租户 | 同一次操作内先调用 WeKnora 租户成员 API 加入租户，再创建 `ProjectMember` |
| 尚未注册 | 先引导完成注册（复用 WeKnora 注册/OIDC 入口），回到上一行 |

第二行需要租户级权限（WeKnora 的 `/tenants/:id/members` 要求 Owner+）。因此**只有同时具备租户管理权和 `project.members.manage` 的人**能走合并路径；否则 LingDoc 应生成一条“需要租户管理员协助”的待办并说明原因，而不是静默失败或让用户在两个系统间来回切换。

### 8.3 状态联动：租户先动，项目跟随

租户侧的成员状态变化必须能传导到项目，否则会出现“人已被停用、项目仍以为他能批变更”的裂缝。传导方向始终是**租户 → 项目**：

```text
WeKnora: TenantMember.status → suspended / 移除 / 角色降级
        ↓
LingDoc: 该用户在本项目的 ProjectMember 标记失效（suspended_by_tenant）
        ↓
若此人是项目 Owner：
        项目进入 owner_unavailable
        ├ changeset.apply、delivery.export 与 project.archive 无人可执行，入口置灰并说明原因
        ├ 项目首页与成员页显示阻断提示，给出两条解除路径：
        │   恢复 Owner 的租户身份，或由租户 Owner / 平台管理员指定受让人发起转让（见第 4 节）
        └ 通知租户 Owner 与项目 Admin（若项目内已配置该项目角色）——租户 Admin 无权发起转让，通知它无法解除该状态
```

注意约束范围：**只冻结 Owner 独有的三项能力**，不冻结编辑、调研、审阅等日常协作——那些不需要 Owner 参与，冻结它们会让项目无谓停摆。真正被卡住的是“改变项目语义”、“放行交付”和“归档项目”。解除方式有两条：恢复 Owner 的租户身份，或按第 4 节转让所有权（Owner 已被停用时由租户 `Owner` 或平台管理员指定受让人发起）。

实现上分两层，缺一不可：

- **请求时检查（必须）**：`ResolveProject` 每次解析项目时，校验 Owner 与当前成员的租户成员状态是否仍为 active；发现失效立即降级并写审计。这一层不依赖 WeKnora 任何改动，是正确性的底线。
- **事件订阅（增强）**：若 WeKnora 能发布租户成员状态变化事件，LingDoc 订阅并即时更新。事件只用于及时性，不能替代请求时检查——事件可能丢失，而权限判断不能有窗口期。

若 WeKnora 当前没有该事件，新增时应只增不减、不改变现有行为，避免影响既有调用方。

### 8.4 单向原则

- 移除项目成员**不等于**移出租户；LingDoc 只在项目范围内生效。
- 调整项目角色**不回写**租户角色。
- 项目归档**不改变**任何租户成员状态。

租户状态决定项目成员的有效性上限，项目侧操作不回写租户。这条单向性是两层模型可维护的前提：一旦允许反向写入，两个系统就会争夺同一事实。

## 9. 前端交互

复用 WeKnora 的登录、租户切换、成员邀请和知识库权限提示，在 LingDoc 增加：

- 项目成员页：一张表同时显示租户角色、治理角色、职能角色、章节责任、资料范围和有效能力。**租户角色列只读**，点击跳转 WeKnora Settings；治理/职能角色列在本页可编辑。失效状态（待接受邀请、被租户 suspended、Owner 不可用）显式展示，不静默隐藏。
- 邀请弹窗：先选治理角色（Admin / Member——`Owner` 不在邀请中出现，只能由转让产生），再勾选职能角色（Author / Researcher / Reviewer / Observer，可多选），并可限定章节、材料或交付版本 scope；五种预设组合列在最前作快捷参考，其中前四种可一键选中，「课题负责人」（即 `Owner`）仅作说明展示、在本步不可选——它由建项产生，之后只能经转让变更。若对方尚不在本租户，弹窗按第 8.2 节的分支提示后续步骤，缺少租户管理权时明确说明需要谁协助。
- 页面上下文栏：`当前租户 / 当前项目 / 治理角色 / 职能角色 / 可用资料范围`；
- 按钮按 capability 显示，但所有操作仍以后端检查为准；
- 拒绝提示区分“项目角色不足”“知识库共享级别不足”“API Key scope 不足”“租户成员身份已失效”“当前版本需要人工确认”；
- 证据、导入候选、ChangeSet 和交付页面展示操作者、确认者、角色、时间和版本。

Beta 仍不在前端复制一套租户角色编辑器：LingDoc 可以**发起**租户邀请，但租户角色本身只在 WeKnora Settings 中编辑，LingDoc 只管理项目成员与项目能力。管理面的分工与状态联动见第 8 节。

## 10. 数据隔离、拒绝策略与验收

- 默认拒绝（fail closed）；权限查询出错时不能降级为允许。
- 对不可见项目，建议返回统一的 `404 project not found`，减少项目 ID 枚举；已确认身份但能力不足的操作返回结构化 `403`，包含机器可读原因码。
- 所有写操作携带 `tenant_id`、`project_id`、`actor_id`、`effective_role/capabilities`、资源 ID 和版本号，防止跨租户写入。
- 项目归档、成员移除、证据确认、ChangeSet 应用和交付发布必须写不可变审计事件。
- 测试至少覆盖：租户切换、suspended 成员、共享 KB viewer/editor、API Key scope、shared-agent 只读、项目成员移除后的旧 token、跨租户 ID 替换、自写自审不被误拦、陈旧 ChangeSet 拒绝应用、Owner 唯一性、邀请不可授予 `Owner`、转让的原子性与受让人确认（含 `owner_unavailable` 下的旁路发起）、转让后受让人职能轴被清空且能力等于全集、降级方职能轴为空即 `Observer`、未指派 scope 的职能角色对章节写权为空、草稿期授予的授权在立项确认后不被重置、Research Agent 导入确认和并发版本冲突。

## 11. 分阶段落地

阶段编号与 [`06-路线图与里程碑`](06-路线图与里程碑.md) 对齐：P0–P3 是构建阶段，P4 交付首个对外版本 Beta；[`01-产品蓝图与范围`](01-产品蓝图与范围.md) 第 3 节定义的 Beta 范围包含本文的全部权限设计。

**P0–P3（构建）**：复用 WeKnora 租户/角色/KB share；新增 Project、ProjectMember、ProjectAsset/EvidenceCard 访问边界、双轴角色模型、完整 capability 与映射矩阵、项目—KB 绑定、统一鉴权中间件、确认与审计。第 3、4 节的设计在此间完整落地——任意“治理角色 + 职能角色”组合在数据层与界面同时可用（见 3.2），章节/材料/交付版本 scope 与鉴权链路第 5 步一并交付，所有权转让（原子性、受让人确认、`owner_unavailable` 旁路发起）可用。这三项缺一即产生结构性缺陷：没有 scope 输入源，第 5 步恒为全集、等于空转；没有转让，Owner 的租户身份一旦不可恢复，项目就永久卡死。

**P4（Beta）**：权限模型不变，只按真实使用调整——按调研量收紧各职能的 scope 默认值。05 号文档 P4 的交付重心是审计、指标、回退与反馈闭环，权限侧不应在这一阶段补功能。

**P5（生产增强）**：增加受限服务账号、临时授权、项目级策略模板、敏感材料脱敏/水印、组织级审批流；这些属于 Beta 之后的扩展，不应借机重新实现 WeKnora 的身份和租户系统。

## 12. 一句话原则

**WeKnora 决定“谁属于哪个租户、能否访问哪个知识库”；LingDoc 决定“这个人在当前课题中能对哪些项目对象执行什么操作”；任何一层拒绝，最终请求都必须拒绝。**

本文的操作演练见 [`14-权限管理操作模拟`](14-权限管理操作模拟.md)。
