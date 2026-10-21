/**
 * G3 验收台-11（#295）· 助手两组说明与依赖候选批次读回链的单测。
 *
 * 这些断言盯住本票最容易出错的几个口径：
 * - 助手说明**只是助手的解读**，字段名是 affected_explanation，与影响确定性
 *   （impacts[].certainty）**不是同一回事**；
 * - 缺 `affected_explanation` 的草稿**必须容忍**（记「助手未给两组说明」而不是崩掉）；
 * - 依据必须能指回一条真实记录（依据类型在词表内且指回了具体记录）；
 * - 候选三态：确认产生边、修改记住被替换元组、拒绝**不产生**边；
 * - 同版本不再重提；确认时版本复检（依据已变更的候选被拒）。
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  G3_ASSISTANT_BASIS_TYPES,
  G3_ASSISTANT_STEPS,
  hasTraceableBasis,
  readAssistantExplanation,
  readDependencyCandidateBatchFact,
  runAssistantChain,
  verdictAcG314,
  verdictCandidateBatchSubmitted,
  type G3AssistantChainResult,
} from './assistant'
import type { G3ProjectState } from './changeset'
import { createFakeClient } from './fakeHttp'
import { createRunContext, type G3RunContext } from './runContext'

function ctxOf(): G3RunContext {
  return createRunContext({ runId: 'g3r-as-1', accountEmail: 'g3demo@example.com' })
}

const ASSISTANT_PROJECT_PATH = '/api/v1/lingdoc/projects/p1'

/** 本链用的项目状态（期望值从这里取）。 */
function projectStateOf(): G3ProjectState {
  return {
    id: 'p1',
    status: 'active',
    projectVersion: 3,
    specRevision: 2,
    currentContextRevision: 1,
    spec: {
      research_subject: '合成课题',
      research_goal: '旧的研究目标',
    },
  }
}

/** 模型这轮给出的结构化输出（变更草稿候选 + 两组说明）。 */
const ASSISTANT_TURN_SUGGESTIONS = {
  change_draft: {
    expected_context_revision: 1,
    reason: '用户明确提出把 research_goal 改成合成值',
    fields: { research_goal: '仅使用合成访谈数据比较软件流程的可追踪性' },
    affected_chapter_ids: [],
  },
  affected_explanation: {
    definite_affected: [
      {
        object: { kind: 'ChapterVersion', id: 'ch-1', version: 'v1' },
        reason: '该章正文引用了被修改的研究条件',
        basis: { source_type: 'change_set', source_id: 'cs-1' },
      },
    ],
    possible_affected: [
      {
        object: { kind: 'ChapterVersion', id: 'ch-2', version: 'v1' },
        reason: '可能间接引用了该条件',
        basis: { source_type: 'object_version', source_id: 'ch-2@v1' },
      },
    ],
  },
}

/** 助手轮次的回包（`suggestions` 可覆盖，用来造不同的模型行为）。 */
function assistantTurnBody(suggestions: Record<string, unknown> | null = null): Record<string, unknown> {
  return {
    data: {
      id: 'turn-1',
      session_id: 'sess-1',
      agent: 'task_assistant',
      status: 'completed',
      failure_code: '',
      model_id: 'deepseek-flash',
      run_mode: 'real',
      input_tokens: 120,
      output_tokens: 80,
      version: 2,
      reply: '已生成研究条件的候选修改草稿，仅作建议，未写入。',
      suggestions: suggestions ?? ASSISTANT_TURN_SUGGESTIONS,
    },
  }
}

/**
 * 铺好「助手草稿路径」的全套应答。
 *
 * 路径与方法一一对应，所以轮次那一条要按方法分开登记（POST 发轮次、GET 读轮次）。
 */
