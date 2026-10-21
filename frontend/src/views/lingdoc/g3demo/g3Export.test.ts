import assert from 'node:assert/strict'
import test from 'node:test'
import {
  G3_CONCLUSION_SOURCE_LABELS,
  G3_EVIDENCE_DIR,
  G3_EVIDENCE_FILE_PREFIX,
  buildAggregateEvidenceExport,
  buildChainEvidenceExport,
  buildRunEvidenceExport,
  conclusionSourceOf,
  exportFileName,
  exportFilePath,
  falsePassRows,
  resolveRowStatus,
  type G3EvidenceFileInput,
} from './g3Export'
import { G3_MATRIX_ROWS } from './g3Matrix'
import { gapIdsForRow, limitingGapIdsForRow } from './g3Gaps'
import { G3_STATUS_VALUES } from './g3Status'
import type { G3BackendHead, G3RunVerdict, G3StepRecord } from './runner/runContext'

function head(commitId: string): G3BackendHead {
  return { commitId, probedAt: '2026-10-20T00:00:00.000Z', probePath: '/api/v1/version', probeStatus: 401 }
}

function step(name: string, status = 200): G3StepRecord {
  return {
    index: 1,
    name,
    method: 'GET',
    path: '/api/v1/lingdoc/projects/p1',
    status,
    ok: true,
    durationMs: 7,
    conclusion: '成功',
    details: { id: 'p1' },
  }
}

function verdict(id: string, overrides: Partial<G3RunVerdict> = {}): G3RunVerdict {
  return {
    id,
    title: `${id} 判定`,
    holds: true,
    detail: '本轮实跑判定成立。',
    evidence: ['GET /api/v1/lingdoc/projects/p1 → HTTP 200'],
    ...overrides,
  }
}

test('导出体含运行标识、head、每步请求与响应、每条验收行的判定与状态', () => {
  const exportBody = buildRunEvidenceExport({
    runId: 'g3r-t8-1',
    startedAt: '2026-10-20T00:00:00.000Z',
    head: head('7f5fd8c'),
    steps: [step('读上下文修订')],
    verdicts: [verdict('AC-G3-01')],
    stale: false,
    generatedAt: '2026-10-20T00:01:00.000Z',
    dateStamp: '20261020',
  })

  assert.equal(exportBody.run_id, 'g3r-t8-1')
  assert.equal(exportBody.head?.commitId, '7f5fd8c')
  assert.equal(exportBody.steps.length, 1)
  assert.equal(exportBody.steps[0]?.path, '/api/v1/lingdoc/projects/p1')
  assert.equal(exportBody.steps[0]?.status, 200)
  assert.equal(exportBody.ac_rows.length, G3_MATRIX_ROWS.length)
  const ac01 = exportBody.ac_rows.find((row) => row.id === 'AC-G3-01')
  assert.ok(ac01)
  assert.equal(ac01.status, 'PASS')
  assert.equal(ac01.source, 'this_run')
  assert.equal(ac01.source_label, '本次运行得出')
  assert.equal(ac01.verdicts.length, 1)
})

test('未实跑、豁免、缺能力、依赖真实模型的行，状态一律不写 PASS', () => {
  const exportBody = buildRunEvidenceExport({
    runId: 'g3r-t8-2',
    startedAt: '2026-10-20T00:00:00.000Z',
    head: head('7f5fd8c'),
    steps: [],
    verdicts: [verdict('AC-G3-06', { status: 'WARNING', modelDependent: true })],
    stale: false,
    generatedAt: '2026-10-20T00:01:00.000Z',
  })

  assert.deepEqual(falsePassRows(exportBody.ac_rows), [], '导出体里有不该写 PASS 的行')
  for (const row of exportBody.ac_rows) {
    // **没有落证**的行不许写 PASS：矩阵的静态状态只是声明，没有实跑落证的行
    // 写 PASS 就是「把没跑成的写成跑通」。
    if (row.source === 'not_judged') {
      assert.notEqual(row.status, 'PASS', `${row.id} 没有落证，却写了 PASS`)
    }
    if (row.gap_ids.length > 0) {
      assert.notEqual(row.status, 'PASS', `${row.id} 有实现缺口，却写了 PASS`)
    }
  }
  const ac06 = exportBody.ac_rows.find((row) => row.id === 'AC-G3-06')
  assert.equal(ac06?.status, 'WARNING')
  const ac03 = exportBody.ac_rows.find((row) => row.id === 'AC-G3-03')
  // AC-G3-03 本轮已实跑判定成立（注入命中 + 回滚读回完整），行状态按缺口清单
  // #246 的规则保留 follow-up（#246 未关闭前，有实现缺口的行不判 PASS）。
  assert.equal(ac03?.status, 'PASS (follow-up)')
  assert.deepEqual(ac03?.gap_ids, ['G3-GAP-01'])
})

