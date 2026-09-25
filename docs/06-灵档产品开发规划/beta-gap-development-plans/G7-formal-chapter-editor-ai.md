# G7. 正式章节编辑器与 AI 辅助写作

> 补充计划：本文件聚焦 active 项目的正式章节编辑器和 AI 辅助写作，不改变 Beta 六项缺口 G1–G6 的编号。  
> 相关计划：[G1. 建项语义与草稿阶段](G1-draft-baseline.md)、[G2. 证据工作台与调研智能体](G2-evidence-research.md)、[G3. PCC、ChangeSet、依赖图与技术路线图](G3-pcc-change-coordination.md)、[G4. 模板、规则与问题治理](G4-template-rules.md)、[G5. 完整权限、治理与审计](G5-permissions-governance.md)、[G6. Beta 质量、运行与效果评估](G6-beta-quality-operations.md)
> 相关基线：[领域架构与 PCC](../02-领域架构与PCC.md)、[数据模型与接口契约](../03-数据模型与接口契约.md)、[用户界面与交互规划](../09-用户界面与交互规划.md)

## 1. 目标和基本判断

G7 把“章节骨架已经生成”升级为“用户可以在正式项目中持续写作、引用证据、接受 AI 辅助、比较版本、协作复核并形成可确认的 ChapterVersion”。

正式章节编辑器服务于 active 项目。它不是草稿阶段的自由文本区，也不是一个把 AI 输出直接变成申报书的按钮。编辑器必须让用户始终知道：

1. 当前正在编辑哪一个章节、哪个版本和哪个 Context Revision；
2. 正文中哪些内容是人工写的、AI 生成的、AI 建议后人工修改的；
3. 每条关键表述关联了哪些 EvidenceCard、SourceAnchor、ResearchClaim 或 ProjectSpec；
4. AI 建议基于哪些资料、规则和版本，是否仍然有权使用；
5. 当前内容是否已提交复核、已经确认，或因变更/来源失效而重新待复核。

核心原则：

- AI 只能对当前授权上下文提出候选、解释和检查结果；
- AI 结果先进入候选或工作副本，不能直接覆盖正式章节或自动确认；
- 正式章节以不可变 ChapterVersion 为单位保存、比较、恢复和确认；
- 普通措辞编辑与研究语义变化分开处理；语义变化进入 G3 ChangeSet；
- G4 负责模板规则和问题，G5 负责能力、scope 和再鉴权，G7 负责写作交互与版本形成。

## 2. 范围和边界

### 2.1 本轮交付

- active 项目的章节树、章节详情和正式写作编辑器；
- Chapter、ChapterVersion、EditorWorkingCopy、CandidateDraft、Comment、ContentReview 和 Confirmation 的交互语义；
- 人工编辑、自动保存、版本创建、比较、恢复和并发冲突；
- 基于章节、选中文本或任务卡的 AI 生成、续写、改写、压缩、扩展、提纲和审校；
- AI 上下文范围选择、来源显示、证据/主张关联、差异预览和局部采纳；
- 术语、模板规则、待核项、证据不足和版本影响的辅助提示；
- 提交复核、退回、确认、重新确认和陈旧处理；
- G3 语义变更入口、G4 Template Check、G5 权限检查和 G6 观测字段；
- 章节编辑器的最小可操作界面和验收 fixture。

### 2.2 明确不负责

G7 不负责：

- 在 draft 项目中创建正式章节；草稿期仍由 G1 管理候选池；
- 自动决定选题、方法、变量、创新性、学术价值或结论正确性；
- 自动确认 EvidenceCard、ResearchClaim、ChapterVersion 或 ReleaseSnapshot；
- 自动新增正式主张、证据卡、SourceAnchor 或依赖边；
- 自动把章节内容同步写回 ProjectSpec；
- 用模型相似度替代 G3 的显式依赖关系；
- 替代 G4 的 Preflight、G5 的权限判定或 G6 的真实运行验证。

## 3. 章节对象和版本语义

### 3.1 Chapter

Chapter 是模板章节在 active 项目中的稳定对象，至少包含：