function registerAssistantDraftReplies(
  fake: ReturnType<typeof createFakeClient>,
  options: {
    turnBody?: Record<string, unknown>
    turnStatus?: number
    /** 草稿上**不带**两组说明（模型这轮没给）。 */
    withoutExplanation?: boolean
    /** 草稿上带两组说明，但**两组都是空的**（模型没指出任何对象）。 */
    emptyExplanation?: boolean
  } = {},
): void {
  fake.replyFor('POST', `${ASSISTANT_PROJECT_PATH}/agents/task_assistant/sessions`, {
    status: 201,
    body: { data: { id: 'sess-1', agent: 'task_assistant', version: 1 } },
  })
  fake.replyFor('POST', `${ASSISTANT_PROJECT_PATH}/agent-sessions/sess-1/turns`, {
    status: options.turnStatus ?? 200,
    body: options.turnBody ?? assistantTurnBody(),
  })
  fake.replyFor('GET', `${ASSISTANT_PROJECT_PATH}/agent-sessions/sess-1/turns`, {
    status: 200,
    body: { data: [assistantTurnBody().data] },
  })
  fake.replyFor('POST', `${ASSISTANT_PROJECT_PATH}/action-proposals`, {
    status: 201,
    body: { data: { id: 'prop-1', status: 'pending', version: 1 } },
  })
  fake.replyFor('POST', `${ASSISTANT_PROJECT_PATH}/action-proposals/prop-1/confirm`, {
    status: 200,
    body: {
      data: {
        id: 'prop-1',
        status: 'completed',
        version: 2,
        result: { id: 'cs-1', project_id: 'p1', status: 'draft' },
      },
    },
  })
  fake.replyFor('GET', `${ASSISTANT_PROJECT_PATH}/change-sets/cs-1`, {
    status: 200,
    body: {
      data: {
        id: 'cs-1',
        project_id: 'p1',
        status: 'draft',
        fields: [],
        impacts: [],
        ...(options.withoutExplanation
          ? {}
          : options.emptyExplanation
            ? { affected_explanation: { definite_affected: [], possible_affected: [] } }
            : { affected_explanation: ASSISTANT_TURN_SUGGESTIONS.affected_explanation }),
      },
    },
  })
  fake.replyFor('POST', `${ASSISTANT_PROJECT_PATH}/change-sets/cs-1/assess`, {
    status: 200,
    body: { data: { id: 'cs-1', project_id: 'p1', status: 'assessed' } },
  })
  fake.replyFor('GET', `${ASSISTANT_PROJECT_PATH}/dependency-candidates`, {
    status: 200,
    body: { data: [] },
  })
}

/** 跑助手草稿路径的链。 */
function runDraftChain(
  fake: ReturnType<typeof createFakeClient>,
  ctx: G3RunContext = ctxOf(),
): Promise<G3AssistantChainResult> {
  return runAssistantChain(ctx, fake.client, 'jwt-1', {
    projectId: 'p1',
    draft: {
      projectId: 'p1',
      project: projectStateOf(),
      userInput: '请把 research_goal 改成「仅使用合成访谈数据比较软件流程的可追踪性」。只提出候选，不要直接应用。',
      affectedChapterIds: ['ch-1'],
      chapterId: 'ch-1',
    },
  })
}

test('助手说明按服务端 json tag 读：两组各自成列', () => {
  const explanation = readAssistantExplanation({
    definite_affected: [
      {
        object: { kind: 'ChapterVersion', id: 'ch-1', version: 'v1' },
        reason: '研究条件直接改动了该章节的输入',
        basis: { source_type: 'change_set', source_id: 'cs-1', detail: '字段变更' },
      },
    ],
    possible_affected: [
      {
        object: { kind: 'ChapterVersion', id: 'ch-2', version: 'v1' },
        reason: '可能是间接影响',
        basis: { source_type: 'dependency_edge', source_id: 'edge-1' },
      },
    ],
  })

  assert.equal(explanation !== null, true)
  assert.equal(explanation?.definiteAffected.length, 1)
  assert.equal(explanation?.possibleAffected.length, 1)
  assert.equal(explanation?.definiteAffected[0].object.id, 'ch-1')
  assert.equal(explanation?.possibleAffected[0].basis.sourceType, 'dependency_edge')
})

test('草稿上没有 affected_explanation 时读成 null（不是空两组）', () => {
  assert.equal(readAssistantExplanation(undefined), null)
  assert.equal(readAssistantExplanation({}), null)
  assert.equal(readAssistantExplanation({ definite_affected: [] }), null)
})

