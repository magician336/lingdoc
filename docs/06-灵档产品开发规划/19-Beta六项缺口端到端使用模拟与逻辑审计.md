# Beta 六项缺口端到端使用模拟与逻辑审计

> 日期：2026-09-25  
> 文档性质：规划模拟与逻辑审计，不是当前功能演示，不代表 G1–G6 已实现。  
> 计划入口：[18-Beta六项缺口开发计划](18-Beta六项缺口开发计划.md)  
> 差距基线：[17-T01-T16与Beta差距清点](17-T01-T16与Beta差距清点.md)

## 1. 模拟目的和边界

本模拟假设 G1–G6 已按计划开发完成，验证一个课题组从首次建项到 Beta 导出的完整路径：

1. 创建草稿项目并邀请成员；
2. AI 辅助填写研究条件并保留溯源；
3. 调研智能体检索、导入来源和对话片段；
4. 建立 ResearchClaim、EvidenceCard 和章节候选；
5. 立项确认并进入 active；
6. 分章节生成、编辑和确认；
7. 修改研究对象，经过 ChangeSet/PCC 产生影响任务；
8. 处置证据、章节、路线图和模板问题；
9. 重新确认、模板检查、Preflight、冻结和导出；
10. 在撤权、异步乱序和灰度回退下继续工作。

模拟采用固定课题和脱敏材料。真实模型质量、真实 WeKnora 权限、Word/WPS 编辑均须在实际验收阶段另行记录。

## 2. 参与者和固定数据

### 2.1 团队

| 人员 | 租户角色 | 项目治理/职能角色 | scope | 本模拟职责 |
|---|---|---|---|---|
| 李老师 | owner | Project Owner | 全项目 | 确认基线、应用 ChangeSet、确认内容、放行导出 |
| 王同学 | contributor | Member + Author | Ch1、Ch2 | 草拟条件、编辑章节、发起生成 |
| 陈同学 | contributor | Member + Researcher | 无章节写 scope | 导入资料、整理候选证据 |
| 赵老师 | admin | Admin + Author | Ch5、Ch6 | 管理配置、分派任务、编辑路线图；不能学术确认或放行导出 |
| 周老师 | owner | 租户 Owner，非项目成员 | 无 | 模拟跨项目边界，并在租户侧恢复李老师身份 |

### 2.2 模板和材料

- 模板：教育部人文社科青年项目，2026，TemplateProfile edu-humanities-youth-2026-v1。
- 章节：选题依据（Ch1）、研究现状（Ch2）、研究目标与内容（Ch3）、研究方法（Ch4）、技术路线（Ch5）、进度与成果（Ch6）。
- M-01：团队既有公开论文，允许作为项目资料。
- M-02：前期问卷和统计摘要，仅授权当前项目。
- M-03：上一年度申请书，只作结构参考。
- M-04：本年度申报通知和模板依据。
- M-05：外部检索结果，起初只有搜索摘要，后续选择原文片段。
- 对话 S-12：AI 辅助讨论研究变量和方法，只有用户选择的轮次可导入。

## 3. 端到端模拟

### 3.1 第一幕：创建草稿项目

**用户动作**

王同学在项目列表点击“新建项目”，选择模板、填写项目名和拟定负责人李老师，并声明自己在草稿期的职能为 Author。

**预期请求**

CreateProject(template_profile_id, name, designated_owner, creator_function_role)

**预期状态**

~~~text
Project.status                   = draft
Project.current_context_revision = 0
Project.template_profile_id      = edu-humanities-youth-2026-v1
章节                             = 空
ChangeSet                        = 空
交付对象                          = 不存在
王同学                           = 临时 Owner
李老师                           = 待接受所有权转让
~~~

指定负责人未确认前，系统不能出现零 Owner。王同学可保存草稿，但不能把临时 Owner 显示成永久负责人。

**逻辑断言**

- 创建不生成章节、证据卡、主张或 ReleaseSnapshot。
- 未加入项目的周老师访问项目返回 404，不泄露存在性。
- 创建请求重放返回原项目，不生成第二个项目。

### 3.2 第二幕：草稿期协作和 AI 填写

