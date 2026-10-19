import assert from 'node:assert/strict'
import test from 'node:test'
import {
  G3_CONCLUSION_SOURCE_LABELS,
  G3_EVIDENCE_DIR,
  G3_EVIDENCE_FILE_PREFIX,
  buildAggregateEvidenceExport,
  buildRunEvidenceExport,
  conclusionSourceOf,
  exportFileName,
  exportFilePath,
  falsePassRows,
  resolveRowStatus,
  type G3EvidenceFileInput,
} from './g3Export'
import { G3_MATRIX_ROWS } from './g3Matrix'
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
    if (row.source !== 'this_run') {
      assert.notEqual(row.status, 'PASS', `${row.id} 不是本次运行得出，却写了 PASS`)
    }
    if (row.gap_ids.length > 0) {
      assert.notEqual(row.status, 'PASS', `${row.id} 有实现缺口，却写了 PASS`)
    }
  }
  const ac06 = exportBody.ac_rows.find((row) => row.id === 'AC-G3-06')
  assert.equal(ac06?.status, 'WARNING')
  const ac03 = exportBody.ac_rows.find((row) => row.id === 'AC-G3-03')
  assert.equal(ac03?.status, 'BLOCKED')
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

test('页面与导出体都区分「本次运行得出」与「历史快照」两类结论', () => {
  const snapshotRow = G3_MATRIX_ROWS.find((row) => row.id === 'AC-G3-10')!
  assert.equal(conclusionSourceOf(snapshotRow, false), 'historical_snapshot')
  assert.equal(conclusionSourceOf(snapshotRow, true), 'this_run')
  assert.equal(G3_CONCLUSION_SOURCE_LABELS.this_run, '本次运行得出')
  assert.equal(G3_CONCLUSION_SOURCE_LABELS.historical_snapshot, '历史快照 · 非本次运行')

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
  assert.equal(ac10?.source_label, '历史快照 · 非本次运行')
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
