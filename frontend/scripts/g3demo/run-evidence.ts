/**
 * G3 验收台-8 · 整链证据汇总：把本轮各票已落盘的证据汇总成一份总证据 JSON。
 *
 * 为什么要汇总而不是重跑：#238–#243 各票的实跑证据已经落在
 * `docs/08-本轮实施方案/G3-验收台-N-*.json`，每一份都带着自己的运行标识与后端 head。
 * 整链证据的形态就是**把这些真实运行汇总成一份**（保留每份的运行标识、head、
 * 逐步请求与响应、每条验收行的判定），再补上本票的验收行状态与实现缺口清单。
 * 重跑一遍只会得到同样的结论，却丢掉「哪一条结论来自哪一次运行」这件事。
 *
 * 用法（在 `frontend/` 目录下）：
 *   npx tsx scripts/g3demo/run-evidence.ts
 *   npx tsx scripts/g3demo/run-evidence.ts --out ../docs/08-本轮实施方案/xxx.json
 *   npx tsx scripts/g3demo/run-evidence.ts --live   # 额外跑一次真实链路并落一份本次运行导出
 *   npx tsx scripts/g3demo/run-evidence.ts --chain   # 走页面的「整链证据」口径落盘
 *
 * 环境变量：
 *   G3_BASE_URL     后端地址，默认 http://127.0.0.1:8080
 *   G3_EVIDENCE_DIR 证据目录，默认 <repo>/docs/08-本轮实施方案
 *   G3_EVIDENCE_OUT 汇总证据落盘路径，默认 <证据目录>/G3-验收台-8-整链证据-<日期>.json
 */

import { mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  G3_EVIDENCE_DIR,
  buildAggregateEvidenceExport,
  buildChainEvidenceExport,
  buildRunEvidenceExport,
  exportFileName,
  type G3ChainRunInput,
  type G3EvidenceFileInput,
} from '../../src/views/lingdoc/g3demo/g3Export'
import { applyRunEvidence } from '../../src/views/lingdoc/g3demo/g3Evidence'
import { G3_MATRIX_ROWS } from '../../src/views/lingdoc/g3demo/g3Matrix'
import { runChangeSetChain } from '../../src/views/lingdoc/g3demo/runner/changeset'
import { runChain } from '../../src/views/lingdoc/g3demo/runner/chain'
import { createDemoAccount } from '../../src/views/lingdoc/g3demo/runner/identity'
import { createRunId } from '../../src/views/lingdoc/g3demo/runner/runContext'
import type { G3BackendHead, G3RunContext, G3RunVerdict, G3StepRecord } from '../../src/views/lingdoc/g3demo/runner/runContext'
import { createFetchClient } from './fetchClient'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO_ROOT = resolve(HERE, '../../..')

function argOf(flag: string): string | undefined {
  const index = process.argv.indexOf(flag)
  if (index < 0) return undefined
  return process.argv[index + 1]
}

const BASE_URL = (argOf('--base') ?? process.env.G3_BASE_URL ?? 'http://127.0.0.1:8080').replace(/\/+$/, '')

