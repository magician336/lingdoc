/**
 * G3 验收台-4 · 依赖图与影响任务执行器的单测。
 *
 * 这些断言盯住本票最容易出错的几个口径：
 * - 三组呈现**完全**采用服务端返回的 `certainty`，页面不自行重算；
 * - 任务闭环集合只有 `DONE` 与 `DISMISSED`，`STALE` 是终态但不算闭环；
 * - `NO_ACTION` 是影响解析结果，不是任务处置动作；
 * - blocking 一律不可「接受本次不处理」；
 * - AC-G3-05 / AC-G3-13 的判定只从响应事实算出，不写死；
 * - AC-G3-15 的三态与三条语义（拒绝不产生边 / 同版本不再重提 /
 *   确认时版本复检 409）缺一条就不成立——没跑成不能写成已跑通。
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  G3_DEP_STEPS,
  IMPACT_TASK_ACTION_VOCABULARY,
  canDismissTask,
  groupImpactAssessment,
  isClosedTaskStatus,
  isTerminalTaskStatus,
  verdictAcG305,
  verdictAcG313,
  verdictAcG315,
  verdictG32,
  verdictG37,
  type G3CandidateDecisionFact,
  type G3DependencyCandidateBatchFact,
  type G3DependencyCandidateFact,
  type G3DependencyChainResult,
  type G3DependencyEdgeFact,
  type G3DependencySuppressionFact,
  type G3ImpactAssessmentFact,
  type G3ImpactTaskDetailFact,
} from './dependencyChain'

function assessmentOf(): G3ImpactAssessmentFact {
  return {
    changeSetId: 'cs-1',
    projectId: 'p-1',
    contextRevision: 2,
    inputHash: 'hash',
    objects: [
      { kind: 'ProjectSpec', id: 'p-1', version: '1', state: 'CURRENT', confirmed: true, contextRevision: 2 },
      { kind: 'ChapterVersion', id: 'ch-method', version: 'v1', state: 'CURRENT', confirmed: true, contextRevision: 2 },
    ],
    impacts: [
      {
        target: { kind: 'ChapterVersion', id: 'ch-method', version: 'v1' },
        action: 'INVALIDATE_CONFIRMATION',
        certainty: 'definite',
        causes: [
          { edgeId: 'e-2', source: { kind: 'ProjectSpec', id: 'p-1', version: '1' }, reason: '研究条件变化' },
          { edgeId: 'e-3', source: { kind: 'TemplateCopy', id: 'template-demo', version: '1' }, reason: '模板依赖' },
        ],
      },
      {
        target: { kind: 'ChapterVersion', id: 'ch-question', version: 'v1' },
        action: 'REVIEW_REQUIRED',
        certainty: 'possible',
        causes: [
          { edgeId: 'e-4', source: { kind: 'ProjectSpec', id: 'p-1', version: '1' }, reason: '模型提出的候选关系' },
        ],
      },
    ],
    edges: [],
  }
}

function edgeFactOf(status: string, relation: string): G3DependencyEdgeFact {
  return {
    id: `edge-${relation}`,
    projectId: 'p-1',
    source: { kind: 'ProjectSpec', id: 'p-1', version: '1' },
    target: { kind: 'ChapterVersion', id: 'ch-question', version: 'v1' },
    relation,
    action: 'NO_ACTION',
    origin: 'ai',
    reason: '合成理由',
    status,
    confidence: 0.5,
    version: 2,
    supersededRelation: '',
    supersededAction: '',
  }
}

function candidateFactOf(
  id: string,
  status: string,
  edgeId: string,
  relation = 'candidate_a',
): G3DependencyCandidateFact {
  return {
    id,
    batchId: 'batch-1',
    source: { kind: 'ProjectSpec', id: 'p-1', version: '1' },
    target: { kind: 'ChapterVersion', id: 'ch-question', version: 'v1' },
    relation,
    action: 'NO_ACTION',
    evidence: '合成依据片段',
    reason: '合成建议理由',
    confidence: 0.5,
    status,
    decision: status === 'proposed' ? '' : status,
    supersededRelation: '',
    supersededAction: '',
    version: 1,
    projectVersion: 3,
    edgeId,
  }
}

function decisionFactOf(
  candidateId: string,
  decision: string,
  status: string,
  httpStatus: number,
  edgeId: string,
  code = '',
): G3CandidateDecisionFact {
  return {
    candidateId,
    decision,
    status,
    httpStatus,
    code,
    edgeId,
    relation: 'candidate_a',
    action: 'NO_ACTION',
    supersededRelation: '',
    supersededAction: '',
  }
}

function suppressionOf(): G3DependencySuppressionFact {
  return {
    source: { kind: 'ProjectSpec', id: 'p-1', version: '1' },
    target: { kind: 'ChapterVersion', id: 'ch-question', version: 'v1' },
    relation: 'candidate_reject',
    action: 'NO_ACTION',
    reason: 'rejected_in_same_version',
    previousCandidateId: 'cand-3',
    previousStatus: 'rejected',
  }
}

function batchOf(
  id: string,
  candidates: G3DependencyCandidateFact[],
  suppressed: G3DependencySuppressionFact[],
): G3DependencyCandidateBatchFact {
  return {
    id,
    status: 'proposed',
    projectVersion: 3,
    contextRevision: 2,
    candidates,
    suppressed,
  }
}

/**
 * 一份「三态齐备 + 三条语义都成立」的候选事实。
 *
 * 每一项都对应 AC-G3-15 判据里的一句：确认/修改/拒绝三态、
 * 只有确认的关系进入正式依赖图（拒绝不产生边）、
 * 拒绝记录不会在同一版本中反复推荐（同版本不再重提）。
 */
