# Spec：G2 ResearchClaim 与 EvidenceCard

> 完整 G2 的第三条开发切片。总设计见 [G2 证据工作台与调研智能体](beta-gap-development-plans/G2-evidence-research.md)。

## Problem Statement

来源片段只能说明“原文在哪里”，不能直接说明“它支持哪条研究主张”。当前章节引用和用途/限制已经能保存，但还没有 ResearchClaim 和 EvidenceCard，无法表达支持、反对、范围、限制、证据不足和争议并存。

## Solution

在 active 项目中建立版本化 ResearchClaim。用户把一个或多个当前 SourceAnchor 组织成候选 EvidenceCard，填写支持类型、适用范围、限制和缺口。候选卡可继续整理；只有具备当前主张、当前资料版本、完整锚点和有权限人员确认的卡，才可作为正式生成依据。

## User Stories

1. As a 项目成员, I want 创建具体研究主张, so that 证据有明确支撑对象。
2. As a 项目成员, I want 编辑和版本化主张, so that 研究条件变化后不会沿用旧语义。
3. As a Researcher, I want 从一个或多个 SourceAnchor 创建候选证据卡, so that 我能整理资料而不立即确认结论。
4. As a Researcher, I want 候选卡暂时没有主张, so that 我可以先整理片段。
5. As a Researcher, I want 记录支持、反驳、限定或背景类型, so that 证据意义清楚。
6. As a Researcher, I want 记录 scope、limitations、gaps 和替代解释, so that 用户不会过度解读来源。
7. As a Reviewer, I want 逐一查看卡片上的所有锚点, so that 确认针对实际原文。
8. As a Reviewer, I want 确认卡片在指定范围内可用, so that 正式生成有明确依据。
9. As a Reviewer, I want 标记证据不足或存在争议, so that 不必删除相反材料。
10. As a 项目成员, I want 同一主张同时保存支持和反对卡片, so that 系统不替我裁定结论。
11. As a 项目成员, I want 清楚区分候选卡和已确认卡, so that 待核材料不会混入正式依据。
12. As a 项目成员, I want 查看确认人、确认范围、依据版本和审阅理由, so that 复核可追溯。
13. As a 项目成员, I want 卡片版本不可变, so that 新锚点不会修改历史确认。
14. As a 项目成员, I want 卡片在来源或主张变化后进入待复核, so that 旧确认不会自动延续。
15. As a 项目成员, I want 相同幂等请求安全重试, so that 不会重复创建卡片或审计事件。

## Implementation Decisions

- 复用 Workspace ApplicationService、Project/Chapter 版本语义、现有 SourcePolicy 和 G5 EffectivePermission；不新建独立权限体系。
- `ResearchClaim` 仅允许 active 项目创建和修改，保存主张文本、版本、状态、适用范围、创建者、revision 和审计理由。
- `EvidenceCard` 版本保存 SourceAnchor 集合、ResearchClaim 版本、support type、scope、limitations、gaps、替代解释、创建者和 based_on_revision。
- EvidenceCard 状态为 `CANDIDATE`、`CONFIRMED`、`NEEDS_REVIEW`、`INSUFFICIENT`、`STALE`、`SUPERSEDED`。
- 创建候选卡要求 active 项目、READY ProjectAsset、至少一个当前可定位 SourceAnchor 和证据整理能力；可以暂时没有 Claim。
- 确认卡要求当前 Claim、支持范围、全部锚点和资料版本有效；确认人必须具备 Owner 或 Reviewer/证据确认能力。
- 确认只表示来源片段在指定范围内已被核对，不表示研究主张已经被系统证明。
- 支持与反对证据并列保存，系统不自动合并、不自动删除、不替用户作学术裁决。
- 添加、移除或替换锚点、修改主张版本或改变适用范围创建新卡片版本，旧确认不原地更新。
- 缺少定位、Claim、scope 或权限时，候选整理可以继续；确认和正式生成必须阻断并显示补齐项。
- 创建、确认、标记不足、撤销和复核均记录操作者、版本、revision、授权范围和审计理由；关键写入采用 fail-closed。

## Testing Decisions

- 最高测试 seam 是 Workspace ApplicationService/HTTP 入口，测试卡片列表、详情、版本、确认和权限结果。
- 必验成功：创建 Claim、创建无 Claim 候选卡、补 Claim、关联多锚点、确认、标记不足、支持/反对并存、重启读回。
- 必验失败：draft 建 Claim/Card、无 Anchor 建卡、PROCESSING 资料建卡、跨项目锚点、旧版本确认复用、越权确认、同键不同请求。
- 来源变化和权限撤回使用 SourcePolicy adapter 构造反例，检查卡状态、敏感内容和审计副作用。
- 前端检查候选/已确认过滤、状态文字、锚点逐条回看、支持范围、限制和恢复入口。

## Out of Scope

ResearchTask/ResearchRun、ImportCandidate、ProjectAsset/SourceAnchor 的导入和解析、完整调研 Agent、自动判断结论、正式章节编辑器和 Beta 首版真实验收。

## Further Notes

EvidenceCard 是可审阅证据对象，不能与当前已实现的章节 `CitationUsage` 混为同一对象；两者通过生成上下文和章节引用交接。
