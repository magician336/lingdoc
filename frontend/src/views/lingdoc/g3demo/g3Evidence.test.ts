import assert from 'node:assert/strict'
import test from 'node:test'
import {
  G3_ROW_EVIDENCE_RULES,
  G3_RUN_SUMMARY_STORAGE_KEY,
  applyRunEvidence,
  headChangedSince,
  loadRunSummary,
  rowEvidence,
  saveRunSummary,
  summarizeRun,
  type G3RunStorage,
} from './g3Evidence'
import { G3_MATRIX_ROWS } from './g3Matrix'
import { G3_STATUS_VALUES } from './g3Status'
import { G3_BLOCKED_STEPS } from './runner/blocked'
import { G3_CS_STEPS } from './runner/changeset'
import { G3_DEP_STEPS } from './runner/dependencies'
import { G3_MODEL_STEPS } from './runner/model'
import { G3_STAB_STEPS } from './runner/stabilization'
import { createRunContext, markRunStale, recordStep, type G3BackendHead, type G3RunVerdict } from './runner/runContext'

function memoryStorage(): G3RunStorage & { map: Map<string, string> } {
  const map = new Map<string, string>()
  return {
    map,
    getItem: (key) => map.get(key) ?? null,
    setItem: (key, value) => {
      map.set(key, value)
    },
  }
}

function head(commitId: string): G3BackendHead {
  return { commitId, probedAt: '2026-10-20T00:00:00.000Z', probePath: '/api/v1/version', probeStatus: 401 }
}

/** 造一个跑完前置链的上下文。 */
function prerequisiteRun() {
  const ctx = createRunContext({ runId: 'g3r-ev-1', accountEmail: 'g3demo-x@example.com' })
  for (const name of ['建项', '写规格', '登记立项阅读', '激活', '读章节列表']) {
    recordStep(ctx, {
      name,
      method: 'POST',
      path: '/api/v1/lingdoc/projects/p1',
      status: 200,
      ok: true,
      durationMs: 5,
      conclusion: '成功',
    })
  }
  ctx.head = head('7f5fd8c')
  return ctx
}

test('跑通前置链不改变任何一行的状态', () => {
  const ctx = prerequisiteRun()
  const before = new Map(G3_MATRIX_ROWS.map((row) => [row.id, row.status]))
  const { rows } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  for (const row of rows) {
    assert.equal(row.status, before.get(row.id), `${row.id} 的状态被前置链改动了`)
  }
})

test('跑通前置链不会把任何一行判成 PASS', () => {
  const ctx = prerequisiteRun()
  const { rows } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  assert.equal(rows.filter((row) => row.status === 'PASS').length, 0)
  assert.equal(rows.filter((row) => row.status === 'TODO').length, 7, '切片七行应保持 TODO')
})

test('未被本轮覆盖的行明确标出未覆盖', () => {
  const ctx = prerequisiteRun()
  const { evidence } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  assert.equal(evidence.length, G3_MATRIX_ROWS.length)
  for (const item of evidence) {
    assert.equal(item.covered, false, `${item.rowId} 不该被前置链覆盖`)
    assert.equal(item.decided, false)
    assert.match(item.note, /未覆盖/)
  }
})

test('head 变化后既有结论被标为过期，且状态不因此改变', () => {
  const ctx = prerequisiteRun()
  markRunStale(ctx, '后端 head 从 7f5fd8c 变为 af18497')
  const before = new Map(G3_MATRIX_ROWS.map((row) => [row.id, row.status]))
  const { rows, evidence } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  for (const row of rows) {
    assert.equal(row.status, before.get(row.id))
  }
  assert.equal(evidence.every((item) => item.stale), true)
  assert.match(evidence[0].note, /过期/)
})

test('head 变了才判过期', () => {
  const summary = summarizeRun(prerequisiteRun(), { missingSections: [] })
  assert.equal(headChangedSince(summary, head('7f5fd8c')).changed, false)
  const changed = headChangedSince(summary, head('af18497'))
  assert.equal(changed.changed, true)
  assert.match(changed.reason, /af18497/)
  assert.equal(headChangedSince(null, head('af18497')).changed, false)
  assert.equal(headChangedSince(summary, null).changed, false)
})

