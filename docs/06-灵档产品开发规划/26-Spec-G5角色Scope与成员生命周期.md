# Spec：G5 角色、Scope 与成员生命周期

## Problem Statement

科研项目通常由课题负责人、章节负责人、研究助理和评审人共同参与。固定的 owner/collaborator 模型无法表达职责边界，也无法安全处理邀请、暂停、移除、租户恢复和资料范围变化。用户需要在 LingDoc 中管理项目成员，而不需要进入 WeKnora 修改底层租户角色。

## Solution

实现 ProjectMember 双轴模型：治理角色为 Owner/Admin/Member，职能角色为 Author/Researcher/Reviewer/Observer，可叠加。Beta 提供成员管理页面，邀请仅限当前租户 active 用户，邀请有效期 3 天；重新发送生成新版本；接受时重新检查身份、项目状态和 scope。Beta UI 只编辑章节 scope 与资料 scope，其他 scope 通过后端保留。

## User Stories

1. As a 项目 Owner, I want to see all project members and their effective capabilities, so that I can understand who can do what.
2. As a 项目 Admin, I want to invite an existing active tenant member, so that I can add collaborators without opening a second administration system.
3. As a 项目负责人, I want to choose governance and function roles separately, so that a project assistant can also be an author without becoming an Owner.
4. As a 项目负责人, I want to assign chapter scope, so that an Author can edit only the chapters they own.
5. As a 项目负责人, I want to assign asset scope, so that a Researcher can use only approved project materials.
6. As an 被邀请人, I want an invitation to show its expiry and current role/scope, so that I know what I am accepting.
7. As an 被邀请人, I want a resent invitation to replace the old version, so that accepting an outdated role cannot grant stale access.
8. As a 项目成员, I want to be suspended or removed with a clear reason, so that I understand why access changed.
9. As a 项目负责人, I want a tenant-restored member to remain pending until I explicitly restore them, so that temporary tenant recovery does not silently regain project access.
10. As a 项目成员, I want revoked scopes to stay revoked after restoration, so that old authorization cannot return by accident.
11. As a 项目成员, I want the member page to show tenant-invalid, project-suspended and pending-restoration states, so that failure is visible.
12. As an Observer, I want read-only access to authorized project content without accidental comment or edit rights, so that observation is safe.
13. As a 项目负责人, I want role and scope changes to be versioned and audited, so that I can explain the project history.
14. As a QA 负责人, I want duplicate invitation acceptance and expired invitations to be idempotent and safe, so that retries do not create duplicate members.

## Implementation Decisions

- Model ProjectMember with governance role, function roles, function scopes, status, inviter, acceptance time and timestamps.
- Keep Owner unique and never grant Owner through invitation. Owner transfer is a separate lifecycle Spec.
- Permit Admin to manage members and configuration but not evidence confirmation, content confirmation, ChangeSet application or export.
- Permit function-role combinations such as Admin + Author; capability is the union subject to tenant ceiling, resource authorization and scope.
- Interpret empty non-Owner function scope as no write access. Owner has full capability through governance role and does not use function scope.
- Keep capability summaries target-specific: `research:<chapterId>` describes chapter work, not project-wide `research`. A chapter Author may reread only assets already referenced by that current formal chapter; a caller-provided chapter ID does not grant use of unrelated materials. New material research requires its explicit approved Researcher asset scope. Source permissions and versions are still checked at use time.
- Beta invitation eligibility is current-tenant `active` membership only. Cross-tenant and unregistered invitations are out of Beta.
- Invitation expiry is 3 days. Resend creates a new invitation version. Acceptance uses the current valid version and rechecks tenant identity, project state and scope.
- Beta member-management UI supports member list, invitation, roles, chapter scope, asset scope, suspension, removal and pending-restoration recovery. Knowledge-base, template and delivery scope remain backend-only.
- `suspended_by_tenant` preserves the original role and scope as pending authorization. Owner/Admin must explicitly restore ordinary members after tenant recovery; project-side suspension/removal and revoked scopes are not overridden.
- Any role, scope, suspension, removal or restoration write uses the current project version, idempotency key and fail-closed audit transaction.

## Testing Decisions

- Test member-page observable behavior and API behavior together: list, invite, accept, resend, expire, role edit, scope edit, suspend, remove and restore.
- Cover active versus inactive tenant members, same-tenant restriction, invitation version races, expired invitations, duplicate acceptance and stale project versions.
- Cover role/capability boundaries including Admin + Author, Reviewer confirmation, Observer read-only, empty scope and revoked asset scope.
- Cover tenant recovery for ordinary members and ensure project-side suspension/removal is not overwritten.
- Reuse existing membership, capability, owner transfer, SQLite migration and HTTP access-status tests; add contract fixtures for the member-management UI.

## Out of Scope

- Cross-tenant invitations or tenant-member creation.
- Editing WeKnora tenant roles inside LingDoc.
- Platform-admin Owner recovery in Beta.
- Temporary access, service accounts, organizational approval and paragraph ACLs.

## Further Notes

This Spec supplies the ProjectMember and scope state consumed by the unified permission evaluator and the Owner lifecycle Spec.
