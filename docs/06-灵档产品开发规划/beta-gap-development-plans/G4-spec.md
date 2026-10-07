# G4 模板与规则治理 Spec

> 基线：`main` / `50f0dba921cd93d0660bfa166d49cefc354fa5bd`（2026-10-05）。
> 来源：G4 Matt 式开发计划；本文件是实现前的产品与工程规格，不表示功能已经实现。

## Problem Statement

LingDoc 当前通过固定的 `template-demo/1` 和确定性 Delivery 规则完成最小流程。项目可以消费模板，但不能拥有独立的模板副本，不能安全编辑字段和章节，不能把项目规则作为可追溯版本保存，也不能把写作期检查、导出前 Preflight、问题处置和历史冻结快照统一到同一套版本语义中。

这会带来四类用户问题：源模板更新可能意外改变项目；字段或章节迁移可能静默丢失项目数据；用户无法知道规则问题针对哪个版本、依据什么输入产生；旧问题处置或旧检查结果可能被错误复用到新模板、新规则或新项目 revision。G4 需要解决模板、规则和问题的版本治理，同时保持 G3 的变更协调、G5 的权限真相和 G6 的质量运营边界。

## Solution

为项目建立不可变、可回放的模板与规则链：已发布模板版本可以复制为项目私有副本；副本在草稿期可编辑并预览迁移影响，发布后形成新的不可变版本；声明式规则通过白名单求值器在写作期和冻结前使用同一输入语义；检查结果形成绑定规则、目标和版本的 `ValidationIssue`；处置重新鉴权并审计；active 项目模板变更通过 G3 `template_upgrade` ChangeSet；只有当前 Preflight 明确通过才允许导出。

最高层 seam 是模板/规则/问题领域服务与 workspace 的窄接口。HTTP 层负责请求校验、鉴权、预期版本和错误映射；规则判据、版本绑定、问题身份和导出闸门不复制到 HTTP 或前端。现有 Delivery 冻结输入作为 Preflight 的唯一事实输入，现有 `TemplateReader` 继续作为模板消费方契约。

## User Stories

1. As a project owner, I want to create a private copy from a published template version, so that my project is isolated from later source-template changes.
2. As a project owner, I want to see the source template, version, scope, content hash and ruleset hash, so that I can identify exactly what my project uses.
3. As a project owner, I want to edit a draft copy's fields, so that the project captures its own required information.
4. As a project owner, I want stable field identifiers, so that values can be migrated without relying on display labels.
5. As a project owner, I want to preview added, removed, renamed and type-changed fields, so that I can fix migration risks before saving.
6. As a project owner, I want orphaned and missing values to remain visible, so that an edit cannot silently discard project data.
7. As a project owner, I want to edit chapter order and structure in a draft, so that the generated project skeleton matches the copy I approved.
8. As a project owner, I want to edit controlled terms and rule bindings, so that the project can express its declared requirements without uploading executable code.
9. As a concurrent editor, I want stale writes to fail with a version conflict, so that another editor's changes are not overwritten.
10. As a user, I want repeated writes with the same idempotency key to be safe, so that retries do not create duplicate versions or bindings.
11. As a project owner, I want a draft to be rejected when required structure or rule parameters are invalid, so that an invalid copy cannot be published.
12. As a writer, I want Template Check to explain which rule, target and evidence caused a problem, so that I know what to fix.
13. As a writer, I want writing-time checks to show warnings and blockers without granting export permission, so that a partial draft cannot be mistaken for a deliverable.
14. As a delivery operator, I want Preflight to re-read the current template, rules, sources and frozen chapters, so that stale checks cannot authorize an export.
15. As a user, I want the same rule to produce the same result for equivalent editing and frozen inputs, so that check behavior is predictable.
16. As a user, I want each validation issue to identify its rule, target, target version and ruleset hash, so that history can be audited and replayed.
17. As a project owner, I want a permitted issue to be resolved or dismissed with a reason, so that the decision is explicit and reviewable.
18. As a project owner, I want blocking issues, authorization failures and version mismatches to remain blocking, so that an annotation cannot bypass safety gates.
19. As an unauthorized member, I want issue actions and template edits to be rejected, so that project governance is enforced by the server.
20. As a project owner, I want a changed ruleset or target revision to create fresh issue evaluations, so that an old dismissal cannot silently close a new problem.
21. As an active-project owner, I want direct template edits to be refused, so that active project semantics change only through an evaluated upgrade.
22. As an active-project owner, I want a template upgrade to preview field, chapter, rule, confirmation and delivery impacts, so that I can decide whether to apply it.
23. As an active-project owner, I want an upgrade to apply atomically or not at all, so that project binding, migration results, impact tasks and audit records cannot be half-updated.
24. As a reviewer, I want old frozen snapshots to remain readable after an upgrade, so that historical deliveries can be reproduced.
25. As a user, I want stale asynchronous results to be retained as history but not overwrite the current project, so that delayed work cannot corrupt a newer revision.
26. As a user, I want model-assisted rule suggestions to be clearly marked as suggestions, so that AI cannot publish rules, change severity, dismiss issues or release a delivery.
27. As a workspace user, I want one editor and issue-center flow with loading, empty, conflict and recovery states, so that the UI does not create a second business state machine.
28. As a reviewer, I want reproducible request sequences and database side-effect evidence, so that each G4 behavior can be independently verified.

