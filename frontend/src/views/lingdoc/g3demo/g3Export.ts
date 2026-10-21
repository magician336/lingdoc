/**
 * G3 验收台-8 · 结构化证据导出。
 *
 * 这一层只做一件事：**把一次运行（或多次运行汇总）变成一份可复核的证据 JSON**。
 *
 * 导出的诚实性约束有四条，写死在代码里：
 *
 * 1. **本次运行与历史快照互斥**。每条验收行都带 `source`（`this_run` /
 *    `historical_snapshot` / `not_judged`）与 `source_label`，两者在页面与导出体里
 *    用不同的文案与色调，不允许混读。
 * 2. **未实跑的不写 `PASS`**。只有本轮真实判定过的行才可能取 `PASS`；
 *    缺能力、豁免、环境限制、依赖真实模型的行一律不写 `PASS`
 *    （`docs/agents/development-rules.md:31`）。
 * 3. **head 变了，旧结论整体过期**。导出体带 `head` 与 `head_stale`，
 *    过期时逐行标 `stale`。
 * 4. **证据落 08 实施文档目录，不写进 Issue**
 *    （`docs/agents/issue-tracker.md:54-56`）。
 *
 * 本模块是纯函数：页面与 node 脚本跑的是同一份，导出的结构不会走样。
 */

import { G3_MATRIX_ROWS, formatAnchor, type G3MatrixRow } from './g3Matrix'
import { G3_ROW_EVIDENCE_RULES, type G3RowEvidence } from './g3Evidence'
import type { G3Status } from './g3Status'
import { G3_GAP_LIST_ISSUE, G3_IMPLEMENTATION_GAPS, downgradeGapPass, type G3ImplementationGap } from './g3Gaps'
import type { G3BackendHead, G3RunVerdict, G3StepRecord } from './runner/runContext'

/** 结论来源：本次运行 / 历史快照 / 尚未判定。三者互斥。 */
export type G3ConclusionSource = 'this_run' | 'historical_snapshot' | 'not_judged'

/** 结论来源的文案（页面与导出体共用同一套说法）。 */
export const G3_CONCLUSION_SOURCE_LABELS: Record<G3ConclusionSource, string> = {
  this_run: '本次运行得出',
  historical_snapshot: '历史快照 · 非本次运行',
  not_judged: '尚未判定',
}

/** 本票的票据名。 */
export const G3_EVIDENCE_TICKET = 'G3 验收台-8：证据导出与实现缺口标注'

/** 证据落盘目录（仓库约定：验证证据落 08 实施文档目录，不写进 Issue）。 */
export const G3_EVIDENCE_DIR = 'docs/08-本轮实施方案'

/** 证据文件名的族前缀（沿用 `G3-验收台-N-*` 既有命名）。 */
export const G3_EVIDENCE_FILE_PREFIX = 'G3-验收台-8-'

/** 导出体里的一条判定。 */
export interface G3ExportVerdict {
  id: string
  title: string
  holds: boolean
  detail: string
  evidence: string[]
  status?: G3Status
  /** 该判定是否依赖真实模型（非确定性）。 */
  model_dependent: boolean
  /** 该判定来自哪一次运行。 */
  run_id: string
}

/** 导出体里的一条验收行。 */
export interface G3ExportRow {
  id: string
  name: string
  criterion: string
  status: G3Status
  source: G3ConclusionSource
  source_label: string
  /** 本轮是否真的判定过这一行。 */
  decided: boolean
  note: string
  follow_up_issue?: number
  /** 直接缺口（因缺能力而无法实跑）。 */
  gap_ids: string[]
  /** 覆盖受限的缺口（判据已判定，但某类证据 / 分支没覆盖到）。 */
  limiting_gap_ids: string[]
  verdicts: G3ExportVerdict[]
  /** head 变化后该行结论是否过期。 */
  stale: boolean
  /**
   * 判定这一行的运行标识。
   *
   * 页面上有几段链，各跑在自己的隔离项目上；这一行说清**是哪一次运行**判的，
   * 读者才不会把不同项目的步骤混成一条链。
   */
  run_id?: string
}