test('依据必须指回一条真实记录：类型在词表内且 source_id 非空', () => {
  const ok = {
    object: { kind: 'ChapterVersion', id: 'ch-1', version: 'v1' },
    reason: '因为研究条件变了',
    basis: { sourceType: 'change_set', sourceId: 'cs-1', detail: '' },
  }
  assert.equal(hasTraceableBasis(ok), true)

  const badType = { ...ok, basis: { ...ok.basis, sourceType: 'unknown_kind' } }
  assert.equal(hasTraceableBasis(badType), false)

  const noId = { ...ok, basis: { ...ok.basis, sourceId: '' } }
  assert.equal(hasTraceableBasis(noId), false)

  const noReason = { ...ok, reason: '' }
  assert.equal(hasTraceableBasis(noReason), false)

  // 词表与后端一致。
  assert.deepEqual([...G3_ASSISTANT_BASIS_TYPES], [
    'change_set',
    'dependency_edge',
    'ruleset',
    'impact_task',
    'object_version',
  ])
})

test('候选批次按服务端 json tag 读：候选与抑制分开存放', () => {
  const batch = readDependencyCandidateBatchFact({
    id: 'batch-1',
    project_id: 'p1',
    status: 'proposed',
    project_version: 3,
    context_revision: 2,
    candidates: [
      {
        id: 'cand-1',
        batch_id: 'batch-1',
        source: { kind: 'ProjectSpec', id: 'p1', version: '1' },
        target: { kind: 'ChapterVersion', id: 'ch-1', version: 'v1' },
        relation: 'spec_invalidates_method',
        action: 'INVALIDATE_CONFIRMATION',
        evidence: '研究条件变了',
        reason: '模型建议',
        confidence: 0.7,
        status: 'proposed',
        version: 1,
        project_version: 3,
      },
    ],
    suppressed: [
      {
        source: { kind: 'ProjectSpec', id: 'p1', version: '1' },
        target: { kind: 'ChapterVersion', id: 'ch-2', version: 'v1' },
        relation: 'r',
        action: 'a',
        reason: 'rejected_in_same_version',
        previous_candidate_id: 'cand-0',
        previous_status: 'rejected',
      },
    ],
  })

  assert.equal(batch.candidates.length, 1)
  assert.equal(batch.candidates[0].status, 'proposed')
  assert.equal(batch.candidates[0].confidence, 0.7)
  assert.equal(batch.suppressed.length, 1)
  assert.equal(batch.suppressed[0].reason, 'rejected_in_same_version')
})

test('AC-G3-14：草稿带两组说明且每条依据可追溯时成立', () => {
  const result = {
    draft: {
      changeSetId: 'cs-1',
      status: 'draft',
      explanationMissing: false,
      explanation: {
        definiteAffected: [
          {
            object: { kind: 'ChapterVersion', id: 'ch-1', version: 'v1' },
            reason: '直接改动',
            basis: { sourceType: 'change_set', sourceId: 'cs-1', detail: '' },
          },
        ],
        possibleAffected: [
          {
            object: { kind: 'ChapterVersion', id: 'ch-2', version: 'v1' },
            reason: '可能间接',
            basis: { sourceType: 'dependency_edge', sourceId: 'e-1', detail: '' },
          },
        ],
      },
    },
  } as unknown as G3AssistantChainResult

  const verdict = verdictAcG314(result)
  assert.equal(verdict.holds, true)
  assert.equal(verdict.id, 'AC-G3-14')
  assert.equal(verdict.status, 'PASS')
  // 结论里必须写明它与「影响确定性」不是同一回事。
  assert.equal(verdict.evidence.some((line) => line.includes('certainty')), true)
})

test('AC-G3-14：草稿缺 affected_explanation 时记「助手未给两组说明」，不崩掉也不判成立', () => {
  const result = {
    draft: { changeSetId: 'cs-1', status: 'draft', explanationMissing: true, explanation: null },
  } as unknown as G3AssistantChainResult

  const verdict = verdictAcG314(result)
  assert.equal(verdict.holds, false)
  assert.equal(verdict.status, 'NOT RUN')
  assert.equal(verdict.detail.includes('助手未给两组说明'), true)
})