- chapter_id、project_id、template_section_id；
- 标题、说明、顺序、是否必需和负责人；
- 当前 ChapterVersion、当前 Context Revision；
- 关联 ResearchClaim、EvidenceCard、RouteNode、Comment/Task；
- 当前状态、创建时间和审计信息。

Chapter 是容器，不直接承载“当前可变正文”。正文必须通过 ChapterVersion 读取。

### 3.2 ChapterVersion

ChapterVersion 是一次可恢复、可比较、可确认的章节正文版本，至少保存：

- version_id、chapter_id、parent_version_id；
- body 或结构化 block 内容；
- based_on_context_revision、template_version、ruleset_hash；
- 来源标记：human、ai_generated、ai_assisted_human、imported；
- 关联的 EvidenceCard、SourceAnchor、ResearchClaim 和 ProvenanceRecord；
- 创建者、创建时间、生成运行标识；
- 状态、差异摘要、是否进入复核或交付快照。

确认绑定 ChapterVersion，而不是 Chapter 当前指针。旧版本继续可读、可比较和按当前权限回放。

### 3.3 EditorWorkingCopy

EditorWorkingCopy 是用户当前编辑的工作副本，可以包含尚未形成正式版本的局部修改和自动保存检查点。它必须绑定：

- 基于的 ChapterVersion；
- 编辑者、编辑会话和客户端；
- 当前项目 revision、模板/规则版本；
- 最后保存时间、冲突状态和恢复入口。

自动保存不等于发布新 ChapterVersion；用户点击保存检查点、采纳候选、提交复核或明确创建版本时，才产生可引用的 ChapterVersion。

### 3.4 CandidateDraft

CandidateDraft 是 AI 或用户任务产生的候选内容。它必须与目标章节、目标范围、上下文快照和生成运行关联。候选可以被预览、局部采纳、修改、拒绝或归档，但不会自动改变当前 ChapterVersion。

候选正文、引用建议和待核项必须分开显示。候选有依据不等于依据已确认；候选没有依据时不能伪装成已引用内容。

## 4. 章节状态和状态转移

推荐的 ChapterVersion 状态：

| 状态 | 含义 | 允许操作 |
|---|---|---|
| DRAFT | 人工或 AI 辅助后的可编辑版本 | 编辑、比较、运行检查 |
| IN_REVIEW | 已提交给责任人/Reviewer | 查看、评论、退回、确认 |
| CONFIRMED | 对固定版本和核查范围完成人工确认 | 只读；新版本从它派生 |
| STALE | 项目 revision、证据、模板或权限上下文变化 | 查看原因、复制为新版本、重新复核 |
| ARCHIVED | 被后续版本替代的历史版本 | 只读、回放 |

基本转移：

- DRAFT → IN_REVIEW：提交者完成保存并提交复核；
- IN_REVIEW → CONFIRMED：有 content.confirm 且 scope 覆盖的 Owner/Reviewer 确认；
- IN_REVIEW → DRAFT：被退回并带理由；
- 任意未确认版本 → STALE：上游对象或版本变化使当前确认范围失效；
- CONFIRMED → ARCHIVED：新版本确认后保留历史；
- STALE → DRAFT：基于当前 revision 创建新工作版本，不复活旧确认。

CONFIRMED 不是“永远正确”。正文、所依证据、ResearchClaim、ProjectSpec、模板规则或适用范围发生实质变化后，G3 或 G4 可使它进入待复核/STALE。

## 5. 编辑器交互模型

### 5.1 三栏布局

正式章节编辑器采用“章节导航 + 正文编辑区 + 依据/任务面板”：

- 左栏：章节树、负责人、版本、确认状态、待处理数量；
- 中栏：富文本或 Markdown 编辑区、块级差异、批注和选区工具；
- 右栏：EvidenceCard、SourceAnchor、ResearchClaim、模板问题、影响任务、评论和 AI 建议。

顶部必须显示：项目、章节、Context Revision、ChapterVersion、模板版本、保存状态和当前权限。AI 运行期间仍显示输入范围、运行状态、取消和恢复入口。