function dateStamp(): string {
  const now = new Date()
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}`
}

const EVIDENCE_DIR = resolve(REPO_ROOT, process.env.G3_EVIDENCE_DIR ?? G3_EVIDENCE_DIR)
const outArg = argOf('--out')
const OUT_PATH = outArg
  ? resolve(process.cwd(), outArg)
  : resolve(
    EVIDENCE_DIR,
    process.env.G3_EVIDENCE_OUT ?? `G3-验收台-8-整链证据-${dateStamp()}.json`,
  )

/** 从一份证据文件里取出判定（顶层或一层嵌套的 `verdicts`）。 */
function verdictsOf(raw: Record<string, unknown>): G3RunVerdict[] {
  const top = raw.verdicts
  if (Array.isArray(top)) return top as G3RunVerdict[]
  for (const value of Object.values(raw)) {
    if (value && typeof value === 'object' && Array.isArray((value as { verdicts?: unknown }).verdicts)) {
      return (value as { verdicts: G3RunVerdict[] }).verdicts
    }
  }
  return []
}

/** 该文件里的判定是否依赖真实模型。 */
function isModelDependent(raw: Record<string, unknown>): boolean {
  const sources = raw.evidence_sources
  if (!Array.isArray(sources)) return false
  return sources.some(
    (item) => item && typeof item === 'object' && (item as { kind?: string }).kind === 'model_dependent',
  )
}

function readEvidenceFile(file: string): { input: G3EvidenceFileInput; raw: Record<string, unknown> } | null {
  const raw = JSON.parse(readFileSync(resolve(EVIDENCE_DIR, file), 'utf8')) as Record<string, unknown>
  if (!raw.run_id) return null
  return {
    raw,
    input: {
      file,
      ticket: typeof raw.ticket === 'string' ? raw.ticket : file,
      runId: String(raw.run_id),
      startedAt: typeof raw.started_at === 'string' ? raw.started_at : '',
      generatedAt: typeof raw.generated_at === 'string' ? raw.generated_at : undefined,
      head: (raw.backend_head ?? null) as G3BackendHead | null,
      steps: Array.isArray(raw.steps) ? (raw.steps as G3StepRecord[]) : [],
      verdicts: verdictsOf(raw),
      modelDependent: isModelDependent(raw),
    },
  }
}

/** 跑一次真实链路（确定性部分），证明「一次运行结束后可导出」这条路径。 */
async function runLiveChain(): Promise<{ input: G3EvidenceFileInput; ctx: G3RunContext }> {
  const account = createDemoAccount()
  const client = createFetchClient(BASE_URL)
  const runId = createRunId()
  console.log(`[g3demo] 实时链路：后端 ${BASE_URL} · 运行标识 ${runId}`)

  const chain = await runChain(client, { account, registerFirst: true, runId })
  console.log(`[g3demo] 前置链完成：项目 ${chain.project.id}`)

  const lifecycle = await runChangeSetChain(chain.ctx, client, chain.token, {
    projectId: chain.project.id,
    chapters: chain.chapters.map((chapter) => ({
      id: chapter.id,
      sectionId: chapter.sectionId,
      title: chapter.title,
    })),
    tenantId: chain.identity.tenantId,
  })

  // 账号口令**不落盘**：证据里只留账号标识，不留凭据。
  console.log(`[g3demo] 实时链路判定：${lifecycle.verdicts.length} 条`)

  return {
    input: {
      file: `本次运行（${runId}）`,
      ticket: 'G3 验收台-8：证据导出与实现缺口标注（实时链路）',
      runId: chain.ctx.runId,
      startedAt: chain.ctx.startedAt,
      head: chain.ctx.head,
      steps: chain.ctx.steps,
      verdicts: lifecycle.verdicts,
      modelDependent: false,
    },
    ctx: chain.ctx,
  }
}

/**
 * 整链口径：把各票已落盘的真实运行并成一份导出体。
 *
 * 与页面上「导出整链证据 JSON」走**同一个构造函数**（`buildChainEvidenceExport`），
 * 矩阵投影也用**同一个纯函数**（`applyRunEvidence`），所以两处不会走样。
 *
 * 这里不重跑：#238–#243 各票的真实运行已经落在证据目录里，
 * 每一份都带着自己的运行标识、head、逐步请求与响应。整链证据的形态就是
 * 「把这些真实运行并成一份」，保留「哪一条结论来自哪一次运行」这件事。
 */
function buildChainExport(
  files: readonly { input: G3EvidenceFileInput; raw: Record<string, unknown> }[],
): ReturnType<typeof buildChainEvidenceExport> {
  const runs: G3ChainRunInput[] = files.map(({ input, raw }) => ({
    runId: input.runId,
    startedAt: input.startedAt,
    head: input.head,
    steps: input.steps,
    verdicts: input.verdicts,
    stale: raw.head_stale === true,
    staleReason: typeof raw.head_stale_reason === 'string' ? raw.head_stale_reason : undefined,
    modelDependent: input.modelDependent,
  }))
  // 投影用的运行上下文：每段的步骤与判定都来自**同一次运行**，不跨项目串味。
  const contexts: G3RunContext[] = files.map(({ input, raw }) => ({
    runId: input.runId,
    startedAt: input.startedAt,
    accountEmail: '',
    steps: input.steps,
    head: input.head,
    chapters: [],
    stale: raw.head_stale === true,
    staleReason: typeof raw.head_stale_reason === 'string' ? raw.head_stale_reason : undefined,
    verdicts: input.verdicts,
  }))
  const view = applyRunEvidence(G3_MATRIX_ROWS, contexts)
  const modelRuns = files.flatMap(({ raw }) =>
    Array.isArray(raw.model_runs) ? (raw.model_runs as unknown[]) : [],
  )
  return buildChainEvidenceExport({
    runs,
    rows: view.rows,
    evidence: view.evidence,
    modelRuns,
    generatedAt: new Date().toISOString(),
    dateStamp: dateStamp(),
  })
}

async function main(): Promise<void> {
  const live = process.argv.includes('--live')
  const chain = process.argv.includes('--chain')
  const files = readdirSync(EVIDENCE_DIR)
    .filter((name) => name.startsWith('G3-验收台-') && name.endsWith('.json'))
    .filter((name) => !name.startsWith('G3-验收台-8-'))
    .sort()

  const loaded: { input: G3EvidenceFileInput; raw: Record<string, unknown> }[] = []
  for (const file of files) {
    const read = readEvidenceFile(file)
    if (read) loaded.push(read)
  }
  const inputs: G3EvidenceFileInput[] = loaded.map((item) => item.input)
  if (!inputs.length) {
    throw new Error(`证据目录里没有可汇总的 G3 验收台证据：${EVIDENCE_DIR}`)
  }

  if (chain) {
    const body = buildChainExport(loaded)
    mkdirSync(dirname(OUT_PATH), { recursive: true })
    writeFileSync(OUT_PATH, `${JSON.stringify(body, null, 2)}\n`, 'utf8')
    console.log('[g3demo] 整链证据（逐段记账）：')
    for (const run of body.runs ?? []) {
      console.log(
        `  ${run.run_id} · head ${run.head?.commitId ?? '未读到'} · 步骤 ${run.steps} 条 · ` +
          `判定 ${run.verdicts.length} 条${run.model_dependent ? '（含依赖真实模型的判定）' : ''}`,
      )
    }
    console.log('')
    console.log(`[g3demo] 验收行：${body.ac_rows.length} 条`)
    for (const row of body.ac_rows) {
      console.log(
        `  ${row.id.padEnd(10)} ${row.status.padEnd(16)} ${row.source_label}` +
          `${row.gap_ids.length ? ` · 缺口 ${row.gap_ids.join('/')}` : ''}` +
          `${row.limiting_gap_ids.length ? ` · 覆盖受限 ${row.limiting_gap_ids.join('/')}` : ''}`,
      )
    }
    console.log('')
    console.log(`[g3demo] 后端 head：${body.head?.commitId ?? '未读到'}${body.head_stale ? '（已过期）' : ''}`)
    console.log(`[g3demo] 证据落盘：${OUT_PATH}`)
    return
  }

  if (live) {
    const { input: liveInput, ctx: liveCtx } = await runLiveChain()
    inputs.push(liveInput)
    // 本次运行的导出体：与页面上「导出整链证据 JSON」走的是同一个构造函数，
    // 矩阵投影也用同一个纯函数（applyRunEvidence），所以两处不会走样。
    const view = applyRunEvidence(G3_MATRIX_ROWS, liveCtx)
    const runExport = buildRunEvidenceExport({
      runId: liveInput.runId,
      startedAt: liveInput.startedAt,
      head: liveInput.head,
      steps: liveInput.steps,
      rows: view.rows,
      evidence: view.evidence,
      verdicts: liveInput.verdicts,
      stale: false,
      generatedAt: new Date().toISOString(),
      dateStamp: dateStamp(),
    })
    const runPath = resolve(EVIDENCE_DIR, exportFileName(dateStamp(), liveInput.runId))
    writeFileSync(runPath, `${JSON.stringify(runExport, null, 2)}\n`, 'utf8')
    console.log(`[g3demo] 本次运行导出落盘：${runPath}`)
  }

  const exportBody = buildAggregateEvidenceExport({
    files: inputs,
    generatedAt: new Date().toISOString(),
    dateStamp: dateStamp(),
    runId: `aggregate-${dateStamp()}`,
  })

  mkdirSync(dirname(OUT_PATH), { recursive: true })
  writeFileSync(OUT_PATH, `${JSON.stringify(exportBody, null, 2)}\n`, 'utf8')

  console.log('[g3demo] 汇总的证据文件：')
  for (const run of exportBody.runs ?? []) {
    console.log(
      `  ${run.file} · 运行标识 ${run.run_id} · head ${run.head?.commitId ?? '未读到'} · ` +
        `步骤 ${run.steps} 条 · 判定 ${run.verdicts.length} 条${run.model_dependent ? '（含依赖真实模型的判定）' : ''}`,
    )
  }
  console.log('')
  console.log(`[g3demo] 验收行：${exportBody.ac_rows.length} 条`)
  for (const row of exportBody.ac_rows) {
    console.log(
      `  ${row.id.padEnd(10)} ${row.status.padEnd(16)} ${row.source_label}` +
        `${row.gap_ids.length ? ` · 缺口 ${row.gap_ids.join('/')}` : ''}` +
        `${row.limiting_gap_ids.length ? ` · 覆盖受限 ${row.limiting_gap_ids.join('/')}` : ''}`,
    )
  }
  console.log('')
  console.log(`[g3demo] 实现缺口：${exportBody.gaps.length} 条（缺口清单 Issue #246）`)
  console.log(`[g3demo] 后端 head：${exportBody.head?.commitId ?? '未读到'}${exportBody.head_stale ? '（已过期）' : ''}`)
  console.log(`[g3demo] 证据落盘：${OUT_PATH}`)
}

main().catch((error: unknown) => {
  const message = error instanceof Error ? error.message : String(error)
  console.error(`[g3demo] 运行失败：${message}`)
  process.exitCode = 1
})
