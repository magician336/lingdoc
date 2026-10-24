## Problem Statement

LingDoc 的 Beta 首版已经有了代码基础，也已经有了一条**证据生产机制**（一条命令产出协议 §6 的六件产物，缺依赖记 `BLOCKED`、违反断言记 `FAIL`、没执行记 `NOT RUN`）。但**没有放行结论**：机制的两轮试跑绑在中间提交上，F01 契约序列在第 1 个必需前置节点就 `BLOCKED`，Word 人工检查、合成材料哈希、隔离 PostgreSQL 的重启读回**都还是 `NOT RUN`**，真实模型调用**账本读到 0 行**。

于是现在处在最坏的状态：**能力看起来齐了，结论是空的**。放行标准写在文档里（三个连续结果、同一已合并 SHA、同一套两章材料、七个关键断言、四项必须实测），但没有任何一条被真正执行过并留下可判定的记录。团队因此既不能对用户说「可以试用了」，也不能说清「差哪一步」——因为缺口散在一堆 `NOT RUN` 里，没有被收敛成一份可执行的收尾计划。

用户要的是**一个诚实的、可复现的 Beta 首版放行结论**：要么证明三个连续结果真的在真实环境下跑通了，要么精确说出「哪一条缺依赖、谁去补、解除条件是什么」。不接受把「代码已合入」当作「Beta 已完成」，也不接受用 Mock 或单次调用冒充真实链路。

## Solution

把 Beta 放行判定收敛到**唯一一个接缝**——F01 契约执行序列——在当前已合并 SHA 上，用**同一套两章材料**，把 §4 的三个连续结果（持续写作 → 依据回看 → 最小变更复核）**串起来真跑一遍**，逐条断言地判 PASS / FAIL / NOT RUN。

具体做四件事：

1. **补上唯一的纯代码缺口**：F01 的 24 个节点 = 原 23 个业务步骤 + **activation-read 前置请求**；前置请求依赖的变量捕获链缺失，导致主流程在第 1 个节点就 `BLOCKED`。补上它，主流程才走得完。（**2026-10-24 更新**：#545 落地后节点数为 **28** ＝ 原 23 个业务步骤 ＋ activation-read 前置 ＋ 四个 model-use 许可节点 `F01-06b…F01-06e`；许可节点是为建立「允许用这份资料做模型生成」这条显式同意记录而存在的，**不是业务步骤**。）
2. **把三个连续结果放进同一个接缝**：不为每一行另造执行路径——三个接缝意味着三份「怎么跑」的知识要各自维护、各自腐烂；一个接缝意味着放行结论只有一份真相。
3. **把 §4 每行必须一起通过的反例**、§8 的七个关键断言（正常保存 / 重试 / 冲突 / 撤权 / 来源变化 / 旧候选返回 / 导出失败）、四项必须实测（真实模型选段改写 / 真实授权来源 / 重启读回 / 真实导出文件在 Word 打开编辑保存重开）逐条落到可 grep 的断言上。
4. **把人工与环境前置逐项登记**：真实模型凭据、隔离 PostgreSQL、合成材料经 WeKnora 真实接入、Word 人工检查、WeKnora 依赖——每一项写清「为什么缺 + 谁来补 + 解除条件」，**不允许留空，也不允许用 Mock 降级宣称通过**。

最终产出：一轮绑定当前 SHA 的证据目录（六件产物齐全），一个逐条的放行结论表，以及一份明确的「尚未放行项」清单。

## User Stories

