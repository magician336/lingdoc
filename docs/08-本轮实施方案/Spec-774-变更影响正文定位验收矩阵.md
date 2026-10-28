# Spec-774 变更影响定位精确到段落/句子 —— 档 A + 档 C 验收矩阵

> 判定依据:《验收判定口径》(docs/08-本轮实施方案/验收判定口径.md,commit = `93b08c82`),总数 = 11。
> 需求票:magician336/lingdoc#774;本矩阵覆盖票面**档 A(登记值出现处定位,纯算法)**与**档 C(AI 句级建议;用户 2026-10-28 已裁定为主路径:quote 逐字验证/建议非判定/用户拉触发/真实记账/召回诚实)**。检测复用源:#753;管线模式复用源:#751(AI 建议→人工确认→真实记账)。验证报告:docs/08-本轮实施方案/T774-验证报告.json。
> 票面三档"可递进,低档先行":档 B(SourceAnchor 选段引用)留作后续递进,不在本表计数与判定范围;路线图(roadmap)任务链不在范围(票面限定"变更应用产生的影响任务")。

## 1. 范围与不变量

- 范围:变更 apply 链(`ApplyChangeSet` → `commitChangeImpactTasks`)为 `ChapterVersion` 目标的影响任务附带正文位置引用(`location_hints`:source/field_key/value/paragraph_index/start_offset/end_offset/excerpt);前端影响任务卡片渲染引用并提供"在编辑器中定位",复用批注定位管道(`locateMarkdownRange`)。
- 不变量(领域):定位是**附件不是判定**——`location_hints` 不参与任务状态机、严重度推导、稳定化阻断与闭环判定;影响仍由既有三层判定(显式字段/依赖图/章节状态机)驱动。
- 不变量(事务):hints 在 apply 既有写事务内随任务一并落库(版本化 JSON 记录,无新表、无迁移);不新增任何写出口。
- 不变量(授权):`location_hints` 仅随任务既有读取口(`ListImpactTasks`/`ActOnImpactTask` 返回)暴露,读取授权不变。
- 定位失败语义:正文已变化、值不再逐字存在或该章有未保存编辑时,前端明示"无法安全定位,没有猜测关联",与批注定位(`locateComment`)同一失败哲学。

## 2. 判定表

