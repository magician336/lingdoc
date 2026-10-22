# Spec：调研智能体的研究提案

> 发布副本：[Issue #393](https://github.com/magician336/lingdoc/issues/393)。由施工并行面 PF-5（[#329](https://github.com/magician336/lingdoc/issues/329)）交付。
> 配套计划：[G2. 调研智能体的研究提案：开发计划](beta-gap-development-plans/G2-research-proposal.md)（决策记录、验收矩阵与施工票序列）

## Problem Statement

When a 科研用户 asks the 调研智能体 to help find material, what comes back is an untyped string. The model may answer with a 调研任务建议 (`task_card`) or may not; when it does, the payload has no shape the server checks — the fields may be missing, the wrong type, or contain a single 查询词 where the product requires at least two. The 科研用户 then copies those words one by one into the 调研任务 form, and the first sign of a problem is a client-side complaint («请填写至少两条查询») long after the model spoke.

Worse, the suggestion carries no boundary. The 调研智能体 can mention a 候选来源 that was never in the 授权上下文 of that turn — or that the 科研用户 has already had revoked — and the interface copies it into the form as if it were a real source. The 科研用户 cannot tell a real 来源 from a 编造的出处, and the gap the 调研智能体 actually noticed (the 证据缺口) is never shown at all.

The result is that the capability the 调研智能体 is supposed to have — 推荐查询词与来源，并能解释理由与缺口 — exists only as three sentences inside a model prompt. Nothing owns it, nothing validates it, and the 调研智能体's answer is 候选 in a weaker sense than the rest of the product: it is not even a candidate, it is prose.

## Solution

The 调研智能体 gets one answer shape that the server owns and checks: a **研究提案 (research proposal)**.

A 研究提案 is produced by the model, decoded and validated by the server against a single contract, and delivered on the existing 调研智能体 turn endpoint. It carries:

- an **editable 调研任务草案** whose field names are the ones the 调研任务 form already uses — 目标 (goal)、问题类型 (question_type)、范围 (scope)、排除项 (exclusions)、查询词 (queries, at least two)、预期产物 (expected_output);
- **候选来源** — each one a citation of a source that is present, and currently authorized, in that turn's 上下文包. A source the model did not get may not be cited;
- **理由** — why this plan, and why each 候选来源;
- **缺口** — what is still missing for the question to be answerable.

The contract has three states, as the 助手's 影响说明 contract already has: **present** (a valid proposal), **absent** (the model proposed nothing — the turn still returns its natural-language reply), and **invalid** (the model proposed something that cannot be trusted — the proposal is refused with a readable failure). A proposal is never silently reshaped into something else.

A 研究提案 is always a 候选. It does not become a 调研任务, an 导入候选, an 证据卡 or a 研究主张 without an explicit act by the person: the 科研用户 edits the 草案 and saves it as a 调研任务, exactly as they do today, and the 候选来源 it names are shown with their 理由 and the 缺口 beside them.

## User Stories

1. As a 科研用户, I want the 调研智能体 to give me an editable 调研计划, so that I do not have to invent 查询词 from scratch.
2. As a 科研用户, I want the 建议 to require at least two 查询词 before it reaches me, so that I am not told «请填写至少两条查询» after the model has already spoken.
3. As a 科研用户, I want to see the 理由 for the proposed 查询词, so that I can judge whether the 调研方向 is the one I meant.
4. As a 科研用户, I want to see the 候选来源 the 调研智能体 picked, so that I can start from material instead of from nothing.
5. As a 科研用户, I want every 候选来源 to be one I am actually allowed to read right now, so that a recommendation cannot widen my own access.
6. As a 科研用户, I want to be told when a 候选来源 cannot be traced back to my 授权上下文, so that I never mistake a fabricated 出处 for a real 来源.
7. As a 科研用户, I want the 调研智能体 to tell me the 缺口 it noticed, so that I know what the 调研 is still missing.
8. As a 科研用户, I want the 缺口 expressed as what is missing and what would fill it, so that I can decide whether to run the 调研 at all.
9. As a 科研用户, I want the 建议 to arrive in the 调研任务 form I can edit, so that I can change 范围、排除项与查询词 in my own words.
10. As a 科研用户, I want the 建议 to be a draft — nothing is created until I save it, so that exploring a question does not litter my project with 调研任务.
11. As a 科研用户, I want the natural-language answer to survive even when there is no 建议, so that asking a question still gets an answer.
12. As a 科研用户, I want an invalid 建议 to fail loudly with a readable reason, so that a broken suggestion is never rendered as a plausible one.
13. As a 科研用户, I want a 建议 that cites a 来源 I have lost access to to be refused, so that revocation takes effect on the 调研智能体 too.
14. As a 科研用户, I want a 建议 that cites material from another project to be refused, so that project boundaries hold for the 调研智能体 as well.
15. As a 科研用户, I want the 调研智能体's 建议 to never become an 证据卡 or a 研究主张 on its own, so that a 模型输出 is never read as a confirmed 依据.
16. As a 科研用户, I want anything the 调研智能体 points at to stay an 导入候选 until I confirm it, so that retrieval results are not silently promoted.
17. As a 项目负责人, I want each 调研建议 to record the 模型 and 运行模式 it came from, so that a demo run is never presented as real-model evidence.
18. As a QA 负责人, I want the model-output contract to have exactly one owner, so that a change to the 研究提案 shape cannot leave the prompt and the server disagreeing.
19. As a QA 负责人, I want the 调研智能体's answer contract to be enforced by a guard, so that a future change cannot quietly reintroduce an unvalidated payload.
20. As a developer, I want the 用途 (purpose) vocabulary owned by the server, so that the model adapter cannot branch on loose strings that drift apart from the service.
21. As a developer, I want the new contract to live in its own module rather than in the service file that already holds seven concerns, so that a change here does not amplify across the agent surface.
22. As a 科研用户, I want 同义词 to arrive as additional 查询词 rather than as a separate concept, so that the 调研任务 I save has one query list I can read.

## Implementation Decisions

1. **One contract owns the 研究提案 shape.** A new research-proposal contract (type + decoder + key + state constants) is the only place that knows what a 研究提案 is. It lives in its own module in the server's domain layer, alongside the two model-output contracts that already exist there (the 助手's 影响说明 and the 变更草稿), and it is registered in the 施工并行面登记处 as part of this face's path set. The prompt renders the shape from the contract; the shape is not written out a second time in a prompt string.