1. As a 项目负责人, I want the three continuous results to run against one already-merged SHA with one fixed set of two-chapter materials, so that the release verdict describes a single real state of the product rather than a moving target.
2. As a 项目负责人, I want the release verdict to be decided by the F01 contract sequence rather than by a step counter, so that a run which stops halfway can never be reported as a pass.
3. As a 项目负责人, I want the activation-read prerequisite to be captured, so that the main flow is no longer blocked at its very first node.
4. As a 项目负责人, I want every one of the seven critical assertions (normal save, retry, conflict, revocation, source change, stale candidate return, export failure) to actually execute, so that I know the failure paths were exercised rather than assumed.
5. As a 项目负责人, I want the three continuous results to be judged by the same seam, so that no row of the result table passes on the strength of a different run.
6. As a 项目负责人, I want a dependency gap to be reported as BLOCKED rather than as a pass or a silent skip, so that I can tell "we could not run it" apart from "it ran and it worked".
7. As a 项目负责人, I want a business rule violation to be reported as FAIL with the offending values, so that the fix has something concrete to point at.
8. As a 项目负责人, I want every NOT RUN entry to carry a reason and an unblock condition, so that an unfinished item is actionable instead of merely absent.
9. As a 项目负责人, I want the run to use real API, permission, queue, file, model, WeKnora and DOCX dependencies, so that the verdict survives contact with production wiring.
10. As a 项目负责人, I want Mock and fake models to be usable for diagnosis but never acceptable as pass evidence, so that a green run cannot be an artifact of a stub.
11. As a 评审人, I want a single real model call never to be reported as a real end-to-end chain, so that partial realism is not mistaken for full realism.
12. As a 评审人, I want the synthetic materials to have actually gone through WeKnora ingestion, processing and retrieval, so that pre-seeded chunks cannot masquerade as parsed material.
13. As a 评审人, I want the material hashes recorded both as input and as asserted output, so that "the same set of materials" is verifiable rather than claimed.
14. As a 评审人, I want the restart read-back executed against both SQLite and an isolated PostgreSQL, so that durability is not inferred from the convenient database.
15. As a 评审人, I want the model call ledger to contain at least one real call row and at least one model-switch row, so that the switch accounting itself is validated on real data.
16. As a 评审人, I want the Word check to be a human opening, editing, saving, closing and reopening the downloaded file, so that the export is proven usable by the tool the user actually has.
17. As a 评审人, I want the Word check to record the Word product version, the Windows version, the original DOCX hash and the post-save hash, so that the check is auditable after the fact.
18. As a 评审人, I want a programmatic round-trip to be explicitly insufficient for the Word check, so that a passing library test cannot be passed off as a passing Word check.
19. As a 评审人, I want the browser check to state which actions ran through the UI and which ran through the API, so that "the page works" is not claimed from a screenshot.
20. As a 评审人, I want the browser, HTTP and post-reload snapshots compared to each other item by item, so that a stale front end is caught.
21. As a 评审人, I want a deliberately injected inconsistency to turn the run red, so that the comparison is proven able to fail.
22. As a 评审人, I want the runtime manifest to pin the database and migration version, material hash, template version, model parameters, budget and every model switch, so that a run can be reproduced or explained.
23. As a 评审人, I want the pre-run report fields to be mandatory and non-empty, so that a run cannot start with unrecorded environment facts.
24. As a 评审人, I want tokens, keys and unredacted raw responses kept out of the repository, so that the evidence directory is safe to publish.
25. As a 评审人, I want toolchain errors and business assertion failures attributed separately, so that a broken toolchain is not mistaken for a broken product.
26. As a 评审人, I want the non-main-flow counter-examples listed with their own results, causes and owners rather than averaged into the headline number, so that one strong number cannot hide a failed scenario.
27. As a 第二名开发者, I want an independent reproduction to be recorded as an outstanding item with its unblock condition, so that the gap is visible without being mistaken for a blocker on the first usable release.
28. As a 第二名开发者, I want to reproduce with a clean work tree, a fresh test identity, a fresh isolated database and an independent report, so that the reproduction is not a copy of the first runner's state.
29. As a 试用者, I want to know exactly which steps helped me, how long my active time was, how many extra confirmations I made, whether anything was lost, and whether I could get back to the evidence, so that the trial record is useful rather than ceremonial.
30. As a 试用者, I want the trial record to hold individual cases and counts only, so that no efficiency claim is made from a single success.
31. As a 试用者, I want a full Beta never to be declared from one successful run, so that the promise stays smaller than the evidence.
32. As a 维护者, I want the release evidence to live under a directory keyed by the full SHA and a run identifier, so that a later run never overwrites an earlier one.
33. As a 维护者, I want the six artifacts to be produced in one execution, so that no artifact can come from a different run.
34. As a 维护者, I want each artifact checked for redaction and structure, so that publishing the directory does not leak content or lose its shape.
35. As a 维护者, I want the release state to be readable from the registry and the path map, so that "what is done" has exactly one authoritative source.
36. As a 维护者, I want every shared path to have exactly one owner and any other claimant recorded as a borrower, so that a merge can never make the registry refuse to load.