| 定位坐标 | 验收点原文 | 判定状态 | 交付分类 | 证据 | 备注 |
| --- | --- | --- | --- | --- | --- |
| TD-774.1 | 变更 apply 后,影响任务详情含至少一条正文位置引用(段/偏移+摘录),随任务落库并经读取口返回(票面验收草案 1 服务端半) | PASS | 已交付 | `TestCommitChangeImpactTasksAttachesLocationHints` 与 `TestCommitChangeImpactTasksWithoutHitsLeavesHintsEmpty`,命令 `go test ./internal/lingdoc/workspacecore/ -run 'TestCommitChangeImpactTasksAttachesLocationHints|TestCommitChangeImpactTasksWithoutHitsLeavesHintsEmpty' -count=1`,head `c3b184be945eebcfc5aaf9094eb8e1ebdff1a844` | WSL 内执行;真 sqlite 走 39 张生产迁移,经真实 assess→apply 链断言三条 hints(2 条旧值+1 条登记值)落库 |
| TD-774.2 | 登记值(spec)逐字存在于正文时定位命中:段落序号(空行分块,1-based)、UTF-16 code unit 偏移(非 BMP 计 2)、前后文摘录正确(票面验收草案 2) | PASS | 已交付 | `TestLocateImpactPositionsFindsRegisteredValuesWithParagraphAndUtf16Offsets`,命令 `go test ./internal/lingdoc/workspacecore/ -run 'TestLocateImpactPositionsFindsRegisteredValuesWithParagraphAndUtf16Offsets' -count=1`,head `c3b184be945eebcfc5aaf9094eb8e1ebdff1a844` | WSL 内执行;emoji 正文钉 UTF-16 口径,手算偏移逐字段断言 |
| TD-774.3 | 变更旧值(`change.fields[].old_value`)逐字存在时定位命中,来源 `change_old_value`;与登记值同值去重保留更具体来源;<4 rune 值不定位(票面验收草案 2"登记值/旧值") | PASS | 已交付 | `TestLocateImpactPositionsPrefersChangeOldValueSourceOnDuplicateValues`,命令 `go test ./internal/lingdoc/workspacecore/ -run 'TestLocateImpactPositionsPrefersChangeOldValueSourceOnDuplicateValues' -count=1`,head `c3b184be945eebcfc5aaf9094eb8e1ebdff1a844` | WSL 内执行;与 #753 同一噪音门槛(conditionValueMinRunes) |
| TD-774.4 | 噪音上限与确定性:每值最多 5 处、每任务最多 50 条;同一输入两次运行逐字节一致且按偏移升序 | PASS | 已交付 | `TestLocateImpactPositionsNoiseFloorCapsAndDeterminism`,命令 `go test ./internal/lingdoc/workspacecore/ -run 'TestLocateImpactPositionsNoiseFloorCapsAndDeterminism' -count=1`,head `c3b184be945eebcfc5aaf9094eb8e1ebdff1a844` | WSL 内执行;上限常量具名(impactLocationMaxMatchesPerValue/impactLocationMaxHintsPerTask) |
| TD-774.5 | 定位是附件不是判定(票面验收草案 3):有/无 hints 的任务 Status/Action/Severity/ResponsibilityDomain 逐字段一致;正文无命中时任务照常创建;非变更链(roadmap)任务行为不变 | PASS | 已交付 | `TestImpactLocationHintsDoNotChangeTaskVerdict` 与 `TestRoadmapCommitFreezesANewNodeVersionAndCreatesTheReviewTask`,命令 `go test ./internal/lingdoc/workspacecore/ -count=1` 及 `go test ./internal/router/ -count=1`,head `c3b184be945eebcfc5aaf9094eb8e1ebdff1a844` | WSL 内执行;全包回归含 roadmap 既有套件,router 包覆盖 HTTP 层序列化面 |
| TD-774.6 | 前端:任务卡片渲染位置引用(段落号+摘录+值)与"在编辑器中定位";定位以值原文就近重查(`findNearestOccurrence`)后复用 `locateMarkdownRange` 管道;值缺失即明示失败不猜测(票面验收草案 1 前端半) | PASS | 已交付 | 「#774 影响任务模板渲染正文位置引用与编辑器定位事件」与 「#774 档 A：findNearestOccurrence 取离参考偏移最近的出现处，缺失即拒绝」,命令 `npx tsx --test src/views/lingdoc/ImpactOpsPanel.test.js` 及 `npx tsx --test src/views/lingdoc/editor/pmSerialize.test.ts`,head `c3b184be945eebcfc5aaf9094eb8e1ebdff1a844` | frontend/ 下执行;type-check 与全量 `npm test`(1837 项,0 失败)同 head 通过 |
| TD-774.7 | 档 C 触发与验证:用户拉触发(幂等键重放不二次调模型,CAS 版本);服务端逐字验证每条 quote,失配丢弃并计数上报(召回诚实);无模型 fail-closed(验收草案 2 的 AI 改写场景承接 + 草案口径④) | PASS | 已交付 | `TestSuggestImpactLocationsVerifiesQuotesAndDropsHallucinations` 与 `TestSuggestImpactLocationsFailsClosedWithoutModel`,命令 `go test ./internal/lingdoc/workspacecore/ -run 'TestSuggestImpactLocations' -count=1`,head `c3b184be945eebcfc5aaf9094eb8e1ebdff1a844` | WSL 内执行;注入已知变更点召回测试:2 条真实句命中、1 条幻觉 quote 被 quote_not_found 丢弃 |
| TD-774.8 | 档 C 采纳/驳回:采纳转 hint(source=ai_sentence)随任务落库;驳回记录状态;重复决定/未知建议/CAS 旧版本拒绝;采纳后任务 verdict 字段不动(定位是附件不是判定,验收草案 3) | PASS | 已交付 | `TestDecideImpactLocationSuggestionAdoptsIntoHintsAndRejects`,命令 `go test ./internal/lingdoc/workspacecore/ -run 'TestDecideImpactLocationSuggestion' -count=1`,head `c3b184be945eebcfc5aaf9094eb8e1ebdff1a844` | WSL 内执行;权限=manage 或责任人(同 assign/dismiss 判定) |
| TD-774.9 | 档 C 章节演进复验:SaveChapter/CommitWorkingCopy 后逐字复验 hints 与 suggestions,失配转 relocate(待重新定位)而非静默过期;正文恢复后标志清除;失配建议不可采纳(裁定验收追加) | PASS | 已交付 | `TestChapterAdvanceMarksMissingLocationsForRelocation`,命令 `go test ./internal/lingdoc/workspacecore/ -run 'TestChapterAdvanceMarks' -count=1`,head `c3b184be945eebcfc5aaf9094eb8e1ebdff1a844` | WSL 内执行;复验在章节推进同事务内 |
| TD-774.10 | 档 C 模型调用走真实 provider 并记账(验收草案 4):一行/调用(source_kind=impact_location_suggestion_run),provider 失败也留 failed 行;截断(FinishReason=length)与编造字段拒绝;输出契约 {quote,paragraph_hint,impact_reason,confidence,suggested_action} | PASS | 已交付 | `TestImpactLocationSuggestionModelRecordsOneSucceededRowPerCall` 与 `TestImpactLocationSuggestionModelSurfacesProviderFailureWithAFailedRow`,命令 `go test ./internal/lingdoc/workspace/ -run 'TestImpactLocationSuggestionModel' -count=1`,head `c3b184be945eebcfc5aaf9094eb8e1ebdff1a844` | WSL 内执行;stub 三件套不触网;5 例全过(解析/无默认模型/失败行/成功行/截断与编造) |
| TD-774.11 | 档 C 前端:任务卡片"AI 句级定位"按钮(用户拉,须填定位说明)、建议列表(quote/段号/理由/置信/状态)、采纳与驳回、触发与决策走幂等键;如实上报"几条通过、几条丢弃"并始终声明"AI 建议不保证完整";relocate 标记展示 | PASS | 已交付 | 「#774 档 C：AI 句级定位触发建议管线并如实上报丢弃」与 「#774 档 C：采纳或驳回建议走决策口且必须带处置说明」及 「#774 档 C：任务模板渲染建议列表、采纳驳回按钮与不保证完整声明」,命令 `npx tsx --test src/views/lingdoc/ImpactOpsPanel.test.js`,head `c3b184be945eebcfc5aaf9094eb8e1ebdff1a844` | frontend/ 下执行;面板 30/30 |

## 3. 范围外(不计入总数,留后续递进)

- 档 B(选段引用 SourceAnchor):#774 票面声明"可递进,低档先行",由该票继续承接。
- 路线图任务链的位置定位:票面限定"变更应用产生的影响任务"。
- AI 语义建议的召回完整性:档 C 按裁定口径明示"不保证完整",以 drops 上报与召回诚实测试代替完备性声明。

## 4. 判定记录

判定 head:`c3b184be945eebcfc5aaf9094eb8e1ebdff1a844`;判定器:`python scripts/lingdoc_acceptance_judge.py --matrix docs/08-本轮实施方案/Spec-774-变更影响正文定位验收矩阵.md --report docs/08-本轮实施方案/T774-验证报告.json`。
