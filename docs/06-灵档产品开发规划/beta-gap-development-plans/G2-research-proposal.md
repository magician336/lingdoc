# G2 调研智能体的研究提案：开发计划

> **Spec**：[#393](https://github.com/magician336/lingdoc/issues/393)（仓内节点 [`50-Spec-调研智能体研究提案.md`](../50-Spec-调研智能体研究提案.md)）
> **施工并行面**：PF-5（[#329](https://github.com/magician336/lingdoc/issues/329)，登记处 `status=claimed`、`claimed_by=magician336`）
> **阶段**：代码前门（验收矩阵已定，施工票已拆）
> **同时服务**：Spec [#135](https://github.com/magician336/lingdoc/issues/135)（调研任务与调研智能体）、[#125](https://github.com/magician336/lingdoc/issues/125)（调研智能体首轮闭环）——两者的关系按路径图「已知矛盾与待裁决」处理，本计划不合并也不裁剪它们。

## 0. 交付身份

| 项 | 内容 |
|---|---|
| 用户可见结果 | 调研智能体给出**服务端校验过的**研究提案：可编辑的调研任务草案 + 可追溯的候选来源 + 理由 + 缺口；坏提案响亮失败，缺提案不打断对话 |
| 范围内 | 研究提案契约（类型 + 解码器 + 三态 + 键常量）、purpose 由服务端按 agent 派生、提案随既有 turn 端点送达、候选来源的授权边界、前端把来源/理由/缺口分列展示、契约文件同步 |
| 范围外 | 助手侧那份「可编辑调研任务建议」的校验、上下文包的前端消费（PF-6）、检索侧（调研任务执行 / 导入候选 / 证据卡 / 研究主张）、那个巨型服务文件的重构、真实提供方链路 |
| 前置依赖 | 无（上下文包后端已在 `agent_research_model.go` 落地） |
| 可并行工作 | 本面内部**串行**（施工票共用 `agent_model.go` / `agent_action_service.go` / 新契约模块）；与 PF-3 / PF-4 / PF-6 路径集零重叠 |
| 真实消费者 | `workspacecore` 的 agent turn 服务 → `workspace` 的模型适配器 → 前端 `ResearchPanel.vue` |

## 1. 决策记录

每条都写明**框架依据**、**必须同时落地的配套条件**、**残留风险**（评审口径：只给结论不算核验）。

| # | 决策 | 框架依据 | 配套条件 | 残留风险 |
|---|---|---|---|---|
| D1 | 研究计划的形状由**一份服务端契约**拥有，提示词按它渲染 | ch05 信息隐藏：同一知识（形状与取值约束）现在既在提示词字符串里、又在 `ResearchTask` 类型里，两处各说各话 | 契约成**独立模块**（与既有两个模型输出契约同层同形），并登记进本面路径集，否则它无主 | 若只改契约不改提示词，两处仍会漂 ⇒ 守护必须同时断言「提示词不另写形状」 |
| D2 | 契约字段**沿用调研任务已有字段名**，不引入第二套词表 | ch17 一致性：前端的任务表单与存储的调研任务已经是这套名字 | 同义词**并入查询词**（每条一行），不加新字段 | 加新字段会改变此后每个调研任务的存储形状 ⇒ 代价大、收益小 |
| D3 | 提案在既有 turn 端点送达，**不新增端点** | ch04 深模块：端点少一个，幂等摘要少一处，客户端少一条路 | 提案键沿用前端已在采纳的 `task_card`；前端只多渲染三块内容 | 同一把钥匙两套强度（助手侧不校验）——**有意保留的残留**，写在 Spec 的 Implementation Decisions 第 5 条 |
| D4 | **purpose 由服务端按 agent 派生**，客户端不声明 | ch07 抽象错位：purpose 属于「这一轮答案的形状」，是服务端拥有的概念，不是客户端自由填的字符串 | purpose / agent 词汇表收成**服务端常量**；适配器不再比裸字符串 | purpose 在 research 这一格上与 agent 同义 ⇒ 词汇表里两者必须各自有明确职责（角色 vs 答案形状），否则就该合并成一个词 |
| D5 | 候选来源**必须可追溯**到该轮授权上下文，对不上**拒绝**（不降级） | ch10 把错误设计掉：编造出处与缺注解是两种失败 | 提案解码时拿该轮上下文的 `AgentSourceRef` 逐个比对，`model_use_allowed` 为假的不算 | 无权 / 撤权 / 跨项目资料**根本不在上下文里**，所以第一道防线是上下文构造，第二道才是解码比对 |
| D6 | 提案三态：**present / absent / invalid**；缺提案不拒，给了非法才拒 | 照抄既有「影响说明」契约的做法（缺注解不丢草稿，非法注解才拒） | 拒的是**提案**，不是整轮的自然语言回复 | 拒提案但保留回复 ⇒ 用户看到「有话说、没建议」，必须读得懂 |
| D7 | 提案**永远是候选**，不自动升格 | 仓内既有红线（AI 不写正式对象 / 推荐不升格为证据） | 升格路径（保存调研任务、确认导入候选、确认证据卡 / 研究主张）全部走既有显式确认与人工门禁 | 调研任务的创建仍需用户显式保存 ⇒ 提案要能被看见才会被采纳，前端不渲染就等于没有 |
| D8 | 新契约**不进那个巨型服务文件** | ch02 变化放大：`agent_action_service.go` 104KB 已装了七件事 | 契约成独立模块；服务文件只做调用 | workspacecore 的「契约文件」已不止一个 ⇒ 将来合并与否是另一件事（ch09），本面不做 |
| D9 | 全仓统一说「**研究提案**」 | ch17 一致性：契约与面板说的是「调研提案」，计划、代码与 Spec 说的是「研究提案」——同一个东西两个名字，读者得自己猜哪个对 | 契约三份文件（openapi / scenarios / g7）、调研面板与前端测试全部改成「研究提案」，`GLOSSARY.md` 收一条；两个词不许并存 | 「调研智能体」这个角色名不动——它是角色，不是这份草案 |
| D10 | 公开契约里**助手那张卡单列自己那份形状** | ch10 把错误设计掉：契约承诺运行时不做的事，比不写更坏——`AgentSuggestions.task_card` 统一 `$ref` 到带「查询词至少两条」的 `ResearchProposal`，而助手卡不经提案契约校验、允许只有一条 | `task_card` 声明成 `ResearchProposal` 与 `AssistantTaskSuggestion` 两者的并集；助手那份不写条数与长度约束，也不禁止额外字段 | 并集意味着「按来源收紧」这条规则只能由服务端保证，schema 表达不了；调研智能体那一轮的查询词下限因此仍只由服务端把住（解码器那条线） |

## 2. 代码事实基线

### 2.1 现状（2026-10-22 逐条核实）

- `ModelRequest.Purpose` 目前只有两个取值在用：`conversation`（服务端在 turn 服务里**硬编码**）与 `rule_suggestion`（模板建议路径）。`change_draft` 是**模型输出的键**，不是 purpose。
- 调研智能体今天只到「按 `request.Agent == "research_agent"` 换一段提示词」这一步；模型交回的 `task_card` **没有任何解码器**——对比之下，`change_draft` 有解码器、`affected_explanation` 有解码器。
- `task_card` 的形状只存在于**提示词字符串**里（目标 / 问题类型 / 范围 / 排除项 / 查询词至少两条 / 预期产物），与服务端 `ResearchTask` 的字段**逐字相同但无类型联系**。
- 前端**已经在消费这个未校验的形状**：调研面板的采纳路径把 `turn.suggestions?.task_card` 的字段逐个 `||''` 灌进任务表单；前端类型把 `task_card` 声明成调研任务保存输入的部分。
- 调研的**执行侧已齐备**：`ResearchTask` / `ResearchRun` / `ResearchResult` / `SearchResearch` / 导入候选 / 证据卡 / 研究主张都在；检索结果没有来源定位或正文摘要时会被**降级**为「未读检索摘要」。

### 2.2 可直接复用的接缝

| 接缝 | 位置 | 本面怎么用 |
|---|---|---|
| turn 端点 | `POST /projects/:projectId/agent-sessions/:sessionID/turns` | 提案随这一轮送达，**不新增端点** |
| turn 存储 | `AgentTurn.Suggestions`（JSON） | 提案进 `suggestions`，与既有 `change_draft` / `affected_explanation` 同处 |
| 模型适配器 | `workspace` 包的 agent 模型 | 按 purpose 选提示词；按 purpose 选解码器 |
| 上下文包 | `BuildAgentContext` → `AgentContextPackage`（含 `SourceRefs`） | 候选来源的可追溯依据 |
| 调研任务保存 | `POST /projects/:projectId/research-tasks` | 提案被用户编辑后落成调研任务的既有路径 |

### 2.3 确认不存在的东西

- **不存在**「模型产出研究提案」的形状校验：任何 `task_card` 都会被前端照单吸收。
- **不存在** purpose 词汇的单一拥有者：服务与适配器各自比字符串。
- **不存在** 提案与「可被确认的对象」之间的隔离：今天靠「前端不渲染就没有」，而不是靠服务端。

## 3. 目标设计

### 3.1 模块边界与文件所有权

本面路径集（11 条，以登记处为准）：

| 路径 | 本面在它上面做什么 |
|---|---|
| `internal/lingdoc/workspacecore/research_proposal.go`（**新建**） | 研究提案契约：类型 + 解码器 + 键常量 + 三态常量 |
| `internal/lingdoc/workspacecore/research_proposal_test.go`（**新建**） | 契约的守护与三态用例 |
| `internal/lingdoc/workspacecore/agent_action_service.go` | purpose 按 agent 派生；提案随 turn 送达；拒绝路径；调研任务与提案共用同一份边界值 |
| `internal/lingdoc/workspacecore/agent_research_model.go` | 只在需要时补上下文侧的常量，不放形状；调研任务自己的边界值声明在这里 |
| `internal/lingdoc/workspacecore/change_draft_action.go` | #404 的词汇归一：助手草稿校验的两处 agent 比较换成服务端常量 |
| `internal/lingdoc/workspace/agent_model.go` | 适配器：按 purpose 选提示词与解码器；不再比裸字符串 |
| `internal/lingdoc/workspace/template_governance_runtime.go` | #404 的词汇归一：rule_suggestion 用途的两处 agent / purpose 比较换成服务端常量 |
| `frontend/src/api/lingdoc/agentApi.ts` / `.test.js` | 提案的类型与客户端 |
| `frontend/src/views/lingdoc/ResearchPanel.vue` / `.test.js` | 提案的来源 / 理由 / 缺口分列展示 + 采纳路径；提案块只对调研智能体的轮次渲染 |

### 3.2 领域级接口契约（研究提案契约）

契约的形状（与调研任务字段同名，额外三项是**加**上去的）：

```
task_card: {
  goal, question_type, scope, exclusions[], queries[]（至少两条）, expected_output
  candidate_sources[]: { source_id, reason }
  reasons[]: 为什么这样建议
  gaps[]: { description, expected_evidence }
}
```

解码规则（每条都有反例）：

| 规则 | 违例时的结果 |
|---|---|
| 未知字段 | 拒 |
| 必填缺失（目标 / 范围 / 预期产物） | 拒 |
| 查询词少于两条 | 拒 |
| 载荷过大 | 拒 |
| 理由 / 缺口超长 | 拒 |
| 候选来源的 `source_id` 不在该轮上下文的 `SourceRefs` 里 | 拒 |
| 候选来源的 `model_use_allowed` 为假 | 拒（它本就不该在上下文里） |
| 提案整段没给 | **不拒**：本轮没有提案，自然语言回复仍在 |

三态：`present`（有且合法）、`absent`（没给）、`invalid`（给了但不合法 ⇒ 拒）。与「影响说明」契约同形。

### 3.3 purpose 由服务端派生

- 服务端按会话的 agent 派生这一轮的 purpose：调研智能体 ⇒ 研究提案；任务助手 ⇒ 现有对话。
- purpose / agent 收成服务端常量（唯一拥有者）；适配器引用常量，不再比裸字符串。
- **请求形状与幂等摘要不变** ⇒ 旧幂等键仍可重放，客户端不新增字段。

### 3.4 授权边界

- 第一道：上下文构造 —— 无权 / 已撤权 / 跨项目资料**不进** `SourceRefs`，模型看不到。
- 第二道：解码比对 —— 提案引用的 `source_id` 必须逐个对得上 `SourceRefs`，对不上即拒。
- 两道都在服务端，**不依赖前端不渲染**。

### 3.5 提案永远是候选

- 提案不创建调研任务（用户显式保存才是）、不创建导入候选、不确认证据卡 / 研究主张。
- 检索结果的等级（候选证据 / 背景线索 / 发现条目 / 未读检索摘要）与**模型提议的来源**是两回事，不共用一套词表。

### 3.6 前端交付形态

- 采纳路径**不变**：提案的任务形状部分照旧灌进可编辑表单（前端类型本来就是「调研任务保存输入的部分」）。
- 新增一块展示：**候选来源**（带各自理由）、**理由**、**缺口**；被拒的提案不会到达前端。

## 4. 领域不变量

1. **提案的形状只有一份拥有者**（契约模块）；提示词与前端类型都从它派生。
2. **purpose 与 agent 词汇只有一份拥有者**（服务端常量）。
3. **候选来源必须可追溯**到该轮授权上下文（对不上即拒，不降级）。
4. **提案永远不自动升格**（调研任务 / 导入候选 / 证据卡 / 研究主张都要显式确认）。
5. **缺提案不打断对话**（自然语言回复仍在）；**非法提案响亮失败**（读得懂的错误码）。
6. **请求形状与幂等摘要不变**（purpose 是服务端派生的，客户端不声明）。
7. **零写入原则**：提案的校验与拒绝发生在任何写入之前。

## 5. 验收矩阵

| 验收项 | 用户可见入口 | 领域不变量 / 事务边界 | 成功 | 失败 / 权限 / 恢复 | 持久化与并发 | 测试或运行证据 | 状态 |
|---|---|---|---|---|---|---|---|
| A1 形状只有一份拥有者 | 服务端契约模块 | 不变量 1 | 契约模块是形状的唯一声明处 | 形状出现在别处 ⇒ 守护红 | 无 | 守护测试（AST 扫提示词不另写形状） | PASS：契约模块 internal/lingdoc/workspacecore/research_proposal.go 是唯一定义处（结构体 `ResearchProposal` 与字段名表 `researchProposalFields` 及其两张子表）；**字段名表收在包内私有**，交出去的是副本（`researchProposalFieldSet`），外部拿不到能改写的切片；守护用例 TestResearchProposalShapeHasOneOwner 用 AST 扫 internal/lingdoc 全树找同形状结构体、并扫适配器提示词的字面量；变异验证：把形状写回提示词、另建一份同形状结构体，守护两次都红，复原后 PASS；go test ./internal/lingdoc/workspacecore/ -run ResearchProposal -count=1 PASS |
| A2 提示词按契约渲染 | 调研智能体回答 | 不变量 1 | 提示词里的字段名与契约逐字一致 | 提示词与契约不一致 ⇒ 红 | 无 | 守护测试 + 适配器用例 | PASS：**本轮复核修复（A1）**——**一种形状、两种渲染**：适配器 internal/lingdoc/workspace/agent_model.go 的两处提示词都按契约模块渲染，不再自带形状——调研智能体那一处按 core.ResearchProposalKey 与 core.ResearchProposalShapeText()（整份提案：6 个任务字段 + 候选来源 / 理由 / 缺口），任务助手那一处按 core.ResearchProposalKey 与 core.ResearchProposalTaskShapeText()（**只有 6 个任务字段**，助手那张同名卡片不走提案契约）；两种渲染互补（任务渲染不带提案那三列、提案渲染带全九列），字段名与「至少两条查询、同义词并入其中」的规则仍只有一处声明；守护用例 TestResearchProposalShapeHasOneOwner 断言适配器两处提示词各自渲染对应的那一份（两种渲染调用各不少于 1 次）、两种渲染互补；适配器用例 TestResearchPromptAsksForTheWholeProposalShape（调研提示词按契约渲染整份提案形状）、TestAssistantPromptIsByteIdenticalToTheTextBeforeTheProposalContract（助手提示词与 #401 之前的原文逐字节相同）；go test ./internal/lingdoc/workspacecore/ -run ResearchProposal -count=1 PASS，go test ./internal/lingdoc/workspace/ -run 'AssistantPrompt\|ResearchPrompt' -count=1 PASS |
| A3 purpose 由服务端派生 | 既有 turn 端点 | 不变量 2、6 | 调研智能体的一轮产出提案 | 客户端无法要求别的组合 | 请求形状不变 ⇒ 幂等摘要不变 | 应用服务层接缝（`SendAgentTurn`） | PASS：**#404 交付**——purpose 与 agent 收成服务端常量（`internal/lingdoc/workspacecore/agent_research_model.go`：`AgentTaskAssistant`/`AgentResearch`/`AgentConstraint` 与 `AgentPurposeConversation`/`AgentPurposeRuleSuggestion`/`AgentPurposeResearch`，外加 `AgentPurposeForAgent(agent)` 按会话的 agent 派生 purpose），服务端 `internal/lingdoc/workspacecore/agent_action_service.go` 的 `SendAgentTurn` 用 `AgentPurposeForAgent(started.Agent)` 取代硬编码的 `"conversation"`；**客户端不新增请求字段**：`AgentTurnInput` 一个字段没动，幂等摘要逐字不变（`operationcore.RequestHash` 的钉死值 `481e6ee7…c930f`），旧键仍可重放；用例 TestAgentPurposeIsDerivedFromTheSessionAgent（调研智能体那一轮拿到 `research_proposal`、任务助手仍是 `conversation`）、TestAgentTurnRequestDigestIsUnchangedAndAnIssuedKeyStillReplays（请求里没有 purpose/agent 字段、摘要钉死、同键重放是同一轮、模型只被问一次、轮次表仍是一轮）；**接缝口径**：这批用例走的是应用服务层（`f.svc.SendAgentTurn`，真实 SQLite），**没有过 HTTP handler**——仓里本来就没有 turn 端点的 HTTP 层用例；go test ./internal/lingdoc/workspacecore/ -run 'AgentPurpose\|AgentTurnRequestDigest' -count=1 PASS |
| A4 合法提案随 turn 送达 | 既有 turn 端点 | 不变量 1、3 | 提案带来源 / 理由 / 缺口出现在该轮建议里 | —— | 提案进 `suggestions`，与既有键同处 | 应用服务层接缝（`SendAgentTurn`） | PASS：#401 交付合法提案随既有 turn 端点送达（形状与服务端契约逐字一致；不新增端点，请求形状与幂等摘要不变）；#402 交付**候选来源**这一列（可追溯，逐条对过该轮授权上下文，见 A7 / A8）；**#403 交付理由与缺口两列**——理由说清整份提案为什么这么定，缺口分「缺什么」与「期望补什么」，缺了任何一半或超长即整份拒绝；实现 internal/lingdoc/workspacecore/agent_action_service.go 的 SendAgentTurn 调用 researchProposalJudgement 校验，判据落在契约模块 internal/lingdoc/workspacecore/research_proposal.go 的 DecodeResearchProposal（服务层本票未改）；用例 TestResearchProposalReachesTheTurnAndCreatesNothing、TestResearchProposalCarriesItsCandidateSourcesToTheTurn、TestResearchProposalCarriesItsReasonsAndGapsToTheTurn、TestResearchProposalWithOverlongReasonOrGapIsRefusedWithoutLosingTheAnswer；go test ./internal/lingdoc/workspacecore/ -run ResearchProposal -count=1 PASS |
| A5 缺提案不打断对话 | 既有 turn 端点 | 不变量 5 | 没有提案时自然语言回复仍在，该轮没有提案 | —— | 同上 | 应用服务层接缝（`SendAgentTurn`） | PASS：**#404 交付**——模型这一轮没给建议 ⇒ 那一轮照样 `completed`、自然语言回复仍在、该轮没有提案，服务端 `internal/lingdoc/workspacecore/research_proposal.go` 的 `researchProposalJudgement` 走 absent 分支（不拒、不改写）；**三态在产品侧由轮次自身的字段表达**（没有第二个复判入口）：present ⇒ `suggestions` 里带着提案；absent ⇒ 没有提案、也没有失败码、回复仍在；invalid ⇒ `failure_code=invalid_proposal`、没有提案、回复仍在；用例 TestAbsentResearchProposalKeepsTheReplyAndCarriesNoProposal（应用服务层接缝，假模型：只给回复不给建议 ⇒ status=completed、reply 在、建议里没有提案、失败码为空）、TestAStoredTurnSaysItsProposalStateThroughItsOwnFields（三种形态都走生产路径，逐条读轮次自身的字段）；前端用例 没有提案的那一轮照样有答复，面板上没有建议；go test ./internal/lingdoc/workspacecore/ -run 'ResearchProposal\|AStoredTurn' -count=1 PASS，cd frontend && npm test PASS（1636 pass / 0 fail / 1 skipped，head = 9cc7ff4e） |
| A6 非法提案响亮失败 | 既有 turn 端点 | 不变量 5、7 | —— | 非法提案 ⇒ 读得懂的失败，提案不送达，零写入 | 失败路径零写入 | 应用服务层接缝（`SendAgentTurn`）+ 解码器用例 | PASS：**#404 交付**——**拒的是提案，不是整轮的问答复**（D6）：模型给了但不可信的提案被整份拒绝、绝不落进 `Suggestions`，而那一轮仍 `completed`、**自然语言回复仍在**，`failure_code=invalid_proposal` 读得懂地说明「本轮的建议未通过服务端校验」；实现落在契约模块 `internal/lingdoc/workspacecore/research_proposal.go` 的 `researchProposalJudgement`（invalid 分支）+ `researchProposalWithoutProposal`（把那份提案从模型回答里摘掉，其余键原样留着）与服务文件 `internal/lingdoc/workspacecore/agent_action_service.go` 的 `SendAgentTurn`（`case proposalState == ResearchProposalStateInvalid` 只记失败码，不清 reply、不清 suggestions）；用例 TestInvalidResearchProposalKeepsTheReplyAndLosesTheProposal（一条查询 / 未知字段 / 缺目标 ⇒ 回复仍在、建议里没有提案、失败码可读、四个列表仍为空）、TestResearchProposalWithOverlongReasonOrGapIsRefusedWithoutLosingTheAnswer、TestResearchProposalCitingASourceOutsideTheTurnIsRefused、TestResearchProposalCitingAnotherProjectsSourceIsRefused、TestAStoredTurnSaysItsProposalStateThroughItsOwnFields（三种形态都走生产路径，逐条读轮次自身的字段）；前端用例 坏提案不连累那一轮的问答复：答复仍在，面板上没有建议；go test ./internal/lingdoc/workspacecore/ -run 'ResearchProposal\|AStoredTurn' -count=1 PASS，cd frontend && npm test PASS（1636 pass / 0 fail / 1 skipped，head = 9cc7ff4e） |
| A7 候选来源可追溯 | 既有 turn 端点 | 不变量 3 | 提案引用的来源都在该轮上下文里 | 引用上下文外的 id ⇒ 拒绝 | 零写入 | 应用服务层接缝（`SendAgentTurn`，撤权 / 跨项目各一） | PASS：候选来源逐条对过**这一轮**的授权上下文——契约模块 internal/lingdoc/workspacecore/research_proposal.go 的 `DecodeResearchProposal(raw, pack)` 与 `validateResearchProposalCandidateSources` 对不上即拒（不降级、不悄悄丢掉那一条）；turn 端点侧 internal/lingdoc/workspacecore/agent_action_service.go 的 `SendAgentTurn` 把 `pack` 传进 `researchProposalJudgement`，非法提案整份被拒、绝不落进 Suggestions，而那一轮仍 `completed`、答复仍在、`failure_code=invalid_proposal`（零写入）；用例 TestResearchProposalAcceptsCandidateSourcesFromTheAuthorizedContext、TestResearchProposalRefusesCandidateSourceOutsideTheContext、TestResearchProposalRefusesCandidateSourceWithoutModelUsePermission、TestResearchProposalRefusesBrokenCandidateSource（理由超长/无理由/无来源 id/多字段）、TestResearchProposalCarriesItsCandidateSourcesToTheTurn、TestResearchProposalCitingASourceOutsideTheTurnIsRefused；go test ./internal/lingdoc/workspacecore/ -run ResearchProposal -count=1 PASS |
| A8 无权与跨项目资料不进上下文 | 既有 turn 端点 | 不变量 3 | 上下文里没有它们，模型看不到 | 撤权后**新一轮**取不到该来源 | 既有上下文构造路径 | 上下文构造用例 | PASS：两道防线都在服务端，不依赖前端不渲染——**第一道是上下文构造**：撤权的资料带 `model_use_allowed=假` 进不了这一轮可用来源（`SendAgentTurn` 建上下文后即 `ErrConsentRequired`，模型根本没被问）、别的项目的资料在读取时不可读（进不了 `SourceRefs`）；**第二道是解码逐条比对**。**实测口径（如实记）**：证据只覆盖**新一轮**取不到该来源（`ErrConsentRequired`、模型 0 次调用）——**已存下来的旧轮次仍读作 present**（它带着自己当时那份上下文，撤权不会回头改写它）；**撤权对已存提案的实际生效点在调研任务那条路径**：提案被用户保存成调研任务时（`SaveResearchTask` → `CheckAssetScope`）与该任务运行时（`RunResearchTask` → `CheckAssetScope`，agent_action_service.go:501）会重新核对资料范围，撤权后运行落到 `stale` + `failure_code=asset_scope_changed`，检索一次也没跑。用例 TestResearchProposalCitingARevokedSourceIsRefused（撤权 ⇒ `ErrConsentRequired`，模型 0 次调用；假模型**真的装进了 `agentRuntime.Model`**（并断言它就是这一轮会去问的模型），会话先建好，所以撤权那道闸是这一轮与模型之间**唯一**的拦阻——拿掉它模型就会被问 1 次、用例随即红，「模型 0 次调用」因此是一次实测而不是恒真断言）、TestRevokedSourceStopsTheRunOfAProposalThatBecameAResearchTask（提案存成调研任务后撤权 ⇒ 运行 stale + asset_scope_changed、检索 0 次调用）、TestResearchProposalCitingAnotherProjectsSourceIsRefused（跨项目资料读不到、进不了上下文；引用它的提案被拒）、TestResearchProposalRefusesCandidateSourceWithoutModelUsePermission（上下文里有但未许可 ⇒ 拒）；go test ./internal/lingdoc/workspacecore/ -run 'ResearchProposal\|RevokedSource' -count=1 PASS |
| A9 提案不自动成为调研任务 | 调研任务列表 | 不变量 4 | 提案送达后任务列表仍为空 | —— | 用户显式保存才建任务 | 应用服务层接缝（`SendAgentTurn`，断言列表为空） | PASS：用例 TestResearchProposalReachesTheTurnAndCreatesNothing 在提案送达后断言 ListResearchTasks 为空（提案只是候选）；go test ./internal/lingdoc/workspacecore/ -run ResearchProposal -count=1 PASS |
| A10 提案不自动成为导入候选 / 证据卡 / 研究主张 | 各列表 | 不变量 4 | 四个列表都不因提案而变 | —— | 既有确认门禁不变 | 应用服务层接缝（`SendAgentTurn`）+ 既有门禁回归 | PASS：用例 TestResearchProposalReachesTheTurnAndCreatesNothing 与 TestInvalidResearchProposalKeepsTheReplyAndLosesTheProposal 逐个断言调研任务、导入候选、证据卡、研究主张四个列表都不因提案而变；go test ./internal/lingdoc/workspacecore/ -run ResearchProposal -count=1 PASS |
| A11 查询词至少两条 | 既有 turn 端点 | 不变量 5 | 两条以上被接受 | 一条 ⇒ 拒绝 | 零写入 | 解码器用例 | PASS：硬要求落在服务端——DecodeResearchProposal 拒绝少于两条、空查询词与超长载荷；用例 TestResearchProposalRefusesFewerThanTwoQueries 与 TestInvalidResearchProposalKeepsTheReplyAndLosesTheProposal（少一条查询词 ⇒ 那份提案整份被拒、不送达，零写入）；go test ./internal/lingdoc/workspacecore/ -run ResearchProposal -count=1 PASS |
| A12 同义词并入查询词 | 提案形状 | 不变量 1 | 同义词作为额外查询词 | —— | 无 | 解码器用例 + 契约文件 | PASS：同义词并入 queries、不新增字段——契约的形状渲染写明「同义词并入其中」；用例 TestResearchProposalAcceptsLegalShapeAndKeepsSynonymAsExtraQuery 与 TestResearchProposalReachesTheTurnAndCreatesNothing 断言同义词作为第三条查询词原样到达；契约正例见 openapi.json 的 synthetic_research_proposal 与 g7-writing-contract.json 的 research-proposal-valid；两个契约校验器 PASS |
| A13 前端分列展示 | 调研面板 | 不变量 1 | 来源 / 理由 / 缺口在可编辑草案旁可见 | 被拒的提案不出现 | —— | 前端组件用例 | PASS：#401 交付提案本体在可编辑草案旁逐项可见（目标、问题类型、范围、排除项、查询词、预期产物）并标出模型与运行模式，被拒的提案不出现；#402 交付**候选来源与各自理由分列展示**（面板按来源 id 与理由两列渲染，服务端拒掉的提案整块不出现）；**#403 交付理由与缺口两列分列展示**——理由一条一行，缺口分「缺什么」与「期望补什么」两列，与候选来源列并列，三者都不揉成一段话；采纳路径不变（理由与缺口不灌进可编辑表单，它们是给用户看懂思路与盲区用的）；前端用例 提案的候选来源与各自理由在面板上分列展示，来源与理由不是一段话…、提案的理由与缺口在面板上分列展示，缺口分缺什么与期望补什么…、被服务端拒绝的提案在面板上不出现任何候选来源…、被服务端拒绝的提案在面板上不出现理由与缺口…（frontend/src/views/lingdoc/ResearchPanel.test.js，真实编译模板渲染）；cd frontend && npm test PASS（1636 pass / 0 fail / 1 skipped，head = 9cc7ff4e）、npm run type-check PASS |
| A14 前端吃的是校验过的草案 | 调研面板采纳路径 | 不变量 1、3 | 采纳路径灌入的是服务端校验过的形状 | —— | —— | 前端组件用例 | PASS：frontend/src/api/lingdoc/agentApi.ts 的 task_card 类型改为 ResearchProposal（服务端契约的镜像），采纳路径灌入的是校验过的草案，被拒的提案不进入表单；前端用例 服务端校验过的提案放进可编辑表单… 与 被服务端拒绝的提案不进入表单…；cd frontend && npm test PASS（1636 pass / 0 fail / 1 skipped，head = 9cc7ff4e） |
| A15 出处可读 | 既有 turn 端点 | 不变量 4 | 提案带着模型与运行模式 | —— | 复用 turn 既有字段 | 应用服务层接缝（`SendAgentTurn`） | PASS：提案复用 turn 既有的 model_id 与 run_mode（不新增字段），面板在提案块里显示二者，演示不冒充真实证据；用例 TestResearchProposalReachesTheTurnAndCreatesNothing 断言 turn.ModelID 与 turn.RunMode，前端用例 服务端校验过的提案放进可编辑表单…；go test 与 cd frontend && npm test 均 PASS |
| A16 助手侧行为不变 | 既有 turn 端点 | 不变量 1 | 助手的对话、影响说明、变更草稿逐字不变 | —— | 同上 | 既有守护全绿 | PASS：**#404 交付 + 本轮复核修复**——本票只动**调研智能体**那一条路：契约模块 `internal/lingdoc/workspacecore/research_proposal.go` 的 `researchProposalJudgement` 先判 agent（`agent != AgentResearch` 即不判），助手侧那张同名卡片按自己的规则走、不受本契约约束；助手的 `change_draft` 与 `affected_explanation` 两个既有契约的实现一个字没改，适配器里那几处比较只是把字面量换成同一批服务端常量（值不变 ⇒ 提示词不变）；**本轮复核修掉了 A1：形状拆成任务卡渲染与提案渲染两种，助手提示词渲染出的文本与 #401 之前的原文逐字相同**（queries 那段仍是「至少两项」，没有「（同义词并入其中）」，也没有候选来源 / 理由 / 缺口三列）——「助手侧逐字不变」此前是假断言，现在有逐字节比对的用例撑着。用例 TestAssistantPromptIsByteIdenticalToTheTextBeforeTheProposalContract（助手系统提示词与 #401 之前的原文逐字节相同，且不含提案那三列）、TestResearchPromptAsksForTheWholeProposalShape（调研提示词按契约渲染整份提案形状）、TestAssistantTaskSuggestionIsNotJudgedByTheProposalContract（助手那条只带一条查询的卡片仍 `completed`、仍带自己的卡片、失败码为空）；既有守护全绿：go test ./internal/lingdoc/workspace/ -count=1 PASS（含 agent_model_test.go、agent_assistant_explanation_boundary_test.go、agent_change_draft_action_test.go、agent_affected_groups_test.go）、go test ./internal/lingdoc/workspacecore/ -count=1 PASS |
| A17 契约文件同步 | OpenAPI / 场景 / G7 契约 | 不变量 1 | 三份契约含提案形状与反例 | 校验器红 | —— | 两个契约校验脚本 | PASS：本票补三份契约的正例——openapi.json 增 ResearchProposal 形状与 POST/GET turns 的 synthetic_research_proposal，scenarios.json 增场景 G2-RP-01，g7-writing-contract.json 增 ResearchProposal 形状与 research-proposal-valid；**#402 补上候选来源的形状与它的反例**——openapi.json 的 ResearchProposal 增 candidate_sources、增反例 synthetic_research_proposal_refused（失败码 invalid_proposal、无 suggestions），scenarios.json 增场景 G2-RP-02（引用上下文外的资料被拒），g7-writing-contract.json 增 ResearchProposalWithinTurnContext 与反例 research-proposal-out-of-context-source-rejected；**#403 补上理由与缺口的形状与超长的反例**——openapi.json 的 ResearchProposal 增 reasons / gaps（各带长度与条数上限）、增反例 synthetic_research_proposal_refused_oversized_reason，scenarios.json 增场景 G2-RP-03（理由或缺口超长被拒），g7-writing-contract.json 的 ResearchProposal 与 ResearchProposalWithinTurnContext 增 reasons / gaps、增反例 research-proposal-oversized-reason-rejected 与 research-proposal-gap-without-what-would-fill-it-rejected；**#404 补齐其余契约反例与三种形态**——openapi.json 增 absent 形态的 synthetic_research_proposal_absent 与形状非法形态的 synthetic_research_proposal_refused_invalid_shape，两份 refused 例子改写成新语义（status=completed、reply 留着、suggestions 里没有提案、failure_code=invalid_proposal），AgentTurn 与 ResearchProposal 的描述写清三态与「failure_code 与 completed 并存」的含义；scenarios.json 的 G2-RP-02 / G2-RP-03 改写成新语义（答复仍在、建议里没有提案），新增 G2-RP-04（这一轮没给建议，答复照样在）与 G2-RP-05（形状非法的提案被整份拒绝）；g7-writing-contract.json 增 ResearchProposalAbsentTurn 与 ResearchProposalRefusedTurn 两个形状与四个例子（research-proposal-absent-turn、research-proposal-refused-turn、research-proposal-unknown-field-rejected、research-proposal-missing-required-field-rejected），三份文件都写明「助手侧那张同名卡片不受本契约约束」这条**有意保留的残留**；**残留（如实记）**：g7 契约的 JSON Schema 子集表达不了 minItems 与整份载荷上限，因此「查询词至少两条」与「载荷过大」这两条规则只落在服务端解码器里（用例 TestResearchProposalRefusesFewerThanTwoQueries、TestResearchProposalRefusesOversizedPayload），g7 的描述里写明了这一点；python -X utf8 docs/08-本轮实施方案/contracts/validate_artifacts.py --source-root . PASS，python -X utf8 docs/08-本轮实施方案/contracts/validate_g7_contract.py PASS |
| A18 守护：词汇归一 | 服务端 | 不变量 2 | 适配器引用常量 | 适配器出现裸字符串比较 ⇒ 红 | 无 | 守护测试 | PASS：**#404 交付**——词表归到服务端常量（`internal/lingdoc/workspacecore/agent_research_model.go`），适配器 `internal/lingdoc/workspace/agent_model.go` 的六处比较、服务 `internal/lingdoc/workspacecore/agent_action_service.go` 的会话 agent 校验与 `internal/lingdoc/workspacecore/change_draft_action.go` 的助手草稿校验、`internal/lingdoc/workspace/template_governance_runtime.go` 的 rule_suggestion 用途，都改成引用常量；**守护用例 TestAgentVocabularyHasOneOwner** 用 AST 扫 `internal/lingdoc` 全树（比较、赋值、switch 三种写法），只看「被比较/被赋值的那一侧是不是 agent 或 purpose」，所以导入候选自己的 `conversation` 选择类型与出处的 `conversation` 来源类型不会误报；**变异验证**：故意把适配器写成 `request.Agent == "task_assistant"` ⇒ 守护红（`..\workspace\agent_model.go: compares Agent`）；故意把 rule_suggestion 写回字面量 ⇒ 守护红（`..\workspace\template_governance_runtime.go: assigns Purpose`）；两处复原后 PASS；go test ./internal/lingdoc/workspacecore/ -run TestAgentVocabularyHasOneOwner -count=1 PASS |

**矩阵状态说明**：18 行**全部有据，没有 `TODO`**。施工票逐行把它们改成指向该 PR 的证据。矩阵在**当前范围内**不得出现 `TODO` / `NOT RUN` / `BLOCKED` 才放行合并；`N/A`、`WARNING` 与已链接验收票的 `PASS (follow-up)` 不阻塞。

**#401 已落的行**：A1、A2、A4、A9、A10、A11、A12、A13、A14、A15、A17。**#402 已落的行**：A7、A8（候选来源可追溯），以及 A4 / A13 里的**候选来源**这一列。**#403 已落的行**：A4 / A13 里的**理由与缺口**两列（A4、A13 两行的状态列已改为本票证据）。**#404 已落的行**：A3（purpose 由服务端派生、旧幂等键仍可重放）、A5（缺提案不打断对话）、A6（非法提案响亮失败、但只拒提案不连累答复）、A16（助手侧逐字不变）、A17（三份契约的反例与三种形态补齐）、A18（词汇归一与守护）。**面已收口**：PF-5 的四张施工票（#401 / #402 / #403 / #404）都落地了，矩阵 18 行全部有据。

**本轮评审修复（2026-10-23）改动的行**：A1 / A2（助手提示词逐字不变 + 两种渲染）、A3 / A5 / A6（三态只有一处实现，证据改成实测口径）、A8（撤权的实际生效点写实 + 新增用例）、A10 / A11（引用不存在的用例名改成真实存在的）、A13 / A14 / A15（前端实测数字与 head）、A16（补上逐字节比对用例）、A17（状态标签去掉陈旧的 follow-up 指针）。**未改动的行**：A4、A7、A9、A12、A18（只改了接缝口径的措辞，未动证据本身）。

**本面仍未跑的东西（如实记）**：真实提供方链路 **NOT RUN**——本面只走 mock / 假模型档（`real_api_fake_model`），真实模型下的提案质量未知，另票。

## 6. 交付切片与 PR 序列

四张施工票，**串行**。每张都是一条**窄但完整**的路径（形状 → 服务端 → 界面 → 测试），**做完即可演示**；契约同步随票走，不另设「只改文档」的横切票。

| 票 | 切片（端到端） | Blocked by | 覆盖矩阵行 | 做完能演示什么 |
|---|---|---|---|---|
| [#401](https://github.com/magician336/lingdoc/issues/401) | 一条最小研究提案从模型走到可编辑的调研任务 | 无（可立即开工） | A1、A2、A4、A9、A10、A11、A12、A13、A14、A15、A17 | 面板出现**可编辑**草案；用户保存后成为调研任务；坏草案（未知字段 / 缺必填 / 查询词少于两条）在服务端被拒、零写入 |
| [#402](https://github.com/magician336/lingdoc/issues/402) | 提案推荐的来源必须是我有权看的 | #401 | A7、A8 | 候选来源与理由分列展示；引用了没拿到的资料 ⇒ 拒绝、零写入、界面上不出现 |
| [#403](https://github.com/magician336/lingdoc/issues/403) | 提案说清为什么这样建议、还缺什么 | #402 | A13 | 理由与缺口分列展示；超长 ⇒ 拒绝 |
| [#404](https://github.com/magician336/lingdoc/issues/404) | 坏提案响亮失败、缺提案不打断对话、用途由服务端说了算 | #403 | A3、A5、A6、A16、A17、A18 | 三态齐备；缺提案时回复仍在；非法提案零写入且失败可读；旧幂等键仍可重放；契约反例补齐、矩阵闭环 |

**切片为什么这么切**：先修「按层切」的那一版（契约 / 服务端 / 前端 / 契约同步）——它交付的契约在下一票之前**没有消费者**，中间那一段不可演示。改成纵切后，第一张票做完就能演示「面板里出现一份被校验过的可编辑草案」。

每票一个分支、一个 PR、merge commit 合入；证据写进 PR 正文。**阻塞边用 GitHub 原生 `blocked_by` 登记**（#402←#401、#403←#402、#404←#403），与 PF-2 的先例一致。

## 7. 依赖边与阻塞

- 本面**不依赖**其他面：上下文包后端已落地，不等 #124 的前端消费（PF-6）。
- 本面与 PF-3 / PF-4 / PF-6 的路径集**零重叠**（登记处校验保证）。
- 迁移：**无**。提案不进库（它是 turn 建议的一部分），因此没有迁移编号争用。

## 8. 迁移与数据形状

无迁移。提案随 `AgentTurn.Suggestions` 走既有存储形状；不新增列、不改既有列。

## 9. 风险登记

| 风险 | 影响 | 处置 |
|---|---|---|
| 助手侧同一把钥匙两套强度 | 助手那张卡仍不被校验 | **有意保留**：助手不是本面范围，写进 Spec 的 Implementation Decisions 第 5 条；将来另票收紧 |
| 巨型服务文件继续变大 | 变化放大 | 新契约成独立模块（D8）；重构另案 |
| 提示词与契约漂移 | 用户拿到形状不对的提案 | 守护 A1 / A2 断言提示词不另写形状 |
| 提案被当成证据 | 学术结论被模型输出污染 | D7 + 既有确认门禁 + A9 / A10 用例 |
| 真实提供方链路未跑 | 真实模型下的提案质量未知 | `NOT RUN`：本面只做 mock 档；真实链路另票 |

## 10. 明确不做

- 助手侧「可编辑调研任务建议」的校验。
- 上下文包的前端消费（PF-6）。
- 检索侧改造：调研任务执行、导入候选、证据卡、研究主张的确认路径。
- 巨型服务文件的重构。
- 同义词作为独立存储字段。
- 提案的任何自动升格。
- 让真实提供方链路跑通。
