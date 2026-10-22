import assert from 'node:assert/strict'
import test from 'node:test'
import {
  G3_ROW_EVIDENCE_RULES,
  G3_RUN_HISTORY_STORAGE_KEY,
  G3_RUN_SUMMARY_STORAGE_KEY,
  appendRunHistory,
  applyRunEvidence,
  contextsFromSummaries,
  headChangedSince,
  loadRunHistory,
  loadRunSummary,
  rowEvidence,
  saveRunHistory,
  saveRunSummary,
  summarizeRun,
  type G3RunStorage,
} from './rowEvidenceRules'
import { G3_MATRIX_ROWS } from './impactMatrix'
import { G3_STATUS_VALUES } from './taskStatus'
import { gapIdsForRow, limitingGapIdsForRow } from './implementationGaps'
import { G3_BLOCKED_STEPS } from './chains/blockedChain'
import { G3_ASSISTANT_STEPS } from './chains/assistantChain'
import { G3_CS_STEPS } from './chains/changeSetChain'
import { G3_DEP_STEPS } from './chains/dependencyChain'
import { G3_FAULT_STEPS } from './chains/faultInjection'
import { G3_MODEL_STEPS } from './chains/modelChain'
import { G3_ROADMAP_STEPS } from './chains/roadmapChain'
import { G3_STAB_STEPS } from './chains/stabilizationChain'
import { createRunContext, markRunStale, recordStep, type G3BackendHead, type G3RunContext, type G3RunVerdict } from './chains/runContext'

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
  const ctx = createRunContext({ runId: 'g3r-ev-1', accountEmail: 'impactCenterAcceptance-x@example.com' })
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
  // 前置链不构成任何判据的证据：每一行的状态都**原样保留**。
  // （切片行的静态状态记的是「当前 head 实跑得到（或最后已知）的值」，
  // 投影只在本轮判定成立时升格，前置链跑不到判定卡，所以状态不动。）
  const before = new Map(G3_MATRIX_ROWS.map((row) => [row.id, row.status]))
  for (const row of rows) {
    assert.equal(row.status, before.get(row.id), `${row.id} 的状态被前置链改动了`)
  }
  assert.equal(
    rows.filter((row) => row.status === 'TODO').length,
    0,
    '切片七行都已有实跑判定，不该再有 TODO',
  )
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
      'AC-G3-01', 'AC-G3-02', 'AC-G3-03',
      'AC-G3-04', 'AC-G3-05', 'AC-G3-06',
      'AC-G3-07', 'AC-G3-08', 'AC-G3-09',
      'AC-G3-11', 'AC-G3-12',
      'AC-G3-13', 'AC-G3-14', 'AC-G3-15', 'AC-G3-16',
      'G3.1',     'G3.2',     'G3.3',     'G3.4',
      'G3.5',     'G3.6',     'G3.7',
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
  // AC-G3-14 / AC-G3-15：助手两组说明与候选批次链**已实现并已接入台面**，
  // 本轮实跑也判定成立了。AC-G3-14 的结论**依赖真实模型**（非确定性），
  // 按仓库口径记 WARNING，不写 PASS；AC-G3-15 是确定性判定，但带缺口，
  // 按 #246 的规则记 PASS (follow-up)。
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-14'].status, 'WARNING')
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-15'].status, 'PASS (follow-up)')
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-14'].followUpIssue, 246)
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-15'].followUpIssue, 246)
  // AC-G3-14 / AC-G3-15 的必需步骤必须挂在**助手链 / 候选批次链**上：
  // 规则表若还停在旧步骤名，这两行就永远命中不了台面判定。
  assert.ok(
    G3_ROW_EVIDENCE_RULES['AC-G3-14'].requires.includes(G3_ASSISTANT_STEPS.readChangeSet),
    'AC-G3-14 的必需步骤没有接上助手链',
  )
  assert.deepEqual(
    [...G3_ROW_EVIDENCE_RULES['AC-G3-15'].requires],
    [
      G3_ASSISTANT_STEPS.submitCandidates,
      G3_ASSISTANT_STEPS.decideConfirm,
      G3_ASSISTANT_STEPS.decideModify,
      G3_ASSISTANT_STEPS.decideReject,
      G3_ASSISTANT_STEPS.resubmitRejected,
      G3_ASSISTANT_STEPS.confirmStale,
    ],
    'AC-G3-15 的必需步骤没有接上候选批次链',
  )
  // AC-G3-08 的判据要求「读版本序列**与前后比较**」：缺比较证据就不该判成立。
  assert.ok(
    G3_ROW_EVIDENCE_RULES['AC-G3-08'].requires.includes(G3_ROADMAP_STEPS.compareVersions),
    'AC-G3-08 的必需步骤缺了前后比较',
  )
  // 状态口径散在矩阵 / 缺口清单 / 规则表 / 镜像 / 报告五处，容易各说各话。
  // **矩阵的静态状态**是「还没被实跑升格」的保守值，**规则表的 status** 是「判定成立
  // 时这一行取什么状态」，两者本就可以不同（无缺口的行：矩阵 PASS (follow-up)、
  // 规则表 PASS，实跑命中后行才升到 PASS）。
  //
  // 真正要守的是 #246 的硬规则：**本票所列缺口对应的验收行，在 #246 关闭前不得判 PASS。**
  // 于是「有实现缺口的行，规则表不得写 PASS」——否则实跑命中时，矩阵行会被投影成
  // 规则表的 status，直接违反那条规则（这正是本轮评审抓到的真缺陷）。
  for (const [id, rule] of Object.entries(G3_ROW_EVIDENCE_RULES)) {
    const hasGap =
      gapIdsForRow(id).length > 0 || limitingGapIdsForRow(id).length > 0
    if (hasGap) {
      assert.notEqual(
        rule.status,
        'PASS',
        `${id} 有实现缺口，规则表不得写 PASS（缺口清单 #246 关闭前不判 PASS）`,
      )
    }
  }
  // 真实模型路径的结论是非确定性的：状态一律 WARNING，并保留缺口清单 #246。
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-06'].status, 'WARNING')
  for (const id of ['AC-G3-14', 'AC-G3-15']) {
    const alternative = G3_ROW_EVIDENCE_RULES[id].alternatives?.[0]
    assert.equal(alternative?.status, 'WARNING')
    assert.equal(alternative?.followUpIssue, 246)
  }
  // 本票新增的两条切片判据：G3.1（ChangeSet 切片）是完全判定；
  // G3.6（稳定化切片）受环境限制（G3-GAP-08：调研类闭环证据本机不可实跑），
  // 按缺口清单的要求只能记环境限制，不得用其他回执冒充。
  assert.equal(G3_ROW_EVIDENCE_RULES['G3.1'].status, 'PASS')
  assert.equal(G3_ROW_EVIDENCE_RULES['G3.6'].status, 'WARNING')
  assert.equal(G3_ROW_EVIDENCE_RULES['G3.6'].followUpIssue, 246)
  // 七条切片行的规则表条目都挂判定卡：没有判定卡就只剩「步骤跑到了」，
  // 而「步骤跑到了」不等于「判据成立」——这是防假 PASS 的结构性守护。
  for (const id of ['G3.1', 'G3.2', 'G3.3', 'G3.4', 'G3.5', 'G3.6', 'G3.7']) {
    const rule = G3_ROW_EVIDENCE_RULES[id]
    assert.ok(rule, `${id} 没有规则表条目`)
    assert.equal(rule.verdictId, id, `${id} 的规则表必须挂自己的判定卡`)
    assert.ok(rule.requires.length > 0, `${id} 没有声明必需步骤`)
  }
  // G3.2 / G3.3 / G3.4：依赖图、影响解析、任务协调三段本轮实跑判定成立，
  // 这三行没有 #246 缺口，可以判 PASS。
  assert.equal(G3_ROW_EVIDENCE_RULES['G3.2'].status, 'PASS')
  assert.equal(G3_ROW_EVIDENCE_RULES['G3.3'].status, 'PASS')
  assert.equal(G3_ROW_EVIDENCE_RULES['G3.4'].status, 'PASS')
  // G3.5：路线图切片只成立一部分（「必需项已声明但缺失」那一种前置本轮没有落点），
  // 按仓库口径记覆盖受限的 WARNING，不写 PASS。
  assert.equal(G3_ROW_EVIDENCE_RULES['G3.5'].status, 'WARNING')
  // G3.7：影响中心是 #246 的缺口行（G3-GAP-06 / G3-GAP-07），
  // 缺口清单关闭前不判 PASS，记 PASS (follow-up) 并保留跟进 Issue。
  assert.equal(G3_ROW_EVIDENCE_RULES['G3.7'].status, 'PASS (follow-up)')
  assert.equal(G3_ROW_EVIDENCE_RULES['G3.7'].followUpIssue, 246)
  // 七条切片行的**规则表状态必须与矩阵行状态一致**：
  // 两处是手工同步的，漂移会让页面同时挂着两个说法。
  // （切片行与 AC 行的差别只在于「静态状态记的是最后一次实跑判定的结论」，
  //  但两处的**状态取值**仍必须一样。）
  for (const row of G3_MATRIX_ROWS.filter((item) => item.group === 'slice')) {
    const rule = G3_ROW_EVIDENCE_RULES[row.id]
    assert.ok(rule, `${row.id} 没有规则表条目`)
    assert.equal(
      rule.status,
      row.status,
      `${row.id} 的规则表状态与矩阵行状态不一致（两处是手工同步的）`,
    )
  }
  // 本票新增的三条领域判据：AC-G3-12 只覆盖了实跑得出的那半段（#246 G3-GAP-11）；
  // AC-G3-09 与 AC-G3-07 同样只覆盖实跑得出的那半段。
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-12'].status, 'PASS (follow-up)')
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-12'].followUpIssue, 246)
  // AC-G3-04 的判据是合取式，后半段（未受影响理由）本机后端给不出，
  // 因此只能部分判定（#246 G3-GAP-06 / G3-GAP-07）。
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-04'].status, 'PASS (follow-up)')
  assert.equal(G3_ROW_EVIDENCE_RULES['AC-G3-04'].followUpIssue, 246)
  // **任何**能判成 PASS 的规则都必须挂判定卡：没有判定卡就只剩「步骤跑到了」，
  // 而「步骤跑到了」不等于「判据成立」——这是防假 PASS 的结构性守护。
  for (const [id, rule] of Object.entries(G3_ROW_EVIDENCE_RULES)) {
    const canPass = rule.status === 'PASS' || (rule.alternatives ?? []).some((item) => item.status === 'PASS')
    if (!canPass) continue
    const hasVerdict = Boolean(rule.verdictId) || (rule.alternatives ?? []).some((item) => Boolean(item.verdictId))
    assert.ok(hasVerdict, `${id} 能判成 PASS 却没有判定卡：步骤跑到了就会被推成 PASS`)
  }
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
    ...Object.values(G3_ROADMAP_STEPS),
    ...Object.values(G3_FAULT_STEPS),
    ...Object.values(G3_ASSISTANT_STEPS),
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
  ctx.verdicts = ['AC-G3-01', 'AC-G3-02', 'AC-G3-04', 'AC-G3-11', 'AC-G3-05', 'AC-G3-13', 'AC-G3-14', 'AC-G3-15'].map((id) => ({
    id,
    title: id,
    holds: true,
    detail: '成立',
    evidence: [],
  }))
  const { rows } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  const byId = new Map(rows.map((row) => [row.id, row]))
  for (const id of ['AC-G3-01', 'AC-G3-02', 'AC-G3-11', 'AC-G3-05', 'AC-G3-13']) {
    assert.equal(byId.get(id)?.status, 'PASS', `${id} 没有被本轮证据驱动`)
    assert.equal(byId.get(id)?.followUpIssue, undefined, `${id} 被判定后不应再挂「跟进 Issue」`)
  }
  // AC-G3-04 的判据有一半未成立（服务端给不出未受影响理由），只能部分判定。
  assert.equal(byId.get('AC-G3-04')?.status, 'PASS (follow-up)')
  assert.equal(byId.get('AC-G3-04')?.followUpIssue, 246)
  // AC-G3-14 / AC-G3-15 的必需步骤挂在**助手链 / 候选批次链**上；
  // 本轮只跑了变更集链与依赖链，所以这两行**未被本轮覆盖**，状态原样保留。
  const baseRows = new Map(G3_MATRIX_ROWS.map((row) => [row.id, row]))
  for (const id of ['AC-G3-14', 'AC-G3-15']) {
    assert.equal(byId.get(id)?.status, baseRows.get(id)?.status, `${id} 本轮未覆盖，状态不应被改动`)
    assert.equal(
      byId.get(id)?.followUpIssue,
      baseRows.get(id)?.followUpIssue,
      `${id} 本轮未覆盖，跟进 Issue 不应被改动`,
    )
  }
  // 规则表之外的行状态原样保留。
  // AC-G3-03 是**例外**：它在本轮已实跑判定成立（注入命中 + 回滚读回完整），
  // 但行状态按缺口清单 #246 的规则保留 follow-up（#246 未关闭前不判 PASS）。
  assert.equal(byId.get('AC-G3-03')?.status, 'PASS (follow-up)')
  // G3.1 的必需步骤挂在**稳定化链**上：本轮只跑了变更集链与依赖链，
  // 所以它未被本轮覆盖，状态原样保留（静态状态记的是上一次实跑判定的结论）。
  assert.equal(byId.get('G3.1')?.status, baseRows.get('G3.1')?.status)
  assert.equal(byId.get('AC-G3-10')?.followUpIssue, 239, '没被本轮判定的行要保留跟进 Issue')
})