李老师接受所有权转让，邀请陈同学作为 Researcher。王同学和李老师共同编辑 ProjectSpec。王同学选取 S-12 的第 12–18 轮，让 AI 建议“拟用方法”和“核心变量”。

草稿期李老师试换另一已发布模板。界面先按稳定 field_id 预览差异：兼容字段保留值、状态和溯源；被移除字段进入可见的 orphaned 区；新增必填字段进入 missing。李老师取消后不发生写入，随后选择原模板继续。若确认切换，需以 project_version 条件写入并留审计；missing 未处理前不能立项。

**预期状态**

~~~text
Project.status                  = draft
Context Revision                = 0
ProjectSpec.method.source       = ai_conversation
ProjectSpec.method.status       = pending_confirmation
ProjectSpec.method.provenance   = S-12 / turns 12–18 / selected spans
ProjectMember(陈同学)            = active
陈同学的章节写 scope             = 空集（章节尚未生成）
~~~

界面必须显示 AI 填写值、人工值、AI 后人工修改值三种来源，以及每个 AI 值的会话、轮次和选用片段。保存冲突时保留本地输入。

**逻辑断言**

- 草稿期条件修改不产生 ChangeSet，不递增 Context Revision。
- 草稿期可以上传或关联 ProjectAsset，可以产生调研候选。
- 草稿期不能创建正式 ResearchClaim 或 EvidenceCard。
- 陈同学的 Researcher 角色不能因为 scope 为空而获得全部章节写权。
- 模板切换不产生 ChangeSet 或 Context Revision，不按字段显示名称猜测等价，也不静默丢失 AI 溯源。
- ProjectSpec 的 AI 值与同一会话片段的资料导入分别引用带 purpose 的 ProvenanceRecord；撤权会使两种用途都待复核。

### 3.3 第三幕：调研智能体和候选来源

陈同学创建调研任务：

~~~text
问题：家庭资源是否影响创业教育与创业意向之间的关系？
范围：中文/英文，2015—2026，论文和政策材料
排除：无作者、无出处、无法定位原文
输出：候选来源、比较表、待验证问题
~~~

Research Agent 返回三类结果：

| 结果 | 内容 | 允许动作 |
|---|---|---|
| R-01 | 有作者、DOI 和可定位原文 | 选取片段，创建 ImportCandidate |
| R-02 | 聚合综述页，无原始出处 | 仅作背景线索导入，不能建证据卡 |
| R-03 | 搜索摘要，无原文定位 | 不得导入正式 ProjectAsset |

陈同学选择 R-01 原文片段并导入 M-05；同时选择 S-12 中关于变量定义的两轮对话作为 ConversationExcerpt。

**预期状态**

~~~text
R-01 -> ImportCandidate -> ProjectAsset(M-05) -> SourceAnchor(A-05)
S-12 selected turns -> ConversationExcerpt(C-12) -> ImportCandidate
R-02 -> ProjectAsset(tier=background)
R-03 -> rejected_at_import
~~~

**逻辑断言**

- 搜索摘要可以显示在发现层，但不能直接支撑正文。
- 导入候选必须记录来源、作者、工具、时间、层级和授权范围。
- M-05 未 READY 或不在项目允许集合时，后续 EvidenceCard 创建必须失败。
- 草稿期候选在立项时不能因为 revision 数字从 0 变 1 就自动失效。

### 3.4 第四幕：立项确认

王同学继续补充条件；李老师打开立项确认页。确认页显示：

~~~text
自上次查看以来被他人修改：
- 核心变量新增“家庭资源”
- 样本范围由“本科生”改为“普通本科生”

仍是 AI 填写、未经人工改动：
- 拟用方法（S-12，第 12–18 轮）
~~~

李老师查看溯源并确认。

**预期事务结果**

~~~text
Project.status                  draft -> active
Context Revision                0 -> 1
章节                            生成 Ch1–Ch6 骨架
Delivery.status                 不存在 -> NOT_READY
草稿期候选                      保留，不能标 STALE
陈同学章节 scope                仍为空，等待正式指派
~~~

**逻辑断言**