### 5.2 普通编辑

用户可以在被授权章节 scope 内：

- 编辑正文、标题层级、段落、列表、表格、图形说明和可编辑路线图文本；
- 拖动或调整允许范围内的块；
- 对选中文字建立主张/证据关联；
- 添加评论、任务和待核说明；
- 预览规则问题、版本差异和影响提示；
- 保存工作副本、创建 ChapterVersion、提交复核或恢复历史版本。

编辑器不能因为用户点击“保存”就自动确认、自动创建 ChangeSet 或自动更新 ProjectSpec。

### 5.3 选区操作

对选中文字提供明确操作：

- 关联已有 ResearchClaim；
- 关联一个或多个 EvidenceCard/SourceAnchor；
- 请求 AI 改写、压缩、扩展、翻译或解释；
- 标记待核、证据不足、术语问题或影响问题；
- 创建任务或评论。

选区操作必须保留原文和新候选的差异，不能直接覆盖用户文本。对一段话的“润色”不等于确认这段话的学术含义。

### 5.4 自动保存与恢复

自动保存只保存 EditorWorkingCopy 检查点，至少有本地恢复、服务端恢复和异常退出恢复入口。服务中断时不能显示“已保存”而实际丢失；恢复时显示基于的 ChapterVersion 和最后保存时间。

自动保存失败、权限被回收、版本冲突或目标章节被升级时，工作副本标记为待处理，不得静默覆盖当前版本。

## 6. AI 辅助能力

### 6.1 AI 功能分层

| 功能 | 输出 | 是否可直接进入正式版本 |
|---|---|---|
| 结构建议 | 小节提纲、段落顺序、缺少的结构项 | 否，需采纳并人工修改 |
| 生成候选 | 根据选定上下文生成段落或小节 | 否，进入 CandidateDraft |
| 局部改写 | 对选区压缩、扩展、改写、调整语气 | 否，显示差异后局部采纳 |
| 依据整理 | 建议关联 EvidenceCard、主张和待核项 | 否，用户确认关联 |
| 规则解释 | 解释字数、术语、必填和一致性问题 | 否，正式问题由 G4 规则产生 |
| 影响提醒 | 提示可能受 ProjectSpec、证据或模板变化影响 | 否，正式影响由 G3 依赖图产生 |
| 语言审校 | 错别字、语病、重复、格式建议 | 可形成工作副本修改，仍不自动确认 |

### 6.2 AI 上下文包

每次 AI 运行前必须显示并记录：

- 当前项目、章节和目标选区；
- Context Revision、ChapterVersion、模板/规则版本；
- 用户选定的 ProjectSpec 字段；
- 用户选定的 ProjectAsset、EvidenceCard、SourceAnchor 和 ResearchClaim；
- 允许使用的资料权限和是否允许模型访问原文；
- 输出目标、字数、格式、保留观点和禁止改变的内容；
- 模型、提示词/任务卡版本和生成时间。

默认只使用当前章节和用户主动选择的范围。AI 不能因为“项目可读”就自动读取所有资料、其他章节正文或隐藏权限对象。

### 6.3 生成流程

统一流程为：

1. 用户选择章节、选区、目标和资料范围；
2. 系统显示上下文包和版本，用户确认范围；
3. AI 生成 CandidateDraft 和 review_items；
4. 用户查看正文差异、依据、限制和未确认项；
5. 用户局部采纳、修改、拒绝或重新生成；
6. 采纳结果写入 EditorWorkingCopy；
7. 用户保存为新 ChapterVersion 或提交复核；
8. 具备权限的人员单独执行 content.confirm。

AI 运行成功不等于章节更新成功；网络失败、权限变化、版本过期或输出不符合结构时，候选保留为失败/陈旧记录，不覆盖工作副本。

### 6.4 AI 边界

AI 不得：