test('运行摘要可以落盘再读回来', () => {
  const storage = memoryStorage()
  const summary = summarizeRun(prerequisiteRun(), { missingSections: ['method'] })
  saveRunSummary(storage, summary)
  assert.ok(storage.map.get(G3_RUN_SUMMARY_STORAGE_KEY))
  const loaded = loadRunSummary(storage)
  assert.equal(loaded?.runId, summary.runId)
  assert.deepEqual(loaded?.missingSections, ['method'])
  assert.equal(loaded?.steps.length, 5)
})

test('坏掉的摘要读成 null，不静默当成空运行', () => {
  const storage = memoryStorage()
  storage.setItem(G3_RUN_SUMMARY_STORAGE_KEY, '{ broken')
  assert.equal(loadRunSummary(storage), null)
  storage.setItem(G3_RUN_SUMMARY_STORAGE_KEY, JSON.stringify({ startedAt: 'x' }))
  assert.equal(loadRunSummary(storage), null)
})

test('单行覆盖计算不依赖规则表', () => {
  const ctx = prerequisiteRun()
  const item = rowEvidence(ctx, G3_MATRIX_ROWS[0])
  assert.equal(item.rowId, 'G3.1')
  assert.equal(item.covered, false)
  assert.equal(item.stale, false)
})

test('规则表只覆盖已被实跑判定的行，状态取门禁词表内的值', () => {
  assert.deepEqual(
    Object.keys(G3_ROW_EVIDENCE_RULES).sort(),
    [
      'AC-G3-01', 'AC-G3-02',
      'AC-G3-04', 'AC-G3-05', 'AC-G3-06',
      'AC-G3-07', 'AC-G3-09',
      'AC-G3-11', 'AC-G3-12',
      'AC-G3-13', 'AC-G3-14', 'AC-G3-15',
      'G3.1',     'G3.6'
    ],
  )
  for (const [id, rule] of Object.entries(G3_ROW_EVIDENCE_RULES)) {
    assert.ok(G3_STATUS_VALUES.includes(rule.status), `${id} 的判定状态不在门禁词表内`)
    assert.ok(rule.requires.length > 0, `${id} 没有声明必需步骤`)
    assert.ok(rule.note.length > 0, `${id} 没有给读者看的说明`)
    for (const alternative of rule.alternatives ?? []) {
      assert.ok(G3_STATUS_VALUES.includes(alternative.status), `${id} 的备选路径状态不在门禁词表内`)
      assert.ok(alternative.requires.length > 0, `${id} 的备选路径没有声明必需步骤`)
      assert.ok(alternative.note.length > 0, `${id} 的备选路径没有给读者看的说明`)
    }
  }
  // 完全判定的行是 PASS；部分覆盖的行是 PASS (follow-up) 并指向缺口清单 #246。
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-05'].status, 'PASS')
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-13'].status, 'PASS')
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-14'].status, 'PASS (follow-up)')
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-15'].status, 'PASS (follow-up)')
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-14'].followUpIssue, 246)
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-15'].followUpIssue, 246)
  // 真实模型路径的结论是非确定性的：状态一律 WARNING，并保留缺口清单 #246。
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-06'].status, 'WARNING')
  for (const id of ['AC-G3-14', 'AC-G3-15']) {
    const alternative = G3_ROW_EVIDENCE_RULES[id].alternatives?.[0]
    assert.equal(alternative?.status, 'WARNING')
    assert.equal(alternative?.followUpIssue, 246)
  }
  // 本票新增的两条切片判据：G3.1（ChangeSet 切片）与 G3.6（稳定化切片）都是完全判定。
  assert.equal(G3_ROW_EVIDENCE_RULES['G3.1'].status, 'PASS')
  assert.equal(G3_ROW_EVIDENCE_RULES['G3.6'].status, 'PASS')
  // 本票新增的三条领域判据：AC-G3-12 完全判定；AC-G3-09 与 AC-G3-07 只覆盖了实跑得出的那半段。
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-12'].status, 'PASS')
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-09'].status, 'PASS (follow-up)')
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-09'].followUpIssue, 246)
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-07'].status, 'PASS (follow-up)')
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-07'].followUpIssue, 243)
})

