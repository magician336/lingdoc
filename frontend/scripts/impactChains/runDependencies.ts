/**
 * G3 验收台-4 · 依赖图与影响任务：对真实后端跑一遍本票链路，把证据 JSON 落盘。
 *
 * 与 #238 / #239 同一形态：执行器是纯 TS、注入式 HTTP 客户端，页面与本脚本
 * 跑的是**同一份**执行器，所以页面上的判定与落盘的证据不会走样。
 *
 * 用法（在 `frontend/` 目录下）：
 *   npx tsx scripts/impactChains/runDependencies.ts
 *   npx tsx scripts/impactChains/runDependencies.ts --base http://127.0.0.1:8080 --out ../docs/08-本轮实施方案/xxx.json
 *
 * 环境变量：
 *   G3_BASE_URL  后端地址，默认 http://127.0.0.1:8080
 *   G3_EVIDENCE_OUT  证据落盘路径，默认 <repo>/docs/08-本轮实施方案/G3-验收台-4-依赖图与影响任务证据-<日期>.json
 */

import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { runChain } from '../../src/views/lingdoc/g3demo/runner/chain'
import { runDependencyChain } from '../../src/views/lingdoc/g3demo/runner/dependencies'
import { createDemoAccount, type G3DemoAccount } from '../../src/views/lingdoc/g3demo/runner/identity'
import { createRunId } from '../../src/views/lingdoc/g3demo/runner/runContext'
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

const outArg = argOf('--out')
const OUT_PATH = outArg
  ? resolve(process.cwd(), outArg)
  : resolve(
    REPO_ROOT,
    process.env.G3_EVIDENCE_OUT ??
      `docs/08-本轮实施方案/G3-验收台-4-依赖图与影响任务证据-${dateStamp()}.json`,
  )