- 未查看两类差异时，ConfirmProjectBaseline 被拒绝。
- 0→1 是冻结基线，不是实质语义变更，不创建 ChangeSet。
- 立项后研究条件的实质编辑没有普通保存入口，只能创建 ChangeSet。
- 草稿期资料授权范围保持不变。

### 3.5 第五幕：创建主张、证据卡和章节候选

立项后，王同学先创建 ResearchClaim C-01；陈同学确认 M-05 已 READY 且 A-05 为当前可定位 SourceAnchor，再把原文片段整理成候选 EvidenceCard。缺少 Claim 或 Anchor 的请求显式失败，不产生孤儿卡。

~~~text
C-01：家庭资源可能调节创业教育对创业意向的影响。
~~~

EvidenceCard 记录：

~~~text
E-01.source_anchor       = A-05
E-01.claim_refs          = [C-01]
E-01.support_type        = partial
E-01.scope               = 家庭支持定义和调节效应背景
E-01.limitations         = 原文样本与本项目人群不同
E-01.status              = CANDIDATE
~~~

李老师以 Owner 身份确认 E-01 后，才允许它作为已确认证据进入章节生成上下文。王同学此时只有 Author 职能，不能代行 evidence.confirm。

**逻辑断言**

- EvidenceCard 必须关联可回原文的 SourceAnchor。
- 证据卡只能表达支持范围和限制，不能把“有文献”自动变成“主张成立”。
- Admin + Author 的赵老师可以编辑 Ch6，但不能调用 evidence.confirm 或 content.confirm。
- 内容确认绑定 ChapterVersion、Context Revision、资料版本和模板版本。

### 3.6 第六幕：生成、采纳和确认

王同学在 Ch1 发起生成，服务端记录 basis：

~~~text
project_revision = 1
chapter_version  = null
template_version = v1
asset_revisions  = [M-01:v2, M-02:v1, M-05:v1]
evidence_ids     = [E-01]
~~~

任务返回候选 CAND-01，包含引用和 review_items。王同学预览后采纳为 ChapterVersion V1。人工修改后产生 V2，王同学提交待核项处置建议，李老师核对并确认 V2；处置建议本身不等于内容确认。

**逻辑断言**

- 候选生成成功不等于候选仍 fresh；采纳前必须重新检查 basis。
- 已有章节再次采纳必须明确整章替换，不能隐式合并。
- 手动编辑继承待核项，不能通过前端省略 review_items 伪造已处置。
- 确认只绑定 V2，之后编辑会使该确认失效。

### 3.7 第七幕：权限变更和撤权恢复

赵老师被授予 Admin + Author，scope 为 Ch5、Ch6。她尝试确认 Ch6 时得到 CAPABILITY_MISSING。她可 dismiss warning/info 误报或按 delivery.preflight 能力 waive warning/info，却不能 dismiss blocking、waive blocking 或导出。随后 WeKnora 租户管理员暂停陈同学的租户身份。

**预期状态**

~~~text
陈同学后续读取项目       = 被租户身份检查拦截
ProjectAsset/M-05        = 不向陈同学返回
ProjectMember            = suspended_by_tenant（若事件可用）
其他成员                  = 按自身权限继续
~~~

李老师被租户停用时：

~~~text
Project.owner_unavailable = true
changeset.apply           = frozen
delivery.export           = frozen
access-status             = 只返回恢复动作，不返回正文/资料标题
~~~

**逻辑断言**

- 租户暂停不能只依赖事件；每次请求必须重新检查租户身份。
- 项目移除成员不等于移出租户。
- Owner 失效时不能留下零 Owner，也不能用旧缓存继续放行导出。
- 撤权后旧 ExportArtifact 是否可下载必须再次检查当前资料和项目权限。

周老师作为租户 Owner 恢复李老师的租户 active 身份；服务端下一个请求重新检查租户状态，owner_unavailable 消失。只有恢复完成，李老师才能继续应用 ChangeSet。若改走所有权转让，须使用 predecessor/successor/project_version 条件事务，竞争失败不产生双 Owner 或零 Owner。历史快照只保证审计元数据仍存在；正文、来源摘录和文件下载分别重新鉴权。

### 3.8 第八幕：应用一次实质变更

李老师提出 ChangeSet CS-01：

