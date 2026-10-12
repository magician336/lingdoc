# Spec：G5 审计与权限可见性

## Problem Statement

科研项目需要知道谁改变了成员、scope、Owner、资料授权、确认和交付状态，但审计内容本身也可能泄露研究正文或项目存在性。当前权限设计还需要区分普通读取审计故障和关键业务写入审计故障，并让用户只看到符合其项目角色的记录。

## Solution

建立追加式、不可变的项目审计记录和分层查询。Owner/Admin 可查看完整项目审计和项目级摘要；其他成员只能查看自己的操作和拒绝记录；撤权成员只能读取最小 `access-status`。普通读取的审计写入失败采用 fail-open 并告警；成员赋权、scope 变更、Owner 转让、正式确认、ChangeSet 应用和正式导出采用 fail-closed，与业务写入同事务成功或失败。

## User Stories

1. As a Owner, I want to see member and scope changes, so that I can understand project responsibility.
2. As an Admin, I want to inspect permission decisions and delivery operations, so that I can operate the project safely.
3. As a 普通项目成员, I want to see my own operations and denials, so that I can correct blocked work.
4. As a 普通项目成员, I do not want to see project-wide audit summaries, so that unrelated research activity is not exposed.
5. As a 被撤权成员, I want a minimal access-status response, so that I can understand the restriction without seeing project metadata.
6. As a 审计负责人, I want allow and deny events to include actor, tenant, project, capability, target, version and reason, so that a decision can be reconstructed.
7. As a 安全负责人, I want audit entries to exclude research正文, source excerpts, files, keys and tokens, so that the audit store is not a second data leak.
8. As a 用户, I want ordinary reads to continue during an audit sink outage, so that a logging failure does not stop safe reading.
9. As a 项目负责人, I want critical writes to stop when their audit record cannot be written, so that a state change never becomes untraceable.
10. As a 项目负责人, I want audit history to survive role changes and revocation, so that later access decisions remain explainable.
11. As a QA 负责人, I want audit query results to obey current tenant and project authorization, so that `audit.read` cannot cross projects.
12. As an 运维负责人, I want alerts for fail-open audit failures, so that temporary observability loss is visible.

## Implementation Decisions

- Use an append-only AuditEvent with event ID, timestamp, actor principal, tenant, project, action, decision, capability, target reference, target version, scope evaluation, project revision, request/correlation IDs, reason code, policy version and source service.
- Never store research正文, SourceAnchor raw text, file content, API keys or access tokens in project audit records.
- Owner/Admin can query complete project audit and project-level summaries. Other active members can query only their own operations and denials. Suspended/removed/suspended_by_tenant members receive only minimal access-status.
- Audit query itself is subject to current tenant, ProjectMember and project scope checks.
- Ordinary read/access audit uses fail-open: continue the read and emit an operational alert when the audit sink fails. This never changes an authorization deny into allow.
- Critical business actions use fail-closed: member grants, scope changes, Owner transfer, formal confirmation, ChangeSet application and formal export commit business state and audit atomically.
- Permission decisions and domain state are separate. Audit cannot be edited by the frontend or used to mutate Project, ProjectMember, ChangeSet or ExportArtifact.
- Reuse the existing workspace audit sink, transaction audit seam and WeKnora `audit_logs` production adapter; do not create a second platform audit system.

## Testing Decisions

- Test role-filtered audit queries, current membership, revoked membership, cross-project IDs and sensitive-field redaction.
- Test fail-open ordinary reads with audit sink failure and verify alert emission.
- Test fail-closed critical actions with audit sink failure and verify no business write, no partial version, no duplicate side effect and safe retry.
- Test audit persistence for allow, deny, role change, scope change, Owner transfer, source revocation, confirmation, export and download.
- Reuse existing authorization audit, audit persistence regression, delivery audit and source audit tests; add critical-action transaction-failure fixtures.

## Out of Scope

- Platform-wide WeKnora/RuoYi audit administration.
- User-configurable retention, legal hold, SIEM integration or enterprise monitoring.
- Auditing raw model prompts or complete Agent transcripts.
- Platform-admin Owner recovery in Beta.

## Further Notes

This Spec defines the evidence needed for G5 completion and the observability boundary consumed by G6.
