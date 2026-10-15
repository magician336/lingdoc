# Spec：G2 证据失效传播、复核与生成接入

> 完整 G2 的第四条开发切片。总设计见 [G2 证据工作台与调研智能体](beta-gap-development-plans/G2-evidence-research.md)。

## Problem Statement

资料重解析、授权撤回、主张变化、项目 Context Revision 变化和异步任务乱序都会让旧证据失去当前适用性。当前减量 G2 已能复核章节来源，但完整 G2 还需要把失效传播到 SourceAnchor、EvidenceCard、ResearchClaim、章节和生成任务，并让正式生成只消费当前可确认的证据。

## Solution

建立显式依赖和局部失效传播。来源变化先影响直接引用的锚点和证据卡，再沿依赖影响主张、章节和交付。系统区分 `NEEDS_REVIEW` 与 `STALE`，保留历史对象和原因。生成上下文创建时重新鉴权、重新检查版本和确认范围，只允许当前有效的 `CONFIRMED` EvidenceCard；候选和待核材料可以用于带标记的草稿，但不能成为正式依据。

## User Stories

1. As a 项目成员, I want 看到某张卡为什么需要复核, so that 我知道下一步。
2. As a 项目成员, I want 来源重解析后保留旧版本, so that 历史不会被静默改写。
3. As a 项目成员, I want 定位失效只影响相关卡片, so that 无关证据不会全项目变旧。
4. As a 项目成员, I want 授权撤回后敏感内容立即停止返回, so that 缓存不能越权。
5. As a 项目成员, I want 项目 revision 变化后看到受影响对象, so that 研究条件变化不会被忽略。
6. As a 项目成员, I want 旧异步结果显示 stale, so that 它不能覆盖新版本。
7. As a 作者, I want 生成上下文显示本轮证据范围和层级, so that 我知道 AI 使用了什么。
8. As a 作者, I want 候选证据能用于带待核标记的草稿, so that 探索不会被过早阻断。
9. As a 交付负责人, I want 正式生成和交付只使用当前确认的证据, so that 失效依据不会进入正式文件。
10. As a Reviewer, I want 重新核对当前锚点后恢复卡片, so that 失败状态可恢复。
11. As a 项目成员, I want 来源恢复后旧确认不自动恢复, so that 重新授权不会跳过人工复核。
12. As a 项目成员, I want 生成运行固定其 basis, so that 运行中新增资料不会静默混入。
13. As a 项目成员, I want 生成失败保留原因和恢复入口, so that 我能重新生成或处理缺口。
14. As a 管理员, I want 审计失效传播、重新确认和生成使用范围, so that 关键动作可追溯。

## Implementation Decisions

- 复用现有 Workspace ApplicationService、SourcePolicy、GenerationContext、CandidateAdoption、Delivery/Preflight 和 G3 DependencyEdge/ImpactTask seam。
- 依赖关系至少覆盖 ProjectAsset → SourceAnchor → EvidenceCard → ResearchClaim → Chapter/GenerationContext/ReleaseSnapshot；没有显式依赖的对象保持原状态。
- `NEEDS_REVIEW` 表示需要人重新核对；`STALE` 表示不能用于当前上下文；两者均保留原因、触发版本和恢复入口。
- 资料重解析、原文哈希变化、定位失效、授权收窄、成员失效、Claim 版本变化、Context Revision 实质变化和过期异步结果都可以触发局部传播。
- 撤权或授权不确定时，所有受限正文、摘录、备注和模型输入均拒绝或清除；历史对象的读取仍受当前权限控制。
- 生成上下文固定 Context Revision、项目资料版本、SourceAnchor/EvidenceCard 版本、确认范围、章节/主张/模板版本和权限范围。
- 正式生成只接受 `CONFIRMED`、当前 Claim、当前锚点和当前 ProjectAsset；候选、待复核、证据不足、STALE、无 Claim 或无权材料只能作为待核提示。
- 生成运行期间新增资料不自动混入；旧结果只能作为历史候选并显示 basis 差异，不能覆盖当前章节或 ReleaseSnapshot。
- 重新确认和正式写入采用 G5 fail-closed；同一幂等键重放前仍重新检查权限和当前性。
- 影响协调交给 G3，G2 只发布证据状态变化和依赖事实，不重复实现完整影响图算法。

## Testing Decisions

- 最高测试 seam 是 Workspace ApplicationService/HTTP 入口到 GenerationContext、CandidateAdoption 和 Delivery 的用户可见结果。
- 必验成功：局部失效、待复核提示、重新选择锚点、重新确认、恢复后生成、导出前当前性检查、历史版本保留。
- 必验失败：撤权后任何敏感响应、STALE 证据进入正式生成、旧异步结果覆盖新版本、来源恢复自动复用旧确认、授权不确定时放行。
- 检查生成输入、候选、冻结输入、导出文件和客户端残留，不只检查 HTTP 状态码。
- 使用现有 SourcePolicy、GenerationContext、Delivery 测试模式；需要真实 WeKnora、PostgreSQL 和真实模型时单独记录 real 验收。
- 前端检查影响原因、证据层级、上下文范围、失败恢复、清理和乱序响应隔离。

## Out of Scope

完整 DependencyGraph/ImpactResolver 实现、G3 ChangeSet 业务、G7 编辑器、G4 模板规则、Beta 首版 #120 验收协议和 Word 人工验收。

## Further Notes

完整 G2 的完成不能用章节 CitationUsage 的单元测试替代。必须证明 EvidenceCard 层级、失效传播和生成上下文边界在真实调用路径上成立。