function candidateChainResultOf(): G3DependencyChainResult {
  return {
    decisions: {
      confirm: edgeFactOf('confirmed', 'candidate_a'),
      modify: edgeFactOf('confirmed', 'candidate_b_corrected'),
      reject: edgeFactOf('rejected', 'candidate_c'),
      reproposed: edgeFactOf('rejected', 'candidate_c'),
    },
    candidates: {
      batch: batchOf(
        'batch-1',
        [
          candidateFactOf('cand-1', 'confirmed', 'edge-1'),
          candidateFactOf('cand-2', 'modified', 'edge-2', 'candidate_b'),
          candidateFactOf('cand-3', 'rejected', '', 'candidate_reject'),
          candidateFactOf('cand-4', 'proposed', '', 'candidate_stale'),
        ],
        [],
      ),
      decisions: [
        decisionFactOf('cand-1', 'confirm', 'confirmed', 200, 'edge-1'),
        decisionFactOf('cand-2', 'modify', 'modified', 200, 'edge-2'),
        decisionFactOf('cand-3', 'reject', 'rejected', 200, ''),
      ],
      reproposal: batchOf('batch-2', [], [suppressionOf()]),
      staleConfirmation: decisionFactOf(
        'cand-4',
        'confirm',
        '',
        409,
        '',
        'dependency_candidate_stale',
      ),
      edgesBeforeDecisions: 5,
      edgesAfterDecisions: 7,
    },
  } as unknown as G3DependencyChainResult
}

test('三组呈现完全采用服务端 certainty：definite / 其余 / 既不在 objects 也不在 impacts', () => {
  const grouping = groupImpactAssessment(assessmentOf(), [
    { kind: 'ProjectSpec', id: 'p-1', version: '1' },
    { kind: 'ChapterVersion', id: 'ch-method', version: 'v1' },
    { kind: 'ChapterVersion', id: 'ch-question', version: 'v1' },
    { kind: 'RuleSet', id: 'template-demo', version: 'ruleset-hash' },
  ])

  assert.equal(grouping.definite.length, 1)
  assert.equal(grouping.definite[0].target.id, 'ch-method')
  assert.equal(grouping.possible.length, 1)
  assert.equal(grouping.possible[0].target.id, 'ch-question')
  // 必需对象集合直接来自 assessment.objects，页面不自己算。
  assert.deepEqual(grouping.requiredObjects.map((item) => item.id), ['p-1', 'ch-method'])
  // 「当前未发现影响」= 既不在 objects 也不在 impacts 里的对象。
  assert.deepEqual(grouping.unaffected.map((item) => item.id), ['template-demo'])
  assert.equal(grouping.unaffected[0].kind, 'RuleSet')
})

test('来源对象取自服务端 causes[].source，页面不自行推断', () => {
  const grouping = groupImpactAssessment(assessmentOf(), [])
  assert.deepEqual(
    grouping.definite[0].sources.map((item) => `${item.kind}/${item.id}`),
    ['ProjectSpec/p-1', 'TemplateCopy/template-demo'],
  )
})

