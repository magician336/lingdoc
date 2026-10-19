import assert from 'node:assert/strict'
import test from 'node:test'
import {
  G3_DOMAIN_DESCRIPTIONS,
  G3_RESPONSIBILITY_DOMAINS,
  G3_SEVERITIES,
  G3_SEVERITY_DESCRIPTIONS,
  G3_STATUS_VALUES,
  IMPACT_TASK_STATUSES,
  blocksRelease,
  isG3Status,
  isImpactResolutionResult,
  isImpactTaskStatus,
  severityOfAction,
  statusSummary,
  statusTone,
} from './g3Status'

test('状态词表只能是仓库门禁的七个值', () => {
  // docs/agents/development-rules.md:82 —— 词表是封闭的，页面不许自造。
  assert.deepEqual([...G3_STATUS_VALUES], [
    'PASS',
    'PASS (follow-up)',
    'N/A',
    'WARNING',
    'NOT RUN',
    'BLOCKED',
    'TODO',
  ])
  assert.equal(G3_STATUS_VALUES.length, 7)
})

test('自造词一律不认', () => {
  for (const value of ['PASSED', '通过', 'OK', 'SKIPPED', 'partial', 'pass', 'Follow-up', '']) {
    assert.equal(isG3Status(value), false, `不应接受：${value}`)
  }
  for (const value of G3_STATUS_VALUES) {
    assert.equal(isG3Status(value), true, `应接受：${value}`)
  }
})

test('放行判定只看三个词，其余不阻塞', () => {
  // development-rules.md:84 —— TODO / NOT RUN / BLOCKED 出现时 PR 不能放行。
  for (const status of ['TODO', 'NOT RUN', 'BLOCKED'] as const) {
    assert.equal(blocksRelease(status), true, status)
  }
  for (const status of ['PASS', 'PASS (follow-up)', 'N/A', 'WARNING'] as const) {
    assert.equal(blocksRelease(status), false, status)
  }
})

test('状态到渲染色调是一一映射，没有兜底分支', () => {
  assert.equal(statusTone('PASS'), 'pass')
  assert.equal(statusTone('PASS (follow-up)'), 'follow-up')
  assert.equal(statusTone('N/A'), 'na')
  assert.equal(statusTone('WARNING'), 'warning')
  assert.equal(statusTone('NOT RUN'), 'not-run')
  assert.equal(statusTone('BLOCKED'), 'blocked')
  assert.equal(statusTone('TODO'), 'todo')
})

test('每个状态都有一句中文释义，不能是空串', () => {
  for (const status of G3_STATUS_VALUES) {
    const summary = statusSummary(status)
    assert.ok(summary.length > 0, `${status} 缺释义`)
  }
  assert.match(statusSummary('PASS (follow-up)'), /Issue/)
})

test('严重度三档、责任域三域，都取自仓库术语表', () => {
  assert.deepEqual([...G3_SEVERITIES], ['blocking', 'warning', 'info'])
  assert.deepEqual([...G3_RESPONSIBILITY_DOMAINS], ['A', 'B', 'C'])
})

test('术语表里每一档严重度与每一个责任域都有中文释义', () => {
  for (const severity of G3_SEVERITIES) {
    assert.ok(G3_SEVERITY_DESCRIPTIONS[severity].length > 0, severity)
  }
  for (const domain of G3_RESPONSIBILITY_DOMAINS) {
    assert.ok(G3_DOMAIN_DESCRIPTIONS[domain].length > 0, domain)
  }
  // 责任域不是严重度：释义里不能把两者混为一谈。
  assert.ok(G3_DOMAIN_DESCRIPTIONS.B.includes('确认'))
  assert.ok(G3_SEVERITY_DESCRIPTIONS.blocking.includes('阻断'))
})

test('NO_ACTION 不是影响任务状态', () => {
  // ADR 0003：NO_ACTION 是影响解析结果，不是任务处置状态。
  assert.equal(isImpactTaskStatus('NO_ACTION'), false)
  assert.equal(isImpactResolutionResult('NO_ACTION'), true)
  assert.equal(IMPACT_TASK_STATUSES.includes('NO_ACTION' as never), false)

  for (const status of IMPACT_TASK_STATUSES) {
    assert.equal(isImpactTaskStatus(status), true, status)
  }
})

test('动作到严重度的推导是 ADR 0003 的那张表，词表外一律 blocking', () => {
  assert.equal(severityOfAction('BLOCK_RELEASE'), 'blocking')
  assert.equal(severityOfAction('REVIEW_REQUIRED'), 'blocking')
  assert.equal(severityOfAction('INVALIDATE_CONFIRMATION'), 'blocking')
  assert.equal(severityOfAction('REVALIDATE_EVIDENCE'), 'warning')
  assert.equal(severityOfAction('RETRIEVE_CANDIDATES'), 'warning')
  assert.equal(severityOfAction('REGENERATE'), 'warning')
  assert.equal(severityOfAction('RECOMPUTE'), 'info')
  assert.equal(severityOfAction('NO_ACTION'), 'info')

  // 认不出的动作 fail closed：不能因为不认识就当成 info 放过去。
  assert.equal(severityOfAction('SOMETHING_ELSE'), 'blocking')
  assert.equal(severityOfAction(''), 'blocking')
})
