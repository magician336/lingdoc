import assert from 'node:assert/strict'
import test from 'node:test'
import { G3_STATUS_VALUES } from './g3Status'
import {
  G3_AC_ROWS,
  G3_LIFECYCLE_READINGS,
  G3_MATRIX_ROWS,
  G3_SATISFIED_READING,
  G3_SLICE_ROWS,
  formatAnchor,
  parseAnchor,
  statusCounts,
} from './g3Matrix'

const MAIN_BASELINE = 'docs/06-灵档产品开发规划/beta-gap-development-plans/G3-pcc-change-coordination.md'

test('切片七行、验收场景十六行', () => {
  assert.equal(G3_SLICE_ROWS.length, 7)
  assert.equal(G3_AC_ROWS.length, 16)
  assert.equal(G3_MATRIX_ROWS.length, 23)
  assert.deepEqual(
    G3_SLICE_ROWS.map((row) => row.id),
    ['G3.1', 'G3.2', 'G3.3', 'G3.4', 'G3.5', 'G3.6', 'G3.7'],
  )
  assert.deepEqual(
    G3_AC_ROWS.map((row) => row.id),
    Array.from({ length: 16 }, (_, index) => `AC-G3-${String(index + 1).padStart(2, '0')}`),
  )
})

test('行 ID 不重复，判据原文不空', () => {
  const ids = G3_MATRIX_ROWS.map((row) => row.id)
  assert.equal(new Set(ids).size, ids.length)
  for (const row of G3_MATRIX_ROWS) {
    assert.ok(row.name.length > 0, `${row.id} 缺名称`)
    assert.ok(row.criterion.length > 0, `${row.id} 缺判据原文`)
    assert.ok(row.evidence.length > 0, `${row.id} 缺证据位`)
  }
})

test('状态只取门禁词表的七个值', () => {
  for (const row of G3_MATRIX_ROWS) {
    assert.ok(
      (G3_STATUS_VALUES as readonly string[]).includes(row.status),
      `${row.id} 的状态 ${row.status} 不在词表内`,
    )
  }
})

test('锚点可以往返解析', () => {
  for (const row of G3_MATRIX_ROWS) {
    const text = formatAnchor(row.anchor)
    const parsed = parseAnchor(text)
    assert.ok(parsed, `${row.id} 的锚点解析失败：${text}`)
    assert.deepEqual(parsed, row.anchor)
  }
})

test('锚点格式是「基准文档路径:行号」或「路径:起-止」', () => {
  assert.equal(
    formatAnchor({ path: MAIN_BASELINE, start: 340, end: 340 }),
    `${MAIN_BASELINE}:340`,
  )
  assert.equal(
    formatAnchor({ path: MAIN_BASELINE, start: 348, end: 412 }),
    `${MAIN_BASELINE}:348-412`,
  )
  assert.deepEqual(parseAnchor(`${MAIN_BASELINE}:348-412`), {
    path: MAIN_BASELINE,
    start: 348,
    end: 412,
  })
  assert.deepEqual(parseAnchor(`${MAIN_BASELINE}:340`), {
    path: MAIN_BASELINE,
    start: 340,
    end: 340,
  })
})

test('非法锚点不解析，也不抛异常', () => {
  for (const bad of ['', 'no-line-number', ':', 'path.md:', 'path.md:0-10', 'path.md:12-', 'path.md:abc']) {
    assert.equal(parseAnchor(bad), null, `不应解析：${bad}`)
  }
})

test('切片行锚定主基准 §12 交付切片的表格行', () => {
  const starts = G3_SLICE_ROWS.map((row) => row.anchor.start)
  assert.deepEqual(starts, [340, 341, 342, 343, 344, 345, 346])
  for (const row of G3_SLICE_ROWS) {
    assert.equal(row.anchor.path, MAIN_BASELINE)
    assert.equal(row.anchor.end, row.anchor.start)
  }
})

test('验收场景行锚定主基准 §13 的逐条标题', () => {
  const starts = G3_AC_ROWS.map((row) => row.anchor.start)
  assert.deepEqual(starts, [
    350, 354, 358, 362, 366, 370, 374, 378, 382, 386, 390, 394, 398, 402, 406, 410,
  ])
  for (const row of G3_AC_ROWS) {
    assert.equal(row.anchor.path, MAIN_BASELINE)
    assert.equal(row.anchor.end, row.anchor.start + 2)
  }
})