/** 导出体里的一条实现缺口。 */
export interface G3ExportGap extends G3ImplementationGap {
  spec_anchor_label: string
}

/** 导出体的来源标记。 */
export interface G3ExportSource {
  kind: G3ConclusionSource
  label: string
  note: string
  run_id?: string
  head?: string | null
  steps?: number
}

/** 结构化证据导出体。 */
export interface G3EvidenceExport {
  generated_at: string
  ticket: string
  run_id: string
  started_at: string
  head: G3BackendHead | null
  head_stale: boolean
  head_stale_reason: string
  conclusion_source: G3ConclusionSource
  sources: G3ExportSource[]
  evidence_sources: G3ExportEvidenceSource[]
  steps: G3StepRecord[]
  ac_rows: G3ExportRow[]
  verdicts: G3ExportVerdict[]
  gaps: G3ExportGap[]
  runs?: G3ExportRun[]
  evidence_dir: string
  evidence_file: string
  evidence_file_note: string
}

/** 导出体里的证据来源（确定性 / 非确定性）。 */
export interface G3ExportEvidenceSource {
  kind: 'deterministic' | 'model_dependent'
  non_deterministic: boolean
  depends_on?: 'real_model'
  note: string
  runs?: unknown[]
}

/** 汇总导出体里的一次运行。 */
export interface G3ExportRun {
  file: string
  ticket: string
  run_id: string
  started_at: string
  head: G3BackendHead | null
  steps: number
  verdicts: string[]
  model_dependent: boolean
}

/**
 * 整链导出体里的一段运行。
 *
 * 页面上有几段链（变更集 / 依赖 / 稳定化 / 阻断 / 模型），**每一段**跑在自己的
 * 隔离项目上、带着自己的运行标识与 head。整链证据要把这些段**并成一份**，
 * 所以每一段都得单独记一笔：它有几步、有哪些判定、跑在哪个 head 上。
 */
export interface G3ChainRunInput {
  runId: string
  startedAt: string
  head: G3BackendHead | null
  steps: G3StepRecord[]
  verdicts: G3RunVerdict[]
  /** 这一段的 head 已变化，结论过期。 */
  stale: boolean
  staleReason?: string
  /** 这一段是否依赖真实模型（非确定性）。 */
  modelDependent?: boolean
}

/** 整链导出体的输入。 */
export interface G3ChainExportInput {
  runs: readonly G3ChainRunInput[]
  /** 投影后的矩阵行（状态与说明已按多次运行判定）。 */
  rows?: readonly G3MatrixRow[]
  /** 投影后的逐行证据。 */
  evidence?: readonly G3RowEvidence[]
  /** 模型调用记录（必须进这份导出，并标为非确定性来源）。 */
  modelRuns?: readonly unknown[]
  /** 历史快照的来源信息（用于互斥标记）。 */
  snapshot?: { sourceDir: string; head?: string | null; notice: string }
  generatedAt?: string
  dateStamp?: string
}

/** 导出的文件命名：沿用 08 实施文档目录的既有族命名。 */
export function exportFileName(dateStamp: string, runId?: string): string {
  return runId
    ? `${G3_EVIDENCE_FILE_PREFIX}证据-${runId}.json`
    : `${G3_EVIDENCE_FILE_PREFIX}整链证据-${dateStamp}.json`
}

/** 证据文件名的仓库相对路径。 */
export function exportFilePath(dateStamp: string, runId?: string): string {
  return `${G3_EVIDENCE_DIR}/${exportFileName(dateStamp, runId)}`
}

/** 结论来源：优先看本轮是否真的判定过这一行。 */
export function conclusionSourceOf(row: G3MatrixRow, decidedThisRun: boolean): G3ConclusionSource {
  if (decidedThisRun) return 'this_run'
  if (row.evidence.includes('历史快照')) return 'historical_snapshot'
  return 'not_judged'
}

/** 导出体里的缺口清单。 */
export function exportGaps(): G3ExportGap[] {
  return G3_IMPLEMENTATION_GAPS.map((gap) => ({
    ...gap,
    spec_anchor_label: formatAnchor(gap.specAnchor),
  }))
}

