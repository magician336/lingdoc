/**
 * G3 验收台-4 · 依赖图与影响任务执行器的单测。
 *
 * 这些断言盯住本票最容易出错的几个口径：
 * - 三组呈现**完全**采用服务端返回的 `certainty`，页面不自行重算；
 * - 任务闭环集合只有 `DONE` 与 `DISMISSED`，`STALE` 是终态但不算闭环；
 * - `NO_ACTION` 是影响解析结果，不是任务处置动作；
 * - blocking 一律不可「接受本次不处理」；
 * - AC-G3-05 / AC-G3-13 的判定只从响应事实算出，不写死。
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
  type G3DependencyChainResult,
  type G3ImpactAssessmentFact,
} from './dependencies'

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

test('步骤名是矩阵规则表的锚，改名会让对应行退回「未被本轮覆盖」', () => {
  assert.equal(G3_DEP_STEPS.createEdge, '建立已确认依赖边')
  assert.equal(G3_DEP_STEPS.createSuggestedEdge, '建立建议依赖边')
  assert.equal(G3_DEP_STEPS.decideConfirm, '裁决依赖边-确认')
  assert.equal(G3_DEP_STEPS.decideModify, '裁决依赖边-修改')
  assert.equal(G3_DEP_STEPS.decideReject, '裁决依赖边-拒绝')
  assert.equal(G3_DEP_STEPS.readTasks, '读影响任务')
})
