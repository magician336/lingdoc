import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { G3_STATUS_VALUES } from './taskStatus'
import { gapIdsForRow, limitingGapIdsForRow } from './implementationGaps'
import {
  G3_AC_ROWS,
  G3_LIFECYCLE_READINGS,
  G3_MATRIX_ROWS,
  G3_SATISFIED_READING,
  G3_SLICE_ROWS,
  formatAnchor,
  parseAnchor,
  statusCounts,
} from './impactMatrix'

const MAIN_BASELINE = 'docs/06-灵档产品开发规划/beta-gap-development-plans/G3-pcc-change-coordination.md'

/**
 * 主基准文档的绝对路径。
 *
 * 判据原文要能被读者**逐字**核对，所以这条守护断言直接读仓库根的基准文档。
 * 前端目录可能被单独 checkout（只带 `frontend/`），所以读不到时**显式 skip**，
 * 绝不静默通过——静默通过等于没有这条守护。
 */
const BASELINE_ABS = resolve(
  dirname(fileURLToPath(import.meta.url)),
  '../../../../../docs/06-灵档产品开发规划/beta-gap-development-plans/G3-pcc-change-coordination.md',
)
const BASELINE_AVAILABLE = existsSync(BASELINE_ABS)

/** 仓库根目录（本文件上溯 5 级）。 */
const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../../../..')

test('每条判据原文都逐字出现在主基准文档中', (t) => {
  if (!BASELINE_AVAILABLE) {
    t.skip(`读不到主基准文档（${BASELINE_ABS}），跳过逐字核对`)
    return
  }
  const baseline = readFileSync(BASELINE_ABS, 'utf8')
  const mismatched: string[] = []
  for (const row of G3_AC_ROWS) {
    if (!baseline.includes(row.criterion)) {
      mismatched.push(`${row.id}（${row.name}）的判据原文与主基准逐字不一致`)
    }
  }
  assert.deepEqual(
    mismatched,
    [],
    `以下判据原文没有逐字出现在 ${MAIN_BASELINE}：\n${mismatched.join('\n')}`,
  )
})

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

test('验收行不再有 BLOCKED：四张解除条件票的完成线都已达成', () => {
  const blocked = G3_AC_ROWS.filter((row) => row.status === 'BLOCKED')
  // 曾经有四行被各自的解除条件票的完成线钉在 `BLOCKED` 上：
  // AC-G3-08 / AC-G3-16（#314）、AC-G3-14（#315）、AC-G3-15（#321 的承接票）。
  // 这四张票的完成线都已达成（判定由验收台在当前 head 实跑得出），
  // 行状态因此离开 `BLOCKED`。
  //
  // `NOT RUN` 与 `BLOCKED` 语义不同，不能混用：
  // `NOT RUN` 是「本轮没观测到判定所需的事实」（例如未装配故障注入时的 AC-G3-03 判定、
  // 影响中心的 G3.7），`BLOCKED` 是「这件事被一个未完成的前置/缺陷挡住」。
  // 现在两者都不该出现在 AC 行上——前者只留在切片行（G3.7），后者一条都不剩。
  assert.deepEqual(
    blocked.map((row) => row.id),
    [],
    `还有行停在 BLOCKED：${blocked.map((row) => row.id).join('、')}`,
  )
  // 这四行都要有自己的判定与说明，不能只把状态改了。
  for (const id of ['AC-G3-08', 'AC-G3-14', 'AC-G3-15', 'AC-G3-16']) {
    const row = G3_AC_ROWS.find((item) => item.id === id)!
    assert.notEqual(row.status, 'BLOCKED', `${id} 仍停在 BLOCKED`)
    assert.ok(row.note && row.note.includes('实跑'), `${id} 的说明必须写明实跑判定`)
  }
})

