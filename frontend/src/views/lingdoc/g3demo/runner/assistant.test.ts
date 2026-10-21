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
  hasTraceableBasis,
  readAssistantExplanation,
  readDependencyCandidateBatchFact,
  runAssistantChain,
  verdictAcG314,
  verdictAcG315,
  type G3AssistantChainResult,
} from './assistant'
import { createFakeClient } from './fakeHttp'
import { createRunContext, type G3RunContext } from './runContext'

function ctxOf(): G3RunContext {
  return createRunContext({ runId: 'g3r-as-1', accountEmail: 'g3demo@example.com' })
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

test('AC-G3-15：三态齐备 + 拒绝不产生边 + 同版本不再重提 + 版本复检拒绝时成立', () => {
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

  const verdict = verdictAcG315(result)
  assert.equal(verdict.holds, true)
  assert.equal(verdict.id, 'AC-G3-15')
  assert.equal(verdict.status, 'PASS')
})

test('AC-G3-15：被拒的候选同版本重投又新建了一条就不成立', () => {
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

  assert.equal(verdictAcG315(result).holds, false)
})

test('AC-G3-15：依据版本已变更的候选被静默确认了就不成立', () => {
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

  const verdict = verdictAcG315(result)
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
  assert.deepEqual((ctx.verdicts ?? []).map((item) => item.id), ['AC-G3-14', 'AC-G3-15'])
  for (const call of fake.calls.filter((item) => item.method === 'GET')) {
    assert.equal(call.idempotencyKey, undefined)
  }
})
