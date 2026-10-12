# Spec：G2 调研任务与调研智能体

> 完整 G2 的第一条开发切片。总设计见 [G2 证据工作台与调研智能体](beta-gap-development-plans/G2-evidence-research.md)。已实现的依据引用减量切片见历史 Spec [G2-spec.md](beta-gap-development-plans/G2-spec.md)，不在本 Spec 中重做。

## Problem Statement

科研人员通常从一个模糊问题开始调研。当前工作区能检索项目资料，但不能把问题、范围、排除条件、查询轮次和结果层级保存成可复核的调研任务。用户无法知道一次检索使用了什么范围，也无法区分发现入口、背景线索和可进入证据流程的候选。

## Solution

提供 `ResearchTask` 和 `ResearchRun`。调研智能体先把自然语言问题整理成可编辑任务卡，再在用户确认后执行受控检索。每轮结果保存查询、来源目录、授权范围、Context Revision、结果层级和当前可用性。智能体可以提出查询词、来源和待验证问题，但不能把摘要、模型推断或自由对话直接写成正式证据。

## User Stories

1. As a 科研人员, I want 用自然语言描述调研问题, so that 我不用先理解检索表单。
2. As a 科研人员, I want 编辑目标、范围、排除条件和输出形式, so that 调研边界由我决定。
3. As a 项目成员, I want 在 draft 项目中保存调研任务, so that 我能先整理方向再立项。
4. As a 项目成员, I want 看到每轮查询词、同义词和排除词, so that 我能理解结果如何产生。
5. As a 项目成员, I want 批准后再执行调研, so that Agent 不会静默扩大范围或启动外部调用。
6. As a 项目成员, I want 暂停、取消和重试任务, so that 中断不会丢失已有结果。
7. As a 项目成员, I want 区分发现入口、背景线索、候选证据和未读摘要, so that 不会把搜索摘要误当作证据。
8. As a 项目成员, I want 查看来源目录、提供方、检索时间和授权范围, so that 一次运行可以被复核。
9. As a 项目成员, I want 结果绑定固定的 Context Revision 和 asset scope, so that 旧结果不会自动混入新任务。
10. As a 项目成员, I want Agent 推荐查询词和来源, so that 检索成本降低。
11. As a 项目成员, I want Agent 解释推荐理由和缺口, so that 我可以判断是否继续。
12. As a 项目成员, I want 看到任务失败原因和重试入口, so that 外部服务故障可恢复。
13. As a 项目成员, I want 旧任务在项目变化后标为 stale, so that 我不会误用旧授权或旧研究条件。
14. As a 项目成员, I want 相同幂等键安全重试, so that 不会重复执行或重复记账。
15. As a 项目成员, I want 任务结果可被后续候选导入流程引用, so that 调研和证据整理连续起来。

## Implementation Decisions

- 复用现有 Workspace ApplicationService、HTTP 入口、WeKnora/KnowledgeGateway 检索能力和 G5 统一权限判定器；不新建绕过租户策略的检索通道。
- `ResearchTask` 保存用户意图：目标、问题类型、范围、排除条件、期望输出、关联项目/章节和状态。
- `ResearchRun` 保存一次执行：任务版本、查询轮次、来源目录版本、提供方、执行时间、模型/工具、固定 Context Revision、asset scope、成员权限快照和结果集合。
- 任务状态使用 `draft`、`approved`、`running`、`completed`、`failed`、`cancelled`、`stale`；结果状态和任务状态分开保存。
- 结果层级至少支持 `candidate_evidence`、`background_lead`、`discovery_entry`、`unread_search_summary`。层级不表示学术结论正确。
- draft 项目允许任务、检索和候选保存；禁止创建 ResearchClaim、EvidenceCard 或正式章节引用。
- Agent 的输出全部是候选或提醒。Agent 不得直接创建 SourceAnchor、确认 EvidenceCard、确认 ResearchClaim 或写入正式章节。
- 每次运行显示本轮引用范围；跨权限、敏感资料和业务写入沿用现有确认机制。
- 任务取消、超时、限流和外部来源失败必须保留运行记录及失败原因；已保存结果可读，未完成部分不能伪装为完成。
- 旧运行返回时，如果 basis 落后于项目 revision、授权或来源目录，标为 `stale`，不能覆盖当前任务或章节。
- 任务、运行和结果写入使用幂等键、预期版本和审计记录；关键写入与审计遵循 G5 fail-closed 策略。

## Testing Decisions

- 最高测试 seam 是 Workspace ApplicationService/HTTP 入口，测试用户可见任务、运行、结果、状态和副作用，不检查内部表结构。
- 使用现有检索 adapter 构造无结果、部分结果、未授权结果、限流、超时、取消和乱序返回。
- 必验成功：创建任务、编辑后批准、执行多轮、查看层级、暂停/恢复、取消、幂等重试、重启读回。
- 必验失败：draft 创建 Claim/Card、越权资料检索、摘要直接升级证据、旧 basis 覆盖新任务、重复运行产生重复副作用。
- 真实接线需要记录 WeKnora、来源提供方、模型、授权范围和运行账本；Mock 只用于领域失败路径。
- 前端检查任务卡可编辑、运行状态、结果层级、失败原因、stale 提示和恢复入口。

## Out of Scope

ImportCandidate、ProjectAsset、SourceAnchor、ResearchClaim、EvidenceCard、PDF/OCR 定位、正式章节写入、正式证据确认和完整 Beta 首版验收。

## Further Notes

本 Spec 只新增完整 G2 的调研任务和调研智能体能力，不修改历史 G2 减量 Spec 或 #80 的验收文件。完成后结果必须能被 G2 候选导入 Spec 消费。
