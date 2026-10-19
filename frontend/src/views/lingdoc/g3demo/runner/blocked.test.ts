/**
 * G3 验收台-6 · 阻断与恢复路径执行器的单测。
 *
 * 这些断言盯住本票最容易走样的几个口径：
 * - **稳定化是判定，不是错误**：阻断态是 200 成功响应，阻断原因逐条原样保留，
 *   页面必须展示完整阻断原因，而不是折叠成一句失败；
 * - **规则问题与影响任务分离**：人工处置规则问题不等于闭环影响任务，
 *   项目仍然不能稳定；
 * - **失效任务**：既不算闭环，也不阻断稳定化，只被单独上报；
 * - **接受不处理只对非阻断影响可达**：阻断级影响上要给出说明，而不是隐藏入口。
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  dismissAvailability,
  readCheckWithIssues,
  readIssueDispositionFact,
  readRuleIssueFact,
  staleTaskSemantics,
  verdictAC0907,
  verdictAC0909,
  verdictAC0912,
  verdictDismissBoundary,
  type G3AC0907Input,
  type G3AC0909Input,
  type G3AC0912Input,
  type G3DismissBoundaryInput,
} from './blocked'

test('规则问题原样读取：编号、严重度、目标与人工处置都不折叠', () => {
  const issue = readRuleIssueFact({
    id: 'vi-1',
    rule_id: 'chapter-confirmed',
    severity: 'blocking',
    target_id: 'ch-method',
    target_version: 'v2',
    message: '确认记录与冻结章节、研究条件及模板不一致',
    disposition: { action: 'resolve', reason: '已核对', actor_id: 'owner-1', created_at: '2026-10-20T00:00:00Z' },
  })

  assert.equal(issue.id, 'vi-1')
  assert.equal(issue.ruleId, 'chapter-confirmed')
  assert.equal(issue.severity, 'blocking')
  assert.equal(issue.targetId, 'ch-method')
  assert.equal(issue.targetVersion, 'v2')
  assert.equal(issue.dispositionAction, 'resolve')
  assert.equal(issue.dispositionReason, '已核对')
  assert.equal(issue.dispositionActorId, 'owner-1')
})

test('没有人工处置的规则问题，处置字段留空而不是编一个', () => {
  const issue = readRuleIssueFact({ id: 'vi-2', rule_id: 'chapter-nonempty', severity: 'blocking' })
  assert.equal(issue.dispositionAction, '')
  assert.equal(issue.dispositionReason, '')
  assert.equal(issue.dispositionActorId, '')
})

test('交付检查结果保留全部规则问题与结论', () => {
  const check = readCheckWithIssues({
    receipt_id: 'receipt-1',
    project_version: 8,
    status: 'blocked',
    issues: [
      { id: 'vi-1', rule_id: 'chapter-confirmed', severity: 'blocking', target_id: 'ch-method' },
      { id: 'vi-2', rule_id: 'chapter-confirmed', severity: 'blocking', target_id: 'ch-question' },
    ],
  })

  assert.equal(check.receiptId, 'receipt-1')
  assert.equal(check.status, 'blocked')
  assert.equal(check.issues.length, 2)
  assert.equal(check.issues[1].targetId, 'ch-question')
})

test('人工处置回执原样读取', () => {
  const disposition = readIssueDispositionFact({
    issue_id: 'vi-1',
    rule_id: 'chapter-confirmed',
    severity: 'blocking',
    target_id: 'ch-method',
    project_version: 8,
    action: 'resolve',
    reason: '本次不处理',
    actor_id: 'owner-1',
    created_at: '2026-10-20T00:00:00Z',
  })

  assert.equal(disposition.issueId, 'vi-1')
  assert.equal(disposition.action, 'resolve')
  assert.equal(disposition.actorId, 'owner-1')
  assert.equal(disposition.projectVersion, 8)
})

test('接受不处理只对非阻断影响可达：阻断级给说明而不是隐藏入口', () => {
  const blocking = dismissAvailability('blocking')
  assert.equal(blocking.allowed, false)
  assert.match(blocking.explanation, /闭环证据/)

  const warning = dismissAvailability('warning')
  assert.equal(warning.allowed, true)
  assert.equal(warning.explanation, '')

  const info = dismissAvailability('info')
  assert.equal(info.allowed, true)
})

test('失效任务：既不算闭环，也不阻断稳定化，只被单独上报', () => {
  const stale = staleTaskSemantics('STALE')
  assert.equal(stale.closed, false)
  assert.equal(stale.blocking, false)
  assert.equal(stale.reported, true)

  const done = staleTaskSemantics('DONE')
  assert.equal(done.closed, true)
  assert.equal(done.blocking, false)

  const dismissed = staleTaskSemantics('DISMISSED')
  assert.equal(dismissed.closed, true)
  assert.equal(dismissed.blocking, false)

  const open = staleTaskSemantics('OPEN')
  assert.equal(open.closed, false)
  assert.equal(open.blocking, true)
})

test('AC-G3-09：处置规则问题不闭环影响任务、项目仍不能稳定这半段判定成立', () => {
  const input: G3AC0909Input = {
    issueId: 'vi-1',
    dismissStatus: 500,
    dismissCode: 'internal_error',
    resolveStatus: 201,
    dispositionAction: 'resolve',
    checkStatusBefore: 'blocked',
    checkStatusAfter: 'blocked',
    issueCarriesDisposition: true,
    impactTaskStatus: 'OPEN',
    stabilizationStatus: 'BLOCKED',
    stabilizationBlockerCodes: ['impact_task_open'],
  }

  const verdict = verdictAC0909(input)
  // 实跑得出来的那半段成立：「影响任务仍为 OPEN、项目仍不能稳定」。
  assert.equal(verdict.holds, true)
  assert.match(verdict.detail, /仍不能稳定/)
  // 未成立那半段必须写在判定里，不能藏起来。
  assert.match(verdict.detail, /未成立那半段/)
  assert.match(verdict.detail, /dismiss/)
})

test('AC-G3-09：影响任务已闭环时判定不成立', () => {
  const verdict = verdictAC0909({
    issueId: 'vi-1',
    dismissStatus: 201,
    dismissCode: '',
    resolveStatus: 201,
    dispositionAction: 'resolve',
    checkStatusBefore: 'blocked',
    checkStatusAfter: 'passed',
    issueCarriesDisposition: true,
    impactTaskStatus: 'DONE',
    stabilizationStatus: 'STABILIZED',
    stabilizationBlockerCodes: [],
  })
  assert.equal(verdict.holds, false)
})

test('AC-G3-12：稳定化重新读取发现版本不匹配时保持阻断并重投任务，判定成立', () => {
  const input: G3AC0912Input = {
    mismatchHttpStatus: 409,
    mismatchCode: 'version_conflict',
    stabilizeStatus: '',
    stabilizeBlockerCodes: [],
    staleTaskIds: [],
    reissuedTaskIds: ['task-new-1'],
    reissuedTaskStatuses: ['OPEN'],
    finalStabilizeStatus: 'STABILIZED',
    finalBlockerCount: 0,
  }

  const verdict = verdictAC0912(input)
  assert.equal(verdict.holds, true)
  assert.match(verdict.detail, /409/)
})

test('AC-G3-12：没有重投任务时判定不成立', () => {
  const verdict = verdictAC0912({
    mismatchHttpStatus: 409,
    mismatchCode: 'version_conflict',
    stabilizeStatus: '',
    stabilizeBlockerCodes: [],
    staleTaskIds: [],
    reissuedTaskIds: [],
    reissuedTaskStatuses: [],
    finalStabilizeStatus: 'STABILIZED',
    finalBlockerCount: 0,
  })
  assert.equal(verdict.holds, false)
})

test('AC-G3-07：没有人工复核决策就不许关闭复核任务，判定成立', () => {
  const input: G3AC0907Input = {
    taskAction: 'REVIEW_REQUIRED',
    targetKind: 'ResearchClaim',
    completeWithoutReviewStatus: 422,
    completeWithoutReviewCode: 'invalid_state',
    completeWithReviewStatus: 200,
    completeWithReviewAccepted: true,
    modelSideCovered: false,
  }

  const verdict = verdictAC0907(input)
  // 实跑得出来的那半段成立：人工复核任务关不掉；模型那半段未覆盖，必须写清。
  assert.equal(verdict.holds, true)
  assert.match(verdict.detail, /人工复核/)
  assert.match(verdict.detail, /#243/)
})

test('接受不处理的边界：阻断级被拒、非阻断级可达时判定成立', () => {
  const input: G3DismissBoundaryInput = {
    blockingDismissStatus: 422,
    blockingDismissCode: 'invalid_state',
    nonBlockingDismissStatus: 200,
    nonBlockingDismissFinalStatus: 'DISMISSED',
  }

  const verdict = verdictDismissBoundary(input)
  assert.equal(verdict.holds, true)
  assert.match(verdict.detail, /DISMISSED/)
})