test('可实跑但本票未实跑的验收项走 follow-up 且必须挂独立 Issue', () => {
  const followUps = G3_AC_ROWS.filter((row) => row.status === 'PASS (follow-up)')
  assert.equal(followUps.length, 15)
  // 这些是**例外**：它们已在本轮实跑判定成立，行状态按缺口清单 #246 的规则保留
  // follow-up（#246 未关闭前，带缺口的行不判 PASS），所以它们不适用
  // 「follow-up 必须写明未实跑」这一条。
  const judgedIds = ['AC-G3-03', 'AC-G3-08', 'AC-G3-15', 'AC-G3-16']
  const judged = followUps.filter((row) => judgedIds.includes(row.id))
  assert.equal(judged.length, judgedIds.length, '已实跑但保留 follow-up 的行不齐')
  for (const row of judged) {
    assert.ok(
      row.note?.includes('实跑') && row.note?.includes('#246'),
      `${row.id} 的说明必须写明已实跑、且状态保留 follow-up 的理由`,
    )
  }
  for (const row of followUps) {
    if (judgedIds.includes(row.id)) continue
    assert.ok(row.followUpIssue, `${row.id} 的 follow-up 缺 Issue 号`)
    assert.ok(row.followUpIssue > 0)
    assert.ok(row.note && row.note.includes('未实跑'), `${row.id} 的 follow-up 必须写明未实跑`)
  }
})

test('切片七行都有实跑判定：状态取本轮实跑观测到的值，不预判也不留空话', () => {
  // 切片行的静态状态记的是「该行最后一次被真实判定得到的结论」。
  // 每一行都必须有实跑落证（G3.5 / G3.6 是覆盖受限的 WARNING），
  // 带 #246 缺口的 G3.7 一律不判 PASS。
  const expected: Record<string, string> = {
    'G3.1': 'PASS',
    'G3.2': 'PASS',
    'G3.3': 'PASS',
    'G3.4': 'PASS',
    'G3.5': 'WARNING',
    'G3.6': 'WARNING',
    'G3.7': 'PASS (follow-up)',
  }
  for (const row of G3_SLICE_ROWS) {
    assert.equal(row.status, expected[row.id], `${row.id} 的状态与本轮实跑观测不一致`)
    // 证据位必须指到**实跑**：没有落证的 PASS 就是把没跑成的写成跑通。
    assert.match(row.evidence, /实跑|判定卡/, `${row.id} 的证据位没有指向实跑落证`)
    // 说明必须写清「成立 / 只成立一部分」与缺什么，不能是空话。
    assert.ok(row.note && row.note.length > 10, `${row.id} 的说明不完整`)
    assert.match(row.note, /实跑|判定卡/, `${row.id} 的说明没有写明实跑判定`)
  }
  // 带缺口的行不判 PASS：缺口清单 #246 关闭前一律不判。
  for (const row of G3_SLICE_ROWS) {
    if (gapIdsForRow(row.id).length > 0 || limitingGapIdsForRow(row.id).length > 0) {
      assert.notEqual(row.status, 'PASS', `${row.id} 带实现缺口，不得判 PASS`)
    }
  }
})