test('候选批次三态：齐备 + 拒绝不产生边 + 同版本不再重提 + 版本复检拒绝时成立', () => {
  const result = {
    batchesBefore: [],
    batchesAfter: [{ id: 'batch-1', candidates: [], suppressed: [] }],
    decisions: [
      { candidateId: 'c1', decision: 'confirm', status: 'confirmed', httpStatus: 200, code: '', edgeId: 'edge-1', relation: '', action: '', supersededRelation: '', supersededAction: '' },
      { candidateId: 'c2', decision: 'modify', status: 'modified', httpStatus: 200, code: '', edgeId: 'edge-2', relation: 'r2', action: 'a2', supersededRelation: 'r1', supersededAction: 'a1' },
      { candidateId: 'c3', decision: 'reject', status: 'rejected', httpStatus: 200, code: '', edgeId: '', relation: '', action: '', supersededRelation: '', supersededAction: '' },
    ],
    resubmission: { id: 'batch-2', candidates: [], suppressed: [{ reason: 'rejected_in_same_version' }] },
    staleConfirmation: { candidateId: 'c4', decision: 'confirm', status: '', httpStatus: 409, code: 'dependency_candidate_requires_reevaluation', edgeId: '', relation: '', action: '', supersededRelation: '', supersededAction: '' },
  } as unknown as G3AssistantChainResult

  const verdict = verdictCandidateBatchSubmitted(result)
  assert.equal(verdict.holds, true)
  // 交付面编号：这条判定**不占用** AC-G3-15（那一行由依赖候选链判定）。
  assert.equal(verdict.id, 'CANDIDATE-BATCH-SUBMITTED')
  assert.equal(verdict.status, 'PASS')
})

test('候选批次三态：被拒的候选同版本重投又新建了一条就不成立', () => {
  const result = {
    batchesBefore: [],
    batchesAfter: [],
    decisions: [
      { candidateId: 'c1', decision: 'confirm', status: 'confirmed', httpStatus: 200, code: '', edgeId: 'edge-1', relation: '', action: '', supersededRelation: '', supersededAction: '' },
      { candidateId: 'c2', decision: 'modify', status: 'modified', httpStatus: 200, code: '', edgeId: 'edge-2', relation: 'r2', action: 'a2', supersededRelation: 'r1', supersededAction: 'a1' },
      { candidateId: 'c3', decision: 'reject', status: 'rejected', httpStatus: 200, code: '', edgeId: '', relation: '', action: '', supersededRelation: '', supersededAction: '' },
    ],
    resubmission: { id: 'batch-2', candidates: [{ id: 'cand-new' }], suppressed: [] },
    staleConfirmation: { candidateId: 'c4', decision: 'confirm', status: '', httpStatus: 409, code: 'dependency_candidate_requires_reevaluation', edgeId: '', relation: '', action: '', supersededRelation: '', supersededAction: '' },
  } as unknown as G3AssistantChainResult

  assert.equal(verdictCandidateBatchSubmitted(result).holds, false)
})

test('候选批次三态：依据版本已变更的候选被静默确认了就不成立', () => {
  const result = {
    batchesBefore: [],
    batchesAfter: [],
    decisions: [
      { candidateId: 'c1', decision: 'confirm', status: 'confirmed', httpStatus: 200, code: '', edgeId: 'edge-1', relation: '', action: '', supersededRelation: '', supersededAction: '' },
      { candidateId: 'c2', decision: 'modify', status: 'modified', httpStatus: 200, code: '', edgeId: 'edge-2', relation: 'r2', action: 'a2', supersededRelation: 'r1', supersededAction: 'a1' },
      { candidateId: 'c3', decision: 'reject', status: 'rejected', httpStatus: 200, code: '', edgeId: '', relation: '', action: '', supersededRelation: '', supersededAction: '' },
    ],
    resubmission: { id: 'batch-2', candidates: [], suppressed: [{ reason: 'rejected_in_same_version' }] },
    staleConfirmation: { candidateId: 'c4', decision: 'confirm', status: 'confirmed', httpStatus: 200, code: '', edgeId: 'edge-3', relation: '', action: '', supersededRelation: '', supersededAction: '' },
  } as unknown as G3AssistantChainResult

  const verdict = verdictCandidateBatchSubmitted(result)
  assert.equal(verdict.holds, false)
  assert.equal(verdict.status, 'NOT RUN')
})

