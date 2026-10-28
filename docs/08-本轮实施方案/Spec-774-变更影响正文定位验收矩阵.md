# Spec-774 变更影响定位精确到段落/句子 —— 档 A 验收矩阵

> 判定依据:《验收判定口径》(docs/08-本轮实施方案/验收判定口径.md,commit = `93b08c82`),总数 = 6。
> 需求票:magician336/lingdoc#774;本矩阵覆盖票面**档 A(登记值出现处定位,纯算法)**。检测逻辑复用源:#753(`suspectedConditionChanges`,internal/lingdoc/workspacecore/condition_change.go)。验证报告:docs/08-本轮实施方案/T774-验证报告.json。
> 票面三档"可递进,低档先行":档 B(SourceAnchor 选段引用)、档 C(AI 句级建议,含票面验收草案 4 的真实 provider 记账)留作后续递进,不在本表计数与判定范围;路线图(roadmap)任务链同样不在档 A 范围(票面限定"变更应用产生的影响任务")。

## 1. 范围与不变量

- 范围:变更 apply 链(`ApplyChangeSet` → `commitChangeImpactTasks`)为 `ChapterVersion` 目标的影响任务附带正文位置引用(`location_hints`:source/field_key/value/paragraph_index/start_offset/end_offset/excerpt);前端影响任务卡片渲染引用并提供"在编辑器中定位",复用批注定位管道(`locateMarkdownRange`)。
- 不变量(领域):定位是**附件不是判定**——`location_hints` 不参与任务状态机、严重度推导、稳定化阻断与闭环判定;影响仍由既有三层判定(显式字段/依赖图/章节状态机)驱动。
- 不变量(事务):hints 在 apply 既有写事务内随任务一并落库(版本化 JSON 记录,无新表、无迁移);不新增任何写出口。
- 不变量(授权):`location_hints` 仅随任务既有读取口(`ListImpactTasks`/`ActOnImpactTask` 返回)暴露,读取授权不变。
- 定位失败语义:正文已变化、值不再逐字存在或该章有未保存编辑时,前端明示"无法安全定位,没有猜测关联",与批注定位(`locateComment`)同一失败哲学。

## 2. 判定表

| 定位坐标 | 验收点原文 | 判定状态 | 交付分类 | 证据 | 备注 |
| --- | --- | --- | --- | --- | --- |
| TD-774.1 | 变更 apply 后,影响任务详情含至少一条正文位置引用(段/偏移+摘录),随任务落库并经读取口返回(票面验收草案 1 服务端半) | PASS | 已交付 | `TestCommitChangeImpactTasksAttachesLocationHints` 与 `TestCommitChangeImpactTasksWithoutHitsLeavesHintsEmpty`,命令 `go test ./internal/lingdoc/workspacecore/ -run 'TestCommitChangeImpactTasksAttachesLocationHints|TestCommitChangeImpactTasksWithoutHitsLeavesHintsEmpty' -count=1`,head `bcaa361c4232b54e97d3fa5ef294123420166d12` | WSL 内执行;真 sqlite 走 39 张生产迁移,经真实 assess→apply 链断言三条 hints(2 条旧值+1 条登记值)落库 |
| TD-774.2 | 登记值(spec)逐字存在于正文时定位命中:段落序号(空行分块,1-based)、UTF-16 code unit 偏移(非 BMP 计 2)、前后文摘录正确(票面验收草案 2) | PASS | 已交付 | `TestLocateImpactPositionsFindsRegisteredValuesWithParagraphAndUtf16Offsets`,命令 `go test ./internal/lingdoc/workspacecore/ -run 'TestLocateImpactPositionsFindsRegisteredValuesWithParagraphAndUtf16Offsets' -count=1`,head `bcaa361c4232b54e97d3fa5ef294123420166d12` | WSL 内执行;emoji 正文钉 UTF-16 口径,手算偏移逐字段断言 |
| TD-774.3 | 变更旧值(`change.fields[].old_value`)逐字存在时定位命中,来源 `change_old_value`;与登记值同值去重保留更具体来源;<4 rune 值不定位(票面验收草案 2"登记值/旧值") | PASS | 已交付 | `TestLocateImpactPositionsPrefersChangeOldValueSourceOnDuplicateValues`,命令 `go test ./internal/lingdoc/workspacecore/ -run 'TestLocateImpactPositionsPrefersChangeOldValueSourceOnDuplicateValues' -count=1`,head `bcaa361c4232b54e97d3fa5ef294123420166d12` | WSL 内执行;与 #753 同一噪音门槛(conditionValueMinRunes) |
| TD-774.4 | 噪音上限与确定性:每值最多 5 处、每任务最多 50 条;同一输入两次运行逐字节一致且按偏移升序 | PASS | 已交付 | `TestLocateImpactPositionsNoiseFloorCapsAndDeterminism`,命令 `go test ./internal/lingdoc/workspacecore/ -run 'TestLocateImpactPositionsNoiseFloorCapsAndDeterminism' -count=1`,head `bcaa361c4232b54e97d3fa5ef294123420166d12` | WSL 内执行;上限常量具名(impactLocationMaxMatchesPerValue/impactLocationMaxHintsPerTask) |
| TD-774.5 | 定位是附件不是判定(票面验收草案 3):有/无 hints 的任务 Status/Action/Severity/ResponsibilityDomain 逐字段一致;正文无命中时任务照常创建;非变更链(roadmap)任务行为不变 | PASS | 已交付 | `TestImpactLocationHintsDoNotChangeTaskVerdict` 与 `TestRoadmapCommitFreezesANewNodeVersionAndCreatesTheReviewTask`,命令 `go test ./internal/lingdoc/workspacecore/ -count=1` 及 `go test ./internal/router/ -count=1`,head `bcaa361c4232b54e97d3fa5ef294123420166d12` | WSL 内执行;全包回归含 roadmap 既有套件,router 包覆盖 HTTP 层序列化面 |
| TD-774.6 | 前端:任务卡片渲染位置引用(段落号+摘录+值)与"在编辑器中定位";定位以值原文就近重查(`findNearestOccurrence`)后复用 `locateMarkdownRange` 管道;值缺失即明示失败不猜测(票面验收草案 1 前端半) | PASS | 已交付 | 「#774 影响任务模板渲染正文位置引用与编辑器定位事件」与 「#774 档 A：findNearestOccurrence 取离参考偏移最近的出现处，缺失即拒绝」,命令 `npx tsx --test src/views/lingdoc/ImpactOpsPanel.test.js` 及 `npx tsx --test src/views/lingdoc/editor/pmSerialize.test.ts`,head `bcaa361c4232b54e97d3fa5ef294123420166d12` | frontend/ 下执行;type-check 与全量 `npm test`(1835 项,0 失败)同 head 通过 |

## 3. 范围外(不计入总数,留后续递进)

- 档 B(选段引用 SourceAnchor)与档 C(AI 句级候选,含票面验收草案 4 的真实 provider 记账):#774 票面声明"可递进,低档先行",由该票继续承接。
- AI 改写表述后的定位(字面匹配盲区):与 #753 同盲区,票面已声明由档 C 覆盖。
- 路线图任务链的位置定位:票面限定"变更应用产生的影响任务"。

## 4. 判定记录

判定 head:`bcaa361c4232b54e97d3fa5ef294123420166d12`;判定器:`python scripts/lingdoc_acceptance_judge.py --matrix docs/08-本轮实施方案/Spec-774-变更影响正文定位验收矩阵.md --report docs/08-本轮实施方案/T774-验证报告.json`。