- 直接覆盖当前 ChapterVersion；
- 自动确认正文、主张、证据或交付；
- 生成没有 SourceAnchor 的“引用”；
- 把模型推断写成已确认事实；
- 跨越项目、章节、资料或租户权限；
- 因用户要求而绕过模板规则、G3 ChangeSet 或 G5 Owner/Reviewer 条件；
- 自动删除用户文本或自动解决 blocking 问题。

## 7. 依据、主张与证据关联

### 7.1 关联层次

正文中的一段话可以分别关联：

1. ResearchClaim：这段话表达的项目主张；
2. EvidenceCard：支持或限制主张的证据卡；
3. SourceAnchor：可以回到原文的来源定位；
4. ProvenanceRecord：这次生成、采纳或人工修改的来源记录。

四者不能合并为一个“引用”按钮。EvidenceCard 未确认时，正文可以显示待核标记，但不能在确认章节时假装其已是正式依据。

### 7.2 生成上下文准入

进入正式生成上下文的 EvidenceCard 必须满足当前项目权限、SourceAnchor 有效、适用范围匹配、版本未陈旧。候选、证据不足、无权、STALE 或超出章节适用范围的对象，只能作为明确标记的待核上下文，不能无标记进入正式生成。

### 7.3 引用和超范围表达

编辑器应能显示“证据支持到哪里”和“这句话是否超出证据范围”的提示。AI 可以建议降低断言强度、拆分主张或增加待核项，但不能替用户决定结论。用户采纳改写后，原文、候选和采纳者仍可追溯。

## 8. 与 G3 变更协调的边界

### 8.1 普通章节编辑

只修改未确认章节的措辞、排序、排版、评论或局部表达，不改变 ProjectSpec、ResearchClaim、变量、方法、证据适用范围和路线图关系时，可直接创建新的 ChapterVersion，不自动创建 ChangeSet。

### 8.2 可能的语义变化

以下内容默认进入“需要判断是否创建 ChangeSet”的提示：

- 改变研究对象、目标、方法、样本或变量；
- 改变主张范围、结论方向或证据适用范围；
- 改变必需章节、RouteNode、交付项或模板绑定；
- 让章节内容与当前 ProjectSpec 不一致。

系统可以给出“可能是项目语义变化”的候选，但不能只凭模型判断就自动创建或应用 ChangeSet。用户选择普通编辑时，应留下选择理由；用户选择进入 G3 时，编辑器把选区、前后版本和关联对象作为 ChangeSet 草稿输入。

### 8.3 影响返回

G3 返回 ImpactTask、STALE 或确认失效后，编辑器在对应章节显示影响原因、上游对象版本和下一步动作。用户不能用保存正文的方式关闭 ImpactTask；必须按 G3 任务和确认流程处理。

## 9. 与 G4 模板规则的边界

编辑器实时显示 Template Check，但不把写作期通过当成可导出。规则问题必须定位到章节、字段、选区或关联对象，并显示规则版本和严重度。

- 格式、字数、标题层级等可计算问题可提供修复预览；
- 术语、证据关联、覆盖提示和模型建议只能提供待核问题；
- 内容修复生成新 ChapterVersion；
- resolve、waive、dismiss 由 G4 规则和 G5 权限共同判定；
- Preflight 对 ReleaseSnapshot 重新求值，不能复用编辑器中旧的通过状态。

## 10. 协作、并发和版本冲突

### 10.1 编辑锁和并发

多人可以同时阅读；同一章节可以有多个工作副本。保存时必须携带基于的 ChapterVersion 和项目 revision：

- 基线未变：创建新 ChapterVersion；
- 基线变化但可自动合并：显示合并结果，用户确认后保存；
- 存在同一段落的不可安全合并修改：进入冲突界面；
- 目标章节已被确认、升级或撤权：拒绝写入当前版本，保留本地/服务端工作副本。

不能用“后保存覆盖前保存”解决并发。

### 10.2 比较和恢复

版本比较至少支持正文差异、章节结构差异、依据变化、待核项变化、确认变化和模板/规则版本。恢复旧版本不是静默改指针，而是以旧版本为输入创建新的 ChapterVersion；若项目语义可能倒退，提示进入 G3 逆向 ChangeSet。

