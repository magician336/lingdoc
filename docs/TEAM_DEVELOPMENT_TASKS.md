# G1–G7 开发任务总表

> 更新日期：2026-10-13

本文件是当前 G1–G7 功能开发的任务入口。任务以 GitHub Issue 和仓库中的 Spec 为准；历史 T01–T16 任务池不再作为新增功能的领取入口，相关实现和验收证据仍保留在 Git 历史及 [`docs/08-本轮实施方案/`](08-本轮实施方案/) 中。

开发前验收矩阵、事务/审计契约、Draft→Ready 门禁和当前 head 证据要求，统一按 [`docs/agents/development-rules.md`](agents/development-rules.md) 执行；本文件保留任务地图、依赖和领取/完成卡片。

## 1. 共同规则

### Spec 是什么

每个 Spec 都是一个独立的功能单元。它说明该功能的目标、用户流程、业务规则、领域对象、接口边界、权限、失败处理、验收条件和不在范围内的内容。Spec 不是任务标题，也不是讨论记录。

每个 Spec 都必须独立建立开发计划，明确：

- 实现切片和代码边界；
- 需要复用的现有 Workspace/ApplicationService、HTTP 或前端入口；
- 前置依赖和可并行工作；
- 成功、失败、权限和恢复场景；
- 验证命令、验收证据和未覆盖风险。

### 依赖与时间

