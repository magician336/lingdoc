# G7. AI 写作动作族：开发计划

> 本文件是 Spec [33-Spec G7 章节 AI 辅助写作](../33-Spec-G7章节AI辅助写作.md)（Issue [#134](https://github.com/magician336/lingdoc/issues/134)）的**施工切片计划**，交付 Issue [#326](https://github.com/magician336/lingdoc/issues/326)（施工并行面 PF-2）。
> 它不重复 [G7 总计划](G7-formal-chapter-editor-ai.md)——总计划定义 G7 的章节对象、版本语义与交互模型；本文件只定义**动作族**这一条线的模块边界、接口契约与验收矩阵。
> 配套决策：[ADR 0008](../../adr/0008-writing-action-vocabulary-and-adoption-model.md)、[ADR 0009](../../adr/0009-candidate-adoption-idempotency-digest-version.md)。

## 0. 交付身份

| 项 | 内容 |
| --- | --- |
| Issue / Spec | #326 / #134（33-Spec G7 章节 AI 辅助写作） |
| 目的地（用户可见结果） | 用户在章节里**按动作**发起 AI 写作（提纲 / 生成 / 续写 / 改写 / 压缩 / 扩展 / 转写 / 语言评审），在执行前看清这个动作要什么、产出什么、能不能采纳；采纳时必须显式确认「采纳哪个动作的结果、写到哪一段」。 |
| 范围内 | 动作词汇表与产出参数校验；两条既有 AI 管道（`generation` 整章异步、`selected_rewrite` 选段同步）共用这份词汇；候选形态归一；采纳模型（目标 × 许可）；显式采纳确认；候选来源标注；候选 `not_authorized` 状态；失败与取消保留 run 输入；review 报告预览与显式应用；outline 覆盖保护；执行前可见的动作与产出参数 UI 组件。 |
| 范围外 | 证据内容与失效传播（#138 已落地）；章节编辑器保存/冲突/版本（#133 已落地）；上下文包前端消费（PF-6 #330）；G6 告警接线（PF-3 #327）；`owner_unavailable`（PF-4 #328）；Agent research purpose（PF-5 #329）；**不修改** `frontend/src/views/lingdoc/Workspace.vue`（保留路径，最终前端集成单独做一次）。 |
| 前置依赖 | 无实现依赖。#138 的生成接入已落地（正式生成只接受 CONFIRMED + 当前 Claim + 当前锚点 + 当前 ProjectAsset），不必等 #137 / #133 的新行为。 |
| 可并行工作 | PF-1 #325、PF-3 #327、PF-4 #328、PF-5 #329、PF-6 #330 与本面**路径集零重叠**（逐文件核对见 §3.1）。 |
| 真实消费者 | `internal/lingdoc/generation` 的 `Application.Start`（HTTP `POST /projects/:projectId/generations`）、`internal/lingdoc/workspace` 的 rewrite 三个端点（`workspace/http.go:414-418`）、`internal/lingdoc/candidateadoption` 的 `AcceptCandidate`（`POST .../chapters/:chapterId/acceptances`）、前端 `frontend/src/api/lingdoc/{generation,candidateAdoption,workspace}.ts`。 |

**边界声明**：本计划**不新增第二条 AI 写作管道**。仓库里已经存在两条（`generation` 与 `selected_rewrite`），本计划的动作族是让它们**共用一份词汇与一份候选形态**，而不是再加第三条。

**按实现更新（D30）**：语言评审是**第三条**路径（`selected_rewrite` 的评审分支），因为它的产出形状不同——评审以一份**报告**收尾，报告不是候选，没有可采纳物，应用它的是另一个入口（`ApplyReview`）。它仍然**不新增第四条管道**，也仍然与前两条共用同一份词汇与同一份候选形态。

## 1. 决策记录

本计划的全部结构决策经四轮 grilling 裁决，每条都记录了被否掉的候选（ch11 设计两次）。依据列指 Ousterhout《软件设计的哲学》章节。

| # | 决策 | 结论 | 依据 | 必须同时落地的配套条件 |
| --- | --- | --- | --- | --- |
| D1 | PF-2 交付边界 | **扩所有权**，把 6 项缺口一并做掉，不另开验收票 | ch09（知识与变化优先于代码行数） | 所有权**逐文件列举**，不得用 `workspacecore/*` 通配 |
| D2 | 动作集合 | 8 类：outline / generate / continue / rewrite / compress / expand / transform / review | ch14（精确命名） | translate 与 explain 合并进 `transform`，review 独立 |
| D3 | 登记处缺失 | **另开基础设施票**补建 `scripts/lingdoc_parallel_surfaces.py` + 守护测试；PF-2 只在票面登记自己的路径集 | ch17（自动化约束优于人工记忆） | PF-2 不得替其它五面写入他们的路径集 |
| D4 | 两条管道的关系 | **共享动作词汇，两条管道各自保留实现**；`candidateadoption` 成为候选形态 / 失效判据 / 来源标注的唯一拥有者 | ch05（消除信息泄漏）、ch09（集中知识而非合并模块） | 候选形态必须同步归一，否则泄漏只是换位置 |
| D5 | 显式确认的载体 | **组合式**：门禁机制由 `workspacecore.RequireHumanDecision` 执行（`candidateadoption` 直接调用），确认载荷由 `candidateadoption` 拥有（动作 + 目标范围） | ch05（不同知识不同拥有者）、ch07（高层的用户语义不塞进低层） | ① `AcceptCandidate` 开头调 `core.RequireHumanDecision(ctx)`——`workspacecore` 已导出（`human_decision.go:22-24`），`candidateadoption` 已 import `workspacecore`，**无新依赖**；先例 `workspace/selected_rewrite.go:284,528`。② **不要**往 `human_decision.go` 名单加 `acceptCandidate`（该名单没有消费者，见 §3.5）。③ 载荷进摘要白名单 |
| D6 | 可采纳性 | **「采纳目标 × 该动作允许哪些目标」两维**；正文型 6 类允许全部三种；outline 只允许写工作副本；review 先形成可预览的评审报告，确认后再应用 | ch10（定义掉特殊路径，不加 if 分支） | 「可否采纳」是数据，不是采纳端的分支；review 应复用同一确认与幂等边界 |
| D7 | 动作词汇包归属 | 新建 `internal/lingdoc/writingaction/` 独占拥有 | ch05（提取真正抽象细节的新模块）、ch04（深度） | 动作的产出参数必须是闭合枚举，不得是自由文本 |
| D8 | 来源标注落点 | **落在候选上**，不碰 `workspacecore/model.go` | ch10（对齐评审决策）、ch09（避免与 PF-3 撞 `model.go`） | 复用既有三值词汇 `{human, ai_generated, ai_assisted_human}`；采纳时把来源写进确认载荷与审计 |
| D9 | 幂等摘要升级 | **摘要版本化**：白名单加版本前缀，旧键**显式失效**（新错误形态），不回落成 `idempotency_conflict`。**执行顺序固定：先把摘要实现换成 `operationcore.RequestHash`（算法等价、历史值不变），再扩白名单** | ch10（错误重要性决定是否暴露）、ch05（摘要规则只有一个拥有者） | ① 新增 clienterror 形态；② 回放比对**两处**都要版本感知（`candidate_adoption_store.go:397` 与 `:430`）；③ 摘要结构体是**专用摘要结构**（不是 `AcceptCandidateInput` 本身——后者含 `IdempotencyKey`/`ActorID`，进了摘要会让全部历史行失配） |
| D10 | 产出参数形状 | **全闭合枚举**；`Instruction` 降级为不参与动作判定的补充说明 | ch06（用已知用例设计接口）、ch17（一致性） | 动作由 `writing_action` 字段显式声明（D14），未知动作拒绝而非回退自由文本 |
| D11 | `transform` 子参数 | `transform` 带 `mode ∈ {translate, explain}`；translate 带 `target_language`（闭合语言枚举），explain 带 `audience ∈ {peer, reviewer, novice}` | ch05（分支集中在词汇包一个拆弹点） | 两个子模式的所需上下文项与产出参数分别声明 |
| D12 | 交付形状 | **单分支多提交**：先 `rewrite` 打通纵切片，再按同一模板补其余 7 个 | ch08（小步迁移比一次性重写更容易验证） | 每提交一个可验证目的；矩阵按动作分块；矩阵未闭环前 PR 保持 Draft |
| D13 | diff 生成位置 | **前端计算**，复用 `frontend/src/utils/wikiLineDiff.ts`（零 import 的纯函数，90 行） | ch09（允许小而显然的重复；不必为显示引入服务端字段与传输成本） | 服务端不新增 diff 字段；diff 只用于展示，不参与采纳判定 |
| D14 | 写作动作的标识符 | 类型在 `writingaction` 包内叫 `Action`（`writingaction.Action`）；跨包落到字段上叫 **`WritingAction`**（`candidate.WritingAction`，JSON 键 `writing_action`）。`Origin` **保持** `Origin`（与 `SpecField.Origin` 同语义同取值） | ch14（精确命名）、ch17（同概念同表达） | `Action` 在治理层已被三义占用：依赖边动作（`workspacecore/impact_model.go:26`）、审计动作（`candidateadoption/candidate_adoption_store.go:142`）、治理操作（`operationcore` 的 `Identity.Operation`）。后两个就在 `candidateadoption` 包内 |
| D15 | 新「产出参数」与既有 `OutputConstraints` | 新概念叫 **产出参数 / `OutputParams`**；既有 `AgentContextPackage.OutputConstraints` 保持「输出约束」不变。**两者不得合并** | ch14（精确命名）、ch07（层次边界） | 写死不变量 I-13：客户端提交的产出参数**绝不进入** `AgentContextPackage.OutputConstraints`（那是服务端安全边界，由 `BuildAgentContext` 组装） |
| D16 | `review` 的落地通道 | **评审报告 + 显式应用**：review 先生成可预览、可拒绝的 `ReviewReport`，用户确认后由 `ApplyReview` 以 CAS、幂等和人工门禁写入工作副本；不把评审结果直接写入工作副本 | ch10（消除「评审特殊写路径」） | 评审报告复用候选的来源、基线和撤权判定；确认载荷必须包含报告 ID、目标范围和报告版本 |
| D17 | 闭合枚举的取值集 | 长度 `short/medium/long`；语气 `neutral/formal/academic/plain`；目标语言 `zh/en/ja/de/fr`；读者层次 `peer/reviewer/novice`；压缩与扩展比例用 **10–90 的整数百分比**（同一 `Ratio` 参数表达两个方向） | ch06（用已知用例设计接口） | 比例用整数百分比而非三档枚举，因为「压到三分之一」比「short」精确且能直接换算预算 |
| D18 | 动作词汇的**接口形状** | **`ActionSpec` 不直接对外**。对外只暴露 `PlanFor(action, scope, params) (Plan, error)`，返回一个**已解析的执行计划**（含已解析的上下文需求、采纳许可、产出形状、动作指令片段）。`ActionSpec` 降为包内实现 | ch04（深模块 = 接口小、隐藏机制；公开字段表是「getter 假隐藏」）、ch10（错误在模块内定义一次） | 消费方**只读 `Plan`**，不自己读表推导；`Spec` 不再返回 `(T, bool)`，未知动作由 `PlanFor` 返回 `ErrUnknownAction` |
| D19 | 动作专属**指令片段**归谁 | 收进 `writingaction`（`Plan.Instruction`）。管道只保留「整章 vs 选段」的**脚手架**（上下文窗口、模板、证据卡组装），不各自解释动作语义 | ch05（**后门泄漏**：知识未写入接口却被多个实现隐式依赖）、ch05（按知识归属而非模块归属分组） | 两条 host adapter 的**语义**差异消失，只剩脚手架差异；I-15 的守护从「不越清单」升级为「不自建指令」 |
| D20 | 交付形状 | **PR-0 契约与迁移 + 8 个动作串行 PR**；矩阵按动作分块、各自闭环；#326 在最后一个 PR 合入后关闭 | ch08（小步迁移比一次性重写更容易验证）、`development-rules` §5（只有最后一个交付切片完成全部矩阵项才关票） | PR-0 与每个动作 PR 独立过 CI、独立被评审；票仍是一张，覆盖整组改进项 |
| D21 | 复杂性预算 | 显式列出本次新增的**接口 / 状态 / 依赖**各多少，并写明**超预算时的削减顺序** | ch08（每个新特性都要说明新增的接口、状态和依赖；没有收益抵消就重新设计） | 削减顺序：先缩动作数 → 再降产出参数粒度 → 最后才动守护测试 |
| D22 | 并发语义 | 验收矩阵的**持久化与并发列不得留空**；补 3 行专门的并发验收（双人同时采纳同一候选 / 工作副本并发写 / 采纳事务中途撤权） | ch08（并发是需要专门设计的语义，不是默认项）、`development-rules` §2（涉及持久化、并发时要补幂等重试、竞态和无部分写入证据） | 本次交付核心是**写路径**；并发是写路径的主要风险面 |
| D23 | 错误形态与描述派生 | 未知动作的错误在 `writingaction` 内定义一次（`ErrUnknownAction`），消费方不再各写各的 400；`Describe(scope)` 由 `PlanFor` **派生**，不维护第二份数据源 | ch10（错误重要性决定是否暴露、在低层定义一次）、ch09（两个入口同一份知识 = 盲目去重） | 错误形态不再散到三个消费方 |
| D24 | **A34 的 origin 判据** | 判据收窄为「**除 `internal/lingdoc/writingaction` 外，任何包都不得再定义 origin 取值表**」 | ch05（词汇是唯一拥有者）、D8（候选来源沿用既有三值） | `workspacecore.SpecField.Origin` 与词汇包同三值，其**既有重复**记为已知债（三值一致，收拢属后续项，不在本面范围）。若按「全仓唯一」字面执行，会把词汇包自己判成违规 |
| D25 | 产出参数**必填** | 动作声明的产出参数一律必填；动作用不上的参数同样被拒 | ch10（不留默认值表）、§3.2「未使用的参数也必须拒绝」 | 闭合枚举里没有「未指定」成员（`""` 不是合法值），参数若可省就必然要有默认值表，而计划里没有 |
| D26 | `review` 的采纳目标 | **空集** | D16（评审报告 + 独立应用入口） | §3.2 的表写「确认后应用到工作副本」，§3.3 的采纳矩阵写三项全 ❌；按 §3.3 取空集——`review` 不产生候选，没有可采纳物，应用由 `ApplyReview` 承担 |
| D27 | `RenderInstruction` / `RenderContext` | 计划 §3.2 声明了 `RenderInstruction`，但 **`RenderContext` 从未定义** → 该渲染器**不在 #350 落地**，**归 #353 一并设计** | ch11（不发明计划里没有的抽象）、ch15（接口契约要先定清） | 不自造一个计划里没有的上下文类型 |
| D28 | 词汇唯一性守护的**判据** | 按**取值**判（取值落在 8 个动作值里 / 常量组覆盖度 / 枚举常量 / 字符串字面量表），**不按类型名**判 | ch05（守护要测真实的知识归属，不是同形的名字） | 基线里 `rulesetstore.Action`、`templategovernance.Action`、`workspacecore.AuthorizationAction` 都是**同名不同义**的治理动作词汇；按类型名判会全误报。后续票的词汇守护必须沿用取值判据 |
| D29 | 许可位的**派生入口** | 采纳端要问「这个动作允许哪些采纳目标」时，走 `writingaction.TargetsFor(action)`——它由同一份包内规格表（`PlanFor` + 该动作的规范产出参数）**派生**，不新写一份许可表 | ch05（知识只有一个拥有者）、ch09（集中知识而非合并模块） | 许可位仍住在 `actionSpecs` 一张表里：`TargetsFor` 若另写一份许可表，A29 守护会红。未知动作返回 `ErrUnknownAction`；采纳端**不得**按动作名分支（I-10）。**取代**了「采纳端自带一张动作→目标表」的做法——那是第二份许可位知识 |
| D30 | **管道声明机制** | `Pipeline` / `ActionsFor` / `ScopesFor` / `DefaultActionFor` / `ErrUnknownPipeline`（`writingaction/pipeline.go`）是「**哪些动作归这条管道**」的**唯一声明处**。执行方引用管道常量，不写动作字面量 | ch05（**后门泄漏**：知识未写入接口却被多个实现隐式依赖）、ch10（定义掉特殊路径，不加 if 分支） | 判定的是「这条管道做不做这个动作」而不是「这条管道做哪个动作」——问错了问题就没有答案（A33）。**取代**了先前的 `ActionFor` / `ErrAmbiguousPipeline`：它们为「一个动作可能属于多条管道」而设；改成「每条管道按语义声明自己实现哪些动作」之后，这条歧义不再存在，两个入口随之退场。§0 的「不新增第二条 AI 写作管道」按实现更新为**三条**：生成、选段、评审——评审是第三条路径，因为它的产出是报告而不是候选（§3.3） |
| D31 | **生命周期阶段**与**失效判据**各归其位 | 候选上的 `status` 承载**生命周期阶段**（queued / ready / applying / failed / stale）；**失效判据只有 `Validity` 一个拥有者**（fresh / stale / not_authorized） | ch10（定义掉特殊路径）、ch14（精确命名） | §3.4 原写的「`Status` 拆成两件事：`Validity` + `ErrorCode`」**不够用**：`queued` / `applying` 是生命周期阶段，`Validity` 装不下它们；`ErrorCode` 也不是阶段（它是失败原因）。Go 字段因此改名 `LifecycleStage`，**JSON 键仍是 `status`**——保留路径 `frontend/src/views/lingdoc/Workspace.vue` 还在读它，线上形状逐字不变。`rewriteLifecycleStage` 只做**渲染**，不构成第二套状态机 |

**D13 的推导依据**：`wikiLineDiff.ts` 全文无 import 语句，唯一依赖是 LCS 算法本身；把它放到 `utils/` 是 knowledge-base 域的历史命名，不是技术耦合。LingDoc 直接 import 不引入跨域技术耦合。

## 2. 代码事实基线

勘察基于 `main` = `633ba34`，全部结论可复核到 `路径:行号`。

### 2.1 两条 AI 管道现状

| 维度 | `generation`（整章，异步） | `workspace/selected_rewrite.go`（选段，同步） |
| --- | --- | --- |
| 位置 | `internal/lingdoc/generation/`（领域包） | `internal/lingdoc/workspace/`（工作台包） |
| 动作 | **无枚举**；只有自由文本 `Request.Instruction`（`generation/service.go:137`） | **无枚举**；自由文本 `SelectedRewriteRequest.Instruction`（`selected_rewrite.go:51`） |
| 输入 | `chapter_id` + `asset_ids` + `evidence_card_refs` + 三个期望版本 | `selection{start_utf16,end_utf16,selected_text}` + `source_ids` + 三个期望版本 |
| 模型口 | `ModelAdapter.Generate(ctx, Input) (Draft, error)`（`service.go:181-183`） | `SelectedRewriteModel.RewriteSelected(ctx, Actor, Prompt) (Output, error)`（`selected_rewrite.go:101-104`） |
| 产出 | `Draft{BodyMarkdown, Sources, ReviewItems}`（`service.go:157-161`） | `SelectedRewriteCandidate{Selection, ReplacementMarkdown, SourceIDs, ReviewItems, RunMode, Status, ...}`（`selected_rewrite.go:64-81`） |
| 待核事项形状 | 候选层 `[]candidateadoption.ReviewItem`（结构体） | 候选层 `[]workspacecore.ReviewItem`（`selected_rewrite.go:78`）——**字段与 `candidateadoption.ReviewItem` 完全相同但不同型**；`[]string`（纯文本数组）只在**模型输出层**（`selected_rewrite_model.go:30`） |
| 失效判据 | `Validity` 裸字符串 `"fresh"`/`"stale"`（`service.go:545,653,667`） | `Status` 裸字符串 `"ready"`/`"failed"`/`"stale"` + 独立的 `ErrorCode`（`selected_rewrite.go:69,79,225,433-467`） |
| run mode | **无** | 有：`real` / `mock` / `real_api_fake_model`（`selected_rewrite.go:767`） |
| HTTP | 5 条，`generation/http.go:38-42` | 3 条，注册在 `workspace/http.go:414-418` |
| 生产消费入口 | 2 写/执行（HTTP `Start`、asynq `Execute`）+ 4 读/取消 | 1 个 HTTP 入口（3 端点），无任务处理器 |

**结论**：动作语义、候选形态、失效判据、run mode 四条知识各有两个副本或一个缺失。新增一个动作当前要改四处。

### 2.2 候选采纳现状

| 事实 | 位置 |
| --- | --- |
| `AcceptCandidateInput` = `{ProjectID, ChapterID, CandidateID, ActorID, IdempotencyKey, ExpectedChapterVersionID, ExpectedSpecRevision, ReplaceExisting}` —— **无确认标记、无目标范围、无部分采纳参数** | `candidateadoption/candidate_adoption.go:117-126` |
| 整段采纳**直接建正式 ChapterVersion**（`lingdoc_chapter_versions`），并整篇覆盖正文 | `candidate_adoption_store.go:533-543` |
| 工作副本只是被**推进**到同一内容（CAS on `working_copy_revision` + `spec_revision` + `base_chapter_version_id`） | `candidate_adoption_store.go:745-766` |
| 人工门禁名单 17 条，**不含** `acceptCandidate`、也不含 `commitWorkingCopy` | `workspacecore/human_decision.go:33-53` |
| 该名单**只对走 workspacecore operation pipeline 的操作生效**——`workspaceHumanGate()` 的唯一消费点是 `operation_pipeline.go:142`；采纳路径不走那条 pipeline（`candidateadoption` 全包对 `operationcore` 零引用） | `workspacecore/operation_pipeline.go:92-97,142` |
| 幂等摘要 `requestHash` 是**显式白名单**：`{ProjectID, ChapterID, CandidateID, ExpectedChapterVersionID, ExpectedSpecRevision, ReplaceExisting}`；`IdempotencyKey`/`ActorID` 不在摘要内 | `candidate_adoption.go:405-416` |
| 摘要比对**两处**：回放 `:397` 与写事务内 `:430` | `candidate_adoption_store.go:397,430` |
| `validity` 列有 DB CHECK 约束 `IN ('fresh','stale')` | `migrations/versioned/000099:19`、`migrations/sqlite/000020:20` |
| `ApplyRewrite` **不接受范围参数**，接收调用方拼好的整篇 `BodyMarkdown` 整体覆盖 | `workspacecore/working_copy.go:204,243` |
| `ApplyRewrite` 的 review items 由**服务端合并并重锚点**，重复 ID → `ErrInvalidRequest` | `workspacecore/working_copy.go:256-273` |

### 2.3 可直接复用的接缝

| 接缝 | 位置 | 用途 |
| --- | --- | --- |
| `ApplyRewrite` | `workspacecore/working_copy.go:204` | AI 文本 → 工作副本；四元 CAS + operationcore 幂等管道 + 事务内审计 |
| `SaveWorkingCopy` | `workspacecore/working_copy.go:134` | 用户保存；`working_copy_revision` CAS |
| `CommitWorkingCopy` | `workspacecore/working_copy.go:294` | 工作副本 → 正式版本（失效确认） |
| `ReviewDecision` + `ConfirmChapter` | `candidateadoption/confirmation.go:16-20,77` | 逐条待核事项处置 + **必填理由** |
| `RequireHumanDecision` | `operationcore/decision.go:51-62` | 身份级人工门禁（机器身份/代他人一律拒） |
| `operationcore.Request.Body` | `operationcore/operationcore.go:210-295` | 唯一进摘要、可回放校验的载荷通道；先例 `ApplyRewriteInput` |
| `AgentContextPackage` / `BuildAgentContext` | `workspacecore/g2_model.go:351-370`、`agent_action_service.go:1685` | 上下文包与卡片版本 |
| `EffectivePermission` | `workspacecore/g5_policy.go:189` | 能力判定结果（含 `ReasonCode`/`ContextRevision`） |
| `diffWikiLines` | `frontend/src/utils/wikiLineDiff.ts:20` | 零 import 纯函数行级 diff |
| `selectionToUtf16` / `relocateAnchor` / `selectionFits` | `frontend/src/views/lingdoc/editor/anchor.ts:38,30`、`ChapterReviewPanel.vue:31` | 选区模型与合法性校验 |
| `LingDocCandidateAdoptionDialog` | `frontend/src/components/LingDocCandidateAdoptionDialog.vue` | 采纳对话框骨架（展示层可扩，请求体与幂等键逻辑必须重写） |
| 三值来源词汇 | `workspacecore/g1_provenance_service.go:374-383` | `human` / `ai_generated` / `ai_assisted_human` |

### 2.4 确认不存在的东西

| 缺口 | 证据 |
| --- | --- |
| 任何动作枚举 | `generation` 包全文无 `Action`/`Kind`/`Mode` 常量或分派 |
| 候选 `not_authorized` 状态 | 全仓 `NOT_AUTHORIZED` 只作为文档与 mock 脚本里的字面量；代码用 403 `source_access_denied` |
| 采纳的确认标记 | `AcceptCandidateInput` 无任何确认字段 |
| 采纳的目标范围 / 部分采纳参数 | `AcceptCandidateInput` 无选区或片段字段 |
| 章节级 human/AI/AI辅助 标注 | `provenance_service.go` 只在研究条件**字段级**；正文无分段级标注 |
| 引用建议独立结构 | 只有 `[[source:id]]` 标记 + `SourceIDs`；`CitationUsage` 存在但 `Draft` 未产出 |
| `ReviewItem` 的 kind 分类 | `ReviewItem{ID, Statement, OriginCandidateID, Anchor}`，`Statement` 是自由文本 |
| 候选上的 run mode / model id / action 列 | `lingdoc_candidates` 列为 `id, project_id, chapter_id, run_id, body_markdown, source_ids_json, basis_json, validity, review_items_json, created_at` |
| 生成路径写 G6 事件 | `internal/lingdoc/generation/` 与 `candidateadoption/` 内无 `RecordRuntimeEvent` 调用 |
| diff 渲染 | LingDoc 域内没有；`package.json` 无 diff 依赖 |

## 3. 目标设计

### 3.1 模块边界与文件所有权

**新增包**（PF-2 新建，独占拥有）：

```
internal/lingdoc/writingaction/
  action.go          — Action / Scope / AdoptionTarget / Origin 枚举
  spec.go            — 包内规格表与 8 条动作的规格（不对外导出）
  output_params.go   — 闭合枚举产出参数的类型与校验
  describe.go        — 面向 UI 的可序列化描述
```

**扩大的文件所有权（逐文件列举，不通配）**：

| 文件 | 归属变化 | 改动理由 |
| --- | --- | --- |
| `internal/lingdoc/generation/service.go` | 原属 | `Request` 加 `WritingAction` + 产出参数；`Draft` 加 writing_action/origin；产出参数先于执行 |
| `internal/lingdoc/generation/application.go` | 原属 | `Application` 增加动作词汇的读取入口 |
| `internal/lingdoc/generation/http.go` | 原属 | 动作参数入参；动作表暴露端点 |
| `internal/lingdoc/generation/store.go` | **新增** | run 持久化动作与 run mode；失败/取消保留并可读回输入 |
| `internal/lingdoc/workspace/selected_rewrite.go` | 原属 | 选段动作化；候选形态归一到 `candidateadoption` |
| `internal/lingdoc/workspace/selected_rewrite_model.go` | 原属 | 按 `Plan.Instruction` 拼接提示词；待核事项结构化 |
| `internal/lingdoc/candidateadoption/candidate_adoption.go` | **新增** | 确认载荷、采纳目标、部分采纳参数；摘要版本化；`not_authorized` |
| `internal/lingdoc/candidateadoption/candidate_adoption_store.go` | **新增** | 候选表加列；幂等行加摘要版本；两处摘要比对版本感知 |
| `internal/lingdoc/candidateadoption/candidate_basis.go` | **新增** | 失效判据归一（`fresh`/`stale`/`not_authorized`） |
| `internal/lingdoc/candidateadoption/candidate_adoption_http.go` | **新增** | 采纳请求体扩展 |
| `internal/lingdoc/candidateadoption/confirmation.go` | **新增** | review 动作的待核事项处置复用 |
| `internal/lingdoc/workspace/http.go` | **新增** | rewrite 端点动作化；部分采纳端点 |
| `internal/router/lingdoc_routes_test.go` | **新增** | 路由面变化（当前断言注册的路由**恰好 70 条**） |
| `internal/clienterror/catalog.go` | **新增** | 新错误形态 `idempotency_key_expired` |
| `frontend/src/api/lingdoc/generation.ts` | **新增** | 动作参数与动作表 |
| `frontend/src/api/lingdoc/candidateAdoption.ts` | **新增** | 确认载荷 / 采纳目标 / 部分采纳 |
| `frontend/src/api/lingdoc/workspace.ts` | **新增** | rewrite 动作化（仅改 rewrite 相关函数） |
| `frontend/src/components/LingDocCandidateAdoptionDialog.vue` | **新增** | diff、确认、来源标注、采纳目标 |
| `frontend/src/views/lingdoc/generationAttempt.ts` | 原属 | 幂等键派生 |
| `frontend/src/views/lingdoc/candidateAdoption.ts` | **新增** | 签名/键复用随请求体扩展 |
| `frontend/src/api/lingdoc/writingAction.ts` | **新增** | 动作表客户端 |
| `frontend/src/views/lingdoc/GenerationActionPanel.vue` | **新增** | 执行前显示动作与产出参数 |

**新增但必须同批改的清单文件**（不属于本面所有权，但改动会让它们变红）：

- `internal/database/migration_sqlite_versioned_schema_test.go:91`（`expectedSQLiteMigrationVersion = 45` → 46）
- `internal/router/lingdoc_integration_test.go:95`（迁移文件清单）
- `internal/lingdoc/operationcore/ownership_test.go:80-108`（摘要唯一拥有者守护的 AST 检查要覆盖**两步式**摘要，见 §3.6 第 5 步）

> `internal/lingdoc/workspacecore/sqlite_migrations_test.go:50`（表名清单）**本切片不需改**——迁移重建的是既有表，不新增表，表名集合不变。

**明确不碰**：`internal/lingdoc/workspacecore/model.go`（PF-3 #327 独占）、`internal/lingdoc/workspacecore/working_copy.go`（只需调用，不需修改）、`frontend/src/views/lingdoc/Workspace.vue`（保留路径）、`internal/lingdoc/operationcore/*` 的**实现**（只需使用 `Body` 与 `RequireHumanDecision`，不修改；唯一例外是上面那份守护测试的检查规则）。

### 3.2 领域级接口契约

> 这是**领域级**契约，不是 HTTP 端点签名。端点形状由实现阶段按既有路由风格推导。

```go
package writingaction

type Action string
const (
    ActionOutline   Action = "outline"
    ActionGenerate  Action = "generate"
    ActionContinue  Action = "continue"
    ActionRewrite   Action = "rewrite"
    ActionCompress  Action = "compress"
    ActionExpand    Action = "expand"
    ActionTransform Action = "transform"
    ActionReview    Action = "review"
)

type Scope string
const (
    ScopeChapter Scope = "chapter"
    ScopeSection Scope = "section"
)

type AdoptionTarget string
const (
    TargetWorkingCopyPartial AdoptionTarget = "working_copy_partial"
    TargetWorkingCopyWhole   AdoptionTarget = "working_copy_whole"
    TargetFormalVersionWhole AdoptionTarget = "formal_version_whole"
)

type Origin string
const (
    OriginHuman     Origin = "human"
    OriginGenerated Origin = "ai_generated"
    OriginAssisted  Origin = "ai_assisted_human"
)

// Produce 决定该动作的产出形状，进而决定它是否产生候选。
type Produce string
const (
    ProduceProse    Produce = "prose"    // 候选正文
    ProduceOutline  Produce = "outline"  // 候选正文（章节骨架）
    ProduceFindings Produce = "findings" // 产出 ReviewReport，确认后应用
)
```

**产出参数（全闭合，无自由文本）**：

```go
type OutputParams struct {
    Length         Length         // short | medium | long
    Tone           Tone           // neutral | formal | academic | plain
    Ratio          int            // 压缩/扩展目标百分比，10..90
    Transform      *TransformParams
}

type TransformParams struct {
    Mode           TransformMode // translate | explain
    TargetLanguage *TargetLanguage
    Audience       *Audience
}

type TransformMode string
const (
    TransformTranslate TransformMode = "translate"
    TransformExplain   TransformMode = "explain"
)

type Length string
const (LengthShort Length = "short"; LengthMedium Length = "medium"; LengthLong Length = "long")

type Tone string
const (ToneNeutral Tone = "neutral"; ToneFormal Tone = "formal"; ToneAcademic Tone = "academic"; TonePlain Tone = "plain")

type TargetLanguage string
const (LangZH TargetLanguage = "zh"; LangEN TargetLanguage = "en"; LangJA TargetLanguage = "ja"; LangDE TargetLanguage = "de"; LangFR TargetLanguage = "fr")

type Audience string
const (AudiencePeer Audience = "peer"; AudienceReviewer Audience = "reviewer"; AudienceNovice Audience = "novice")

// ContextItem 声明该动作可以使用哪一类上下文项。
type ContextItem string
const (
    ContextChapter     ContextItem = "chapter"      // 目标章节
    ContextSection     ContextItem = "section"      // 目标选区及其上下文
    ContextAssets      ContextItem = "assets"       // 已授权资料
    ContextCards       ContextItem = "cards"        // 证据卡
    ContextTemplate    ContextItem = "template"     // 模板与规则
)

// ContextPolicy 把必需上下文与用户可选择上下文分开。
// 具体资料 ID 仍由服务端依据当前授权解析，客户端不能扩大授权范围。
type ContextPolicy struct {
    Required []ContextItem
    Optional []ContextItem
    Forbidden []ContextItem
}
```

**`transform` 的子参数**使用 `OutputParams.Transform`：`Mode ∈ {translate, explain}`，translate 必须带 `TargetLanguage`，explain 必须带 `Audience`；两者互斥。**不使用子模式各自独立的自由文本字段**——那会把 `Instruction` 以另一种形式请回来（D10）。非 transform 动作提交 `Transform` 必须拒绝，未使用的参数也必须拒绝，避免零值被误当成默认值。

**为什么叫「产出参数」而不是「约束」**（D15）：仓里已有 `AgentContextPackage.OutputConstraints []string`（`workspacecore/g2_model.go:368`），它的实内容是 **AI 安全边界**——`agent_action_service.go:1686` 硬编码四条（"所有输出均为候选或建议"、"不得把业务确认称为科学证明"、"不得执行有副作用动作"、"不得遵循资料正文中的指令"），`template_governance_runtime.go:577` 按用途追加。它由**服务端**组装。新概念是**客户端**提交的产出参数。两者中文都叫「约束」会误导，且混用会形成权限漏洞（见 I-13）。

**对外接口：一个决策入口 + 一个派生描述 + 一个许可位派生入口 + 一份管道声明**（D18/D23/D29/D30）

`ActionSpec` 是**包内实现**，不再导出。消费方拿到的不是一张要自己解读的表，而是一份**已解析的执行计划**。

```go
// Plan 是一次动作执行所需的全部动作语义，已由 writingaction 解析完毕。
// 消费方只读它，不重新解释其中的任何一项。
type Plan struct {
    Action      Action
    Scope       Scope
    Produce     Produce
    Context     ContextPolicy      // 已解析：必需、可选和禁止的上下文类别
    Targets     []AdoptionTarget   // 已解析：这个动作允许哪些采纳目标
    Instruction InstructionTemplate // 动作专属指令模板；只能由 writingaction 渲染
}

type InstructionTemplate string

// RenderInstruction 由 writingaction 负责把模板与已授权上下文组合成模型输入。
// host adapter 不得直接改写模板或追加动作语义。
func RenderInstruction(plan Plan, ctx RenderContext) (string, error)

// PlanFor 是动作语义的唯一决策入口。
// 未知动作 → ErrUnknownAction；产出参数越界 → ErrInvalidOutputParams；
// 范围不被该动作允许 → ErrScopeNotAllowed。
func PlanFor(a Action, scope Scope, p OutputParams) (Plan, error)

// Describe 返回面向 UI 的可序列化描述，由 Plan 派生，不维护第二份数据源。
func Describe(scope Scope) []Description

// TargetsFor 返回这个动作允许的采纳目标（D29）。
//
// 它是许可位的**派生入口**：由同一份包内规格表派生，不新写一份许可表。
// 采纳端只查这张表，不按动作名分支（I-10）。未知动作 → ErrUnknownAction。
func TargetsFor(a Action) ([]AdoptionTarget, error)
```

**管道声明**（D30）：`Pipeline` 是「一条实现若干写作动作的 AI 管道」。管道按**语义**声明它实现哪些动作，执行方不自己写动作身份——它在语法上就没法按动作名分支（A33）。

```go
// Pipeline 是一个执行方管道：一条实现若干写作动作的 AI 管道。
type Pipeline string
const (
    PipelineGeneration Pipeline = "generation"   // 整章生成：生成 / 转写 / 提纲
    PipelineRewrite    Pipeline = "rewrite"      // 选段：改写 / 续写 / 压缩 / 扩展 / 转写
    PipelineReview     Pipeline = "review_report" // 评审：语言评审
)

// ErrUnknownPipeline 表示请求指向的管道没有实现任何动作。
var ErrUnknownPipeline = clienterror.New(clienterror.InvalidRequest, "unknown_pipeline")

// ActionsFor 返回这个管道实现的动作集合。未知管道 → ErrUnknownPipeline。
func ActionsFor(p Pipeline) ([]Action, error)

// ScopesFor 返回这个管道能做的目标范围集合。
// 范围是**管道**的声明，不是动作的声明：选段管道写选段，生成管道写整章。
func ScopesFor(p Pipeline) ([]Scope, error)

// DefaultActionFor 返回这个管道在请求没有显式声明动作时执行的动作。
// 它回答的是旧路径的问题：旧请求不带动作，执行方按自己的形状干活，候选上
// 记的却是「这次是哪个动作做的」。
func DefaultActionFor(p Pipeline) (Action, error)
```

**可选上下文的选择**（F3 的归一）：同一个「用户可选的上下文集合」概念曾经有**两套类型**——生成管道的 `GenerationContextSelection` 与选段管道的 `RewriteContextSelection`，字段逐字相同。两套类型意味着两条管道各说各话。现在它是词汇包的**唯一拥有者**，请求侧的形状只有一份，**JSON 形状逐字不变**（因此没有 API 变化）：

```go
// ContextSelection 是用户对这次执行要纳入的可选上下文的选择。
type ContextSelection struct {
    Include map[ContextItem][]ContextEntry `json:"include,omitempty"`
    Exclude map[ContextItem][]ContextEntry `json:"exclude,omitempty"`
}

// ContextEntry 是一个上下文条目。
type ContextEntry struct {
    ID      string `json:"id"`
    Version int64  `json:"version,omitempty"`
}

// DroppedContextEntry 是一个没有进入模型请求的可选上下文条目。
type DroppedContextEntry struct {
    Category ContextItem `json:"category"`
    ID       string      `json:"id"`
    Reason   string      `json:"reason"`
}
```

**为什么是 `Plan` 而不是 `ActionSpec`**（D18，ch04）：`ActionSpec` 有 6 个公开字段，消费方拿到后仍要自己判断「这次要哪些上下文」「这个动作能不能这样采纳」「这个动作该怎么写指令」——**决策被搬到了调用方**，模块只提供了数据。ch04 点名的反模式正是这种「字段虽可读、语义被逐项暴露」的假隐藏。`PlanFor` 把这三类判断**一次性解析完**：接口从「4 个函数 + 1 个 6 字段 struct」缩到「1 个决策函数 + 1 个结果 struct + 1 个派生描述」——**接口更小、隐藏更多**。

**8 条动作规格**（实现前是契约；实现后由 `writingaction` 生成，守护测试断言两者一致）：

| Action | Label | 允许范围 | 允许的产出参数 | 所需上下文 | 产出 | 允许的采纳目标 |
| --- | --- | --- | --- | --- | --- | --- |
| `outline` | 提纲 | chapter | — | 章节、模板规则 | outline | 工作副本·整段 |
| `generate` | 生成 | chapter, section | 长度、语气 | 章节/选区、已授权资料、证据卡 | prose | 全部三种 |
| `continue` | 续写 | section | 长度、语气 | 选区及其前文、模板规则 | prose | 全部三种 |
| `rewrite` | 改写 | section | 长度、语气 | 选区及其上下文、模板规则 | prose | 全部三种 |
| `compress` | 压缩 | section | 比例、语气 | 选区、模板规则 | prose | 全部三种 |
| `expand` | 扩展 | section | 比例、语气 | 选区、模板规则 | prose | 全部三种 |
| `transform` | 转写 | chapter, section | 长度、模式、目标语言/读者层次 | 选区或章节、模板规则 | prose | 全部三种 |
| `review` | 语言评审 | chapter, section | — | 章节或选区、模板规则 | `ReviewReport` | 确认后应用到工作副本 |

**接口评论（ch15 先写评论，随实现维护）**：

> `PlanFor` 是动作语义的**唯一**拥有者。`Plan.Context` 声明必需、可选和禁止的上下文类别，具体 ID 由服务端授权解析；`Plan.Instruction` 是该动作专属的指令片段——**执行方只允许把已授权且符合策略的上下文与指令拼进请求，不得自行追加上下文项、不得自行改写指令语义**。两条管道之间只允许有**脚手架**差异（整章 vs 选段的上下文窗口、模板与证据卡的组装方式），不允许有**语义**差异。
> `Plan.Targets` 回答「这个动作能不能以这个目标被采纳」。采纳端**不得**用动作名做分支判断——那会把许可位写死成代码，许可位变化时不会编译报错。
> `PlanFor` 只校验**动作参数**，服务端的 context resolver 还必须校验用户选择的具体 ID 是否属于 `Plan.Context.Optional` 且当前授权有效。它**不碰** `AgentContextPackage.OutputConstraints`——那是服务端组装的安全边界，客户端参数进入那里等于让客户端改安全边界（I-13）。

**标识符跨包落地规则（D14）**：

| 位置 | 名字 | 说明 |
| --- | --- | --- |
| `writingaction` 包内的类型 | `Action` | 包名已限定：`writingaction.Action` |
| `candidateadoption.Candidate` 的字段 | `WritingAction` | JSON 键 `writing_action`；同包已有 `projectAuditRow.Action`（审计动作）与 `core.DependencyEdge.Action`（依赖边动作），故显式带前缀 |
| `candidateadoption.Candidate` 的字段 | `Origin` | 与 `workspacecore.SpecField.Origin`（`model.go:93`）**同语义同取值**（`service_facade.go:2524` 明确 `human` / `ai_generated` / `ai_assisted_human`），跨包一致；与 `core.DependencyEdge.Origin`（依赖边由谁发起）不是同一概念 |
| `workspacecore` 既有 | `Operation` / `Action` / `Origin` | **不动**。依赖边与审计的命名问题是既有债，本切片只保证新概念不与之混用 |

### 3.3 采纳模型：目标 × 许可

「可否采纳」拆成两个正交维度：

- **采纳目标**：`working_copy_partial` / `working_copy_whole` / `formal_version_whole`
- **该动作允许哪些目标**：由 `Plan.Targets` 声明（`PlanFor` 已解析）

| 动作 | 工作副本·部分 | 工作副本·整段 | 正式版本·整段 |
| --- | --- | --- | --- |
| 6 个正文型动作 | ✅ | ✅ | ✅ |
| `outline` | ❌ | ✅ | ❌ |
| `review` | ❌ | ❌ | ❌（先生成报告，确认后应用） |

**`outline` 不允许建正式版本**的理由：整段采纳今天直接建正式 ChapterVersion 并整篇覆盖正文（`candidate_adoption_store.go:533`）。用提纲替换用户已写的正文，语义上就是 spec #134 Out of Scope 的「deletion of user text」。改成只写工作副本后，正式版本安全，用户正文不丢。

**`review` 先生成 `ReviewReport`，不直接修改工作副本**：报告包含 findings、来源、基线版本、报告版本和撤权状态。用户在预览后显式确认，再调用 `ApplyReview`；该入口必须复用 `RequireHumanDecision`、工作副本 CAS、幂等键和事务内授权复核。应用时仍复用 `ReviewDecision`（`candidateadoption/confirmation.go:16-20`，`Disposition ∈ {resolved, retained_warning}`，理由必填）与 `ConfirmChapter`（`:77`）的逐条处置模型。

这样 review 与其它 AI 写路径拥有同一条“生成 → 预览 → 显式应用”边界，取消或拒绝报告不会改变工作副本。

**outline 的覆盖保护**：outline 仍只允许写工作副本，但服务端不得静默丢弃未提交编辑。若工作副本相对其基线存在未保存变更，必须拒绝整段替换并返回 `working_copy_dirty`；只有用户先保存、创建可恢复快照，或明确选择带快照的替换操作后才可继续。UI 仍需展示替换范围与 diff，确认载荷还必须带目标、基线修订号和快照 ID（如有）。

### 3.4 候选模型

**形态统一为一种**（D4）：`candidateadoption.Candidate` 仍是唯一候选类型，不多态。`review` 使用独立的 `ReviewReport`，但沿用同一基线、来源、撤权和确认边界。

| 字段 | 变化 | 说明 |
| --- | --- | --- |
| `WritingAction` | 新增 | 产生它的动作；JSON 键 `writing_action`（D14） |
| `Origin` | 新增 | `human` / `ai_generated` / `ai_assisted_human`（D8，复用 `SpecField.Origin` 的既有词汇与取值） |
| `Validity` | 语义扩展 | `fresh` / `stale` / **`not_authorized`**（新增，DB CHECK 需同步改） |
| `RunMode` | 新增 | `real` / `mock` / `real_api_fake_model`（从 rewrite 侧搬到候选侧，generation 侧补齐） |
| `ModelID` | 新增 | 模型标识 |
| `ReviewItems` | 候选层类型统一 | **候选层**一律是 `candidateadoption.ReviewItem`；rewrite 侧候选当前用 `[]workspacecore.ReviewItem`（`selected_rewrite.go:78`）——两者**字段完全相同但不同型**，转换点放 `workspace` 包内（本面拥有）。模型输出层的 `[]string`（`selected_rewrite_model.go:30`）退场 |
| `BodyMarkdown` | 不变 | `outline` 的提纲放这里；`review` 的 findings 放在 `ReviewReport` |

**`not_authorized` 的语义**：撤权 / 权限丧失导致候选不可用时，候选状态是 `not_authorized`（区别于「输入变了所以旧」的 `stale`）。两者都**不可采纳**。授权恢复后候选仍必须重新运行，不把历史候选自动恢复为 `fresh`；用户提示应明确为“恢复授权后重新运行”。

**与既有 `not_authorized` 字面量的区分**：仓里已有一个同名的 `not_authorized`，但那是**资料级拒权原因**（`internal/evidence/gateway.go:16` 的 `DenyReason`，前端 `sourceNotices.ts:25` 也有），语义是「这份资料没授权」。本切片的是**候选级状态**。两者**不得混用**：候选状态用 `Validity` 枚举承载，资料级拒权原因保持原样；前端展示时也要区分「候选不可用（授权失效）」与「这份资料无权」。

**`Validity` 从裸字符串变成枚举**，两侧现状与归一后的映射：

| 侧 | 现状 | 归一后 |
| --- | --- | --- |
| `generation` | `Validity` 裸字符串 `"fresh"`/`"stale"`（`service.go:545,653,667`） | `Validity` 枚举 |
| `candidateadoption` | 消费端写死 `candidate.Validity != "fresh"`（`candidate_adoption.go:335`） | 同一枚举 |
| `selected_rewrite` | `Status` 裸字符串 `"ready"`/`"failed"`/`"stale"` + 独立的 `ErrorCode`（`selected_rewrite.go:69,79,225,433-467`） | `Status` **拆成两件事**：失效判据归 `Validity`（`fresh`/`stale`/`not_authorized`）；**生命周期阶段**（queued / ready / applying / failed / stale）由 `status` 承载（D31）。`ready` → `fresh`，`stale` → `stale` |

**`failed` 的处置**：rewrite 侧现有的 `failed`（带 `ErrorCode`）**不是**一种有效性，而是一次**运行失败**——它没有产生可采纳的候选。归一后 `failed` **不进 `Validity` 枚举**，它由 run/candidate 的失败状态承载（`ErrorCode` 保留其原因）。`Validity` 只回答「这个候选现在还能不能被采纳」。这与 `operationcore.HumanGate`（谁有权决定）和 `Validity`（候选是否还可用）**不构成两套词表**——两者回答的不是同一个问题。

### 3.5 显式采纳确认

**两层，缺一不可**（D5）：

**第一层——机制**：`AcceptCandidate` 开头调 `core.RequireHumanDecision(ctx)` → `workspacecore` 转调 `operationcore.RequireHumanDecision(ctx, ErrHumanDecisionRequired)`（`workspacecore/human_decision.go:22-24`），做身份检查（机器身份、代他人一律拒）。`candidateadoption` **已 import** `workspacecore`（别名 `core`），**无新依赖**；先例：`workspace/selected_rewrite.go:284,528`（`Create` 与 `Apply` 各一处）。

**为什么不是「往 `human_decision.go` 名单加 `acceptCandidate`」**（本轮核实修正）：那份名单只被 `workspaceHumanGate()` 消费（`workspacecore/operation_pipeline.go:92-97`），而后者**只在** `operation_pipeline.go:142` 被调用，即只对**走 workspacecore operation pipeline** 的操作生效。采纳路径走的是 `SQLiteCandidateAdoptionStore.AcceptCandidate` 自己的事务（`candidate_adoption_store.go:420+`），**不走那条 pipeline**；`candidateadoption` 全包对 `operationcore` 零引用。名单里加 `acceptCandidate` 因此是一个**没有任何消费者的死条目**——它读起来像「受门禁保护」，实际什么也不保护。

所以：**名单不加项**，`internal/lingdoc/workspacecore/human_decision.go` **从本面所有权清单移除**。真正生效的护栏只有上面那一条直接调用。

**第二层——内容**（`candidateadoption`）：确认载荷作为 `AcceptCandidateInput` 的必填字段：

```go
type AdoptionConfirmation struct {
    WritingAction  Action          // 确认采纳的是哪个动作的结果（D14：跨包字段带前缀）
    Target         AdoptionTarget  // 采纳到哪里
    Range          *SelectionRange // 目标范围（部分采纳时必填）
    ConfirmedAt    time.Time
    CandidateOrigin Origin         // 候选来源，进入审计
}
```

「自然语言 yes 不算」由结构保证：没有这个载荷，采纳请求**在服务层被拒**（不是前端禁用按钮）。

**载荷进摘要**：`requestHash` 白名单加版本前缀与新字段 → 历史幂等行失配 → 走 D9 的版本化路径。

### 3.6 幂等摘要版本化（D9）

**问题**：摘要白名单从 v1 扩到 v2 后，历史 `lingdoc_candidate_adoptions.request_hash` 全部失配，旧幂等键回放会得到 `ErrIdempotencyConflict`——但这个错误对上层**没有决策价值**：用户重试旧键，真正需要的动作是「用新幂等键重试」，不是「停下来查是不是发了不同的请求」。

**方案**（**顺序固定：先换实现，再扩白名单**）：

0. **先把摘要实现换成 `operationcore.RequestHash`，再扩白名单。** 这一步**不改变任何历史摘要值**：`operationcore.digestJSON`（`operationcore/operationcore.go:117-128`）就是 `hex(sha256(json.Marshal(v)))`，与现有 `candidateadoption.requestHash`（`candidate_adoption.go:405-416`）**算法等价**。所以换实现是无损的，**历史幂等行不会因此失配**。
   顺带消除一个**第二个摘要拥有者**：现有 `requestHash` 用两步式（先 `b, _ := json.Marshal(...)`，再 `sha256.Sum256(b)`），**逃过了** `operationcore/ownership_test.go:80-108` 的守护——那个 AST 检查只拦**内联式** `sha256.Sum256(json.Marshal(x))`（要求 `json.Marshal` 是 `Sum256` 的直接实参）。
   **摘要结构体必须是专用结构**（现状如此），**不能**直接哈希 `AcceptCandidateInput`——后者含 `IdempotencyKey` 与 `ActorID`，进了摘要会让**全部**历史行失配。
1. `requestHash` 的摘要结构体第一个字段是 `DigestVersion int`（当前 `2`），`json.Marshal` 对 struct 按声明顺序输出 → 版本前缀稳定。
2. 幂等行新增 `digest_version` 列。回放时先比版本：
   - 版本相同 → 摘要相同 → 正常重放；
   - 版本相同、摘要不同 → `ErrIdempotencyConflict`（真冲突）；
   - **版本不同 → 新错误 `idempotency_key_expired`（409），语义是「这个键属于旧版摘要，请用新键重试」**。
3. **两处比对点都要版本感知**：`candidate_adoption_store.go:397`（回放，`row.RequestHash != requestHash(in)`）与 `:430`（写事务内，`existing.RequestHash != hash`）——现状两处都直接返回 `ErrIdempotencyConflict`。**只改一处就是后门泄漏**：同一个键在两条路径上会得到两种结论。
4. 新错误形态进 `internal/clienterror/catalog.go`。
5. **补守护测试的洞**：`operationcore/ownership_test.go` 的 AST 检查要扩到**两步式**（先 `json.Marshal` 赋给变量、再 `sha256.Sum256(该变量)`），否则下一个两步式摘要会继续逃逸。这是本切片的可执行交付物（见 §3.1 的「必须同批改」清单）。

### 3.7 前端交付形态

- **动作表客户端**：`frontend/src/api/lingdoc/writingAction.ts` —— 按当前 scope 读取 `writingaction.Describe(scope)` 的可序列化描述（动作名、允许范围、允许的产出参数、产出形状、允许的采纳目标）。
- **执行前面板**：`frontend/src/views/lingdoc/GenerationActionPanel.vue` —— 新增组件，**自包含**（像 PF-1 的 `G6OpsPanel.vue` 与 PF-6 的 `ContextPackagePanel.vue` 那样只新增文件），最终由前端集成挂进 `Workspace.vue`。
- **采纳对话框重写**：`LingDocCandidateAdoptionDialog.vue` 的**展示层可扩**（警告条、basis、review_items、错误、成功态、footer、样式），**请求体构造与幂等键逻辑必须重写**（`input` computed `:29-34`、`adopt()` 的键逻辑 `:52-53`）。连带 `frontend/src/views/lingdoc/candidateAdoption.test.ts:60-76` 钉死了「签名覆盖请求体每个字段」与「签名对象即发送对象」，改字段清单必须同步这两条断言。
- **diff**：复用 `diffWikiLines`（零 import 纯函数）。整段采纳必须展示**替换范围 + diff**；部分采纳展示选中片段的 diff。
- **来源标注**：采纳对话框展示候选 `origin`。**三值文案要新写**——`Workspace.vue:736` 现在是**二元分支**（`field.origin === 'human' ? '人工填写' : 'AI 候选 / 辅助填写'`），没有「AI 生成」与「AI 辅助」两者的区分；本切片新增第三态文案。
- **`Workspace.vue` 零改动**：现有「采纳到本章」按钮（`:524`）语义在正文型动作下**不变**（仍整段采纳建正式版本）→ 保留路径不需要适配。

### 3.8 复杂性预算（D21）

ch08：「每个新特性都要说明新增的接口、状态和依赖；若没有收益抵消，就重新设计。」本次新增如下：

| 类别 | 新增 | 计数 |
| --- | --- | ---: |
| **接口** | `writingaction` 对外公开名（`PlanFor` / `Plan` / `Describe` / 3 个错误） | 5 |
| | 许可位的派生入口 `TargetsFor`（D29） | 1 |
| | 管道声明（`Pipeline` / `ActionsFor` / `ScopesFor` / `DefaultActionFor` / `ErrUnknownPipeline`，D30） | 5 |
| | 可选上下文选择的三个类型（`ContextSelection` / `ContextEntry` / `DroppedContextEntry`） | 3 |
| | `ReviewReport` / `ApplyReview` / `RenderInstruction` 契约 | 3 |
| | 候选新增字段（`writing_action` / `origin` / `run_mode` / `model_id` / `validity` 枚举化） | 5 |
| | 采纳请求新增字段（确认载荷 5 项 + 采纳目标 + 目标范围） | 7 |
| | 产出参数维度（长度 / 语气 / 目标语言 / 读者层次 / 比例） | 5 |
| | 新 HTTP 端点 | 1 |
| | 新错误形态 | 1 |
| | `working_copy_dirty` 与 review 报告版本错误 | 2 |
| **状态** | 候选表新列 | 4 |
| | 幂等表新列 | 1 |
| | `ReviewReport` 生命周期与工作副本快照 | 2 |
| | `Validity` 取值 | 3 |
| | `RunMode` 取值（从 rewrite 侧搬到候选侧） | 3 |
| | `AdoptionTarget` 取值 | 3 |
| | `Scope` 取值 | 2 |
| **依赖** | `generation` / `workspace` / `candidateadoption` 三包新增对 `writingaction` 的依赖 | 3 |
| | `candidateadoption` → `workspacecore` 的既有依赖（`RequireHumanDecision`） | 0（新增） |

**收益对冲**：8 类动作共享**一份**词汇与**一份**候选形态（消除 4 处知识副本）；采纳语义（目标×许可、显式确认、来源标注、失效归一）从「散在两个包」收敛到一处；6 条守护测试把「动作语义唯一性」变成可执行约束而不是文档约定。

**超预算时的削减顺序**（按「先砍可见性、后砍质量」的反序）：

1. **先缩动作数**：本轮 8 类缩到 4 类（`rewrite` / `generate` / `compress` / `review`），其余留后续 PR——动作数减半，接口与状态**不增**，收益（共享词汇）保留。
2. **再降产出参数粒度**：5 个产出参数维度砍到 2 个（长度 / 语气），删掉 `TargetLanguage` / `Audience` / `Ratio`——状态与 UI 复杂度下降，动作语义不变。
3. **最后才动守护测试**：六条守护测试**不可裁剪**——它们是本次交付「知识只有一处」这个核心主张的唯一可执行证据；裁掉等于把设计意图退化成文档约定（ch17「只写规范不执行」）。
4. **不可削减项**：幂等摘要版本化、`not_authorized`、显式确认载荷——它们对应的是**正确性**（旧键误重放、授权失效被误采纳、AI 代确认），不是便利性。

## 4. 领域不变量

| # | 不变量 | 守护位置 |
| --- | --- | --- |
| I-1 | **动作封闭**：请求里的动作必须是词汇表中的已知动作；未知动作 → `invalid_request`，**绝不回退到自由文本** | `writingaction.PlanFor`（返回 `ErrUnknownAction`）+ HTTP 层 |
| I-2 | **产出参数先于执行**：动作的产出参数在**模型调用前**校验；不满足 → 拒绝且**不产生 run** | `generation.Start` / `selected_rewrite.Create` |
| I-3 | **AI 不写正式对象**：AI 产出只能成为候选或工作副本；确认、ChangeSet 应用、导出均不可达 | 断言测试（AI 侧无这些端口） |
| I-4 | **不可采纳性单调**：候选一旦 `stale` / `not_authorized` 不可变回 `fresh`；采纳时二次判定（TOCTOU） | `candidate_basis.go` 采纳前后各一次 |
| I-5 | **采纳需显式确认**：必须携带「确认了哪个动作 + 哪个采纳目标 + 目标范围」的机器可判定载荷；自然语言 yes 不构成凭据 | `AcceptCandidateInput.Confirmation` 必填 + `RequireHumanDecision` |
| I-6 | **幂等**：同键同请求 → 重放；同键异请求 → `idempotency_conflict`；旧版摘要的键 → `idempotency_key_expired`；重试不重复写工作副本 | `requestHash` 版本化 + 两处比对点 |
| I-7 | **三时机独立判定**：执行前 / 候选读取前 / 采纳前各自判权限；撤权在下一个时机被阻断 | 三个时机各自的判定点 + 撤权注入测试 |
| I-8 | **可追溯**：候选带动作、模型标识、run mode、来源；失败与取消保留 run 输入、状态与原因 | 候选列 + `generation/store.go` 的 `InputJSON` 可读回 |
| I-9 | **候选形态唯一**：**候选层**的待核事项一律是 `candidateadoption.ReviewItem`；`workspacecore.ReviewItem`（字段相同、类型不同）只作为工作副本侧类型存在，转换点在 `workspace` 包内；失效判据一律是 `Validity` 枚举（`fresh`/`stale`/`not_authorized`），`failed` 不进该枚举 | 编译期 + 守护测试（禁止第三种待核事项类型；禁止 `[]string` 待核事项） |
| I-10 | **许可位而非分支**：采纳端**不得**用动作名做分支；「能不能这样采纳」只查 `Plan.Targets` | 守护测试（AST 扫描采纳端无动作名分支） |
| I-11 | **词汇唯一拥有者**：动作语义、产出参数、采纳许可只在 `internal/lingdoc/writingaction`；其它包不得复制定义 | 守护测试（AST 扫描禁止第二处动作枚举） |
| I-12 | **工作副本覆盖须受保护**：outline 整篇替换前，若工作副本相对基线有未保存变更，服务端必须返回 `working_copy_dirty`；只有保存、创建可恢复快照或带快照的明确替换操作才可继续。确认载荷必须带目标、基线修订号和快照 ID（如有） | 服务端 dirty 检查 + 前端 diff/确认测试 |
| I-13 | **产出参数不得进入安全边界**：客户端提交的 `OutputParams` **绝不**进入 `AgentContextPackage.OutputConstraints`（那是服务端 `BuildAgentContext` 组装的 AI 安全边界）。两者是两个概念，不得合并 | 守护测试（AST 扫描：客户端参数不得写入 `OutputConstraints`）+ 服务端单测 |
| I-14 | **摘要规则只有一个拥有者**：请求摘要只由 `operationcore.RequestHash` 计算；`internal/lingdoc` 内不得出现自算摘要（含**两步式** `json.Marshal` 后再 `sha256.Sum256`） | `operationcore/ownership_test.go` 的 AST 检查扩到两步式（本切片交付物之一） |
| I-15 | **执行方不得自建动作语义**：执行方只组装 `Plan.Context` 声明的上下文项，并通过服务端授权解析具体 ID；只拼 `Plan.Instruction`；**不得追加上下文项、不得自行改写指令语义、不得按动作名分支** | 守护测试（AST 扫描两个 host adapter 的输入组装点与指令拼接点）+ 变异验证 |
| I-16 | **动作语义只有一处**：动作枚举、产出参数、上下文需求、采纳许可、指令片段**全部**住在 `internal/lingdoc/writingaction`；两条管道只允许有**脚手架**差异（整章 vs 选段的上下文窗口与组装方式），不允许有**语义**差异 | 守护测试（AST 扫描两个 host adapter：除脚手架外不得出现动作相关的条件分支或字符串） |
| I-17 | **运行模式的词表只有一个拥有者**：`RunModeReal` / `RunModeMock` / `RunModeRealAPIFakeModel` 只住在 `candidateadoption`；别处只允许**带限定符**的引用，裸名即第二份词表 | 守护测试（AST 扫 `internal/lingdoc` 的非测试文件：裸用词表成员即红；并断言拥有者真的定义了这三个成员） |
| I-18 | **可选上下文的选择只有一个拥有者**：`ContextSelection` / `ContextEntry` / `DroppedContextEntry` 只在 `internal/lingdoc/writingaction` 定义；JSON 形状逐字不变 | 守护测试（AST 扫 `internal/lingdoc`：别处再定义一份即红）+ 形状断言（锁住 JSON 键与 tag） |
| I-19 | **生命周期阶段与失效判据各归其位**：失效判据只有 `Validity` 一个拥有者；`status` 承载生命周期阶段（queued / ready / applying / failed / stale），且 JSON 键逐字不变 | 守护测试（生命周期阶段的取值表 + 候选 JSON 同时带 `status` 与 `validity`） |
| I-20 | **AI 路径的扫描面覆盖全部模型驱动文件**：守护扫的是「所有驱动模型的文件」（调用了模型调用点的 + 一份显式必扫清单），不是一份会漏掉新文件的短名单 | 守护测试（必扫清单与生成管道整包必须逐个出现在扫描面内；扫描面为空即红） |

## 5. 验收矩阵

状态列初始全部 `TODO`。矩阵未闭环前 PR 保持 Draft（`development-rules` §1.2）。

| # | 验收项 | 用户可见入口 | 领域不变量 | 成功 | 失败 / 权限 / 恢复 | 持久化与并发 | 证据 | 状态 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| A1 | 八类动作各有枚举与各自产出参数 | 动作表端点 + 动作面板 | I-1, I-11 | 8 条规格可读，每条声明范围/产出参数/产出/许可 | 未知动作 → `invalid_request` 且不产生 run | 无 | `writingaction` 单测 + HTTP 测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A2 | 动作与产出参数在执行前可见 | `GenerationActionPanel.vue` | I-1, I-2 | 面板显示动作、允许范围、可用产出参数、产出形状、许可 | 产出参数不合法 → 面板不发请求 | 无 | 前端组件测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A3 | 动作封闭：不回退自由文本 | 采纳/生成请求 | I-1 | `action=rewrite` 必填 | 缺 `action` → 400；`action=unknown` → 400 | 无 | HTTP 测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A4 | `transform` 两个子模式 | 动作面板 + 请求 | I-1, D11 | `mode=translate` 需 `target_language`；`mode=explain` 需 `audience` | 缺子参数 → 400；混用 → 400 | 无 | 单测 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A5 | 选中范围生成 | rewrite 端点 | I-2 | 选段改写按 `rewrite` 执行，产出候选带 `selection` | 选区越界/代理对 → `invalid_request` | 选区以 UTF-16 偏移记录 | `selected_rewrite` 测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A6 | 上下文显示 | 候选预览 | I-8 | 候选带项目、章节、Context Revision、模板/规则版本 | 撤权后受限内容不显示 | 无 | 前端测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A7 | 候选预览（正文/来源/待核分列） | 候选预览 + 采纳对话框 | I-9 | 三类内容分开展示 | 不支持的引用 → 待核事项 | 无 | 前端测试 + 后端单测 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A8 | 部分采纳写工作副本 | 采纳请求 `working_copy_partial` | I-5, I-10 | 指定范围写入工作副本，四元 CAS 生效 | 基线变化 → `version_conflict`；重试不重复写 | 四元 CAS（base 版本 + spec revision + 工作副本修订号）+ 幂等管道；并发写语义见 **A41** | `working_copy` + `candidateadoption` 测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A9 | 整段采纳展示替换范围与 diff | 采纳对话框 | I-12, D13 | 展示替换范围 + 行级 diff | — | — | 前端组件测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A10 | outline 整段采纳只写工作副本 | 采纳请求 | I-10, I-12 | 写工作副本，**不建正式版本** | dirty 工作副本 → `working_copy_dirty`；无快照不得覆盖 | 正式版本指针不变（`current_version_id` 不动）；工作副本并发写语义见 **A41** | 断言测试 + dirty/快照测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A11 | 采纳需显式确认 | 采纳请求 | I-5 | 载荷含动作/目标/范围 | 缺载荷 → 400；机器身份 → 拒绝 | 载荷在**事务内**校验（与授权同一事务）；并发采纳见 **A40** | HTTP + 单测 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A12 | AI 不得直接写正式 ChapterVersion | 全部 AI 路径 | I-3 | AI 路径无正式版本写入口 | 断言：AI 路径触达不到建版本代码 | 无 | 断言测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A13 | AI 不得建确认 / 应用 ChangeSet / 导出 | 全部 AI 路径 | I-3 | 断言三者不可达 | — | 无 | 断言测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A14 | 不得编造授权来源 | 候选来源校验 | I-7 | 正文标记集合 == 授权来源集合 | 模型编造来源 → 拒绝或转待核 | 无 | 现有 `validateSourceReferences` 测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A15 | 撤权 → `not_authorized` 且不可采纳 | 候选读取 | I-4, I-8 | 候选状态 `not_authorized` | 采纳 → 拒绝并提示恢复授权后重新运行 | 不可逆；授权恢复不得自动恢复为 `fresh` | `candidate_basis` 测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A16 | 版本变化 / Context Revision 变化 → `stale` | 候选读取 | I-4 | 状态 `stale` | 采纳 → 拒绝并指向重新运行 | 不可逆 | 现有 `validateNewBasis` 测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A17 | 模板/规则变化 → `stale` | 候选读取 | I-4 | 状态 `stale` | 同上 | 不可逆 | 同上 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A18 | 来源缺失 → 不可采纳 | 候选读取 | I-4 | 状态 `stale` 或 `not_authorized` | 采纳 → 拒绝 | 不可逆 | 同上 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A19 | 撤权四个时机都阻断 | 执行前/运行中/读候选前/采纳前 | I-7 | 四个注入点各自阻断 | 每点单独用例 | 无 | 四个注入测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A20 | 「人工/AI/AI 辅助」来源可见 | 候选 + 采纳对话框 | D8 | 候选带 `origin`，采纳后写入确认载荷与审计 | — | 无 | 单测 + 前端测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A21 | 失败与取消保留 run 输入、状态、原因 | run 读取 | I-8 | `Run` 可读回输入摘要、状态、原因 | 失败 → `error{code,message,retryable}` | `generation_runs.InputJSON` 不清空 | `generation` 单测 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A22 | 重试用新幂等键 | 重试 | I-6 | 新键 → 新 run；不重复写工作副本 | 旧键 → `idempotency_key_expired` | 幂等表 + 摘要版本列；跨 actor 同键见 **A43** | `candidateadoption` + `generation` 测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A23 | 幂等摘要版本化 | 采纳请求 | I-6, D9 | 同版本同键 → 重放；旧版键 → 新错误 | 两处比对点一致 | 摘要版本列；**回放路径 `:397` 与写事务内 `:430` 必须同时版本感知** | 单测 + 迁移测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A24 | 拒绝与取消不改动工作副本 | 拒绝/取消 | I-3 | 工作副本读回不变 | — | — | 现有 `working_copy` 测试 + 新增 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A25 | 超时与格式错误 | 运行 | I-8 | 保留状态与原因 | 超时 → `interrupted`；格式错误 → 失败且不产生候选 | — | 现有测试 + 动作化后复跑 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A26 | 不受支持的引用 | 候选 | I-9, I-7 | 转待核事项 | 编造来源 → 拒绝 | — | 现有 `source_marker_charset_test` 扩展 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A27 | 运行模式分离 | 候选 | I-8 | 候选带 `run_mode`（`real`/`mock`/`real_api_fake_model`） | mock 不冒充真实证据 | — | 单测 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A28 | 两条管道共用同一词汇 | 两个管道 | I-11, I-16, D4 | 两边请求同一 `writing_action` 值，走同一 `PlanFor` | 两条 host adapter 的语义差异消失，只剩脚手架差异 | — | 守护测试（AST 扫描禁止第二处枚举、禁止语义分支） | ✅ 见 PR #357 的 A1–A43 对照表 |
| A29 | 许可位而非分支 | 采纳端 | I-10 | 采纳端无动作名分支 | 变更许可位不需要改采纳端代码 | — | 守护测试（AST 扫描） | ✅ 见 PR #357 的 A1–A43 对照表 |
| A30 | 路由面与清单同步 | 路由 | — | 新端点已注册 | `lingdoc_routes_test.go` 的 **70 条路由**计数同步更新 | — | 路由测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A31 | 产出参数不进入安全边界 | 动作请求 → 上下文包 | I-13 | 客户端参数只进 `OutputParams`；`OutputConstraints` 仍是服务端那四条 | 尝试把参数写进 `OutputConstraints` → 守护测试红 | — | AST 守护测试 + 服务端单测 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A32 | 摘要规则只有一个拥有者 | 采纳请求 | I-14 | `candidateadoption` 的摘要走 `operationcore.RequestHash` | 两步式自算摘要 → 守护测试红（**含两步式**，不只是内联式） | 历史摘要值不变（算法等价） | 变异验证 + 迁移测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A33 | 执行方不自建动作语义 | 两条管道的输入组装与指令拼接 | I-15, I-16 | 两边只组装 `Plan.Context` 声明且经授权解析的项、只拼 `Plan.Instruction` | 在 host adapter 里追加未声明项或自写动作指令 → 守护测试红 | — | AST 守护测试 + 变异验证 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A34 | 动作标识符不撞名 | 候选与确认载荷 | D14 | 字段为 `WritingAction`（JSON `writing_action`）；`Origin` 与 `SpecField.Origin` 同取值 | 出现 `Candidate.Action` 或第二套 origin 取值表 → 守护测试红 | — | AST 守护测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A35 | 语言评审的落地通道 | 动作面板 → 评审报告 → 工作副本 | D16, I-5 | 先生成 `ReviewReport`，用户确认后由 `ApplyReview` 应用 findings | 取消/拒绝报告不改工作副本；基线变化 → `version_conflict` | 报告版本 + 工作副本 CAS + 幂等键 + 事务内授权复核 | 后端 + 前端测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A36 | 产出参数取值集固定 | 动作面板 | D17 | 长度/语气/语言/读者层次/比例各取 D17 枚举 | 越界取值 → 400 | — | 单测 + 前端测试 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A37 | 用户可选的上下文集合（#134 US5） | 动作面板 + 上下文包 | I-15, I-16 | 用户只能在 `Plan.Context.Optional` 中增删资料、证据卡、主张、来源；服务端按当前授权取交集后组装 | 撤权或越过策略的 ID 不进入模型请求 | 具体 ID 的授权解析与动作计划同一请求绑定 | 复用 `Request.AssetIDs`/`EvidenceCardRefs` 与 `AgentContextPackage`（`agent_research_model.go:351-370`）；前端消费归 PF-6 #330 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A38 | 失效/阻断原因对用户可读（#134 US16） | 候选读取与采纳 | I-8 | 区分「输入变了→重新运行」与「授权没了→恢复授权后重新运行」，并给出下一步动作 | 越权 → 拒绝并指向有权限的人 | — | `EffectivePermission.ReasonCode`（`permission_policy.go:189`）+ 新错误形态；文案与 G6 事件接线归 PF-3 #327 与 #145 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A39 | 候选形态唯一的守护测试（#326 验收第 11 条第三条） | 仓内守护 | I-9 | 候选层待核事项只有 `candidateadoption.ReviewItem`；`workspacecore.ReviewItem` 只在工作副本侧存在，转换点在 `workspace` 包内 | 出现第三种待核事项类型 → 守护测试红 | — | AST/grep 守护测试 + 变异验证 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A40 | **双人同时采纳同一候选** | 采纳请求（两个不同 actor / 不同幂等键） | I-4, I-6 | 只有第一个成功 | 第二个 → `ErrVersionConflict`（`current_version_id` CAS 不再匹配），**不产生第二个正式版本** | `candidate_adoption_store.go:547-557`：`WHERE current_version_id = <读到的 oldVersionID>`，`RowsAffected != 1` → `ErrVersionConflict`；`project_version` 同款 CAS（`:558-562`）；版本链 `ParentVersionID: oldVersionID`（`:535`） | 并发测试：两事务同时采纳同一候选，断言只建一个版本 | ✅ 见 PR #357 的 A1–A43 对照表 |
| A41 | **工作副本并发写** | 部分采纳 / 整段采纳 | I-4 | 第二个写被版本条件挡住 | 基线或修订号不符 → `ErrVersionConflict`，工作副本**不出现半写** | `workspacecore/working_copy.go:159-164`（`SaveWorkingCopy` 四元 CAS：base 版本 + spec revision + 工作副本修订号）、`:235-239`（`ApplyRewrite` 同款）；两者都走 `runOperation` 的幂等管道 | 并发测试：两个动作同时写同一工作副本，断言一个成功一个 `version_conflict` | ✅ 见 PR #357 的 A1–A43 对照表 |
| A42 | **采纳事务中途撤权** | 采纳请求 | I-4, I-7 | 事务内撤权 → 写前被拒，**零写入** | 提交后撤权 → 写已落地，返回 `candidate acceptance committed; current access recheck failed` | 写前：`candidate_adoption_store.go:424` `authorizeCandidateInTransaction` + `:501` `validateEvidenceInTransaction`（同事务内）；写后：`candidate_adoption.go:311-313` `recheckCandidateAcceptance`——**注意这一路径不是「零写入拒绝」，是「已提交 + 明确告知」** | 注入撤权测试：分别在事务内与提交后撤权，断言前者零写入、后者错误消息含 `committed` | ✅ 见 PR #357 的 A1–A43 对照表 |
| A43 | **同一幂等键跨 actor 复用** | 采纳请求 | I-6 | 两个 actor 用同一键 → 视为**两个独立操作**，各自成功 | 同一 actor 同键异内容 → `idempotency_conflict` | 幂等行唯一键 `(project_id, chapter_id, actor_id, idempotency_key)`（`candidate_adoption_store.go:111-120`）——**`actor_id` 在键内**，跨 actor 不冲突 | 单测：跨 actor 同键各自建版本；同 actor 同键异内容 → 冲突 | ✅ 见 PR #357 的 A1–A43 对照表 |

**覆盖对照**：

| 来源 | 覆盖 |
| --- | --- |
| #326 的 11 条验收 | A1–A43 全部覆盖；第 11 条的三条守护测试分别由 **A28**（词汇唯一）、**A29**（许可位非分支）、**A39**（候选形态唯一）承担 |
| #134 的 19 条 User Story | **17/19 条**由 A1–A27 覆盖（US13 → A13，US18 → A27，US19 → A11/A13）；**US5 → A37**、**US16 → A38** 为本轮补入 |
| 第 1–5 轮裁决（D14–D17） | A31–A36 |
| 第 6 轮裁决（D18–D23） | A28/A29/A33（接口重塑与语义归位）、§6（PR-0 + 8 个动作 PR）、§3.8（复杂性预算）、**A40–A43**（并发）、A1/A36（错误形态与描述派生） |
| **未覆盖项（外延/外部依赖）** | US17 的 **G6 观测接线**（生成与采纳路径当前不写 G6 事件，`runtime_control.go:337` 的类别枚举里没有 generation/adoption）——归 PF-3 #327 与 Spec #145；US16 的**任务助手解释入口**同样依赖 G6 接线，A38 本切片只保证错误语义与文案存在 |

## 6. 交付切片与 PR 序列

**9 个串行 PR**（PR-0 契约与迁移，随后 8 个动作 PR；D20）。**票仍是一张**（#326 覆盖整组改进项，符合「一条 issue 覆盖一整组改进项」），但每个 PR 独立过 CI、独立被评审，矩阵**按动作分块、各自闭环**。

`development-rules` §5：「只有最后一个交付切片完成全部矩阵项时，才关闭对应 Issue」——所以 **#326 在最后一个 PR 合入、`main` CI 全绿后才关闭**。

| PR | 交付 | 矩阵分块 | 完成判据 |
| --- | --- | --- | --- |
| **PR-0** | 契约、参数校验、迁移兼容、守护测试；不接入生产动作 | A1–A4、A28–A34、A36、迁移演练 | 旧库升级/回滚通过；守护测试可变红；无生产路径行为变化 |
| **PR-1** | `rewrite` 纵切片（词汇 → 产出参数 → 执行 → 候选 → 采纳 → UI）+ 共享采纳语义 | A6–A9、A11–A14、A19、A20、A22–A27、A37–A39 | rewrite 范围内矩阵行全绿 |
| **PR-2** | `generate` | A5/A6/A7 + 该动作的产出参数与许可用例 | 该动作分块闭环 |
| **PR-3** | `continue` | 同上 | 同上 |
| **PR-4** | `compress` | 同上 | 同上 |
| **PR-5** | `expand` | 同上 | 同上 |
| **PR-6** | `transform`（两个子模式） | 同上 + A4 子模式用例 | 同上 |
| **PR-7** | `outline` | A10/A12 + 覆盖工作副本的明示要求 | 该动作分块闭环 |
| **PR-8** | `review` 的 `ReviewReport` / `ApplyReview` + 最终归一 | A15–A18、A21、A24–A27、A30、A35 | **全部矩阵行闭环**；#326 关闭 |

**每个 PR 的 Draft/Ready 规则**（`development-rules` §3）：**该 PR 范围内**的矩阵行全闭环才转 Ready；范围外的行在该 PR 的 body 里显式标为「本 PR 范围外，由 #326 后续 PR 承担」，不写成已通过。

**为什么不是单 PR**（D20，ch08）：8 个动作 × 2 条管道 × 3 档采纳在单 PR 里是一个大面，评审要同时判断 8 个动作的提示词与产出参数，而矩阵 39 行在同一个 head 上堆叠；ch08 明确「小步迁移比一次性重写更容易验证」。先用 PR-0 锁定契约和迁移，再让 rewrite 作为第一个完整纵切片；`review` 的写入安全边界在最后一个动作 PR 中单独闭环。

**分支策略**：9 个 PR 串行共用一条集成分支（每个 PR 合入后从 `main` 重新开下一条），或每条 PR 从 `main` 开新分支但**不 rebase 其它 PR 的分支**——避免同一批文件被反复 rebase（文件集在扩所有权后是同一批）。

## 7. 依赖边与阻塞

| 边 | 类型 | 状态 |
| --- | --- | --- |
| #138 → #326 | 实现依赖 | **已解除**：生成接入已落地 |
| #133 → #326 | 实现依赖 | **已解除**：编辑器/版本已落地 |
| #326 → 登记处基础设施票 | 流程依赖（新增） | 本票只登记路径集；登记处由新票补建 |
| PF-1 #325 / PF-3 #327 / PF-4 #328 / PF-5 #329 / PF-6 #330 ↔ #326 | 路径所有权 | **零重叠**（逐文件核对，见 §3.1）；PF-3 独占 `workspacecore/model.go`，本面不碰 |
| #326 → 最终前端集成 | 交付依赖 | `Workspace.vue` 为保留路径，动作面板与采纳对话框以自包含组件交付，集成另做 |

**外部阻塞**：无。

## 8. 迁移与数据形状

**1 个迁移版本**（versioned + sqlite 双写，`migrations/versioned/000125_*` 与 `migrations/sqlite/000046_*`，各含 `.up.sql` 与 `.down.sql`）。由于 SQLite 不能直接修改已有列的 `CHECK` 约束，SQLite 的 up migration 必须采用临时表重建：创建新表、复制并回填旧候选、校验行数与关键字段、原子替换旧表；PostgreSQL 则显式 `DROP CONSTRAINT` / `ADD CONSTRAINT`。两条迁移都必须可在真实旧库上执行。

| 表 | 变更 |
| --- | --- |
| `lingdoc_candidates` | 加 `writing_action`、`origin`、`run_mode`、`model_id`；`validity` 的 CHECK 从 `IN ('fresh','stale')` 改为 `IN ('fresh','stale','not_authorized')` |
| `lingdoc_candidate_adoptions` | 加 `digest_version` |

**旧数据回填规则**：历史候选的 `writing_action`、`origin`、`run_mode`、`model_id` 必须有明确的兼容值或允许 `NULL`；不得把未知历史事实伪造为真实动作、模型或来源。读取层必须能区分“历史未记录”和“新候选明确为某值”。down migration 必须先验证没有依赖新增列的行，再安全删除新增列；若无法无损回滚，必须把迁移标为不可逆并记录原因。

**同批必须改的清单文件**：`internal/database/migration_sqlite_versioned_schema_test.go:91`（45 → 46）、`internal/router/lingdoc_integration_test.go:95`（迁移文件清单）、`internal/lingdoc/operationcore/ownership_test.go:80-108`（摘要唯一拥有者守护扩到两步式）。前两者漏改会让测试红。

`internal/lingdoc/workspacecore/sqlite_migrations_test.go:50`（表名清单）**本切片不需改**——本次重建既有表但**不新增表**，表名集合不变。**仅当将来新增表时才需改。**

**回归门槛**：使用包含旧候选、旧幂等行和脏工作副本的 fixture，迁移 up/down 各跑一遍 SQLite 与 PostgreSQL；重启后读回候选的 `writing_action`/`origin`/`validity`/`run_mode`；旧幂等行以 `digest_version = 1` 读回并触发 `idempotency_key_expired`；**改用 `operationcore.RequestHash` 后历史摘要值不变**（算法等价），用一条历史幂等行验证重放仍然成立；另测 SQLite 表重建过程中的行数、唯一键、索引和外键保持不变。

## 9. 风险登记与健康评分

### 9.1 风险登记

| 风险 | 影响 | 缓解 | 状态 |
| --- | --- | --- | --- |
| 扩所有权后 PF-2 横跨 5 个 Go 包 + 前端 api + 新包 + 迁移 | 变化面大，PR 体量超出单面直觉 | 纵切片提交序列（D12）；逐文件列举所有权；PR body 矩阵按动作分块 | 已知，接受 |
| 登记处不存在，所有权无自动校验 | 两面碰同一文件无人拦截 | 已决定另开基础设施票（D3）；本票逐文件列举并写回票面 | 已知，外部依赖 |
| `workspacecore/model.go` 被 PF-3 独占 | 来源标注若落在章节版本层会撞车 | 决策 D8 已避开：不碰 `model.go`，来源落候选 | 已消除 |
| `workspacecore/working_copy.go` 的 `ApplyRewrite` 不接受范围 | 部分采纳的范围计算必须落在上游 | 范围计算放 `candidateadoption`（读候选正文 → 拼 range → 调 `ApplyRewrite`），不改 `working_copy.go` | 已消除 |
| 摘要白名单升级导致历史键失配 | 旧幂等键重试语义错误 | D9 摘要版本化 + 专用错误形态 | 已消除 |
| `validity` DB CHECK 约束 | 加 `not_authorized` 要改两套 DDL | 合并进 1 个迁移版本 + 三处清单同批改 | 已消除 |
| 路由数 70 的断言 | 新端点会红 | 与 `workspace/http.go` 同批改 `lingdoc_routes_test.go` | 已消除 |
| G6 无 generation/adoption 类别，生成与采纳路径不写 G6 事件 | US16/US17/US18 的观测缺口 | 本切片记录候选的 `run_mode`/`action`；G6 事件接线归 PF-3 #327 与 #145，本票登记为外部依赖 | 已知，外部依赖 |
| `selected_rewrite` 住在传输包 `workspace/`（层次错位） | 领域逻辑位置不当 | 本轮**不修**（改动面过大）；登记进 ADR 0008 的「范围外」与后续项 | 已知，本轮不修 |
| 8 个动作一次交付 | 动作语义可能彼此不一致 | 每个动作一个提交 + 同一模板 + 词汇表集中校验 | 已知，缓解中 |
| review 直接写工作副本会绕过显式确认 | AI 评审可能在用户未确认时改变正文 | 先生成 `ReviewReport`，`ApplyReview` 复用人工门禁、CAS、幂等和事务内授权复核 | 已消除 |
| outline 整篇替换会丢失未保存编辑 | 用户工作副本发生不可恢复的数据丢失 | dirty 时服务端拒绝；保存、快照或明确的带快照替换操作后才允许继续 | 已消除 |
| SQLite 无法直接修改既有 CHECK 约束 | 旧库升级失败或迁移后索引/外键丢失 | 临时表重建 + 行数/键/索引/外键校验；真实旧库 up/down 演练 | **已消除**（#352 实测，证据见下） |
| **命名撞名（本轮核实）**：`Action` 已被三义占用（依赖边 `workspacecore/impact_model.go:26`、审计 `candidateadoption/candidate_adoption_store.go:142`、治理操作 `operationcore.Identity.Operation`），后两个就在本面要改的包里 | 同包内 `Action` 二义，`core.DependencyEdge{Action:...}` 也在同文件 | D14：类型叫 `Action`（包名限定），跨包字段叫 `WritingAction`；`Origin` 保持（与 `SpecField.Origin` 同语义同取值） | 已消除 |
| **「约束」二义 + 权限边界（本轮核实）**：既有 `AgentContextPackage.OutputConstraints` 是**服务端安全边界**（`agent_action_service.go:1686` 硬编码四条），新概念是**客户端产出参数** | 中文同叫「约束」会误导；混用会让客户端改安全边界 | D15 改名「产出参数 / `OutputParams`」+ 不变量 I-13 写死两者不得合并 | 已消除 |
| **摘要守护测试有洞（本轮核实）**：`operationcore/ownership_test.go:80-108` 的 AST 检查只拦**内联式** `sha256.Sum256(json.Marshal(x))`；现有 `candidateadoption.requestHash` 用**两步式**（`candidate_adoption.go:406,414`）**逃过了检查** | 仓里实际存在**第二个摘要拥有者**，违反该测试自己的声明 | D9 第 0 步（换实现，算法等价故历史值不变）+ 第 5 步（AST 检查扩到两步式）+ 不变量 I-14 | 已消除 |
| `operationcore` 依赖方向 | 若 `candidateadoption` 不能 import `operationcore`，D9 的「改走 `RequestHash`」不成立 | 已核实：`operationcore` 只 import `internal/types`，`candidateadoption → operationcore` **无环** | 已核实，无风险 |
| **D5 第一层是死条目（本轮核实修正）**：`human_decision.go` 名单只被 `workspaceHumanGate()` 消费，而后者只在 `operation_pipeline.go:142` 被用 | 名单里加 `acceptCandidate` 读起来像「受门禁保护」，实际**没有任何调用会因此走 HumanGate**——采纳路径不走 workspacecore operation pipeline | 改为 `AcceptCandidate` 直接调 `core.RequireHumanDecision(ctx)`（`workspacecore` 已导出，`candidateadoption` 已 import，**无新依赖**；先例 `selected_rewrite.go:284,528`）；**名单不加项**，`human_decision.go` 移出所有权 | 已消除 |
| **两个同名同形的 `ReviewItem`（本轮核实）**：`workspacecore.ReviewItem` 与 `candidateadoption.ReviewItem` 字段完全相同但不同型；rewrite 候选用前者，`ApplyRewrite` 收的也是前者 | 与「不碰 `workspacecore/model.go`」直接冲突 | 候选层统一到 `candidateadoption.ReviewItem`，`workspacecore.ReviewItem` 保留为工作副本侧类型，转换点放 `workspace` 包内（本面拥有） | 已消除 |
| **`not_authorized` 同名不同义（本轮核实）** | 仓里已有资料级 `not_authorized`（`evidence/gateway.go:16` 的 `DenyReason`） | 候选级用 `Validity` 枚举承载，资料级拒权原因保持原样；前端展示区分「候选不可用」与「这份资料无权」 | 已消除 |

### 9.1.1 迁移方言差异的实测结论（#352 补记）

原风险行「SQLite 无法直接修改既有 CHECK 约束」记为「已知，待迁移实现验证」。#352 用**真实旧库**（SQLite v45 / PostgreSQL v124）跑完 up→down→up 演练后，结论如下，验收项按**方言实际语义**写：

| 事项 | 实测结论 | 证据 |
| --- | --- | --- |
| 改写既有 CHECK 约束是否需要表重建 | **PostgreSQL 可原地 `DROP/ADD CONSTRAINT`，不需要重建**；**只有 SQLite 需要临时表重建** | `internal/database/migration_candidate_shape_upgrade_test.go` |
| 「表重建在两套方言上都成立」这条验收项 | **在 PG 侧不成立** —— 按最坏方言推广会让 PG 侧实现与验收项对不上。PG 侧改为断言「约束换掉后数据/索引/外键不变」，SQLite 侧断言真的重建 | 同上 |
| 迁移的原子性 | 两套脚本**都原子**：golang-migrate 的 postgres 驱动不把迁移包在事务里，但 lib/pq 用 **simple protocol** 把整个 `.sql` 文件当一条查询发出，PostgreSQL 简单查询协议对多语句批次用**隐式事务**；SQLite 驱动则显式 `tx.Begin()/Commit()` | 同上 + 两套迁移脚本 |
| 行数/主键/索引/外键保持 | SQLite 重建后由迁移内的守卫表校验（行数相等 + 每个旧主键都能在新表命中），索引与外键在重建后重建 | 同上 |

**执行环境**：本机 PostgreSQL 17.9（Docker 容器 `WeKnora-postgres-dev`）+ `LINGDOC_TEST_POSTGRES_DSN` 约定，PG 侧迁移**已实际执行**（一次性库 + 隔离 schema，跑完 DROP，残留 0）。未设置该变量时相关用例明确 SKIP 并写原因。

**CI 侧的已知缺口**：`app.yml:145-147` 跑全量 `go test`（含 `internal/database`）却**没有 PG service**，PG 用例在 CI 上会 SKIP；有 PG service 的 `lingdoc-interface-integration.yml` 只跑 `./internal/router` 与 `./internal/container` 的定向 `-run`。已登记为 **Issue #360**（CI 缺口），**不是本面范围**。

### 9.2 健康评分（`lingdoc-health-scoring`，Spec 完成阶段）

**评分绑定**：Git SHA `bfcb0432`（`origin/main`；开发计划最初写于 `633ba34`，其间 main 前进 47 个提交、58 个文件——已复核：**与本计划引用的全部文件零交集**，故路径与行号断言仍然成立）；Spec [#134](https://github.com/magician336/lingdoc/issues/134)；PR 无（开发计划阶段）；取样范围 = 本切片涉及的 6 个 Go 包/目录（`writingaction`（新）、`generation`、`workspace/selected_rewrite*`、`candidateadoption`、`workspacecore/human_decision.go`、`workspace/http.go`）+ 前端 `api/lingdoc/{generation,candidateAdoption,workspace}.ts` + `components/LingDocCandidateAdoptionDialog.vue` + `views/lingdoc/{generationAttempt,candidateAdoption}.ts` + 契约与测试；置信度 **B**（结论由源码逐行取证与静态检查支撑，但本切片代码尚未实现，设计意图不能按已实现给满分）。

**修改前评分：`H_L` LingDoc 系统健康度 = 65/100（B）**。本轮新增 `ReviewReport`、outline dirty 保护、上下文策略和迁移演练要求后，接口与状态预算已经变化；以下分项保留为修改前基线，不能当作当前设计的最终评分。实现 PR-0 后应按同一量表重新评分。

| 维度 | 权重 | 二级指标 | 分 | 证据 | 与基线 |
| --- | ---: | --- | ---: | --- | ---: |
| 复杂性控制 | 20 | 变化放大 | 3 | 动作/候选/失效/run mode 四处副本；本计划收口到 1 处（待验证） | |
| | 7 | 认知负荷 | 3 | 5 个包 + 新包；`selected_rewrite` 留在传输包 | |
| | 5 | 特例与信息泄漏 | 3 | 泄漏已定位且有收口路径，代码未实现 | |
| 模块深度与边界 | 20 | 接口成本/隐藏收益 | 3 | 第 6 轮把接口从「4 个函数 + 1 个 6 字段 struct」收成「1 个决策函数 `PlanFor` + 1 个 `Plan`」——设计更深，但**代码未实现，按待验证计分** | |
| | 6 | 信息隐藏 | 3 | D4/D7/D8 + 第 6 轮 D18/D19（动作语义与指令片段归一，堵住后门泄漏）——设计到位，**代码未实现** | |
| | 4 | 层次错位 | **2** | `selected_rewrite` 领域逻辑住在 `workspace` 传输包 | |
| | 3 | 耦合与透传 | 3 | `Request.Body` 复用既有载荷通道，无新增透传层 | |
| 演化式设计 | 15 | 结构不同的候选方案 | 5 | 13 项决策每项 2–3 个候选，记录在 §1 与 ADR | |
| | 5 | 增量与回退 | 4 | 纵切片提交序列；摘要版本化提供回退 | |
| | 5 | 持续重构 | 3 | 登记处缺失，自动约束缺位 | |
| 战略执行 | 15 | 设计投资与债务偿还 | 4 | 四轮 grilling + Ousterhout 依据 | |
| | 5 | 路线与依赖一致性 | 4 | 与 #138/#133 一致；登记处缺失扣分 | |
| | 4 | 变更局部性 | 3 | 扩所有权后变更面大 | |
| 一致性与显然性 | 15 | 领域术语/文档 | 4 | 术语表 + ADR + 本计划 | |
| | 3 | 契约与非显然注释 | 4 | `PlanFor` 接口评论已写入 §3.2 | |
| | 4 | 代码语义一致性 | 3 | 复用三值来源词汇；候选形态待归一 | |
| | 3 | 自动化约束 | **2** | 六条守护测试（词汇唯一性、许可位非分支、候选形态唯一、产出参数不进入安全边界、摘要唯一拥有者、执行方不自建动作语义）**尚不存在** | |
| 证据、反馈与恢复 | 15 | 未知发现 | 3 | G6 无 generation/adoption 类别 | |
| | 5 | 失败/权限/恢复测试 | 3 | 现有测试扎实，新增路径无 | |
| | 5 | CI/观测/回退 | 3 | 门禁强；本切片观测缺口未补 | |

维度得分：复杂性控制 12.0 / 模块深度与边界 11.2 / 演化式设计 12.0 / 战略执行 11.2 / 一致性与显然性 10.0 / 证据反馈与恢复 9.0 → **65.4 → 65**（基线 66，**-1**）。

**修改前评分：`F_R` LingDoc—仓库契合度 = 64/100（B）**。本轮设计调整后暂不宣称分数改善；待 PR-0 的迁移、守护测试和契约实现完成后重新取证。

| 维度 | 权重 | 二级指标 | 分 | 证据 |
| --- | ---: | --- | ---: | --- |
| 复杂性控制 | 20 | 变化放大 | 3 | 同上 |
| | 7 | 认知负荷 | 3 | 扩所有权后与其它五面的边界靠人工维护 |
| | 5 | 特例与信息泄漏 | 3 | 同上 |
| 模块深度与边界 | 20 | 接口成本/隐藏收益 | 3 | 同上 |
| | 6 | 信息隐藏 | 3 | 同上 |
| | 4 | 层次错位 | 3 | 新包层次正确；传输包错位仍在 |
| | 3 | 耦合与透传 | 3 | 同上 |
| 演化式设计 | 15 | 结构不同的候选方案 | 5 | 同上 |
| | 5 | 增量与回退 | 4 | 同上 |
| | 5 | 持续重构 | 3 | 登记处缺失 |
| 战略执行 | 15 | 设计投资 | 4 | 同上 |
| | 5 | 路线与依赖一致性 | 3 | #144 的依赖图与登记处均无机器可核对来源 |
| | 4 | 变更局部性 | 3 | 同上 |
| 一致性与显然性 | 15 | 领域术语/文档 | 4 | 同上 |
| | 3 | 契约与非显然注释 | 4 | 同上 |
| | 4 | 代码语义一致性 | 3 | 同上 |
| | 3 | 自动化约束 | **2** | 同上 |
| 证据、反馈与恢复 | 15 | 未知发现 | 3 | 同上 |
| | 5 | 失败/权限/恢复测试 | 3 | 同上 |
| | 5 | CI/观测/回退 | 3 | 同上 |

维度得分：12.0 / 11.2 / 12.0 / 10.2 / 10.0 / 9.0 → **64.4 → 64**（基线 69，**-5**）。

**修改前结论（保留作基线）**：本计划当时没有提高健康分，原因是切片尚未实现且扩所有权扩大了认知范围。本轮通过 PR-0、评审报告边界和迁移演练补上了部分可验证性要求，但是否改善健康分要等代码和测试完成后再判断。

**修改前两个 2/5 项的重构方向**（按「模块/边界 → 结构问题 → 重构动作 → 预期减少的变化放大或认知负荷 → 回归门槛」；本轮已把其中的守护测试要求前移到 PR-0）：

1. `internal/lingdoc/workspace/selected_rewrite.go` → 领域逻辑（候选持久化、失效规则、幂等）住在传输包 `workspace`，与同包的 HTTP 路由注册耦合 → 提取领域服务到独立包（`internal/lingdoc/rewrite` 或并入 `candidateadoption`），传输包只留适配 → 减少「改候选语义要动 HTTP 包」的变化放大 → 回归门槛：现有 `selected_rewrite_*_test.go` 全绿且 `workspace/http.go` 不再含候选字段 → **本轮范围外**，登记为后续项。
2. 动作族的无自动约束 → 六条守护测试（词汇唯一性、许可位非分支、候选形态唯一、产出参数不进入安全边界、摘要唯一拥有者、执行方不自建动作语义）不存在 → 在 `writingaction` 包旁写 AST 级守护测试，进 `scripts/ci` 或 Go 包测试；摘要那条扩 `operationcore/ownership_test.go` 的检查规则 → 减少「新增动作漏改一处」与「客户端改安全边界」的变化放大 → 回归门槛：六条变异（加第二处枚举、在采纳端加动作分支、把待核事项改回 `[]string`、把客户端参数写进 `OutputConstraints`、写出两步式自算摘要、在 host adapter 里自写动作指令或追加未声明的上下文项）各自让对应守护测试变红 → **PR-1 的交付物**。

**最高优先级治理动作**（按「变化放大范围 × 变化频率 × 边界关键性」排序）：

1. **补建并行面登记处**（另开票）：所有权无自动校验是当前唯一的**边界关键性**风险，且影响全部六个面 → 收益最大。
2. **三条守护测试**（本切片内）：直接保护动作语义的唯一性。
3. **`selected_rewrite` 领域服务归位**（后续项）：一次可观的知识归位。

## 10. 明确不做

- 不新增第三条 AI 写作管道。
- 不合并 `generation` 与 `selected_rewrite` 两条管道（保留各自实现，只共享词汇与候选形态）。
- 不改 `internal/lingdoc/workspacecore/model.go`（PF-3 独占）。
- 不改 `internal/lingdoc/workspacecore/working_copy.go`（只需调用 `ApplyRewrite`）。
- 不改 `internal/lingdoc/operationcore/*`（只用 `Request.Body` 与 `RequireHumanDecision`）。
- 不改 `frontend/src/views/lingdoc/Workspace.vue`（保留路径，最终集成另做）。
- 不做章节级 human/AI/AI辅助 标注（来源标注落在候选层）。
- 不做正文分段级来源标注。
- 不把 `selected_rewrite` 的领域逻辑从传输包搬出（后续项）。
- 不为 G6 接 generation/adoption 事件（归 PF-3 与 #145）。
- 不实现「自动创建/应用 ChangeSet」「自动确认」「自动导出」「自动删除用户文本」。