/** 缺口对应行在矩阵中的缺口编号（直接缺口）。 */
function gapIdsOf(rowId: string): string[] {
  return G3_IMPLEMENTATION_GAPS.filter((gap) => gap.rows.includes(rowId)).map((gap) => gap.id)
}

/** 覆盖受限的缺口编号。 */
function limitingGapIdsOf(rowId: string): string[] {
  return G3_IMPLEMENTATION_GAPS.filter((gap) => gap.limits.includes(rowId)).map((gap) => gap.id)
}

/**
 * 判定一行的状态。
 *
 * 规则（顺序不可换）：
 * 1. **判定不成立就不许升格**：没有成立的判定卡时，保留矩阵原状态
 *    （历史快照 / 缺口标注），绝不按规则表写 `PASS`。
 * 2. 判定卡自带状态时以判定卡为准（部分判定会写 `PASS (follow-up)` / `WARNING`）。
 * 3. 成立的确定性判定：取规则表的状态；**但如果这一行只有依赖真实模型的判定**，
 *    规则里的 `PASS` 不得直接用，改记 `WARNING`（非确定性结论不写 `PASS`）。
 */
export function resolveRowStatus(
  row: G3MatrixRow,
  verdicts: readonly G3ExportVerdict[],
): G3Status {
  if (!verdicts.length) return row.status
  const deterministic = verdicts.filter((verdict) => !verdict.model_dependent)
  const rule = G3_ROW_EVIDENCE_RULES[row.id]

  // 成立的确定性判定优先：它的状态比模型侧的状态更可信，也更接近矩阵口径。
  const held = deterministic.filter((verdict) => verdict.holds)
  if (held.length) {
    for (const verdict of held) {
      if (verdict.status) return downgradeGapPass(row.id, verdict.status)
    }
    if (rule) return downgradeGapPass(row.id, rule.status)
    return downgradeGapPass(row.id, 'PASS')
  }

  // 判定卡不成立：状态由判定卡自己带（部分判定），没有就**不升格**。
  for (const verdict of verdicts) {
    if (verdict.status) return downgradeGapPass(row.id, verdict.status)
  }

  // 只有模型侧判定：状态一律不写 PASS。
  const modelHeld = verdicts.filter((verdict) => verdict.model_dependent && verdict.holds)
  if (modelHeld.length) {
    if (rule) return downgradeGapPass(row.id, rule.status === 'PASS' ? 'WARNING' : rule.status)
    return 'WARNING'
  }

  return row.status
}

/**
 * 一行的跟进 Issue。
 *
 * 本轮完全判定的行不再挂跟进 Issue（没有待办了）；没被本轮判定的行保留
 * 静态链接（缺口清单或历史快照的跟进票），部分覆盖的行保留缺口清单链接。
 */
function followUpIssueOf(
  row: G3MatrixRow,
  status: G3Status,
  source: G3ConclusionSource,
): number | undefined {
  if (source === 'this_run' && status !== 'PASS (follow-up)') return undefined
  return row.followUpIssue
}

/** 判定一行的结论来源（本轮判定过就是本次运行得出）。 */
function rowSource(row: G3MatrixRow, verdicts: readonly G3ExportVerdict[]): G3ConclusionSource {
  return conclusionSourceOf(row, verdicts.length > 0)
}

/**
 * 把判定转成导出体里的形态。
 *
 * `model_dependent` 分两层，**含义不同**：
 * - **轮次级**（`runs[].model_dependent` / `evidence_sources`）：这一段**含**模型依赖的
 *   结论。它是整段的属性，不是每一条判定的属性。
 * - **判定级**（每条判定上的 `model_dependent`）：**这条**判定是不是模型依赖。
 *
 * 所以判定自己声明了标志时，**以判定为准**：一轮里只要有一条判定走了真实模型，
 * 不等于这一轮里每一条判定都模型依赖。把轮次级标志摊到每条判定上，会让确定性
 * 判定（AC-G3-01 那一类）在投影时被当成模型侧结论，`PASS` 被改写成 `WARNING`。
 *
 * 判定**没有**声明标志时，标志的来源要看**这份文件**：见
 * `fileLevelModelDependent()`。
 */
