# Spec：G7 正式章节的比较、复核包与依据可追溯

> 配套计划：[G7. 正式章节编辑器与 AI 辅助写作](beta-gap-development-plans/G7-formal-chapter-editor-ai.md)（总计划）、[G7. AI 写作动作族：开发计划](beta-gap-development-plans/G7-ai-action-family.md)（动作族切片）
> 相邻 Spec：[32-Spec G7 正式章节编辑器、版本与复核](32-Spec-G7正式章节编辑器版本与复核.md)、[33-Spec G7 章节 AI 辅助写作](33-Spec-G7章节AI辅助写作.md)

## Problem Statement

active 项目的正式章节已经可以编辑、自动保存工作副本、形成正式版本、提交复核、退回、确认和恢复。但这条线只做到「可写」与「可存」，没有做到「可比较、可复核、可追溯」。

Reviewer 拿到一章要签字的正文时，拿不到一份固定的复核输入：正文差异是什么、这一版依据了哪些主张与证据卡、模板规则报了什么问题、项目影响任务还开着哪些、哪些依据尚未人工确认——这些都要 Reviewer 自己重新推导。而当前提交复核只写了一个状态串加一条审计记录，Reviewer 手上没有任何交付物。

正文里的依据也只挂了一层：章节级有一个来源清单，正文里的引用标记指向资料块而不是证据卡；一个未确认的证据卡在侧栏显示为「待重新核对」，但在它所支撑的那句话旁边没有任何标记。于是写得很顺的一段话，看起来和已被正式确认的段落没有区别。

版本之间没有比较。冲突时用户只看到一个「当前输入已保留，请先重新读取」；恢复旧版本时也看不到差异。而版本对象自身不记录创建者、不记录正文是人写的、AI 生成的、AI 辅助整理的还是导入的，所以「这一章的哪一段是 AI 写的、由谁采纳的、来自哪一次运行」无法追溯。

结果是：复核成为一句承诺，追溯成为一句声明，两者都没有可核对的载体。

## Solution

在既有的正式章节表面（不新建第二套编辑器、工作副本或版本模型）上补齐三件事，并把它们的形状声明进冻结的 G7 写作契约。

**可比较**：提供唯一的章节版本比较操作，返回结构化差异——正文差异、章节结构差异、依据变化、待核项变化、确认变化、模板与规则版本变化——而不是两份正文并排。比较是读操作：不产生版本、不移动指针，对调用者无权读取的来源不返回任何来源正文。冲突界面与恢复界面都消费同一个比较结果。

**可复核**：把「复核包」定义成一个可持久化、可读取、可审计的交付物，在提交复核时生成，绑定目标版本与父版本、Context Revision、模板与规则版本、正文差异及其来源标记、所关联的主张与证据卡与来源锚点汇总、模板规则问题、项目影响任务、未确认项，以及 Reviewer 自己选择的核查范围。评审处置在提交与退回之外扩展出转派、要求修改、标记证据不足，每一种处置都必须带理由。复核包不确认任何内容；确认仍然只由确认操作产生，并且绑定目标版本与核查范围。

**可追溯**：引入段落级依据关联，一段文字可以分别关联项目主张、证据卡、来源锚点与来源记录，四者不合并为一个「引用」按钮。未确认的证据卡在它所支撑的段落旁显示为待核，且在章节确认时不得被当作正式依据。锚点按引用文本重定位，重定位不到就报告失效并要求人工重新关联，绝不猜。正式版本记录创建者、时间与正文来源标记（人工撰写 / AI 生成 / AI 辅助整理 / 导入），AI 辅助的版本还记录产生该 AI 贡献的运行标识。

恢复旧版本保持既有语义——复制进当前工作副本，不移动当前章节指针，不复活旧确认——同时补上总计划里缺的两件事：应用前显示差异，以及可能使项目语义倒退时提示进入逆向变更集。

## User Stories