test('规则表引用的步骤名都真实存在（防止证据链断在名字上）', () => {
  const known = new Set<string>([
    ...Object.values(G3_CS_STEPS),
    ...Object.values(G3_DEP_STEPS),
    ...Object.values(G3_STAB_STEPS),
    ...Object.values(G3_BLOCKED_STEPS),
    ...Object.values(G3_MODEL_STEPS),
  ])
  for (const [id, rule] of Object.entries(G3_ROW_EVIDENCE_RULES)) {
    for (const name of rule.requires) {
      assert.ok(known.has(name), `${id} 引用了不存在的步骤名：${name}`)
    }
    for (const alternative of rule.alternatives ?? []) {
      for (const name of alternative.requires) {
        assert.ok(known.has(name), `${id} 的备选路径引用了不存在的步骤名：${name}`)
      }
    }
  }
})

test('跑完本票步骤后，被覆盖的行被本轮证据驱动', () => {
  const ctx = prerequisiteRun()
  const allSteps = [...Object.values(G3_CS_STEPS), ...Object.values(G3_DEP_STEPS)]
  for (const name of allSteps) {
    recordStep(ctx, {
      name,
      method: 'POST',
      path: '/api/v1/lingdoc/projects/p1',
      status: 200,
      ok: true,
      durationMs: 5,
      conclusion: '成功',
    })
  }
  // 带 verdictId 的规则以**判定结论**为准：判定不成立就不许判 PASS。
  ctx.verdicts = ['AC-G3-05', 'AC-G3-13', 'AC-G3-14', 'AC-G3-15'].map((id) => ({
    id,
    title: id,
    holds: true,
    detail: '成立',
    evidence: [],
  }))
  const { rows } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  const byId = new Map(rows.map((row) => [row.id, row]))
  for (const id of ['AC-G3-01', 'AC-G3-02', 'AC-G3-04', 'AC-G3-11', 'AC-G3-05', 'AC-G3-13']) {
    assert.equal(byId.get(id)?.status, 'PASS', `${id} 没有被本轮证据驱动`)
    assert.equal(byId.get(id)?.followUpIssue, undefined, `${id} 被判定后不应再挂「跟进 Issue」`)
  }
  // 部分覆盖的两行：状态是 PASS (follow-up)，并保留指向缺口清单的跟进 Issue。
  for (const id of ['AC-G3-14', 'AC-G3-15']) {
    assert.equal(byId.get(id)?.status, 'PASS (follow-up)')
    assert.equal(byId.get(id)?.followUpIssue, 246)
  }
  // 规则表之外的行状态原样保留。
  assert.equal(byId.get('AC-G3-03')?.status, 'BLOCKED')
  assert.equal(byId.get('G3.1')?.status, 'TODO')
  assert.equal(byId.get('AC-G3-10')?.followUpIssue, 239, '没被本轮判定的行要保留跟进 Issue')
})

test('步骤跑齐但判定不成立时，那一行仍然不被判定', () => {
  const ctx = prerequisiteRun()
  for (const name of [
    G3_DEP_STEPS.createEdge,
    G3_CS_STEPS.readAssessment,
    G3_CS_STEPS.apply,
    G3_DEP_STEPS.readTasks,
    G3_DEP_STEPS.replayApply,
  ]) {
    recordStep(ctx, {
      name,
      method: 'POST',
      path: '/api/v1/lingdoc/projects/p1',
      status: 200,
      ok: true,
      durationMs: 5,
      conclusion: '成功',
    })
  }
  ctx.verdicts = [{ id: 'AC-G3-05', title: '多上游任务合并', holds: false, detail: '不成立', evidence: [] }]
  const { rows, evidence } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  const byId = new Map(rows.map((row) => [row.id, row]))
  assert.equal(byId.get('AC-G3-05')?.status, 'PASS (follow-up)', '判定不成立不得判成 PASS')
  const item = evidence.find((entry) => entry.rowId === 'AC-G3-05')
  assert.equal(item?.decided, false)
  assert.match(item?.note ?? '', /判定卡/)
})