2. **The contract's fields are the 调研任务's fields, by the same names.** 目标、问题类型、范围、排除项、查询词、预期产物 use the names the 调研任务 form and the stored 调研任务 already use. The proposal introduces no second vocabulary. 同义词 are delivered as additional 查询词 entries rather than as a new field, because the stored 调研任务 has no such field and adding one would change the shape of every task saved from now on.

3. **The proposal adds 候选来源、理由、缺口 on top of the task-shaped part.** The 候选来源 entries cite a source by id plus a 理由; the 缺口 entries say what is missing and what would fill it. These are additions to the existing editable-suggestion shape, not a second card: one 建议 object, read by one consumer.

4. **A 候选来源 must be traceable to the turn's authorized context.** Every cited source id must appear among the turn's 上下文包 source references, and must be allowed for model use. A proposal that cites a source it was not given is **refused**, not downgraded: a fabricated 出处 is a different failure from a missing annotation, and the product already refuses fabricated 授权来源 elsewhere. Sources that are unauthorized, revoked, or belong to another project are not in the context in the first place, so a proposal cannot reach them at all.

5. **Three states, as the 影响说明 contract already has them.** `present` — a valid proposal, carried on the turn. `absent` — the model proposed nothing; the turn keeps its natural-language reply and carries no proposal. `invalid` — the model proposed something that cannot be trusted; the proposal is refused with a readable failure. A refusal is of the **proposal**, not of the whole turn's usefulness: the natural-language reply is kept where the model produced one, so a broken 建议 does not cost the 科研用户 their answer. The 助手's existing editable-suggestion path keeps its current behaviour; this spec does not tighten it, and that asymmetry is recorded as a known residual.

6. **Purpose is derived by the server, not declared by the client.** A session declares its agent; the server derives the answer shape (purpose) from that agent. The 调研智能体's turns therefore produce a 研究提案; the 助手's turns keep producing conversation. The client states no new request field, so the request shape and the idempotency digest are unchanged and previously issued keys still replay. The purpose and agent vocabularies become server-owned constants, and the model adapter refers to them instead of comparing loose strings in three places.

7. **Delivery is the existing turn endpoint.** The proposal is carried on that turn's structured suggestions, under the same key the editable 调研任务建议 already uses, so the existing adoption path in the 调研面板 consumes it unchanged. The panel additionally renders the 候选来源 (with their 理由) and the 缺口 beside the editable 草案. No new endpoint and no new idempotency key are introduced.

