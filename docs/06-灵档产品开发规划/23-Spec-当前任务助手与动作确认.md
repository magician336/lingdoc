# Spec：当前任务助手与业务动作确认

## Problem Statement

科研用户不一定熟悉软件操作。单独提供“生成”按钮无法告诉用户当前项目处于什么状态、为什么被阻断、下一步该做什么，或如何恢复失败。用户需要一个独立的当前任务助手，既能在关键状态变化时引导，也能随时对话；同时 Agent 必须能准备并执行业务动作，但不能把模型回复误当成用户确认或学术判断。

## Solution

提供当前任务助手 Agent。它在关键状态变化时展示摘要卡片，在任意页面提供对话入口；卡片可以被引用到同一 Agent 的多轮对话。Agent 可以读取、解释、检索、整理和生成候选，也可以提出保存研究条件、导入资料、创建 EvidenceCard、应用 ChangeSet、提交章节、确认和导出的动作。所有有副作用的完整业务动作先展示目标、输入范围、版本、权限、预期结果和可逆性，由用户明确确认后执行。

## User Stories

1. As a 科研用户, I want to open the task assistant from any project page, so that I can ask what to do next.
2. As a 科研用户, I want to see a concise card when a key state changes, so that I notice work that needs attention without being interrupted by a dialog.
3. As a 科研用户, I want to ask follow-up questions in the same Agent conversation, so that the assistant understands the immediate discussion.
4. As a 科研用户, I want each card to explain its trigger, source, status and next action, so that I can decide whether to act.
5. As a 科研用户, I want the assistant to explain why a project cannot proceed, so that I can fix the blocking condition.
6. As a 科研用户, I want the assistant to propose a task-card or form with fields prefilled, so that I do not have to translate advice into UI operations.
7. As a 科研用户, I want the assistant to prepare a research-condition save, so that I can review the proposed values before writing them.
8. As a 科研用户, I want the assistant to prepare an import, evidence-card creation, ChangeSet, chapter submission, confirmation or export, so that complex operations are easier to start.
9. As a 科研用户, I want one confirmation for one complete business action, so that I do not approve hidden internal API steps one by one.
10. As a 科研用户, I want the confirmation preview to show scope, versions, permissions and expected effects, so that I know what will change.
11. As a 科研用户, I want to cancel or reject a proposal before it writes, so that the Agent cannot silently change my work.
12. As a 科研用户, I want failed or timed-out actions to preserve my input and run record, so that I can retry without losing text.
13. As a 科研用户, I want to undo a candidate or working-copy change, so that I can safely experiment.
14. As a 负责人, I want an applied ChangeSet to be recoverable through a reverse ChangeSet, so that history and audit remain truthful.
15. As a 审阅者, I want a content confirmation to state that it authorizes a business action rather than proves an academic conclusion, so that governance is not confused with scientific validation.
16. As a 安全责任人, I want permission expansion, deletion, formal-version overwrite and blocking-issue closure to require explicit confirmation, so that high-risk actions are never automatic.
17. As a 产品负责人, I want proactive prompts limited to meaningful status changes, so that the assistant remains helpful rather than noisy.
18. As a QA 负责人, I want every proposal, confirmation, execution, rejection, retry and recovery to be traceable, so that failures can be diagnosed.

## Implementation Decisions

- Trigger assistant cards for project creation/spec changes; asset ready/failed/revoked/locator-invalid; AI candidate ready/failed/stale; review items, conflicts or invalidated confirmations; ChangeSet impacts; Template Check/Preflight blockers; and export ready/blocked.
- Keep cards non-modal by default. They show a summary and optional next action; they do not auto-open a conversation or execute a write.
- Provide a persistent assistant entry from project preparation, project overview, evidence, writing, change/impact, review and delivery routes.
- Support read/explain/search/organize/candidate-generation actions automatically. Require confirmation for saving research conditions, importing assets, creating or confirming EvidenceCards, applying ChangeSets, submitting chapters, confirming content and exporting.
- A confirmation card represents one complete business action. It includes target objects, selected context, input range, expected versions, permissions, expected side effects, reversibility and failure behavior.
- Confirmation must be an explicit UI action; a natural-language “yes” in an ordinary answer is not sufficient unless the UI has entered a clearly labelled confirmation state.
- Business-action confirmation is not academic proof. EvidenceCard confirmation and Chapter confirmation continue to require their existing role, target version, scope and reason rules.
- Candidate and working-copy changes are recoverable by undo or version restore. Asset/evidence changes retain history and can be deactivated or removed from current scope. Applied semantic changes use reverse ChangeSets. Submission and confirmation retain the original record and create a new revision or re-confirmation. Export cannot retract an already downloaded external file; it can stop later downloads and mark the current artifact invalid.
- Failed or timed-out actions retain user input and Agent run state, and idempotency prevents duplicate writes.
- Reuse existing Workspace operation/idempotency, CandidateAdoption, working-copy, ChangeSet, Confirmation, Template Check and Delivery seams. The assistant orchestrates these business services and does not become a second write model.

## Testing Decisions

- Test notification triggers and dismissal/read states from user-visible state transitions, not internal event names.
- Test that read-only suggestions never write, and that each write action requires the correct explicit confirmation and current authorization/version check.
- Test duplicate confirmation, lost response, timeout, retry, stale target, permission revocation, owner unavailable, reverse ChangeSet and export invalidation.
- Test that undo/recovery creates truthful history and never mutates an immutable ChapterVersion, Confirmation or ReleaseSnapshot in place.
- Reuse existing idempotency, candidate-adoption, working-copy recovery, ChangeSet atomicity, confirmation currentness and delivery authorization tests.

## Out of Scope

- An always-open modal or unsolicited multi-turn conversation.
- Automatic expansion of permissions, deletion, formal-version overwrite or blocking-issue closure.
- The assistant deciding scientific validity, selecting a research method, or declaring evidence sufficient.
- Real-time collaborative editing, OT/CRDT or a general workflow engine.

## Further Notes

The assistant should adapt its wording to a single user who may own several consecutive responsibilities. It should not require a role handoff to continue the project.
