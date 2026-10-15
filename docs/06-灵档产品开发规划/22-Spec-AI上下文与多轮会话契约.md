# Spec：AI 上下文与多轮会话契约

## Problem Statement

科研用户需要在同一个项目中连续和 AI 对话、引用调研结果、解释当前状态和继续写作。当前设计已经区分当前任务助手与调研智能体，但如果各自组装上下文，会出现原始会话混用、旧资料继续使用、权限撤回后仍暴露正文、跨章节隐式读取和版本陈旧结果覆盖当前内容等问题。

## Solution

建立两个 Agent 共用的上下文与会话契约：每个 Agent 保留自己的完整多轮会话；卡片、资料和任务结果通过可引用标识交接；每轮请求根据当前任务重新组装上下文包并重新检查权限、对象版本和资料状态。系统显示本轮引用范围和选择理由，但不要求用户逐张手工拼上下文。

## User Stories

1. As a 科研用户, I want to continue an Agent conversation over multiple turns, so that I can ask follow-up questions without restating the whole discussion.
2. As a 科研用户, I want to review the complete conversation history, so that I can understand how a suggestion was produced.
3. As a 科研用户, I want to see which project, chapter, task and revision a reply uses, so that I can judge whether it applies to my current work.
4. As a 科研用户, I want the Agent to propose relevant cards automatically, so that I do not need to understand internal data objects.
5. As a 科研用户, I want to remove or add proposed cards before a sensitive request, so that I retain control of the working scope.
6. As a 科研用户, I want a card referenced in one Agent conversation to be available to another Agent, so that I can carry research findings into writing guidance.
7. As a 科研用户, I want the original conversations of the two Agents to remain separate, so that process guidance is not confused with research evidence.
8. As a 科研用户, I want a revoked or stale source to be rechecked on the next turn, so that an old response cannot bypass current authorization.
9. As a 科研用户, I want a stale card to be marked clearly, so that I know when to refresh or rerun the task.
10. As a 负责人, I want every Agent run to record its context range, versions and authorization result, so that an action can be audited.
11. As a 项目成员, I want a task switch to keep the conversation history but recompute source content, so that continuity does not become implicit data leakage.
12. As a 安全责任人, I want the context builder to fail closed when identity, scope or version cannot be checked, so that uncertainty cannot become access.
13. As a 开发者, I want the task assistant and research agent to consume the same context contract, so that they do not implement incompatible authorization rules.
14. As a 开发者, I want card references to survive service restarts, so that a conversation can be resumed without copying sensitive text into client storage.
15. As a 审阅者, I want to see whether a reply used candidate, pending-review or confirmed material, so that I do not mistake a candidate for evidence.

## Implementation Decisions

- Introduce a shared Agent Context Package contract containing project, task/chapter, target object and revision, selected cards and source references, authorization snapshot, output constraints, model/task-card version and run metadata.
- Persist complete per-Agent conversation history and turn-level references; do not merge the original transcript of the task assistant with the research agent.
- Store card references and provenance as durable identifiers. The context builder may retrieve current content by identifier on each turn, subject to current authorization and version checks.
- Let the Agent propose and assemble a context package. The UI must show included cards, reasons, versions, status and permission state; users can edit the package before confirmation-sensitive work.
- Low-risk project/task status cards may be included without an extra confirmation. Sensitive source excerpts, cross-chapter content, new scope, or formal-object writes require an explicit action confirmation.
- A turn must not inherit raw restricted text solely because a previous turn saw it. Re-fetch or refuse according to current authorization and currentness.
- Context packages and Agent runs must carry correlation identifiers and be auditable without logging unnecessary research正文, secrets or tokens.
- Existing Workspace `GenerationContext`, SourcePolicy/AssetGateway checks, Candidate/Chapter version basis and G7 context-package semantics are the prior art; extend these seams rather than creating a second permission or generation context.

## Testing Decisions

- Test observable behavior at the context-builder and Agent transport seam: full conversation replay, card inclusion/removal, permission recheck, version staleness, project/task switch, and restart recovery.
- Test that revoked, cross-project, out-of-scope and unknown-version content is excluded or fails closed, and that the denial is visible without leaking restricted text.
- Test that a stale response cannot write to a newer working copy, ChapterVersion or project revision.
- Reuse existing Workspace generation, source currentness, candidate staleness, working-copy recovery and authorization tests; add contract tests shared by both Agents.
- Test mock, real API/fake model and real modes separately when the Agent is connected; do not treat mock context assembly as real-model validation.

## Out of Scope

- A shared raw transcript between the two Agents.
- Permanent inclusion of all project cards or all project正文 in every prompt.
- A new general-purpose ACL engine, RAG engine or workflow platform.
- Automatic confirmation of research claims, evidence, chapters or delivery.

## Further Notes

This contract is a prerequisite for the task assistant and research-agent specs. It preserves project-level continuity while making per-turn source selection explicit and re-authorized.
