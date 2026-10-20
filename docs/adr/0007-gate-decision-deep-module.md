# 7. 门禁判定收成一个深模块：`decide(event) -> Decision` 是唯一决策源

**状态**：已采纳（2026-10-20，由 #298「门禁判定层改造」的实现票 #300 产生）；2026-10-21 由 #304 定稿（结构收敛完成，公开接口与最终文件清单以本文为准）

**修订关系**：本决策修订 [ADR 0006](0006-gate-surface-by-contribution.md) 的**实现结构**，**不修订其语义**；0006 定义的验证单元口径、贡献面口径、基线移动判定、治理类改动走全量与 fail-open 全部保持不变。本决策与 [ADR 0005](0005-ci-gate-by-diff-content.md) 互补：0005 给出「什么算纯格式改动」，0006 给出「基线合并后测什么」，本决策给出「判定这件事由谁做、结果以什么形状存在」。

## 背景

ADR 0006 把**决策**收敛到一处，却让**支撑知识**从三处变成四处。落地 0006 之后，仓库里有四个判定脚本，各自实现了一遍 git 原语与范围语义：

| 脚本（当时） | 职责 |
| --- | --- |
| `scripts/ci/lingdoc_change_scope.py` | 工作流级门禁的 scope 脚本 |
| `scripts/ci/lingdoc_pr_scope.py` | PR gate 的 scope 脚本 |
| `scripts/ci/lingdoc_format_scope.py` | 纯格式快判（ADR 0005 的分类器） |
| `scripts/ci/lingdoc_risk_surface.py` | ADR 0006 新增的判定器 |

具体的重复有四处：provider 根目录、contracts 目录与治理文件集被抄了两份；`changed_paths` 这个 git 原语被抄了三份；diff 范围的计算散在三个文件；门禁面分类被抄了两份。

重复的代价不是啰嗦，而是**可以互相矛盾**：两个脚本各判一次，等于门禁自相矛盾；一份知识有两个副本，改了一处忘了另一处，门禁就会在无人察觉的情况下看错地方。

0006 自己已经写下了那条纪律：「判定只读 Git：贡献面、树哈希、合并基点。**任何异常都回退到全量**（fail-open）」。但当时的实现把 fail-open 放在 `decide_or_full` 里，把严格判定放在 `decide` 里，于是同一个模块有两条入口、两种行为，调用方各自选择——这正是「各脚本各回退一次」的开始。

## 决策

### 一、一个模块，一个决策

门禁判定只有一个模块：`scripts/ci/lingdoc_gate_decision.py`。它的公开接口是：

- `decide(event, root) -> Decision`：回答一个事件，返回一个**带类型**的判定结果。
- `write_outputs(decision) -> dict[str, str]`：**唯一**把类型退化成字符串的边界。
- `report(decision) -> dict[str, Any]`：门禁发布的报告——判定结论与它选中的东西。
- `publish(event, root) -> dict[str, Any]`：门禁 scope 步骤实际执行的那一件事：判定、发布、写产物、检查 PR 的空白边界。
- `gate_conclusion(decision, workflow)` 与 `conclusion_markdown(conclusion)`：把判定变成**读者看得见**的东西——PR 页面上的 job summary 与门禁产物。
- `check_whitespace(root, diff_range)`：PR gate 的边界检查：diff 不带空白损伤。

`Decision` 是**一等类型**：它携带每个 job 的布尔量、被测面、原因、基线、被测面内容指纹、基线是否移动、被测面是否变化、是否强制全量、要跑的 provider 包、是否治理类改动。判定结果在跨进程边界之前**不以字符串形式存在**。

模块里其余的函数都是**内部实现**：diff 范围计算、改动文件、被测面推导、门禁面分类、格式快判。它们不决定任何事；判定只在 `decide` 里做一次。

### 二、fail-open 只有一条恢复路径

判定失败（范围解析不出来、提交不存在、清单读不出来）时，`decide` **不抛异常**：它返回一份「全量」的 `Decision`，并在 `reason` 里写明「risk surface undetermined, fell back to the full scope」。

理由是 ADR 0006 的原话：**门禁的目的是不漏，不是卡人**。既然恢复路径只有一条，它就必须住在判定里；否则每个调用方都会长出自己的回退，恢复路径又会散开。`reason` 是**人类可读的短语**，产物直接给人看——fail-open 这一条沿用 0006 已发布的措辞，不另造一套说法。

### 三、判定模块独占它需要的事实

以下事实只由判定模块实现，其他脚本一律调用它：