test('助手链：读回草稿与候选批次，写路由带幂等键、GET 不带', async () => {
  const fake = createFakeClient()
  const projectPath = '/api/v1/lingdoc/projects/p1'

  fake.reply(`${projectPath}/change-sets/cs-1`, {
    status: 200,
    body: {
      data: {
        id: 'cs-1',
        project_id: 'p1',
        status: 'draft',
        fields: [],
        impacts: [],
        affected_explanation: {
          definite_affected: [
            {
              object: { kind: 'ChapterVersion', id: 'ch-1', version: 'v1' },
              reason: '直接改动',
              basis: { source_type: 'change_set', source_id: 'cs-1' },
            },
          ],
          possible_affected: [],
        },
      },
    },
  })
  fake.reply(`${projectPath}/dependency-candidates`, {
    status: 200,
    body: {
      data: [
        {
          id: 'batch-1',
          project_id: 'p1',
          status: 'proposed',
          project_version: 3,
          context_revision: 2,
          candidates: [],
          suppressed: [],
        },
      ],
    },
  })

  const ctx = ctxOf()
  const result = await runAssistantChain(ctx, fake.client, 'jwt-1', {
    projectId: 'p1',
    draftChangeSetId: 'cs-1',
  })

  assert.equal(result.draft?.explanationMissing, false)
  assert.equal(result.draft?.explanation?.definiteAffected.length, 1)
  assert.equal(result.batchesAfter.length, 1)
  assert.equal(result.verdicts.length, 2)
  // 助手链**不占用** AC-G3-15：那一行由依赖候选链判定（runner/dependencies.ts）。
  // 助手链只提交候选批次、不做三态裁决，同号会盖住依赖链的真实判定。
  assert.deepEqual(
    (ctx.verdicts ?? []).map((item) => item.id),
    ['AC-G3-14', 'CANDIDATE-BATCH-SUBMITTED'],
  )
  assert.equal((ctx.verdicts ?? []).some((item) => item.id === 'AC-G3-15'), false)
  for (const call of fake.calls.filter((item) => item.method === 'GET')) {
    assert.equal(call.idempotencyKey, undefined)
  }
})

// ---------------------------------------------------------------------------
// #320：验收台接上「自然语言 → 助手草稿 → 读回两组说明 → 人工确认 → 正式评估」
// ---------------------------------------------------------------------------

test('助手草稿链：自然语言 → 轮次 → 提案 → 人工确认 → 读回两组说明 → 正式评估', async () => {
  const fake = createFakeClient()
  registerAssistantDraftReplies(fake)

  const ctx = ctxOf()
  const result = await runDraftChain(fake, ctx)

  // 助手草稿是**跑出来的**，不是外部塞进来的：读回的是本轮人工确认后落库的那条草稿。
  assert.equal(result.draftRun?.draftId, 'cs-1')
  assert.equal(result.draftRun?.proposalStatus, 'completed')
  assert.equal(result.draftRun?.turn?.status, 'completed')
  assert.equal(result.draftRun?.turn?.runMode, 'real')
  assert.equal(result.draftRun?.assessmentStatus, 'assessed')
  assert.equal(result.draftRun?.unavailableReason, '')

  // 两组说明是**从草稿上读回来的**（不是模型回复里的自由文本）。
  assert.equal(result.draft?.changeSetId, 'cs-1')
  assert.equal(result.draft?.explanationMissing, false)
  assert.equal(result.draft?.explanation?.definiteAffected.length, 1)
  assert.equal(result.draft?.explanation?.possibleAffected.length, 1)

  // 矩阵 AC-G3-14 的两个步骤锚都在本轮成功执行了。
  const stepNames = ctx.steps.filter((step) => step.ok).map((step) => step.name)
  assert.equal(stepNames.includes('创建变更草稿'), true)
  assert.equal(stepNames.includes(G3_ASSISTANT_STEPS.readChangeSet), true)

  // 判定卡成立。这条路径**跑了真实模型轮次**（两组说明的内容由模型给），
  // 所以按仓库口径它不是确定性结论：状态记 WARNING，并标成依赖真实模型。
  const verdict = result.verdicts.find((item) => item.id === 'AC-G3-14')
  assert.equal(verdict?.holds, true)
  assert.equal(verdict?.status, 'WARNING')
  assert.equal(verdict?.modelDependent, true)
})

