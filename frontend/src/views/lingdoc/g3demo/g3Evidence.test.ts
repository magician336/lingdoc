import assert from 'node:assert/strict'
import test from 'node:test'
import {
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
import { createRunContext, markRunStale, recordStep, type G3BackendHead } from './runner/runContext'

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