- diff 范围计算（PR 自己的范围、一次 push 报的范围）；
- 改动文件的取法；
- 树哈希与被测面推导；
- 门禁面分类；
- **格式快判的实现**（ADR 0005 的两个判据与它的兜底）。

**归属知识**（provider 根目录、contracts 目录、治理文件集、前端根）不再由判定模块自己抄一份，而是从**门禁面清单**（`scripts/ci/lingdoc_gate_surfaces.py`）读。清单是「什么属于什么」的唯一真相源；判定模块是「什么会跑」的唯一决策源。

### 四、每个 job 的布尔量

`Decision.jobs` 为清单里的每条消费关系给出一个条目：workflow、job、job 显示名、所属面、**这次跑不跑**、门禁级别、路径种类、路径集、以及该 job 是否在纯格式提交时被跳过。

这不是新造的一层抽象，而是 0006 那条纪律的形状化：给定贡献面与基线，判定模块能算出「`frontend.yml` 的 `verify` job 这次跑不跑」。工作流改成直接消费判定结论之后，这条布尔量就是被消费的东西。

### 五、最终结构：旧脚本退场

过渡期内，七个工作流级门禁曾调用旧脚本，因此本决策曾要求「新旧逐项一致」并用一组**跨实现一致性测试**守着。

#301 让工作流直接消费判定结论之后，跨实现对照就失去了对象；#304 把过渡期结束：

| 文件 | 角色 |
| --- | --- |
| `scripts/ci/lingdoc_gate_decision.py` | **判定模块**：唯一决策源，公开接口见第一节 |
| `scripts/ci/lingdoc_gate_surfaces.py` | 门禁面清单：归属知识与路径表的唯一登记处 |
| `scripts/ci/lingdoc_gate_concepts.py` | 文档与判定之间的词汇表（文档契约的唯一登记处） |
| `scripts/ci/lingdoc_required_checks.py` | ruleset 要求的必需检查名清单 |
| `scripts/ci/check_lingdoc_routes.py` | 路由契约检查器（与门禁判定无关） |

被删除的四个脚本：`lingdoc_change_scope.py`、`lingdoc_pr_scope.py`、`lingdoc_risk_surface.py`（三个薄壳）与 `lingdoc_format_scope.py`（格式快判的实现）。最后一个不是薄壳，所以它的实现**折进了判定模块**——格式快判是一项判定能力，留在一个独立文件里就等于多了一个「跑不跑」可能被决定的地方。

删除之后：

- **判定只能有一个入口**。`scripts/ci/` 下除了上表之外没有别的判定脚本，工作流只调用 `lingdoc_gate_decision.py`；守护测试 `scripts/ci/test_lingdoc_gate_scripts.py` 逐条断言这一点。
- **判定语义只有一个 seam**。判定语义的用例只在 `scripts/ci/test_lingdoc_gate_decision.py`（判定模块的公开接口）里；接线、清单与文档的守护测试各自成文件，不重复判定语义。
- 跨实现对照**变成了冻结的金标准**：旧实现给过的逐 job 结论作为字面量留在 `test_lingdoc_gate_decision.py` 的 `JobVerdictGoldenTests` 里，作为「结构变了、结论没变」的独立证据。

### 六、判定语义只有一个 seam

同一个语义在两处各测一遍，等于两份会漂移的真相。因此：

- **判定语义**只通过判定模块的公开接口（`decide` / `write_outputs` / `report` / `publish` / `gate_conclusion` / `check_whitespace`）来测，全部在 `scripts/ci/test_lingdoc_gate_decision.py`。
- **守护测试**分文件：`test_lingdoc_gate_surfaces.py`（清单与归属知识）、`test_lingdoc_workflow_wiring.py`（工作流接线）、`test_lingdoc_gate_conclusion.py`（结论发布）、`test_lingdoc_gate_contracts.py`（三方一致、必需检查名、文档契约）、`test_lingdoc_gate_workflow_triggers.py`（触发器）、`test_lingdoc_gate_scripts.py`（判定层只有一个入口）。每个文件都命名 `test_lingdoc_*.py`，否则 PR gate 的「Test CI scope scripts」步骤不会执行它。

## 不修订 0006 的什么

以下七条语义与 0006 完全一致，每一条都有夹具测试证明：

| 语义 | 0006 的口径 |
| --- | --- |
| 验证单元 | (被测面内容, 基线内容)；任一变了就重测 |
| 贡献面 | `base...head`，不是「最后一次推送的 diff」 |
| 基线移动 | 两个 merge-base 不同即视为移动 |
| 治理类改动 | 改判定脚本或门禁 workflow → 全量面 |
| fail-open | 算不出来 → 回退全量，产物写明理由 |
| 格式快判 | 沿用 0005：只 Go；含编译期指令的文件永不算格式改动；判不出来一律不算格式提交 |
| 产物三字段 | `surface` / `reason` / `base` 的字段名与含义不变 |