test('任务闭环集合只有 DONE 与 DISMISSED；STALE 是终态但不算闭环', () => {
  assert.equal(isClosedTaskStatus('DONE'), true)
  assert.equal(isClosedTaskStatus('DISMISSED'), true)
  assert.equal(isClosedTaskStatus('STALE'), false)
  assert.equal(isClosedTaskStatus('OPEN'), false)
  assert.equal(isTerminalTaskStatus('STALE'), true)
  assert.equal(isTerminalTaskStatus('IN_PROGRESS'), false)
})

test('blocking 一律不可「接受本次不处理」，非 blocking 才可以', () => {
  assert.equal(canDismissTask('blocking'), false)
  assert.equal(canDismissTask('warning'), true)
  assert.equal(canDismissTask('info'), true)
})

test('任务处置动作只认词表内的五个；NO_ACTION 不是任务动作', () => {
  assert.deepEqual([...IMPACT_TASK_ACTION_VOCABULARY].sort(), ['assign', 'complete', 'dismiss', 'start', 'wait'])
  assert.equal(IMPACT_TASK_ACTION_VOCABULARY.includes('NO_ACTION' as never), false)
})

test('AC-G3-05：一逻辑任务合并两个上游原因、且不产生第二条任务时判定成立', () => {
  const result = {
    assessment: assessmentOf(),
    tasks: [
      {
        id: 'task-1',
        changeSetId: 'cs-1',
        target: { kind: 'ChapterVersion', id: 'ch-method', version: 'v1' },
        targetRevision: 2,
        action: 'INVALIDATE_CONFIRMATION',
        severity: 'blocking',
        responsibilityDomain: 'B',
        assigneeId: '',
        status: 'OPEN',
        causes: assessmentOf().impacts[0].causes,
        version: 1,
        disposition: '',
        humanDecision: '',
        closureEvidence: [],
        staleReason: '',
        staleAt: '',
      },
    ],
    taskCountAfterReplay: 1,
    tasksAfterReplay: [
      {
        id: 'task-1',
        changeSetId: 'cs-1',
        target: { kind: 'ChapterVersion', id: 'ch-method', version: 'v1' },
        targetRevision: 2,
        action: 'INVALIDATE_CONFIRMATION',
        severity: 'blocking',
        responsibilityDomain: 'B',
        assigneeId: '',
        status: 'OPEN',
        causes: assessmentOf().impacts[0].causes,
        version: 1,
        disposition: '',
        humanDecision: '',
        closureEvidence: [],
        staleReason: '',
        staleAt: '',
      },
    ],
  } as unknown as G3DependencyChainResult

  const verdict = verdictAcG305(result)
  assert.equal(verdict.holds, true)
  assert.equal(verdict.id, 'AC-G3-05')
})

test('AC-G3-13：建议边只产生 possible 影响、不产生任务、章节版本确认仍有效时判定成立', () => {
  const result = {
    assessment: assessmentOf(),
    suggestedEdge: {
      id: 'e-4',
      projectId: 'p-1',
      source: { kind: 'ProjectSpec', id: 'p-1', version: '1' },
      target: { kind: 'ChapterVersion', id: 'ch-question', version: 'v1' },
      relation: 'candidate_question_dependency',
      action: 'REVIEW_REQUIRED',
      origin: 'ai',
      reason: '模型提出的候选关系',
      status: 'suggested',
      confidence: 0.6,
      version: 1,
      supersededRelation: '',
      supersededAction: '',
    },
    tasks: [],
    suggestedTargetVersionConfirmationValid: true,
  } as unknown as G3DependencyChainResult

  const verdict = verdictAcG313(result)
  assert.equal(verdict.holds, true)
  assert.equal(verdict.id, 'AC-G3-13')
})

