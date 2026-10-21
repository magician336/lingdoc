/**
 * G3 验收台-5 · 稳定化与交付闭环执行器的单测。
 *
 * 这些断言盯住本票最容易走样的几个口径：
 * - **稳定化是判定，不是错误**：阻断态是 200 成功响应，blockers 原样保留，
 *   页面必须展示完整阻断原因，而不是折叠成一句失败；
 * - **判定导出成功只看产物最终状态与文件哈希**，不看受理接口的 202；
 * - 下载按**字节与哈希**核对，页面展示大小与哈希；
 * - 闭环证据按动作类型不同：`RECOMPUTE` 要交付检查回执，
 *   `RETRIEVE_CANDIDATES` 要真实调研回执；
 * - 导出正文标题必须 2–4 级，页面在导出前给出可操作提示。
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  DOCX_BODY_HEADING_HINT,
  blockerLines,
  closureEvidenceKindForAction,
  deliveryBodyTitleCheck,
  exportSucceeded,
  isStabilizedFact,
  readCheckFact,
  readExportArtifactFact,
  readReleaseSnapshotFact,
  readStabilizationFact,
  runDeliveryClosure,
  runStabilizationPhase,
  verdictDeliveryClosure,
  verdictG31,
  verdictG36,
  verifyDownloadedArtifact,
  type G3StabilizationChainResult,
} from './stabilization'
import { createFakeClient } from './fakeHttp'
import { createRunContext } from './runContext'

const PROJECT_PATH = '/api/v1/lingdoc/projects/p-1'

const PROJECT_BODY = {
  data: {
    id: 'p-1',
    status: 'active',
    project_version: 3,
    spec_revision: 2,
    current_context_revision: 2,
    spec: { research_subject: '对象', research_goal: '目标' },
    template_id: 'template-demo',
    template_copy: { ruleset_hash: 'ruleset-hash' },
  },
}

test('闭环证据按动作类型不同：重算要检查回执，重新调研要调研回执', () => {
  assert.equal(closureEvidenceKindForAction('RECOMPUTE'), 'ComputationReceipt')
  assert.equal(closureEvidenceKindForAction('RETRIEVE_CANDIDATES'), 'ResearchRun')
  assert.equal(closureEvidenceKindForAction('INVALIDATE_CONFIRMATION'), 'ChapterVersion')
  assert.equal(closureEvidenceKindForAction('REVIEW_REQUIRED'), 'ChapterVersion')
  assert.equal(closureEvidenceKindForAction('REGENERATE'), 'ChapterVersion')
})

test('阻断态是成功响应：blockers 原样保留，不折叠成一句失败', () => {
  const fact = readStabilizationFact({
    change_set_id: 'cs-1',
    context_revision: 3,
    status: 'BLOCKED',
    blockers: [
      { code: 'impact_task_open', target_id: 'task-1', reason: 'REVIEW_REQUIRED' },
      { code: 'impact_object_unconfirmed', target_id: 'ch-method', reason: '受影响章节尚未重新确认' },
    ],
    stale_task_ids: ['task-9'],
    checked_at: '2026-10-20T00:00:00Z',
  })

  assert.equal(fact.status, 'BLOCKED')
  assert.equal(isStabilizedFact(fact), false)
  assert.equal(fact.blockers.length, 2)
  assert.equal(fact.blockers[0].code, 'impact_task_open')
  assert.equal(fact.blockers[0].targetId, 'task-1')
  assert.equal(fact.blockers[0].reason, 'REVIEW_REQUIRED')
  assert.deepEqual(fact.staleTaskIds, ['task-9'])

  // 完整阻断原因：码、对象、原因三样都在。
  const lines = blockerLines(fact)
  assert.equal(lines.length, 2)
  assert.match(lines[0], /impact_task_open/)
  assert.match(lines[0], /task-1/)
  assert.match(lines[0], /REVIEW_REQUIRED/)
  assert.match(lines[1], /ch-method/)
  assert.match(lines[1], /尚未重新确认/)
})

test('判定导出成功只看产物最终状态与文件哈希，不看受理状态', () => {
  const verified = readExportArtifactFact({
    id: 'exp-1',
    project_id: 'p-1',
    snapshot_id: 'snap-1',
    status: 'verified',
    file_sha256: 'a'.repeat(64),
    download_path: '/api/v1/lingdoc/projects/p-1/exports/exp-1/file',
  })
  assert.equal(verified.status, 'verified')
  assert.equal(verified.fileSha256.length, 64)
  assert.equal(verified.downloadPath, '/api/v1/lingdoc/projects/p-1/exports/exp-1/file')
  assert.equal(exportSucceeded(verified), true)

  // 渲染失败落成 failed：不是成功，哪怕有哈希。
  const failed = readExportArtifactFact({
    id: 'exp-2',
    status: 'failed',
    file_sha256: '',
    error: { code: 'unsupported_format', message: '正文包含当前导出格式不支持的 Markdown 结构' },
  })
  assert.equal(exportSucceeded(failed), false)
  assert.equal(failed.errorCode, 'unsupported_format')
  assert.match(failed.errorMessage, /不支持的 Markdown 结构/)

  // 没有哈希的 verified 也不算成功——判定依据是「状态 + 哈希」两样。
  assert.equal(exportSucceeded(readExportArtifactFact({ id: 'exp-3', status: 'verified' })), false)
})

test('下载按字节与哈希核对', async () => {
  const bytes = new Uint8Array([1, 2, 3, 4])
  const digest = await verifyDownloadedArtifact(bytes, '')
  assert.equal(digest.size, 4)
  assert.equal(digest.sha256.length, 64)
  // 传入空期望值时只如实报出实测值，不谎称一致。
  assert.equal(digest.matches, false)

  const again = await verifyDownloadedArtifact(bytes, digest.sha256)
  assert.equal(again.matches, true)
  assert.equal(again.size, 4)

  const wrong = await verifyDownloadedArtifact(bytes, 'b'.repeat(64))
  assert.equal(wrong.matches, false)
})

test('导出前提示：正文标题必须 2–4 级', () => {
  assert.match(DOCX_BODY_HEADING_HINT, /2–4 级/)
  const ok = deliveryBodyTitleCheck('## 研究方案\n\n正文一段。')
  assert.equal(ok.ok, true)
  assert.deepEqual(ok.offending, [])

  const bad = deliveryBodyTitleCheck('# 一级标题\n\n正文。')
  assert.equal(bad.ok, false)
  assert.equal(bad.offending.length, 1)
  assert.match(bad.offending[0], /# 一级标题/)

  const tooDeep = deliveryBodyTitleCheck('##### 五级标题')
  assert.equal(tooDeep.ok, false)
})

test('交付检查与冻结快照的事实读法', () => {
  const check = readCheckFact({
    receipt_id: 'rcpt-1',
    project_version: 3,
    ruleset_hash: 'hash',
    status: 'passed',
    issues: [{ rule_id: 'chapter-confirmed', severity: 'blocking', target_id: 'ch-method', message: '未确认' }],
  })
  assert.equal(check.receiptId, 'rcpt-1')
  assert.equal(check.status, 'passed')
  assert.equal(check.issues.length, 1)
  assert.equal(check.issues[0].ruleId, 'chapter-confirmed')

  const snapshot = readReleaseSnapshotFact({
    id: 'snap-1',
    project_id: 'p-1',
    snapshot_digest: 'digest',
    check: { status: 'passed' },
    is_current: true,
    created_at: '2026-10-20T00:00:00Z',
  })
  assert.equal(snapshot.id, 'snap-1')
  assert.equal(snapshot.checkStatus, 'passed')
  assert.equal(snapshot.isCurrent, true)
})

function chainResultOf(overrides: Partial<G3StabilizationChainResult> = {}): G3StabilizationChainResult {
  const base = {
    changeSet: { id: 'cs-1', status: 'applied' },
    concurrentChangeSet: { id: 'cs-2', status: 'stale' },
    staleApply: { status: 409, code: 'version_conflict' },
    contextRevisionsBefore: 2,
    contextRevisionsAfter: 2,
    tasksBeforeStaleApply: 1,
    tasksAfterStaleApply: 1,
    releaseList: { count: 1, truncated: false },
    blocked: readStabilizationFact({
      change_set_id: 'cs-1',
      status: 'BLOCKED',
      blockers: [{ code: 'impact_task_open', target_id: 'task-1', reason: 'REVIEW_REQUIRED' }],
      stale_task_ids: [],
      checked_at: '2026-10-20T00:00:00Z',
    }),
    freezeBeforeStable: { status: 422, code: 'context_unstable' },
    check: readCheckFact({ receipt_id: 'rcpt-1', status: 'passed', issues: [] }),
    receipt: { id: 'rcpt-1', version: 1, status: 'passed', contextRevision: 3, projectVersion: 3 },
    stabilized: readStabilizationFact({
      change_set_id: 'cs-1',
      status: 'STABILIZED',
      blockers: [],
      stale_task_ids: [],
      checked_at: '2026-10-20T00:01:00Z',
    }),
    snapshot: readReleaseSnapshotFact({ id: 'snap-1', check: { status: 'passed' }, is_current: true }),
    exportAccepted: { status: 202 },
    exportArtifact: readExportArtifactFact({
      id: 'exp-1',
      status: 'verified',
      file_sha256: 'a'.repeat(64),
      download_path: '/api/v1/lingdoc/projects/p-1/exports/exp-1/file',
    }),
    download: { size: 1234, sha256: 'a'.repeat(64), expectedSha256: 'a'.repeat(64), matches: true },
    pollStatuses: ['verified'],
    templateCheck: { status: 'passed', evaluationCount: 5, issueCount: 0 },
    transitions: [],
    verdicts: [],
  } as unknown as G3StabilizationChainResult
  return { ...base, ...overrides } as G3StabilizationChainResult
}

test('G3.1 判定：旧基线不能应用，且被拒的应用没有半完成写入', () => {
  const verdict = verdictG31(chainResultOf())
  assert.equal(verdict.id, 'G3.1')
  assert.equal(verdict.holds, true)
  assert.ok(verdict.evidence.length > 0)
  assert.match(verdict.evidence.join('\n'), /409/)

  const broken = verdictG31(
    chainResultOf({
      staleApply: { status: 200, code: '' },
      tasksAfterStaleApply: 2,
    } as Partial<G3StabilizationChainResult>),
  )
  assert.equal(broken.holds, false)
})

test('G3.6 判定：未关闭阻断项时不能 STABILIZED，也不能冻结导出', () => {
  const verdict = verdictG36(chainResultOf())
  assert.equal(verdict.id, 'G3.6')
  assert.equal(verdict.holds, true)

  const notBlocked = verdictG36(
    chainResultOf({
      blocked: readStabilizationFact({ change_set_id: 'cs-1', status: 'STABILIZED', blockers: [] }),
    } as Partial<G3StabilizationChainResult>),
  )
  assert.equal(notBlocked.holds, false)

  const freezeAllowed = verdictG36(
    chainResultOf({ freezeBeforeStable: { status: 201, code: '' } } as Partial<G3StabilizationChainResult>),
  )
  assert.equal(freezeAllowed.holds, false)
})

test('G3.6：冻结被拒只认 4xx（5xx 是服务端自身错误，不算「按契约拒掉」）', () => {
  // 4xx：服务端按契约拒掉（当前落证里的真实码是 422）⇒ 判定成立。
  assert.equal(verdictG36(chainResultOf()).holds, true)
  // 5xx：服务端自己崩了，不能拿它当「不能导出」的证据。
  const serverError = verdictG36(
    chainResultOf({ freezeBeforeStable: { status: 500, code: 'internal_error' } } as Partial<G3StabilizationChainResult>),
  )
  assert.equal(serverError.holds, false, '5xx 被当成了「按契约拒掉」')
})

test('交付闭环判定：产物 verified + 哈希一致才算导出成功', () => {
  const verdict = verdictDeliveryClosure(chainResultOf())
  assert.equal(verdict.holds, true)

  const mismatched = verdictDeliveryClosure(
    chainResultOf({
      download: { size: 1, sha256: 'a'.repeat(64), expectedSha256: 'b'.repeat(64), matches: false },
    } as Partial<G3StabilizationChainResult>),
  )
  assert.equal(mismatched.holds, false)
})

/** 造一份「稳定化阶段」用得上的假后端。 */
function phaseFake(): ReturnType<typeof createFakeClient> {
  const fake = createFakeClient()

  fake.reply(PROJECT_PATH, { status: 200, body: PROJECT_BODY })
  fake.replyFor('POST', `${PROJECT_PATH}/chapters/ch-q/versions`, {
    status: 201,
    body: { data: { id: 'ch-q', current_version_id: 'v-q1' } },
  })
  fake.replyFor('POST', `${PROJECT_PATH}/chapters/ch-m/versions`, {
    status: 201,
    body: { data: { id: 'ch-m', current_version_id: 'v-m1' } },
  })
  fake.reply(`${PROJECT_PATH}/chapters/ch-q/confirmations`, {
    status: 201,
    body: { data: { id: 'cf-q', chapter_version_id: 'v-q1', spec_revision: 2 } },
  })
  fake.reply(`${PROJECT_PATH}/chapters/ch-m/confirmations`, {
    status: 201,
    body: { data: { id: 'cf-m', chapter_version_id: 'v-m1', spec_revision: 2 } },
  })
  fake.reply(`${PROJECT_PATH}/dependencies`, {
    status: 201,
    body: { data: { id: 'edge-1', status: 'confirmed', version: 1 } },
  })
  // 两个变更集：同一个路径，所以按次序给两个应答。
  fake.replySequence(`${PROJECT_PATH}/change-sets`, [
    {
      status: 201,
      body: { data: { id: 'cs-1', status: 'assessed', base_context_revision: 2, base_spec_revision: 2 } },
    },
    {
      status: 201,
      body: { data: { id: 'cs-2', status: 'assessed', base_context_revision: 2, base_spec_revision: 2 } },
    },
  ])
  fake.reply(`${PROJECT_PATH}/change-sets/cs-1/assess`, {
    status: 200,
    body: { data: { id: 'cs-1', status: 'assessed' } },
  })
  fake.reply(`${PROJECT_PATH}/change-sets/cs-1/impact-assessment`, {
    status: 200,
    body: {
      data: {
        change_set_id: 'cs-1',
        project_id: 'p-1',
        context_revision: 2,
        objects: [
          { kind: 'ChapterVersion', id: 'ch-m', version: 'v-m1', state: 'CURRENT', confirmed: true },
        ],
        impacts: [
          {
            target: { kind: 'ChapterVersion', id: 'ch-m', version: 'v-m1' },
            action: 'INVALIDATE_CONFIRMATION',
            certainty: 'definite',
            causes: [
              {
                edge_id: 'edge-1',
                source: { kind: 'ProjectSpec', id: 'p-1', version: '2' },
                reason: '研究条件变化',
              },
            ],
          },
        ],
        edges: [],
      },
    },
  })
  fake.reply(`${PROJECT_PATH}/change-sets/cs-1/apply`, {
    status: 200,
    body: {
      data: {
        id: 'cs-1',
        status: 'applied',
        base_context_revision: 2,
        target_context_revision: 3,
        base_spec_revision: 2,
        target_spec_revision: 3,
      },
    },
  })
  fake.reply(`${PROJECT_PATH}/change-sets/cs-2/apply`, {
    status: 409,
    body: { error: { code: 'version_conflict', message: '内容已变化，请先读取当前版本。' } },
  })
  fake.reply(`${PROJECT_PATH}/change-sets/cs-2`, {
    status: 200,
    body: { data: { id: 'cs-2', status: 'stale', stale_reason: 'spec_revision_changed' } },
  })
  fake.reply(`${PROJECT_PATH}/context-revisions`, {
    status: 200,
    body: { data: [{ revision: 1 }, { revision: 2 }] },
  })
  fake.reply(`${PROJECT_PATH}/impact-tasks`, {
    status: 200,
    body: {
      data: [
        {
          id: 'task-1',
          change_set_id: 'cs-1',
          target: { kind: 'ChapterVersion', id: 'ch-m', version: 'v-m1' },
          target_revision: 3,
          action: 'INVALIDATE_CONFIRMATION',
          severity: 'blocking',
          status: 'OPEN',
          causes: [],
          version: 1,
        },
      ],
    },
  })
  fake.replySequence(`${PROJECT_PATH}/change-sets/cs-1/stabilize`, [
    {
      status: 200,
      body: {
        data: {
          change_set_id: 'cs-1',
          context_revision: 3,
          status: 'BLOCKED',
          blockers: [
            { code: 'impact_task_open', target_id: 'task-1', reason: 'INVALIDATE_CONFIRMATION' },
          ],
          stale_task_ids: [],
          checked_at: '2026-10-20T00:00:00Z',
        },
      },
    },
    {
      status: 200,
      body: {
        data: {
          change_set_id: 'cs-1',
          context_revision: 3,
          status: 'STABILIZED',
          blockers: [],
          stale_task_ids: [],
          checked_at: '2026-10-20T00:01:00Z',
        },
      },
    },
  ])
  // 稳定化之前冻结：被服务端拒掉。
  fake.reply(`${PROJECT_PATH}/releases`, {
    status: 422,
    body: { error: { code: 'context_unstable', message: '正式变更尚未完成稳定化。' } },
  })
  fake.reply(`${PROJECT_PATH}/impact-tasks/task-1/actions`, {
    status: 200,
    body: {
      data: {
        id: 'task-1',
        change_set_id: 'cs-1',
        target: { kind: 'ChapterVersion', id: 'ch-m', version: 'v-m1' },
        target_revision: 3,
        action: 'INVALIDATE_CONFIRMATION',
        severity: 'blocking',
        status: 'DONE',
        causes: [],
        version: 4,
      },
    },
  })
  return fake
}

