# Spec：Beta 首版验收执行器与证据目录

> 发布副本：[Issue #451](https://github.com/magician336/lingdoc/issues/451)。
> 定稿依据：[#442](https://github.com/magician336/lingdoc/issues/442) §2.1（编号 **53**）｜研究底稿：`research/new-spec-candidates` @ `9ae5d9fb`（只推分支、未合入 `main`）。
> 施工并行面：**待切**——按 [#441](https://github.com/magician336/lingdoc/issues/441) §0.2，本 Spec 的仓内文档写出来之后，它的施工面才能切。
> 上游：[#452](https://github.com/magician336/lingdoc/issues/452)（54-Spec 真实浏览器 × 真实后端对偶执行器——**证据目录里要有 `browser-check.md`，浏览器对偶得先能跑**）。
> 契约依据：[`Beta首版真实验收协议.md`](../08-本轮实施方案/Beta首版真实验收协议.md)——**协议是已裁决的验收契约，本 Spec 是它的可执行化**。

## Problem Statement

Beta 首版真实验收的协议写得完整，**但协议要求的产物在仓内除协议文档外零实现**。

协议 §6（L49–L60）要求把证据保存到 `docs/08-本轮实施方案/acceptance/beta-first/<完整SHA>/<运行标识>/`，含六件产物：`README.md`、`f01-report.json`、`runtime-manifest.json`、`browser-check.md`、`restart-readback.json`、`word-check.md`。实测：**`docs/08-本轮实施方案/acceptance/beta-first/` 不存在**（`docs/08-本轮实施方案/acceptance/` 本身存在，但里面只有 G4 浏览器证据与 T16 closeout 两处）。

三处具体的缺口：

1. **断言算子表达不了协议 §3 的语义读回。** `scripts/lingdoc_mock/run_f01.py:40` 的算子清单是 `CHECK_OPERATORS = ("equals", "length", "keys", "one_of")`——四种**结构**断言。而协议 §3（L24–L33）要的是**语义**断言：「引用指向合成材料中的有效来源」「待核事项显式标记，模型不声称已读取实际未授权的材料」「候选、采纳、确认和冻结绑定正确正文版本；数据库副作用通过读回核对」「文件来自本次真实渲染和下载，文件哈希与导出记录一致」。更根本的是：**契约文件 `docs/08-本轮实施方案/contracts/workflow.json` 里的 `checks` 数组现在是 0 条**，只有 11 条散文断言——散文无法被机器判。
2. **执行器不调模型，所以它自己如实声明覆盖范围不足。** `run_f01.py` 只通过 `ProviderClient`（L412–L476）对 `--base-url` 打 HTTP；它的报告里写着 `verification_scope: "http_smoke_only"`、`provider_semantics_status: "not_verified"`（L695–L696），散文断言**全部**记为 `status: "not_run"`（L697–L701）。契约文件自己的 `mode` 也是 `specification_not_executed_against_provider`。
3. **重启脚本写死 SQLite。** `scripts/acceptance/lingdoc_full_server_restart.py:39-41` 写死 `"DB_DRIVER": "sqlite"` / `"DB_PATH": .../lingdoc.sqlite` / `"RETRIEVE_DRIVER": "sqlite"`，而协议 §1（L9）要求「持久化使用**隔离 PostgreSQL**」。

结果是：协议是一份**没人能整跑一遍**的规格。每一轮验收要手工拼证据目录、拼断言、拼环境描述——所以 [#120](https://github.com/magician336/lingdoc/issues/120) 的豁免无法解除，因为解除豁免需要**可复跑的证据**，而证据目录不存在。

## Solution

把协议 §2–§6 变成**一条可复跑命令**，产出 `docs/08-本轮实施方案/acceptance/beta-first/<完整SHA>/<运行标识>/` 六件**脱敏**产物。

- 证据目录按 **head SHA + 运行标识**命名 ⇒ 同一 head 可复跑、可比对。
- 协议 §3 的语义读回**表达为契约里的 check**，由执行器求值 ⇒ `provider_semantics_status` 才能从 `not_verified` 变成 `verified`。
- 执行环境是**隔离 PostgreSQL**，不是 SQLite。
- 产物**脱敏**——令牌、密钥与未脱敏原始响应不写入仓库。
- **状态三态严格按协议 §5**：`BLOCKED` / `FAIL` / `NOT RUN`，不以 Mock 降级宣称通过。

## User Stories

1. As a 执行者, I want one command that runs the whole F01 sequence, so that I do not hand-assemble the evidence directory every round.
2. As a 执行者, I want the evidence directory named by head SHA and run id, so that the same head's two runs can be compared and a stale directory is obvious.
3. As a 执行者, I want all six artifacts produced by the same run, so that the artifacts belong to one execution rather than being stitched together.
4. As a QA 负责人, I want the protocol §3 semantic checks expressed as machine-checkable contract entries, so that 「引用指向有效来源」 is verified rather than asserted in prose.
5. As a QA 负责人, I want a failing check to make the run FAIL, so that a run cannot pass with a broken readback.
6. As a QA 负责人, I want the report to distinguish BLOCKED, FAIL and NOT RUN, so that 「没做」 is never read as 「做了且通过」.
7. As a QA 负责人, I want the report to state its own verification scope, so that an HTTP-only run is not presented as a full verification.
8. As a 安全责任人, I want tokens, keys and raw responses excluded from the artifacts, so that committing evidence does not commit secrets.
9. As a 安全责任人, I want a guard that fails when an artifact contains an unredacted secret, so that redaction is enforced rather than remembered.
10. As a 审阅者, I want the manifest to record the database, migrations, material hashes, template, model parameters, budget and model switches, so that a run can be reconstructed from its evidence.
11. As a 审阅者, I want the restart evidence to show the same data read back after a real process restart, so that persistence is demonstrated rather than assumed.
12. As a 审阅者, I want the Word check result recorded with the Word version, Windows version and DOCX hash, so that the human check is traceable.
13. As a 项目负责人, I want a mock-mode run to be visibly not-a-pass, so that a demo run is never filed as Beta evidence.
14. As a developer, I want the existing four structural check operators to keep working unchanged, so that adding semantics does not break the current contract.
15. As a developer, I want the evidence JSON to follow the repo's existing verification-report shape, so that a reader already knows where to look.

## Implementation Decisions

1. **证据目录以协议 §6 为准：`docs/08-本轮实施方案/acceptance/beta-first/<完整SHA>/<运行标识>/`。** 本 Spec 的票面（#451）写的是仓根 `acceptance/beta-first/<SHA>/`——**两处不一致，本 Spec 裁协议**。理由：协议是**已裁决的验收契约**，票面是候选阶段的简写；且 `docs/08-本轮实施方案/acceptance/` 下已有 G4 浏览器证据与 T16 closeout 两处，位置一致。**运行标识那层保留**——同一 head 同一天可能跑多次（例如一次 BLOCKED、一次重跑通过），没有它两次运行会互相覆盖。

2. **扩 `run_f01.py` 的算子集合，不新建执行器。** `run_f01.py` 已经是 F01 的执行器，而 F01 的**步骤与断言都声明在 `contracts/workflow.json` 里**。⇒ 执行器只负责**求值**，断言的表达力归契约。理由：再建一个执行器，等于把「F01 是什么」这件事有两个家。

3. **协议 §3 的语义读回表达为契约里的 check，新增的 check kind 是领域断言，不是通用算子。** 现有四种算子都是**结构**断言（相等、长度、键集、取值之一），表达不了 §3 的四条。新增 kind：

   | kind | 家族 | 表达什么 | 对应协议 §3 的一句 |
   |---|---|---|---|
   | `ref_in` | 领域 | 一个 id 出现在另一个数组的 id 集合里 | 「引用指向合成材料中的有效来源」 |
   | `marked_or_absent` | 领域 | 待核事项要么被显式标记、要么不出现 | 「待核事项显式标记，模型不声称已读取实际未授权的材料」 |
   | `version_bound` | 领域 | 两个对象的版本/正文引用互相绑定 | 「候选、采纳、确认和冻结绑定正确正文版本」 |
   | `readback_equals` | 领域 | 读回的值等于期望值（走一次真实读） | 「数据库副作用通过读回核对」 |
   | `hash_equals` | 领域 | 文件哈希等于导出记录里的哈希 | 「文件哈希与导出记录一致」 |
   | `non_empty` | 领域 | 该值存在且非空 | 「来源定位可读」「正文非空」 |
   | `http_error` | 领域 | 这次响应**是**失败路径：错误状态码，且错误信封的 `code`（可选 `retryable`）与契约声明的一致 | 「冲突要显式」「撤权后不放行」「导出失败要显式失败」 |
   | `not_in` | 领域 | 一个 id **不**出现在另一个数组的 id 集合里（`ref_in` 的反向） | 「撤权后不放行」「可用子集没有一起被拒」 |
   | `all_non_empty` | 领域 | 集合级：该集合存在、非空，且每一项都非空 | 「依赖登记逐项齐全」 |
   | `equals_step` | 领域 | 一处的值等于**另一步骤**记录的同一处的值 | 「输入侧登记的材料哈希＝运行时读到的材料哈希」 |
   | `provenance_real` | 领域 | 该处的来源标记属于「真」的词表（`real`）；`mock` / `real_api_fake_model` 不算真 | 「整链 real；单个真实调用不代表整链」 |

   **它们是领域断言而不是「万能断言」**：每一条都对应协议里的一句可判要求，不做通用表达式语言。理由：通用断言 DSL 会变成第二个契约语言，而协议 §3 的要求是**有限且已列全**的。

   **这张表是词表的唯一出处吗？不是。** 词表本身只写在一处——`scripts/lingdoc_check_kinds.py`；执行器、契约静态校验器与这张表都从那里读。理由见决策 13。

4. **散文断言要逐条变成 check。** 契约文件现在有 **11 条散文断言**、`checks` 数组 **0 条**。本 Spec 的交付要求：散文断言**逐条**落成机器可判的 check，或**明确标注为人工语义检查**（`manual_assertions`，保留 `not_run` 状态）。⇒ 报告里 `provider_semantics_status` 从 `not_verified` 变成 `verified` 的条件是：散文断言**全部**已机器化——不是「大部分」。

5. **执行器仍然不调模型，但覆盖范围必须如实标注。** 模型调用由被测后端负责，执行器只对 `--base-url` 打 HTTP——这是**正确的分工**（执行器不该替被测系统调模型）。⇒ 但报告必须继续写 `verification_scope`；`provider_semantics_status` 的取值由「散文断言是否已机器化」决定，与「执行器是否调模型」无关。

6. **执行环境参数化：隔离 PostgreSQL 与 SQLite 双模式，且证据必须记录用的是哪个。** `lingdoc_full_server_restart.py:39-41` 写死的三个键（`DB_DRIVER` / `DB_PATH` / `RETRIEVE_DRIVER`）改为参数；`runtime-manifest.json` 记录实际用的驱动、DSN 的**脱敏形态**、与迁移版本。理由：协议 §1 要求隔离 PostgreSQL，但 SQLite 模式在**本地诊断**时仍有用——禁止它不如**标注它**。

7. **Word 检查是人工的，执行器只承载它的结论与元数据。** 协议 §4（L35–L39）要求：记录 Word 产品版本、Windows 版本与原始 DOCX SHA-256；执行者打开下载文件、改一处正文、保存、关闭并重开；「**程序库 roundtrip 不能替代此检查**」。⇒ `word-check.md` 由执行者填写，执行器提供**填写模板 + 哈希计算**（DOCX 的 SHA-256、修改后文件的哈希）并**校验必填字段齐全**，但不假装机器判定内容。

8. **脱敏由守护测试强制，不靠记得。** 协议 §6（L59）：「令牌、密钥及未脱敏原始响应不写入仓库。」⇒ 一条守护：产物里出现令牌形态的串、密钥字段或未脱敏的绝对路径 ⇒ 红。`run_f01.py` 已有 `redact()`（L73–L93）与变量替换（L314–L334），本 Spec 把它们**扩到全部六件产物**（现状只覆盖 f01-report）。

9. **证据 JSON 沿用仓内既有的验证报告体例。** `docs/08-本轮实施方案/T327-验证报告.json` 与 `T49-验证报告.json` 的顶层字段完全一致：`mode` / `note` / `head` / `base` / `head_note` / `base_note` / `passed[{id,status,evidence}]` / `not_run[{id,reason,unblock}]` / `known_limits[{id,detail,unblock?}]`。⇒ `f01-report.json` 与 `restart-readback.json` 按这个形状，**不新造第四种体例**。

10. **状态三态按协议 §5，一字不改。** 「主流程缺依赖或无法继续为 `BLOCKED`，业务行为违反断言为 `FAIL`，没有执行为 `NOT RUN`；**不以 Mock 降级宣称通过**。」⇒ 报告的总结果由**主流程**的状态决定；非主流程的反例单独列出结果与原因，**不用总平均覆盖**（协议 §6）。

11. **一个执行器产六件产物，不是六个脚本各产一件。** 协议 §6 的六件产物之间有引用关系（`f01-report.json` 的读回断言要与 `restart-readback.json` 的重启后读回对上）。⇒ 一次运行、一个进程上下文、一个 SHA。

12. **`workflow.json` 的 `mode` 字段是执行状态，不是声明。** 现在是 `specification_not_executed_against_provider`；本 Spec 落地后，真实执行应把它改为实际模式（如 `executed_against_provider`）——**改的是契约文件，不是执行器的输出**。

13. **check kind 的词表只写在一处：`scripts/lingdoc_check_kinds.py`。** 执行器（`scripts/lingdoc_mock/run_f01.py`）、契约静态校验器（`contracts/validate_artifacts.py`）与报告文案（`scripts/lingdoc_mock/run_scenario.py`）都从那里读，**不许各留一份**。理由：词表曾经分三处（执行器 10 个、校验器 4 个、本表 5 个），且三处互不一致——校验器只认 4 个结构 kind，于是「用了领域 kind 的场景断言」在静态校验里是硬错；而执行器只**数**它认识的 kind、不**拒**它不认识的，于是「拼错的 kind」会与一个认识的 kind 并排存在、被静默忽略。⇒ 收敛成单一权威后，两处硬错各归其位：未知 kind、两个 kind、多余的键**一律报错**（`CheckKindError`），不再静默。

14. **穷举校验是双向的：每个 kind 都要被契约用上，每条断言都要能被判定。** 「每个 kind 都被用上」由 `scripts/ci/test_lingdoc_check_kinds.py` 守着（闲置的 kind ＝ 携带的表达力没人用）；「每条断言都能判定」由契约静态校验器守着（F01 契约的每条断言都要与**本节点自己的 `reference_response`** 对上，对不上的断言在跑之前就被拒）。**暂时挂不上的 kind 必须自己说出来**：契约的 `check_kinds.unmounted` 逐条写明是哪个 kind、为什么挂不上、解除条件是哪个 `GAP-` 标识——这样「挂不上」是登记项，不是沉默。

15. **失败态用「失败路径节点 + `http_error` 断言」表达；F01 序列内的失败态靠两步结构。** `contracts/workflow.json` 的 24 个节点**没有任何 4xx 节点**（协议 §3 的「冲突要显式」「撤权后不放行」「导出失败要显式失败」都是失败态）。⇒ 失败路径的断言挂在 `contracts/scenarios.json` **已有的**失败路径节点上（F05-04 的 409、F06 的 409、F07 的 403、F11-10 的 409、F22-01 的 422 等）；F01 **序列内**的失败态（例如「先撤权、再取内容」）需要「先操作同一资源、再断言」的两步结构，由 #501 落地。⇒ 在那之前，涉及失败态的放行要求一律登记为未完成项（NOT RUN，`GAP-FAILURE-PATH-NODES`），**不得记为已通过**。
    **失败态的说明写在节点上**：F01 序列里凡会出现失败态的节点（F01-06 撤权、F01-11 旧候选失效、F01-13 同基线冲突、F01-21 导出被拒、F01-22 导出终态失败）都带一个 `failure_state` 字段，写清四件事：**失败态是什么**、**用哪个 kind 表达**、**断言挂在哪个现成节点**、**为什么本节点不挂**。理由：失败态的表达方式写在契约的顶层说明里，读者要读到节点的失败态时才用得上；写在节点上，答案就在那个节点旁边。**注意**：这些说明**不进 `assertions`**——`assertions` 是执行器认的「散文断言」，会变成 `manual_assertion`（`not_run`）并把 `provider_semantics_status` 拉成 `not_verified`；说明是给读者看的，不是待人工判定的验收项。

## Testing Decisions

好的测试断言的是**「执行器能回答验收者的问题」**（产物齐不齐、状态对不对、结论能不能被机器判），不是脚本内部怎么写的。

- **主接缝 —— 一次完整运行**（在合成环境上跑，真实模型与真实 Word 除外）：
  - 一次运行产出**六件**产物，缺任何一件 ⇒ 红；
  - 证据目录名是 `<完整SHA>/<运行标识>`，两次运行不互相覆盖；
  - `runtime-manifest.json` 记录了数据库驱动、迁移版本、材料哈希、模板、模型参数、预算与模型切换记录；
  - 总结果按协议 §5 的三态计算，主流程含 `FAIL` / `BLOCKED` / `NOT RUN` 时**不得**报「通过」。
- **契约接缝 —— 新增 check kind 的求值**（表驱动，沿用 `run_f01.py` 既有算子测试的形态）：每种 kind 各有成立与不成立两组用例；`ref_in` 对不存在的 id、`marked_or_absent` 对未标记的待核项、`version_bound` 对错版正文、`readback_equals` 对读回不一致、`hash_equals` 对哈希不符，都必须**判失败而不是跳过**。
- **回归**：`run_f01.py` 既有的四种结构算子行为不变；`contracts/workflow.json` 的 **24 个 step** 不变；既有 `scripts/acceptance/` 脚本的单测仍绿。
- **穷举校验（双向）**：词表里每个 kind 要么被契约用上、要么在 `contracts/workflow.json` 的 `check_kinds.unmounted` 里写明挂不上的原因与 `GAP-` 标识；契约里每条断言都要能被权威校验器接受（一个 kind、不多不少、没有多余键），且 F01 的每条断言都要与本节点的 `reference_response` 对上。
- **阴性验证（可破坏）**：把契约里 `keys` / `one_of` / `all_non_empty` / `equals_step` 的期望值破坏（少一个键、换成一个不在词表里的值、把某一项清空、让两步对不上），断言**必须转红**；只证明「对的时候是绿的」不算数。
- **脱敏守护**：产物里出现令牌形态串、密钥字段或未脱敏绝对路径 ⇒ 红；六件产物**逐一**检查（现状 `redact()` 只覆盖 f01-report）。
- **Word 结论的完整性**：`word-check.md` 缺 Word 版本 / Windows 版本 / DOCX SHA-256 / 修改后哈希 / 检查结果任一项 ⇒ 红（**只校验字段齐全，不判定内容**）。
- **模式**：
  - **跑**：合成环境 + fake 模型 + 假 provider；
  - **不跑**：真实模型调用、真实 Microsoft Word 打开与重开（协议 §4 的人工部分）、真实凭据。
- **先例**：`scripts/acceptance/` 下的既有脚本（`lingdoc_full_server_restart.py` 的「起真进程 → 读回 → 停 → 同 env 重启 → 再读回」两段式）就是本 Spec 的骨架；`docs/08-本轮实施方案/acceptance/issue196-g4-browser-20261020/` 的 README 与 `browser-evidence.json` 是证据目录与脱敏的体例参照。

## Out of Scope

- **真实模型凭据的申请与授权**、**WSL2 与隔离 PostgreSQL 的环境搭建**——执行前置，不是本 Spec。
- **真实 Microsoft Word 的人工检查**——本 Spec 只承载结论与元数据，**不判定内容**。
- **浏览器侧的对偶执行**——归 [54-Spec](https://github.com/magician336/lingdoc/issues/452)；本 Spec 负责「目录里要有 `browser-check.md`」这一位。
- **[#120](https://github.com/magician336/lingdoc/issues/120) 的豁免解除本身**——属 #112 的平行路线；本 Spec 只是让解除有证据可依。
- **F01 之外的验收轮次**（F02 白盒、T15 族、G3 验收台）——各有既有执行器。
- **模型切换记账本身**——归 [55-Spec](https://github.com/magician336/lingdoc/issues/453)；本 Spec 只在 manifest 里**引用**它产出的记录。
- **把 `contracts/workflow.json` 之外的东西塞进契约**——契约只描述 F01。

## Further Notes

- 本 Spec 由 [#442](https://github.com/magician336/lingdoc/issues/442) 定稿编号 **53**。**筛选三条标准**：① 验收点能写成机器可判的断言 ✅ ② 至少解锁一张下游票（#120、#436 的 Beta 路由）✅ ③ 不与现有 G 线 Spec 重叠 ✅。
- **本 Spec 与 #235 的关系已裁决为「并列，不合并」**：#235（Spec: G3 验收台）已 CLOSED 且**无仓内文档**，它的域是**产品页面**（人点的验收矩阵界面）；本 Spec 的域是**构建期执行器 + 证据产出**（机器产的六件文件）。交付形态、消费者、验收方式都不同。
- **票面 #451 的证据目录路径与协议不一致**（票面写仓根 `acceptance/beta-first/<SHA>/`，协议写 `docs/08-本轮实施方案/acceptance/beta-first/<完整SHA>/<运行标识>/`）。本 Spec 裁协议（决策 1）；**#451 的票面需同步更正**。
- **`run_f01.py` 的文件名被三处写死**（CI 触发路径、`py_compile` 清单、`docs/08-本轮实施方案/T15-执行器场景驱动.md:59,63`）⇒ 本 Spec 若重命名或拆出子模块，这三处要同步；**本 Spec 不重命名它**（扩算子即可）。
- **依赖边**：上游是 #452（54-Spec）⇒ 原生边 `#451 blocked_by #452`（已加）。边档：验收未满足档。
- **一个已知的仓内不一致**：`governance_real_http.py:382` 把 G4 的 `G4-BROWSER-01` 记为 `NOT RUN`，理由「No Edge/CDP endpoint was supplied to this HTTP runner.」——这说明仓内**已经有过「HTTP 与浏览器分两次跑」的形态**（54-Spec 沿用它）。

- **判定口径**：本 Spec 按 [`验收判定口径.md`](../08-本轮实施方案/验收判定口径.md) **v1** 判定——
  **验收点 = 一个可单独核验、可单独判 PASS / FAIL / NOT RUN 的原子要求**；
  `## User Stories` 每条 = 1 个验收点，`## Testing Decisions` 每条 = 1 个验收点（聚合条目按顿号 / 逗号切分成子项），
  `## Implementation Decisions` **不计入点数**、只作约束引用。
  本 Spec 的 `## Out of Scope` 与 `## Further Notes` 不计入验收点。