test('只有依赖真实模型的判定时，规则里的 PASS 不得被照抄', () => {
  const row = G3_MATRIX_ROWS.find((item) => item.id === 'AC-G3-01')!
  const modelVerdict = {
    id: 'AC-G3-01',
    title: '模型侧',
    holds: true,
    detail: '模型侧判定',
    evidence: [],
    model_dependent: true,
    run_id: 'g3r-x',
  }
  assert.equal(resolveRowStatus(row, [modelVerdict]), 'WARNING')
})

test('轮次级「本段含模型依赖」不得摊到每条判定上', () => {
  // 一轮里只要**有一条**判定走了真实模型，不等于这一轮里每条判定都模型依赖。
  // 把轮次级标志摊到每条判定上，确定性判定（变更集链那几条）在投影时会被当成
  // 模型侧结论，它们的 `PASS` 被改写成 `WARNING`——那是假的口径。
  const files: G3EvidenceFileInput[] = [
    {
      file: '本次运行.json',
      ticket: 'G3 验收台-8',
      runId: 'g3r-live',
      startedAt: '2026-10-21T00:00:00.000Z',
      head: head('abcdef0'),
      steps: [step('助手-发送轮次')],
      // 这一段**含**模型依赖的结论（助手链走了真实模型）。
      modelDependent: true,
      verdicts: [
        // 变更集链的判定是确定性的：它自己声明了「不是模型依赖」。
        verdict('AC-G3-01', { status: 'PASS', modelDependent: false }),
        // 这一条**没有**声明（链上没设这个字段）：它也必须按确定性判定处理。
        verdict('AC-G3-02', { status: 'PASS' }),
        // 助手链的判定是模型依赖的。
        verdict('AC-G3-14', { status: 'WARNING', modelDependent: true }),
      ],
    },
  ]
  const exportBody = buildAggregateEvidenceExport({
    files,
    generatedAt: '2026-10-21T00:01:00.000Z',
    dateStamp: '20261021',
    runId: 'aggregate-20261021',
  })
  const ac01 = exportBody.ac_rows.find((row) => row.id === 'AC-G3-01')
  assert.equal(ac01?.verdicts[0]?.model_dependent, false, '确定性判定被轮次级标志污染了')
  assert.equal(ac01?.status, 'PASS', '确定性判定的 PASS 被改写成了模型侧的 WARNING')
  // 没声明标志的判定也一样：这份文件里有判定自己声明过，文件级就不摊下来。
  const ac02 = exportBody.ac_rows.find((row) => row.id === 'AC-G3-02')
  assert.equal(ac02?.verdicts[0]?.model_dependent, false, '没声明标志的判定被轮次级标志污染了')
  assert.equal(ac02?.status, 'PASS')
  const ac14 = exportBody.ac_rows.find((row) => row.id === 'AC-G3-14')
  assert.equal(ac14?.verdicts[0]?.model_dependent, true, '模型侧判定必须仍是模型依赖')
  assert.equal(ac14?.status, 'WARNING', '模型侧判定不写 PASS')
  // 轮次级标志本身仍要如实记在那一段上。
  assert.equal(exportBody.runs?.[0]?.model_dependent, true)
})

test('#246 的硬规则：带实现缺口的行，导出投影不得写成 PASS', () => {
  // 判定卡**自带** status 时，投影优先用判定卡的状态（这是「判定成立」的口径）。
  // 但缺口清单 #246 未关闭前，它列出的缺口所对应的验收行**一律不得判 PASS**。
  //
  // 这条护卫生的就是实跑里踩到的那条路：AC-G3-03 的原子性判定成立时自带
  // status=PASS，直接照抄就会把一个仍带 G3-GAP-01 的行写成 PASS。
  const heldVerdict = {
    id: 'AC-G3-03',
    title: '应用原子性',
    holds: true,
    detail: '判定成立',
    evidence: [],
    status: 'PASS' as const,
    model_dependent: false,
    run_id: 'g3r-guard',
  }
  const row = G3_MATRIX_ROWS.find((item) => item.id === 'AC-G3-03')!
  assert.equal(resolveRowStatus(row, [heldVerdict]), 'PASS (follow-up)')

  // 逐行推一遍：带缺口的行不得投影成 PASS。
  let checked = 0
  for (const item of G3_MATRIX_ROWS) {
    const hasGap = gapIdsForRow(item.id).length > 0 || limitingGapIdsForRow(item.id).length > 0
    if (!hasGap) continue
    const status = resolveRowStatus(item, [{ ...heldVerdict, id: item.id }])
    assert.notEqual(
      status,
      'PASS',
      `${item.id} 带实现缺口，导出投影不得写成 PASS（缺口清单 #246 关闭前不判 PASS）`,
    )
    checked += 1
  }
  assert.ok(checked >= 8, `守护测试只覆盖到 ${checked} 行，覆盖面不足`)
})

