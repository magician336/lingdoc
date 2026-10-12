# Spec：G7 正式章节编辑器、版本与复核

## Problem Statement

active 项目需要持续写作。用户必须知道当前章节、正文版本、Context Revision、权限和确认状态，并能在自动保存、多人修改、AI 候选和资料撤权时恢复工作。若把工作副本、正式版本和确认记录混在一起，用户可能误以为一次保存已经确认，旧版本也可能覆盖新版本。

## Solution

建立以 ChapterVersion 为核心的正式章节编辑器。EditorWorkingCopy 负责可恢复的编辑检查点，ChapterVersion 负责可比较、可确认和可交付的正式版本。编辑器提供章节导航、正文编辑和依据/任务面板；保存、提交复核、退回、确认、重新确认和恢复均绑定版本、Context Revision、模板/规则版本和当前权限。

## User Stories

1. As a 章节负责人, I want to open the chapters I am authorized to edit, so that I can work without seeing an empty or unrelated editor.
2. As a 科研用户, I want the editor to show project, chapter, Context Revision, ChapterVersion, template version and save state, so that I know what I am editing.
3. As a Author, I want autosave to preserve a recoverable working copy, so that a browser or service failure does not lose my work.
4. As a Author, I want autosave failure to be visible, so that I do not mistake a local draft for a server save.
5. As a Author, I want to create a new ChapterVersion deliberately, so that ordinary editing does not silently confirm or publish content.
6. As a Reviewer, I want to compare versions and see the author, source revision and pending items, so that I can review the correct content.
7. As a Reviewer, I want to return a chapter with a reason, so that the author knows what to change.
8. As a Owner or Reviewer, I want to confirm one fixed version and scope, so that confirmation cannot drift to later edits.
9. As a 科研用户, I want a confirmed version to become stale when its evidence, project semantics, template or permission changes, so that old confirmation is not reused.
10. As a Author, I want to restore a historical version into a current working copy, so that recovery does not silently move the project pointer backward.
11. As a Author, I want concurrent edits to produce a conflict, so that another person's work is not overwritten.
12. As a Author, I want comments, tasks and pending items attached to a version or range, so that review work is traceable.
13. As a 科研用户, I want source citations and EvidenceCard/ResearchClaim links preserved when I save, so that writing remains auditable.
14. As a 项目成员, I want changes to project permissions or source access to affect the next read and save, so that a working copy cannot bypass current authorization.
15. As a 负责人, I want active project boundaries to prevent ordinary ProjectSpec writes from changing formal chapter semantics, so that semantic changes enter ChangeSet.
16. As a 科研用户, I want a chapter to show whether it is DRAFT, IN_REVIEW, CONFIRMED, STALE or ARCHIVED, so that I know the next action.
17. As a 交付负责人, I want only frozen, current and authorized versions to enter ReleaseSnapshot, so that editing state cannot leak into export.
18. As a QA 负责人, I want restart and retry behavior to be observable, so that recovery evidence is reproducible.

## Implementation Decisions

- Reuse the existing Workspace ApplicationService, working-copy/ChapterVersion contracts, candidate-adoption seam, HTTP routes and current frontend Workspace components. Do not create a second chapter editor service.
- Model Chapter as a stable template section container. Model ChapterVersion as immutable, versioned body plus parent, project revision, template/rules versions, source/claim/evidence associations, provenance, creator, status and generation metadata.
- Model EditorWorkingCopy as a mutable checkpoint bound to a base ChapterVersion, editor/session, project/spec revision, source IDs, citation usages, review items and conflict state. Autosave updates the working copy only.
- Model CandidateDraft separately from ChapterVersion. AI or task output is previewable, rejectable and adoptable; it never changes the current formal version without explicit user action.
- Use statuses DRAFT, IN_REVIEW, CONFIRMED, STALE and ARCHIVED. Confirmation binds target version, target revision and review scope. A later version is derived from a prior version and does not mutate the confirmed record.
- Ordinary wording, ordering, formatting and comments remain ChapterVersion/working-copy edits when they do not change project semantics. Research-condition, claim, evidence-scope, template or route semantics enter G3 ChangeSet.
- Every save, commit, restore, submit, return and confirm checks current G5 capability/scope, project state, expected project/spec/working-copy/chapter versions and current source authorization.
- Use conditional writes and idempotency keys. A conflict returns the current conflict state and preserves both the user's working copy and the current formal version; no implicit merge is performed.
- Restoring a historical version copies it into the current working copy under the current revision. It does not move the current chapter pointer backward and does not revive old confirmation.
- A chapter confirmation or version change can trigger G3/G4 stale handling. G4 owns rule issues; G5 owns permissions; G6 owns operational metrics; G7 owns editor transitions and user presentation.
- The editor uses three visible areas: chapter navigation/status,正文 editing/work copy, and evidence/claims/source/rules/tasks/comments/AI assistance. Top-level status must expose authorization and save/processing state.
- ReleaseSnapshot and export consume explicitly frozen current versions only. Historical versions remain readable according to current authorization and cannot become current by ordinary read.

## Testing Decisions

- The user confirmed reuse of the existing Workspace ApplicationService, working-copy/ChapterVersion APIs, candidate-adoption seam and HTTP/frontend routes. Test user-visible behavior, not storage table layout.
- Cover chapter list/detail, working-copy load/save, autosave retry, commit, restore, version comparison, submit/return/confirm, stale transition, permission denial and export input selection.
- Cover concurrent saves, stale project/spec/chapter/working-copy versions, idempotency replay, conflicting payloads and restart recovery.
- Cover source revocation, removed/suspended member, changed Context Revision, template/rules change and confirmation invalidation.
- Assert no ordinary save creates confirmation, no restore rewinds the current pointer, no conflict silently overwrites another version, and no historical version enters a current ReleaseSnapshot without explicit adoption.
- Reuse existing working-copy, chapter-version, candidate adoption, confirmation, G3 delivery and source-revocation tests; add browser-level contract fixtures for status and recovery messages.
- Run mock, real API/fake model and real dependency evidence separately. This Spec creation does not run tests.

## Out of Scope

- Automatic academic confirmation or scientific correctness.
- Full real-time multi-user editing and operational transform/CRDT infrastructure.
- Changing ProjectSpec through ordinary chapter save.
- Template/rule evaluation, ChangeSet impact resolution, permission engine or DOCX renderer implementation.
- Automatic deletion or silent overwrite of user text.

## Further Notes

This Spec is the formal writing surface. The companion AI Spec supplies CandidateDraft generation and controlled adoption; G3, G4, G5 and G6 remain the authorities for semantic change, rules, permission and operations.
