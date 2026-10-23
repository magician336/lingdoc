# Spec：模型调用账本与模型切换记账

> 发布副本：[Issue #453](https://github.com/magician336/lingdoc/issues/453)。
> 定稿依据：[#442](https://github.com/magician336/lingdoc/issues/442) §2.1（编号 **55**）｜研究底稿：`research/new-spec-candidates` @ `9ae5d9fb`（只推分支、未合入 `main`）。
> 施工并行面：**待切**——按 [#441](https://github.com/magician336/lingdoc/issues/441) §0.2，本 Spec 的仓内文档写出来之后，它的施工面才能切。
> 相关：[#146](https://github.com/magician336/lingdoc/issues/146)（46-Spec G6 真实试用窗口与质量决策——本 Spec 交付它要的数据层）、[Beta 首版真实验收协议](https://github.com/magician336/lingdoc/issues/451) §2.1 / §5。

## Problem Statement

每次真实模型调用**不留账**。

`lingdoc_generation_runs` 是生成运行的幂等与状态机表（`migrations/sqlite/000021_lingdoc_generation_runs.up.sql:1-18`，18 列），它的列里**没有任何一列与 provider / 模型 / 参数 / 预算沾边**。助手轮次倒是带着模型信息——`AgentTurn` 有 `ModelID` / `RunMode` / `InputTokens` / `OutputTokens`（`internal/lingdoc/workspacecore/agent_research_model.go:441-459`）——但它是**一次助手轮次的记录**，落库时写进通用对象表的 **JSON blob**（`lingdoc_g2_objects.payload_json`，`internal/lingdoc/workspacecore/agent_object_storage.go:16-22,33-41`）⇒ **不可按列查询、不可聚合**。

模型参数**完全不落库**：温度与最大 token 数是内存里的硬编码（`internal/lingdoc/workspace/agent_model.go:90`）。

**模型切换无处可查**：全仓搜 `switch_model` / `model_switch` / `切换模型` / `fallback_model` / `model_override` —— **0 命中**。没有任何地方记录「为什么切、切了哪些步骤受影响」。

后果具体有三件：

1. **一次演示跑的产物和一次真模型跑的产物在库里长得一样。** #146 的验收点「区分 mock、real_api_fake_model 和 real，只有真实样本进入效果汇总」（`46-Spec:20`）要靠 `runtime_mode=real` 判定，而 `runtime_mode` 此刻只存在于 G6 试用窗口的记录里，**不在每一次调用上**。
2. **协议 §2.1 与 §5 的可追溯要求无法兑现。** 「这一轮跑的是什么模型、什么参数、为什么换的」没有落点。
3. **词表有两个主人。** `run_mode`（`internal/lingdoc/candidateadoption/candidate_shape.go:30-52`，取值 `real` / `mock` / `real_api_fake_model`）与 `runtime_mode`（`internal/lingdoc/workspacecore/runtime_control.go:166-174`，同值）是**两套独立的同值词表**——同一份含义住在两个模块里。

## Solution

让每次真实模型调用留下**账本**，让模型切换留下**原因与受影响步骤**。

- **一张账本表**，每行是一次模型调用（`kind = call`）或一次模型切换（`kind = switch`）。
- **账本行是事实**：提供方、模型、参数、预算、token 数、耗时、状态、错误码。
- **切换行也是事实**：从哪个模型切到哪个模型、为什么切、**受影响的步骤**。
- **词表收口**：`run_mode` / `runtime_mode` 合为一个服务端拥有的常量。

## User Stories

1. As a 项目负责人, I want every model call to record which provider and model answered, so that a demo run is never presented as a real-model result.
2. As a 项目负责人, I want the parameters of each call recorded, so that "it used temperature 0.2" is a fact and not a recollection.
3. As a 项目负责人, I want the token counts and the budget of each call recorded, so that the cost of a turn is attributable.
4. As a QA 负责人, I want 「this run was a real model」 to be answerable by querying the ledger, so that real and fake samples can be separated mechanically.
5. As a QA 负责人, I want a model switch to be recorded with its reason, so that a mid-run change is never invisible.
6. As a QA 负责人, I want a model switch to name the steps it affected, so that the blast radius of a switch is known.
7. As a QA 负责人, I want 「混入 mock」 to be detectable from the ledger alone, so that the G6 试用窗口's effect statistics cannot be contaminated.
8. As a developer, I want the ledger shape to follow the one already proven in this repo, so that I do not invent a third table shape.
9. As a developer, I want the run-mode vocabulary owned by one server-side constant, so that the two same-valued word lists in the codebase stop being two.
10. As a developer, I want the model parameters captured at the call site rather than read from a second configuration, so that the ledger cannot disagree with what was actually sent.
11. As a 审阅者, I want a ledger row to point at the run or turn it belongs to, so that a model call can be traced to the product action that caused it.
12. As a 审阅者, I want a failed call recorded as a failure with its error code, so that a retry is not mistaken for a first success.

## Implementation Decisions

1. **新建一张账本表，不扩 `lingdoc_generation_runs`。** 后者是**生成运行**的幂等与状态机（`CreateOrReplay` / `Claim` / `Complete` / `Fail`，`internal/lingdoc/generation/store.go:98-345`），而模型调用是它的**上游事实**；助手轮次那条链又完全不走这张表（走 `lingdoc_g2_objects`）。⇒ 扩它会让一个表同时回答两个问题，且助手侧仍然没有账。

2. **账本表的列形状沿用 `lingdoc_rule_suggestion_runs`——本仓已验证的「一次模型调用一行」先例。** 那张表的形状是：一次调用一行，含 `model_id`、`run_mode`、`prompt_version`、`input_tokens`、`output_tokens`、`duration_ms`、`error_code`、`status`（`migrations/sqlite/000037_lingdoc_full_workflow.up.sql:43-56`；`migrations/versioned/000116_lingdoc_full_workflow.up.sql:25-38`；Go 行结构 `internal/lingdoc/templategovernance/schema.go:136-161`）。本 Spec 在它之上**扩展** `provider`、参数与预算三组列，**不复用那张表**（那张表的语义是「规则建议运行」，不是通用调用账本）。

3. **一张表带 `kind` 列（`call` / `switch`），不拆两张表。** 模型切换与它引发的后续调用是同一件事的两面：拆开就得跨表 join 才能回答「这次调用用的模型为什么换了」。这与仓内既有的通用记录表形态一致（`lingdoc_g6_records` 的 `kind` 形态，`internal/lingdoc/workspacecore/trial_window.go:198`）。

4. **`provider` 与参数在调用点派生并落账，不另设配置源。** 温度与最大 token 数此刻是内存硬编码（`internal/lingdoc/workspace/agent_model.go:90`）⇒ 账本记的是**实际发出去的那一份**，从调用点取。理由：账本与配置若分两处，就可能出现「账本说 0.2、实际发的是 0.7」——而那正是账本要消灭的那类不可信。

5. **`run_mode` 与 `runtime_mode` 收口为一个服务端常量。** 现在是 `internal/lingdoc/candidateadoption/candidate_shape.go:30-52` 与 `internal/lingdoc/workspacecore/runtime_control.go:166-174` 两套独立的同值词表。⇒ 词表搬到一个包里、两边引用它。**取值集合不变**（`real` / `mock` / `real_api_fake_model`），所以现有的消费方（`internal/lingdoc/generation/action.go:357-368`、`selected_rewrite.go:517-518`、`selected_rewrite_review.go:370-395`、验收脚本 `scripts/lingdoc_mock/quality_evidence.py:10`）行为不变。

6. **预算只记不策略。** 账本记**预留预算与实际预算**（沿用验收预算账本的语义：`internal/lingdoc/workspace/generation_acceptance_budget.go:51-80` 的 `ReservedNanoUSD` / `ActualNanoUSD`），但**不做配额、不做扣费、不做熔断**。理由：预算是账本要记的事实，预算策略是产品决策，两者混在一起会让「记账」这件事被「管账」的复杂度拖住。

7. **不把 `AgentTurn.payload_json` 里的模型信息当账本。** 它不可查询、不可聚合、不可按 provider 过滤——账本的价值恰恰在于**能查**。

8. **账本行关联到它引发的那次产品动作。** 一次模型调用要么来自一次生成运行、要么来自一次助手轮次 ⇒ 账本行带一个**来源引用**（运行 id 或轮次 id）+ 它的种类。这让「一个产品动作花了多少 token、用了哪个模型」可以直接 join 出来。

9. **两方言同步，加列同步 Go 内嵌 DDL。** SQLite 与 PostgreSQL 的既有差异必须照抄既有形态：id `TEXT` vs `UUID`、tenant `INTEGER` vs `BIGINT`、JSON `TEXT` vs `JSONB`、时间 `DATETIME DEFAULT CURRENT_TIMESTAMP` vs `TIMESTAMPTZ DEFAULT now()`、唯一约束内联 vs 具名 `CONSTRAINT`（对照 `migrations/sqlite/000021:1-18` 与 `migrations/versioned/000100:1-18`）。**并且**：仓内存在**Go 内嵌 DDL**（`internal/lingdoc/templategovernance/schema.go:28-41` 的 sqlite 版与 `:72-85` 的 postgres 版），若本 Spec 的表也走内嵌形态，三处（`migrations/sqlite/`、`migrations/versioned/`、Go 内嵌 DDL）必须同步改；守护测试 `internal/database/migration_sqlite_versioned_schema_test.go:50`（表清单）、`:90`（`versionedSQLiteColumns` 列映射）、`:107`（`expectedSQLiteMigrationVersion`）也要跟着更新。

10. **切换记录包含「受影响的步骤」。** 一次切换影响的不是「下一个调用」而是**一段流程**——受影响的步骤用结构化条目记（步骤标识 + 该步骤的模型变化），不是自由文本。

## Testing Decisions

好的测试断言的是**「账本能回答什么问题」**，而不是「表有几列」。

- **主接缝 —— 账本本身**（数据库层）：
  - 一次模型调用产生**一行** `kind = call`，提供方 / 模型 / 参数 / 预算 / token 数 / 耗时 / 状态齐全；
  - 一次模型切换产生**一行** `kind = switch`，含 from / to / 原因 / 受影响步骤；
  - 一次失败调用记为**失败**并带错误码，不记成成功；
  - 账本行可按 `run_mode = real` 过滤出来，且 **mock 与 real_api_fake_model 不在其中**；
  - 账本行可关联到它引发的那次产品动作。
- **词表接缝 —— 收口后的常量**：
  - `run_mode` 与 `runtime_mode` 现在指向**同一个**服务端常量（静态断言：两处不再各自定义取值集合）；
  - 模型适配器不写裸字符串（既有形态：purpose / agent 词表的同类守护）。
- **回归**：现有 `run_mode` 的消费方（生成动作、选段改写、选段评审、验收脚本）行为不变；`lingdoc_rule_suggestion_runs` 与 `lingdoc_generation_runs` 的读写不变；迁移守护（表清单、列映射、版本常量）随新表更新后仍绿。
- **模式**：模型调用走仓内既有的 **fake / mock 模式**；**不跑真实 provider**。预算与 token 的断言用 fake 模式下的确定性输入。
- **先例**：`lingdoc_rule_suggestion_runs` 的形状与它的行结构测试是本 Spec 两个接缝的模型；`internal/lingdoc/workspacecore/agent_research_model.go:441-459` 的 `AgentTurn` 字段是账本行要覆盖的字段集合的参照。

## Out of Scope

- **预算的配额、扣费与熔断策略**——本 Spec 只记账。
- **provider 的路由与降级**——「先试 A 再退 B」是模型调用策略，不是账本。
- **模型能力评测、模型选型、提示词优化**。
- **试用窗口纪律**（责任人、人工裁定、P2 补救、关键场景复跑）——归 #146；本 Spec 只提供它要的数据层。
- **把 `lingdoc_generation_runs` 或 `lingdoc_rule_suggestion_runs` 改造成通用调用账本**——本 Spec 新建表，不动既有表。
- **真实 provider 路径的可用性**。
- **为历史调用补账**——账本从本 Spec 落地之日起记，不追溯。

## Further Notes

- 本 Spec 由 [#442](https://github.com/magician336/lingdoc/issues/442) 定稿编号 **55**，是本批**最低优先**的一张。
- **降级条件**（#442 裁决）：若施工时发现体量不足以支撑独立 Spec，降级为 **#146 的验收点 + #120 的执行前置**，不开独立 Spec。**降级必须在写仓内文档之前判定**——本仓内文档写出来了，所以降级判定的时点已过；**若要降级，须先关掉本 Spec 并改写 #146 的验收点**，不能让它半落地。
- **迁移编号不在本 Spec 定死**：取号必须看最新 `main` 已占用的编号（sqlite 最新 `000053`、versioned 最新 `000132`）——**注意 PF-7（审计链完整性）已预留 `000054` / `000133`**（见 #441 方案 §2.1），两处不能撞号。
- **依赖边**：本 Spec **无上游**；解锁 #120 §2.1 / §5 的可追溯要求与 #146 的「模型与运行模式」验收点。
- **一个已知的仓内不一致**：PF-3 的方案档写 `expectedSQLiteMigrationVersion` 从 51 改到 52，而当前 head 已是 **53**（`000053_lingdoc_project_archive` 已合入）。写本 Spec 的迁移时**以实际文件为准**，不要沿用 PF-3 文档里的数字。