test('页面与导出体都区分「本次运行得出」与「非本次运行」两类结论', () => {
  const snapshotRow = G3_MATRIX_ROWS.find((row) => row.id === 'AC-G3-10')!
  assert.equal(conclusionSourceOf(snapshotRow, false), 'historical_snapshot')
  assert.equal(conclusionSourceOf(snapshotRow, true), 'this_run')
  assert.equal(G3_CONCLUSION_SOURCE_LABELS.this_run, '本次运行得出')
  assert.equal(G3_CONCLUSION_SOURCE_LABELS.historical_snapshot, '非本次运行 · 历史快照或既有实跑')
  // 切片行的静态状态记着上一次真实运行的判定：它不是「尚未判定」，
  // 只是不是**本次**运行得出的。
  const sliceRow = G3_MATRIX_ROWS.find((row) => row.id === 'G3.1')!
  assert.equal(conclusionSourceOf(sliceRow, false), 'historical_snapshot')
  assert.equal(conclusionSourceOf(sliceRow, true), 'this_run')

  const exportBody = buildRunEvidenceExport({
    runId: 'g3r-t8-3',
    startedAt: '2026-10-20T00:00:00.000Z',
    head: head('7f5fd8c'),
    steps: [],
    stale: false,
    generatedAt: '2026-10-20T00:01:00.000Z',
  })
  const kinds = exportBody.sources.map((source) => source.kind)
  assert.ok(kinds.includes('this_run'))
  assert.ok(kinds.includes('historical_snapshot'))
  const ac10 = exportBody.ac_rows.find((row) => row.id === 'AC-G3-10')
  assert.equal(ac10?.source, 'historical_snapshot')
  assert.equal(ac10?.source_label, '非本次运行 · 历史快照或既有实跑')
  assert.equal(ac10?.decided, false)
})

test('换 head 后旧结论整体标为过期', () => {
  const exportBody = buildRunEvidenceExport({
    runId: 'g3r-t8-4',
    startedAt: '2026-10-20T00:00:00.000Z',
    head: head('abcdef0'),
    steps: [],
    verdicts: [verdict('AC-G3-01')],
    stale: true,
    staleReason: '后端 head 从 7f5fd8c 变为 abcdef0，上一次运行的结论已过期。',
    generatedAt: '2026-10-20T00:01:00.000Z',
  })
  assert.equal(exportBody.head_stale, true)
  assert.match(exportBody.head_stale_reason, /过期/)
  assert.equal(exportBody.ac_rows.every((row) => row.stale), true)
})

test('证据按仓库既有命名落到 08 实施文档目录，不写进 Issue', () => {
  assert.equal(G3_EVIDENCE_DIR, 'docs/08-本轮实施方案')
  assert.ok(exportFileName('20261020').startsWith(G3_EVIDENCE_FILE_PREFIX))
  assert.equal(exportFileName('20261020'), 'G3-验收台-8-整链证据-20261020.json')
  assert.equal(exportFileName('20261020', 'g3r-t8-5'), 'G3-验收台-8-证据-g3r-t8-5.json')
  assert.equal(exportFilePath('20261020'), 'docs/08-本轮实施方案/G3-验收台-8-整链证据-20261020.json')
})

