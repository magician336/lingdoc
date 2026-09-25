# Beta 六项缺口开发计划

> 本文件是总入口；六条能力线已拆为独立子文件，便于单独领取、评审和更新。
> 前置假设：T01–T16 已完成并通过各自验收。
> 对照基线：[T01–T16 与 Beta 差距清点](17-T01-T16与Beta差距清点.md)。

## 子计划

| 编号 | 子计划 | 文件 |
|---|---|---|
| G1 | 建项语义与草稿阶段 | [G1-draft-baseline.md](beta-gap-development-plans/G1-draft-baseline.md) |
| G2 | 证据工作台与调研智能体 | [G2-evidence-research.md](beta-gap-development-plans/G2-evidence-research.md) |
| G3 | PCC、ChangeSet、依赖图与技术路线图 | [G3-pcc-change-coordination.md](beta-gap-development-plans/G3-pcc-change-coordination.md) |
| G4 | 模板、规则与问题治理 | [G4-template-rules.md](beta-gap-development-plans/G4-template-rules.md)；[项目级模板副本与规则编辑](beta-gap-development-plans/G4-project-template-copy.md) |
| G5 | 完整权限、治理与审计 | [G5-permissions-governance.md](beta-gap-development-plans/G5-permissions-governance.md) |
| G6 | Beta 质量、运行与效果评估 | [G6-beta-quality-operations.md](beta-gap-development-plans/G6-beta-quality-operations.md) |

## 补充开发计划

| 编号 | 补充计划 | 文件 |
|---|---|---|
| G7 | 正式章节编辑器与 AI 辅助写作 | [G7-formal-chapter-editor-ai.md](beta-gap-development-plans/G7-formal-chapter-editor-ai.md) |

G7 聚焦 active 项目的正式章节编辑、ChapterVersion、证据关联、AI 候选和内容确认。它消费 G1–G5 已定义的项目、证据、变更、模板和权限对象，不改变六项缺口的阶段门；G6 负责对 G7 的编辑器、AI、并发、权限、DOCX 和真实试用进行验收。

## 1. 总体执行原则

六项计划不是六个一次性大 PR，而是六条可交接的能力线。每条能力线都按以下顺序推进：

1. 先冻结领域对象、状态机、权限边界和失败语义；
2. 交付可注入的小接口、OpenAPI/场景样例和数据库迁移；
3. 用固定数据完成单域验证；
4. 再接入真实 WeKnora、模型、数据库和前端；
5. 最后接入跨域流程和真实验收 fixture。

共同要求：

- 领域服务只写自己的数据；跨域通过小接口、事件或一致读交接；
- 所有异步结果携带 project、revision、版本和 correlation/causation 信息；
- Mock、real_api_fake_model、real 三种运行模式显式标记；
- 每项计划同时交付 API adapter、加载/错误/空状态和最小可操作界面；
- 静态契约通过不等于真实能力通过，必须分别记录真实服务、真实模型、文件和权限验证；
- 不把 P5 生产增强（临时授权、服务账号、水印、组织审批流）混入 Beta 主路径。

## 2. 总体依赖与阶段

```text
G1 建项基线 ──────┐
                  ├─> G2 证据与调研 ──┐
G5 权限治理 ──────┘                   │
                                      ├─> G3 PCC/变更协调
G4 模板与规则 ────────────────────────┘
                                      └─> G6 Beta 质量与运行
```

- **阶段 A：真相对象**：G1、G2、G4、G5 可并行建模；G1 先提供正式项目基线。
- **阶段 B：变更闭环**：G3 消费 G1 的 Context Revision、G2 的证据对象、G4 的模板版本和 G5 的权限。
- **阶段 C：Beta 验证**：G6 汇总 G1–G5 的真实接口，建立完整验收、指标和回退证据。

建议每条能力线拆为 3–5 个小 PR；每个 PR 只引入一个可复现用户结果或一个不可变数据不变量。

---

## 3. 交付顺序与阶段门

| 阶段门 | 必须完成 | 通过证据 |
|---|---|---|
| M0 逻辑契约收敛 | G1/G2/G3/G4/G5/G6 的跨域补充条款（DraftTemplateChange、ProvenanceRecord、template_upgrade、稳定化聚合、权限矩阵、分层历史读取、RouteNode 必需性、指标字典、Owner 条件写入、ImpactTask 幂等） | 12 个审计问题逐项映射到对象、状态、权限、迁移和失败样例；端到端模拟无未决矛盾 |
| M1 真相对象可写 | G1.1、G2.3、G3.1、G4.1、G5.1 | 迁移、状态机、契约样例、单域测试 |
| M2 单域闭环 | G1.3、G2.4、G3.3、G4.4、G5.2 | 固定数据成功/失败场景、审计和副作用断言 |
| M3 跨域闭环 | G2.5、G3.5、G4.5、G5.4 | 新想法/资料变化/模板变化均能影响到确认和导出 |
| M4 Beta 验证 | G6.1–G6.5 | 真实服务、模型、文件、权限、观测、回退和用户试用记录 |

M0 未通过前，只能做不产生正式跨域写入的静态契约和原型；任何阶段门未通过时，只能宣称完成相应能力线的内部验证，不能宣称完整 Beta。

## 4. PR 与任务卡模板

每个后续任务卡至少填写：

```text
任务：G__-__
用户结果：
领域对象 / 状态：
输入 / 输出契约：
依赖任务：
界面切片：
成功场景：
关键失败场景：
副作用与幂等断言：
真实依赖：
交接证据：
PASS / FAIL / NOT RUN：
未覆盖风险：
```

建议 PR 标题使用：

- feat(G1): establish draft baseline semantics
- feat(G2): add evidence provenance workflow
- feat(G3): add changeset impact coordination
- feat(G4): add versioned template rule evaluation
- feat(G5): complete scoped project authorization
- test(G6): add beta acceptance and rollback evidence

## 5. 计划边界

本计划把前一份差距清点的六类缺口转成可执行能力线，但不把计划本身当作需求已冻结或代码已完成。开始每条能力线前，应先补充：

- 真实业务模板和资料来源；
- WeKnora 当前可调用接口及权限行为；
- 数据库/队列/模型运行环境；
- 领域负责人、消费者和评审人；
- 预计投入量、阻塞条件和退出标准。