## Implementation Decisions

- **判定接缝唯一**：Beta 放行只在 F01 契约执行序列上判定。三个连续结果按 §4 的顺序串行跑在这一个序列里，不为「持续写作」「依据回看」「最小变更复核」各建一条执行路径。理由（Ousterhout 的深模块原则）：接缝越多，每处「怎么跑」的判断就越分散、越容易各自漂移；一个接缝让放行结论只有一份真相。
- **资料标识必须参数化，且不传时必须报错**：F01 契约在绑定资料处把标识写成字面量，执行器没有对应参数入口 ⇒ 真实环境里只要该标识不存在，流程就在该节点断掉，后面全部节点都跑不到。⇒ 标识由运行时输入决定，**不传时明确报错，不静默回退到演示标识**（静默回退会把「跑过了」变成假象）。这是「真跑到底」的**最前置**一项。
- **断言能力要补齐到能表达放行要求（审计结论，67 条放行要求中 16 条当前无法判定）**：只读取证审计（#482 评论）把放行要求拆成 **67** 条原子要求后，分类是**覆盖成立 32 · 缺 check kind 16 · 人工/环境/记录项 19**。现有 10 个算子全是「成功响应上的结构断言」，表达不了四类要求：
  - **失败态 / 错误路径**：契约 24 个节点的 `expected_http` 只有 200/201/202，**没有任何 4xx 节点** ⇒ 冲突(409)、撤权(403)、导出失败无法判定 ⇒ 需要「期望状态码 + 错误体语义」的断言能力。
  - **失效 / 取代（staleness）**：来源重解析后旧定位失效、旧候选被取代 ⇒ 需要「已被取代 / 不再新鲜」的断言能力。
  - **授权随时间变化**：这是**结构缺口，不只是加断言** —— 撤权是「先撤后断言」，而契约是**线性单遍**流程，没有「二次操作同一资源 + 事后断言」的结构。
  - **real / provenance**：必须逐段溯源才能证明「整链 real」，单个真实调用不代表整链 ⇒ 需要逐段溯源的断言能力。
  另需补两处表达力：**「不在」**（现 `ref_in` 是单向的，只能表达「在」）、**集合级断言**（现 `non_empty` 是单点的，表达不了「集合里每项都非空 / 元素齐全」）。同时**启用两个已实现但闲置的算子**（`keys` 键集、`one_of` 取值之一 —— 契约 50 条里一条都没用），并**修正 53-Spec 决策 3 的表**（它只列 5 个 kind，实现已加第 6 个 `non_empty`；新增的能力也要补进那张表）。
  ⇒ 这四类能力与两处表达力补齐后，16 条「缺 check kind」里的大部分才可能判 PASS。**但这不改变「判定接缝唯一」** —— 新能力仍然是**同一个 F01 契约执行序列**上的断言，不是新接缝。