test('步骤没跑齐时，那一行不被判定，状态原样保留', () => {
  const ctx = prerequisiteRun()
  recordStep(ctx, {
    name: G3_CS_STEPS.create,
    method: 'POST',
    path: '/api/v1/lingdoc/projects/p1/change-sets',
    status: 201,
    ok: true,
    durationMs: 5,
    conclusion: '创建变更集',
  })
  const { rows, evidence } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  const byId = new Map(rows.map((row) => [row.id, row]))
  assert.equal(byId.get('AC-G3-01')?.status, 'PASS (follow-up)', '步骤没跑齐不得判成 PASS')
  const item = evidence.find((entry) => entry.rowId === 'AC-G3-01')
  assert.equal(item?.decided, false)
  assert.ok(item?.missingSteps.length)
})

test('稳定化链跑完后，G3.1 与 G3.6 由本轮证据驱动为 PASS', () => {
  const ctx = prerequisiteRun()
  for (const name of Object.values(G3_STAB_STEPS)) {
    recordStep(ctx, {
      name,
      method: 'POST',
      path: '/api/v1/lingdoc/projects/p1/stabilize',
      status: 200,
      ok: true,
      durationMs: 5,
      conclusion: '成功',
    })
  }
  ctx.verdicts = ['G3.1', 'G3.6'].map((id) => ({
    id,
    title: id,
    holds: true,
    detail: '成立',
    evidence: [],
  }))
  const { rows } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  const byId = new Map(rows.map((row) => [row.id, row]))
  assert.equal(byId.get('G3.1')?.status, 'PASS', 'G3.1 没有被稳定化链的证据驱动')
  assert.equal(byId.get('G3.6')?.status, 'PASS', 'G3.6 没有被稳定化链的证据驱动')
})

test('稳定化链的判定不成立时，G3.1 与 G3.6 保持原样', () => {
  const ctx = prerequisiteRun()
  for (const name of Object.values(G3_STAB_STEPS)) {
    recordStep(ctx, {
      name,
      method: 'POST',
      path: '/api/v1/lingdoc/projects/p1/stabilize',
      status: 200,
      ok: true,
      durationMs: 5,
      conclusion: '成功',
    })
  }
  ctx.verdicts = [
    { id: 'G3.1', title: 'G3.1', holds: false, detail: '不成立', evidence: [] },
    { id: 'G3.6', title: 'G3.6', holds: false, detail: '不成立', evidence: [] },
  ]
  const { rows } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  const byId = new Map(rows.map((row) => [row.id, row]))
  assert.equal(byId.get('G3.1')?.status, 'TODO', '判定不成立不得判成 PASS')
  assert.equal(byId.get('G3.6')?.status, 'TODO', '判定不成立不得判成 PASS')
})

test('阻断与恢复链跑完后，AC-G3-09 / AC-G3-12 / AC-G3-07 由本轮证据驱动', () => {
  const ctx = prerequisiteRun()
  for (const name of Object.values(G3_BLOCKED_STEPS)) {
    recordStep(ctx, {
      name,
      method: 'POST',
      path: '/api/v1/lingdoc/projects/p1/stabilize',
      status: 200,
      ok: true,
      durationMs: 5,
      conclusion: '成功',
    })
  }
  ctx.verdicts = ['AC-G3-09', 'AC-G3-12', 'AC-G3-07'].map((id) => ({
    id,
    title: id,
    holds: true,
    detail: '成立',
    evidence: [],
  }))
  const { rows } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  const byId = new Map(rows.map((row) => [row.id, row]))
  assert.equal(byId.get('AC-G3-12')?.status, 'PASS')
  assert.equal(byId.get('AC-G3-12')?.followUpIssue, undefined)
  // 部分覆盖的两行：状态是 PASS (follow-up)，并保留指向缺口清单的跟进 Issue。
  assert.equal(byId.get('AC-G3-09')?.status, 'PASS (follow-up)')
  assert.equal(byId.get('AC-G3-09')?.followUpIssue, 246)
  assert.equal(byId.get('AC-G3-07')?.status, 'PASS (follow-up)')
  assert.equal(byId.get('AC-G3-07')?.followUpIssue, 243)
})