test('助手草稿链：人工确认的请求体带 turn 出处，写路由都带幂等键、GET 不带', async () => {
  const fake = createFakeClient()
  registerAssistantDraftReplies(fake)

  await runDraftChain(fake)

  const turn = fake.calls.find((item) => item.path.endsWith('/agent-sessions/sess-1/turns') && item.method === 'POST')
  assert.equal(turn !== undefined, true)
  const turnBody = turn?.body as Record<string, unknown>
  // 自然语言诉求原样送出（用户用自然语言提出修改）。
  assert.equal(typeof turnBody.user_input, 'string')
  assert.equal((turnBody.user_input as string).includes('research_goal'), true)
  // 轮次挂在**一个真实章节**上：助手才能引用得到对象（object_version 依据），
  // 两组说明才有可指回的对象可写。
  assert.equal(turnBody.chapter_id, 'ch-1')

  const proposal = fake.calls.find((item) => item.path.endsWith('/action-proposals'))
  assert.equal(proposal?.method, 'POST')
  const proposalBody = proposal?.body as Record<string, unknown>
  assert.equal(proposalBody.action, 'create_change_set')
  assert.equal(proposalBody.target_id, 'p1')
  const input = proposalBody.input as Record<string, unknown>
  // 旧值由台面按当前研究条件填（模型不编造旧值），新值取模型的候选。
  assert.deepEqual(input.fields, {
    research_goal: {
      old_value: '旧的研究目标',
      new_value: '仅使用合成访谈数据比较软件流程的可追踪性',
    },
  })
  assert.deepEqual(input.affected_chapter_ids, ['ch-1'])
  // 字段出处必须指回本轮助手轮次：服务端据此从**已落库的 turn** 重新推导两组说明。
  const metadata = (input.field_metadata as Record<string, Record<string, unknown>>).research_goal
  assert.equal(metadata.origin, 'ai_assisted_human')
  assert.equal(metadata.status, 'pending_confirmation')
  const provenance = metadata.provenance as Record<string, unknown>
  assert.equal(provenance.source_type, 'agent_turn')
  assert.equal(provenance.source_id, 'turn-1')
  assert.equal(provenance.source_version, '2')
  assert.equal(provenance.purpose, 'project_spec')
  assert.equal(provenance.locator, 'reply')

  // 人工确认走的是提案的确认端点，带上提案版本。
  const confirm = fake.calls.find((item) => item.path.includes('/action-proposals/prop-1/confirm'))
  assert.equal(confirm?.method, 'POST')
  const confirmBody = confirm?.body as Record<string, unknown>
  assert.equal(confirmBody.confirmed, true)
  assert.equal(confirmBody.expected_version, 1)
  assert.equal(typeof confirmBody.reason, 'string')

  for (const call of fake.calls.filter((item) => item.method !== 'GET')) {
    assert.equal(typeof call.idempotencyKey, 'string')
    assert.equal((call.idempotencyKey ?? '').length >= 8, true)
  }
  for (const call of fake.calls.filter((item) => item.method === 'GET')) {
    assert.equal(call.idempotencyKey, undefined)
  }
})

test('AC-G3-14：模型没给两组说明时如实记「助手未给两组说明」，不判成立也不崩掉', async () => {
  const fake = createFakeClient()
  registerAssistantDraftReplies(fake, { withoutExplanation: true })

  const result = await runDraftChain(fake)

  assert.equal(result.draftRun?.draftId, 'cs-1')
  assert.equal(result.draft?.explanationMissing, true)
  const verdict = result.verdicts.find((item) => item.id === 'AC-G3-14')
  assert.equal(verdict?.holds, false)
  assert.equal(verdict?.status, 'NOT RUN')
  assert.equal(verdict?.detail.includes('助手未给两组说明'), true)
})