8. **A proposal never becomes anything by itself.** Becoming a 调研任务 is the existing explicit save; becoming an 导入候选, an 证据卡 or a 研究主张 stays behind the existing explicit confirmation and human-gate paths. The 检索 result levels that already exist apply to retrieval output, not to a model's proposal: a proposed 候选来源 and a retrieved result are two different things and do not share one level vocabulary.

9. **Each proposal records its provenance.** The 模型 id and 运行模式 of the turn that produced it are recorded with it, so a mock run is distinguishable from a real-model run.

10. **The new contract does not go into the large service module.** The agent service module already holds research, import, anchor, claim, card, session/turn and proposal concerns; this spec adds a module of its own instead of a further section to that file. Restructuring that service is out of scope.

## Testing Decisions

A good test here asserts what a 科研用户 or a 项目负责人 can observe — what the turn carries, what the 调研任务 list contains, what a failure says — and never how a particular function is written internally. The 调研智能体 is exercised through a fake model, so the tests describe the model contract rather than a real provider.

- **Primary seam — the existing turn endpoint** (the 调研智能体's turn, through the application/HTTP surface, with a fake model):
  - a valid 研究提案 is carried on the turn, with its 候选来源、理由、缺口 alongside the editable task-shaped part;
  - a turn with no proposal still returns its natural-language reply and carries no proposal;
  - an invalid proposal is refused with a readable failure, and the proposal is not carried;
  - a proposal citing a source that is not in the turn's authorized context is refused, and a proposal citing a revoked or cross-project source is refused too;
  - a proposal does **not** create a 调研任务, an 导入候选, an 证据卡 or a 研究主张 on its own — the 调研任务 list stays as it was;
  - the 助手's turns keep their current behaviour (its editable suggestion is untouched, its 影响说明 and 变更草稿 contracts still behave as they do).
- **Contract seam — the decoder** (table-driven, same idiom as the existing 影响说明 and 变更草稿 contract tests): a missing required field, fewer than two 查询词, an unknown field, a 候选来源 that is not in the context, a 理由 or 缺口 beyond its length limit, and an oversized payload are each rejected; a valid payload is accepted.
- **Regression**: the 调研任务 save/approve/run flow, the 导入候选 flow, the 证据卡 and 研究主张 confirmation gates, and the 助手's conversation behaviour are unchanged; the existing guards stay green; the server's own vocabulary guard must hold — the model adapter refers to the server-owned purpose and agent constants rather than to loose strings.
- **Mode**: the model call is exercised through the mock/fake-model modes already in the codebase, and the 运行模式 is recorded on the turn. **Not run**: the real provider path.
- **Prior art**: the existing 影响说明 and 变更草稿 model-output contracts and their decoder tests, and the existing 调研智能体 turn tests, are the model for both seams.

## Out of Scope

- Validating the 助手's editable 调研任务建议 — it keeps its current behaviour; tightening it is separate work.
- The 上下文包's front-end consumption (another parallel face).
- Changing the retrieval side: 调研任务 execution, 导入候选 creation, 证据卡 and 研究主张 confirmation are all already in place and are not touched.
- Restructuring the large agent service module.
- 同义词 as a separate stored field on 调研任务.
- Any automatic promotion of a proposal into a 调研任务, 导入候选, 证据卡 or 研究主张.
- Making the real provider path work.

## Further Notes

- This spec delivers the research capability that Spec #135 (调研任务与调研智能体) and Spec #125 (调研智能体首轮闭环) both depend on. The relationship between those two Specs is recorded in the path diagram's known-contradictions section; this spec does not merge or trim them.
- The 施工并行面登记处 is the single authority for path ownership. The new contract module and the front-end files this spec touches must be registered into this face's path set before construction starts, and the face's ticket currently names two files that no longer exist — the registry's set is the correct one.
- The G7 contract files (the OpenAPI document, the scenarios, and the G7 writing contract) must gain the 研究提案 shape and its negative cases alongside the implementation, and the contract validators must accept them.
- Two 语义遗留 from the neighbouring G7 work are explicitly not settled here and must not be settled by this spec: whether 续写 replaces a selection or is inserted after it, and whether 提纲 replaces the whole working copy or coexists with the text already there.