test('判定卡不成立时，即使步骤全部跑齐也不许升格成 PASS', () => {
  const ctx = prerequisiteRun()
  for (const name of [...Object.values(G3_CS_STEPS), ...Object.values(G3_DEP_STEPS)]) {
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
  // 步骤都跑到了，但判定卡明确不成立 —— 这一行**不得**被判成 PASS。
  ctx.verdicts = ['AC-G3-01', 'AC-G3-05'].map((id) => ({
    id,
    title: id,
    holds: false,
    detail: '不成立',
    evidence: [],
  }))
  const before = new Map(G3_MATRIX_ROWS.map((row) => [row.id, row.status]))
  const { rows, evidence } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  const byId = new Map(rows.map((row) => [row.id, row]))
  for (const id of ['AC-G3-01', 'AC-G3-05']) {
    assert.equal(byId.get(id)?.status, before.get(id), `${id} 在判定不成立时被升格了`)
  }
  const notes = new Map(evidence.map((item) => [item.rowId, item.note]))
  assert.match(notes.get('AC-G3-01') ?? '', /判定卡/, '判定不成立时说明要写明判定卡')
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
  // G3.6 受环境限制（G3-GAP-08）：只能记 WARNING，不得用其他回执冒充 PASS。
  assert.equal(byId.get('G3.6')?.status, 'WARNING', 'G3.6 应当记环境限制 WARNING')
  assert.equal(byId.get('G3.6')?.followUpIssue, 246)
})

test('判定卡不成立但自带状态时（生产同形），该行取判定卡自带的状态', () => {
  // 生产同形的夹具：这张卡**确实自带 `status`**，而且是**真的**自带——
  // `chains/dependencyChain.ts` 的 `G3.2`（判定卡 `verdictG32`）写的是
  // `status: holds ? 'PASS' : 'NOT RUN'`（同文件 `AC-G3-15` 的 `verdictAcG315`
  // 同一形状，`G3.7` 的 `verdictG37` 写的是 `holds ? 'PASS (follow-up)' : 'NOT RUN'`）。
  //
  // **不要**拿 `G3.1` / `G3.6` 来做这个夹具：它们出自
  // `chains/stabilizationChain.ts`（`verdictG31` / `verdictG36`），**那两张卡不写
  // `status`**——生产里它们失败时走的是「不成立，状态保持原样」分支。
  const ctx = prerequisiteRun()
  for (const name of [...Object.values(G3_CS_STEPS), ...Object.values(G3_DEP_STEPS)]) {
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
  ctx.verdicts = [
    {
      id: 'G3.2',
      title: 'G3.2',
      holds: false,
      detail: '不成立：影响可追溯到来源边那一半没跑出来。',
      evidence: [],
      status: 'NOT RUN',
    },
  ]
  const { rows, evidence } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  const byId = new Map(rows.map((row) => [row.id, row]))
  // 判定卡自带状态 ⇒ 这一行取判定卡自带的状态（不是矩阵原状态，也不是 PASS）。
  assert.equal(byId.get('G3.2')?.status, 'NOT RUN')
  // 证据说明必须写明「只部分成立（状态记为 X）」——
  // 否则规则表的说明（「本轮实跑判定成立：……」）会被挂在一张**不成立**的判定卡上。
  const notes = new Map(evidence.map((item) => [item.rowId, item.note]))
  assert.match(notes.get('G3.2') ?? '', /只部分成立/, 'G3.2 的说明没有写明判定卡只部分成立')
  assert.match(notes.get('G3.2') ?? '', /状态记为 NOT RUN/, 'G3.2 的说明没有写明判定卡自带的状态')
})

test('判定卡不成立且没有状态时（生产同形），该行状态保持原样', () => {
  // 生产同形的夹具：这张卡**不带 `status`**——`G3.1` / `G3.6` 出自
  // `chains/stabilizationChain.ts`（`verdictG31` / `verdictG36`），两张卡都**不写
  // `status`**。生产里它们失败时走的就是「不成立，状态保持原样」这条分支。
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
  const { rows, evidence } = applyRunEvidence(G3_MATRIX_ROWS, ctx)
  const byId = new Map(rows.map((row) => [row.id, row]))
  // 判定不成立时，投影**不改动**矩阵里的静态状态：切片行的静态状态记的是
  // 上一次实跑判定的结论，不是一次失败就抹掉。
  const before = new Map(G3_MATRIX_ROWS.map((row) => [row.id, row.status]))
  assert.equal(byId.get('G3.1')?.status, before.get('G3.1'))
  assert.equal(byId.get('G3.6')?.status, before.get('G3.6'))
  // 但**证据说明里必须写明判定卡不成立**——状态不变不等于这一轮判定成立。
  const notes = new Map(evidence.map((item) => [item.rowId, item.note]))
  for (const id of ['G3.1', 'G3.6']) {
    assert.match(notes.get(id) ?? '', /判定卡.*不成立/, `${id} 的说明没有写明判定卡不成立`)
    assert.match(notes.get(id) ?? '', /状态保持原样/, `${id} 的说明没有写明状态保持原样`)
  }
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
  // AC-G3-12 只覆盖了实跑得出的那半段（#246 G3-GAP-11：closure_stale 分支不可达）。
  assert.equal(byId.get('AC-G3-12')?.status, 'PASS (follow-up)')
  assert.equal(byId.get('AC-G3-12')?.followUpIssue, 246)
  // 部分覆盖的两行：状态是 PASS (follow-up)，并保留指向缺口清单的跟进 Issue。
  assert.equal(byId.get('AC-G3-09')?.status, 'PASS (follow-up)')
  assert.equal(byId.get('AC-G3-09')?.followUpIssue, 246)
  assert.equal(byId.get('AC-G3-07')?.status, 'PASS (follow-up)')
  assert.equal(byId.get('AC-G3-07')?.followUpIssue, 243)
})

test('「结论过期」「还缺步骤」优先于「只部分成立」：说明不得被盖掉', () => {
  // ① 过期运行：判定卡不成立且自带 status（`chains/dependencyChain.ts` 的卡形状）。
  //    说明必须写「结论过期」——「只部分成立」会盖掉过期这个信号。
  const staleCtx = prerequisiteRun()
  for (const name of Object.values(G3_STAB_STEPS)) {
    recordStep(staleCtx, {
      name,
      method: 'POST',
      path: '/api/v1/lingdoc/projects/p1/stabilize',
      status: 200,
      ok: true,
      durationMs: 5,
      conclusion: '成功',
    })
  }
  staleCtx.verdicts = [
    { id: 'G3.1', title: 'G3.1', holds: false, detail: '不成立', evidence: [], status: 'NOT RUN' },
  ]
  markRunStale(staleCtx, '后端 head 从 7f5fd8c 变为 af18497')
  const staleEvidence = applyRunEvidence(G3_MATRIX_ROWS, staleCtx).evidence
  const staleNote = staleEvidence.find((item) => item.rowId === 'G3.1')?.note ?? ''
  assert.match(staleNote, /结论过期/)
  assert.doesNotMatch(staleNote, /只部分成立/)

  // ② 还缺步骤：同一张自带 status 的失败卡，但这一行所需步骤没跑齐。
  //    说明必须写「还缺步骤」。
  const partialCtx = prerequisiteRun()
  recordStep(partialCtx, {
    name: G3_DEP_STEPS.createEdge,
    method: 'POST',
    path: '/api/v1/lingdoc/projects/p1',
    status: 200,
    ok: true,
    durationMs: 5,
    conclusion: '成功',
  })
  partialCtx.verdicts = [
    { id: 'G3.2', title: 'G3.2', holds: false, detail: '不成立', evidence: [], status: 'NOT RUN' },
  ]
  const partialEvidence = applyRunEvidence(G3_MATRIX_ROWS, partialCtx).evidence
  const partialNote = partialEvidence.find((item) => item.rowId === 'G3.2')?.note ?? ''
  assert.match(partialNote, /还缺步骤/)
  assert.doesNotMatch(partialNote, /只部分成立/)
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
  // 其余行状态原样保留（AC-G3-03 已实跑判定成立，按 #246 规则保留 follow-up）。
  assert.equal(byId.get('AC-G3-01')?.status, 'PASS (follow-up)')
  assert.equal(byId.get('AC-G3-03')?.status, 'PASS (follow-up)')
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

/**
 * 造一个只跑了某一条链的上下文。
 *
 * 页面上有几段链，各跑在自己的隔离项目上。跨段累计时，
 * **每一段只带自己那几步与自己那几个判定**。
 */
function segmentRun(
  runId: string,
  stepNames: readonly string[],
  verdictIds: readonly string[],
  holds = true,
): G3RunContext {
  const ctx = createRunContext({ runId, accountEmail: 'impactCenterAcceptance-x@example.com' })
  for (const name of stepNames) {
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
  ctx.verdicts = verdictIds.map((id) => ({
    id,
    title: id,
    holds,
    detail: `${id} 判定`,
    evidence: [],
  }))
  return ctx
}

test('跨段累计：后一段不抹掉前一段的判定', () => {
  const changeset = segmentRun('g3r-seg-1', Object.values(G3_CS_STEPS), ['AC-G3-01', 'AC-G3-02', 'AC-G3-04', 'AC-G3-11'])
  const stabilization = segmentRun('g3r-seg-2', Object.values(G3_STAB_STEPS), ['G3.1', 'G3.6'])
  const { rows, evidence } = applyRunEvidence(G3_MATRIX_ROWS, [changeset, stabilization])
  const byId = new Map(rows.map((row) => [row.id, row]))
  // 变更集链判的行仍判着，不会被后一段的稳定化链覆盖掉。
  assert.equal(byId.get('AC-G3-01')?.status, 'PASS')
  assert.equal(byId.get('AC-G3-11')?.status, 'PASS')
  // 稳定化链判的行同样在。
  assert.equal(byId.get('G3.1')?.status, 'PASS')
  assert.equal(byId.get('G3.6')?.status, 'WARNING')
  // 每行都写清是哪一次运行判的。
  const byEvidence = new Map(evidence.map((item) => [item.rowId, item]))
  assert.equal(byEvidence.get('AC-G3-01')?.runId, 'g3r-seg-1')
  assert.equal(byEvidence.get('G3.1')?.runId, 'g3r-seg-2')
  assert.match(byEvidence.get('AC-G3-01')?.note ?? '', /g3r-seg-1/)
})

test('跨段累计不串味：A 段的步骤不能配 B 段的判定', () => {
  // A 段只跑到步骤、没有判定卡；B 段有判定卡、但没跑到步骤。
  const stepsOnly = segmentRun('g3r-mix-a', Object.values(G3_CS_STEPS), [])
  const verdictOnly = segmentRun('g3r-mix-b', [], ['AC-G3-01'])
  const { rows, evidence } = applyRunEvidence(G3_MATRIX_ROWS, [stepsOnly, verdictOnly])
  const byId = new Map(rows.map((row) => [row.id, row]))
  // 步骤与判定分属两次运行 ⇒ 不得判成 PASS。
  assert.equal(byId.get('AC-G3-01')?.status, 'PASS (follow-up)', '跨运行的步骤与判定被拼在了一起')
  const item = evidence.find((entry) => entry.rowId === 'AC-G3-01')
  assert.equal(item?.decided, false)
})

test('跨段累计：确定性判定优先于模型侧判定', () => {
  // AC-G3-14 的确定性路径要求「创建变更草稿 + 读回草稿上的两组说明」两步。
  const deterministic = segmentRun('g3r-det', ['创建变更草稿', G3_ASSISTANT_STEPS.readChangeSet], ['AC-G3-14'])
  // 判定卡自带状态时，投影取**判定自己的**状态（规则表主路径的状态是兜底）。
  deterministic.verdicts = [
    { id: 'AC-G3-14', title: 'AC-G3-14', holds: true, detail: '确定性路径', evidence: [], status: 'PASS (follow-up)' },
  ]
  const model = segmentRun('g3r-model', ['模型-助手草稿-结论'], ['AC-G3-14'])
  model.verdicts = [{ id: 'AC-G3-14', title: 'AC-G3-14', holds: true, detail: '模型侧', evidence: [], status: 'WARNING', modelDependent: true }]
  const { rows, evidence } = applyRunEvidence(G3_MATRIX_ROWS, [deterministic, model])
  const byId = new Map(rows.map((row) => [row.id, row]))
  // 确定性路径判成立：取确定性路径的状态，不取模型侧的 WARNING。
  assert.equal(byId.get('AC-G3-14')?.status, 'PASS (follow-up)')
  assert.notEqual(byId.get('AC-G3-14')?.status, 'WARNING', '模型侧的 WARNING 不该盖过确定性判定')
  const item = evidence.find((entry) => entry.rowId === 'AC-G3-14')
  assert.equal(item?.runId, 'g3r-det')
})

test('累计清单：同一运行只留一份，超上限丢最旧的', () => {
  const storage = memoryStorage()
  const first = summarizeRun(segmentRun('g3r-h-1', [], []), { missingSections: [] })
  const second = summarizeRun(segmentRun('g3r-h-2', [], []), { missingSections: [] })
  let history = appendRunHistory([], first)
  history = appendRunHistory(history, second)
  assert.equal(history.length, 2)
  // 同一运行标识重放：不新增一份。
  history = appendRunHistory(history, first)
  assert.equal(history.length, 2, '同一运行标识被并了两份')
  assert.equal(history[history.length - 1]?.runId, 'g3r-h-1')
  saveRunHistory(storage, history)
  const loaded = loadRunHistory(storage)
  assert.equal(loaded.length, 2)
  assert.deepEqual(loaded.map((item) => item.runId), ['g3r-h-2', 'g3r-h-1'])
  // 上下文还原：矩阵投影读的就是这一份清单。
  assert.equal(contextsFromSummaries(loaded).length, 2)
})

test('累计清单坏掉时读成空数组，不静默当成有历史', () => {
  const storage = memoryStorage()
  storage.setItem(G3_RUN_HISTORY_STORAGE_KEY, '{ broken')
  assert.deepEqual(loadRunHistory(storage), [])
  storage.setItem(G3_RUN_HISTORY_STORAGE_KEY, JSON.stringify({ runId: 'x' }))
  assert.deepEqual(loadRunHistory(storage), [])
})

test('#246 的硬规则：带实现缺口的行，页面矩阵投影不得写成 PASS', () => {
  // 缺口清单 #246 未关闭前，**它列出的缺口所对应的验收行一律不得判 PASS**
  // （docs/agents/development-rules.md）。
  //
  // 这条规则必须在**投影处**兜住：判定卡自己会带 status，判定成立时常常就是
  // PASS。只改静态声明不够——投影会把判定卡的状态盖回矩阵行。
  // 这里用「一个判定成立、且自带 PASS 的判定卡」把每一行都推一遍：
  // 带缺口的行必须被降级成 PASS (follow-up)，无缺口的行才允许 PASS。
  const head: G3BackendHead = {
    commitId: 'guard0000',
    probedAt: '2026-10-21T00:00:00.000Z',
    probePath: '/api/v1/version',
    probeStatus: 401,
  }
  let checked = 0
  for (const row of G3_MATRIX_ROWS) {
    const rule = G3_ROW_EVIDENCE_RULES[row.id]
    if (!rule?.verdictId) continue
    const hasGap = gapIdsForRow(row.id).length > 0 || limitingGapIdsForRow(row.id).length > 0
    const ctx: G3RunContext = {
      runId: 'g3r-guard',
      startedAt: '2026-10-21T00:00:00.000Z',
      accountEmail: 'guard@example.com',
      steps: rule.requires.map((name, index) => ({
        index: index + 1,
        name,
        method: 'GET',
        path: '/api/v1/lingdoc/guard',
        status: 200,
        ok: true,
        durationMs: 1,
        conclusion: '守护测试用的假步骤',
      })),
      head,
      chapters: [],
      stale: false,
      verdicts: [
        {
          id: rule.verdictId,
          title: '守护测试用的判定卡',
          holds: true,
          detail: '守护测试用的判定卡',
          evidence: [],
          status: 'PASS',
        },
      ],
    }
    const projected = applyRunEvidence(G3_MATRIX_ROWS, ctx).rows.find((item) => item.id === row.id)
    assert.ok(projected, `${row.id} 没有投影出矩阵行`)
    if (hasGap) {
      assert.notEqual(
        projected.status,
        'PASS',
        `${row.id} 带实现缺口，页面投影不得写成 PASS（缺口清单 #246 关闭前不判 PASS）`,
      )
    }
    checked += 1
  }
  assert.ok(checked >= 10, `守护测试只覆盖到 ${checked} 行，覆盖面不足`)
})

test('#246 的硬规则：带缺口的 AC 行，规则表状态必须与矩阵行一致，且不得判 PASS', () => {
  // 两处状态是**手工同步**的：`impactMatrix.ts` 的静态声明（这一行现在是什么状态）
  // 与 `rowEvidenceRules.ts` 的规则表（这一行被本轮判定之后取什么状态）。
  // 同步一旦漂移，页面就会同时挂着两个说法：「声明的口径」与「跑出来的口径」。
  //
  // 口径只覆盖**带 #246 缺口的 AC 行**，理由写在这里，避免以后有人扩大或缩小它：
  // - 切片行（G3.x）的静态状态是「未判定」（TODO / NOT RUN），它与规则表的
  //   「判定成立后取什么状态」本就是两件事，不在同一条口径上；
  // - 不带缺口的行（例如 followUpIssue 指向 #239 的行）由**别的** Issue 决定
  //   它的 follow-up 形态，规则表写的是「判定成立后取什么状态」，两表不必相等。
  let checked = 0
  for (const row of G3_MATRIX_ROWS) {
    if (row.group !== 'ac') continue
    const rule = G3_ROW_EVIDENCE_RULES[row.id]
    if (!rule) continue
    const hasGap = gapIdsForRow(row.id).length > 0 || limitingGapIdsForRow(row.id).length > 0
    if (!hasGap) continue
    assert.equal(
      rule.status,
      row.status,
      `${row.id} 带实现缺口：规则表状态必须与矩阵行一致（两处状态是手工同步的）`,
    )
    assert.notEqual(
      rule.status,
      'PASS',
      `${row.id} 带实现缺口：缺口清单 #246 关闭前不得判 PASS`,
    )
    checked += 1
  }
  assert.ok(checked >= 8, `守护测试只覆盖到 ${checked} 行，覆盖面不足`)
})
