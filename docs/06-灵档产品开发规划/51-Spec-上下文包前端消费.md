# Spec：上下文包的前端消费

> 发布副本：[Issue #431](https://github.com/magician336/lingdoc/issues/431)。由施工并行面 PF-6（[#330](https://github.com/magician336/lingdoc/issues/330)）交付。
> 兑现的 G 线 Spec：[#124 AI 上下文与多轮会话契约](https://github.com/magician336/lingdoc/issues/124)（仓内副本 `22-Spec-AI上下文与多轮会话契约.md`）的前端验收点。

## Problem Statement

科研用户按下「发送」之前，看不到这一轮**到底带了什么**。

服务端的上下文包早就落地了：`POST /projects/{projectId}/agent-context` 会按当前授权、当前版本与当前卡片状态组装一份上下文包，连同选择理由、授权结论、输出约束一起返回。`GET /projects/{projectId}/agent-sessions/{sessionId}/turns` 也会把每一轮实际用过的上下文包留在回执里。

前端却没有消费它。研究面板里只有一行 notice，显示来源定位、资料版本、模型使用许可和输出约束——**卡片、选择理由、卡片状态三样都没有**。后果是三件具体的事：

1. 用户勾了证据卡，界面上看不出那张卡有没有真的进了这一轮。卡片会连带拉进它的来源，来源又可能被撤权剔除，于是「勾了」和「带上了」是两件事，界面只呈现了前者。
2. 选择理由完全不可见。用户不知道某份资料为什么在、某张卡为什么在，也就无从判断这一轮的范围是不是自己要的。
3. 卡片状态不可见。一张 `CANDIDATE` 卡和一张 `CONFIRMED` 卡在上下文包里长得一模一样（卡片引用只有 id、version、kind），用户无从区分「可用的证据」和「还没人确认的主张」。

增删也只发生在表单的多选框上。改完之后界面从不复述服务端的组装结果，于是「我以为带了」和「实际带了」之间没有任何一屏可以对照。等模型在错误的范围上给出答案，用户看到的只是一段正常的回复，没有任何线索指向范围本身。

这不是「少一个界面」的问题。上下文包是服务端唯一说了算的事实，而前端在没有复述它的情况下，让用户对着一个自己拼的影子做决定。

## Solution

用户得到一个**上下文包面板**：它把服务端组装好的那一份上下文包原样显示出来——卡片、选择理由、版本基线、状态、权限态——并允许在敏感请求发出之前增删引用卡片。

两条规则决定了这个面板的全部行为：

**规则一：事实只来自服务端。** 面板显示的「本轮包含什么」，永远等于服务端组装的那一份。用户勾了什么只是**意图**，不是事实；界面把两者分成两块显示，且事实区不接受任何本地推断。

**规则二：意图与事实是两个类型。** 增删之后必须重新向服务端预览，用返回的包刷新事实区。本地编辑态永远不能被渲染成「本轮包含」。

配套的三条实现约束把规则落到实处：

- 上下文包的契约类型由**已有的**传输层模块拥有，本面**只读不改**，不复制第二份；
- 上下文包模块是**用例门面**：从「构造请求 → 向服务端预览 → 对齐卡片状态 → 分组展示」整套流程只有一个入口，视图不直接碰传输层；
- 选择理由按服务端给出的**原文**逐条展示，前端不把它解析成「哪一条对应哪个条目」的映射。

## User Stories

1. As a 科研用户, I want to see which 卡片 this turn actually carries, so that I know whether the card I picked is in the scope.
2. As a 科研用户, I want to see why each item was selected, so that I can judge whether the scope is the one I meant.
3. As a 科研用户, I want to see the version baseline of the turn, so that I know which project revision the reply applies to.
4. As a 科研用户, I want to see whether a referenced 卡片 is 候选 or 已确认, so that I do not mistake a candidate for evidence.
5. As a 科研用户, I want to see whether each 来源 is allowed for 模型使用, so that I can see where the boundary of my own access is.
6. As a 科研用户, I want to see the 授权结论 of this turn, so that I know whether the turn still needs my confirmation.
7. As a 科研用户, I want to see the 输出约束 the assistant is held to, so that I know what shape the answer is allowed to take.
8. As a 科研用户, I want to remove a card from the scope before sending, so that I keep control of what the assistant reads.
9. As a 科研用户, I want to add a card to the scope before sending, so that I can widen the scope deliberately rather than by accident.
10. As a 科研用户, I want the scope to refresh after every edit, so that what I see is what the server would assemble.
11. As a 科研用户, I want the panel to distinguish my intent from the server's assembly, so that I never mistake my selection for the real scope.
12. As a 科研用户, I want a card whose version moved to be marked as 状态未知 rather than silently shown with a stale status, so that a stale status never misleads me.
13. As a 科研用户, I want the panel to show nothing when the preview fails, so that a failed request never leaves a stale scope on screen.
14. As a 科研用户, I want the panel to say the baseline moved when the version conflict is refused, so that I know to refresh rather than retry blindly.
15. As a 科研用户, I want the two assistants' conversations to stay separate, so that a task-side turn and a research-side turn are never merged into one scope.
16. As a 科研用户, I want each assistant's panel to show only that assistant's turns, so that the scope I read belongs to the conversation I am in.
17. As a 科研用户, I want the selection reasons shown in the server's own words, so that the explanation I read is the one the server actually gave.
18. As a 项目负责人, I want every sensitive request to show what it will carry before it is sent, so that an accidental over-share is visible before it happens.
19. As a 安全责任人, I want the panel to show only what the server assembled, so that the interface cannot widen access beyond the server's judgment.
20. As a 安全责任人, I want revoked material to disappear from the panel, so that an already-revoked excerpt is not still on screen from an earlier preview.
21. As a 审阅者, I want to see the correlation id of the assembly, so that a turn can be traced back to the scope it was built from.
22. As a 审阅者, I want the panel to show the same scope that was recorded on the turn, so that a recorded turn can be checked against what was shown.
23. As a 开发者, I want the context-package contract to have exactly one owner in the front end, so that adding a field to it cannot leave two copies behind.
24. As a 开发者, I want the view to talk to one context-package facade instead of several transport calls, so that the rules about intent and facts live in one place.
25. As a 开发者, I want to reuse the existing context-package client rather than write a second one, so that the front end and the server cannot disagree about the request shape.
26. As a QA 负责人, I want the request body built by the front end to be checked field by field against the server contract, so that a renamed field cannot pass unnoticed.
27. As a QA 负责人, I want a guard that the view does not reach past the facade into the transport layer, so that the layering cannot quietly erode.
28. As a QA 负责人, I want the selection reasons to be shown verbatim, so that a future parser cannot invent a mapping the server never made.

## Implementation Decisions

1. **本面是纯前端面，不改服务端。** 上下文包的组装、授权判定与卡片状态校验全部留在服务端已有的上下文构建路径上。本面新增两个前端文件，不新增端点、不改端点、不改请求或响应形状。

2. **上下文包的契约类型不复制。** `AgentContextPackage`、`AgentContextInput`、`VersionRef`、`AgentSourceRef` 由已有的传输层模块拥有（PF-5 的路径，已在 main 上）。上下文包模块用类型导入复用它们，**不重新声明**。理由：同一份契约知识住在两个模块，就是信息泄漏——将来服务端加字段，两个模块都要跟着改，漏掉一个就开始漂移。配套条件：上下文包模块的导出清单里不出现这些契约类型本身，并有一条测试守住这一点。

3. **上下文包模块是用例门面，不是又一个原子客户端。** 原子客户端（一个端点一个函数）留在传输层模块；上下文包模块负责这一用例的完整编排——从意图构造请求体、调用预览、对齐卡片状态、到分组产出可展示的事实——对外只暴露一个入口。视图只与这个门面说话，**不直接调用传输层**。配套条件：视图文件不导入传输层模块，并有一条测试守住这个边界。

4. **意图与事实是两个不可互换的类型。** 意图是用户勾选的卡片与来源标识；事实是服务端组装好的那一份上下文包。两者的字段名不同，结构上不可互相赋值——视图的事实区只接受事实，编辑区只接受意图。理由：把两者合成一个类型，就等于允许「本地勾选」被当成「本轮包含」渲染，而那正是本面要消灭的错误。

5. **增删之后必须重新预览，事实区只由预览结果刷新。** 服务端的上下文输入是全量替换语义，没有「排除」这一说：删掉一张卡就是不再提交它，服务端重新组装时可能连带去掉它依赖的来源，也可能因为别的卡仍然依赖而保留某些来源。因此增删的正确实现是「改意图 → 重新预览 → 用返回的包刷新事实区」，而不是在本地增删事实区里的条目。

6. **预览失败或版本冲突时，事实区清空。** 保留上一次成功的包会让人以为那就是当前范围。预览失败时事实区置空并显示错误；版本冲突被拒时置空并说明基线已变。理由：这是「把错误设计掉」——错误状态不应该有一个看起来正常的画面。

7. **卡片状态来自证据卡清单，按 id 与 version 双重对齐。** 上下文包的卡片引用只有 id、version、kind，没有业务状态；证据卡端点才有状态。面板额外拉一次证据卡清单来补状态，并按 id **与** version 双重对齐：对得上显示状态，对不上（版本已经变了）显示「状态未知（版本已变）」，**不回退**到任何猜测值。理由：状态是两个来源的事实，对齐键必须是完整版本，否则一张已经翻新的卡会挂着旧卡的状态。

8. **对齐不上时按缺失处理，不按旧值处理。** 证据卡清单里找不到对应 id，或版本不等，面板一律显示「状态未知」，不显示任何缓存过的状态。

9. **选择理由按服务端原文逐条展示，不解析成条目映射。** 服务端的 `selection_reasons` 是一组自由文本，前端把它当**不透明字符串列表**渲染。它与卡片、来源之间没有可靠的机器可读对应关系，硬要解析就得靠字符串匹配去猜——那是本地推断，会在后端改措辞时静默失真。结构化的理由映射是服务端契约缺口，本面不代偿。

10. **版本基线只显示前端契约里已有的字段。** 面板显示项目版本、规范修订、上下文修订、章节版本、任务版本与模板版本。服务端上下文包另有规则集标识与规则集版本两个字段，但前端契约类型尚未收录；本面不替传输层补字段（那等于让前端替服务端维护契约），这两个字段不显示，作为契约漂移的残留缺口交出去。

11. **授权结论按服务端给的原值展示，不翻译成前端语义。** 服务端给出的是授权状态原值与逐来源的模型使用许可；面板原样呈现，不在本地推导「这条要不要确认」。

12. **两种助手的会话与面板各自独立。** 面板按助手机别分别承载，面板不跨助手合并任何一轮。服务端已经按助手隔离会话与轮次，前端不得把两边的轮次拼进同一份范围。

13. **确认敏感内容是显式动作，不是默认勾选。** 面板显示服务端给出的授权结论与输出约束；确认由人做出，勾选状态在上下文变化时清空——上下文变了，上一次的确认就不作数。这一条沿用现有行为，本面不改变它。

14. **模块名按命名规范取，不沿用票面留下的旧名。** 登记处最初给本面留的 API 模块路径名以交付批次代号开头（`g2Context.ts`），而仓里的命名登记表把 `^[gGtT]\d` 判为编号命名。**施工前实测**：新增该文件会让守护测试报 `breaks numbered but is not declared in the registry` 并以退出码 1 失败，改名后的 `agentContext.ts` 不报。PF-1 当年也把登记的 `g6Runtime.ts` 改成了 `runtimeMetrics.ts`，是同一类修正。因此本面的 API 模块叫 `agentContext.ts`——它承载的正是 Agent 上下文包，名字与内容一致（ch14）。两个测试文件用 `.test.ts`（仓内前端测试的主流形态，且已有组件用 `.test.ts` 的先例）。

15. **本面不改研究面板。** 研究面板已经有一份自己的上下文构造逻辑与一处预览，属于另一个面已经交付的文件，本面不得改动。由此产生的重复由残留缺口承载，不在本面顺带处理。

## Testing Decisions

好测试断言的是科研用户能看见什么、点下去发生什么，而不是某个函数内部怎么写。两个接缝，第三个不另开。

- **接缝一 —— 上下文包模块的纯函数**（与传输无关的部分）：
  - 意图增删是纯粹的：加一张卡、删一张卡、重复加同一张卡、加一张已存在的来源，结果是意图里对应标识集合的正确变化，且原意图不被就地修改；
  - 请求体按服务端契约逐字段构造：意图里有的字段进去、意图里没有的可选字段不进、版本期望原样带上——并与后端上下文输入契约逐字段对照，防止字段改名后前端静默失配；
  - 卡片状态对齐：id 与 version 都相等时给出状态；版本不等时给出「状态未知（版本已变）」；清单里没有该 id 时同样给出「状态未知」；任何情况下都不回退到别的值；
  - 事实分组：卡片、来源、理由、版本基线、权限各成一组，来源按服务端给的顺序呈现，理由保持服务端给的条数与顺序；
  - 事实分组只接受事实，不接受意图——类型层面拒绝，不靠运行时约定。
- **接缝二 —— 上下文包面板**（用户可见行为，注入假的预览入口）：
  - 面板显示卡片、理由、版本、状态与权限态，且状态未知时显示的是「状态未知」而不是某个状态；
  - 用户删一张卡后，面板发出的是「意图变了」，事实区在新的预览结果回来之前不显示旧事实；
  - 预览失败时事实区为空且有错误提示，不残留上一次成功的结果；
  - 版本冲突被拒时事实区为空且提示基线已变；
  - 撤权后服务端不再返回受限条目时，面板不再显示它；
  - 面板只与上下文包模块交互，不直接调用传输层。
- **守护**：上下文包模块不导出契约类型本身；面板不导入传输层模块。这两条用静态断言守住，因为它们是本面全部设计的地基，靠人读代码守不住。
- **先例**：前端组件测试用注入假客户端的方式断言用户可见行为（研究面板与原子客户端的测试是同一手法）；契约形状断言用「与后端契约逐字段对照」的方式（后端上下文选择契约的形状测试是同一手法）。
- **不跑**：真实模型链路。本面不接触模型调用。

## Out of Scope

- 服务端上下文组装、授权判定与卡片状态校验——全部已落地，本面不改。
- 选择理由的结构化映射。服务端给出的是自由文本，理由到条目的机器可读对应关系是服务端契约缺口，单独开票。
- 按卡片风险分级。低风险的项目状态或任务状态卡免额外确认这条要求，在当前服务端判定下并不成立（服务端只看这一轮有没有来源，不区分来源来源的风险）；前端自行分级就是本地推断，会与服务端判定漂移，单独开票。
- 研究面板里已有的那份上下文构造逻辑与预览的收拢。它属于另一个面已交付的文件，本面不得改动，单独开票。
- 前端契约类型与后端上下文包之间新增字段的同步（规则集标识与规则集版本目前只落后端），单独开票。
- 会话与轮次的多轮展示形态。本面交付的是**一轮**的范围面板；历史轮次的浏览沿用现有回执视图。
- 两种助手共用一个面板实例或合并会话。助手之间必须保持分离。
- 工作区的集成接线。工作区视图是保留路径，所有前端面完成后单独集成一次，不由本面独占。

## Further Notes

- 本面兑现的是 Spec #124 的前端验收点。#124 保持开放：它的后端契约虽已落地，但该 Spec 的其余验收点不由本面交付。
- 施工并行面登记处是路径归属的唯一真相。本面新增的两个前端文件必须先登记进本面的路径集；票面提到的两个路径名沿用登记处的登记名。
- 本 Spec 自述的四处缺口都已单独开票承接，避免它们随本面完成而消失：选择理由的结构化映射；按卡片风险分级；研究面板里重复的上下文构造逻辑；前端契约类型落后于服务端上下文包的两个字段。
- 一条上下文包模块与传输层之间的方向约束值得记下来：模块依赖传输层，传输层不反过来依赖模块。若将来传输层需要用到上下文包模块的语义，那是层次错位的信号，应当合并而不是加一条反向依赖。