function toExportVerdict(verdict: G3RunVerdict, runId: string, modelDependent: boolean): G3ExportVerdict {
  return {
    id: verdict.id,
    title: verdict.title,
    holds: verdict.holds,
    detail: verdict.detail,
    evidence: [...verdict.evidence],
    status: verdict.status,
    model_dependent: verdict.modelDependent ?? modelDependent,
    run_id: runId,
  }
}

/**
 * 这一份文件的轮次级标志，要不要摊到它那些**没有自己声明**标志的判定上。
 *
 * 两种文件要分开看：
 * - **整份由一次模型轮次产出**的历史证据文件：它的判定一条都没声明过标志，
 *   标志记在文件级（「这份证据的结论依赖真实模型」）。这时文件级标志就是每条判定
 *   的标志，摊下去才对。
 * - **一次运行里混着多条链**的文件（实时链）：模型依赖的判定自己声明了标志，
 *   其余判定（确定性链）没声明。文件级在这里只表示「这一段含模型依赖的结论」，
 *   摊下去会让确定性判定被误标成模型侧结论，它们的 `PASS` 在投影时被改写成
 *   `WARNING`——那是假的口径。
 *
 * 判据：**只要这份文件里有任意一条判定自己声明过标志，就以判定为准**；
 * 一条都没声明，才用文件级标志兜底。
 */
function fileLevelModelDependent(
  verdicts: readonly G3RunVerdict[],
  fileModelDependent: boolean,
): boolean {
  const anyVerdictDeclares = verdicts.some((verdict) => verdict.modelDependent !== undefined)
  return anyVerdictDeclares ? false : fileModelDependent
}

/** 一次运行的导出体输入。 */
export interface G3RunExportInput {
  runId: string
  startedAt: string
  head: G3BackendHead | null
  steps: G3StepRecord[]
  rows?: readonly G3MatrixRow[]
  evidence?: readonly G3RowEvidence[]
  verdicts?: readonly G3RunVerdict[]
  modelRuns?: readonly unknown[]
  stale: boolean
  staleReason?: string
  generatedAt?: string
  dateStamp?: string
  /** 历史快照的来源信息（用于互斥标记）。 */
  snapshot?: { sourceDir: string; head?: string | null; notice: string }
}

