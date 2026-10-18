# Spec：G3 ChangeSet 生命周期与 Context Revision

## Problem Statement

科研项目立项后，修改研究对象、方法、正式资料范围或已确认结论会影响其他内容。用户需要先看到变更和影响，再决定是否正式改变项目。现有切片已有字段差异、显式章节影响、Owner 应用/驳回、并发保护和确认失效；完整 G3 需要把它们扩展为统一、版本化的变更生命周期，而不是重建已经交付的入口。

## Solution

通过 ChangeSet 记录针对当前已确认项目基线的一组拟议语义变化。创建和评估只产生草稿和预览；当前可用 Owner 明确应用后，正式语义、Context Revision、影响任务和审计一起提交。旧基线拒绝应用，旧内容保留历史；撤销通过新的逆向 ChangeSet 完成。

## User Stories

1. As a Author, I want to propose a research-condition change, so that I can discuss it before changing the formal project.
2. As a 科研用户, I want ordinary wording and formatting edits to remain ordinary edits, so that writing does not require approval for every sentence.
3. As a 科研用户, I want ambiguous semantic changes to require a human choice, so that the system does not silently classify them as harmless.
4. As a Author, I want to see old values, new values, reasons and object versions, so that the proposal is understandable.
5. As a Owner, I want an impact preview before application, so that I can judge the cost of a change.
6. As a Owner, I want certain impacts separated from possible impacts, so that model guesses are not treated as facts.
7. As a Owner, I want to reject a proposal without changing the project, so that discussion history remains available.
8. As a 项目成员, I want application to produce one new Context Revision, so that all follow-up work refers to one baseline.
9. As a Owner, I want a stale proposal rejected, so that another member's newer changes are preserved.
10. As a 项目成员, I want old versions and confirmations preserved, so that I can understand the project's history.
11. As a Owner, I want to reverse an applied change through a new proposal, so that reversal is evaluated and audited.
12. As a 科研用户, I want retries to return the original outcome, so that a network failure does not apply a change twice.
13. As a 科研用户, I want application failure to leave the prior baseline intact, so that I never receive a half-changed project.
14. As an Admin, I want to inspect and coordinate proposals without applying them, so that project management does not grant academic authority.
15. As a 科研用户, I want AI to prepare a structured proposal with my confirmation, so that I can use natural language without granting AI independent decision power.
16. As a Owner, I want source, permission and version checks repeated at application, so that a previously valid preview cannot bypass current conditions.
17. As a 项目成员, I want to copy a stale proposal onto the current baseline for reassessment, so that useful work is not lost.
18. As a Owner, I want active template upgrades to use the same change lifecycle, so that template changes cannot silently alter project meaning.

## Implementation Decisions

- Reuse Workspace application services, HTTP ChangeSet endpoints, Repository.Transaction, version checks, idempotency and existing G4 template-upgrade handoff.
- Semantic changes include research objects/questions/goals/methods/variables, inclusion/exclusion conditions, formal asset scope, confirmed claims/evidence relationships, template/rule bindings and required route structure. Unconfirmed wording, ordering, formatting and comments can create ChapterVersion without ChangeSet when project meaning is unchanged.
- Human choice resolves changes that deterministic rules cannot classify. AI may prepare structured deltas and explain evidence; confirmed business actions still require user confirmation and current G5 authority.
- ChangeSet records project, creator, reason, type, base revision, structured delta, target versions, assessment inputs/results, decision actor/time, target revision and audit references.
- Lifecycle is DRAFT, ASSESSED, APPLIED, STABILIZED, REJECTED or STALE. APPROVED is not a separate persisted milestone. Stabilization belongs to the companion impact-coordination Spec.
- Assessment has no formal project side effects. It records dependency, source, target and ruleset versions. Changes to assessment inputs require reassessment before application.
- Only current available Owner may apply or reject. Application rechecks current permissions, source authorization, project state, target versions and base revision.
- Application atomically advances Context Revision by one, writes the formal delta, preserves history, records APPLIED and creates the required logical impact tasks plus audit. Audit or task persistence failure rolls back the business write. External processing occurs after commit through durable handoff; no distributed transaction is assumed.
- G1 activation from revision 0 to 1 remains a separate baseline-confirmation action. Active ProjectSpec semantic changes cannot use ordinary save.
- Concurrent proposals may share a baseline, but only one can apply first. Later stale application cannot create a revision or business tasks. Persist or return staleness without partial formal writes.
- Same idempotency key and payload return the original result only after current authorization checks; changed payload conflicts. Reversal creates a new inverse ChangeSet against the current revision.
- Reassessing a stale field ChangeSet whose every proposed value is already current creates a terminal `SATISFIED` record linked to the unchanged stale history; it does not advance revisions or create impacts, and it cannot be applied or rejected.
- The reassess HTTP operation is bodyless: its idempotency payload is determined by the project/change-set path and key. Any request body is rejected; the OpenAPI response describes both `assessed` and `satisfied` outcomes.
- The UI presents before/after values, assessment basis, impact status, Owner decision, stale reasons and recovery actions using accessible LingDoc language.

## Testing Decisions

- The user confirmed Workspace domain-service and HTTP entry points as the testing seams. Assert observable revisions, proposal states, confirmations, tasks, audit and delivery outcomes, not internal table layouts.
- Reuse existing G3 ChangeSet, template-upgrade, confirmation-invalidation, HTTP replay and change-review delivery tests. Historical PASS records do not establish completion of the extended lifecycle.
- Cover create/edit/assess/reassess/apply/reject, ordinary versus semantic editing, concurrent proposals, stale assessment inputs, unauthorized Owner/Admin, tenant/source revocation and missing target versions.
- Inject audit and task persistence failures and assert old formal state is intact. Cover retry after lost response and conflicting idempotency payloads.
- Verify reverse changes advance revision and preserve original history. Verify active template upgrades use G4 migration assessment and current G5 checks.
- Keep mock, real API with fake model and real dependency evidence separate. No new tests are run as part of writing this Spec.

## Out of Scope

- Automatic scientific judgment, automatic application without confirmed action, or automatic full-document rewriting.
- Reimplementation of G5 authorization or G4 template evaluation.
- A distributed workflow platform or real-time collaborative editor.
- The detailed dependency resolver, ImpactTask coordination and stabilization algorithm, owned by the companion Spec.

## Further Notes

The companion Spec owns DependencyEdge, ImpactResolver, ImpactTask and Stabilization. The application boundary must consume its assessed impact plan atomically without embedding a second impact model. Technical-route versions participate as targets; a full route editor is a separate delivery slice.
