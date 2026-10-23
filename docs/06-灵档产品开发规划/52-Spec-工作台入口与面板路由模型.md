# Spec：工作台入口与面板路由模型

> 发布副本：[Issue #450](https://github.com/magician336/lingdoc/issues/450)。
> 定稿依据：[#442](https://github.com/magician336/lingdoc/issues/442) §2.1（编号 **52**）｜研究底稿：`research/new-spec-candidates` @ `9ae5d9fb`（只推分支、未合入 `main`）。
> 施工并行面：**待切**——按 [#441](https://github.com/magician336/lingdoc/issues/441) §0.2，本 Spec 的仓内文档写出来之后，它的施工面才能切（文件路径集在文档定稿前无法确定）。
> 相关：[#384](https://github.com/magician336/lingdoc/issues/384)（G6 运维面板独立化与 Workspace 集成——**本 Spec 解锁它**）、[#116](https://github.com/magician336/lingdoc/issues/116)（C 工作台与正式入口合流——本 Spec 承接的是它背后的**能力**，不是那张决策票）。

## Problem Statement

工作台的面板入口是**写死的**。`frontend/src/views/lingdoc/Workspace.vue` 的面板机制由五处散点共同构成，每加一个面板要改五处：

| 散点 | 位置 | 现状 |
|---|---|---|
| **类型** | `Workspace.vue:841` | `type FullPanel = 'research' \| 'changes' \| 'governance' \| 'members' \| 'review'` |
| **渲染** | `Workspace.vue:738-742` | 五段并列的 `v-if="fullPanel === 'xxx'"`，每段直接写死该面板的全部 props 与事件 |
| **引用** | `Workspace.vue:843-844` | 五个 `ref`：`researchPanelRef` / `opsPanelRef` / `governancePanelRef` / `collaborationPanelRef` / `chapterReviewPanelRef` |
| **忙碌态** | `Workspace.vue:845` | `fullPanelBusy` 是上五个 ref 的 `busy` / `operationBusy` 的逻辑或 |
| **标签** | `Workspace.vue:846` | `fullPanelLabel` 是一张查表：五个取值 → 五个中文标签 |

五个面板的组件本身**分处两个目录**：`ResearchPanel` / `ImpactOpsPanel` / `ProjectCollaborationPanel` / `ChapterReviewPanel` 在 `frontend/src/views/lingdoc/`，而 `TemplateGovernancePanel` 在 `frontend/src/components/`（`Workspace.vue:782` 的 import）。⇒ 「面板在哪」不是一个能由规则推出的事实，但它不影响注册机制本身。

面板的**入口按钮**同样分散：`Workspace.vue:38`（members）与 `:126-129`（research / changes / governance / review）各有一处。

由此产生三件具体的事：

1. **新面板没有合法入口位。** G6 运维面板要独立入口（#384）——`FullPanel` 是写死的联合类型，加一个 `'ops'` 取值要改类型、渲染、ref、busy 表达式、标签表五处，而其中四处是**纯机械的重复**。
2. **视图不按项目状态与当前责任分流。** 草稿项目、有章节责任、无章节责任三种情形现在渲染同一套界面；用户看到的是「一个工作台」，不是「他此刻该看到的那一屏」。
3. **保留路径的集成无人拥有。** `frontend/src/views/lingdoc/Workspace.vue` 在登记处里是 **`RESERVED_PATHS`**（保留路径，不归任何面），按路径图的约定「保留路径的集成是所有前端面完成后单独做一次」——**这次集成没有任何票或 Spec 拥有**。于是 G6 面板要挂进去时，只能绕过约定去改保留路径。

这不是「少一个界面」的问题。面板入口是「**谁可以出现在什么位置、什么时候出现**」的规则，现在这条规则住在组件的 props 与条件表达式里，既不能注册也不能按状态分流——而它本来应该是**数据**。

## Solution

工作台的面板与路由**可注册**，并按**项目状态与当前责任**分流视图；保留路径的集成由本 Spec 承接。

- **面板注册表**：一份声明式数据，每条声明面板的身份（id、显示名、组件、条件），`Workspace.vue` 从它渲染而不是维护五段并列的 `v-if`。
- **分流规则**：草稿项目渲染准备台且**不**渲染空章节编辑器；有章节责任渲染写作台；无章节责任渲染项目简报。规则由注册表驱动，不是组件内的分支。
- **集成有主人**：本 Spec 就是「保留路径单独集成一次」这件事的规格与归属。

## User Stories

1. As a 科研用户, I want the workspace to show me the 准备台 when the project is still a 草稿, so that I am not dropped into an empty chapter editor.
2. As a 科研用户, I want the 准备台 to not render the empty chapter editor at all in a 草稿 project, so that an empty editor is never mistaken for missing work.
3. As a 科研用户, I want the 项目简报 when I have no chapter responsibility, so that the workspace shows what is relevant to a reader rather than to an author.
4. As a 科研用户, I want the 写作台 when I do have chapter responsibility, so that the writing surface is where my responsibility starts.
5. As a 值班人员, I want the G6 运维面板 to have its own entry in the workspace, so that I do not have to enter the 变更与运行 panel and then switch tabs.
6. As a 值班人员, I want the G6 运维面板 to be reachable without going through the G3 panel, so that the value of moving it out is actually realized.
7. As a developer, I want to add a panel by adding one registry entry, so that adding a panel does not mean editing five scattered places in a reserved file.
8. As a developer, I want the panel id to be unique and type-checked, so that a typo in a registry entry is a compile error rather than a panel that silently never appears.
9. As a developer, I want a panel to declare a condition under which it appears, so that a panel is not offered to a project state it cannot serve.
10. As a developer, I want the registry to be the single place that knows which panels exist, so that 「有哪些面板」 is a question with one answer.
11. As a QA 负责人, I want the routing rules asserted at the component level, so that a rule change cannot silently mis-route a project state.
12. As a QA 负责人, I want the existing five panels to keep their behaviour after the registry lands, so that the refactor is not a behaviour change.
13. As a QA 负责人, I want the dirty-guard and focus management to keep working for every panel, so that an unsaved input is still protected.
14. As a 项目负责人, I want the workspace to route by state, so that different members of the same project can be looking at different surfaces on purpose.

## Implementation Decisions

1. **面板注册表是一份声明式数据，不是插件机制。** 它是仓内的一份普通模块导出的常量数组，每条含 `id`、显示名、组件、条件。它不引入运行时加载、不引入动态 `import()`、不引入外部注册 API。理由：本仓已有足够多的「机制」（登记处、契约校验、命名登记表），再引入一套插件体系会放大复杂度，而眼下真正缺的是**把写死的五处变成一处**。

   **仓内没有可借鉴的前端注册先例**——`frontend/src` 里 `registry` / `plugin` / `manifest` 的命中都是别的东西（系统设置项 registry、审计动作 i18n registry、Vite/Vue/ProseMirror 的 plugin），`addRoute` 零命中。**可借鉴的是仓内另一种「一处真相」形态**：`scripts/lingdoc_parallel_surfaces.py`（声明式数据 + loader 校验 + 守护测试）与 `scripts/lingdoc_naming_registry.py`（数据内联在 `NAMED_FILES`）。⇒ 本 Spec 的注册表沿用同一形态：**声明式数据 + 一条静态断言 + 组件测试**，不新造门禁层。

2. **`FullPanel` 由注册表推导，不再手写。** 面板 id 的类型从注册表条目导出（`type FullPanel = (typeof panels)[number]['id']`），于是「加一个面板」的第一个散点自动消失。**但**：id 联合类型只覆盖**已注册**的面板，注册表之外的 id 在类型上就是非法的——这正是我们要的。

3. **注册表只声明「身份」，不承载 props 形状。** 五个既有面板的 props 互不相同（`ResearchPanel` 要 `selected-evidence`，`ChapterReviewPanel` 要 `selection` 与 `formal-current`，`ProjectCollaborationPanel` 要 `chapters` 与 `assets`……）。让注册表承载 props 会逼出一套 `{ project: Project; currentUserId: string; … }` 的统一形状，那是**为抽象而抽象**。⇒ 注册表只管「这个面板是谁、叫什么、什么条件下出现」，**props 仍由 Workspace 逐个传**——这一处不合并。

4. **面板组件遵守统一的 `busy` 暴露契约。** 现在 `fullPanelBusy` 读的是五个 ref 上各自的 `busy` / `operationBusy`（`Workspace.vue:845`）。注册化之后 ref 的数量不定 ⇒ 统一为一个契约：面板 `defineExpose({ busy })`，Workspace 按注册表顺序收集 ref 并求或。**不引入事件总线**——面板的忙碌态由 Workspace 持有的 ref 读取，与现状同源。

5. **分流规则是注册表的一部分，不是组件内分支。** 三条规则（草稿 → 准备台 / 有章节责任 → 写作台 / 无章节责任 → 项目简报）表达为注册表条目上的条件，而不是散在模板里的 `v-if`。这样「一个项目状态下能看到哪些面板」是**读注册表就能回答**的问题。

6. **保留路径的集成由本 Spec 一次性做完。** `Workspace.vue` 是保留路径 ⇒ 按路径图约定，所有前端面落地后单独集成一次。本 Spec 就是那次集成。**并且**：集成时新增的面板入口必须走注册表，**不得**再出现并列的 `v-if`。

7. **不改 `fullPanel` 抽屉的交互契约。** `role="dialog" aria-modal="true"`（`Workspace.vue:736`）、`closeFullPanel` 的 dirty 保护（`:867`）、`onFullDialogKey` 的焦点陷阱与回焦（`:887-888`）全部保持不变。注册化只换「有哪些面板」与「何时显示」，不换「面板怎么开、怎么关、怎么保护未保存输入」。

8. **不为「面板路由」引入前端路由库。** 面板不是 URL 路由——它是一个覆盖在文稿上的抽屉，现状也不产生 URL。`frontend/src/router/index.ts` 是**单一静态 `routes` 数组**（`:46-244`），LingDoc 只有 3 条路由（`:131-142`），**没有面板级路由**；全仓 `addRoute` 零命中 ⇒ 仓内**没有**动态路由注册的先例。引入路由会把「打开面板」变成可分享的链接，牵出一串「面板状态要不要进 URL」的决策，不属本 Spec。

9. **G6 面板的接入归 #384，不在本 Spec 内。** 本 Spec 提供**机制**（注册表 + 分流 + 集成）；#384 交付 G6 运维面板的**内容**。本 Spec 落地后，#384 只需加一条注册表条目。

## Testing Decisions

好的测试断言的是**科研用户与值班人员能看见什么**（哪个状态下出现哪个面板），而不是注册表内部怎么写的。**一个接缝**。

- **主接缝 —— 工作台组件的渲染结果**（前端组件测试，与仓内 `frontend/src/views/lingdoc/*.test.ts` 既有形态同款）：
  - 草稿项目渲染**准备台**，且**不**渲染空章节编辑器；
  - 有章节责任的项目渲染**写作台**；
  - 无章节责任的项目渲染**项目简报**；
  - 一个满足条件的面板出现在抽屉里，一个不满足条件的**不**出现；
  - 抽屉的标题用的是该面板在注册表里声明的显示名；
  - 面板的 `busy` 为真时，工作台的返回动作被拦住（`fullPanelBusy` 的契约保持）；
  - 面板有未保存输入时，返回动作仍然被 dirty 保护拦住。
- **注册表自身的静态断言**：
  - 面板 id 唯一（重复 id 即失败）；
  - 面板 id 与 `FullPanel` 类型一致（由类型系统保证，编译期红）；
  - 每条注册表条目都有显示名与组件。
- **回归**：既有的五个面板（调研与证据 / 正式变更与运行 / 模板目录与规则 / 成员协作 / 章节评审与批注）的打开、关闭、dirty 保护、焦点陷阱与回焦行为不变；既有前端测试全绿。
- **模式**：纯前端组件测试，**不跑真实浏览器**——「真实浏览器 × 真实后端」的一致性对偶是 [54-Spec](https://github.com/magician336/lingdoc/issues/452) 的事，本 Spec 不重复。
- **先例**：仓内前端组件测试用「注入假客户端、断言用户可见行为」的既有手法；本 Spec 的分流断言沿用同一手法。

## Out of Scope

- 章节编辑器、草稿立项、上下文包面板各自的**业务语义**——它们是既有 Spec（#133 / #143 / #431）的域。
- G6 运维面板的**内容**（独立 API 模块、独立面板组件）——归 #384。
- 动作族「插入 vs 替换」的词汇语义（#342 / #347 的遗留，两张票已 CLOSED）。
- 前端路由、URL 状态、面板的可分享链接。
- 懒加载 / 代码分割 / 性能优化。
- 移动端与窄屏布局。
- 工作台外壳的两态（`mode = 'research' | 'focus'`，`Workspace.vue:1106`）——本 Spec 只管抽屉里的面板，不动外壳。

## Further Notes

- 本 Spec 由 [#442](https://github.com/magician336/lingdoc/issues/442) 定稿编号 **52**。**筛选三条标准**：① 验收点能写成机器可判的断言 ✅ ② 至少解锁一张下游票（#384）✅ ③ 不与现有 G 线 Spec 重叠 ✅（#133 / #143 / #431 各有各的域，**没有一张拥有工作台的信息架构与路由模型**）。
- **与 #116 的关系**：#116 是决策票（「锁定一条不另造平行业务状态的前端入口方案」）；本 Spec 承接的是它**背后的能力**。#436 已记「候选 1 正落在 #116 那一片上，两边需对齐」⇒ 本 Spec 开工前要与 #116 的决策对齐，两边不重复裁剪。
- **开工时序**：本 Spec 要动保留路径 `Workspace.vue`，按路径图约定须在**其它前端面落地后单独做一次集成** ⇒ **须等 PF-6（#330，PR #443）落地之后**。
- **依赖边**：`#384 blocked_by #450`（已加）；本 Spec 自身**无上游**。
- **本仓已有的「一处真相」先例**：施工并行面登记处（`scripts/lingdoc_parallel_surfaces.py`）就是一份声明式数据 + loader 校验 + 守护测试。本 Spec 的注册表沿用同一形态——**声明式数据 + 一条静态断言 + 组件测试**，不新造门禁层。

- **判定口径**：本 Spec 按 [`验收判定口径.md`](../08-本轮实施方案/验收判定口径.md) **v1** 判定——
  **验收点 = 一个可单独核验、可单独判 PASS / FAIL / NOT RUN 的原子要求**；
  `## User Stories` 每条 = 1 个验收点，`## Testing Decisions` 每条 = 1 个验收点（聚合条目按顿号 / 逗号切分成子项），
  `## Implementation Decisions` **不计入点数**、只作约束引用。
  本 Spec 的 `## Out of Scope` 与 `## Further Notes` 不计入验收点。
