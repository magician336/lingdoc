# G1–G7 Spec 与开发计划入口

本目录是完整 G1–G7 开发的当前入口。每个 Spec 都是一个独立的功能单元：开发者需要为它建立独立开发计划，按 Spec 实现、验证并提交关联 PR。Spec 之间的阻塞关系不在本文件中手工复制，以 GitHub [Issue #144 并行依赖图](https://github.com/magician336/lingdoc/issues/144) 为准。

Beta 首版验收仍沿另一条路线推进，协议见 [`../../08-本轮实施方案/Beta首版真实验收协议.md`](../../08-本轮实施方案/Beta首版真实验收协议.md)；Beta 验收通过不等于完整 G1–G7 全部完成。

横切契约 Spec [#164：LingDoc 应用层传输解耦与幂等结果边界](https://github.com/magician336/lingdoc/issues/164) 已完成实现切片，交付 PR 为 [#170](https://github.com/magician336/lingdoc/pull/170)，8 项 PR 检查全部通过，当前仍等待合并。它不属于历史 T01–T16 的 T08 章节任务；编号和状态以该 Issue、PR 及 [`../../08-本轮实施方案/Spec-164-传输中立应用边界.md`](../../08-本轮实施方案/Spec-164-传输中立应用边界.md) 为准。

目标：在 2026 年 10 月结束前完成当前开放的 G1–G7 Spec 范围。

## 阅读顺序

1. 本目录中对应的 Spec：确认目标、边界、业务规则和验收条件。
2. Issue #144：确认前置阻塞、可并行工作和交付顺序。
3. 对应 Issue 与实现 PR：确认认领、实际接口、验证证据和当前状态。
4. Beta 首版实施计划：仅在工作涉及首版验收时读取，不用它覆盖完整 G1–G7 Spec。

| 长期方向 | 参考文档 |
|---|---|
| 建项与草稿 | [G1](G1-draft-baseline.md) |
| 证据与调研 | [G2](G2-evidence-research.md) |
| 变更协调与路线图 | [G3](G3-pcc-change-coordination.md) |
| 模板与规则 | [G4 Spec](G4-spec.md)、[G4 实施计划](G4-development-plan.md)、[G4](G4-template-rules.md)、[项目模板副本](G4-project-template-copy.md) |
| 权限与审计 | [G5](G5-permissions-governance.md) |
| 质量与运行 | [G6](G6-beta-quality-operations.md) |
| 章节编辑与 AI | [G7](G7-formal-chapter-editor-ai.md) |

## 当前 Spec 总览

| 能力线 | 当前 Spec 文件 | 对应 Issue |
|---|---|---|
| AI 辅助 | [当前任务助手与动作确认](../23-Spec-当前任务助手与动作确认.md)、[AI 上下文与多轮会话](../22-Spec-AI上下文与多轮会话契约.md)、[调研智能体首轮闭环](../24-Spec-调研智能体首轮闭环.md) | #123、#124、#125 |
| G1 草稿与立项 | [草稿项目创建与立项确认](../42-Spec-G1草稿项目创建与立项确认.md) | #143 |
| G2 证据调研 | [调研任务](../34-Spec-G2调研任务与调研智能体.md)、[候选导入](../35-Spec-G2候选导入与资料锚点.md)、[主张与证据卡](../36-Spec-G2主张与证据卡.md)、[证据失效](../37-Spec-G2证据失效与生成接入.md) | #135–#138 |
| G3 变更协调 | [ChangeSet 生命周期](../30-Spec-G3-ChangeSet生命周期与ContextRevision.md)、[影响解析与稳定化](../31-Spec-G3依赖图ImpactTask与稳定化.md) | #131、#132 |
| G4 模板与规则 | [模板目录](../38-Spec-G4模板目录与版本治理.md)、[RuleSet 治理](../39-Spec-G4独立RuleSet与RuleBinding治理.md)、[规则建议](../40-Spec-G4模型辅助规则建议.md)、[模板升级交接](../41-Spec-G4模板升级与G3稳定化交接.md) | #139–#142 |
| G5 权限治理 | [角色与 Scope](../26-Spec-G5角色Scope与成员生命周期.md)、[Owner 生命周期](../27-Spec-G5Owner生命周期与转让.md)、[资料授权](../28-Spec-G5资料授权撤权与再鉴权.md)、[审计可见性](../29-Spec-G5审计与权限可见性.md)、[统一判定器](../25-Spec-G5统一权限判定器.md) | #126–#130 |
| G6 质量运行 | [运行观测与回退](../45-Spec-G6运行观测告警与暂停回退.md)、[真实试用与质量决策](../46-Spec-G6真实试用窗口与质量决策.md) | #145、#146 |
| G7 正式章节编辑 | [版本与复核](../32-Spec-G7正式章节编辑器版本与复核.md)、[AI 辅助写作](../33-Spec-G7章节AI辅助写作.md) | #133、#134 |

### 完整 G2 已发布 Spec

完整 G2 与首版减量切片分开推进。以下 Spec 不修改历史的 G2 减量 Spec（#76）和首版验收文件（#80）：

| 切片 | Spec | GitHub Issue |
|---|---|---|
| G2.1 调研任务与调研智能体 | [Spec](../34-Spec-G2调研任务与调研智能体.md) | [#135](https://github.com/magician336/lingdoc/issues/135) |
| G2.2 候选导入、ProjectAsset 与 SourceAnchor | [Spec](../35-Spec-G2候选导入与资料锚点.md) | [#136](https://github.com/magician336/lingdoc/issues/136) |
| G2.3 ResearchClaim 与 EvidenceCard | [Spec](../36-Spec-G2主张与证据卡.md) | [#137](https://github.com/magician336/lingdoc/issues/137) |
| G2.4 证据失效传播、复核与生成接入 | [Spec](../37-Spec-G2证据失效与生成接入.md) | [#138](https://github.com/magician336/lingdoc/issues/138) |

### G4 下一阶段 Spec

固定 `template-demo/1` 继续作为当前 Beta 基线；以下切片面向后续 G4 开发，不属于 #120 首版验收：

| 切片 | Spec | GitHub Issue |
|---|---|---|
| G4.8 模板目录与版本治理 | [Spec](../38-Spec-G4模板目录与版本治理.md) | [#139](https://github.com/magician336/lingdoc/issues/139) |
| G4.9 独立 RuleSet 与 RuleBinding 治理 | [Spec](../39-Spec-G4独立RuleSet与RuleBinding治理.md) | [#140](https://github.com/magician336/lingdoc/issues/140) |
| G4.10 模型辅助规则建议 | [Spec](../40-Spec-G4模型辅助规则建议.md) | [#141](https://github.com/magician336/lingdoc/issues/141) |
| G4.11 模板升级与 G3 稳定化交接 | [Spec](../41-Spec-G4模板升级与G3稳定化交接.md) | [#142](https://github.com/magician336/lingdoc/issues/142) |

### G1 下一阶段 Spec

| 切片 | Spec | GitHub Issue |
|---|---|---|
| G1 草稿项目创建、协作与立项确认 | [Spec](../42-Spec-G1草稿项目创建与立项确认.md) | [#143](https://github.com/magician336/lingdoc/issues/143) |

### G6 下一阶段 Spec

当前 G6 Python 门禁和报告属于质量证据工具；以下 Spec 面向产品级运行控制和真实试用决策：

| 切片 | Spec | GitHub Issue |
|---|---|---|
| G6 运行观测、告警与暂停回退 | [Spec](../45-Spec-G6运行观测告警与暂停回退.md) | [#145](https://github.com/magician336/lingdoc/issues/145) |
| G6 真实试用窗口、指标与扩大决策 | [Spec](../46-Spec-G6真实试用窗口与质量决策.md) | [#146](https://github.com/magician336/lingdoc/issues/146) |

## 已知歧义不能隐瞒

[完整 Beta 模拟与逻辑审计](../19-Beta六项缺口端到端使用模拟与逻辑审计.md)基于全量能力假设；其中历史 PASS 不是首版验收，也不能证明后来修改的全部子计划一致。本次保留这些原文，不冒称它们已逐项修复。

首版对证据准入、候选卡顺序、blocking 处置、稳定化前置和工作副本的处理，集中在首版计划第 7 节。以后选入尚未统一的功能时，先同步相关正文与反例，再实施；不因未来问题未定而阻塞无关的小切片。

缩范围不缩安全：无权材料不能进入任何模型上下文；来源不可用、版本失配或无效文件不能因标记“待核”而放行。任务合并、Mock 通过、真实接线和用户验收仍是不同状态。