~~~text
将研究对象从“普通本科生”收窄为“低年级本科生”，
并新增“家庭资源”变量的调节效应分析。
base_revision = 1
~~~

系统先评估，不立即覆盖正式状态。

**影响预览**

~~~text
受影响：
- C-01 主张
- E-01 证据卡（样本不一致，需要重核）
- Ch1 选题依据
- Ch2 研究现状
- Ch4 研究方法
- Ch5 技术路线
- Ch6 进度与成果
- 当前确认记录
- 交付状态

不受影响：
- 与研究对象无依赖的管理备注
~~~

李老师确认应用，产生 revision 2 和三类 ImpactTask：重新验证 E-01，复核 Ch1/Ch2/Ch4，更新 RouteNode，重新执行模板/交付检查。每条任务按 change_set_id、target_ref、target_revision、action_type 唯一标识，重复事件返回原任务。

**逻辑断言**

- CS-01 的 base_revision 不是当前 revision 时，Apply 被拒绝且无部分写入。
- 应用后旧确认保留为历史，不继续标记当前有效。
- 无关章节不被全量标记为失效。
- 未关闭的 BLOCKING ImpactTask 不能进入 STABILIZED、READY 或导出。

### 3.9 第九幕：异步乱序和证据重核

A-EVIDENCE-01 仍在运行时，王同学提出 CS-02 调整样本量；李老师评估并应用，产生 revision 3。A 任务返回时 basis_revision=2，当前 revision=3。

**预期处理**

~~~text
A-EVIDENCE-01 result.status = STALE
不得覆盖 revision 3 的证据/确认
保留结果和原因供审计
重新创建针对 revision 3 的任务
~~~

**逻辑断言**

- 任务的 succeeded 和 fresh/stale 是两个字段。
- 旧结果可以被查看，但不能直接写入当前对象。
- 重试是新任务，不复用旧结果冒充当前验证。
- 影响任务不能因为“任务完成”自动关闭，必须有责任人处置。
- 重投同一影响动作只返回原任务；针对 revision 3 的新任务使用新的 target_revision，不能关闭 revision 2 的任务。

### 3.10 第十幕：模板升级和规则问题

业务专家发布 TemplateProfile v2，新增“伦理审批说明”必要字段和 blocking 规则 R-ETH-01。已有项目不能静默改绑 v2。

李老师选择把模板升级作为 CS-03（template_upgrade）受控变更，先预览字段、章节、路线图、规则和确认影响，再以当前 base_revision=3 应用；项目模板引用与 Context Revision 在同一事务切到 v2 / revision 4。

~~~text
旧模板 v1 保留在历史快照
当前项目进入模板迁移评估
新增字段状态 = missing
R-ETH-01 = OPEN / BLOCKING
~~~

王同学补充伦理说明；规则求值产生 R-ETH-01 RESOLVED、R-METHOD-02 OPEN/WARNING。若 R-METHOD-02 确实是规则误判，Reviewer 执行 dismiss 并填写理由；warning 若真实存在，按权限执行 waive。blocking 问题不能 waive。

**逻辑断言**

- 模板检查只反映当前编辑内容，不直接把交付状态改成 READY。
- Preflight 对冻结快照重新求值；写作期已通过不代表导出时仍通过。
- 已发布模板和规则不可原地修改，快照可以回放旧版本。
- dismiss、waive、resolve 三者不能共用一个无语义的关闭按钮。
- 必需 RouteNode 缺失、待复核或未冻结时产生 blocking；可选路线图按模板规则报告 warning/info。
- dismiss blocking 只移除当前规则/目标版本的问题阻断，不自动关闭 PCC ImpactTask；新版本重新求值可再出现。

### 3.11 第十一幕：稳定化、冻结和导出

所有针对当前 revision 4 的 ImpactTask 已由责任人处置，E-01 重新确认，相关章节与 RouteNode 产生新版本并确认，陈同学的租户身份已恢复。稳定化聚合器在同一目标 revision 上重新检查 ImpactTask、ValidationIssue、确认、资料版本和当前授权；全部通过后才标记稳定。

DeliveryService 执行一致读取、权限和资料指纹复核、保存完整 FrozenDeliveryInput、Preflight、生成 ReleaseSnapshot、生成 DOCX、人工打开编辑和下载再鉴权。