1. As a Author, I want to compare two chapter versions and see the differences highlighted, so that I can see what actually changed before I adopt or restore anything.
2. As a Author, I want the comparison to cover body text, chapter structure, source and evidence associations, pending review items, confirmation state, and template/rule versions, so that a change in one dimension cannot hide a change in another.
3. As a Reviewer, I want the conflict view to show the real difference between my pending input and the other person's version, so that a conflict is a decision rather than a blind overwrite prompt.
4. As a Author, I want to compare the version I am editing against the version I based it on, so that I can tell what my own edit changed.
5. As a 科研用户, I want to compare a historical version against the current one without changing anything, so that comparing is safe.
6. As a Reviewer, I want the review submission to carry a review package, so that I review against a fixed snapshot instead of a moving page.
7. As a Reviewer, I want the review package to name the target version and its parent, the Context Revision, and the template/rule versions, so that I know exactly what is being reviewed.
8. As a Reviewer, I want the review package to carry the body difference and its source markers, so that I can see what the submission changed relative to its parent.
9. As a Reviewer, I want the review package to summarize the linked claims, evidence cards and source anchors, so that I judge support instead of re-deriving it.
10. As a Reviewer, I want the review package to include template rule issues, project impact tasks and unconfirmed items, so that a technical problem or an unconfirmed basis cannot hide inside well-written prose.
11. As a Reviewer, I want to choose the review scope when I submit, so that the later confirmation binds exactly the scope I checked.
12. As a Reviewer, I want to hand the review to another reviewer, so that the right person judges it.
13. As a Reviewer, I want to ask for specific revisions instead of a plain return, so that the author receives an actionable instruction.
14. As a Reviewer, I want to mark a review item as evidence-insufficient, so that insufficient support is visible rather than silently accepted.
15. As a Reviewer, I want every disposition to carry a reason, so that the record explains itself later.
16. As a 负责人, I want the review package and the dispositions recorded and auditable, so that a later reader can reconstruct why the chapter was confirmed.
17. As a Author, I want to see why my chapter was returned, reassigned or sent back for revision, so that I know what to act on.
18. As a Author, I want to attach a project claim, an evidence card, a source anchor and a provenance record to a specific passage, so that each statement carries its own basis.
19. As a 科研用户, I want those four kinds of association to stay distinguishable, so that a citation is not mistaken for confirmed evidence.
20. As a Reviewer, I want an unconfirmed evidence card to appear as pending inside the passage it supports, so that fluent text does not look confirmed.
21. As a 科研用户, I want the passage anchor to relocate when the text around it changes but the quoted text still occurs exactly once, so that the association survives ordinary editing.
22. As a 科研用户, I want a passage whose quoted text no longer occurs to be reported as stale rather than guessed, so that an association is never silently moved to the wrong passage.
23. As a Reviewer, I want the chapter version to record who created it and whether the text is human-written, AI-generated, AI-assisted or imported, so that I review the right risk.
24. As a Reviewer, I want an AI-assisted version to record the run that produced the AI contribution, so that an AI contribution can be traced to the run that made it and to the person who adopted it.
25. As a 负责人, I want the chapter's source list narrowed to the passages that actually use each source when I save, so that an unused source stops being attached to the chapter.
26. As a 科研用户, I want to see the difference before an old version is restored, so that I do not replace newer content blindly.
27. As a 科研用户, I want a restore that could move project semantics backwards to tell me to open a reverse change set, so that recovery does not bypass the semantic change boundary.

## Implementation Decisions

