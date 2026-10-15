# Spec：G5 统一权限判定器

## Problem Statement

LingDoc 的请求同时受到租户身份、项目成员、职能角色、资源授权、项目状态和对象版本影响。若各领域分别判断，成员可能在章节、资料、证据、ChangeSet 或导出入口得到不一致结果。科研用户只知道自己在使用 LingDoc，也不应被迫理解底层 WeKnora 的多层授权链。

## Solution

建立统一的 `EffectivePermission` 判定 seam。每次请求按租户身份、WeKnora 租户角色上限、ProjectMember、capability、Beta scope、资源授权和领域条件求交集。读取和写入、同步请求和异步任务都使用同一判定规则；无法确认身份、成员关系、资源范围或版本时拒绝，并返回不泄露敏感存在性的结果。

## User Stories

1. As a 科研用户, I want LingDoc to present one understandable permission model, so that I do not need to know WeKnora internals.
2. As a 项目成员, I want every project action to use my current tenant and project membership, so that an old token cannot keep access.
3. As a 项目成员, I want my governance role and function role to combine predictably, so that I know why an action is allowed or denied.
4. As a 项目成员, I want chapter and asset scope to be checked at the target object, so that a role without scope cannot write by guessing an ID.
5. As a 项目成员, I want a clear denial reason, so that I know whether I need a project role, a scope grant or a source authorization.
6. As a 非项目成员, I want the system not to reveal a project exists when existence is sensitive, so that project identifiers cannot be enumerated.
7. As a 项目负责人, I want asynchronous tasks to recheck authorization at execution time, so that a task created before revocation cannot write after revocation.
8. As an Agent, I want the caller, project and source scope to be explicit, so that I cannot silently widen the user's access.
9. As a QA 负责人, I want the same authorization result across API, background task, preview and download, so that each route does not develop a separate security rule.
10. As a 审计负责人, I want every allow and deny decision to include capability, target and reason, so that an incident can be explained.
11. As a 开发者, I want domain services to call one authorization seam, so that G1–G7 do not duplicate policy logic.
12. As a 项目成员, I want a permission failure to leave my draft input intact, so that access errors do not destroy work.

## Implementation Decisions

- Define a shared authorization input containing caller principal, tenant, project, ProjectMember, governance/function roles, capability, target reference, Beta scope, resource authorization, project state, expected version and operation type.
- Compute `EffectivePermission` as the intersection of tenant identity, tenant-role ceiling, project membership/capability, resource scope and domain/version conditions.
- Use `active` tenant membership and `active` ProjectMember as prerequisites. `invited`, `suspended`, `removed` and `suspended_by_tenant` cannot perform project content operations.
- Treat Owner as the governance-axis capability set; Owner has no function roles or function scopes. Non-Owner empty scope means an empty write set.
- Beta UI exposes chapter and asset scope only. Knowledge-base, template and delivery scope remain server-side contracts.
- Use a fail-closed result when identity, membership, resource permission or version cannot be determined. This is separate from the approved audit-write failure policy.
- Return unified not-found-or-not-authorized responses when object existence is sensitive; return structured forbidden reasons to an authenticated member who is allowed to know the object exists.
- Recheck authorization for asynchronous task execution, preview, file access, export and download; creation-time permission is not completion-time permission.
- Preserve a single caller identity through human, Agent and API-key routes. API keys require both WeKnora KB scope and LingDoc project scope and cannot call high-risk human-confirmation actions.
- Reuse existing Workspace service authorization, SourcePolicy/AssetGateway, project version checks, transaction seam and audit sink rather than creating a second ACL engine.

## Testing Decisions

- Test external authorization behavior at the shared service seam and at representative HTTP routes.
- Cover active/inactive tenant membership, non-member access, role/capability combinations, empty scope, cross-project IDs, cross-tenant IDs, stale versions and unknown targets.
- Cover synchronous calls, asynchronous completion, source preview, export and download with the same caller state.
- Assert denial responses do not leak project names, chapter titles, asset titles, source text or file size when existence is sensitive.
- Reuse existing `ProjectAuthorizer`, Workspace, SourcePolicy, AssetGateway and router authorization tests; add contract fixtures consumed by G1–G7.

## Out of Scope

- Replacing WeKnora identity, tenant or KB sharing.
- Temporary authorization, service accounts, organization approval flows or fine-grained paragraph ACLs.
- Scientific correctness or evidence sufficiency.
- A second Agent-specific permission system.

## Further Notes

This is the shared seam for the other G5 Specs. Its output must be explainable to LingDoc users without exposing WeKnora implementation terms.
