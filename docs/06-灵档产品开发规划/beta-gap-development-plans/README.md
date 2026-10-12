# G1–G7 设计参考：不是全量开工清单

第二阶段先按[首版减量实施计划](../18-Beta六项缺口开发计划.md)选择范围。该计划替代旧的全量推进顺序；本目录长文保留为未来设计参考，不会因为写着“本轮交付/必须”而自动变成本期任务。

## 阅读顺序

1. 首版实施计划：本期必须、触发后再做、暂不做，以及三条可复现用户结果。
2. 将要实现的局部 G 设计：只读取本切片需要的对象与边界，不自动领取整条能力线。
3. 对应实现 PR 的具体基线、接口、样例与验证：独立评审确认后用于开发，不能用未来蓝图覆盖现有运行时契约。

| 长期方向 | 参考文档 |
|---|---|
| 建项与草稿 | [G1](G1-draft-baseline.md) |
| 证据与调研 | [G2](G2-evidence-research.md) |
| 变更协调与路线图 | [G3](G3-pcc-change-coordination.md) |
| 模板与规则 | [G4 Spec](G4-spec.md)、[G4 实施计划](G4-development-plan.md)、[G4](G4-template-rules.md)、[项目模板副本](G4-project-template-copy.md) |
| 权限与审计 | [G5](G5-permissions-governance.md) |
| 质量与运行 | [G6](G6-beta-quality-operations.md) |
| 章节编辑与 AI | [G7](G7-formal-chapter-editor-ai.md) |

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
