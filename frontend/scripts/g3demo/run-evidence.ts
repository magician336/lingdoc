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
import { readProjectState, runChangeSetChain } from '../../src/views/lingdoc/g3demo/runner/changeset'
import { runChain } from '../../src/views/lingdoc/g3demo/runner/chain'
import { runRoadmapChain } from '../../src/views/lingdoc/g3demo/runner/roadmap'
import { runFaultInjectionChain } from '../../src/views/lingdoc/g3demo/runner/faultInjection'
import { runAssistantChain } from '../../src/views/lingdoc/g3demo/runner/assistant'
import { createDemoAccount, type G3DemoAccount } from '../../src/views/lingdoc/g3demo/runner/identity'
import { runProjectChain } from '../../src/views/lingdoc/g3demo/runner/project'
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

/**
 * 从环境变量读取**已注册**的演示账号。
 *
 * 为什么需要这个入口：受控故障注入是组合根夹具，按 `LINGDOC_FAULT_TENANT_ID`
 * 限定**唯一租户**（`internal/container/lingdoc_fault_injection.go`）。要让它命中，
 * 跑链路的账号必须属于那个租户；而每次新建随机账号都会落到**别的**租户去。
 * 所以实跑时可以先注册一次拿到租户号，用同样的账号在装配了注入的后端上再跑一轮。
 *
 * 账号口令**不落盘**：它只在进程环境里，读完即用。
 */
function accountFromEnv(): G3DemoAccount | null {
  const email = (process.env.G3_ACCOUNT_EMAIL ?? '').trim()
  const password = process.env.G3_ACCOUNT_PASSWORD ?? ''
  if (!email || !password) return null
  return {
    username: (process.env.G3_ACCOUNT_USERNAME ?? '').trim() || email.split('@')[0] || 'g3demo',
    email,
    password,
  }
}

