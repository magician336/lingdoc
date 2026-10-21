# Spec #48：代码命名规范收口

> 状态：草案（2026-10-21）。本 Spec 由 `/ousterhout-software-design` 的代码命名审查产出。
> 审查依据：Ousterhout《软件设计的哲学》第 2、5、14、17、18 章（复杂性、信息隐藏、选择的名字、一致性、显而易见的代码）。
> 配套实施与验收证据：[`../../08-本轮实施方案/Spec-48-代码命名规范收口.md`](../../08-本轮实施方案/Spec-48-代码命名规范收口.md)。
> 上游坐标：[#144（Wayfinder：G1–G7 并行 Spec 依赖图）](https://github.com/magician336/lingdoc/issues/144)、[#259（模块边界收口与知识归属归位）](https://github.com/magician336/lingdoc/issues/259)。
> 取证方式：静态扫描（文件树、导出符号、标识符正则、包分布）。**未运行测试，未启动服务。**

## Problem Statement

代码层用**项目坐标轴**给文件和标识符命名。`g5_policy.go` 的名字里没有 G5，它拥有的是 `EffectivePermission`；`g6_runtime.go` 拥有的是 `RuntimeControl` / `RuntimeEvent`；`g6_trial.go` 拥有的是 `TrialWindow` / `TrialSample`。编号描述的是「这批工作来自哪个产品线」，不是「这段代码拥有什么知识」。

读者要读懂这些文件，必须先知道一条**只存在于文档里的映射**：「G5 = 权限治理」「G6 = 质量运行」。这条映射是典型的不明显知识，而复杂性 = 依赖数量 × 不明显程度。

**规模（实测，`internal/` + `frontend/src/` + `scripts/`）**

| 项 | 数量 |
| --- | --- |
| 带 `g<数字>` / `t<数字>` 前缀的代码文件 | **115**（internal 55 / frontend 53 / scripts 7），合计 **44,675 行** |
| 按编号分布 | g3 = 46、g2 = 23、g5 = 13、g1 = 11、g6 = 11、g4 = 6、g7 = 1 |
| 编号跨包数 | **G2 散在 5 个包**，G1 / G3 / G4 各散在 3 个包，G5 / G6 各 2 个包 |
| 带 `G<数字>` 前缀的导出标识符 | **47**（`G2AgentModel`、`G2HostAgentModel`、`G2ResearchAdapter`、`G2Runtime`、`G5Get`、`G5List`、`G5Put`、`G6CAS`、`G6Insert`、`G6List`、`G6Read` …） |

**四个具体问题**

1. **编号不是模块边界。** G2 散在 5 个包 23 个文件，G3 散在 4 个包 46 个文件。一个编号横跨多个包，说明它是任务批次标签，不是知识归属。
2. **编号在导出标识符里是 0 比特信息。** `g4_full_http.go` 导出 `RegisterG4FullRoutes`、`sendG4Error`、`sendG4OK`——「G4」在这三个名字里不说明任何行为，只说明「这批工作做的」。
3. **工单号与验收标准号进了代码。** `internal/container/g4_issue196_legacy_e2e_test.go`、`scripts/acceptance/issue196_g4_real_http.py`；`AC1`–`AC6` 出现在 `dependency_edge_test.go` 与 `impact_task_test.go` 的注释里。注释一旦引用文档里的编号，文档改了注释就撒谎。
4. **前端文件名四套风格并存。** `frontend/src` 里 camel 332 / Pascal 260 / kebab 52 / 全小写 111；同一个 `frontend/src/utils/` 目录里 kebab 13 个、camel 68 个。`AGENTS.md` 规定了组件 PascalCase 与 composable `useX.ts`，**没有规定非 composable 的 util 文件叫什么**。

**代价**：每一次读代码都要多付一次「翻译编号」的成本；新人要额外背一张 G1–G7 对照表；改名时无法判断一个文件属于哪个知识，因为文件名不回答这个问题。

## Solution

**编号不是坏东西，位置错了才是问题。** 本 Spec 把命名分成三层，各层用各该用的词。

| 层 | 规则 | 现状 |
| --- | --- | --- |
| **代码层**（Go / Vue / TS / 脚本） | 文件名与标识符表达「这段代码拥有什么知识」或「这个动作做什么」。**不用项目编号。** | 违规 115 文件 + 47 标识符 |
| **文档 / 票 / 契约 / 证据层** | **保留** `G1`–`G7` 与 `T01`–`T16`。那里编号是项目坐标，是有效的。 | `internal/` 下 `t<数字>` 文件数 = **0**；文档层 127 个 T*/G* 文件 |
| **一次性全量改名** | **不可行。** 115 文件 / 44,675 行 / 47 标识符及其引用点，装不进一个 PR。 | — |

所以解法是三件事：

1. **立一条判据**：一个名字可以进入代码层，当且仅当它表达知识归属或动作语义。
2. **建一个登记表 + 一条守护测试**：把「文件拥有什么知识」写成机器可校验的声明，**冻结新增**（棘轮：遗留清单只减不增）。
3. **分批偿还**：按批次改名，每批一个 PR，每批证明对外契约零变化。

## User Stories

**作为领域开发者（读代码的人）**

1. As a 领域开发者, I want 文件名说明这段代码拥有什么知识, so that 我不用先查文档再读代码。
2. As a 领域开发者, I want 导出标识符的名字自足, so that 我只看签名就知道行为。
3. As a 领域开发者, I want 同一个动作只用一个动词, so that 我不用猜 `Get` 和 `List` 的差别。
4. As a 领域开发者, I want 不用记住 G1–G7 分别对应什么, so that 我能直接进入任何一个模块工作。
5. As a 领域开发者, I want 一个知识只有一个主人文件, so that 我知道一个行为应该改在哪。
6. As a 领域开发者, I want 看到文件拥有什么知识的唯一来源, so that 我不用在多个文件里比对。

**作为评审者**

7. As a 评审者, I want PR 里的新名字能说明意图, so that 我不用问作者「这属于哪批工作」。
8. As a 评审者, I want 编号只出现在文档和票上, so that 代码与项目坐标不混在一起。
9. As a 评审者, I want 改名 PR 显式声明对外契约零变化, so that 我能快速批准而不必逐行核。
10. As a 评审者, I want 改名与新增功能分开提交, so that 我能一眼看出哪个 PR 只是重命名。

**作为 CI 维护者**

11. As a CI 维护者, I want 守护测试拒绝新增编号命名的代码文件, so that 债务不再增长。
12. As a CI 维护者, I want 遗留清单只减不增, so that 棘轮有效而不是装饰。
13. As a CI 维护者, I want 登记表里同一个知识不被两个文件声明, so that 信息不泄漏。
14. As a CI 维护者, I want 导出标识符规则也被守护, so that 编号不会从文件名转移到标识符。
15. As a CI 维护者, I want 改名不改变门禁行为, so that 门禁面清单与判定层不需要跟着改。

**作为工具作者**

16. As a 工具作者, I want 脚本与人工评审读同一份登记表, so that 只有一个真相。
17. As a 工具作者, I want 前端文件名有唯一约定, so that 我不用猜 import 路径的大小写风格。
18. As a 工具作者, I want 登记表与施工并行面登记处同构, so that 仓库里只有一种「归属声明」形态。

**作为值班的人**

19. As a 值班的人, I want 生产代码里不出现 demo / 临时 / 调试残留的名字, so that 我不误判哪些路径是活的。
20. As a 值班的人, I want 临时文档这类概念有一个正式名字, so that 我知道它是有意的设计还是遗留。

## Implementation Decisions

### 1. 命名分层的判据（唯一权威规则）

一个名字可以进入代码层，**当且仅当**它满足以下之一：

- 它表达该文件或该类型**拥有的知识**（如 `permission.go` 拥有 `EffectivePermission`）；或
- 它表达该动作**做什么**（如 `RegisterTemplateGovernanceRoutes`）；或
- 它表达该概念在领域里的**通用词**（如 `chapter_review.go` 拥有 `ChapterReviewState`）。

**项目编号（G1–G7、T01–T16）、工单号、验收标准号不属于以上三类**，因此不得出现在代码层的文件名、目录名、导出标识符或注释里。

### 2. 登记表

新建仓内登记表，形态**照 `scripts/lingdoc_parallel_surfaces.py`**（declarative 数据 + loader 校验 + 非零退出，不静默降级）。登记内容：

- 每个代码文件的**路径**；
- 它**拥有的知识**（一行自然语言，必须能唯一指向该文件）；
- 它是否属于**遗留**（当前 115 个带编号的文件 + 47 个带编号的导出标识符全部登记为遗留）；
- 遗留项的**所属批次**（见第 6 条）。

登记表放在 `scripts/` 而非 `scripts/ci/`：`scripts/ci/` 是门禁判定层，必须保持「只有一个模块回答『什么会跑』」；`test_lingdoc_gate_scripts.py` 的 `KNOWN_SCRIPTS` 白名单会强制每个进入 `scripts/ci/` 的脚本登记用途。守护测试仍放 `scripts/ci/`。

### 3. 守护测试与棘轮

新建守护测试，随 `lingdoc-review.yml` 的 `python -m unittest discover -s scripts/ci -p 'test_lingdoc_*.py'` **自动进 CI，无需改 workflow**。

断言集合：

- **A**：新增代码文件不得以 `g<数字>` / `t<数字>` 开头（目录名同样）。
- **B**：代码里的导出标识符不得以 `G<数字>` / `T<数字>` 开头。
- **C**：代码文件名不得含 `issue<数字>` / `spec<数字>` / `ac<数字>`。
- **D**：**遗留清单只减不增**——遗留条目数不得高于登记表的基线值。
- **E**：登记表中同一个「拥有的知识」不得被两个文件声明。
- **F**：前端文件名符合第 4 条的单一约定。
- **G**：每个代码文件都在登记表中声明（登记表是完整清单，不是增量清单）。

**棘轮基线值写在登记表里**，随每次偿还递减。这样断言 D 在偿还开始前就成立，不会因为「现在还有 115 个违规文件」而让 CI 立刻变红。

### 4. 前端文件名约定

- **camelCase**：所有非组件的 `.ts` / `.js` / `.mjs` 文件（现状 332 / 755 是多数，kebab 52 个与全小写 111 个收编至此）。
- **PascalCase**：仅 `.vue` 组件文件。
- `useX.ts`：composable（`AGENTS.md` 已有，保留）。
- kebab-case 与全小写文件名作为**遗留**登记，按批次收编。

### 5. 文档层编号保留

`docs/06-灵档产品开发规划/` 与 `docs/08-本轮实施方案/` 下的 `G*` / `T*` 文件名与内容**保持不变**。这些编号在文档与票上是有效的项目坐标。

### 6. 分批偿还顺序

| 批次 | 内容 | 规模 | 理由 |
| --- | --- | --- | --- |
| **1** | 守护测试 + 登记表 + 棘轮基线 | 1 个 PR | **先冻结**，让债务不再增长 |
| **2** | 47 个带编号的导出标识符 | 1–2 个 PR | 调用点少、收益直接；`G6CAS` 这类名字改完立刻自足 |
| **3** | `frontend/src/views/lingdoc/g3demo/` 目录及其 44 个文件 | 1–2 个 PR | 单文件最大 3,271 行，需要独立批次 |
| **4** | `internal/lingdoc/workspacecore` 与 `internal/lingdoc/workspace` 的 g* 文件 | 2–4 个 PR | 两个包内改名，按知识簇切 |
| **5** | 弱语义名与调试残留（`temporary_*`、`_repro_gap.mjs`、`copy.go`、`legacy`） | 1 个 PR | 独立于编号问题 |
| **6** | 包名万能词（`workspacecore` / `workspace` / `demoroute`） | 另案 | 涉及包边界，需单独设计讨论 |

**每个批次一个 PR。** 批次之间不合并。

### 7. 改名手法

- **先改导出标识符，后改文件名**。标识符的调用点少，改动面可控；文件名会牵动 import。
- 每次改名后跑全量 Go 测试与前端测试，并用现有契约校验证明**对外契约零变化**。
- 改名前先在登记表里把该文件的「拥有的知识」写准——**登记表是改名的依据，不是改名的结果**。

### 8. 明确不改的东西

不改产品行为、不改 HTTP 端点与错误形态、不改数据库迁移、不改 `openapi.json` 与 `scenarios.json`、不改 `internal/lingdoc/` 下已有的知识归属名子包（`candidateadoption` / `delivery` / `membership` / `operationcore` / `reviewanchor` / `rulesetstore` / `sourcemarker` / `sourcepolicy` / `template` / `templategovernance`）——**这些名字已经是本 Spec 要的标准**。

## Testing Decisions

### 好测试的标准

只断言**外部可观察的性质**：一个文件是否存在、一个名字是否合规、登记表是否自洽、遗留清单有没有变长。**不断言实现细节**，不检查函数体内容，不检查注释措辞。

### 主 seam（一个）

**命名守护测试**——仓级静态检查，直接观察仓库状态。这是本 Spec 唯一需要**新建**的 seam，也是最高的 seam：它不需要跑产品，不需要起服务，不需要真实数据库。

先例（仓内已有同形态两例）：

- `test_lingdoc_gate_scripts.py` 的 `KNOWN_SCRIPTS` 白名单——「每个脚本必须登记用途」的强制登记。
- `test_lingdoc_parallel_surfaces.py`——登记处 + 守护测试 + 13 条阴性验证。

### 复用的现有 seam（不新造）

- **对外契约零变化**：`docs/08-本轮实施方案/contracts/` 下的契约校验与 `check_lingdoc_routes.py` 路由检查。改名 PR 必须证明它们仍然通过。
- **功能未破坏**：现有 Go 测试与前端测试。

### 阴性验证

守护测试必须包含「构造一份坏登记表 → 断言 loader **拒绝**它」的用例，覆盖：路径重叠、知识重复声明、遗留清单变长、非法状态、缺失声明。**坏数据必须响亮地失败，不得静默降级**——这是仓里既有的做法。

### 不做的事

- 不为命名写**行为测试**。名字不是行为。
- 不写**快照测试**去断言「所有文件名列表不变」——那只会在每次合法改名时红。

## Out of Scope

- 不改产品行为、HTTP 契约、错误形态、数据库迁移。
- 不重命名 `internal/lingdoc/` 下已有的知识归属名子包。
- 不动 `docs/` 下的 `G*` / `T*` 编号文件。
- 不改 G1–G7 的依赖图与 Spec 边界（属 [#144](https://github.com/magician336/lingdoc/issues/144) 与 [#259](https://github.com/magician336/lingdoc/issues/259) 的范围）。
- 不在本 Spec 里裁决 #125 与 #135 的范围重叠（属产品范围裁决，见 #144 的「已知矛盾与待裁决」）。
- 不做全量重命名的「一次性大 PR」。

## Further Notes

- **与 #259 同源。** #259 说「按知识归属归位」，本 Spec 说「名字要体现知识归属」。#259 段二已经把 `internal/clienterror/catalog.go`、`operationcore` 等按知识拆开——本 Spec 补上名字这一层。
- **登记表不是新发明。** 仓里已经有两份同形态的归属声明：门禁面清单（`scripts/ci/lingdoc_gate_surfaces.py`）与施工并行面登记处（`scripts/lingdoc_parallel_surfaces.py`）。本 Spec 用第三份，走同一套校验形态。
- **命名与依赖是一件事的两面。** 本 Spec 只改名字，不改依赖。但改了名字之后，「这条边属于哪条知识」会变得可判断——这会直接影响 #144 上那张 66 条边的依赖图的可读性。
- **待裁决**：批次 6（包名万能词）需要单独设计讨论，不在本 Spec 内解决。
