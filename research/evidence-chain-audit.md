# 证据关联链现状盘点(#745)

> 基线:main @ `0fd8afbb`(Merge PR #739)。方法:静态走查 + 证据引用(仓库开发门禁口径)。
> 所有「文件:行号」均相对本工作树根;除特别标注「推断」外,均为直接核实。
> 核对对象:产品卖点「AI 写的东西可查证——AI 生成段落绑定原文位置、支持范围和待核问题;检索结果只进候选队列、不自动成为证据」。

## 0. 盘点范围与四档口径

每条结论按三档区分:**端点存在**(服务端 HTTP 面 + 持久化)、**前端可见**(有 UI 入口/渲染)、**端到端可走**(用户从界面完成全链且服务端强制)。第四档「缺失」用于完全不存在的能力。

---

## 1. 卖点承诺核对表

### 承诺①「AI 生成段落绑定原文位置」 —— **部分成立**(绑定存在,但粒度是检索分块级,不是锚点级)

| 环节 | 现状 | 证据 |
|---|---|---|
| 生成指令强制标注 | 已成立 | 系统提示词:「所有事实性表述都必须用 [[source:SOURCE_ID]] 在正文中标注……证据不足时写入 review_items」+ 证据纪律指令 `internal/lingdoc/workspace/generation.go:539`、`internal/lingdoc/workspace/generation_evidence.go:381` |
| 生成结果服务端校验 | 已成立 | 缺引用→`missing_source_reference` 失败;越界引用→`invalid_source_reference`;标记集合与 source_ids 必须严格相等:`internal/lingdoc/generation/service.go:677,687,731,1015-1018`(sourcemarker 校验) |
| 候选采纳二次校验 | 已成立 | `validateSourceReferences`:`internal/lingdoc/candidateadoption/candidate_adoption.go:485-509`;章节保存同规则(`frontend/src/views/lingdoc/chapterCitations.ts:7-10` 注释并实现同一规则) |
| 正文角标渲染 + 回跳 | 已成立(前端可见+可走) | 编辑器内联 `[来源]` 角标,`data-source-ref` + 点击 `source-open` 回看原文:`frontend/src/views/lingdoc/editor/LingDocEditor.vue:57-58,173-174`;sourceLabels 由 Workspace 注入 |
| 绑定粒度 | **缺口** | 生成段落绑定的是 **chunk 级 source_id**(检索分块)。SourceAnchor 级(页码/段落/坐标/选区哈希/人工核对,`internal/lingdoc/workspacecore/agent_research_model.go:269-295`)是**人工选文建立**的另一层(`frontend/src/views/lingdoc/ResearchPanel.vue:26-27`),经 EvidenceCard.AnchorRefs 进入生成,但 AI 段落本身不会自动绑定到 anchor 级位置 |

**结论**:「段落 ↔ 原文」绑定在 chunk 粒度上端到端成立且服务端强校验;「段落 ↔ 原文精确位置(页/段/坐标)」不存在自动化路径,靠人工建锚点-建卡链补足。**推断**:把卖点读成「自动绑定到原文精确位置」则不成立;读成「每个事实表述可回指原文分块」则成立。

### 承诺②「支持范围、待核问题」 —— **已成立**(端到端)

| 环节 | 现状 | 证据 |
|---|---|---|
| 支持范围字段(卡级) | 已成立 | `EvidenceCard.Scope/ScopeChapterIDs/Limitations/Gaps/AlternativeExplanation` + `SupportType(supports/refutes/qualifies/background)`:`agent_research_model.go:347-366` |
| 支持范围字段(主张级) | 已成立 | `ResearchClaim.Scope/Status/Explanation`:`agent_research_model.go:313-325` |
| 确认语义含范围 | 已成立 | `EvidenceConfirmation.Meaning=「来源片段在指定范围内经人工核对;不表示研究主张已被证明。」`,`ConfirmEvidenceCard` 逐章授权:`internal/lingdoc/workspacecore/agent_action_service.go:1441-1453` |
| 待核状态机(卡级) | 已成立 | `ReviewEvidenceCard`→`INSUFFICIENT/NEEDS_REVIEW/SUPERSEDED`,取消已有确认:`agent_action_service.go:1458-1485` |
| 待核问题(生成级) | 已成立 | review_items 生成时格式与去重校验(`generation/service.go:735-743`);指令要求「适用范围或限制不明、材料不足和冲突时应保留待核表述并列入 review_items」(`generation_evidence.go:381`) |
| 待核问题(前端可见) | 已成立 | 候选稿面板「引用 X 条来源 · 待核事项 Y 条」(`Workspace.vue:546`);采纳对话框列待核事项(`frontend/src/components/LingDocCandidateAdoptionDialog.vue:258-262`);编辑器内待核高亮 + 「核查」按钮(`LingDocEditor.vue:135-153`);检查器「待核」标签页与计数(`Workspace.vue:270`);待核项与正文选段重新关联/清除锚点(`Workspace.vue:1029-1093`,`putReviewItemAnchor`) |

### 承诺③「检索结果只进候选队列、不自动成为证据」 —— **已成立**(多层强制点,均直接核实)

强制点链(每一跳都有人工门或状态闸):

1. 检索结果分级:`candidate_evidence/background_lead/discovery_entry/unread_search_summary/AI_GENERATED_CANDIDATE`,UI 明示「未读原文的搜索摘要」「AI 生成候选,不能直接确认」:`frontend/src/api/lingdoc/agentApi.ts:21`、`ResearchPanel.vue:144`;面板文案「搜索摘要不能直接变成正式证据。」:`ResearchPanel.vue:32`。
2. 检索结果→资料:必须建 `ImportCandidate`(状态 `awaiting_consent`),人工「明确确认保存」才 `imported`:路由 `internal/lingdoc/workspace/agent_routes.go:144-152`;前端确认/拒绝按钮 `ResearchPanel.vue:23`。
3. 资料→定位:人工在原文预览选中文字 + 勾选「我已打开原文,核对定位与摘录」才能存锚点:`ResearchPanel.vue:26-27`;服务端 `IsAnchorCurrent` 按选区哈希重核原文(`agent_object_storage.go:337-368`)。
4. 定位→卡:建卡即 `Status="CANDIDATE"`,要求 Confirmed 输入 + `HumanGateRequired` + 锚点全部 CURRENT + 项目 active:`agent_action_service.go:1257-1320`。
5. 卡→正式证据:`ConfirmEvidenceCard` 人工门 + 逐章授权,写入确认语义:`agent_action_service.go:1421-1456`。
6. 正式证据→生成:`SelectGenerationEvidence(formal=true)` 只接受 `CONFIRMED` 且确认范围含目标章的卡,非 CONFIRMED 进 `Pending` 并令 formal 失败;来源须 `ModelUseAllowed`:`agent_action_service.go:1600-1646`。
7. 生成→正文:产物是「候选稿」,入正文须走候选采纳:人工门禁 `RequireHumanDecision` + 确认载荷必填 + 工作副本修订号/正文摘要声明 + 采纳后当前授权复查:`candidate_adoption.go:160-192,315-322,396-403`。

**结论**:该承诺是三条里落地最完整的,不存在任何「检索结果自动变成证据」的旁路(静态走查未发现,直接核实口径)。

---

## 2. 三层基础设施现状

### 2.1 服务端端点(全部存在)

- 路由面(`internal/lingdoc/workspace/agent_routes.go`):`GET/POST source-anchors`(104-106,153-155)、`GET/POST research-claims`(107-109,156-158)、`GET/POST evidence-cards` + `:cardID` 详情 + `confirm` + `review`(110-112,159-167,193-204)、`research-tasks/runs` 全套、`import-candidates` 全套、`agent-context` 预览(190-192)、`action-proposals`(含 `create_evidence_card/confirm_evidence_card` 动作,68,174-182)。
- 持久化:JSON 版本化对象存储 `lingdoc_g2_objects` + `lingdoc_g2_object_versions`(追加式版本表),迁移 `migrations/versioned/000116_lingdoc_full_workflow.up.sql:48-49`(sqlite 对应 `migrations/sqlite/000037_lingdoc_full_workflow.up.sql`);存取实现 `agent_object_storage.go:33-107`。EvidenceCard/ResearchClaim/SourceAnchor 均为版本化对象(非关系表)。
- 失效传播:`CurrentEvidenceObject` 对四种对象给出当前性判定,Anchor 失效→`NEEDS_REVIEW`,卡确认当前性重算(claim 版本/锚点版本/锚点当前性/来源集合一致):`agent_object_storage.go:146-208,369-410`;确认元组失效与依赖发布 `publishCardDependencies`(ConfirmEvidenceCard 内,`agent_action_service.go:1453`)。对应 Spec 37「失效传播」。

### 2.2 前端可见(工作台 UI 已存在)

- **ResearchPanel**(`frontend/src/views/lingdoc/ResearchPanel.vue`,486 行):四个页签「1 调研 / 2 摘录 / 3 证据 / 助手与动作」。证据页可建主张(37 行)、建卡(39-40 行:锚点多选+主张关联+支持类型+适用章节+范围/局限/缺口/替代解释)、按状态过滤(41 行)、确认/证据不足/要求复核(42 行,`confirmCard/reviewCard` 264-266 行)、「选择用于正文」(42 行,`emit('useEvidence')`)。
- **SelectedEvidenceContext**(`frontend/src/views/lingdoc/SelectedEvidenceContext.vue`):常驻展示「明确选定用于正文」的每张卡——主张文本、关系徽章(supports/refutes/qualifies/background 配色)、范围、局限/缺口、锚点与来源计数、回看/取消按钮(84-113 行);读取失败或授权受限即清空展示不降级(40-46 行)。
- **ProjectContextPackagePanel / ContextPackagePanel**(R-3,PR #661,已并入 HEAD,`git merge-base --is-ancestor 2daa0da4 HEAD` 核实):上下文包预览「按当前权限预览,不创建对话、不调用模型」(`ProjectContextPackagePanel.vue:85`),卡清单不可读时明示「不能据此判断没有证据卡」(63 行)。
- **生成面板**:`Workspace.vue:523`「本次使用 N 张已选择证据卡的原文;额外资料不会混入」;选段改写须另行勾选(461 行);请求携带 `evidence_card_refs`(2553-2554,2711-2712 行)。
- **ImpactOpsPanel**:变更草稿的 EvidenceCard 语义编辑器(`ImpactOpsPanel.vue:117,127`)。

### 2.3 端到端可走(用户实际能做到)

完整链已可走:调研任务批准→运行→结果分级→保存资料候选→人工确认导入→打开分块选文→人工核对存锚点→写主张→建候选卡→人工确认适用范围(逐章授权)→选卡用于正文→生成候选稿(强制 [[source:]] 标注+review_items)→人工门禁采纳入正文(工作副本版本对账)→编辑器角标回看+待核核查标记。**推断**(未起真实模型链路):涉及外部模型/检索 provider 的环节(生成、联网检索)在无 provider 配置下不可走,但其契约与失败路径有测试覆盖(如 `generation/evidence_test.go`、`workspace/generation_evidence_test.go`)。

---

## 3. R-5(证据工作台首版)剩余依赖

R-5 最小可交付(提案 `D:\wt\lingdoc-20261024\product-audit\PRODUCT_ROADMAP_PROPOSAL.md:52`)逐项对照 main:

| R-5 项 | 现状 | 证据 |
|---|---|---|
| EvidenceCard 列表 | **已成立**(超出提案基线认知) | `ResearchPanel.vue:41-42` 状态过滤列表 |
| 新建(从来源分块) | **已成立** | 摘录页选文建锚点(26-27)→证据页建卡(39-40);从检索结果需经「保存资料候选→确认导入」两跳(18,21-23 行) |
| 确认 | **已成立** | `confirmCard` 人工确认对话框含范围/局限/缺口全文(ResearchPanel:265);复核者按章 scope 授权(`canConfirmCard`,184 行) |
| 待核标记 | **已成立** | 卡级 `reviewCard`→INSUFFICIENT/NEEDS_REVIEW(266 行);生成级 review_items + 编辑器核查标记 |
| 与章节主张的关联展示 | **部分** | 卡→章方向存在(`scope_chapter_ids`:建卡选章、确认逐章授权、`SelectedEvidenceContext` 显示范围);**章节侧反向视图缺失**——没有「本段/本章依赖 N 张已确认卡」的界面;`SelectedEvidenceContext` 只反映本次生成选择,非章节持久依赖 |
| ResearchClaim 只读树 | **缺口(树)/超出(读写)** | 主张是平列表且可建可修(37-38 行);模型无 parent/层级字段(`agent_research_model.go:313-325`),不存在树 |
| 验收「从一条检索结果建卡、标待核、在章节侧看到依赖」 | 前两步可走,**第三步缺** | 同上两行 |

**重要修正(直接核实)**:R-5 提案基线 `21b5fb8d` 上 ResearchPanel.vue 已含 `saveCard/saveEvidenceCard`(`git show 21b5fb8d:.../ResearchPanel.vue` 命中 2 处)。提案「端点已齐备、界面缺」的判断**低估了当时现状**——工作台 UI 在提案撰写时已存在,此后 PR #661(上下文包面板挂载)与 SelectedEvidenceContext 等又补强了「依据回看」。

**剩余依赖清单(按大小排序)**:
1. **章节↔证据卡双向关联视图**(R-5 验收第三步,最大缺口;纯前端+一个既有数据源即可:`listEvidenceCards` 已返回 `scope_chapter_ids`,缺的是章节侧的汇总渲染)。
2. **ResearchClaim 层级/树**(需模型加 parent 字段或前端按章/主题分组;提案只要求只读树,当前连平铺只读树视图也没有——现在是可编辑平列表)。
3. (轻)证据对象按 project 隔离(`lingdoc_g2_objects` 主键含 project_id),跨项目证据复用不存在——提案未要求,仅登记。
4. (轻)#136/#137 Spec 票仍 OPEN(`gh issue view` 核实),实现已大幅超前于票面状态,票未回填。

---

## 4. 地基完成度结论

**结论:证据工作台的地基已完成,约 85-90%;剩余全部是展示层/组织层缺口,不涉及新的存储、路由或权限地基。**

- 数据模型与状态机:**完成**。三实体版本化对象 + 卡状态机(CANDIDATE→CONFIRMED→NEEDS_REVIEW/INSUFFICIENT/SUPERSEDED)+ 锚点当前性哈希重核 + 确认元组失效传播,与 Spec 35/36/37 的用户故事逐条对得上(35 的 1-14、36 的 1-15、37 的 1/2/5/7/9/11/12 均有对应实现;Spec 37 的 STALE 区分在对象层以 `stale` 状态词体现,如 `ResearchPanel.vue:138` 词表)。
- HTTP 面:**完成**。九个窄口全部注册并有结构断言(`context_ports.go:279-328`)。
- 工作台 UI:**基本完成**。R-5 四项核心操作(列表/新建/确认/待核)在 ResearchPanel 已端到端可走,且确认走人工对话框 + 服务端 HumanGate。
- 生成接入:**完成**。正式生成只消费 CONFIRMED 卡;候选/待核材料用于带标记草稿的路径由 review_items + 采纳门禁承载。
- 缺口:章节侧关联视图、主张树、(若追求)anchor 级自动定位——均为增量功能,不动地基。

**对卖点的最终判定**:承诺③完全成立;承诺②完全成立;承诺①在 chunk 粒度成立、在「原文精确位置」粒度靠人工锚点链,宣传口径应按「每个事实表述可回指原文分块,精确位置由人工核对的锚点链承载」表述。

---

## 附:核对方法与边界

- 静态走查,未运行服务、未走真实浏览器链路;「端到端可走」基于代码路径与测试文件存在性判定,运行时行为**未实测**(推断)。
- PR #661 并入 HEAD 由 `git merge-base --is-ancestor` 核实;#136/#137 状态由 `gh issue view` 核实。
- 未覆盖:交付导出侧(docx/validate.go 亦校验 source 标记,未深查)、CLI/client 模块、多租户权限矩阵全量。