/** 跑一次真实链路（确定性部分），证明「一次运行结束后可导出」这条路径。 */
async function runLiveChain(): Promise<{ input: G3EvidenceFileInput; ctx: G3RunContext }> {
  const reused = accountFromEnv()
  const account = reused ?? createDemoAccount()
  const client = createFetchClient(BASE_URL)
  const runId = createRunId()
  console.log(`[g3demo] 实时链路：后端 ${BASE_URL} · 运行标识 ${runId}`)
  if (reused) {
    console.log(`[g3demo] 复用已注册账号 ${reused.email}（不落盘口令）`)
  }

  const chain = await runChain(client, { account, registerFirst: !reused, runId })
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

  // 缺口收口-11（#295）新增的三条链：路线图编辑会话 / 受控故障注入的可观测口径 /
  // 助手两组说明与依赖候选读回。任一条链失败都**不中断**整条实跑——失败本身是
  // 真实事实，要落进证据，而不是把整轮丢掉。
  //
  // **每条链各建各的项目**：链路自己会 apply 变更集，从而推进项目的上下文修订；
  // 三条链共用一个项目时，后一条链拿着上一条推进前的修订号去写，就会撞
  // 409「基线已漂移」。各用各的项目，这条干扰就不存在了。
  const extra: G3RunVerdict[] = []
  // 链过滤：默认三条全跑。`G3_ONLY_CHAINS=fault,roadmap,assistant` 可以只跑其中几条
  // ——例如轮 2 只跑故障注入链（其余链的结论由轮 1 给出，不必重复）。
  const onlyChains = (process.env.G3_ONLY_CHAINS ?? '')
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
  const wants = (name: string): boolean => onlyChains.length === 0 || onlyChains.includes(name)
  const guard = async (
    id: string,
    title: string,
    body: () => Promise<{ verdicts: G3RunVerdict[] }>,
  ): Promise<void> => {
    try {
      const result = await body()
      extra.push(...result.verdicts)
    } catch (error: unknown) {
      const message = error instanceof Error ? error.message : String(error)
      extra.push({
        id,
        title,
        holds: false,
        detail: `本条链没有跑完：${message}`,
        evidence: ['链路异常：真实报错已如实记录，未中断其余链路。'],
        status: 'NOT RUN',
      })
    }
  }

  const newProject = async (
    label: string,
  ): Promise<{ projectId: string; chapters: { id: string; currentVersionId: string | null }[] }> => {
    const created = await runProjectChain(chain.ctx, client, chain.token, {
      projectName: `G3 验收台演示项目（${label} · 运行 ${chain.ctx.runId}）`,
      // 同一次运行里建**多个**项目时必须给不同的幂等键前缀：键相同而请求不同，
      // 服务端会按 409 `idempotency_conflict` 拒掉。前缀用 ASCII：中文进到幂等键里
      // 会被服务端的键长度上限截断，截断后不同前缀会撞成同一个键。
      idemPrefix: `g3-${label}`,
    })
    return { projectId: created.project.id, chapters: created.chapters }
  }

  const faultProject = await newProject('fault')
  if (wants('fault')) {
    await guard('AC-G3-03', 'G3 应用原子性：受控故障注入的可观测口径', async () =>
      runFaultInjectionChain(chain.ctx, client, chain.token, {
        projectId: faultProject.projectId,
        // 受影响章节要**本项目**的章节：变更集指向空章节列表会被服务端按
        // 400 `invalid_request` 拒掉（请求字段不符合约定）。
        affectedChapterIds: faultProject.chapters.map((chapter) => chapter.id),
      }),
    )
  }
  const roadmapProject = await newProject('roadmap')
  if (wants('roadmap')) {
    await guard('AC-G3-08', 'G3 路线图必需项：阻断稳定化', async () =>
      runRoadmapChain(chain.ctx, client, chain.token, {
        projectId: roadmapProject.projectId,
        operatorId: chain.identity.userId,
      }),
    )
  }
  if (wants('assistant')) {
    await guard('AC-G3-14', 'G3 助手影响说明与依赖候选读回', async () => {
      // 候选批次的 source 必须是**变更集 roots 里的 ProjectSpec ref**：影响评估从
      // roots 出发遍历依赖边，边的 source 不是那个 ref 时，action 不会传播。
      // target 取本轮影响评估里真实存在的章节版本。依据与理由是合成材料，
      // 不含真实课题信息。
      const assistantProject = await newProject('assistant')
      const projectState = await readProjectState(
        chain.ctx,
        client,
        chain.token,
        assistantProject.projectId,
      )
      // 候选的 target 必须是**本项目**的章节版本：拿别的项目的对象引用提交，
      // 服务端会按 404「资源不存在或不可访问」拒掉。
      const chapter = assistantProject.chapters.find((item) => item.currentVersionId)
      const candidates = chapter?.currentVersionId
        ? [
            {
              source: {
                kind: 'ProjectSpec',
                id: assistantProject.projectId,
                version: String(projectState.specRevision),
              },
              target: {
                kind: 'ChapterVersion',
                id: chapter.id,
                version: chapter.currentVersionId,
              },
              relation: 'synthetic_research_dependency',
              action: 'RETRIEVE_CANDIDATES',
              evidence: 'G3 验收台-11 合成依据：章节正文变更后需要重新检索项目资料候选',
              reason: 'G3 验收台-11 合成候选：正文变化使既有检索结论过期',
              confidence: 1,
            },
          ]
        : []
    if (!candidates.length) {
      throw new Error('本轮影响评估里没有可用的 ChapterVersion 对象，候选批次无从提交')
    }
    return runAssistantChain(chain.ctx, client, chain.token, {
      projectId: assistantProject.projectId,
      candidates,
      expectedContextRevision: projectState.currentContextRevision,
    })
    })
  }

  const verdicts = [...lifecycle.verdicts, ...extra]
  // 账号口令**不落盘**：证据里只留账号标识，不留凭据。
  console.log(`[g3demo] 实时链路判定：${verdicts.length} 条`)

  return {
    input: {
      file: `本次运行（${runId}）`,
      ticket: 'G3 验收台-8：证据导出与实现缺口标注（实时链路）',
      runId: chain.ctx.runId,
      startedAt: chain.ctx.startedAt,
      head: chain.ctx.head,
      steps: chain.ctx.steps,
      verdicts,
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
  if (!inputs.length && !live) {
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