test('稳定化阶段：阻断态是 200 成功响应，闭环之后才转为已稳定', async () => {
  const fake = phaseFake()
  const ctx = createRunContext({ runId: 'g3r-test-0001', accountEmail: 'demo@example.com' })
  ctx.head = {
    commitId: 'test-commit',
    probedAt: '2026-10-20T00:00:00Z',
    probePath: '/api/v1/version',
    probeStatus: 401,
  }

  const result = await runStabilizationPhase(ctx, fake.client, 'token-1', {
    projectId: 'p-1',
    chapters: [
      { id: 'ch-q', sectionId: 'question', title: '研究问题' },
      { id: 'ch-m', sectionId: 'method', title: '研究方案' },
    ],
    operatorId: 'user-1',
  })

  // 阻断态：不是错误，阻断原因完整。
  assert.equal(result.blocked.status, 'BLOCKED')
  assert.equal(result.blocked.blockers.length, 1)
  assert.equal(result.blocked.blockers[0].code, 'impact_task_open')
  // 旧基线不能应用，且没有半完成写入。
  assert.equal(result.staleApply.status, 409)
  assert.equal(result.concurrentChangeSet.status, 'stale')
  assert.equal(result.contextRevisionsAfter, result.contextRevisionsBefore)
  assert.equal(result.tasksAfterStaleApply, result.tasksBeforeStaleApply)
  // 稳定化之前冻结被拒。
  assert.equal(result.freezeBeforeStable.status, 422)
  assert.equal(result.freezeBeforeStable.code, 'context_unstable')
  // 闭环之后已稳定。
  assert.equal(result.stabilized.status, 'STABILIZED')
  assert.ok(result.stabilized.checkedAt)

  const stabilizeCalls = fake.calls.filter((call) => call.path.endsWith('/stabilize'))
  assert.equal(stabilizeCalls.length, 2)
  for (const call of stabilizeCalls) {
    // 请求体严格是 {}：多一个字段服务端就 400。
    assert.deepEqual(call.body, {})
    assert.ok(call.idempotencyKey)
  }

  // 指派 → 开始 → 以闭环证据完成。
  const actionCalls = fake.calls.filter((call) => call.path.endsWith('/actions'))
  assert.equal(actionCalls.length, 3)
  const completeCall = actionCalls[2]
  assert.deepEqual((completeCall.body as { closure_evidence: unknown[] }).closure_evidence, [
    { kind: 'ChapterVersion', id: 'ch-m', version: 'v-m1' },
  ])
})