test('AC-G3-14：模型不可用（轮次 503）时如实记 NOT RUN 并写明缺什么', async () => {
  const fake = createFakeClient()
  registerAssistantDraftReplies(fake, {
    turnStatus: 503,
    turnBody: { error: { code: 'dependency_unavailable', message: '所需资料、搜索或模型能力暂不可用。' } },
  })

  const result = await runDraftChain(fake)

  assert.equal(result.draftRun?.draftId, '')
  assert.equal(result.draft, null)
  assert.equal(result.draftRun?.unavailableReason.includes('503'), true)
  const verdict = result.verdicts.find((item) => item.id === 'AC-G3-14')
  assert.equal(verdict?.holds, false)
  assert.equal(verdict?.status, 'NOT RUN')
  // 缺什么要写清楚：模型不可用或缺外部凭据。
  assert.equal(verdict?.detail.includes('模型'), true)
})

test('AC-G3-14：模型这轮只回自然语言、没给变更草稿候选时如实记 NOT RUN', async () => {
  const fake = createFakeClient()
  registerAssistantDraftReplies(fake, {
    turnBody: assistantTurnBody({ reply: '请说明要改哪一个研究条件字段。' }),
  })

  const result = await runDraftChain(fake)

  assert.equal(result.draftRun?.draftId, '')
  assert.equal(result.draftRun?.unavailableReason.includes('没有给出变更草稿候选'), true)
  const verdict = result.verdicts.find((item) => item.id === 'AC-G3-14')
  assert.equal(verdict?.holds, false)
  assert.equal(verdict?.status, 'NOT RUN')
})

test('AC-G3-14：模型给了两组但**都是空的**时如实记「两组都是空的」，不判成立', async () => {
  const fake = createFakeClient()
  registerAssistantDraftReplies(fake, {
    emptyExplanation: true,
  })

  const result = await runDraftChain(fake)

  // 两组**存在**（服务端状态 present，键在），但一条都没有。
  assert.equal(result.draft?.explanationMissing, false)
  assert.equal(result.draft?.explanation?.definiteAffected.length, 0)
  assert.equal(result.draft?.explanation?.possibleAffected.length, 0)
  const verdict = result.verdicts.find((item) => item.id === 'AC-G3-14')
  assert.equal(verdict?.holds, false)
  assert.equal(verdict?.status, 'NOT RUN')
  // 「两组都是空的」与「助手未给两组说明」是**两件事**：前者是模型给了两组却没指出对象。
  assert.equal(verdict?.detail.includes('两组都是空的'), true)
  assert.equal(verdict?.detail.includes('助手未给两组说明'), false)
})

test('AC-G3-14：只读回一条已存在的草稿（本轮没跑模型轮次）时仍是确定性结论', async () => {
  const fake = createFakeClient()
  fake.reply(`${ASSISTANT_PROJECT_PATH}/change-sets/cs-1`, {
    status: 200,
    body: {
      data: {
        id: 'cs-1',
        project_id: 'p1',
        status: 'draft',
        fields: [],
        impacts: [],
        affected_explanation: ASSISTANT_TURN_SUGGESTIONS.affected_explanation,
      },
    },
  })
  fake.reply(`${ASSISTANT_PROJECT_PATH}/dependency-candidates`, { status: 200, body: { data: [] } })

  const result = await runAssistantChain(ctxOf(), fake.client, 'jwt-1', {
    projectId: 'p1',
    draftChangeSetId: 'cs-1',
  })

  const verdict = result.verdicts.find((item) => item.id === 'AC-G3-14')
  assert.equal(verdict?.holds, true)
  // 本轮没有跑模型轮次 → 不是模型依赖结论，状态照旧是 PASS。
  assert.equal(verdict?.status, 'PASS')
  assert.equal(verdict?.modelDependent, undefined)
})

test('助手草稿链：助手不触达应用 / 任务处置 / 导出端点（否定性证据）', async () => {
  const fake = createFakeClient()
  registerAssistantDraftReplies(fake)

  const result = await runDraftChain(fake)

  assert.notEqual(result.draftRun, null)
  assert.equal(result.draftRun?.draftId, 'cs-1')
  assert.deepEqual(result.draftRun?.forbiddenCalls ?? [], [])
})
