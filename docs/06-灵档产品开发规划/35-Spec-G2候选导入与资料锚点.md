# Spec：G2 候选导入、ProjectAsset 与 SourceAnchor

> 完整 G2 的第二条开发切片。总设计见 [G2 证据工作台与调研智能体](beta-gap-development-plans/G2-evidence-research.md)。

## Problem Statement

检索结果和项目资料不是同一个对象。用户需要选择来源片段、确认保存和授权用途，再将资料交给 WeKnora 处理。当前代码可以使用已存在的项目资料和来源引用，但缺少从调研结果到 `ImportCandidate`、`ProjectAsset`、`SourceAnchor` 的完整可追溯链路。

## Solution

用户从 ResearchRun 或项目资料中选择来源和片段，建立候选并明确保存方式、资料范围、模型使用许可和结果层级。确认后通过现有 KnowledgeGateway/WeKnora 完成处理，生成带版本的 ProjectAsset。用户在原文预览中选择片段，系统保存页码、章节、段落、坐标、文本哈希和定位置信度，形成可回看的 SourceAnchor。摘要或无法定位的文本只能保留为发现记录。

## User Stories

1. As a 项目成员, I want 从调研结果选择来源和片段, so that 我能控制哪些内容进入项目。
2. As a 项目成员, I want 选择保存全文、链接加摘录或仅元数据, so that 保存方式符合资料许可和团队需要。
3. As a 项目成员, I want 选择个人草稿、项目资料或团队共享范围, so that 资料不会默认扩大可见范围。
4. As a 项目成员, I want 标记背景线索、候选证据或待补出处, so that 结果层级不会丢失。
5. As a 项目成员, I want 看到导入处理中、成功、失败和重试状态, so that 我不会把半成品当作资料。
6. As a 项目成员, I want 在原文预览中选中文本、表格或图注, so that 系统自动生成稳定定位。
7. As a 项目成员, I want 看到页码、章节、段落、坐标和文本哈希, so that 我能回到原文复核。
8. As a 项目成员, I want 区分原生文本、可靠 OCR、低置信 OCR 和人工修正, so that 我知道定位可靠程度。
9. As a 项目成员, I want 一本长书按章节和片段建立多个锚点, so that 不必手工标注整本书。
10. As a 项目成员, I want 看到来源版本变化和定位失效, so that 系统不会静默替换引用文本。
11. As a 项目成员, I want 重新选择片段并建立新版本, so that 失效资料可以恢复使用。
12. As a 项目成员, I want 相同来源片段去重但保留不同用途和审计, so that 资料不会重复处理。
13. As a 项目成员, I want 撤销导入或拒绝保存, so that 未确认内容不会进入项目资料。
14. As a 项目成员, I want 授权变化后受限摘录不再返回, so that 缓存不会绕过权限。
15. As a 管理员, I want 查看导入者、授权范围、处理版本和失败原因, so that 资料来源可审计。

## Implementation Decisions

- 复用现有 Workspace ApplicationService、SourceApplicationService、KnowledgeGateway/WeKnora 上传解析索引能力和 G5 权限判定器。
- `ImportCandidate` 状态至少包含 `selected`、`awaiting_consent`、`importing`、`imported`、`rejected`、`failed`；结果层级与处理状态分开保存。
- `ProjectAsset` 保存项目归属、来源身份、处理状态、授权范围、版本、原始来源谱系和去重键；PROCESSING 或 FAILED 不能作为正式证据输入。
- `SourceAnchor` 保存 ProjectAsset 版本、定位器、可读位置、选中文本哈希、定位置信度、创建者、创建时间和原文复核信息。
- 原文定位按资料类型支持页码、章节、段落、表/图编号和 OCR 页面坐标；不能把 WeKnora chunk ID 单独当作业务锚点。
- 只有稳定原文定位才能创建正式 SourceAnchor；搜索摘要、模型转述和无出处文本只能保留为发现入口。
- 人工 OCR 修正保留原始 OCR、修正文本、对照结果和差异，不覆盖原始内容。
- 导入确认、授权范围、用途和审计记录必须原子写入；失败保留候选和失败原因，不产生 READY 资料。
- 相同 `source_version + locator + quoted_text_hash` 默认复用 ProjectAsset/SourceAnchor；不同项目范围或用途保留独立候选和审计关系。
- 资料替换、重解析、删除、授权收窄或原文变化发布来源变化事件，相关锚点进入待复核，不自动迁移到新文本。
- 项目范围只允许当前租户 active 成员；资料能力和 scope 由 G5 判定，客户端不能自报授权。

## Testing Decisions

- 最高测试 seam 是 Workspace ApplicationService/HTTP 入口和现有 KnowledgeGateway adapter。
- 必验成功：候选选择、用途确认、处理完成、READY 资料、PDF/书籍片段定位、多个锚点、去重、重启读回。
- 必验失败：摘要导入正式证据、无定位锚点、跨项目来源、未就绪资料建卡、重解析失败覆盖旧版本、重复提交产生重复资料、撤权后返回摘录。
- 使用固定合成 PDF、书籍片段、扫描 OCR 和来源版本 fixture；真实 WeKnora 接线另做集成验收。
- 前端检查候选层级、处理状态、定位预览、OCR 风险、授权范围、失败恢复和受限内容清理。

## Out of Scope

ResearchTask/ResearchRun 的任务编辑、ResearchClaim、EvidenceCard、争议证据、多轮调研 Agent、完整 PDF/OCR 解析平台、正式章节确认和 Beta 首版验收。

## Further Notes

本 Spec 的输出必须能被 EvidenceCard Spec 消费；不能把 ProjectAsset 或 SourceAnchor 自动视为已确认依据。