test('汇总导出体把各票证据合并成一份总证据，并带出各自运行标识与 head', () => {
  const files: G3EvidenceFileInput[] = [
    {
      file: 'G3-验收台-3-变更集生命周期证据-20261020.json',
      ticket: 'G3 验收台-3',
      runId: 'g3r-a',
      startedAt: '2026-10-20T00:00:00.000Z',
      head: head('7f5fd8c'),
      steps: [step('读影响评估')],
      verdicts: [verdict('AC-G3-01')],
      modelDependent: false,
    },
    {
      file: 'G3-验收台-7-真实模型验收证据-20261020.json',
      ticket: 'G3 验收台-7',
      runId: 'g3r-b',
      startedAt: '2026-10-20T00:05:00.000Z',
      head: head('7f5fd8c'),
      steps: [step('模型轮次')],
      verdicts: [verdict('AC-G3-06', { status: 'WARNING' })],
      modelDependent: true,
    },
  ]

  const exportBody = buildAggregateEvidenceExport({
    files,
    generatedAt: '2026-10-20T00:10:00.000Z',
    dateStamp: '20261020',
    runId: 'aggregate-20261020',
  })

  assert.equal(exportBody.runs?.length, 2)
  assert.equal(exportBody.steps.length, 2)
  assert.equal(exportBody.evidence_file, 'G3-验收台-8-整链证据-20261020.json')
  assert.equal(exportBody.head_stale, false)
  assert.equal(exportBody.gaps.length, 11)
  const ac01 = exportBody.ac_rows.find((row) => row.id === 'AC-G3-01')
  assert.equal(ac01?.source, 'this_run')
  assert.equal(ac01?.status, 'PASS')
  const ac06 = exportBody.ac_rows.find((row) => row.id === 'AC-G3-06')
  assert.equal(ac06?.status, 'WARNING')
  assert.equal(ac06?.verdicts[0]?.model_dependent, true)
  assert.deepEqual(falsePassRows(exportBody.ac_rows), [])
})

test('汇总的各份证据 head 不一致时整体标为过期', () => {
  const exportBody = buildAggregateEvidenceExport({
    files: [
      {
        file: 'a.json',
        ticket: 'G3 验收台-3',
        runId: 'g3r-a',
        startedAt: '2026-10-20T00:00:00.000Z',
        head: head('7f5fd8c'),
        steps: [],
        verdicts: [],
        modelDependent: false,
      },
      {
        file: 'b.json',
        ticket: 'G3 验收台-7',
        runId: 'g3r-b',
        startedAt: '2026-10-20T00:05:00.000Z',
        head: head('abcdef0'),
        steps: [],
        verdicts: [],
        modelDependent: true,
      },
    ],
    generatedAt: '2026-10-20T00:10:00.000Z',
    dateStamp: '20261020',
  })
  assert.equal(exportBody.head_stale, true)
  assert.match(exportBody.head_stale_reason, /head 不一致/)
})

test('导出体里的状态都取自仓库门禁词表', () => {
  const exportBody = buildRunEvidenceExport({
    runId: 'g3r-t8-6',
    startedAt: '2026-10-20T00:00:00.000Z',
    head: head('7f5fd8c'),
    steps: [],
    stale: false,
    generatedAt: '2026-10-20T00:01:00.000Z',
  })
  for (const row of exportBody.ac_rows) {
    assert.ok((G3_STATUS_VALUES as readonly string[]).includes(row.status), `${row.id} 状态不在词表内`)
  }
})

test('本轮完全判定的行不再挂跟进 Issue；没判定的行保留缺口清单链接', () => {
  const exportBody = buildRunEvidenceExport({
    runId: 'g3r-t8-7',
    startedAt: '2026-10-20T00:00:00.000Z',
    head: head('7f5fd8c'),
    steps: [],
    verdicts: [verdict('AC-G3-01')],
    stale: false,
    generatedAt: '2026-10-20T00:01:00.000Z',
  })
  // AC-G3-01 本轮判定成立：不再挂历史快照那票的跟进 Issue。
  assert.equal(exportBody.ac_rows.find((row) => row.id === 'AC-G3-01')?.follow_up_issue, undefined)
  // AC-G3-03 没实跑（缺能力）：保留缺口清单链接。
  assert.equal(exportBody.ac_rows.find((row) => row.id === 'AC-G3-03')?.follow_up_issue, 246)
})