/** 构造一次运行的证据导出体。 */
export function buildRunEvidenceExport(input: G3RunExportInput): G3EvidenceExport {
  const rows = input.rows ?? G3_MATRIX_ROWS
  const evidenceById = new Map((input.evidence ?? []).map((item) => [item.rowId, item]))
  const verdicts = (input.verdicts ?? []).map((verdict) =>
    toExportVerdict(verdict, input.runId, false),
  )

  const acRows: G3ExportRow[] = rows.map((row) => {
    const rowVerdicts = verdicts.filter((verdict) => verdict.id === row.id)
    const evidence = evidenceById.get(row.id)
    const source = rowSource(row, rowVerdicts)
    const status = resolveRowStatus(row, rowVerdicts)
    return {
      id: row.id,
      name: row.name,
      criterion: row.criterion,
      status,
      source,
      source_label: G3_CONCLUSION_SOURCE_LABELS[source],
      decided: source === 'this_run',
      note: evidence?.note ?? row.note ?? '',
      follow_up_issue: followUpIssueOf(row, status, source),
      gap_ids: gapIdsOf(row.id),
      limiting_gap_ids: limitingGapIdsOf(row.id),
      verdicts: rowVerdicts,
      stale: input.stale,
    }
  })

  const sources: G3ExportSource[] = [
    {
      kind: 'this_run',
      label: G3_CONCLUSION_SOURCE_LABELS.this_run,
      note: '本次运行真实调用得出的结论：绑定本次运行标识与后端 head。',
      run_id: input.runId,
      head: input.head?.commitId ?? null,
      steps: input.steps.length,
    },
    {
      kind: 'historical_snapshot',
      label: G3_CONCLUSION_SOURCE_LABELS.historical_snapshot,
      note:
        '历史快照的结论：跑在另一个 head 上，**不是本次运行的结果**；' +
        '两者在页面与本导出体里分开列示，不得混读。',
      head: input.snapshot?.head ?? null,
    },
  ]

  const evidenceSources: G3ExportEvidenceSource[] = [
    {
      kind: 'deterministic',
      non_deterministic: false,
      note: '确定性结论：状态由本轮真实调用与响应事实推出。',
    },
    {
      kind: 'model_dependent',
      non_deterministic: true,
      depends_on: 'real_model',
      note:
        '非确定性来源：以下结论依赖真实模型，同样的输入不保证得到同样的输出，也不保证逐字复现；' +
        '矩阵状态一律记 WARNING（模型不可用或缺外部凭据时记 NOT RUN）。',
      runs: (input.modelRuns ?? []) as unknown[],
    },
  ]

  const fileName = exportFileName(input.dateStamp ?? '00000000', input.runId)

  return {
    generated_at: input.generatedAt ?? new Date().toISOString(),
    ticket: G3_EVIDENCE_TICKET,
    run_id: input.runId,
    started_at: input.startedAt,
    head: input.head,
    head_stale: input.stale,
    head_stale_reason: input.staleReason ?? '',
    conclusion_source: 'this_run',
    sources,
    evidence_sources: evidenceSources,
    steps: input.steps,
    ac_rows: acRows,
    verdicts,
    gaps: exportGaps(),
    evidence_dir: G3_EVIDENCE_DIR,
    evidence_file: fileName,
    evidence_file_note:
      `证据落 ${G3_EVIDENCE_DIR}/ 目录，**不写进 Issue**（docs/agents/issue-tracker.md:54-56）。`,
  }
}

/** 汇总导出体里的一份既有证据文件。 */
export interface G3EvidenceFileInput {
  file: string
  ticket: string
  runId: string
  startedAt: string
  generatedAt?: string
  head: G3BackendHead | null
  steps: G3StepRecord[]
  verdicts: G3RunVerdict[]
  /** 该文件里的判定是否依赖真实模型。 */
  modelDependent: boolean
}

/** 汇总导出体的输入。 */
export interface G3AggregateExportInput {
  files: readonly G3EvidenceFileInput[]
  rows?: readonly G3MatrixRow[]
  stale?: boolean
  staleReason?: string
  generatedAt?: string
  dateStamp?: string
  runId?: string
}

/**
 * 把多份既有证据文件汇总成一份总证据。
 *
 * head 不一致时整体标为过期：结论跑在不同的代码版本上，不能混成一份。
 */
