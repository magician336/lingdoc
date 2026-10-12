# Spec：G3 依赖图、ImpactTask 与稳定化

## Problem Statement

应用正式变更后，科研用户需要知道哪些证据、主张、章节和技术路线必须处理，谁负责，以及什么时候能够继续交付。现有显式章节影响不能代替完整版本化依赖图；模型成功、任务页面完成或规则误报被删除，也不能证明项目已经稳定。

## Solution

以有效、已确认的 DependencyEdge 解析可解释影响，生成幂等 ImpactTask，由责任域和有权限的责任人完成处置。影响中心展示原因、版本、责任人和下一步。StabilizeRevision 重新读取当前版本、确认、资料授权和阻断条件，全部满足后标记 STABILIZED；Delivery 仍独立执行冻结和 Preflight，才可进入 READY 和导出。

## User Stories

1. As a 科研用户, I want every definite impact to cite a dependency, so that I know why an object needs work.
2. As a 科研用户, I want unrelated objects left unchanged, so that a local change does not force project-wide review.
3. As a Researcher, I want source and evidence relationships recorded with versions, so that changes propagate along actual references.
4. As a 科研用户, I want AI to propose dependency candidates with reasons, so that I do not manually discover every relationship.
5. As a 科研用户, I want to confirm, reject or edit proposed edges, so that model guesses do not become blocking facts.
6. As a 科研用户, I want rejected candidates remembered for the same versions, so that the assistant does not repeatedly ask the same question.
7. As an Admin, I want to assign and reassign impact tasks, so that one person or several people can carry out the required work.
8. As a 责任人, I want a task to state its action, target version and closure evidence, so that I know what completion requires.
9. As a Reviewer, I want evidence rechecking separated from my confirmation, so that model output does not automatically become accepted evidence.
10. As a 科研用户, I want duplicated upstream causes consolidated with all reasons retained, so that I do not receive duplicate tasks.
11. As a 科研用户, I want old asynchronous results marked stale, so that they cannot close tasks for a newer revision.
12. As a Owner, I want stabilization to re-read actual current state, so that a stale dashboard cannot falsely permit delivery.
13. As a Owner, I want every blocking reason identified, so that I can direct the team toward recovery.
14. As a 科研用户, I want rule-issue dismissal separated from impact-task completion, so that neither accidentally substitutes for the other.
15. As a 科研用户, I want required technical-route nodes checked by version, so that obsolete routes cannot enter delivery.
16. As a 项目成员, I want task history and retry recovery, so that service restarts do not lose responsibilities.
17. As a 科研用户, I want the impact assistant to explain certain and possible impacts separately, so that uncertainty remains visible.
18. As a 安全负责人, I want graph and task responses filtered by current authorization, so that edges do not reveal inaccessible research.
19. As a Owner, I want STABILIZED separated from READY, so that a stable project still requires a valid frozen delivery snapshot.
20. As a QA 负责人, I want duplicate, cyclic and out-of-order inputs handled safely, so that dependency traversal cannot create unbounded or repeated work.

## Implementation Decisions