test('整链导出把多段运行并成一份，并逐段记账', () => {
  const exportBody = buildChainEvidenceExport({
    runs: [
      {
        runId: 'g3r-seg-1',
        startedAt: '2026-10-20T00:00:00.000Z',
        head: head('7f5fd8c'),
        steps: [step('创建变更集')],
        verdicts: [verdict('AC-G3-01')],
        stale: false,
      },
      {
        runId: 'g3r-seg-2',
        startedAt: '2026-10-20T00:05:00.000Z',
        head: head('7f5fd8c'),
        steps: [step('稳定化')],
        verdicts: [verdict('G3.1')],
        stale: false,
      },
    ],
    rows: G3_MATRIX_ROWS.map((row) => (
      row.id === 'AC-G3-01'
        ? { ...row, status: 'PASS' as const }
        : row.id === 'G3.1'
          ? { ...row, status: 'PASS' as const }
          : row
    )),
    evidence: [
      {
        rowId: 'AC-G3-01',
        covered: true,
        decided: true,
        observedSteps: ['创建变更集'],
        missingSteps: [],
        stale: false,
        runId: 'g3r-seg-1',
        note: '本轮实跑判定成立。',
      },
      {
        rowId: 'G3.1',
        covered: true,
        decided: true,
        observedSteps: ['稳定化'],
        missingSteps: [],
        stale: false,
        runId: 'g3r-seg-2',
        note: '本轮实跑判定成立。',
      },
    ],
    generatedAt: '2026-10-20T00:10:00.000Z',
    dateStamp: '20261020',
  })

  assert.equal(exportBody.runs?.length, 2)
  assert.equal(exportBody.steps.length, 2)
  assert.equal(exportBody.evidence_file, 'G3-验收台-8-整链证据-20261020.json')
  assert.equal(exportBody.head_stale, false)
  // 两段的判定各自绑到自己的运行上，不混读。
  const ac01 = exportBody.ac_rows.find((row) => row.id === 'AC-G3-01')
  assert.equal(ac01?.status, 'PASS')
  assert.equal(ac01?.run_id, 'g3r-seg-1')
  assert.equal(ac01?.verdicts.length, 1)
  assert.equal(ac01?.verdicts[0]?.run_id, 'g3r-seg-1')
  const g31 = exportBody.ac_rows.find((row) => row.id === 'G3.1')
  assert.equal(g31?.status, 'PASS')
  assert.equal(g31?.run_id, 'g3r-seg-2')
  assert.deepEqual(falsePassRows(exportBody.ac_rows), [])
})

test('整链导出：各段 head 不一致时整体标为过期', () => {
  const exportBody = buildChainEvidenceExport({
    runs: [
      {
        runId: 'g3r-a',
        startedAt: '2026-10-20T00:00:00.000Z',
        head: head('7f5fd8c'),
        steps: [],
        verdicts: [],
        stale: false,
      },
      {
        runId: 'g3r-b',
        startedAt: '2026-10-20T00:05:00.000Z',
        head: head('abcdef0'),
        steps: [],
        verdicts: [],
        stale: false,
      },
    ],
    generatedAt: '2026-10-20T00:10:00.000Z',
    dateStamp: '20261020',
  })
  assert.equal(exportBody.head_stale, true)
  assert.match(exportBody.head_stale_reason, /head 不一致/)
  assert.equal(exportBody.ac_rows.every((row) => row.stale), true)
})

test('整链导出：判定不成立的行不会被写成 PASS', () => {
  const exportBody = buildChainEvidenceExport({
    runs: [
      {
        runId: 'g3r-x',
        startedAt: '2026-10-20T00:00:00.000Z',
        head: head('7f5fd8c'),
        steps: [step('创建变更集')],
        verdicts: [verdict('AC-G3-01', { holds: false })],
        stale: false,
      },
    ],
    rows: G3_MATRIX_ROWS.map((row) => (
      row.id === 'AC-G3-01' ? { ...row, status: 'PASS' as const } : row
    )),
    evidence: [
      {
        rowId: 'AC-G3-01',
        covered: true,
        decided: true,
        observedSteps: ['创建变更集'],
        missingSteps: [],
        stale: false,
        runId: 'g3r-x',
        note: '本轮实跑判定成立。',
      },
    ],
    generatedAt: '2026-10-20T00:10:00.000Z',
    dateStamp: '20261020',
  })
  const ac01 = exportBody.ac_rows.find((row) => row.id === 'AC-G3-01')
  assert.notEqual(ac01?.status, 'PASS', '判定不成立却被写成 PASS')
  // 兜底把状态退回矩阵原状态，并在说明里写明原因。
  assert.equal(ac01?.status, 'PASS (follow-up)')
  assert.match(ac01?.note ?? '', /导出兜底/)
  assert.deepEqual(falsePassRows(exportBody.ac_rows), [], '导出体里还有不该写 PASS 的行')
})