test('阻断与恢复链的判定不成立时，那三行保持原样', () => {
  const ctx = prerequisiteRun()
  for (const name of Object.values(G3_BLOCKED_STEPS)) {
    recordStep(ctx, {
      name,
      method: 'POST',
      path: '/api/v1/lingdoc/projects/p1/stabilize',
      status: 200,
      ok: true,
      durationMs: 5,
      conclusion: '成功',
    })
  }
  ctx.verdicts = ['AC-G3-09', 'AC-G3-12', 'AC-G3-07'].map((id) => ({
    id,
    title: id,
    holds: false,
    detail: '不成立',
    evidence: [],
  }))
  const { rows } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  const byId = new Map(rows.map((row) => [row.id, row]))
  for (const id of ['AC-G3-09', 'AC-G3-12', 'AC-G3-07']) {
    assert.notEqual(byId.get(id)?.status, 'PASS', `${id} 判定不成立不得判成 PASS`)
  }
})

/** 造一个跑完真实模型链的上下文。 */
function modelRun(verdicts: G3RunVerdict[]) {
  const ctx = prerequisiteRun()
  for (const name of [
    G3_MODEL_STEPS.ac06Conclusion,
    G3_MODEL_STEPS.ac14Conclusion,
    G3_MODEL_STEPS.ac15Conclusion,
  ]) {
    recordStep(ctx, {
      name,
      method: 'NOTE',
      path: '/api/v1/lingdoc/projects/p1（场景结论）',
      status: 200,
      ok: true,
      durationMs: 5,
      conclusion: '场景结论',
    })
  }
  ctx.verdicts = verdicts
  return ctx
}

test('真实模型链跑完后，三条非确定性结论标 WARNING 并与确定性结论分列', () => {
  const ctx = modelRun([
    { id: 'AC-G3-06', title: '旧异步结果', holds: true, detail: '模型运行标识 turn-1 · 耗时 4200ms', evidence: [], status: 'WARNING' },
    { id: 'AC-G3-14', title: '影响助手', holds: true, detail: '模型运行标识 turn-2 · 耗时 3800ms', evidence: [], status: 'WARNING' },
    { id: 'AC-G3-15', title: '依赖候选', holds: true, detail: '模型运行标识 turn-3 · 耗时 5100ms', evidence: [], status: 'WARNING' },
  ])
  const { rows } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  const byId = new Map(rows.map((row) => [row.id, row]))
  for (const id of ['AC-G3-06', 'AC-G3-14', 'AC-G3-15']) {
    assert.equal(byId.get(id)?.status, 'WARNING', `${id} 没有按非确定性结论标 WARNING`)
  }
  // 部分覆盖的两行仍然指向缺口清单 #246。
  assert.equal(byId.get('AC-G3-14')?.followUpIssue, 246)
  assert.equal(byId.get('AC-G3-15')?.followUpIssue, 246)
  // 其余行状态原样保留。
  assert.equal(byId.get('AC-G3-01')?.status, 'PASS (follow-up)')
  assert.equal(byId.get('AC-G3-03')?.status, 'BLOCKED')
  // 判定卡的说明里带模型运行标识与耗时。
  assert.match(byId.get('AC-G3-06')?.note ?? '', /turn-1/)
  assert.match(byId.get('AC-G3-14')?.note ?? '', /4200|3800/)
})

test('模型不可用时，这三行标 NOT RUN，其余行照常判定', () => {
  const ctx = modelRun([
    { id: 'AC-G3-06', title: '旧异步结果', holds: false, detail: '缺外部凭据，模型不可用', evidence: [], status: 'NOT RUN' },
    { id: 'AC-G3-14', title: '影响助手', holds: false, detail: '缺外部凭据，模型不可用', evidence: [], status: 'NOT RUN' },
    { id: 'AC-G3-15', title: '依赖候选', holds: false, detail: '缺外部凭据，模型不可用', evidence: [], status: 'NOT RUN' },
  ])
  const { rows } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  const byId = new Map(rows.map((row) => [row.id, row]))
  for (const id of ['AC-G3-06', 'AC-G3-14', 'AC-G3-15']) {
    assert.equal(byId.get(id)?.status, 'NOT RUN', `${id} 没有标 NOT RUN`)
  }
  // 其余行照常判定：确定性路径的规则不在这三条里，状态原样保留。
  assert.equal(byId.get('AC-G3-01')?.status, 'PASS (follow-up)')
  assert.equal(byId.get('AC-G3-05')?.status, 'PASS (follow-up)')
})