- Reuse the existing Workspace application service, the working-copy/ChapterVersion contracts, the candidate-adoption seam, the existing HTTP routes and the current front-end workspace components. This spec adds a second chapter editor, a second working-copy model or a second version model — it does not.
- One comparison operation. It returns a structured comparison covering body difference, chapter structure difference, association change, review-item change, confirmation change and template/rule version change. The existing "two complete bodies side by side" presentation is replaced by this comparison rather than kept alongside it.
- Comparison is a read. It creates no version, moves no pointer, and writes nothing. It requires current project read authorization plus current authorization for every referenced source; a source the caller may not read contributes no source text to the result.
- The review package is a persisted, readable, auditable object created when a chapter is submitted for review. It binds the target version and its parent, the Context Revision, template/rule versions, the body difference and its source markers, the summary of linked claims/evidence cards/source anchors, the template rule issues, the project impact tasks, the unconfirmed items, and the reviewer-chosen review scope.
- The review package references, it does not copy. It stores identifiers and version references rather than a snapshot of the body. The body difference is recomputed against the bound target version; a version the reader may no longer access is reported as unavailable rather than reconstructed.
- The review scope chosen at submission is what a later confirmation binds. Confirmation cannot widen the scope, and the review package is not a confirmation.
- Review disposition extends beyond submit and return: hand the review to another reviewer, request specific revisions, and mark a review item as evidence-insufficient. The disposition vocabulary is closed and every disposition carries a reason. Handing a review to another reviewer requires that reviewer to be authorized for the chapter.
- Passage-level association is a new layer above the chapter-level source list. The chapter keeps its chapter-level source list; a passage can additionally carry a project claim, an evidence card, a source anchor and a provenance record. The four are distinct objects and are never collapsed into one citation affordance.
- Unconfirmed evidence is visible inside the passage. An evidence card that is not confirmed is shown as pending at the passage that uses it, and it is never counted as a formal basis during chapter confirmation. The pending state is derived from the card's current state rather than stored as a separate flag that can drift.
- Association anchors relocate or report stale; they never guess. An anchor that identifies a passage by quoted text relocates to the new offset when the quoted text occurs exactly once in the current body; otherwise it is reported as stale and must be re-anchored by a person.
- The chapter version records its own provenance: who created it, when, and whether the text is human-written, AI-generated, AI-assisted or imported. An AI-assisted version additionally records the run that produced the AI contribution, so an AI contribution is traceable to the run that made it and to the person who adopted it.
- Saving a chapter recomputes the chapter-level source list from the passages that actually use each source; a source no longer used by any passage is no longer attached to the chapter.
- The comparison, the review package, the dispositions and the passage associations are exposed on the existing application/HTTP surface, and their shapes are declared in the frozen G7 writing contract. That contract is the single place where these operations, their request/response shapes and their side-effect guarantees are declared, and the contract check compares the open API description and the scenario definitions in the same run, so the three cannot drift.
- Restore keeps its existing semantics: it copies the historical version into the current working copy under the current revision, without moving the current chapter pointer and without reviving an old confirmation. This spec adds the two pieces the overall G7 plan still required: the difference is shown before the restore is applied, and a restore that could move project semantics backwards tells the user to open a reverse change set rather than applying it silently.
- Observability follows the data, not a new metric line. These operations produce the records that the G6 line needs — comparisons, review packages, dispositions, passage associations, version provenance — but the metric definitions, the trial instrumentation and the operations front end stay with the G6 line.
- This spec closes no AI action gap and no operations gap. The AI action family, the human/AI/AI-assisted candidate label and the candidate adoption confirmation payload belong to the AI writing spec and its action-family construction; owner unavailability, audit-failure alerting, agent research purpose and the context package front end belong to their own lines.

## Testing Decisions

