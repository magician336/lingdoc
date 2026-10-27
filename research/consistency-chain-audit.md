# 变更影响链现状盘点(#744)

- 基线:main @ `0fd8afbb`(分支 `research/consistency-chain-audit` 的工作树,静态走查)
- 范围:G3 ChangeSet / 依赖图 / ImpactTask(Spec #131/#132)、#142 模板升级与 G3 稳定化交接、Spec 37(G2 证据失效与生成接入),服务端 + 前端真实暴露程度
- 口径:每条结论标注证据(文件:行号),状态分三档——**代码存在**(服务端实现+路由)、**前端可见**(产品 UI 接线,验收台/测试除外)、**端到端可走**(有跨 seam 测试佐证或 UI+API 全链接线)。未标「推断」的均为直接核实(grep/读码)。

## 一、旅程走查表

产品旅程:**用户改了研究条件(样本量 / 技术路线 / 预算科目)→ 系统提示哪些章节、图表、预算需要跟改 → 用户逐项核对 → 导出前自动一致性核对**。

### 环节 1:用户改研究条件

| 触发源 | 现状 | 证据 | 断点 |
|---|---|---|---|
| spec 表单字段(模板定义的研究条件,含数字类字段) | **前端可见**。draft 项目直接保存(`SaveSpec` 仅允许 draft,service_facade.go:1631);active 项目点「保存研究条件」直接转入变更预览(`saveConditions` Workspace.vue:2250-2255 → `createConditionsChangePreview`) | frontend/src/views/lingdoc/Workspace.vue:563-576(表单)、588-615(变更复核区);internal/lingdoc/workspacecore/service_facade.go:1611-1633 | 无。样本量这类条件若建为模板字段,链路完整 |
| 技术路线(RouteNode/RouteEdge) | **前端可见但门槛高**。「正式变更与运行」面板的语义修改区用表单手填对象编号/当前版本/依赖列表 | frontend/src/views/lingdoc/ImpactOpsPanel.vue:17(RouteNode 表单);workspacecore/semantic_delta.go:29-33、181-183(Kind 支持) | 无图形化路线编辑(Spec 31 明示 out of scope);能用但要懂版本号 |
| 预算科目 | **能力缺口**。无预算对象:依赖图对象 Kind 全集为 ProjectSpec/ProjectAsset/SourceAnchor/EvidenceCard/ResearchClaim/ChapterVersion/TemplateCopy/RuleSet/RuleBinding/RouteNode,无 budget/chart | ImpactOpsPanel.vue:136(objectKinds);workspacecore/impact_model.go:8-11(ObjectVersionRef 通用 kind) | 预算科目只能作为模板字段(走环节 1)或正文文本(无任何提示) |
| 正文里改研究条件(如直接改样本量数字) | **前端可见(自报式)**。章节提交时弹「修改性质」二选一(措辞/语义);选语义则正文不提交,提示「请在正式变更中填写实际条件或语义差异并评估」并跳转变更面板 | Workspace.vue:763(`@semantic` → `routeSemanticDraft`)、2820-2824(routeSemanticDraft + 提示文案) | 无自动识别「这处正文像研究条件」,完全依赖用户自报;选「措辞」则改动不进影响链 |

### 环节 2:系统提示哪些章节/图表/预算需要跟改

| 机制 | 现状 | 证据 | 断点 |
|---|---|---|---|
| 显式章节影响(旧切片) | **前端可见**。变更预览要求 Owner 勾选「需要重新复核的章节」,预览显示字段 old→new、已登记受影响章节,并列出「尚未核对范围」(未勾选章节)提醒 | Workspace.vue:592-608;http.go:142(POST /change-sets) | 影响范围靠人勾;系统不推断 |
| 依赖图传播(G3) | **代码存在 + 前端可见**。`buildImpactAssessment` 以变更对象为根(Fields/TemplateUpgrade 变更挂 ProjectSpec 根,impact_service.go:428-432),沿 confirmed/suggested 依赖边 BFS,产出 definite/possible 两级影响;评估指纹(InputHash)防旧预览被套用 | workspacecore/impact_service.go:412-593;ImpactOpsPanel.vue:43(影响预览:「必须处理对象/确定影响/可能影响/依赖边快照」分栏) | **最大断点:图的冷启动**。见下行 |
| 依赖边来源 | 人工登记表单(Origin user)+ AI 候选导入(Origin ai,需人工 confirm/modify/reject,拒绝记忆防复提)。**无任何自动派生边**(不会从正文引用、模板结构、字段-章节关系自动建边) | ImpactOpsPanel.vue:48-50(登记表单);impact_routes.go:120-128(dependencies 端点);workspacecore/impact_dependency_candidate.go:18-38、721-730(候选→边);PutDependencyEdge 调用点仅此两处(直接核实) | 零依赖边的项目里,改 spec 字段的影响提示退化为「只有人工勾选的章节」;AI 候选虽有导入端点(POST /dependency-candidates,impact_dependency_candidate_routes.go:28),但服务端不主动跑模型产候选,需外部模型结果导入 |
| 图表跟改 | **能力缺口**。无 Chart/Figure 对象;「图表需要跟改」最多落到章节粒度(ChapterVersion 的 REVIEW_REQUIRED) | objectKinds(ImpactOpsPanel.vue:136);semantic_delta.go:173-183(6 种 delta kind) | 同预算科目,缺对象模型 |

### 环节 3:用户逐项核对

| 机制 | 现状 | 证据 | 断点 |
|---|---|---|---|
| Owner 应用变更 | **代码存在 + 前端可见 + 端到端可走**。应用原子完成:写 spec 新值、ContextRevision+1、失效受影响章节确认、创建逻辑 ImpactTask、审计;基线/章节版本漂移标 STALE;幂等键重放 | workspacecore/service_facade.go:2158 起(ApplyChangeSet;后半段 CurrentContextRevision++、commitChangeImpactTasks、InvalidateChapterConfirmations);workspacecore/impact_service.go:627-741;ImpactOpsPanel.vue:36-37(「Owner 应用/驳回」按钮) | 无 |
| ImpactTask 处置 | **前端可见**。任务列表含 action/severity/责任人/causes;支持分配、开始处理、等待人工、提交闭环证据、非阻断任务「接受本次不处理」;BLOCK_RELEASE 只能被交付 Preflight 解除;RECOMPUTE 要求真实检查回执;任务版本漂移标 STALE(不算闭环) | ImpactOpsPanel.vue:52-54;workspacecore/impact_model.go:125-148(stale 语义);impact_service.go:894(ActOnImpactTask) | 无 |
| 章节重新确认 | **前端可见**。应用后旧确认失效,章节走 ChapterCommitReview 重新确认 | service_facade ApplyChangeSet 内 InvalidateChapterConfirmations;frontend/src/views/lingdoc/ChapterCommitReview.vue | 无 |
| 证据失效传播(Spec 37) | **代码存在 + 前端可见(读时判定式)**。锚点不当前/卡确认不当前时,读取即呈现 NEEDS_REVIEW;ResearchPanel 可按状态过滤、显示 invalid_reason、要求复核、人工确认适用范围 | workspacecore/agent_object_storage.go:172-202(IsAnchorCurrent / cardConfirmationCurrent 失败 → State=NEEDS_REVIEW);frontend/src/views/lingdoc/ResearchPanel.vue:41-42、138、266 | Spec 37 设想的「显式局部失效传播(写库的 NEEDS_REVIEW/STALE 状态 + 触发版本)」落地为按需判定;用户可见效果等价(推断:失败恢复入口、basis 差异展示等细粒度 UX 未逐项核对) |
| 稳定化 | **前端可见**。「检查稳定化」重读当前任务/确认/来源/版本,不满足返回 BLOCKED+原因清单;STALE 任务单列(既非闭环也非阻断) | impact_service.go:1234(StabilizeRevision);ImpactOpsPanel.vue:44 | 无 |
| 模板升级交接(#142) | **代码存在 + 前端可见(部分)**。Workspace 模板抽屉可「创建模板升级评估」,预览字段/章节/规则变化、将失效的旧确认数、问题处置与快照影响,Owner 应用/驳回;G4 审计有回归测试 | Workspace.vue:219、240-256;internal/lingdoc/workspace/full_workflow_g4_audit_regression_test.go | `TemplateUpgradeProgressPanel.vue`(170 行,展示升级 ImpactTask 进度/稳定化阻断/恢复动作,注释标明 Spec #142 前端面)**已写好但未被任何产品组件或路由引用**(只有其测试编译它)——升级后任务进度要走通用 ImpactOpsPanel,专用 UX 未接线 |

### 环节 4:导出前自动一致性核对

| 机制 | 现状 | 证据 | 断点 |
|---|---|---|---|
| 交付检查(规则 Preflight) | **前端可见**。「交付检查」跑规则评估 + 模板场景检查,产出 ValidationIssue(可处置)与检查回执;回执可一键「用此回执处理正式变更」(闭环 BLOCK_RELEASE 任务) | frontend/src/views/lingdoc/DeliveryPanel.vue:425-470;workspace/full_workflow_guard.go:131-203(Check 写 ImpactComputationReceipt) | 无 |
| 冻结快照闸门 | **代码存在 + 前端可见**。冻结(freeze)前置 `AssertRevisionStabilized`:当前 revision 的 ChangeSet 必须 STABILIZED,且 stabilizationConditions 全过(未闭环任务、阻断问题、确认、版本一致性) | workspacecore/release_gate.go:207-297;full_workflow_guard.go:91、213-215 | 无 |
| 导出闸门 | **代码存在 + 前端可见 + 端到端可走**。导出 Start/Download 再查 `AssertRevisionStabilized` + 快照当前性;前端禁用不当前快照的导出按钮 | full_workflow_guard.go:264、292-301;DeliveryPanel.vue:517-533(canExportSnapshot) | 无 |
| 全链回归 | **端到端可走**。`impact_to_export_test.go` 用真实组合(SQLite+生产 GORM 源+真实守卫)驱动 ChangeSet apply → ImpactTask → 处置/确认 → StabilizeRevision → 冻结 Preflight → READY → 导出/下载,且每个阻断场景配对恢复路径 | internal/lingdoc/workspace/impact_to_export_test.go:9-21(注释声明范围) | 无 |

### 变更触发源小结(票面关键问题)

「用户改了正文里的一处研究条件,系统影响提示」这条链**存在但靠自报**:正文提交时的措辞/语义二选一把语义改动引到 ChangeSet(Workspace.vue:763、2820-2824)。除此之外,系统侧触发源为模板升级(Workspace.vue:240-256)与规则绑定(RuleBinding delta,ImpactOpsPanel.vue:16)。没有任何一条路径会在普通措辞提交后回头检测研究条件变化。

## 二、地基完成度结论(环节清单口径)

- **服务端地基基本齐全**:14 个环节全部「代码存在」,HTTP seam 完整(drafts/assess/apply/reject/reassess/stabilize/reverse/preflight、dependencies、dependency-candidates、impact-tasks、route-nodes、context-revisions,impact_routes.go:78-189 + http.go:140-145),迁移齐备(000032 change_sets、000037 lingdoc_g3_records 等),且 apply→task→stabilize→export 有端到端测试。「改研究条件 → 提示 → 核对 → 导出拦截」的骨架在服务端是闭环的。
- **前端主链已接线**:条件变更预览、影响面板(草案/评估/影响预览/任务处置/稳定化)、交付面板、证据面板、模板升级预览/应用,均在 Workspace 产品界面可达(入口:Workspace.vue:132「正式变更与运行 →」)。
- **三个结构性缺口(缺能力,非接线)**:
  1. **依赖图冷启动**:无自动建边机制(正文引用、模板字段-章节关系、锚点引用都不自动成边),新项目零边时 G3 影响提示退化为「人工勾选章节」——这是「改一处、全篇跟改」卖点的最大空洞。
  2. **正文研究条件无自动识别**:语义/措辞二选一靠用户自报;错选「措辞」的实质性条件变化不进影响链。
  3. **图表与预算科目无对象模型**:影响提示只能到章节粒度,无法说「某张图/某科目需要跟改」。

## 三、对「一致性闭环」方案的建议

**UI 接线类(改前端即可,能力已在)**:
- 挂载已写好的 `TemplateUpgradeProgressPanel.vue`(升级任务进度/稳定化阻断/恢复动作),或明确废弃它并统一走 ImpactOpsPanel(二选一,避免 #142 交接的专用面漂在仓里)。
- 统一两条变更创建入口:旧 POST /change-sets(显式勾章节)与新 POST /change-sets/drafts + assess(依赖图)并存(Workspace.vue:588 区 vs ImpactOpsPanel.vue:9 区),同一用户旅程出现两套表单与两套影响口径,应收敛为「起草 → 评估(依赖图+显式章节合并展示)→ 应用」。
- 影响预览嵌入写作流:语义修改跳转后用户面对的是运维风格表单(手填对象 ID/版本),可把「本次必须处理的对象」以章节/卡片链接形式回链到编辑器。

**缺能力类(需要新服务端机制)**:
- **依赖边自动派生(优先级最高)**:从正文 CitationUsage/ReviewItems/锚点引用、模板字段与章节的静态关系自动生成 suggested 边(走既有 AI 候选确认流),解决零图冷启动。已有 suggested 边「不阻断交付」的安全语义(impact_service.go:482、541-544)可作为默认档。
- **正文研究条件变更检测**:措辞提交时对正文中已登记的条件值(模板字段值出现处)做一致性比对,发现疑似变更即引导走语义路径;可复用 working copy 的 spec_revision 基线机制。
- **图表/预算对象**:最小做法是给 ChapterVersion 内的图表块登记轻量对象 ref(kind=ChartBlock)进依赖图;预算科目则建议模板字段结构化(预算表字段),避免新造大对象。

## 附:Spec 与落地差距速览

- Spec 30(#131,CLOSED)ChangeSet 生命周期:生命周期状态机、幂等、stale、逆向、审计均已落地(impact_routes.go 全端点 + change_set_*_test.go 系);Story 15「AI 准备结构化提案」只有候选导入 seam,无服务端模型直连(推断:未逐票核对 agent 面全部能力)。
- Spec 31(#132,CLOSED)依赖图/ImpactTask/稳定化:解析器、任务身份合并、stale 语义、G5 责任分配、稳定化条件、STABILIZED≠READY 均落地;差距同「冷启动」——「AI propose dependency candidates」依赖外部导入,图不自动生长。
- Spec 37(G2 证据失效):失效判定为读时计算而非显式传播写库;正式生成/交付侧靠确认当前性 fail-closed(agent_action_service.go:1439 只接受 CANDIDATE/NEEDS_REVIEW/INSUFFICIENT 之外的正式态)。
- Spec 41(#142,OPEN)模板升级交接:服务端交接完整(影响七类、原子 apply、G3 任务、审计回归测试);前端预览/应用可见,专用进度面板未接线。
- Spec 42(G1 草稿与立项):与变更链的衔接点是 SaveSpec 仅 draft、active 走 ChangeSet(service_facade.go:1631),已落地。

## 附:主要证据文件索引

- 服务端路由:internal/lingdoc/workspace/impact_routes.go、http.go:140-145、full_workflow_http.go、impact_dependency_candidate_routes.go
- 评估与任务:internal/lingdoc/workspacecore/impact_service.go(buildImpactAssessment:412、ActOnImpactTask:894、StabilizeRevision:1234)
- 应用与失效:internal/lingdoc/workspacecore/service_facade.go:2158(ApplyChangeSet)、change_template_fields.go、semantic_delta.go
- 导出闸门:internal/lingdoc/workspacecore/release_gate.go:207、internal/lingdoc/workspace/full_workflow_guard.go、impact_to_export_test.go
- 证据失效:internal/lingdoc/workspacecore/agent_object_storage.go:172-202、agent_action_service.go
- 前端:frontend/src/views/lingdoc/Workspace.vue(563-615 条件变更、755 面板挂载、2820-2824 语义路由)、ImpactOpsPanel.vue、DeliveryPanel.vue、ResearchPanel.vue、DependencyCandidatePanel.vue;frontend/src/components/TemplateUpgradeProgressPanel.vue(未挂载)
- 迁移:migrations/sqlite/000032_lingdoc_change_sets.up.sql、000037_lingdoc_full_workflow.up.sql(lingdoc_g3_records 等)