- **activation-read 前置必须补**：F01 当前 24 个节点 = 原 23 个业务步骤 + activation-read 前置请求。缺 `template_id` 捕获链 ⇒ 主流程在第 1 个节点 `BLOCKED` ⇒ 后面 23 个节点一个都没真跑到。补它是唯一的纯代码前置工作，且**不得为维持旧步数关闭生产门槛**。（**2026-10-24 更新**：#545 落地后为 **28** 个节点 ＝ 原 23 个业务步骤 ＋ activation-read 前置 ＋ 四个 model-use 许可节点 `F01-06b…F01-06e`。）
- **计数不是判定**：`completed_steps` 只能作为辅助信号，不参与判定。放行结论由逐条断言得出；主流程里出现任何 `FAIL` / `BLOCKED` / `NOT RUN` 都不算通过。
- **三态语义固定**：主流程缺依赖或无法继续 = `BLOCKED`；业务行为违反断言 = `FAIL`；没有执行 = `NOT RUN`。三者**互不替代**，且都**不允许**用 Mock 降级成通过。每个 `NOT RUN` 必须带「为什么 + 解除条件」。
- **真实性分档并逐条标注**：每条断言标注 `real` / `mock` / `fake`。放行判定只认 `real`。**单个真实调用不代表整条链路为 real** —— 链路级的真实性由每一段各自的标注共同证明。
- **材料同一性可核**：同一套两章材料的哈希必须在**输入侧（运行前记录）与断言侧（运行时核对）**两处出现，两处不一致即 `FAIL`。材料须固定版本，且实际经过 WeKnora 的接入、处理与检索 —— 直接把分块记录灌入库**不算**解析通过。
- **双库重启读回**：SQLite 与隔离 PostgreSQL 各跑一次重启读回（项目、章节、来源绑定、交付文件、下载哈希）。只跑 SQLite 不构成放行。
- **模型账本必须非空**：真实模型调用账本须读到非 0 行，且含至少一行 `switch`（模型切换记账）。被限流的尝试要保留记录；替换模型完成其生成与后续引用、采纳、确认、冻结、文件检查后，才可用于通过结论。限流以外的余额不足 / 不可用故障保留 `BLOCKED`，不自动扩大已授权的切换条件。
- **第二人复现不是首版放行门槛，但必须登记**：这一条是**本 spec 的口径裁定**，因为三份权威文档在此正面冲突——验收协议（产品负责人确认，决策票 #115）的 §1 写「一位执行者完成整套验收，不要求第二位开发者独立复现」，§6 的总门槛逐项列全且不含第二人复现；而 18 号 Beta 开发计划的 §3「本期必须」列、§8、§11 一致要求「另一个成员能按仓库记录复现」。**裁定：以专门定义放行判定的协议为准**——它是穷举列举、有决策票、且是「通过」这一定义本身；18 号是开发计划的叙述。⇒ 第二人复现**不构成**首版放行的关票条件，但**必须**作为未完成项登记（原因 + 解除条件），且**不得**被记为已通过。后续裁定：18 号 §3 / §8 / §11 的措辞按协议 §6 校正，另开票处理。
- **Word 检查不可被程序替代**：只要求 Microsoft Word（不要求 WPS）。执行者打开下载文件、修改一处正文、保存、关闭并重新打开，检查修改保留、正文 / 标题层级 / 引用 / 基本格式仍可用。程序库 round-trip **不能**替代此检查。必填字段：Word 产品版本、Windows 版本、原始 DOCX SHA-256、保存后 SHA-256、检查结果。
- **浏览器检查要声明动作归属**：真实浏览器核对项目、资料、候选预览与取消、采纳、章节编辑 / 确认、冻结、下载交互，并注明哪些动作经 UI、哪些经 API。**只展示成品不能称为整个页面流程通过**。浏览器侧 / HTTP 侧 / 刷新后读回三份快照逐项相等；能注入不一致并转红。
- **运行前字段不允许留空**：完整 SHA、环境与依赖版本、数据库 / 迁移版本、模板版本、合成材料哈希、模型提供方、模型名称、参数、调用预算、模型切换记录 —— 全部由执行者在运行前填入，不允许留空。
- **证据目录形态固定**：绑定完整 SHA 与运行标识的目录下产出六件产物（README、逐步报告、运行时清单、浏览器检查、重启读回、Word 检查），**同一次执行、同一 head、同一运行标识**。令牌、密钥与未脱敏原始响应不入库。工具链错误与业务断言失败分别归因。**协议制定完成不等于验收已执行或已通过。**
- **共享路径一个拥有者，其余借用**：已登记且票面写明 owner 的面优先保留 owner；后来的「移交」改为借用（owner 保持拥有，借用方在 summary 记一笔借用事实）。这条同时约束登记处、路径图与票面三处副本必须同步。
- **不宣称超出证据的东西**：首版通过不等于所有长期扩展能力完成；不凭一次成功计算效率提升；不宣称完整 Beta；不新建任务平台 / 质量平台 / 全量共同规范。