## 影响

- `scripts/ci/lingdoc_gate_decision.py` 是判定模块：公开接口是 `decide`、`write_outputs`、`report`、`publish`、`gate_conclusion`、`conclusion_markdown`、`check_whitespace`。
- `scripts/ci/lingdoc_change_scope.py`、`scripts/ci/lingdoc_pr_scope.py`、`scripts/ci/lingdoc_risk_surface.py`、`scripts/ci/lingdoc_format_scope.py` **删除**；前三个是过渡期薄壳，最后一个的实现折进判定模块。
- 门禁面清单的 YAML 依赖改为惰性导入：工作流级门禁在没装依赖的 job 里也能读到归属知识。
- 判定模块自带离线入口：给定一份事件描述与一个工作树即可打印判定结论，不依赖网络。

## 风险与限制

- **过渡期有两套判定来源**——这条已经结束（#304）。现在只有一套判定来源，恢复路径也只有一条。
- **`write_outputs` 的 `run` 键需要调用方的路径表**。同一个 `Decision` 对不同工作流给出不同的 `run`，因为不同工作流看的路径不同；这是事实，不是缺陷。
- 判定模块变长是**有意的**：它把四个脚本的知识收进一个模块，模块变大而重复消失。若将来它继续变大，应拆的是**内部实现**，不是它的公开接口。
- **语义级风险仍然存在**。基线若改了 PR 依赖的对外契约（API、迁移、规则）而文件不同，包级重叠判不出来；基线合并之后仓库级矩阵不再重跑，这类风险没有自动化兜底，仍靠人工评审兜底。**本次只保证它被记录，不假装已关闭**：证据位置是 [ADR 0006 的「风险与限制」](0006-gate-surface-by-contribution.md#风险与限制)与本节；关闭它需要独立决定，不在 #298 的范围内。
- **反向依赖仍然存在**。贡献改了某个包的接口、而基线里有别的包依赖它时，仓库级矩阵不在这次重跑范围内（代码没变时它按贡献是否变化决定是否跑）；main 合并后的自身 CI 是最后一道防线。**同样只被记录，未被关闭**：证据位置同上（ADR 0006「风险与限制」与本节）。

## 后续（#301）：工作流直接消费判定结论

本决策写下时，七个工作流级门禁仍在调用旧脚本。#301 把它们改成直接消费判定结论，并落定三件事：

- **逐 job 的布尔量以扁平键发布**（`job_frontend_verify`）。job 级引用（`needs.scope.outputs.<name>`）必须先在该 job 的 `outputs:` 里声明，而 GitHub workflow schema 的 output id 只接受 `^[_a-zA-Z][a-zA-Z0-9_-]*$`——带点的键连声明都过不了 schema。可读的 `job.frontend.verify` 形式保留在产物里。
- **纯格式快判折进判定结果**。工作流的 `if:` 只读一个布尔量，不再组合 `run` 与 `format_only`；lint 类 job 照跑（lint 是格式的裁判），这仍是 0005 的口径。
- **判定范围统一为贡献面 `base...head`**，不再取最后一次推送的 diff。0006 的贡献面口径没有变，是过渡脚本的「最后一次推送」口径被淘汰了。

## 后续（#302 / #308 / #309 / #304）

- **#302：每个门禁都发布判定结论**。`gate_conclusion` 与 `conclusion_markdown` 是为此新增的公开接口：八个门禁发布同样的三个字段（`surface` / `reason` / `base`），并为每个没有运行的检查写明原因。
- **#308：push 的被测面**。push 没有 PR 贡献面，它的被测面是**这次推送自己的 diff**（`before...after`），基线是推送开始前的那个提交；创建分支时 `before` 全零，基线就是新 head 的父提交。验证单元仍是 (被测面内容, 基线内容)。push 不走纯格式快判。
- **#309：门禁在 PR 上始终出现**。PR gate 的 job 不用顶层路径过滤把自己藏起来——**门禁 job 永远创建，跑不跑由 job 条件决定**，必需检查因此有一个稳定的名字，不会永远 Pending。缩面发生在 job 条件里，不在触发器里。
- **#304：判定层收成一个深模块**。四个旧脚本删除，格式快判折进判定模块，判定语义归一到 `test_lingdoc_gate_decision.py` 一个 seam，文档与本决策同步定稿。
