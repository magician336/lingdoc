import assert from 'node:assert/strict'
import test from 'node:test'
import {
  G3_GAP_LIST_ISSUE,
  G3_IMPLEMENTATION_GAPS,
  gapIdsForRow,
  gapsForRow,
  limitingGapIdsForRow,
} from './g3Gaps'
import { G3_MATRIX_ROWS } from './g3Matrix'
import { G3_STATUS_VALUES } from './g3Status'

test('缺口清单覆盖本票点名的五条：AC-G3-03、AC-G3-08、AC-G3-16 与影响中心两个渲染缺口', () => {
  const ids = new Set(G3_IMPLEMENTATION_GAPS.map((gap) => gap.id))
  assert.ok(ids.has('G3-GAP-01'), '缺 AC-G3-03 的故障注入缺口')
  assert.ok(ids.has('G3-GAP-02'), '缺 AC-G3-08 的 RouteNode 只读缺口')
  assert.ok(ids.has('G3-GAP-03'), '缺 AC-G3-16 的 RouteNode 写入 / 取消编辑缺口')
  assert.ok(ids.has('G3-GAP-06'), '缺影响中心「必需对象集合」缺口')
  assert.ok(ids.has('G3-GAP-07'), '缺影响中心「来源对象 / 确定可能分组」缺口')
  assert.deepEqual(gapIdsForRow('AC-G3-03'), ['G3-GAP-01'])
  assert.deepEqual(gapIdsForRow('AC-G3-08'), ['G3-GAP-02'])
  assert.deepEqual(gapIdsForRow('AC-G3-16'), ['G3-GAP-03'])
  assert.ok(gapIdsForRow('G3.7').includes('G3-GAP-06'))
  assert.ok(gapIdsForRow('G3.7').includes('G3-GAP-07'))
})

test('「覆盖受限」与「直接缺口」分开：判据已判定的行不会被误标成没跑过', () => {
  // 影响中心两条缺口：G3.7 是直接缺口行（页面渲染证据待落），
  // AC-G3-04 的局部影响判定本身已在 #240 落证，只是渲染证据受限于同一条缺口。
  const gap06 = G3_IMPLEMENTATION_GAPS.find((gap) => gap.id === 'G3-GAP-06')!
  assert.deepEqual([...gap06.rows], ['G3.7'])
  assert.deepEqual([...gap06.limits], ['AC-G3-04'])
  // 环境阻断与 409 那条是覆盖受限，不是「判据没跑」。
  const gap08 = G3_IMPLEMENTATION_GAPS.find((gap) => gap.id === 'G3-GAP-08')!
  assert.deepEqual([...gap08.rows], [])
  assert.deepEqual([...gap08.limits], ['G3.6'])
  const gap11 = G3_IMPLEMENTATION_GAPS.find((gap) => gap.id === 'G3-GAP-11')!
  assert.deepEqual([...gap11.rows], [])
  assert.deepEqual([...gap11.limits], ['AC-G3-12'])
})

test('本轮新发现的四条缺口也在清单里：环境阻断、dismiss 500、disposition 不被求值读取、409 替代 BLOCKED', () => {
  const ids = new Set(G3_IMPLEMENTATION_GAPS.map((gap) => gap.id))
  for (const id of ['G3-GAP-08', 'G3-GAP-09', 'G3-GAP-10', 'G3-GAP-11']) {
    assert.ok(ids.has(id), `缺 ${id}`)
  }
  assert.ok(gapIdsForRow('AC-G3-09').includes('G3-GAP-09'))
  assert.ok(gapIdsForRow('AC-G3-09').includes('G3-GAP-10'))
  assert.ok(limitingGapIdsForRow('AC-G3-12').includes('G3-GAP-11'))
  assert.ok(limitingGapIdsForRow('G3.6').includes('G3-GAP-08'))
})

test('每条缺口都写明缺什么、对应 Spec 位置与解除条件', () => {
  for (const gap of G3_IMPLEMENTATION_GAPS) {
    assert.ok(gap.missing.trim().length > 0, `${gap.id} 缺「缺什么」`)
    assert.ok(gap.specAnchor.path.length > 0, `${gap.id} 缺 Spec 路径`)
    assert.ok(gap.specAnchor.start >= 1, `${gap.id} 缺 Spec 行号`)
    assert.ok(gap.unblockCondition.trim().length > 0, `${gap.id} 缺「解除条件」`)
    assert.ok(gap.title.trim().length > 0, `${gap.id} 缺标题`)
  }
})

test('每条缺口都链接缺口清单 Issue #246', () => {
  for (const gap of G3_IMPLEMENTATION_GAPS) {
    assert.equal(gap.issue, G3_GAP_LIST_ISSUE, `${gap.id} 的缺口清单 Issue 不对`)
  }
  assert.equal(G3_GAP_LIST_ISSUE, 246)
})

test('缺口对应行的状态不写 PASS，且取自仓库门禁词表', () => {
  for (const gap of G3_IMPLEMENTATION_GAPS) {
    assert.notEqual(gap.rowStatus, 'PASS', `${gap.id} 对应行不得写 PASS`)
    assert.ok((G3_STATUS_VALUES as readonly string[]).includes(gap.rowStatus), `${gap.id} 状态不在词表内`)
  }
})

test('影响中心两条缺口的措辞是「工作台已补齐，验收台页面实跑证据待落」', () => {
  const gap06 = gapsForRow('G3.7').find((gap) => gap.id === 'G3-GAP-06')
  const gap07 = gapsForRow('G3.7').find((gap) => gap.id === 'G3-GAP-07')
  assert.ok(gap06)
  assert.ok(gap07)
  for (const gap of [gap06, gap07]) {
    assert.match(gap.title, /工作台已补齐/)
    assert.match(gap.missing, /验收台页面/)
    assert.match(gap.fact ?? '', /ImpactOpsPanel\.vue/)
    assert.doesNotMatch(gap.fact ?? '', /工作台面板未渲染/)
  }
})

test('AC-G3-14 的助手解释分组与影响解析分组不混同', () => {
  const gap = gapsForRow('AC-G3-14').find((item) => item.id === 'G3-GAP-04')
  assert.ok(gap)
  assert.match(gap.missing, /助手/)
  assert.match(gap.fact ?? '', /影响解析结果/)
  assert.match(gap.fact ?? '', /不可互替/)
})

test('每条缺口都指向矩阵里真实存在的行', () => {
  const rowIds = new Set(G3_MATRIX_ROWS.map((row) => row.id))
  for (const gap of G3_IMPLEMENTATION_GAPS) {
    const related = [...gap.rows, ...gap.limits]
    assert.ok(related.length > 0, `${gap.id} 没写对应验收行`)
    for (const row of related) {
      assert.ok(rowIds.has(row), `${gap.id} 指向了不存在的矩阵行 ${row}`)
    }
  }
})