export function buildAggregateEvidenceExport(input: G3AggregateExportInput): G3EvidenceExport {
  const rows = input.rows ?? G3_MATRIX_ROWS
  const verdicts: G3ExportVerdict[] = []
  const runs: G3ExportRun[] = []
  const heads = new Set<string>()

  for (const file of input.files) {
    if (file.head?.commitId) heads.add(file.head.commitId)
    const fileVerdicts = file.verdicts.map((verdict) =>
      toExportVerdict(
        verdict,
        file.runId,
        fileLevelModelDependent(file.verdicts, file.modelDependent),
      ),
    )
    verdicts.push(...fileVerdicts)
    runs.push({
      file: file.file,
      ticket: file.ticket,
      run_id: file.runId,
      started_at: file.startedAt,
      head: file.head,
      steps: file.steps.length,
      verdicts: fileVerdicts.map((verdict) => verdict.id),
      model_dependent: file.modelDependent,
    })
  }

  const headMismatch = heads.size > 1
  const stale = Boolean(input.stale) || headMismatch
  const staleReason =
    input.staleReason ??
    (headMismatch
      ? `各份证据的后端 head 不一致（${[...heads].join(' / ')}），结论整体过期。`
      : '')

  const acRows: G3ExportRow[] = rows.map((row) => {
    const rowVerdicts = verdicts.filter((verdict) => verdict.id === row.id)
    const source = rowSource(row, rowVerdicts)
    const status = resolveRowStatus(row, rowVerdicts)
    const rule = G3_ROW_EVIDENCE_RULES[row.id]
    // 说明与跟进 Issue 的口径与页面一致：完全判定的行不再挂跟进 Issue，
    // 部分覆盖（PASS (follow-up)）的行保留缺口清单链接。
    const partial = status === 'PASS (follow-up)'
    // 规则表里的说明写的是「本轮实跑判定成立：……」。只有判定**成立**时才能带这句；
    // 判定不成立（哪怕只部分成立）时换成如实说明，否则导出行会写着「判定成立」
    // 却挂着一条不成立的判定卡。
    const heldVerdicts = rowVerdicts.filter((verdict) => verdict.holds)
    const note = rowVerdicts.length
      ? [
          heldVerdicts.length ? rule?.note : undefined,
          rowVerdicts
            .map((verdict) =>
              verdict.holds
                ? verdict.detail
                : `判定卡「${verdict.id}」不成立：${verdict.detail}`,
            )
            .join(' '),
        ]
          .filter(Boolean)
          .join('｜')
      : (row.note ?? '')
    return {
      id: row.id,
      name: row.name,
      criterion: row.criterion,
      status,
      source,
      source_label: G3_CONCLUSION_SOURCE_LABELS[source],
      decided: source === 'this_run',
      note,
      follow_up_issue: partial
        ? (rule?.followUpIssue ?? row.followUpIssue)
        : followUpIssueOf(row, status, source),
      gap_ids: gapIdsOf(row.id),
      limiting_gap_ids: limitingGapIdsOf(row.id),
      verdicts: rowVerdicts,
      stale,
    }
  })

  const firstHead = input.files[0]?.head ?? null
  const dateStamp = input.dateStamp ?? '00000000'
  const runId = input.runId ?? 'aggregate'

  const evidenceSources: G3ExportEvidenceSource[] = [
    {
      kind: 'deterministic',
      non_deterministic: false,
      note: '确定性结论：状态由真实调用与响应事实推出。',
    },
    {
      kind: 'model_dependent',
      non_deterministic: true,
      depends_on: 'real_model',
      note:
        '非确定性来源：以下结论依赖真实模型，同样的输入不保证得到同样的输出，也不保证逐字复现；' +
        '矩阵状态一律记 WARNING（模型不可用或缺外部凭据时记 NOT RUN）。',
    },
  ]

  return {
    generated_at: input.generatedAt ?? new Date().toISOString(),
    ticket: G3_EVIDENCE_TICKET,
    run_id: runId,
    started_at: input.files[0]?.startedAt ?? '',
    head: firstHead,
    head_stale: stale,
    head_stale_reason: staleReason,
    conclusion_source: 'this_run',
    sources: [
      {
        kind: 'this_run',
        label: G3_CONCLUSION_SOURCE_LABELS.this_run,
        note: `本次汇总所含的 ${input.files.length} 次真实运行的结论：绑定各自运行标识与后端 head。`,
        run_id: runId,
        head: firstHead?.commitId ?? null,
        steps: input.files.reduce((sum, file) => sum + file.steps.length, 0),
      },
      {
        kind: 'historical_snapshot',
        label: G3_CONCLUSION_SOURCE_LABELS.historical_snapshot,
        note:
          '历史快照的结论：跑在另一个 head 上，**不是本次运行的结果**；' +
          '两者在页面与本导出体里分开列示，不得混读。',
      },
    ],
    evidence_sources: evidenceSources,
    steps: input.files.flatMap((file) => file.steps),
    ac_rows: acRows,
    verdicts,
    gaps: exportGaps(),
    runs,
    evidence_dir: G3_EVIDENCE_DIR,
    evidence_file: exportFileName(dateStamp),
    evidence_file_note:
      `证据落 ${G3_EVIDENCE_DIR}/ 目录，**不写进 Issue**（docs/agents/issue-tracker.md:54-56）。`,
  }
}