**预期状态**

~~~text
当前项目基线               = STABILIZED
ReleaseSnapshot RS-02       = READY
ExportArtifact              = VERIFIED
Project.current_revision    = 4
~~~

如果存在空章或 blocking issue：

~~~text
ReleaseSnapshot = BLOCKED
chapter_version_id = null（空章允许进入阻断快照）
ExportArtifact = 不生成可下载文件
~~~

**逻辑断言**

- FrozenDeliveryInput 保存正文、引用、待核项、确认、来源定位、hash 和权限关联集合。
- DOCX 生成失败不能创建假下载记录。
- 当前正文变化后，RS-02 的 is_current 变为 false，但文件字节和 hash 不变。
- 来源撤权后，旧文件可以保留审计元数据；正文/来源摘录和下载请求必须分别重新授权。

### 3.12 第十二幕：Beta 运行和回退

Beta 灰度期间只对内部测试租户启用 Research Agent 和新规则集。模型服务故障时任务保留失败原因，用户编辑保留，重试创建新任务，系统不回退到未标记的 Mock 输出。

模板迁移出现问题时关闭新模板的 feature flag，停止新项目绑定 v2；已应用 CS-03 的项目不能被后台静默改回 v1，只能保持 v2 并暂停新导出，或另建受控 ChangeSet 回迁：

~~~text
历史 RS-01       = 元数据仍可查询，正文/下载按当前权限检查
新项目            = 可选已验证的旧模板
当前 v2 项目       = 保持已应用版本；新任务明确返回暂停原因
~~~

G6 记录人工投入时间、来源支撑率、漏改数、规则/模型误报、未确认项数、导出损失数、任务陈旧率和撤权拒绝率。试用开始前由产品和 QA 锁定时间窗口、纳入项目、最低可报告样本量；每项指标报告分子、分母与缺失值，样本不足时仅报告个案。

## 4. 全流程状态账本

| 时间点 | Project | Context Revision | 证据/主张 | 内容/确认 | 变更 | 交付 |
|---|---|---:|---|---|---|---|
| 创建后 | draft | 0 | 只有候选池 | 无章节 | 无 | 无对象 |
| 草稿协作后 | draft | 0 | 候选/背景线索 | ProjectSpec 草稿 | 无 | 无对象 |
| 立项后 | active | 1 | 可建立 Claim/Card | 章节骨架，无确认 | 无 | NOT_READY |
| 初次写作后 | active | 1 | E-01 CONFIRMED | Ch1 V2 + confirmation | 无 | NOT_READY |
| CS-01 应用后 | active | 2 | E-01 NEEDS_REVIEW | 相关确认失效 | APPLIED | BLOCKED |
| CS-02 应用、旧任务返回后 | active | 3 | 旧结果 STALE | 新版本待复核 | APPLIED | BLOCKED |
| CS-03 模板升级后 | active | 4 | 依赖需重核 | 模板/路线图/确认待复核 | APPLIED | BLOCKED |
| 影响处置后 | active | 4 | 重新确认 | 新版本确认 | 当前基线 STABILIZED | READY |
| 导出后 | active | 4 | 证据可追溯 | 快照绑定确认 | 历史变更可审计 | EXPORTED |
| 正文再次编辑 | active | 4 | 仅受影响确认失效 | 新 ChapterVersion；旧确认历史 | 无须因纯正文编辑创建 ChangeSet | 旧快照历史 |

## 5. 逻辑审计结果

### 5.1 已通过的逻辑链

| 编号 | 检查项 | 结果 |
|---|---|---|
| PASS-01 | 草稿期不产生正式主张/证据，立项后才建立证据闭环 | 通过 |
| PASS-02 | 0→1 基线确认不把草稿候选机械标为 STALE | 通过 |
| PASS-03 | 生成候选、整章采纳、编辑、确认分别绑定版本 | 通过 |
| PASS-04 | ChangeSet 应用后只影响依赖对象，旧确认保留历史 | 通过 |
| PASS-05 | 异步旧结果不能覆盖新 revision | 通过 |
| PASS-06 | blocking、warning、info 的处置语义不同 | 通过 |
| PASS-07 | 撤权、Owner 失效和下载再鉴权形成 fail-closed 链 | 通过 |
| PASS-08 | 冻结快照内容不可变，动态 is_current 不进入 digest | 通过 |
| PASS-09 | Mock、真实服务、真实文件和用户试用分开验收 | 通过 |