- Reuse Workspace service and HTTP seams for graph, assessment, task actions and stabilization; add focused application interfaces for ImpactResolver and StabilizeRevision rather than a new workflow engine.
- DependencyEdge references source/target objects and version ranges, relation type, origin, reason, confidence where applicable, actor/time and lifecycle. Supported targets include ProjectSpec, ProjectAsset, SourceAnchor, EvidenceCard, ResearchClaim, ChapterVersion, template/rules and RouteNode versions.
- User-established or rule-confirmed relationships may be confirmed edges; model-proposed relationships remain suggested until an authorized user confirms them. Suggested edges explain possible impacts but cannot independently invalidate objects or block export.
- AI candidate import shows scope, object versions, evidence references, reasons and run metadata. Users can confirm, modify or reject individually or in batches. Reject records remain version-bound; new runs cannot overwrite human decisions.
- Traverse current valid confirmed edges only, with cycle detection and deduplication. Propagate action types REVALIDATE_EVIDENCE, RETRIEVE_CANDIDATES, REVIEW_REQUIRED, REGENERATE, RECOMPUTE, INVALIDATE_CONFIRMATION and BLOCK_RELEASE. Do not claim all implicit relationships are discovered.
- Logical ImpactTask identity is change_set_id, target_ref, target_revision and action_type. Merge repeated causes for that identity; different required actions remain distinguishable. Preserve every causal reference.
- ImpactTask records target version, severity, responsibility domain/person, source edges, status, timestamps, disposition and closure evidence. States include OPEN, IN_PROGRESS, WAITING_USER, DONE, DISMISSED, STALE and CANCELLED.
- Assignment and disposition use current G5 capabilities and target scope. Responsibility can remain with the same person across stages. An unassigned task or successful asynchronous job does not automatically close human review.
- Task completion rechecks target revision/version, source authorization and required review or recomputation. BLOCK_RELEASE is cleared only through Delivery's current-input Preflight. Old results cannot close new tasks.
- Persist application handoff durably with idempotent task identities; duplicate or out-of-order events re-read current domain truth. A owns source/evidence state, B owns confirmations/project/task state, C owns rules and delivery; events do not authorize cross-domain direct writes.
- RouteNode/RouteEdge versions participate in dependency and task checks. Required-node policy comes from G4. This Spec provides version-reference integration and impact display, not a full graphical route editor.
- Stabilization checks unresolved blocking tasks and current blocking issues, current affected-object confirmations, source/anchor versions and permissions, and revision/template/ruleset agreement. Any failed condition returns BLOCKED with reasons while ChangeSet stays APPLIED. All conditions passing marks STABILIZED.
- Delivery separately checks frozen inputs and Preflight before READY/export; STABILIZED is necessary but is not export authorization. Dismissing a ValidationIssue does not close ImpactTask or confirm content.
- Impact center shows before/after summary, grouped impacts, evidence edges, task action/severity/owner/version, stale results, blocking reasons and retry/reassessment/history actions. AI explanations cite their basis and use the existing task-assistant/context contracts; no additional autonomous authority is introduced.

## Testing Decisions

- The user confirmed domain-service and HTTP entry points as the seams. Assert returned impacts, tasks, revisions, confirmations, stabilization and delivery outcomes.
- Reuse G3 local-impact and confirmation-invalidation tests, G2 versioned-source contracts, G4 issue-disposition/Preflight behavior and G5 authorization/revocation fixtures.
- Cover confirmed versus suggested edges, candidate confirmation/rejection, missing/inaccessible objects, replaced versions, cyclic graphs, multi-hop paths, unrelated objects and multiple causes.
- Verify duplicate task delivery is idempotent; different actions are retained; task reassignment and closure obey scope and versions. Missing responsible users and successful model runs cannot auto-close required human review.
- Cover stale and reordered evidence/generation/Preflight results, source revocation during execution, restart recovery and repeated completion requests.
- Verify current blocking tasks/issues prevent stabilization, dismissed issues do not close tasks, changed objects invalidate stale completion, and required route nodes cannot be omitted.
- Exercise change application → task handling → fresh confirmations → stabilization → frozen Preflight → export, including blocked and recovery paths. Test fake and real dependencies separately.

## Out of Scope

- Claiming complete discovery of implicit dependencies or proving scientific validity.
- Full technical-route graphical editing and rich DOCX route rendering.
- Automatic human confirmation, silent full-text rewriting, permission expansion or bypassing release blockers.
- Replacing G2 evidence truth, G4 rule truth, G5 permission truth or the shared AI context/action-confirmation Specs.
- Distributed workflow infrastructure and real-time multiplayer editing.

## Further Notes

The companion ChangeSet lifecycle Spec owns formal application and revision advancement. This Spec owns impact plans and their completion semantics. A future route-editor Spec should reuse these versioned references and task rules rather than create another dependency graph.
