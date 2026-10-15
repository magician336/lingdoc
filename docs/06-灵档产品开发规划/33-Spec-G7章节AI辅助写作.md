# Spec：G7 章节 AI 辅助写作

## Problem Statement

科研用户需要 AI 帮助提纲、续写、改写、压缩、扩展、依据整理和语言审校，但 AI 输出不能直接成为正式章节、证据或确认。用户还需要知道每次建议使用了哪些资料、主张、版本和权限范围，并能在资料撤权或项目变化后安全处理旧候选。

## Solution

在正式章节编辑器中加入受控的 AI 写作入口。每次运行固定目标章节/选区和显式上下文包，生成 CandidateDraft、引用建议和 review_items；用户查看差异、依据、限制和版本后局部或整段采纳到 EditorWorkingCopy，再由用户保存 ChapterVersion、提交复核或确认。AI 不获得独立写入正式对象的能力。

## User Stories

1. As a Author, I want AI to suggest an outline for a chapter, so that I can organize my writing without losing control.
2. As a Author, I want to request continuation, rewrite, compression or expansion for a selected range, so that AI works on the task I actually chose.
3. As a Author, I want to set the output goal, length, tone and constraints, so that a candidate fits my writing task.
4. As a Author, I want to see the project, chapter, revision and selected range used by AI, so that the result has a clear boundary.
5. As a Author, I want to choose which ProjectSpec, assets, EvidenceCards, claims and sources are included, so that AI does not silently read unrelated content.
6. As a Author, I want AI to propose source or claim links separately from generated prose, so that a citation suggestion is not mistaken for confirmed evidence.
7. As a Author, I want every generated candidate to show model/task version, run status and provenance, so that I can reproduce or question it.
8. As a Author, I want to compare candidate and current text side by side, so that I can adopt only the useful changes.
9. As a Author, I want to accept part of a candidate, so that unrelated or unsupported sentences are not imported.
10. As a Author, I want rejection and cancellation to leave the current working copy unchanged, so that experimentation is safe.
11. As a Author, I want unsupported statements and missing sources shown as review items, so that fluent text does not look confirmed.
12. As a Reviewer, I want to see whether text was human-written, AI-generated or AI-assisted, so that I can review the right risk.
13. As a Owner, I want AI to respect G3 ChangeSet boundaries, so that semantic project changes do not hide inside a writing action.
14. As a 项目成员, I want a revoked source or changed revision to stale a candidate, so that old AI output cannot be adopted as current.
15. As a 项目成员, I want permission loss to stop AI from reading or applying a candidate, so that a running task cannot bypass revocation.
16. As a 科研用户, I want the task assistant to explain why a candidate is stale or blocked, so that I know whether to refresh, rerun or ask a responsible person.
17. As a Author, I want failed or interrupted generation to retain its run state and reason, so that I can retry without losing my work.
18. As a QA 负责人, I want model, mock and real modes separated, so that demo output is not presented as real-model evidence.
19. As a 项目负责人, I want AI to suggest next actions but never confirm academic conclusions, so that responsibility remains with authorized people.

## Implementation Decisions

- Reuse the existing generation application, selected-rewrite service, Candidate/WorkingCopy/ChapterVersion contracts, source authorization and Workspace HTTP/frontend routes. Extend existing seams instead of adding a second AI writing pipeline.
- Define each run with project, chapter, selected range, target working-copy/version, Context Revision, template/rules versions, explicit context package, allowed source scope, model/task-card version, run mode, status, cancellation/retry metadata and correlation IDs.
- Output is a CandidateDraft plus separately structured citation/evidence suggestions and review_items. A candidate is not a ChapterVersion, EvidenceCard, ResearchClaim or Confirmation.
- Supported actions include outline, generate, continue, rewrite, compress, expand, translate/explain and language review. The action and constraints are visible before execution.
- Context package includes only current authorized project/chapter/task cards, selected ProjectSpec fields, permitted ProjectAssets/SourceAnchors/EvidenceCards/ResearchClaims and output constraints. The UI shows scope, versions and reasons; Agent auto-assembly cannot expand permissions.
- Current task assistant and research agent can provide citeable cards through the shared AI context contract, but raw conversations are not merged. Each run rechecks current authorization and version.
- AI-generated source markers must correspond to authorized source IDs and stable locators. Model-invented citations are rejected or displayed as unsupported review items.
- Adoption requires explicit user confirmation of the candidate action and target range. Partial adoption writes EditorWorkingCopy through the existing conditional/idempotent seam; full adoption must show replacement scope and diff. No adoption directly confirms content.
- On source revocation, permission loss, Context Revision change, chapter-version change, template/rules change or missing source, candidate becomes STALE/NOT_AUTHORIZED and cannot be adopted. Historical candidate content is retained only according to current authorization and redaction policy.
- Failure and cancellation retain run input, status and reason. Retry uses a new idempotency key or replays the original operation according to existing operation semantics; it cannot duplicate working-copy writes.
- G3 semantic-change detection remains authoritative. AI may flag a possible semantic change, but the user must choose whether to create a ChangeSet; AI cannot apply ChangeSet, confirm evidence, confirm content or export.
- G5 checks capability, chapter/asset scope, source access and current versions before model invocation, before candidate read and before adoption. G6 receives run mode, latency, failure, stale and adoption metrics.
- The UI distinguishes candidate prose, source support, uncertainty, review_items and confirmed content. “采纳” means write to working copy, not academic approval.

## Testing Decisions

- The user confirmed reuse of Workspace ApplicationService, generation/selected-rewrite, candidate adoption, working-copy/ChapterVersion APIs and HTTP/frontend routes. Assert user-visible candidate, adoption, version, permission and recovery behavior.
- Cover selected-range generation, context display, candidate preview, partial/full adoption, rejection, cancellation, retry, timeout, malformed output and unsupported citation.
- Cover source revocation before model call, during run, before candidate read and before adoption; cover changed project/chapter/template/rules versions and cross-project or out-of-scope sources.
- Assert AI cannot write formal ChapterVersion directly, cannot create confirmation, cannot invent authorized sources and cannot apply ChangeSet or export.
- Reuse existing generation, selected-rewrite inherited-source, source authorization, candidate adoption, working-copy conflict and budget tests; add contract cases for task assistant/research-agent card references.
- Test mock, real API/fake model and real modes separately. Record model/task version, inputs, output status, usage, failure and human adoption; do not treat mock output as real-model quality evidence.

## Out of Scope

- Automatic academic judgment, evidence confirmation, content confirmation or export.
- A general-purpose autonomous writing Agent with unrestricted project access.
- Automatic semantic ChangeSet creation/application.
- Full citation-format engine, OCR platform, controversy resolution or real-time co-editing.
- Silent overwrite of formal chapters or automatic deletion of user text.

## Further Notes

This Spec depends on the formal editor/version Spec, the shared AI context and action-confirmation Specs, G2 source/evidence behavior, G3 semantic-change boundary, G4 rule evaluation and G5 authorization/revocation.
