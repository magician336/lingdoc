# Spec：调研智能体首轮闭环

## Problem Statement

科研用户通常从模糊想法开始，而不是从已有 PDF 开始。当前产品需要一个独立调研智能体来澄清问题、制定检索计划、进行多轮受控检索并沉淀可追溯的候选资料。搜索结果不能自动成为正式知识，AI 对话也不能自动成为研究结论；但如果没有从检索到项目资料的可用闭环，任务助手和写作工作台无法帮助用户建立可靠依据。

## Solution

实现首轮纵向闭环：用户提出研究想法，调研智能体生成可编辑 ResearchTask；用户确认范围后执行至少两轮检索；系统去重、分层并展示来源与不确定性；用户选择来源片段生成 ImportCandidate；用户确认导入后，项目资料可回看、重新鉴权、撤权和追踪。ResearchClaim、EvidenceCard、争议证据、多锚点 OCR 等对象预留交接接口，但不作为首轮完成条件。

## User Stories

1. As a 科研用户, I want to start research from the draft project or a chapter, so that research begins where my question arises.
2. As a 科研用户, I want the Agent to turn a vague idea into questions, concepts, keywords, synonyms and exclusion conditions, so that I can inspect the research plan.
3. As a 科研用户, I want to edit the ResearchTask before searching, so that the Agent does not assume my scope.
4. As a 科研用户, I want to see the project, chapter, revision, allowed assets and source-catalog context, so that I know the task boundary.
5. As a 科研用户, I want at least two search rounds with query history, so that I can refine the search rather than accept one opaque result.
6. As a 科研用户, I want results deduplicated and grouped, so that repeated sources do not appear as independent support.
7. As a 科研用户, I want every result to show author/institution, date, URL, retrieval time, source type, quality note and uncertainty, so that I can judge whether it is worth reading.
8. As a 科研用户, I want results separated into candidate evidence, background clue and discovery entry, so that I do not confuse a search summary with a citable source.
9. As a 科研用户, I want to inspect an allowed source and select a precise excerpt, so that an import can retain a stable locator.
10. As a 科研用户, I want to select a conversation range or source list from the Agent dialogue, so that useful research work can be preserved without importing an entire chat as fact.
11. As a 科研用户, I want to create an ImportCandidate before importing, so that I can review purpose, scope, license and future model-use settings.
12. As a 科研用户, I want to confirm an import once for the complete action, so that the Agent can perform the work without hidden writes.
13. As a 科研用户, I want an imported asset to be visible in the project with its lineage and processing state, so that I can find it later.
14. As a 科研用户, I want authorization revocation and source changes to affect later reads and AI use, so that old results cannot bypass current rules.
15. As a 科研用户, I want a running chapter generation to keep its fixed source snapshot, so that a newly imported asset does not silently change the result.
16. As a 研究负责人, I want draft-stage results to remain candidates, so that project activation does not falsely turn them into evidence.
17. As a 研究负责人, I want the handoff to Evidence to preserve source anchors, classification, provenance, revision and permission scope, so that later evidence work can continue without re-importing.
18. As a 安全责任人, I want external source access to use approved providers and tenant policy, so that the Agent cannot bypass network, copyright or permission controls.
19. As a QA 负责人, I want each run and import to be reproducible by mode and fixture, so that real and mock results are not mixed.

## Implementation Decisions

- Define ResearchTask/ResearchRun as the multi-round research container with project, optional chapter, query plan, scope, provider/catalog version, context revision, status and run metadata.
- Reuse the approved WeKnora search/fetch and tenant provider configuration; do not create a second search engine or RAG platform.
- Require a user-visible task card before the first search. The task card includes idea, questions, language/time range, inclusion/exclusion rules and requested output.
- Execute at least two rounds in the first acceptance path. Persist queries, result sets, provider, catalog version, user feedback and run status per round.
- Deduplicate and classify results as candidate evidence, background clue or discovery entry. Classification is advisory and does not decide scientific validity.
- Search-engine summaries without a stable source locator cannot be imported as evidence. Background clues may be imported only with their lower classification; discovery entries require an explicit upgrade after an original source is supplied.
- Model `ResearchExcerpt`, `ConversationExcerpt` and `ImportCandidate` as handoff objects. Each carries source/conversation reference, locator or turn range, text hash where applicable, selection type, target project scope, purpose, provenance, revision and authorization state.
- Import is a confirmed business action. After confirmation, route through the existing KnowledgeGateway/ProjectAsset processing path and retain original URL, final URL, metadata, retrieval time, catalog/license information, selected excerpt and importer.
- Draft-stage imports remain in the candidate pool and do not create formal Chapter, ResearchClaim or EvidenceCard objects. Active-stage handoff exposes the fields needed by A Evidence to create those candidates later.
- A running generation/research task uses a fixed context and asset-scope snapshot. New imports require an explicit refresh or rerun; stale results are historical and cannot overwrite current objects.
- Every read, search, inspect, candidate creation and import rechecks current project/source permissions. Revocation removes restricted content from later Agent context and prevents download or evidence promotion.
- Expose research results to the task assistant through cards and references, not by merging raw Agent transcripts.
- Reuse existing source policy, AssetGateway, source-context, generation-run, candidate and project-asset seams; add the smallest orchestration layer for ResearchTask/Run and ImportCandidate.

## Testing Decisions

- Test the complete user-visible path from idea to confirmed ProjectAsset in mock and real_api_fake_model modes; run real mode separately for Beta evidence.
- Test task editing, two search rounds, deduplication, classification, source inspection, excerpt selection, conversation-range selection, import confirmation and restart recovery.
- Test rejection of unstable search summaries, cross-project sources, missing locators, revoked assets, changed source versions, provider failures, partial upstream results and duplicate confirmation.
- Test that draft imports remain candidates, active imports preserve lineage, and no EvidenceCard/ResearchClaim is silently confirmed.
- Test stale generation/research results after context or asset-scope changes; they must remain historical and not overwrite current work.
- Reuse source authorization/currentness, AssetGateway, project-asset processing, generation snapshot and idempotency tests; add contract tests for the ResearchTask/Run and ImportCandidate handoff.

## Out of Scope

- A general external-source marketplace or a second search/RAG engine.
- Automatic formal EvidenceCard or ResearchClaim confirmation.
- Automatic scientific conclusion, controversy resolution or source authority judgment.
- Full multi-anchor OCR, parser platform, citation-format engine or evidence graph.
- Automatic chapter overwrite or silent inclusion of new assets in a running generation.

## Further Notes

This spec is the first implementation slice for the complete research agent. Its interfaces must leave room for EvidenceCard/ResearchClaim and G3 impact propagation without pretending those capabilities are complete in the first delivery.