### 5.2 原发现与计划修订结果

| 编号 | 严重度 | 原发现 | 修订后的规则与验收 | 落点 |
|---|---|---|---|---|
| ISSUE-01 | P0 | 草稿期切模板缺字段迁移 | 稳定 field_id 映射、orphaned/missing、预览与条件写入；立项前处理必填 | G1、G4，3.2 幕 |
| ISSUE-02 | P0 | EvidenceCard 建立顺序不明 | READY 资料与 Anchor → 当前 Claim → Candidate Card；失败无孤儿卡 | G2，3.5 幕 |
| ISSUE-03 | P0 | active 项目可能直接改模板 | template_upgrade ChangeSet 评估/应用，旧快照留旧版本 | G3、G4，3.10 幕 |
| ISSUE-04 | P0 | blocking dismiss 后阻断语义不明 | DISMISSED 仅移除同规则/目标/规则集版本问题；不自动关闭 ImpactTask | G4、G3，3.10–3.11 幕 |
| ISSUE-05 | P1 | ImpactTask 与 ValidationIssue 关闭条件分裂 | 稳定化聚合器按当前 revision 复核任务、问题、确认和资料 | G3、G4，3.11 幕 |
| ISSUE-06 | P1 | waive/dismiss 权限不明 | G5 矩阵区分 warning/info 与 blocking；Admin 可裁定低级误报，不能裁定 blocking | G5，3.7 幕 |
| ISSUE-07 | P1 | 历史快照可能绕过撤权 | 元数据、正文/摘录、文件下载分层并重新鉴权 | G5、G6，3.7、3.12 幕 |
| ISSUE-08 | P1 | 条件字段和对话导入溯源分裂 | ProvenanceRecord 带 purpose、版本、授权和反向失效 | G1、G2，3.2–3.3 幕 |
| ISSUE-09 | P1 | RouteNode 缺失仍可能 READY | 模板声明必需性；必需项缺失/待核阻断 Preflight，DOCX 检查图形 | G3、G4、G6，3.10–3.11 幕 |
| ISSUE-10 | P2 | 指标不可比较 | G6 指标字典定义分母/窗口/责任，试用前锁样本与最低报告量 | G6，3.12 幕 |
| ISSUE-11 | P2 | 并发转让产生双/零 Owner | 条件事务、唯一 Owner、受让人租户状态复核 | G1、G5，3.1、3.7 幕 |
| ISSUE-12 | P2 | 影响任务重试重复副作用 | 任务身份四元组与唯一约束；旧 revision 任务不能关闭新任务 | G3，3.8–3.9 幕 |

## 6. 实施前仍需完成的验证

以上 12 项**计划层面的逻辑缺口已补入 G1–G6**；这不表示接口、迁移、权限或真实运行已实现。进入跨域 PR 前，提供方须把新增规则转成 OpenAPI、状态机、迁移和可复现失败样例。真实 Beta 放行还需 G6 的完整验收矩阵通过。特定业务模板的路线图必需性、规则阈值和试用最低样本量由业务/产品与 QA 在试用前明确记录，不能由开发者在实现中自行补造。

## 7. 模拟结论

按当前 G1–G6 计划，主流程在概念上可以闭环：

~~~text
draft
  → baseline revision 1
  → source import / claim / evidence
  → chapter generation / confirmation
  → ChangeSet revision 2/3/4
  → impact handling / revalidation
  → template check / preflight
  → frozen snapshot / DOCX export
~~~

原发现的 12 个逻辑问题已在计划文本中给出确定规则和新增验收，见 5.2 的逐项映射。文档模拟已同步修正角色、确认、Owner 恢复和 revision 时序。当前结论限于**计划内部逻辑闭环**；仍须把规则落成契约和代码，并用真实依赖验证，才能宣称 Beta 能力完成。
