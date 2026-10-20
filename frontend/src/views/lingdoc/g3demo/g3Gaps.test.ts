import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import {
  G3_GAP_LIST_ISSUE,
  G3_GAP_TICKET_REGISTRY,
  G3_IMPLEMENTATION_GAPS,
  gapIdsForRow,
  gapsForRow,
  limitingGapIdsForRow,
} from './g3Gaps'
import { G3_MATRIX_ROWS } from './g3Matrix'
import { G3_STATUS_VALUES } from './g3Status'

/**
 * 缺口清单 Issue #246 的**仓库内镜像**。
 *
 * #246 是权威副本，但前端还有一份结构化副本（`g3Gaps.ts`）。没有镜像就离线
 * 核不动这两份副本，事实会各自漂移——所以这条守护直接读镜像逐条比对。
 *
 * 路径从本文件所在目录（`frontend/src/views/lingdoc/g3demo/`）上溯 5 级到仓库根。
 */
const MIRROR_ABS = resolve(
  dirname(fileURLToPath(import.meta.url)),
  '../../../../../docs/08-本轮实施方案/G3-缺口清单-246-副本.md',
)

/** 镜像里的一行缺口（按表头列序）。 */
interface MirrorGap {
  id: string
  title: string
  rows: string[]
  limits: string[]
  status: string
  ticket: number
}

/** 拆表格行：去掉首尾空列，逐格 trim。 */
function cellsOf(line: string): string[] {
  return line
    .split('|')
    .slice(1, -1)
    .map((cell) => cell.trim())
}

/**
 * 解析镜像里的「对应验收行」列。
 *
 * 写法：`rows: AC-G3-03`、`rows: G3.7; limits: AC-G3-04`、`rows: 无; limits: G3.6`。
 */
function parseRowsCell(cell: string): { rows: string[]; limits: string[] } {
  const rows: string[] = []
  const limits: string[] = []
  for (const part of cell.split(';')) {
    const matched = part.trim().match(/^(rows|limits)\s*[:：]\s*(.*)$/)
    if (!matched) continue
    const values = matched[2]
      .split(',')
      .map((value) => value.trim())
      .filter((value) => value.length > 0 && !/^(无|—|-)$/.test(value))
    if (matched[1] === 'rows') rows.push(...values)
    else limits.push(...values)
  }
  return { rows, limits }
}

/** 读镜像并解析出 11 条缺口。 */
function readMirrorGaps(): MirrorGap[] {
  assert.ok(
    existsSync(MIRROR_ABS),
    `读不到缺口清单的仓库内镜像（${MIRROR_ABS}）：镜像缺失就无法做同源守护`,
  )
  const source = readFileSync(MIRROR_ABS, 'utf8')
  const gaps: MirrorGap[] = []
  for (const line of source.split('\n')) {
    if (!/^\|\s*G3-GAP-\d+/.test(line.trim())) continue
    const cells = cellsOf(line)
    const { rows, limits } = parseRowsCell(cells[2] ?? '')
    gaps.push({
      id: cells[0] ?? '',
      title: cells[1] ?? '',
      rows,
      limits,
      status: cells[3] ?? '',
      ticket: Number((cells[4] ?? '').replace('#', '').trim()),
    })
  }
  return gaps
}