## Testing Decisions

- **好测试的判据**：只测外部可观察行为，不测实现细节。放行层的一个好测试 = 一条可 grep 的断言名 + 一条可复现命令 + 绑定的当前 SHA，三者齐备才算 PASS 谓词。
- **判定单位**：一个原子可核验要求（口径 v1）。一行 = 一条要求 = 一个可独立判定的结论。
- **被测模块**：F01 契约的 24 个节点与其 50 条断言（执行器支持 **10** 种 check kind：`equals` / `ref_in` / `readback_equals` / `non_empty` / `length` / `version_bound` / `marked_or_absent` / `hash_equals` / `keys` / `one_of` —— 其中 **`keys` 与 `one_of` 契约里一条都没用**，属闲置能力）；证据目录生成器与脱敏守卫；对偶执行器的三方快照比对；重启读回执行器；F01 执行器的领域断言扩展；模型调用账本与迁移。
- **先例**：仓内已有 F01 契约（24 节点 / 50 条 check）、证据守护测试（32 项）、对偶执行器守护测试（32 项，其中「注入不一致能红」是现成的阴性验证先例）、模型调用账本用例（10 项）、迁移成对性检查、登记处与棘轮守护。**新增断言一律沿用这些形状。**
- **每条新断言都要有阴性验证**：故意破坏输入 ⇒ 必须转红。只证明「能过」不算验证。
- **真实优先**：放行层用真实依赖；Mock / 假模型只允许出现在诊断路径与单元测试里，且必须与放行路径物理分开。
- **静态 SKIP = FAIL**：任何被静默跳过的断言按失败计。这条是口径 v1 的硬要求，也是本 spec 存在的理由之一。
- **守护测试与验收脚本分离**：验收脚本不得放进 CI（它有副作用、会写出证据目录）；CI 只跑守护测试。

## Out of Scope

- **19 张开放 Spec 的全量收口与判定矩阵 / 判定器**（#458）：那是质量轨的覆盖面工作，不是 Beta 首版放行的门槛。本 spec 只判 §4 的三个连续结果与 §8 的放行要求。
- **PF-7 / PF-9 / PF-12 / PF-13 各面的实现内容本身**：它们各自有票与 Spec（审计链完整性、Owner 冻结归因、工作台入口与路由、模板目录与版本治理），本 spec 不重做它们的交付，只约束与本轮共享路径的归属裁决。
- **第二人独立复现作为放行门槛**：见 Implementation Decisions 的口径裁定 —— 它是记录项，不是关票条件。
- **模板市场、通用图谱 / 角色引擎、通用 ACL 配置引擎、实时协同 / CRDT、全格式富文本、质量平台与灰度管理**：均为「本期暂不做」。
- **效率提升的量化宣称与「完整 Beta」的宣称**：只记个案与计数。
- **让协议回归旧步数**：不得为了 23 步的历史口径关闭 activation-read 前置门槛。

## Further Notes