- The primary seam is the application/HTTP surface. Cross-module, persistence, concurrency and audit changes must be proven through the real routes, the real service and the real database; a direct store test is supporting evidence only and cannot replace production-path evidence.
- The frozen G7 writing contract is the shape guard. The new operations, their request/response shapes, their side-effect guarantees, worked examples and counter-examples are declared in that contract, and the contract check compares the open API description and the scenario definitions in the same run. A shape change without a contract change is red.
- Counter-examples are part of the contract. Every new operation declares what it must not do: a comparison creates nothing, a review package confirms nothing, a restore does not move the pointer, an unconfirmed evidence card never counts as a formal basis.
- The user-visible side is tested with the existing front-end contract fixtures: the front-end type/open API drift check, the template-text assertions and the server-rendered component assertions. No new front-end test framework is introduced.
- Comparison is tested as a read: comparing two versions creates no version, moves no pointer, and returns no source text for a source the caller may not read.
- The review package is tested for immutability: submit, then change the chapter, then read the package — the package still describes the submitted version, and the chapter reports itself stale instead.
- The disposition vocabulary is tested for closure: every disposition carries a reason, a disposition outside the vocabulary is rejected, and handing a review to another reviewer requires that reviewer to be authorized for the chapter.
- Passage associations are tested for relocation and staleness: insert text above a passage and the association relocates; remove the quoted text and the association reports stale rather than moving to a different passage.
- Unconfirmed evidence is tested at the passage: an unconfirmed evidence card is rendered as pending inside the passage, and a chapter containing such a passage cannot be confirmed as if the basis were formal.
- Version provenance is tested for traceability: a version created by adopting AI output records that origin, the run that produced it, and the person who adopted it.
- Concurrency: two reviewers submitting or disposing the same chapter at once, and a comparison running while a version is being created, must not produce a half-written review package or a torn comparison.
- Prior art: reuse the existing working-copy, chapter-version, confirmation, review, candidate-adoption, source-revocation and G7 contract tests. Extend them; do not duplicate them.
- Run mock, real API with a fake model, and real separately. A mock result is never recorded as real-model evidence.
- This spec creation does not run tests. The test plan is a decision, not a result.

## Out of Scope

- A second chapter editor, a second working-copy model, or a second version model.
- Automatic academic judgment, automatic confirmation, automatic evidence confirmation, and automatic creation or application of a change set.
- Real-time collaborative editing and operational transform / CRDT infrastructure.
- Chapter hierarchy. The chapter remains a stable container of a template section. The chapter tree, the per-chapter owner and the per-chapter pending count are a separate decision, because the current chapter spec describes a flat template-section container while the overall G7 plan describes a tree — the two cannot both hold.
- The AI action family, the human/AI/AI-assisted candidate label, and the candidate adoption confirmation payload.
- G6 metric definitions, trial instrumentation and the operations front end.
- Owner unavailability handling, audit-failure alerting, agent research purpose, and the context package front end.
- Template rule evaluation, change set impact resolution, permission engine internals and the document renderer.
- Real-time template checks inside the editor, and the semantic-change prompt inside the editor.
- Deleting or silently overwriting user text.

## Further Notes

- This spec fills a gap, it does not restate. The two existing G7 specs already own the writing surface (editing, autosave, version creation, comparison intent, restore into the working copy, submit, return, confirm, stale transitions, permission checks) and the AI candidate surface (candidate generation, controlled adoption, source authorization, staleness, run modes). The overall G7 plan's delivery slices G7.2 (version difference), G7.3 (passage-level association) and G7.6 (review package and disposition) are the parts that had no contract, no implementation and no owning spec; this spec takes those three.
- Two spec conflicts surfaced while scoping and are resolved here. First, restore semantics: the overall G7 plan describes restoring as creating a new chapter version, while the existing chapter spec and the frozen contract describe it as copying into the current working copy. The latter is kept, because the plan's actual requirement — that a restore must not silently move the current pointer — is already satisfied, and because producing a formal version is the user's deliberate commit step. Second, chapter hierarchy: the plan describes a chapter tree, the existing chapter spec describes a flat template-section container. Hierarchy is excluded here and left as an open decision, because changing it touches the chapter model, the editor's document assembly and the migration.
- The actions in this spec depend on the version and working-copy contracts owned by the chapter spec, and on the candidate contracts owned by the AI writing spec. They produce the records the G6 line needs for adoption rate, partial-adoption ratio, generation failure rate, stale candidate rate, pending-item closure time, version conflict rate and confirmation rework rate; the metric definitions themselves stay with G6.
- The user's earlier decision on the action-family slice — one vocabulary, one candidate shape, one adoption model, and an explicit adoption confirmation — is unaffected by this spec, and the passage-level provenance defined here is what lets that slice's human/AI/AI-assisted label remain visible after adoption.
