# Spec-774 变更影响定位精确到段落/句子 —— 档 A 验收矩阵

> 判定依据:验收判定口径 v1.1(docs/08-本轮实施方案/验收判定口径.md)。本矩阵覆盖 issue #774 的**档 A(登记值出现处定位,纯算法)**;档 B(SourceAnchor 选段引用)与档 C(AI 句级建议,含验收草案 4 的真实 provider 记账)按票面"可递进,低档先行"留作后续票,不在本矩阵判定范围。
>
> 需求票:magician336/lingdoc#774;检测逻辑复用源:#753(`suspectedConditionChanges`,internal/lingdoc/workspacecore/condition_change.go)。

## 1. 范围与不变量

- 范围:变更 apply 链(`ApplyChangeSet` → `commitChangeImpactTasks`)为 `ChapterVersion` 目标的影响任务附带正文位置引用(`location_hints`);前端影响任务卡片渲染引用并提供"在编辑器中定位",复用批注定位管道(`locateMarkdownRange`)。
- 不变量(领域):定位是**附件不是判定**——`location_hints` 不参与任务状态机、严重度推导、稳定化阻断与闭环判定;影响仍由既有三层判定(显式字段/依赖图/章节状态机)驱动。
- 不变量(事务):hints 在 apply 既有写事务内随任务一并落库(JSON 记录,无新表、无迁移);不新增任何写出口。
- 不变量(授权):`location_hints` 仅随任务读取口(`ListImpactTasks`/`ActOnImpactTask` 返回)暴露,读取授权不变。
- 定位失败语义:正文已变化、值不再逐字存在时,前端明示"无法安全定位,没有猜测关联",与批注定位(`locateComment`)同一失败哲学。

## 2. 验收点

| 编号 | 验收点(票面验收草案映射) | 判定方式 | 证据 |
| --- | --- | --- | --- |
| TD-774.1 | 变更 apply 后,`ChapterVersion` 影响任务详情含至少一条正文位置引用:字段齐全(source/field_key/value/paragraph_index/start_offset/end_offset/excerpt),随任务落库并经读取口返回(验收草案 1 服务端半) | 集成测试(真 sqlite,service 层 apply 链) | `TestCommitChangeImpactTasksAttachesLocationHints`(internal/lingdoc/workspacecore/impact_location_test.go) |
| TD-774.2 | 登记值(spec)逐字存在于正文时定位命中:段落序号(空行分块,1-based)、UTF-16 code unit 偏移(含非 BMP 字符口径)、前后文摘录正确(验收草案 2) | 单元测试(纯函数) | `TestLocateImpactPositionsFindsRegisteredValuesWithParagraphAndUtf16Offsets` |
| TD-774.3 | 变更旧值(`change.fields[].old_value`)逐字存在时定位命中,来源标记 `change_old_value`;与登记值同值去重保留更具体来源(验收草案 2"登记值/旧值") | 单元测试 | `TestLocateImpactPositionsPrefersChangeOldValueSourceOnDuplicateValues` |
| TD-774.4 | 噪音门槛与上限:短于 4 rune 的值不定位(与 #753 同门槛);每个值最多输出前 5 处;每条任务最多 50 条;同一输入两次运行结果逐字节一致且按偏移升序(确定性) | 单元测试 | `TestLocateImpactPositionsNoiseFloorCapsAndDeterminism` |
| TD-774.5 | 定位是附件不是判定(验收草案 3):有 hints 与无 hints 的任务在 Status/Action/Severity/ResponsibilityDomain 上逐字段一致;正文无任何命中时任务照常创建(hints 为空);非变更链(roadmap)任务不携带 hints 且行为不变 | 集成 + 单元测试 | `TestImpactLocationHintsDoNotChangeTaskVerdict`;`TestCommitChangeImpactTasksWithoutHitsLeavesHintsEmpty` |
| TD-774.6 | 前端:影响任务卡片渲染位置引用(段落号+摘录+值),提供"在编辑器中定位";点击 emit locate 事件,由 Workspace 复用 `locateMarkdownRange` 管道跳转;正文已变化或存在未保存编辑时明示失败、不猜测位置(验收草案 1 前端半) | 组件/单元测试(vitest) | `ImpactOpsPanel.test.js` 的 `test('影响任务渲染正文位置引用并发出定位事件')`;`pmSerialize.test.ts` 的 `test('findNearestOccurrence 就近匹配并在缺失时拒绝')` |

## 3. 范围外(本矩阵明示 NOT IN SCOPE)

- 档 B(选段引用 SourceAnchor)、档 C(AI 句级候选与真实 provider 记账,票面验收草案 4):后续票。
- 路线图(roadmap)任务链的位置定位:票面限定"变更应用产生的影响任务"。
- AI 改写表述后的定位(字面匹配盲区):与 #753 同盲区,票面已声明由档 C 覆盖。

## 4. 判定记录

判定 head:`__HEAD__`;验证报告:`docs/08-本轮实施方案/T774-验证报告.json`;判定器:`python scripts/lingdoc_acceptance_judge.py --matrix docs/08-本轮实施方案/Spec-774-变更影响正文定位验收矩阵.md --report docs/08-本轮实施方案/T774-验证报告.json`。
