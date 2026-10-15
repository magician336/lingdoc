# Spec：G4 模板升级与 G3 稳定化交接

> G4 下一阶段切片。当前已有最小 `template_upgrade` ChangeSet 应用；本 Spec 完善模板升级与完整 G3 ImpactTask/稳定化的交接，不涉及 Beta 首版验收。

## Problem Statement

当前 active 项目可以通过 ChangeSet 执行模板升级，已经具备版本冲突、回滚和历史副本保留。但完整 G4 还需要把字段、章节、规则、确认、证据、问题和交付影响完整交给 G3，确保升级后在稳定化前不能继续把旧检查和旧确认当作当前状态。

## Solution

模板升级先生成带基线的 ChangeSet，列出字段迁移、章节变化、RuleSet 变化、EvidenceCard/Confirmation 影响、ValidationIssue 重算和 ReleaseSnapshot 影响。G3 创建并追踪 ImpactTask；只有所有必需任务稳定后，G4 才允许建立新的当前 ReleaseSnapshot。应用过程原子更新模板绑定、迁移结果、Context Revision、任务和审计；旧快照仍可读但不能作为当前导出输入。

## User Stories

1. As a project owner, I want preview all template-upgrade impacts, so that I can decide before applying.
2. As a project owner, I want see the exact baseline used by the preview, so that a stale preview cannot be applied.
3. As a project owner, I want field migration failures visible, so that values are not silently dropped.
4. As a reviewer, I want see which chapter confirmations become invalid, so that affected content is rechecked.
5. As a reviewer, I want see which evidence cards and claims require review, so that template changes do not hide evidence gaps.
6. As a delivery operator, I want see which ValidationIssue results must be recomputed, so that old decisions do not survive a ruleset change.
7. As a project owner, I want apply the upgrade atomically, so that partial project updates cannot occur.
8. As a project owner, I want stale or revoked upgrades rejected, so that an old proposal cannot overwrite current work.
9. As a project member, I want old frozen snapshots readable after upgrade, so that history remains reproducible.
10. As a delivery operator, I want current export blocked until required ImpactTasks stabilize, so that unresolved effects cannot ship.
11. As a project owner, I want a failed stabilization task recoverable, so that I can fix and retry without rebuilding the project.
12. As an auditor, I want the upgrade, tasks, decisions and snapshot changes linked, so that the complete chain is reviewable.

## Implementation Decisions

- Reuse existing Workspace ApplicationService, ChangeSet lifecycle, Context Revision and the G3 DependencyEdge/ImpactTask/Stable seam.
- A `template_upgrade` ChangeSet records source template copy version, target template/RuleSet versions, project version, migration preview hash and expected current snapshot.
- Impact categories include field migration, chapter structure, RuleSet/evaluation, EvidenceCard/ResearchClaim, confirmation, ValidationIssue and delivery snapshot.
- G4 owns template/rule impact facts; G3 owns task identity, assignment, status, stabilization and ordering.
- Required tasks must reach the G3 stable state before a new current ReleaseSnapshot or export can pass. Historical snapshots remain readable and are marked not current.
- Apply is one transaction for project template binding, migrated values, Context Revision, ChangeSet status, ImpactTask creation/update and audit; any failure rolls back all writes.
- Stale baseline, revoked actor, missing mapping, unresolved required migration or audit failure rejects the apply without partial writes.
- Replaying the same idempotency key returns the original result only after current authorization and baseline checks; the same key with different input is rejected.
- After apply, old confirmations and validation dispositions remain historical; current checks must bind new target versions and RuleSet hashes.
- UI shows preview, task status, blocked reasons, recovery actions and historical snapshot labels using server states; it does not infer stability locally.

## Testing Decisions

- Highest seam is Workspace ApplicationService through ChangeSet and ImpactTask HTTP routes to currentness/export behavior.
- Must pass: preview, stale rejection, atomic apply, task creation, stabilization, old snapshot replay, new check, re-confirmation and current export.
- Must fail: partial migration, old disposition reuse, export before stabilization, old snapshot treated as current, duplicate apply, revoked actor and audit failure.
- Cross-domain tests reuse G3 ChangeSet tests, G4 template migration tests, delivery currentness tests and G5 authorization tests.
- Verify SQLite/PostgreSQL transaction behavior, restart read-back, idempotent replay and absence of partial writes.
- Frontend checks task progress, conflict recovery, failure messages, historical/current labels and no accidental writes on cancel.

## Out of Scope

Reimplementing G3 ChangeSet or ImpactTask internals, creating a new permission engine, redesigning G7 chapter editing, template catalog publication, independent RuleSet governance and Beta首版验收。

## Further Notes

This Spec should be implemented after the template catalog and RuleSet version seams are stable; otherwise upgrade baselines cannot identify the objects being changed.
