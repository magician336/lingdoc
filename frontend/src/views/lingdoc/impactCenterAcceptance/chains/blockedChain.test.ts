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
  verdictG34,
  type G3AC0907Input,
  type G3AC0909Input,
  type G3AC0912Input,
  type G3BlockedChainResult,
  type G3DismissBoundaryInput,
} from './blockedChain'

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

/** 造一份「旧任务关不掉新版本、人工复核关不掉」的阻断链事实。 */
function g34ResultOf(overrides: Partial<G3BlockedChainResult> = {}): G3BlockedChainResult {
  const base = {
    mismatchStatus: 409,
    mismatchCode: 'version_conflict',
    versionMismatch: { status: '', blockers: [], staleTaskIds: [] },
    staleTasks: [{ id: 'task-9', action: 'INVALIDATE_CONFIRMATION', severity: 'blocking', staleReason: 'closure_stale' }],
    replacedVersionId: 'v-m2',
    reviewProbe: {
      taskId: 'task-7',
      taskAction: 'REVIEW_REQUIRED',
      targetKind: 'ResearchClaim',
      completeWithoutReviewStatus: 422,
      completeWithoutReviewCode: 'invalid_state',
      completeWithReviewStatus: 200,
      completeWithReviewAccepted: true,
      modelSideCovered: false,
    },
  } as unknown as G3BlockedChainResult
  return { ...base, ...overrides } as G3BlockedChainResult
}

test('G3.4：旧任务不能关闭新版本、异步成功不等于人工关闭时判定成立', () => {
  const verdict = verdictG34(g34ResultOf())
  assert.equal(verdict.id, 'G3.4')
  assert.equal(verdict.holds, true)
  assert.equal(verdict.status, 'PASS')
  assert.match(verdict.detail, /409/)
  assert.match(verdict.detail, /invalid_state/)
  assert.ok(verdict.evidence.length > 0)
})

test('G3.4：版本被替换之后这一版修订被放行了就不成立', () => {
  const broken = verdictG34(g34ResultOf({ mismatchStatus: 200 } as Partial<G3BlockedChainResult>))
  assert.equal(broken.holds, false)
  assert.equal(broken.status, 'NOT RUN')
  assert.match(broken.detail, /旧任务不能关闭新版本=false/)
})

test('G3.4：没有跑失效任务就不成立（没观测到就不许说旧任务失效）', () => {
  const broken = verdictG34(g34ResultOf({ staleTasks: [] } as Partial<G3BlockedChainResult>))
  assert.equal(broken.holds, false)
  assert.match(broken.detail, /失效任务 0 条/)
})

test('G3.4：没有人工复核探针就不成立（异步成功不等于人工关闭没观测到）', () => {
  const broken = verdictG34(g34ResultOf({ reviewProbe: null } as Partial<G3BlockedChainResult>))
  assert.equal(broken.holds, false)
  assert.match(broken.detail, /本轮没有跑人工复核探针/)
})

/**
 * 「服务端按契约拒了这个请求」只认 **4xx**。
 *
 * 5xx 是服务端**自己**崩了：把它算成「被拒」，等于让判定卡在服务端故障时
 * 反而判成立——那是拿错误冒充拒绝。当前落证里的真实码是 422（4xx），
 * 所以收紧后本轮已有落证的结论不变。
 */
test('5xx 不算「服务端按契约拒掉」：G3.4 / AC-G3-07 / 接受不处理边界', () => {
  // AC-G3-07：缺人工复核决策时服务端 500（自身错误）⇒ 不算被拒 ⇒ 判定不成立。
  const ac07 = verdictAC0907({
    taskAction: 'REVIEW_REQUIRED',
    targetKind: 'ResearchClaim',
    completeWithoutReviewStatus: 500,
    completeWithoutReviewCode: 'internal_error',
    completeWithReviewStatus: 200,
    completeWithReviewAccepted: true,
    modelSideCovered: false,
  })
  assert.equal(ac07.holds, false, '5xx 被当成了「按契约拒掉」')
  // `blocked.ts` 的判定卡**大部分不带 `status`**：`AC-G3-07`（`verdictAC0907`）
  // 这张卡不写 `status`，所以「不成立」时投影保持该行的原状态，这里只能断言 holds。
  // （同一文件里**带** `status` 的是 `G3.4`：`holds ? 'PASS' : 'NOT RUN'`。）
  assert.equal(ac07.status, undefined)

  // 4xx 仍然是「被拒」：本轮落证里的真实码 422 判定成立。
  const ac07Rejected = verdictAC0907({
    taskAction: 'REVIEW_REQUIRED',
    targetKind: 'ResearchClaim',
    completeWithoutReviewStatus: 422,
    completeWithoutReviewCode: 'invalid_state',
    completeWithReviewStatus: 200,
    completeWithReviewAccepted: true,
    modelSideCovered: false,
  })
  assert.equal(ac07Rejected.holds, true)

  // 接受不处理边界：阻断级 dismiss 返回 500 ⇒ 不算被拒 ⇒ 判定不成立。
  const boundary = verdictDismissBoundary({
    blockingDismissStatus: 500,
    blockingDismissCode: 'internal_error',
    nonBlockingDismissStatus: 200,
    nonBlockingDismissFinalStatus: 'DISMISSED',
  })
  assert.equal(boundary.holds, false, '5xx 被当成了「按契约拒掉」')

  // G3.4：缺人工复核决策那一步返回 5xx ⇒ 判定不成立。
  const g34 = verdictG34(
    g34ResultOf({
      reviewProbe: {
        taskId: 'task-7',
        taskAction: 'REVIEW_REQUIRED',
        targetKind: 'ResearchClaim',
        completeWithoutReviewStatus: 500,
        completeWithoutReviewCode: 'internal_error',
        completeWithReviewStatus: 200,
        completeWithReviewAccepted: true,
        modelSideCovered: false,
      },
    } as unknown as Partial<G3BlockedChainResult>),
  )
  assert.equal(g34.holds, false, '5xx 被当成了「按契约拒掉」')
  assert.equal(g34.status, 'NOT RUN')
})