/**
 * 把**多段运行**并成一份整链证据。
 *
 * 这是页面上「导出整链证据 JSON」真正落的东西：页面上每段链（变更集 / 依赖 /
 * 稳定化 / 阻断 / 模型）各跑在自己的隔离项目上，各带自己的运行标识与 head。
 * 把它并成一份时守住三条：
 *
 * 1. **每段运行单独记账**（`runs[]`），每一步都带自己的运行标识——
 *    读者能判断某一步与某条结论是不是同一次运行。
 * 2. **验收行的状态取投影结果**（`rows` / `evidence` 由调用方按多次运行投影
 *    得出），不在这里重新判定：判定只在单次运行内做一次，导出体不再判第二次。
 * 3. **head 不一致就整体标过期**：不同代码版本上的结论不能混成一份。
 */
export function buildChainEvidenceExport(input: G3ChainExportInput): G3EvidenceExport {
  const rows = input.rows ?? G3_MATRIX_ROWS
  const evidenceById = new Map((input.evidence ?? []).map((item) => [item.rowId, item]))
  const verdicts: G3ExportVerdict[] = []
  const runs: G3ExportRun[] = []
  const heads = new Set<string>()

  for (const run of input.runs) {
    if (run.head?.commitId) heads.add(run.head.commitId)
    const runVerdicts = run.verdicts.map((verdict) =>
      toExportVerdict(
        verdict,
        run.runId,
        fileLevelModelDependent(run.verdicts, run.modelDependent === true),
      ),
    )
    verdicts.push(...runVerdicts)
    runs.push({
      file: `本次运行（${run.runId}）`,
      ticket: G3_EVIDENCE_TICKET,
      run_id: run.runId,
      started_at: run.startedAt,
      head: run.head,
      steps: run.steps.length,
      verdicts: runVerdicts.map((verdict) => verdict.id),
      model_dependent: run.modelDependent === true,
    })
  }

  const headMismatch = heads.size > 1
  const anyStale = input.runs.some((run) => run.stale)
  const stale = headMismatch || anyStale
  const staleReason = headMismatch
    ? `各段运行的后端 head 不一致（${[...heads].join(' / ')}），结论整体过期。`
    : anyStale
      ? (input.runs.find((run) => run.stale)?.staleReason ?? '存在已过期的运行段，结论整体过期。')
      : ''

  const acRows: G3ExportRow[] = rows.map((row) => {
    const evidence = evidenceById.get(row.id)
    const decided = evidence?.decided === true
    // 这一行的判定只取**判定它的那一次运行**的判定卡：
    // 不同运行的同名判定不是一回事，混在一起会让人读不出结论来自哪一段。
    const rowVerdicts = decided
      ? verdicts.filter(
        (verdict) =>
          verdict.id === row.id
          && (!evidence?.runId || verdict.run_id === evidence.runId),
      )
      : []
    const source = conclusionSourceOf(row, decided)
    // 状态以**投影结果**为权威（判定已在单次运行内做完一次，导出体不重判）；
    // 未判定的行回到矩阵原状态。
    const base = G3_MATRIX_ROWS.find((item) => item.id === row.id) ?? row
    // 同样过一遍 #246 的降级：这条路径的 `row.status` 可能来自上一次导出，
    // 带着实现缺口的行不得在这里重新变成 PASS。
    let status = downgradeGapPass(row.id, decided ? row.status : base.status)
    let note = evidence?.note ?? row.note ?? ''
    // 兜底：判定卡不成立时**不许**写 PASS —— 退回矩阵原状态并写明原因。
    if (status === 'PASS' && rowVerdicts.length > 0 && !rowVerdicts.some((verdict) => verdict.holds)) {
      status = base.status
      note = `${note}｜导出兜底：本行的判定卡不成立，状态已从 PASS 退回 ${base.status}。`.trim()
    }
    const partial = status === 'PASS (follow-up)'
    const rule = G3_ROW_EVIDENCE_RULES[row.id]
    return {
      id: row.id,
      name: row.name,
      criterion: row.criterion,
      status,
      source,
      source_label: G3_CONCLUSION_SOURCE_LABELS[source],
      decided,
      note,
      follow_up_issue: partial
        ? (rule?.followUpIssue ?? row.followUpIssue)
        : followUpIssueOf(row, status, source),
      gap_ids: gapIdsOf(row.id),
      limiting_gap_ids: limitingGapIdsOf(row.id),
      verdicts: rowVerdicts,
      stale,
      run_id: evidence?.runId,
    }
  })

  const firstHead = input.runs[0]?.head ?? null
  const dateStamp = input.dateStamp ?? '00000000'
  const runId = input.runs.length === 1 ? input.runs[0].runId : `chain-${dateStamp}`

  const evidenceSources: G3ExportEvidenceSource[] = [
    {
      kind: 'deterministic',
      non_deterministic: false,
      note: '确定性结论：状态由真实调用与响应事实推出。',
    },
    {
      kind: 'model_dependent',
      non_deterministic: true,
      depends_on: 'real_model',
      note:
        '非确定性来源：以下结论依赖真实模型，同样的输入不保证得到同样的输出，也不保证逐字复现；' +
        '矩阵状态一律记 WARNING（模型不可用或缺外部凭据时记 NOT RUN）。',
      runs: (input.modelRuns ?? []) as unknown[],
    },
  ]

  return {
    generated_at: input.generatedAt ?? new Date().toISOString(),
    ticket: G3_EVIDENCE_TICKET,
    run_id: runId,
    started_at: input.runs[0]?.startedAt ?? '',
    head: firstHead,
    head_stale: stale,
    head_stale_reason: staleReason,
    conclusion_source: 'this_run',
    sources: [
      {
        kind: 'this_run',
        label: G3_CONCLUSION_SOURCE_LABELS.this_run,
        note:
          `本次会话所含的 ${input.runs.length} 段真实运行的结论：` +
          '每段各跑在自己的隔离项目上，各带自己的运行标识与后端 head。',
        run_id: runId,
        head: firstHead?.commitId ?? null,
        steps: input.runs.reduce((sum, run) => sum + run.steps.length, 0),
      },
      {
        kind: 'historical_snapshot',
        label: G3_CONCLUSION_SOURCE_LABELS.historical_snapshot,
        note:
          '历史快照的结论：跑在另一个 head 上，**不是本次运行的结果**；' +
          '两者在页面与本导出体里分开列示，不得混读。',
        head: input.snapshot?.head ?? null,
      },
    ],
    evidence_sources: evidenceSources,
    steps: input.runs.flatMap((run) => run.steps),
    ac_rows: acRows,
    verdicts,
    gaps: exportGaps(),
    runs,
    evidence_dir: G3_EVIDENCE_DIR,
    evidence_file: exportFileName(dateStamp),
    evidence_file_note:
      `证据落 ${G3_EVIDENCE_DIR}/ 目录，**不写进 Issue**（docs/agents/issue-tracker.md:54-56）。`,
  }
}

/**
 * 找出导出体里「不诚实」的行：不该写 `PASS` 却写了 `PASS`。
 *
 * 规则：来源不是本次运行、涉及实现缺口、只有依赖真实模型的判定、
 * **或判定卡本身不成立**（`holds=false` 却仍被写成 `PASS`）—— 四者任一，
 * 都不许是 `PASS`。
 */
export function falsePassRows(rows: readonly G3ExportRow[]): G3ExportRow[] {
  return rows.filter((row) => {
    if (row.status !== 'PASS') return false
    if (row.source !== 'this_run') return true
    if (row.gap_ids.length > 0) return true
    if (row.verdicts.length > 0 && row.verdicts.every((verdict) => verdict.model_dependent)) return true
    // 判定卡不成立就不许升格：这一步是「假 PASS」的最后一道兜底。
    if (row.verdicts.some((verdict) => !verdict.holds)) return true
    return false
  })
}

/** 缺口清单 Issue（导出体里带上，读者可直接跳转）。 */
export const G3_EXPORT_GAP_ISSUE = G3_GAP_LIST_ISSUE