test('AC-G3-13：建议边产生了任务就不成立', () => {
  const result = {
    assessment: assessmentOf(),
    suggestedEdge: {
      id: 'e-4',
      projectId: 'p-1',
      source: { kind: 'ProjectSpec', id: 'p-1', version: '1' },
      target: { kind: 'ChapterVersion', id: 'ch-question', version: 'v1' },
      relation: 'candidate_question_dependency',
      action: 'REVIEW_REQUIRED',
      origin: 'ai',
      reason: '模型提出的候选关系',
      status: 'suggested',
      confidence: 0.6,
      version: 1,
      supersededRelation: '',
      supersededAction: '',
    },
    tasks: [
      {
        id: 'task-x',
        changeSetId: 'cs-1',
        target: { kind: 'ChapterVersion', id: 'ch-question', version: 'v1' },
        targetRevision: 2,
        action: 'REVIEW_REQUIRED',
        severity: 'blocking',
        responsibilityDomain: 'B',
        assigneeId: '',
        status: 'OPEN',
        causes: [],
        version: 1,
        disposition: '',
        humanDecision: '',
        closureEvidence: [],
        staleReason: '',
        staleAt: '',
      },
    ],
    suggestedTargetVersionConfirmationValid: true,
  } as unknown as G3DependencyChainResult

  assert.equal(verdictAcG313(result).holds, false)
})

test('AC-G3-05：证据指向真的产生了任务的那条影响，不是别的多原因影响', () => {
  // 同一张图里可能还有别的多原因影响（例如候选边合流出的 NO_ACTION 影响）：
  // 它们不产生任务，不能被拿来当「多上游任务合并」的证据。
  const assessment = assessmentOf()
  assessment.impacts.push({
    target: { kind: 'ChapterVersion', id: 'ch-question', version: 'v1' },
    action: 'NO_ACTION',
    certainty: 'definite',
    causes: [
      { edgeId: 'e-7', source: { kind: 'ProjectSpec', id: 'p-1', version: '1' }, reason: '候选边 A' },
      { edgeId: 'e-8', source: { kind: 'ProjectSpec', id: 'p-1', version: '1' }, reason: '候选边 B' },
    ],
  })
  const task = {
    id: 'task-1',
    changeSetId: 'cs-1',
    target: { kind: 'ChapterVersion', id: 'ch-method', version: 'v1' },
    targetRevision: 2,
    action: 'INVALIDATE_CONFIRMATION',
    severity: 'blocking',
    responsibilityDomain: 'B',
    assigneeId: '',
    status: 'OPEN',
    causes: assessment.impacts[0].causes,
    version: 1,
    disposition: '',
    humanDecision: '',
    closureEvidence: [],
    staleReason: '',
    staleAt: '',
  }
  const result = {
    assessment,
    tasks: [task],
    tasksAfterReplay: [task],
  } as unknown as G3DependencyChainResult

  const verdict = verdictAcG305(result)
  assert.equal(verdict.holds, true)
  assert.match(verdict.detail, /ch-method/)
  assert.doesNotMatch(verdict.detail, /ch-question/)
})

test('AC-G3-15：三态齐备 + 拒绝不产生边 + 同版本不再重提 + 版本复检 409 时判定成立', () => {
  const verdict = verdictAcG315(candidateChainResultOf())
  assert.equal(verdict.id, 'AC-G3-15')
  assert.equal(verdict.holds, true)
})

test('AC-G3-15：拒绝产生了依赖边就不成立（只有确认的关系进入正式依赖图）', () => {
  const result = candidateChainResultOf()
  result.candidates.decisions[2].edgeId = 'edge-3'
  assert.equal(verdictAcG315(result).holds, false)
})

test('AC-G3-15：拒绝那条多出了一条依赖边就不成立', () => {
  const result = candidateChainResultOf()
  result.candidates.edgesAfterDecisions = 8
  assert.equal(verdictAcG315(result).holds, false)
})

test('AC-G3-15：同版本重投又新建了候选就不成立（拒绝记录不在同一版本里反复推荐）', () => {
  const result = candidateChainResultOf()
  const reproposal = result.candidates.reproposal
  assert.ok(reproposal)
  reproposal.candidates = [candidateFactOf('cand-5', 'proposed', '')]
  assert.equal(verdictAcG315(result).holds, false)
})

test('AC-G3-15：同版本重投没有被抑制就不成立', () => {
  const result = candidateChainResultOf()
  const reproposal = result.candidates.reproposal
  assert.ok(reproposal)
  reproposal.suppressed = []
  assert.equal(verdictAcG315(result).holds, false)
})

test('AC-G3-15：依据版本已变更的确认没有被 409 拒掉就不成立', () => {
  const result = candidateChainResultOf()
  const stale = result.candidates.staleConfirmation
  assert.ok(stale)
  stale.httpStatus = 200
  stale.status = 'confirmed'
  assert.equal(verdictAcG315(result).holds, false)
})