### 10.3 评论和任务

评论、任务和 AI review_items 不等于正文确认。评论应绑定选区或块、ChapterVersion、作者和状态；版本替换后显示为历史或待迁移。关闭任务必须有处置说明和目标版本，不能因为新版本保存就自动关闭。

## 11. 复核与确认

提交复核时，系统生成复核包：

- 目标 ChapterVersion 和父版本；
- 当前 Context Revision、模板/规则版本；
- 正文差异和来源标记；
- ResearchClaim、EvidenceCard、SourceAnchor 汇总；
- Template Check 问题、G3 ImpactTask 和未确认项；
- 用户选择的核查范围。

Reviewer/Owner 可以确认、退回、要求修改、标记证据不足、转派或标记问题待核。Confirmation 必须绑定 target_ref、target_version、scope、actor、时间、理由和基于的版本。

内容确认不能替代证据确认；证据确认不能替代内容确认。允许自写自审，但确认范围和审计必须完整。

## 12. 权限和安全

编辑器每次加载、保存、AI 运行、采纳候选、提交复核、确认、导出和下载都接入 G5：

- content.read：读取章节和允许显示的关联对象；
- content.edit：编辑指定章节 scope；
- content.confirm：确认 ChapterVersion/ResearchClaimVersion；
- evidence.confirm：确认 EvidenceCard，不由编辑器代替；
- changeset.create：从语义变化入口创建 ChangeSet 草稿；
- delivery.preflight/export：由 G4/G6/G5 共同控制。

资料正文和 SourceAnchor 详情另行检查 ProjectAsset、知识库和 SourcePolicy。权限撤回后，AI 运行结果、缓存、预览 URL 和工作副本不能继续暴露受限正文。

## 13. 运行、观测和 AI 审计

每次 AI 运行至少记录：

- project_id、chapter_id、目标版本和 Context Revision；
- 输入范围、资料授权快照、EvidenceCard/SourceAnchor 版本；
- 模型/提示词/任务卡版本、运行模式、耗时和结果状态；
- CandidateDraft、采纳差异、拒绝理由、人工修改和最终 ChapterVersion；
- correlation_id、causation_id、actor 和权限判定结果。

日志不记录不必要的正文和密钥。AI 输出、用户采纳和最终确认必须能够区分统计。需要复盘时以脱敏 fixture 和受控授权读取原文。

## 14. 交付切片

| 切片 | 交付物 | 完成判据 |
|---|---|---|
| G7.1 章节工作区 | 章节树、编辑器、工作副本、自动保存 | active 项目可安全编辑、恢复和处理冲突 |
| G7.2 ChapterVersion | 版本、差异、恢复、确认绑定 | 旧版本可回放，确认绑定固定版本 |
| G7.3 依据关联 | 主张、证据卡、来源锚点、待核标记 | 关联可定位，未确认依据不伪装成正式依据 |
| G7.4 AI 候选 | 上下文包、生成、改写、审校、局部采纳 | AI 只产生候选，采纳前可比较和撤销 |
| G7.5 G3/G4/G5 接入 | 语义变化、模板检查、权限和审计 | 普通编辑不绕过 G3，所有操作再鉴权 |
| G7.6 复核确认 | 提交、退回、转派、Confirmation | 内容确认和证据确认分离且可审计 |
| G7.7 观测与评估 | AI 运行、采纳、失败和人工投入指标 | 可追踪到最终版本和责任人 |

## 15. 与其他能力线交接

### 交给 G1

- 只消费 active 项目和立项生成的章节骨架；
- 不在草稿期产生正式 Chapter 或 ChapterVersion。

### 交给 G2

- 章节选区关联 ResearchClaim、EvidenceCard、SourceAnchor；
- 证据状态、授权、来源变化触发的待核和重新生成入口；
- 调研助手从章节上下文发起任务，但结果先进入 CandidateDraft。

### 交给 G3

- 可能改变项目语义的编辑转为 ChangeSet 草稿；
- ImpactTask、STALE、确认失效和稳定化状态回传编辑器；
- 旧结果不能覆盖新 revision。