test('矩阵证据位不再硬编码运行标识与 head（它们每次重跑都会变）', () => {
  for (const row of G3_MATRIX_ROWS) {
    assert.doesNotMatch(row.evidence, /g3r-/, `${row.id} 的证据位写了运行标识（重跑即过期）`)
    assert.doesNotMatch(
      row.evidence,
      /head [`'“]?[0-9a-f]{7,}/i,
      `${row.id} 的证据位写了 head（重跑即过期）`,
    )
  }
  // 切片七行：证据位必须指到「链 + 判定卡 + 落证文件」三样。
  for (const row of G3_SLICE_ROWS) {
    assert.match(row.evidence, /run-[a-z]+\.ts/, `${row.id} 的证据位没有指到链脚本`)
    assert.match(row.evidence, new RegExp(`判定卡 ${row.id}`), `${row.id} 的证据位没有指到自己的判定卡`)
    assert.match(
      row.evidence,
      /落证：docs\/08-本轮实施方案\/.+\.json/,
      `${row.id} 的证据位没有指到落证文件`,
    )
    // 结构化字段：哪条链的哪张卡判的（导出体按它核对「本文件里有没有这张卡」）。
    assert.equal(row.judgedBy?.card, row.id, `${row.id} 的判定来源没有指名自己的判定卡`)
    assert.ok(row.judgedBy?.chain, `${row.id} 的判定来源没有指名链脚本`)
  }
})

test('每一行都写明证据位的类别（结构化，不靠中文子串判定）', () => {
  for (const row of G3_MATRIX_ROWS) {
    assert.ok(
      (['this_run', 'historical_snapshot', 'not_judged'] as const).includes(row.evidenceKind),
      `${row.id} 缺证据类别`,
    )
  }
  // 历史快照行（b3 那一轮）标成 historical_snapshot；切片七行标成 this_run。
  for (const id of ['AC-G3-01', 'AC-G3-10', 'AC-G3-13']) {
    assert.equal(
      G3_MATRIX_ROWS.find((row) => row.id === id)?.evidenceKind,
      'historical_snapshot',
      `${id} 的证据类别不是历史快照`,
    )
  }
  for (const row of G3_SLICE_ROWS) {
    assert.equal(row.evidenceKind, 'this_run', `${row.id} 的证据类别不是本轮实跑`)
  }
})

test('状态计数覆盖全部七种状态', () => {  const counts = statusCounts(G3_MATRIX_ROWS)
  assert.equal(Object.keys(counts).length, 7)
  const total = Object.values(counts).reduce((sum, value) => sum + value, 0)
  assert.equal(total, 23)
  assert.equal(counts['BLOCKED'], 0)
  assert.equal(counts['PASS (follow-up)'], 16)
  assert.equal(counts['TODO'], 0)
  assert.equal(counts['NOT RUN'], 0)
  // AC-G3-14 的结论依赖真实模型（非确定性），按仓库口径记 WARNING，不写 PASS。
  // G3.5（路线图切片）与 G3.6（稳定化切片）是覆盖受限，也记 WARNING。
  assert.equal(counts['WARNING'], 3)
  // 切片行 G3.1–G3.4 本轮实跑判定成立，且**没有** #246 缺口，可以判 PASS；
  // 带缺口的行（AC 行与 G3.7）一律不判 PASS。
  assert.equal(counts['PASS'], 4)
  for (const row of G3_MATRIX_ROWS) {
    if (row.status !== 'PASS') continue
    assert.equal(
      gapIdsForRow(row.id).length + limitingGapIdsForRow(row.id).length,
      0,
      `${row.id} 带实现缺口，不得判 PASS（缺口清单 #246 关闭前不判 PASS）`,
    )
  }
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
  // 来源文档已随历史文档清理删除，读法记录在收口总结第七节，锚点指向那一节。
  const review = G3_LIFECYCLE_READINGS.find((reading) => reading.source.includes('首版最小变更复核'))
  assert.ok(review)
  assert.deepEqual([...review.states], ['assessed', 'applied', 'rejected', 'stale', 'satisfied'])
  assert.equal(review.anchor.path, 'docs/08-本轮实施方案/G3-缺口收口-总结.md')
  assert.equal(review.anchor.start, 79)

  for (const reading of G3_LIFECYCLE_READINGS) {
    assert.ok(reading.note.length > 0, `${reading.source} 缺说明`)
    assert.ok(parseAnchor(formatAnchor(reading.anchor)), `${reading.source} 的锚点不合法`)
  }
})

/**
 * 页面上的每一处锚点都必须指向**仓内真实存在的文件**。
 *
 * 为什么要有这条：文档被清理时，页面上的锚点会静默指向已不存在的文件 ——
 * 读者点过去才发现，而那时没人会回来修。锚点存在性是可以在提交前机械核对的。
 */
test('状态集合与 SATISFIED 读法的每一处锚点都指向仓内真实存在的文件', (t) => {
  if (!BASELINE_AVAILABLE) {
    t.skip('读不到仓库根的文档目录（前端单独 checkout），跳过锚点存在性核对')
    return
  }
  const anchors = [
    ...G3_LIFECYCLE_READINGS.map((reading) => ({
      label: reading.source,
      path: reading.anchor.path,
    })),
    {
      label: 'SATISFIED 主读法',
      path: G3_SATISFIED_READING.primary.anchor.path,
    },
    {
      label: 'SATISFIED 另一读法',
      path: G3_SATISFIED_READING.alternative.anchor.path,
    },
  ]
  for (const anchor of anchors) {
    assert.ok(
      existsSync(resolve(REPO_ROOT, anchor.path)),
      `${anchor.label} 的锚点指向不存在的文件：${anchor.path}`,
    )
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