test('交付闭环阶段：检查不带幂等键、导出体只有 format、下载用 download_path', async () => {
  const fake = createFakeClient()
  const bytes = new Uint8Array([9, 8, 7])

  fake.reply(PROJECT_PATH, { status: 200, body: PROJECT_BODY })
  fake.reply(`${PROJECT_PATH}/checks`, {
    status: 200,
    body: {
      data: {
        receipt_id: 'rcpt-1',
        project_version: 3,
        ruleset_hash: 'ruleset-hash',
        status: 'passed',
        issues: [],
      },
    },
  })
  fake.reply(`${PROJECT_PATH}/template-checks`, {
    status: 200,
    body: { data: { status: 'passed', evaluations: [] } },
  })
  fake.reply(`${PROJECT_PATH}/check-receipts/rcpt-1`, {
    status: 200,
    body: { data: { id: 'rcpt-1', version: 1, status: 'passed', project_version: 3, context_revision: 3 } },
  })
  fake.reply(`${PROJECT_PATH}/impact-tasks`, {
    status: 200,
    body: {
      data: [
        {
          id: 'task-9',
          change_set_id: 'cs-1',
          target: { kind: 'TemplateCopy', id: 'template-demo', version: '1' },
          target_revision: 3,
          action: 'RECOMPUTE',
          severity: 'info',
          status: 'OPEN',
          causes: [],
          version: 1,
        },
      ],
    },
  })
  fake.reply(`${PROJECT_PATH}/impact-tasks/task-9/actions`, {
    status: 200,
    body: {
      data: {
        id: 'task-9',
        change_set_id: 'cs-1',
        target: { kind: 'TemplateCopy', id: 'template-demo', version: '1' },
        target_revision: 3,
        action: 'RECOMPUTE',
        severity: 'info',
        status: 'DONE',
        causes: [],
        version: 4,
      },
    },
  })
  fake.replyFor('POST', `${PROJECT_PATH}/releases`, {
    status: 201,
    body: {
      data: {
        id: 'snap-1',
        project_id: 'p-1',
        snapshot_digest: 'digest',
        check: { status: 'passed' },
        is_current: true,
      },
    },
  })
  fake.replyFor('GET', `${PROJECT_PATH}/releases`, {
    status: 200,
    body: { data: { items: [], truncated: false } },
  })
  fake.reply(`${PROJECT_PATH}/releases/snap-1/exports`, {
    status: 202,
    body: {
      data: {
        id: 'exp-1',
        project_id: 'p-1',
        snapshot_id: 'snap-1',
        status: 'verified',
        file_sha256: 'c'.repeat(64),
        download_path: '/api/v1/lingdoc/projects/p-1/exports/exp-1/file',
      },
    },
  })
  fake.reply(`${PROJECT_PATH}/exports/exp-1`, {
    status: 200,
    body: {
      data: {
        id: 'exp-1',
        project_id: 'p-1',
        snapshot_id: 'snap-1',
        status: 'verified',
        file_sha256: 'c'.repeat(64),
        download_path: '/api/v1/lingdoc/projects/p-1/exports/exp-1/file',
      },
    },
  })
  fake.reply('/api/v1/lingdoc/projects/p-1/exports/exp-1/file', { status: 200, bytes })
  fake.replyFor('POST', `${PROJECT_PATH}/change-sets/cs-1/stabilize`, {
    status: 200,
    body: {
      data: {
        change_set_id: 'cs-1',
        context_revision: 3,
        status: 'STABILIZED',
        blockers: [],
        stale_task_ids: [],
        checked_at: '2026-10-20T00:02:00Z',
      },
    },
  })

  const ctx = createRunContext({ runId: 'g3r-test-0002', accountEmail: 'demo@example.com' })
  const closure = await runDeliveryClosure(ctx, fake.client, 'token-1', {
    projectId: 'p-1',
    changeSetId: 'cs-1',
    expectedProjectVersion: 3,
  })

  assert.equal(closure.check.status, 'passed')
  assert.equal(closure.receipt.id, 'rcpt-1')
  assert.equal(closure.snapshot.checkStatus, 'passed')
  assert.equal(closure.exportAccepted.status, 202)
  assert.equal(closure.exportArtifact.status, 'verified')
  assert.equal(closure.download.size, 3)
  assert.equal(closure.download.sha256.length, 64)

  const checkCall = fake.calls.find((call) => call.path.endsWith('/checks'))
  assert.ok(checkCall)
  // 契约未标幂等键的交付检查**不带**键。
  assert.equal(checkCall.idempotencyKey, undefined)
  assert.deepEqual(checkCall.body, { expected_project_version: 3 })

  const exportCall = fake.calls.find((call) => call.path.endsWith('/exports'))
  assert.ok(exportCall)
  assert.deepEqual(exportCall.body, { format: 'docx' })
  assert.ok(exportCall.idempotencyKey)

  // 下载用的是响应里的 download_path，不自己拼前缀。
  const downloadCall = fake.calls.find((call) => call.path.endsWith('/file'))
  assert.ok(downloadCall)
  assert.equal(downloadCall.path, '/api/v1/lingdoc/projects/p-1/exports/exp-1/file')
})