### 交给 G4

- 当前章节的 Template Check；
- 规则问题、严重度、处置和 Preflight 再计算；
- 模板升级导致的章节迁移和版本回放。

### 交给 G5

- content.read/edit/confirm、资料读取、AI 运行、确认和导出权限；
- 章节 scope、撤权、Owner unavailable、审计和历史读取。

### 交给 G6

- 编辑器、AI、并发、权限、DOCX、回退和真实试用 fixture；
- AI 候选采纳率、人工修改量、确认耗时、来源支撑率、误报和导出损失指标。

## 16. 验收场景

### AC-G7-01：正式章节编辑

active 项目中，Author 在被授权章节编辑正文，自动保存工作副本，明确保存后形成新 ChapterVersion；draft 项目没有正式章节编辑入口。

### AC-G7-02：AI 选区改写

用户选中一段文字请求压缩。系统显示上下文、前后差异和待核提示；用户局部采纳后写入工作副本，原版本保留，AI 不自动确认。

### AC-G7-03：证据关联

用户将段落关联到一个 EvidenceCard 和多个 SourceAnchor。编辑器显示支持范围和限制；未确认 EvidenceCard 显示待核，不伪装成正式依据。

### AC-G7-04：受限资料

用户有章节编辑权但没有某 ProjectAsset 读取权。编辑器不显示该资料正文，AI 不能将其作为上下文，历史缓存和预览 URL 也不能泄露。

### AC-G7-05：语义变化提示

用户把研究对象从“本科生”改成“接受创业教育的本科生”。系统提示可能需要 G3 ChangeSet，并列出受影响对象；用户确认前不自动创建或应用正式变更。

### AC-G7-06：并发冲突

两名 Author 基于同一 ChapterVersion 修改同一段落。后保存者看到差异和冲突，不能覆盖前者；合并后形成新的版本。

### AC-G7-07：陈旧 AI 结果

AI 基于 Context Revision 2 运行期间项目进入 Revision 3。返回候选标记为 STALE/历史，不能覆盖 Revision 3 的工作副本或确认。

### AC-G7-08：内容确认

Reviewer 确认固定 ChapterVersion 的核查范围和理由。确认记录绑定版本；后续正文或证据变化使确认失效，旧确认保留历史。

### AC-G7-09：规则检查

编辑器发现术语不一致和字数问题。字数问题提供修复预览；术语问题提供建议；两者都不能替用户做学术选择，Preflight 仍在快照上重新求值。

### AC-G7-10：Owner 停用

Owner 失效后，Author 可在既有 scope 内保存普通工作版本，但不能执行 Owner 专属的模板升级、ChangeSet 应用或导出；恢复/转让后重新鉴权。

### AC-G7-11：生成失败和恢复

模型超时或输出结构无效。系统保留失败运行摘要，不产生正式版本；用户可以重试、改小范围或继续人工编辑，已有正文不被清空。

### AC-G7-12：版本恢复

用户选择旧 ChapterVersion 恢复。系统创建新的子版本并显示差异，不静默改当前指针；若涉及研究条件或主张变化，提示创建逆向 ChangeSet。

## 17. 质量指标和不纳入

建议统计：AI 候选采纳率、局部采纳比例、人工修改字符/段落量、AI 运行失败率、陈旧候选率、来源关联覆盖率、待核项关闭时长、ChapterVersion 冲突率、确认返工率、章节规则误报率和导出损失数。指标用于改进工作流，不作为学术质量评分。

本阶段不做全自动写完整申报书、无范围的“帮我写全部”、自动引用生成、自动证据确认、自动 ChangeSet 应用、自动清空正文、隐式跨章节读取和绕过权限的超级 Agent。

G7 完成标准是：用户能在 active 项目中稳定写章节，AI 能在明确范围内提供可追溯候选，人工可以逐段比较和采纳，正式版本可恢复和确认，语义变化进入 G3，证据和权限边界不被绕过，最终章节可进入 G4 Preflight 和 G6 验收。

---