async function main(): Promise<void> {
  // 账号文件放在系统临时目录里，**不进仓库**：口令不能落进版本库。
  const accountFile = resolve(argOf('--account-file') ?? resolve(tmpdir(), 'g3demo-account-t4.json'))
  const reuse = process.argv.includes('--reuse')

  let account: G3DemoAccount
  let registerFirst: boolean
  if (reuse) {
    account = JSON.parse(readFileSync(accountFile, 'utf8')) as G3DemoAccount
    registerFirst = false
  } else {
    account = createDemoAccount()
    registerFirst = true
  }

  const client = createFetchClient(BASE_URL)
  const runId = createRunId()
  console.log(`[g3demo] 后端：${BASE_URL}`)
  console.log(`[g3demo] 运行标识：${runId}`)
  console.log(`[g3demo] 演示账号：${account.email}（${registerFirst ? '本次自建' : '复用本地账号'}）`)

  const chain = await runChain(client, { account, registerFirst, runId })
  if (!reuse) {
    writeFileSync(accountFile, `${JSON.stringify(account)}\n`, 'utf8')
  }
  console.log(`[g3demo] 前置链完成：项目 ${chain.project.id}`)

  const dependency = await runDependencyChain(chain.ctx, client, chain.token, {
    projectId: chain.project.id,
    chapters: chain.chapters.map((chapter) => ({
      id: chapter.id,
      sectionId: chapter.sectionId,
      title: chapter.title,
    })),
    operatorId: chain.identity.userId,
  })

  const evidence = {
    generated_at: new Date().toISOString(),
    backend_base_url: BASE_URL,
    run_id: chain.ctx.runId,
    started_at: chain.ctx.startedAt,
    ticket: 'G3 验收台-4：依赖图、影响任务与影响预览分组',
    demo_account: { username: account.username, email: account.email },
    account_reused: !registerFirst,
    backend_head: chain.ctx.head,
    identity: chain.identity,
    project: chain.project,
    chapters: chain.chapters,
    change_set: dependency.changeSet,
    change_draft: dependency.draft,
    dependency_edges: dependency.edges,
    impact_assessment: dependency.assessment,
    impact_groups: dependency.grouping,
    impact_tasks: dependency.tasks,
    impact_tasks_after_replay: dependency.tasksAfterReplay,
    unassigned_completion: dependency.unassignedCompletion,
    task_transitions: dependency.transitions,
    dependency_decisions: dependency.decisions,
    dependency_candidates: dependency.candidates,
    suggested_edge: dependency.suggestedEdge,
    suggested_target_version_confirmation_valid: dependency.suggestedTargetVersionConfirmationValid,
    known_objects: dependency.knownObjects,
    verdicts: dependency.verdicts,
    steps: chain.ctx.steps,
  }

  mkdirSync(dirname(OUT_PATH), { recursive: true })
  writeFileSync(OUT_PATH, `${JSON.stringify(evidence, null, 2)}\n`, 'utf8')

  console.log('')
  console.log('[g3demo] 逐步结果：')
  for (const step of chain.ctx.steps) {
    console.log(
      `  ${String(step.index).padStart(2, '0')} ${step.method.padEnd(4)} ${step.path}` +
        ` → ${step.status}（${step.durationMs}ms）` +
        `${step.idempotencyKey ? ` 幂等键=${step.idempotencyKey}` : ' 无幂等键'}` +
        `${step.ok ? '' : ' （未达成预期）'}`,
    )
  }
  console.log('')
  console.log('[g3demo] 三组呈现：')
  console.log(`  确定受影响：${dependency.grouping.definite.length} 条`)
  console.log(`  可能受影响：${dependency.grouping.possible.length} 条`)
  console.log(`  当前未发现影响：${dependency.grouping.unaffected.length} 个对象`)
  console.log(`  必需对象集合：${dependency.grouping.requiredObjects.length} 个`)
  console.log('')
  console.log('[g3demo] 依赖候选三态（AC-G3-15）：')
  const candidates = dependency.candidates
  for (const decision of candidates.decisions) {
    console.log(
      `  ${decision.decision} → HTTP ${decision.httpStatus} · status=${decision.status || '（无）'}` +
        ` · edge_id=${decision.edgeId || '（无）'}` +
        `${decision.code ? ` · code=${decision.code}` : ''}`,
    )
  }
  console.log(
    `  拒绝不产生边：依赖边 ${candidates.edgesBeforeDecisions} → ${candidates.edgesAfterDecisions} 条` +
      `（确认 + 修改各一条）`,
  )
  console.log(
    `  同版本不再重提：新建 ${candidates.reproposal?.candidates.length ?? 0} 条 · ` +
      `抑制 ${candidates.reproposal?.suppressed.length ?? 0} 条` +
      `（原因 ${candidates.reproposal?.suppressed.map((item) => item.reason).join('、') || '（无）'}）`,
  )
  console.log(
    `  确认时版本复检：HTTP ${candidates.staleConfirmation?.httpStatus ?? '（未执行）'} · ` +
      `code=${candidates.staleConfirmation?.code || '（无）'}`,
  )
  console.log('')
  console.log('[g3demo] 判定：')
  for (const verdict of dependency.verdicts) {
    console.log(`  ${verdict.id} ${verdict.holds ? '成立' : '不成立'} —— ${verdict.detail}`)
    for (const line of verdict.evidence) {
      console.log(`      ${line}`)
    }
  }
  console.log('')
  console.log(`[g3demo] 后端 head：${chain.ctx.head?.commitId ?? '未读到'}`)
  console.log(`[g3demo] 项目 id：${chain.project.id}`)
  console.log(`[g3demo] 证据落盘：${OUT_PATH}`)
}

main().catch((error: unknown) => {
  const message = error instanceof Error ? error.message : String(error)
  const detail = error as {
    step?: string
    path?: string
    status?: number
    code?: string
    runContext?: { steps: unknown[] }
  }
  console.error(`[g3demo] 运行失败：${message}`)
  if (detail?.step) {
    console.error(`[g3demo] 失败步骤：${detail.step} · ${detail.path} · HTTP ${detail.status} · code=${detail.code}`)
  }
  const steps = detail?.runContext?.steps as
    | { index: number; name: string; method: string; path: string; status: number; ok: boolean; conclusion: string }[]
    | undefined
  if (steps) {
    console.error('[g3demo] 已跑过的步骤：')
    for (const step of steps) {
      console.error(
        `  ${String(step.index).padStart(2, '0')} ${step.method.padEnd(4)} ${step.path} → ${step.status} ${step.ok ? '' : '✗'} ${step.conclusion}`,
      )
    }
  }
  process.exitCode = 1
})