## Implementation Decisions

- Introduce the minimum domain concepts `TemplateProfile`, immutable `TemplateVersion`, project-scoped `ProjectTemplateCopy`, immutable `RuleSet`, declarative `RuleDefinition`, evaluation input/result and version-bound `ValidationIssue`.
- A project copy records its source template version, project binding, status, content hash, ruleset hash, migration result and audit information. A source update never mutates an existing copy.
- Draft writes use expected-version conditions and idempotency. Published versions are append-only; historical versions remain readable and replayable.
- Rule kinds are allowlisted: `presence`, `pattern`, `consistency` and `computed`. `model_assisted` may provide an explanation or suggestion, but cannot change severity, publish a rule, alter issue state or pass Preflight. Arbitrary user code and unrestricted expressions are rejected.
- The evaluator returns rule identity, target, severity, message, evidence, evaluator version and canonical ordering. Severity and ruleset hashes are server-owned values.
- Template Check and Preflight call the same evaluator contract. Preflight reads the current `DeliveryInput`/frozen input, revalidates sources and versions, and remains the only export gate.
- A validation issue's logical identity includes rule ID, target reference and target version; the stored result also binds the ruleset hash and evaluation input version. A new ruleset or target revision is evaluated as a new current result and cannot inherit an old decision.
- `resolve` and `dismiss` require current authorization, expected target/ruleset version and an auditable reason. `waive` is optional and policy-controlled; blocking issues cannot be waived. Issue disposition never bypasses source authorization, version checks, freeze integrity or G3 impact tasks.
- Active-project template changes reuse G3 `template_upgrade` ChangeSet and its atomic apply/stale semantics. G4 owns template and rule effects; G3 owns change coordination and impact-task truth; G5 owns permissions; G6 owns operational measurement.
- The workspace depends on narrow domain interfaces for catalog, copy, rule evaluation and issue actions. HTTP remains a thin adapter, and the frontend consumes server states instead of inventing local enums or permissions.
- Persistence must support uniqueness, restart read-back, historical replay and atomic binding/apply. SQLite is the first reproducible environment; PostgreSQL parity is required before production claims.
- The first delivery slice is G4-1 through G4-4: copy isolation, draft migration, deterministic evaluation, Template Check/Preflight and issue identity. Issue disposition, active upgrades and full UI follow only after their G5/G3 seams are stable.

## Testing Decisions

- Tests assert observable user behavior, persisted side effects, authorization outcomes and currentness; they do not assert private helper structure or a particular storage query.
- The template domain tests cover unknown versions, copy isolation, immutable history, field migration, orphan preservation, invalid rule definitions, stable hashes and concurrent/idempotent writes.
- Delivery tests cover equivalent editing/frozen inputs, deterministic ordering, blocked empty or incomplete chapters, stale frozen inputs, source/version drift and refusal to export when Preflight is not current.
- Workspace HTTP tests cover request envelopes, expected-version conflicts, idempotent retries, error mapping, reauthorization and the absence of side effects after rejected issue actions.
- Cross-domain tests cover G3 upgrade baselines, atomic rollback, stale asynchronous results, old snapshot replay and re-evaluation after ruleset or target revision changes.
- Frontend tests cover the existing workspace seam: loading, empty, conflict, permission failure, rule evidence, issue disposition, refresh/recovery and no-write cancellation. The UI must consume the server contract rather than duplicate evaluator logic.
- Prior art is the repository's existing TemplateReader contract tests, workspace version/idempotency tests, Delivery snapshot/currentness tests, G1 migration tests and T12–T14 freeze/export scenarios.
- Every acceptance scenario records the command, request sequence, response/error code, database side effects, sanitized rule input/output summary and one of `PASS`, `FAIL`, `NOT RUN` or `BLOCKED`. Real DOCX open/edit/save remains a separate integration check.

## Out of Scope

- Template marketplace, cross-project shared editing, arbitrary drag-and-drop template builders and real-time collaborative editing.
- User-uploaded executable rules, unrestricted scripting, automatic template publication, automatic severity changes or automatic export release.
- Automated judgments about novelty, academic value, correctness, expert quality or whether a research conclusion is true.
- Reimplementation of G3 ChangeSet/ImpactTask, G5's permission engine or G6's operations/metrics platform.
- Treating AI output, a dismissed issue or a stale result as proof that a source, confirmation or frozen delivery is current.

## Further Notes

- The long G4 design documents remain the reference for future detail, but they do not override the reduced second-stage scope or current T01–T16 contracts.
- Stop and reopen this spec if the shared evaluator cannot demonstrate equivalent results on editing and frozen inputs, if a client-controlled field can affect authorization/severity/currentness, or if active upgrades cannot preserve old snapshot replay.
- The next handoff is `/to-tickets`: split the implementation into G4-1 through G4-7 with explicit blocking edges, then implement each ticket in a fresh context using TDD and close with Standards/Spec review.
