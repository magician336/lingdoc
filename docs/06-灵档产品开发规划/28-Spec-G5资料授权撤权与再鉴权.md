# Spec：G5 资料授权、撤权与再鉴权

## Problem Statement

项目成员权限和资料权限是两件事。用户可能仍是项目成员，但不再有某个知识库、ProjectAsset、SourceAnchor、预览文件或导出物的访问权。若只在检索入口检查，缓存、引用、异步任务和下载会泄露已撤权内容。

## Solution

建立项目—知识库绑定和 ProjectAsset/SourcePolicy 再鉴权链。每次读取、检索、来源定位、Agent 引用、预览、导出和下载都同时检查 ProjectMember、LingDoc scope、WeKnora KB share/API Key scope、SourcePolicy、对象版本和当前状态。上游只返回允许子集时必须识别过滤结果；撤权后旧缓存和预签名地址不能继续提供正文或文件。

## User Stories

1. As a 项目成员, I want project membership and material authorization to be shown separately, so that I know why I can see a project but not a source.
2. As a Researcher, I want to import only materials allowed by the project and knowledge-base scope, so that research stays within the approved boundary.
3. As a 项目负责人, I want to bind a knowledge base to a project with purpose and version, so that the source boundary is auditable.
4. As a 项目成员, I want a source excerpt to show whether it is currently readable, so that a stale citation is not mistaken for live evidence.
5. As a 用户, I want upstream partial results to show that some items were filtered, so that a successful HTTP response is not mistaken for full authorization.
6. As a 项目负责人, I want source revocation to affect future Agent prompts, so that old cards cannot silently restore restricted text.
7. As a 项目成员, I want an already opened preview to be rechecked on refresh and download, so that a cached URL is not an access bypass.
8. As a 项目负责人, I want asynchronous research and generation tasks to stop writing after source revocation, so that a task cannot publish unauthorized material later.
9. As a 被撤权成员, I want only minimal access status, so that I can understand the restriction without seeing source titles or content.
10. As a 项目负责人, I want historical audit and lineage to remain without raw research text, so that accountability survives revocation.
11. As a QA 负责人, I want cross-project and cross-tenant source IDs to fail, so that identifiers cannot be substituted across boundaries.
12. As an Agent, I want source scope to be explicit in every run, so that it cannot search an entire knowledge base because one asset was allowed.

## Implementation Decisions

- Store project–knowledge-base binding with project, tenant, KB, binder, binding version and allowed purpose.
- Use ProjectAsset, SourceAnchor, SourcePolicy and WeKnora ResolveKB/KBAccess as the source authorization chain; do not copy or recreate WeKnora sharing logic.
- Beta member UI exposes asset scope; knowledge-base scope remains backend-only but is always enforced.
- Compare requested source IDs with the IDs returned by upstream services. A partial result is not full success and must be marked or hidden according to the caller's existence policy.
- Recheck authorization at source list, source read, source context, retrieval, Agent context assembly, preview, export and download.
- On revocation, preserve immutable audit and lineage metadata but remove restricted body, excerpt, source title and reusable download addresses from the revoked user's responses.
- Running tasks use a fixed source/version snapshot; new grants require explicit refresh or rerun, while revoked access causes `STALE` or `NOT_AUTHORIZED` without formal writes.
- ExportArtifact and preview access are authorized at every open/download; historical snapshots do not override current source authorization.
- Reuse existing AssetGateway, SourcePolicy, source context, delivery access checker, currentness checker and audit sink.

## Testing Decisions

- Test project-member allowed but source-denied, KB viewer/editor, API Key scope, source-not-ready, revoked source, cross-project ID, cross-tenant ID and upstream partial-result behavior.
- Test revocation after source list, source read, preview URL issuance, Agent run creation and export creation; all later reads/downloads/writes must be blocked as appropriate.
- Test cached body, pre-signed URL, browser refresh, async completion and retry paths.
- Test that revoked users receive only minimal access-status and no titles, excerpts, file size or raw audit details.
- Reuse source-service, AssetGateway, source-revocation integration, delivery download and currentness tests.

## Out of Scope

- A second knowledge base or search engine.
- Sensitive-material watermarking, paragraph ACLs, service accounts or temporary grants.
- Automatic migration of revoked derived content into a clean project.
- Scientific validity of a source or claim.

## Further Notes

This Spec is the resource boundary for G2, the context package for AI Specs, and the download gate for G6/G7.