- **本 spec 的接缝裁定需要用户复核**：上面「判定接缝唯一」与「第二人复现不是门槛」两条是管理会话的裁定（用户已授权「需要人工裁定的地方你来决策」）。若你认为第二人复现应当是首版硬门槛，改判的成本是：协议 §6 的总门槛要加一项、18 号 §8 与 §11 要回改、#480 的验收清单要把 AC-BETA-12 从记录项升为关票条件 —— 一次改完，不影响本 spec 的其余部分。
- **上游依赖**：[#480](https://github.com/magician336/lingdoc/issues/480)（Beta 首版放行执行票，已钉住 head）与 [#481](https://github.com/magician336/lingdoc/issues/481)（补 activation-read 变量捕获链，唯一纯代码前置项）。本 spec 是它们的规格来源，票由 `/to-tickets` 拆出。
- **共享路径的既有裁决**：PF-7 与 PF-11 争 `internal/container/lingdoc.go` 与 `internal/lingdoc/workspacecore/model.go` ⇒ 归 PF-7（已登记、票面写明、且 PF-7 即将开工），PF-11 改借用；PF-11 与 PF-13 争 `contracts/openapi.json` 与 `context_ports_test.go` ⇒ 归 PF-11（先合者），PF-13 改借用；两者都新建的 `template_governance_adapter_test.go` ⇒ 内容取 PF-13 的超集版并补上 PF-11 新增的 `ProjectPorts.Resources` 字段，owner 归 PF-11。合并顺序：PF-11 先、PF-13 后。
- **覆盖度审计（已完成）**：「放行断言覆盖度审计」（每一条放行要求 ↔ F01 50 条断言的逐条映射，标出缺 check kind 的项）已完成，结论回填在 #482 的评论里；据其发现新增的「断言能力要补齐到能表达放行要求」决策已写在上方 Implementation Decisions，其余「新增 kind」的实施由 #499（补齐断言表达力）与 #501（扩展契约形状支持两步结构）承接。
- **文档副本**：**发布副本在 tracker（#482）**，本文件是它的仓内副本（编号 57，接在现有 Spec 之后）。**两边内容以 spec 为准** —— spec 更新时本文件要跟着更新；本文件不反过来成为权威。
- **实施期间的发现（2026-10-23）**：只读取证发现 F01 契约把资料标识写死为演示标识，执行器无对应参数入口 ⇒ 真实跑必在绑定资料那一步断掉，即使 activation-read 前置补齐也跑不到底。已据此**新增一张实施票**（资料标识参数化）并插在 activation-read 之后、材料接入之前 —— 后者依赖前者（真实接入产出的标识必须能传进执行器，否则「接进来的材料」与「流程操作的材料」不是同一个）。同时确认：**「处理就绪」已经存在且已被契约断言**，缺的是产生它的那一步（真实接入），不是断言本身；且仓内唯一的「材料」脚本走的是直接灌库路线，正是验收协议 §1 明令禁止的，**不可复用**。

- **覆盖度审计（只读取证，回填在 #482 的评论里）**：放行要求 **67** 条（US 侧 54 + TD 侧 13，按口径 v1 拆成原子要求；spec 与 §4/§8/协议指向同一要求时只算一行，来源列并列）⇒ **覆盖成立 32 · 缺 check kind 16 · 人工/环境/记录项 19**。19 条人工/环境/记录项**全部已在 #480 的缺口 2–7 与本 spec 的裁定里登记**，不重复登记。
- **判定口径**：[`docs/08-本轮实施方案/验收判定口径.md`](https://github.com/magician336/lingdoc/blob/main/docs/08-本轮实施方案/验收判定口径.md)（v1）：判定单位 = 一个原子可核验要求；PASS 谓词 = 可 grep 用例名 + 可复现命令 + 绑当前 head + 命令在当前 head 跑通；静默 SKIP = FAIL；NOT RUN 必须登在 `not_run[]` 并带「为什么 + 解除条件」；纯机器判定，关票即完成。
- **放行标准的权威出处**：验收协议 `Beta首版真实验收协议.md`（§1 人员环境输入 / §2 执行顺序 / §3 通过条件 / §4 Word 检查 / §5 模型切换与失败记账 / §6 总门槛与证据目录）、18 号 Beta 六项缺口开发计划（§3 本期范围 / §4 三个连续结果与反例 / §8 何时算首版可试用）、`Beta首版真实验收协议.md` 的决策票 #115。