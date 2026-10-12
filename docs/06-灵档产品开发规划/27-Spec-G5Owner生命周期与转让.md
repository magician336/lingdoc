# Spec：G5 Owner 生命周期与转让

## Problem Statement

每个项目必须有且只有一个 Owner，因为立项确认、ChangeSet 应用、正式导出和归档都依赖项目最终责任人。Owner 可能暂时失去租户身份；项目既不能被错误地置于无 Owner 状态，也不能让 Admin 静默取得所有权。

## Solution

保留 Project 的唯一 Owner 记录，并将 Owner 租户身份失效表示为派生的 `owner_unavailable`。普通协作继续，Owner 专属动作冻结。普通 Owner 转让只能由当前 Owner 发起，受让人确认后原子完成。`owner_unavailable` 下，Beta 只允许 WeKnora 租户 Owner 指定受让人并发起旁路转让；暂不开放平台管理员旁路。

## User Stories

1. As a 项目 Owner, I want to transfer ownership to an active project member, so that responsibility can change without creating a second Owner.
2. As a 受让人, I want to review and accept a transfer, so that ownership is not forced on me.
3. As a 项目成员, I want an unaccepted transfer to leave the current Owner unchanged, so that responsibility remains clear.
4. As a 项目 Owner, I want transfer to clear the recipient's function axis, so that Owner capability is not mixed with old role scopes.
5. As a 被降级的原 Owner, I want my new role and scope to be explicit, so that I do not retain hidden Owner powers.
6. As a 项目成员, I want the project to remain usable for ordinary collaboration when the Owner's tenant identity is unavailable, so that work does not stop unnecessarily.
7. As a 项目成员, I want Owner-only actions to show why they are frozen, so that I know how to recover the project.
8. As a 租户 Owner, I want to nominate a successor when the project Owner is unavailable, so that a stranded project can recover.
9. As a 项目负责人, I want concurrent transfer requests to produce one winner, so that the project never has zero or two Owners.
10. As a 项目负责人, I want transfer and audit to succeed or fail together, so that ownership history is reliable.
11. As a QA 负责人, I want a stale project version to reject a transfer, so that a decision based on old membership cannot overwrite newer state.
12. As a 项目负责人, I want tenant restoration to clear `owner_unavailable` for the same Owner, so that temporary tenant suspension does not require a new transfer.

## Implementation Decisions

- `Project.owner_user_id` and exactly one ProjectMember with governance role Owner are an atomic invariant.
- `owner_unavailable` is derived from the current tenant membership of `owner_user_id`; it is not a Project status.
- When `owner_unavailable` is true, freeze project activation, template upgrade, ChangeSet application, export and archive. Continue authorized reading, editing, research and review.
- Normal transfer can be initiated only by the current available Owner. Admin cannot initiate ordinary transfer.
- The recipient must be an active tenant member and existing project member, and must explicitly accept.
- Transfer uses expected project version, predecessor/successor IDs, unique-Owner constraint and idempotency in one transaction.
- On acceptance, recipient governance role becomes Owner and function roles/scopes are cleared; predecessor is explicitly demoted and does not retain Owner capability.
- If tenant identity for the same Owner is restored before transfer completes, clear `owner_unavailable` and keep ownership. A completed transfer is not undone by later restoration.
- In Beta, the `owner_unavailable` bypass is initiated only by a WeKnora tenant Owner. Platform-admin bypass is reserved until its identity source, authority boundary and audit contract exist.

## Testing Decisions

- Test normal transfer request, pending acceptance, rejection/expiry, acceptance replay, stale version, concurrent requests and transaction rollback.
- Test recipient role/scope clearing and predecessor demotion.
- Test owner tenant suspension and restoration, including ordinary collaboration during `owner_unavailable` and frozen Owner-only actions.
- Test tenant Owner bypass initiation and ensure project Admin and ordinary platform identities cannot initiate it in Beta.
- Reuse existing `TransferOwner`, owner-transfer migration, access-status, project version and audit tests.

## Out of Scope

- Platform-admin bypass in Beta.
- Multiple Owners, delegated temporary Owners or service-account ownership.
- Automatic semantic changes caused by ownership transfer.
- Tenant role editing inside LingDoc.

## Further Notes

Ownership transfer is a business action. A successful transfer does not prove that the project's academic content is correct.