test('当前无法实跑的验收项标为阻塞并写明缺口', () => {
  const blocked = G3_AC_ROWS.filter((row) => row.status === 'BLOCKED')
  assert.deepEqual(
    blocked.map((row) => row.id),
    ['AC-G3-03', 'AC-G3-08', 'AC-G3-14', 'AC-G3-15', 'AC-G3-16'],
  )
  for (const row of blocked) {
    assert.ok(row.note && row.note.length > 0, `${row.id} 缺缺口说明`)
  }
})

test('可实跑但本票未实跑的验收项走 follow-up 且必须挂独立 Issue', () => {
  const followUps = G3_AC_ROWS.filter((row) => row.status === 'PASS (follow-up)')
  assert.equal(followUps.length, 11)
  for (const row of followUps) {
    assert.ok(row.followUpIssue, `${row.id} 的 follow-up 缺 Issue 号`)
    assert.ok(row.followUpIssue > 0)
    assert.ok(row.note && row.note.includes('未实跑'), `${row.id} 的 follow-up 必须写明未实跑`)
  }
})

test('切片行尚未判定，标 TODO 并说明本票只建立判据基准', () => {
  for (const row of G3_SLICE_ROWS) {
    assert.equal(row.status, 'TODO')
    assert.ok(row.note && row.note.includes('判据基准'), `${row.id} 的说明不完整`)
  }
})

test('状态计数覆盖全部七种状态', () => {
  const counts = statusCounts(G3_MATRIX_ROWS)
  assert.equal(Object.keys(counts).length, 7)
  const total = Object.values(counts).reduce((sum, value) => sum + value, 0)
  assert.equal(total, 23)
  assert.equal(counts['BLOCKED'], 5)
  assert.equal(counts['PASS (follow-up)'], 11)
  assert.equal(counts['TODO'], 7)
})

test('ChangeSet 状态集合的三种读法都被标出位置，且以主基准为准', () => {
  assert.equal(G3_LIFECYCLE_READINGS.length, 3)

  const primary = G3_LIFECYCLE_READINGS.filter((reading) => reading.primary)
  assert.equal(primary.length, 1)
  assert.deepEqual([...primary[0].states], [
    'DRAFT',
    'ASSESSED',
    'APPLIED',
    'STABILIZED',
    'REJECTED',
    'STALE',
    'SATISFIED',
  ])

  // 30-Spec：六态，列了 DRAFT 却漏了 SATISFIED。
  const spec = G3_LIFECYCLE_READINGS.find((reading) => reading.source.includes('30-Spec'))
  assert.ok(spec)
  assert.equal(spec.states.length, 6)
  assert.equal(spec.states.includes('SATISFIED'), false)
  assert.equal(spec.anchor.path.endsWith('30-Spec-G3-ChangeSet生命周期与ContextRevision.md'), true)
  assert.equal(spec.anchor.start, 38)

  // 首版最小变更复核：五态，小写，且没有 draft / stabilized。
  const review = G3_LIFECYCLE_READINGS.find((reading) => reading.source.includes('首版最小变更复核'))
  assert.ok(review)
  assert.deepEqual([...review.states], ['assessed', 'applied', 'rejected', 'stale', 'satisfied'])
  assert.equal(review.anchor.start, 48)

  for (const reading of G3_LIFECYCLE_READINGS) {
    assert.ok(reading.note.length > 0, `${reading.source} 缺说明`)
    assert.ok(parseAnchor(formatAnchor(reading.anchor)), `${reading.source} 的锚点不合法`)
  }
})

test('SATISFIED 的两种读法都被标出位置，主基准读作新建评估记录、原记录留历史', () => {
  assert.ok(G3_SATISFIED_READING.primary.text.includes('新建评估记录'))
  assert.ok(G3_SATISFIED_READING.primary.text.includes('历史'))
  assert.ok(G3_SATISFIED_READING.primary.text.includes('reassessed_from_id'))
  assert.equal(G3_SATISFIED_READING.primary.anchor.path, MAIN_BASELINE)
  assert.equal(G3_SATISFIED_READING.primary.anchor.start, 147)

  assert.ok(G3_SATISFIED_READING.alternative.text.includes('原记录'))
  assert.equal(
    G3_SATISFIED_READING.alternative.anchor.path.endsWith(
      '30-Spec-G3-ChangeSet生命周期与ContextRevision.md',
    ),
    true,
  )
  assert.equal(G3_SATISFIED_READING.alternative.anchor.start, 45)
})