test('AC-G3-15：三态缺一条就不成立', () => {
  const result = candidateChainResultOf()
  result.candidates.decisions = result.candidates.decisions.slice(0, 2)
  assert.equal(verdictAcG315(result).holds, false)
})

test('AC-G3-15：没有候选事实就不成立（没跑成不能写成已跑通）', () => {
  const result = candidateChainResultOf()
  delete (result as { candidates?: unknown }).candidates
  assert.equal(verdictAcG315(result).holds, false)
})

test('步骤名是矩阵规则表的锚，改名会让对应行退回「未被本轮覆盖」', () => {
  assert.equal(G3_DEP_STEPS.createEdge, '建立已确认依赖边')
  assert.equal(G3_DEP_STEPS.createSuggestedEdge, '建立建议依赖边')
  assert.equal(G3_DEP_STEPS.decideConfirm, '裁决依赖边-确认')
  assert.equal(G3_DEP_STEPS.decideModify, '裁决依赖边-修改')
  assert.equal(G3_DEP_STEPS.decideReject, '裁决依赖边-拒绝')
  assert.equal(G3_DEP_STEPS.readTasks, '读影响任务')
})

test('候选三态的步骤名与矩阵规则表 AC-G3-15 要求的锚一致', () => {
  // 这六个名字是 rowEvidenceRules.ts 里 AC-G3-15 那一行的 requires：
  // 名字对不上，矩阵那行会退回「未被本轮覆盖」，而不是给出假的 PASS。
  assert.equal(G3_DEP_STEPS.submitCandidates, '助手-提交候选批次')
  assert.equal(G3_DEP_STEPS.decideCandidateConfirm, '助手-候选决策-确认')
  assert.equal(G3_DEP_STEPS.decideCandidateModify, '助手-候选决策-修改')
  assert.equal(G3_DEP_STEPS.decideCandidateReject, '助手-候选决策-拒绝')
  assert.equal(G3_DEP_STEPS.reproposeRejectedCandidate, '助手-重投被拒候选')
  assert.equal(G3_DEP_STEPS.decideStaleCandidate, '助手-确认版本已变更的候选')
})

/** 造一条带指定 id 的依赖边。 */
function edgeWithId(id: string, status: string, relation: string): G3DependencyEdgeFact {
  return { ...edgeFactOf(status, relation), id }
}

/** 造一条影响任务。 */
function taskWith(
  id: string,
  targetId: string,
  action: string,
  causes: number,
  overrides: Partial<G3ImpactTaskDetailFact> = {},
): G3ImpactTaskDetailFact {
  return {
    id,
    changeSetId: 'cs-1',
    target: { kind: 'ChapterVersion', id: targetId, version: 'v1' },
    targetRevision: 2,
    action,
    severity: 'blocking',
    responsibilityDomain: 'B',
    assigneeId: 'user-1',
    status: 'OPEN',
    causes: Array.from({ length: causes }, (_, index) => ({
      edgeId: `e-${index + 2}`,
      source: { kind: 'ProjectSpec', id: 'p-1', version: '1' },
      reason: '合成理由',
    })),
    version: 1,
    disposition: '',
    humanDecision: '',
    closureEvidence: [],
    staleReason: '',
    staleAt: '',
    ...overrides,
  }
}

/**
 * 一份「三类边语义都成立」的依赖链事实。
 *
 * 对应判据：每条影响可追溯到来源边；重复边、建议边和人工确认边有明确语义。
 */
function g32ResultOf(overrides: Partial<G3DependencyChainResult> = {}): G3DependencyChainResult {
  const assessment = assessmentOf()
  const base = {
    changeSet: { id: 'cs-1' },
    edges: [],
    finalEdges: [
      edgeWithId('e-2', 'confirmed', 'spec_invalidates_method'),
      edgeWithId('e-3', 'confirmed', 'template_invalidates_method'),
      edgeWithId('e-4', 'suggested', 'candidate_question_dependency'),
    ],
    assessment,
    grouping: groupImpactAssessment(assessment, [
      { kind: 'ProjectSpec', id: 'p-1', version: '1' },
      { kind: 'ChapterVersion', id: 'ch-method', version: 'v1' },
      { kind: 'ChapterVersion', id: 'ch-question', version: 'v1' },
    ]),
    tasks: [taskWith('task-1', 'ch-method', 'INVALIDATE_CONFIRMATION', 2)],
    finalTasks: [taskWith('task-1', 'ch-method', 'INVALIDATE_CONFIRMATION', 2)],
    tasksAfterReplay: [taskWith('task-1', 'ch-method', 'INVALIDATE_CONFIRMATION', 2)],
    decisions: {
      confirm: edgeWithId('edge-confirm', 'confirmed', 'candidate_a'),
      modify: {
        ...edgeWithId('edge-modify', 'confirmed', 'candidate_b_corrected'),
        supersededRelation: 'candidate_b',
      },
      reject: edgeWithId('edge-reject', 'rejected', 'candidate_c'),
      reproposed: edgeWithId('edge-reject', 'rejected', 'candidate_c'),
    },
    suggestedEdge: edgeWithId('edge-suggested', 'suggested', 'candidate_question_dependency'),
  } as unknown as G3DependencyChainResult
  return { ...base, ...overrides } as G3DependencyChainResult
}