/** 读镜像里的「承接票清单」表，返回票号集合。 */
function readMirrorTickets(): number[] {
  assert.ok(
    existsSync(MIRROR_ABS),
    `读不到缺口清单的仓库内镜像（${MIRROR_ABS}）：镜像缺失就无法做同源守护`,
  )
  const source = readFileSync(MIRROR_ABS, 'utf8')
  const tickets: number[] = []
  for (const line of source.split('\n')) {
    if (!/^\|\s*#\d+/.test(line.trim())) continue
    const cells = cellsOf(line)
    const ticket = Number((cells[0] ?? '').replace('#', '').trim())
    if (Number.isFinite(ticket)) tickets.push(ticket)
  }
  return tickets
}

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
  // 影响中心两条缺口：G3.7 是直接缺口行（渲染已落证，但该行整体仍 NOT RUN），
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

test('影响中心两条缺口的措辞是「已实现并已在 head `a87459f` 落证」，不是「实跑证据待落」', () => {
  const gap06 = gapsForRow('G3.7').find((gap) => gap.id === 'G3-GAP-06')
  const gap07 = gapsForRow('G3.7').find((gap) => gap.id === 'G3-GAP-07')
  assert.ok(gap06)
  assert.ok(gap07)
  for (const gap of [gap06, gap07]) {
    // #257 已在 head `a87459f`（运行标识 g3r-mvgqpt88-7b4e074d）落证；本轮 head
    // 未重跑这一段，所以措辞必须**指名 head**，不写「已在当前 head 落证」。
    // 「实跑证据待落」「盘上证据仍绑 head 7f5fd8c」是已被实跑作废的旧口径。
    assert.match(gap.title, /已落证/)
    assert.doesNotMatch(gap.title, /实跑证据待落/)
    assert.doesNotMatch(gap.missing, /实跑证据待落/)
    assert.doesNotMatch(gap.missing, /7f5fd8c/)
    assert.match(gap.missing, /已在 head `a87459f` 落证/)
    assert.doesNotMatch(gap.missing, /已在当前 head 落证/)
    assert.match(gap.fact ?? '', /本轮 head 未重跑这一段/)
    assert.match(gap.fact ?? '', /G3Demo\.vue/)
    assert.match(gap.fact ?? '', /ImpactOpsPanel\.vue/)
    assert.match(gap.fact ?? '', /g3r-mvgqpt88-7b4e074d/)
    // 「工作台已补齐」是旧措辞：它把缺口说成「工作台没渲染」，与事实相反。
    assert.doesNotMatch(gap.title, /工作台已补齐/)
    assert.doesNotMatch(gap.fact ?? '', /工作台面板未渲染/)
  }
})

test('每条缺口都有承接票，且票号落在本轮拆出的子票区间内', () => {
  for (const gap of G3_IMPLEMENTATION_GAPS) {
    assert.ok(typeof gap.ticket === 'number' && gap.ticket > 0, `${gap.id} 缺承接票号`)
    assert.ok(gap.ticket! >= 249 && gap.ticket! <= 258, `${gap.id} 的承接票号不在 #249–#258 区间内`)
  }
  const ticketOf = (id: string) => G3_IMPLEMENTATION_GAPS.find((gap) => gap.id === id)!.ticket
  // 路线图两条缺口由同一张票承接；影响中心两条由整链落证票承接。
  assert.equal(ticketOf('G3-GAP-02'), ticketOf('G3-GAP-03'))
  assert.equal(ticketOf('G3-GAP-06'), ticketOf('G3-GAP-07'))
  assert.equal(ticketOf('G3-GAP-01'), 256)
  assert.equal(ticketOf('G3-GAP-08'), 258)
})

test('G3-GAP-11 的措辞是「该链路上到不了」，不是「分支不可达」', () => {
  const gap = G3_IMPLEMENTATION_GAPS.find((item) => item.id === 'G3-GAP-11')!
  // closure_stale 分支存在且有字面量断言；问题是在到达该分支前被 409 拦掉。
  assert.match(gap.title, /该链路上到不了/)
  assert.match(gap.fact ?? '', /存在/)
  assert.match(gap.fact ?? '', /stabilization_conditions\.go/)
  assert.doesNotMatch(gap.title, /closure_stale 分支不可达/)
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

/**
 * 同源守护：前端副本（`g3Gaps.ts`）与仓库内镜像（#246 的镜像）必须逐条相等。
 *
 * 这是本票的核心断言——编号、标题、对应验收行、当前状态、承接票任一项不等即失败，
 * 并把差异逐条打印出来。只比字段、不比数量的话，少一条缺口会被当成「一致」。
 */
test('同源守护：前端缺口副本与 #246 的仓库内镜像逐条相等', () => {
  const mirror = readMirrorGaps()
  assert.equal(
    mirror.length,
    G3_IMPLEMENTATION_GAPS.length,
    `缺口条数不一致：镜像 ${mirror.length} 条，前端副本 ${G3_IMPLEMENTATION_GAPS.length} 条`,
  )
  assert.deepEqual(
    mirror.map((gap) => gap.id).sort(),
    G3_IMPLEMENTATION_GAPS.map((gap) => gap.id).sort(),
    '缺口编号集合不一致',
  )

  const diffs: string[] = []
  for (const gap of G3_IMPLEMENTATION_GAPS) {
    const mirrored = mirror.find((item) => item.id === gap.id)
    if (!mirrored) {
      diffs.push(`${gap.id}：镜像里没有这条缺口`)
      continue
    }
    if (mirrored.title !== gap.title) {
      diffs.push(`${gap.id} 标题不一致：镜像「${mirrored.title}」/ 前端「${gap.title}」`)
    }
    if (JSON.stringify(mirrored.rows) !== JSON.stringify([...gap.rows])) {
      diffs.push(
        `${gap.id} 对应验收行（rows）不一致：镜像 ${JSON.stringify(mirrored.rows)} / ` +
          `前端 ${JSON.stringify([...gap.rows])}`,
      )
    }
    if (JSON.stringify(mirrored.limits) !== JSON.stringify([...gap.limits])) {
      diffs.push(
        `${gap.id} 对应验收行（limits）不一致：镜像 ${JSON.stringify(mirrored.limits)} / ` +
          `前端 ${JSON.stringify([...gap.limits])}`,
      )
    }
    if (mirrored.status !== gap.rowStatus) {
      diffs.push(`${gap.id} 当前状态不一致：镜像「${mirrored.status}」/ 前端「${gap.rowStatus}」`)
    }
    if (mirrored.ticket !== gap.ticket) {
      diffs.push(`${gap.id} 承接票不一致：镜像 #${mirrored.ticket} / 前端 #${gap.ticket}`)
    }
  }
  assert.deepEqual(diffs, [], `前端副本与仓库内镜像不一致：\n${diffs.join('\n')}`)
})

test('承接票存在性守护：每条缺口的承接票都在镜像的「承接票清单」表里', () => {
  const tickets = readMirrorTickets()
  for (const gap of G3_IMPLEMENTATION_GAPS) {
    assert.ok(
      tickets.includes(gap.ticket!),
      `${gap.id} 的承接票 #${gap.ticket} 不在镜像的「承接票清单」表里`,
    )
  }
  // 清单本身必须覆盖本轮拆出的十张票。
  for (const ticket of [249, 250, 251, 252, 253, 254, 255, 256, 257, 258]) {
    assert.ok(tickets.includes(ticket), `镜像的「承接票清单」缺 #${ticket}`)
  }
})

/**
 * 承接票的**更强守护**：镜像的「承接票清单」表必须与代码里的
 * `G3_GAP_TICKET_REGISTRY`（登记表）逐条一致。
 *
 * 为什么不能只断言「票号出现在镜像表里」：那两条事实都来自同一处（镜像表），
 * 是**自证**——票号写错了、重复登记了、漏登记解除条件票了，都发现不了。
 * 这条守护改成三方对照：
 * **镜像表 ↔ 代码登记表 ↔ 缺口条目自身的 `ticket` 字段**，
 * 任何一方写错票号、重复登记、漏登记都会失败。
 *
 * 关于「票两两互不相同」：这条做不到，也不该做——一张票可以承接多条缺口
 * （#250 承接两条路线图缺口、#257 承接两条影响中心缺口），
 * 登记表把「哪张票承接哪几条缺口」显式写出来，守护按这份映射逐条核对。
 *
 * **注意**：登记表只保证「内部自洽、无遗漏、无重复」。
 * 票在 GitHub 上确实存在这一点由 PR 评审与逐票回写承担，不在自动化守护范围内。
 */
test('承接票登记表守护：镜像的承接票清单与代码登记表逐条一致（无重复、无遗漏、无写错）', () => {
  const mirror = readMirrorTickets()
  const registry = G3_GAP_TICKET_REGISTRY.map((entry) => entry.ticket)

  /** 票号 → 出现次数。 */
  const countBy = (tickets: readonly number[]): Map<number, number> => {
    const counts = new Map<number, number>()
    for (const ticket of tickets) counts.set(ticket, (counts.get(ticket) ?? 0) + 1)
    return counts
  }

  // 1. 镜像表里每个票号出现恰好一次（防重复登记）。
  const mirrorCounts = countBy(mirror)
  const duplicatedInMirror = [...mirrorCounts.entries()].filter(([, count]) => count > 1)
  assert.deepEqual(
    duplicatedInMirror.map(([ticket, count]) => `#${ticket} 登记了 ${count} 次`),
    [],
    '镜像的「承接票清单」里有票号被登记了多次',
  )

  // 2. 登记表本身每个票号出现恰好一次。
  const registryCounts = countBy(registry)
  const duplicatedInRegistry = [...registryCounts.entries()].filter(([, count]) => count > 1)
  assert.deepEqual(
    duplicatedInRegistry.map(([ticket, count]) => `#${ticket} 登记了 ${count} 次`),
    [],
    '承接票登记表里有票号被登记了多次',
  )

  // 3. 镜像表与登记表的票号集合完全一致（无遗漏、无多出）。
  const sorted = (tickets: Iterable<number>): number[] => [...tickets].sort((a, b) => a - b)
  assert.deepEqual(
    sorted(mirrorCounts.keys()),
    sorted(registryCounts.keys()),
    '镜像的「承接票清单」与代码登记表的票号集合不一致',
  )

  // 4. 解除条件 / 派生缺陷票必须登记完整（不能只登记子票）。
  for (const ticket of [306, 310, 314, 315]) {
    assert.ok(mirrorCounts.has(ticket), `镜像的「承接票清单」缺解除条件票 #${ticket}`)
    assert.ok(registryCounts.has(ticket), `承接票登记表缺解除条件票 #${ticket}`)
  }

  // 5. 每条缺口的票号必须与登记表里「承接它的那张票」逐条一致，
  //    且落在本轮拆出的子票区间内。
  const subEntries = G3_GAP_TICKET_REGISTRY.filter((entry) => entry.kind === 'sub')
  for (const gap of G3_IMPLEMENTATION_GAPS) {
    const owners = subEntries.filter((entry) => entry.gaps.includes(gap.id))
    assert.equal(owners.length, 1, `${gap.id} 在登记表里被登记了 ${owners.length} 次（应恰好一次）`)
    assert.equal(owners[0].ticket, gap.ticket, `${gap.id} 的承接票与登记表不一致`)
    assert.ok(gap.ticket! >= 249 && gap.ticket! <= 258, `${gap.id} 的承接票号不在 #249–#258 区间内`)
  }

  // 6. 反向核对：登记表登记的每条缺口都真实存在，票号关系也对得上。
  const gapIds = new Set(G3_IMPLEMENTATION_GAPS.map((gap) => gap.id))
  for (const entry of G3_GAP_TICKET_REGISTRY) {
    for (const gapId of entry.gaps) {
      assert.ok(gapIds.has(gapId), `登记表 #${entry.ticket} 登记了不存在的缺口 ${gapId}`)
      const gap = G3_IMPLEMENTATION_GAPS.find((item) => item.id === gapId)!
      if (entry.kind === 'sub') {
        assert.equal(
          gap.ticket,
          entry.ticket,
          `${gapId} 的承接票是 #${gap.ticket}，与登记的 #${entry.ticket} 不一致`,
        )
      } else {
        assert.notEqual(
          gap.ticket,
          entry.ticket,
          `${gapId} 的承接票不应是解除条件票 #${entry.ticket}`,
        )
      }
    }
  }

  // 7. 全部缺口都被登记（没有漏登记的缺口）。
  const registered = new Set(subEntries.flatMap((entry) => [...entry.gaps]))
  for (const gap of G3_IMPLEMENTATION_GAPS) {
    assert.ok(registered.has(gap.id), `${gap.id} 没有出现在承接票登记表里`)
  }
})

/**
 * 矩阵口径一致性守护：缺口的 `rowStatus` 必须等于矩阵里同名行的 `status`。
 *
 * 只比 `rows`（直接缺口行）；`limits`（覆盖受限行）的状态口径不同——
 * 判据已判定、只是某类证据没覆盖到，不按直接缺口行的口径写。
 */
test('矩阵口径一致性：缺口的 rowStatus 等于矩阵同名行的 status', () => {
  const statusOf = new Map(G3_MATRIX_ROWS.map((row) => [row.id, row.status]))
  for (const gap of G3_IMPLEMENTATION_GAPS) {
    for (const row of gap.rows) {
      assert.equal(
        gap.rowStatus,
        statusOf.get(row),
        `${gap.id} 的 rowStatus 与矩阵行 ${row} 的状态不一致`,
      )
    }
  }
})