- Spec 之间存在阻塞关系，统一参照 GitHub [Issue #144 并行依赖图](https://github.com/magician336/lingdoc/issues/144)。
- 不按 Issue 编号机械串行开发；前置契约明确且不违反依赖时，可以并行推进。
- 当前开放范围目标是在 **2026 年 10 月结束前**完成。
- Beta 首版真实验收与完整 G1–G7 开发并行；验收协议见 [`08-本轮实施方案/Beta首版真实验收协议.md`](08-本轮实施方案/Beta首版真实验收协议.md)。

### 统一交付流程

1. **开发前**：在飞书说明准备认领的 Issue、计划、前置依赖和预计交付；随后在 GitHub Issue 中认领并补充负责人。
2. **开发中**：按 Spec 实现，复用已约定的领域服务和 HTTP/前端 seam；依赖或范围变化时同步更新 Issue 和 Spec。
3. **开发结束**：提交 PR，PR 必须关联对应 Issue，并写明实现范围、验证命令、结果和未覆盖项。
4. **合并后**：PR 合并到 `main` 后，确认 CI 和验收证据完整，再关闭对应 Issue。合并前不得提前关闭 Issue。

### 本轮 15 天协作节奏与质量门禁

本轮从 **2026-10-17 至 2026-10-31** 执行，共 15 天；当前范围共 23 个 Spec，平均目标约为每天 1.5 个。主要开发目标在 10 月 29 日前完成，10 月 30—31 日用于集中验收和返工。

- 同时处于开发中的 Spec 不超过 2 个；一个 Spec 完成并提交 PR 后，可以在同一天串行开始并完成下一个 Spec。
- 同一天完成的多个 Spec 必须分别提交 PR；次日分别 review，不合并成一个无法逐项验收的 PR。
- 每天 9:00 review 前一天提交的 PR。需要 review 的 PR 必须点名 `magician336`，并将 PR 状态设置为 `Open`。
- 本轮由项目负责人负责验收、代码 review 和最终合并。
- Review 意见分为三类：
  - **Blocker**：必须修改，修复并复审前不得合并；
  - **Required**：原则上在本轮修改，并在 PR 中回复处理结果；
  - **Suggestion**：记录为后续任务，不阻塞本次合并。
- 被打回的问题由原作者修改并提供新的验证证据。Reviewer 说明原因、影响和修复方向，不直接替作者重写核心代码。

#### `lingdoc-health-scoring` 使用要求

项目级 Skill `lingdoc-health-scoring` 是本轮开发的统一设计与质量反馈工具：

1. **建立 Spec 后、写代码前**：在开发计划完成后运行一次，记录六个维度的评分、主要风险和拟采取的措施。
2. **准备提交 PR、代码完成后**：再次运行一次，说明与开发前相比的变化，并附测试、构建、运行或验收证据。
3. **Reviewer review 时**：项目负责人使用同一 Skill 复核评分并提出修复建议。评分不是机械合并线，但低分项必须在 PR 中说明原因、风险和处理计划；复杂性控制、模块深度或证据与反馈明显失控时，不得合并。

六个评分维度及其边界如下：

| 维度 | LingDoc 健康度 | 仓库契合度 |
| --- | --- | --- |
| 复杂性控制 | LingDoc 内部变化放大和认知负荷 | LingDoc 改动是否扩散到宿主仓库多处 |
| 模块深度 | LingDoc 内部模块和接口质量 | LingDoc 与宿主模块的边界、依赖和信息隐藏 |
| 演化式设计 | Spec、增量实现、重构和回退 | 是否能沿仓库既有流程独立演化、合并和发布 |
| 战略执行 | LingDoc 自身路线、债务和设计投资 | LingDoc 路线与主仓库路线、Issue、负责人是否一致 |
| 一致性 | LingDoc 代码、文档、注释和术语 | LingDoc 与主仓库 API、目录、CI、部署和术语是否一致 |
| 证据与反馈 | LingDoc 的测试、验收、观测和恢复 | 集成测试、跨模块构建、发布和真实运行证据 |

## 2. 当前任务地图

Issue #144 是依赖图，不是功能实现任务。下表列出功能 Issue 与对应 Spec；详细阻塞关系以 Issue #144 为准。

| 能力线 | 功能任务 | Spec | Issue |
|---|---|---|---|
| AI 基础 | 当前任务助手与动作确认 | [`23-Spec-当前任务助手与动作确认.md`](06-灵档产品开发规划/23-Spec-当前任务助手与动作确认.md) | [#123](https://github.com/magician336/lingdoc/issues/123) |
| AI 基础 | AI 上下文与多轮会话契约 | [`22-Spec-AI上下文与多轮会话契约.md`](06-灵档产品开发规划/22-Spec-AI上下文与多轮会话契约.md) | [#124](https://github.com/magician336/lingdoc/issues/124) |
| AI 基础 | 调研智能体首轮闭环 | [`24-Spec-调研智能体首轮闭环.md`](06-灵档产品开发规划/24-Spec-调研智能体首轮闭环.md) | [#125](https://github.com/magician336/lingdoc/issues/125) |
| G1 | 草稿项目创建与立项确认 | [`42-Spec-G1草稿项目创建与立项确认.md`](06-灵档产品开发规划/42-Spec-G1草稿项目创建与立项确认.md) | [#143](https://github.com/magician336/lingdoc/issues/143) |
| G2 | 调研任务与调研智能体 | [`34-Spec-G2调研任务与调研智能体.md`](06-灵档产品开发规划/34-Spec-G2调研任务与调研智能体.md) | [#135](https://github.com/magician336/lingdoc/issues/135) |
| G2 | 候选导入与资料锚点 | [`35-Spec-G2候选导入与资料锚点.md`](06-灵档产品开发规划/35-Spec-G2候选导入与资料锚点.md) | [#136](https://github.com/magician336/lingdoc/issues/136) |
| G2 | 主张与证据卡 | [`36-Spec-G2主张与证据卡.md`](06-灵档产品开发规划/36-Spec-G2主张与证据卡.md) | [#137](https://github.com/magician336/lingdoc/issues/137) |
| G2 | 证据失效传播、复核与生成接入 | [`37-Spec-G2证据失效与生成接入.md`](06-灵档产品开发规划/37-Spec-G2证据失效与生成接入.md) | [#138](https://github.com/magician336/lingdoc/issues/138) |
| G3 | ChangeSet 生命周期与 ContextRevision | [`30-Spec-G3-ChangeSet生命周期与ContextRevision.md`](06-灵档产品开发规划/30-Spec-G3-ChangeSet生命周期与ContextRevision.md) | [#131](https://github.com/magician336/lingdoc/issues/131) |
| G3 | 依赖图、ImpactTask 与稳定化 | [`31-Spec-G3依赖图ImpactTask与稳定化.md`](06-灵档产品开发规划/31-Spec-G3依赖图ImpactTask与稳定化.md) | [#132](https://github.com/magician336/lingdoc/issues/132) |
| G4 | 模板目录与版本治理 | [`38-Spec-G4模板目录与版本治理.md`](06-灵档产品开发规划/38-Spec-G4模板目录与版本治理.md) | [#139](https://github.com/magician336/lingdoc/issues/139) |
| G4 | 独立 RuleSet 与 RuleBinding 治理 | [`39-Spec-G4独立RuleSet与RuleBinding治理.md`](06-灵档产品开发规划/39-Spec-G4独立RuleSet与RuleBinding治理.md) | [#140](https://github.com/magician336/lingdoc/issues/140) |
| G4 | 模型辅助规则建议 | [`40-Spec-G4模型辅助规则建议.md`](06-灵档产品开发规划/40-Spec-G4模型辅助规则建议.md) | [#141](https://github.com/magician336/lingdoc/issues/141) |
| G4 | 模板升级与 G3 稳定化交接 | [`41-Spec-G4模板升级与G3稳定化交接.md`](06-灵档产品开发规划/41-Spec-G4模板升级与G3稳定化交接.md) | [#142](https://github.com/magician336/lingdoc/issues/142) |
| G5 | 统一权限判定器 | [`25-Spec-G5统一权限判定器.md`](06-灵档产品开发规划/25-Spec-G5统一权限判定器.md) | [#126](https://github.com/magician336/lingdoc/issues/126) |
| G5 | Owner 生命周期与转让 | [`27-Spec-G5Owner生命周期与转让.md`](06-灵档产品开发规划/27-Spec-G5Owner生命周期与转让.md) | [#127](https://github.com/magician336/lingdoc/issues/127) |
| G5 | 资料授权、撤权与再鉴权 | [`28-Spec-G5资料授权撤权与再鉴权.md`](06-灵档产品开发规划/28-Spec-G5资料授权撤权与再鉴权.md) | [#128](https://github.com/magician336/lingdoc/issues/128) |
| G5 | 角色、Scope 与成员生命周期 | [`26-Spec-G5角色Scope与成员生命周期.md`](06-灵档产品开发规划/26-Spec-G5角色Scope与成员生命周期.md) | [#129](https://github.com/magician336/lingdoc/issues/129) |
| G5 | 审计与权限可见性 | [`29-Spec-G5审计与权限可见性.md`](06-灵档产品开发规划/29-Spec-G5审计与权限可见性.md) | [#130](https://github.com/magician336/lingdoc/issues/130) |
| G6 | 运行观测、告警与暂停回退 | [`45-Spec-G6运行观测告警与暂停回退.md`](06-灵档产品开发规划/45-Spec-G6运行观测告警与暂停回退.md) | [#145](https://github.com/magician336/lingdoc/issues/145) |
| G6 | 真实试用窗口、指标与质量决策 | [`46-Spec-G6真实试用窗口与质量决策.md`](06-灵档产品开发规划/46-Spec-G6真实试用窗口与质量决策.md) | [#146](https://github.com/magician336/lingdoc/issues/146) |
| G7 | 正式章节编辑器版本与复核 | [`32-Spec-G7正式章节编辑器版本与复核.md`](06-灵档产品开发规划/32-Spec-G7正式章节编辑器版本与复核.md) | [#133](https://github.com/magician336/lingdoc/issues/133) |
| G7 | 章节 AI 辅助写作 | [`33-Spec-G7章节AI辅助写作.md`](06-灵档产品开发规划/33-Spec-G7章节AI辅助写作.md) | [#134](https://github.com/magician336/lingdoc/issues/134) |
| G7 | 章节比较、复核包与依据可追溯 | [`43-Spec-G7正式章节比较复核与依据可追溯.md`](06-灵档产品开发规划/43-Spec-G7正式章节比较复核与依据可追溯.md) | [#337](https://github.com/magician336/lingdoc/issues/337) |

## 3. 领取卡片

认领 Issue 时，在 Issue 评论中补充以下信息：

```text
负责人：
Spec：
计划摘要：
前置 Issue / 当前依赖：
实际消费者与评审人：
预计交付时间：2026-10-__
实现切片：
验证场景：成功 / 失败 / 权限 / 恢复
风险与未决问题：
```

## 4. 完成卡片

提交 PR 时必须说明：

- 关联的 Issue 和 Spec；
- 已实现的用户可见行为与未实现边界；
- 复用的领域服务、HTTP 或前端入口；
- 测试/验证命令和准确结果；
- 权限、失败恢复、审计和数据副作用证据；
- 是否需要后续 Issue 或文档更新。

PR 合并到 `main` 后，维护者根据 PR、CI 和验收证据关闭对应 Issue。若功能拆成多个 PR，Issue 只能在最后一个交付切片完成后关闭。

## 5. 历史任务的对应关系

T01–T16 是上一阶段的基础接入、契约验证、工作区、生成、交付和连续流程任务。它们的接口和证据仍可复用，但不再代表当前 G1–G7 的功能边界。需要追溯时阅读 [`docs/08-本轮实施方案/T01-T16-全景状态与下一步.md`](08-本轮实施方案/T01-T16-全景状态与下一步.md)；新增开发不得只领取一个旧 T 编号而跳过对应 Spec 和 Issue。