test('G3.2：影响可追溯到来源边、三类边语义明确时判定成立', () => {
  const verdict = verdictG32(g32ResultOf())
  assert.equal(verdict.id, 'G3.2')
  assert.equal(verdict.holds, true)
  assert.equal(verdict.status, 'PASS')
  assert.ok(verdict.evidence.length > 0)
  assert.match(verdict.evidence.join('\n'), /dependencies/)
})

test('G3.2：有影响追不到来源边就不成立（不猜）', () => {
  const broken = verdictG32(
    g32ResultOf({
      finalEdges: [edgeWithId('e-4', 'suggested', 'candidate_question_dependency')],
    } as Partial<G3DependencyChainResult>),
  )
  assert.equal(broken.holds, false)
  assert.equal(broken.status, 'NOT RUN')
  assert.match(broken.detail, /未能追溯/)
})

test('G3.2：变更集直接列入的对象按服务端给的 explicit: 来源边记，同样可追溯', () => {
  const assessment = assessmentOf()
  assessment.impacts[0].causes[0].edgeId = 'explicit:cs-1'
  const verdict = verdictG32(g32ResultOf({ assessment } as Partial<G3DependencyChainResult>))
  assert.equal(verdict.holds, true)
})

test('G3.2：建议边产生了任务就不成立（建议边不单独使章节失效）', () => {
  const tasks = [
    taskWith('task-1', 'ch-method', 'INVALIDATE_CONFIRMATION', 2),
    taskWith('task-2', 'ch-question', 'REVIEW_REQUIRED', 1),
  ]
  const broken = verdictG32(
    g32ResultOf({
      tasks,
      finalTasks: tasks,
      tasksAfterReplay: tasks,
    } as Partial<G3DependencyChainResult>),
  )
  assert.equal(broken.holds, false)
  assert.match(broken.detail, /建议边语义=false/)
})

test('G3.2：人工拒绝的边没有被记成 rejected 就不成立', () => {
  const result = g32ResultOf()
  result.decisions.reject = { ...result.decisions.reject, status: 'confirmed' }
  assert.equal(verdictG32(result).holds, false)
})

test('G3.7：必需对象集合、来源与理由、分组、责任人与动作都读得回来时判定成立', () => {
  const verdict = verdictG37(g32ResultOf())
  assert.equal(verdict.id, 'G3.7')
  assert.equal(verdict.holds, true)
  // 这一行是 #246 的缺口行：缺口清单关闭前不判 PASS。
  assert.equal(verdict.status, 'PASS (follow-up)')
  assert.match(verdict.detail, /#246/)
})

test('G3.7：必需对象集合缺字段就不成立（逐字段核对）', () => {
  const assessment = assessmentOf()
  assessment.objects[0].contextRevision = 0
  const broken = verdictG37(g32ResultOf({ assessment } as Partial<G3DependencyChainResult>))
  assert.equal(broken.holds, false)
  assert.match(broken.detail, /必需对象集合=false/)
})

test('G3.7：任务没有责任人与指派人不成立（用户看不见责任人）', () => {
  const finalTasks = [
    taskWith('task-1', 'ch-method', 'INVALIDATE_CONFIRMATION', 2, {
      responsibilityDomain: '',
      assigneeId: '',
    }),
  ]
  const broken = verdictG37(g32ResultOf({ finalTasks } as Partial<G3DependencyChainResult>))
  assert.equal(broken.holds, false)
  assert.match(broken.detail, /责任人与动作=false/)
})
