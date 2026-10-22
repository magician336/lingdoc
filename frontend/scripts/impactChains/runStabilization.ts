/**
 * G3 验收台-5 · 稳定化与交付闭环：对真实后端跑一遍本票链路，把证据 JSON 落盘。
 *
 * 与 #238 / #239 / #240 同一形态：执行器是纯 TS、注入式 HTTP 客户端，页面与本脚本
 * 跑的是**同一份**执行器，所以页面上的判定与落盘的证据不会走样。
 *
 * 用法（在 `frontend/` 目录下）：
 *   npx tsx scripts/impactChains/runStabilization.ts
 *   npx tsx scripts/impactChains/runStabilization.ts --base http://127.0.0.1:8080 --out ../docs/08-本轮实施方案/xxx.json
 *
 * 环境变量：
 *   G3_BASE_URL  后端地址，默认 http://127.0.0.1:8080
 *   G3_EVIDENCE_OUT  证据落盘路径，默认 <repo>/docs/08-本轮实施方案/G3-验收台-5-稳定化与交付闭环证据-<日期>.json
 */

import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { runChain } from '../../src/views/lingdoc/impactCenterAcceptance/chains/chainExecution'
import {
  G3StabilizationChainError,
  runStabilizationChain,
  type G3StabilizationChainResult,
} from '../../src/views/lingdoc/impactCenterAcceptance/chains/stabilizationChain'
import { createDemoAccount, type G3DemoAccount } from '../../src/views/lingdoc/impactCenterAcceptance/chains/demoIdentity'
import { createRunId } from '../../src/views/lingdoc/impactCenterAcceptance/chains/runContext'
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
      `docs/08-本轮实施方案/G3-验收台-5-稳定化与交付闭环证据-${dateStamp()}.json`,
  )

async function main(): Promise<void> {
  // 账号文件放在系统临时目录里，**不进仓库**：口令不能落进版本库。
  const accountFile = resolve(argOf('--account-file') ?? resolve(tmpdir(), 'impactChains-account-t5.json'))
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
  console.log(`[impactChains] 后端：${BASE_URL}`)
  console.log(`[impactChains] 运行标识：${runId}`)
  console.log(`[impactChains] 演示账号：${account.email}（${registerFirst ? '本次自建' : '复用本地账号'}）`)

  const chain = await runChain(client, { account, registerFirst, runId })
  if (!reuse) {
    writeFileSync(accountFile, `${JSON.stringify(account)}\n`, 'utf8')
  }
  console.log(`[impactChains] 前置链完成：项目 ${chain.project.id}`)

  let result: G3StabilizationChainResult | null = null
  let failure = ''
  try {
    result = await runStabilizationChain(chain.ctx, client, chain.token, {
      projectId: chain.project.id,
      chapters: chain.chapters.map((chapter) => ({
        id: chapter.id,
        sectionId: chapter.sectionId,
        title: chapter.title,
      })),
      operatorId: chain.identity.userId,
    })
  } catch (error) {
    failure = error instanceof Error ? error.message : String(error)
    if (error instanceof G3StabilizationChainError) {
      result = error.partial as G3StabilizationChainResult
    }
    console.error(`[impactChains] 链路中断：${failure}`)
  }

  const evidence = {
    generated_at: new Date().toISOString(),
    backend_base_url: BASE_URL,
    run_id: chain.ctx.runId,
    started_at: chain.ctx.startedAt,
    ticket: 'G3 验收台-5：稳定化与交付闭环',
    demo_account: { username: account.username, email: account.email },
    account_reused: !registerFirst,
    backend_head: chain.ctx.head,
    identity: chain.identity,
    project: chain.project,
    chapters: chain.chapters,
    failure,
    stabilization: result
      ? {
        change_set: result.changeSet,
        concurrent_change_set: result.concurrentChangeSet,
        stale_apply: result.staleApply,
        context_revisions_before: result.contextRevisionsBefore,
        context_revisions_after: result.contextRevisionsAfter,
        tasks_before_stale_apply: result.tasksBeforeStaleApply,
        tasks_after_stale_apply: result.tasksAfterStaleApply,
        blocked: result.blocked,
        freeze_before_stable: result.freezeBeforeStable,
        stabilized: result.stabilized,
        delivery_check: result.check,
        template_check: result.templateCheck,
        check_receipt: result.receipt,
        release_snapshot: result.snapshot,
        release_list: result.releaseList,
        export_accepted: result.exportAccepted,
        export_artifact: result.exportArtifact,
        export_poll_statuses: result.pollStatuses,
        download: result.download,
        task_transitions: result.transitions,
        verdicts: result.verdicts,
      }
      : null,
    steps: chain.ctx.steps,
  }

  mkdirSync(dirname(OUT_PATH), { recursive: true })
  writeFileSync(OUT_PATH, `${JSON.stringify(evidence, null, 2)}\n`, 'utf8')

  console.log('')
  console.log('[impactChains] 逐步结果：')
  for (const step of chain.ctx.steps) {
    console.log(
      `  ${String(step.index).padStart(2, '0')} ${step.method.padEnd(4)} ${step.path}` +
        ` → ${step.status}（${step.durationMs}ms）` +
        `${step.idempotencyKey ? ` 幂等键=${step.idempotencyKey}` : ' 无幂等键'}` +
        `${step.ok ? '' : ' （未达成预期）'}`,
    )
  }

  if (result) {
    console.log('')
    console.log('[impactChains] 稳定化（阻断态）：')
    console.log(`  状态：${result.blocked.status} · 检查时刻：${result.blocked.checkedAt || '（服务端未给）'}`)
    for (const blocker of result.blocked.blockers) {
      console.log(`  阻断原因：${blocker.code}｜对象 ${blocker.targetId}｜原因 ${blocker.reason}`)
    }
    console.log(`  失效任务：${result.blocked.staleTaskIds.length} 条`)
    console.log(`  稳定化前冻结：HTTP ${result.freezeBeforeStable.status} · code=${result.freezeBeforeStable.code}`)
    console.log('')
    console.log('[impactChains] 稳定化（闭环后）：')
    console.log(`  状态：${result.stabilized.status} · 检查时刻：${result.stabilized.checkedAt || '（服务端未给）'}`)
    console.log(`  阻断原因：${result.stabilized.blockers.length} 条`)
    console.log('')
    console.log('[impactChains] 交付检查与导出：')
    console.log(`  交付检查：${result.check.status} · 回执 ${result.check.receiptId || '（无）'}`)
    console.log(`  快照：${result.snapshot.id} · check.status=${result.snapshot.checkStatus} · is_current=${result.snapshot.isCurrent}`)
    console.log(`  受理：HTTP ${result.exportAccepted.status} · 产物状态序列：${result.pollStatuses.join(' → ')}`)
    console.log(`  产物哈希：${result.exportArtifact.fileSha256 || '（无）'}`)
    if (result.exportArtifact.errorCode) {
      console.log(`  产物失败码：${result.exportArtifact.errorCode} · ${result.exportArtifact.errorMessage}`)
    }
    console.log(`  下载：${result.download.size} 字节 · sha256=${result.download.sha256} · 与产物一致=${result.download.matches}`)
    console.log('')
    console.log('[impactChains] 判定：')
    for (const verdict of result.verdicts) {
      console.log(`  ${verdict.id} ${verdict.holds ? '成立' : '不成立'} —— ${verdict.detail}`)
      for (const line of verdict.evidence) {
        console.log(`      ${line}`)
      }
    }
  }

  console.log('')
  console.log(`[impactChains] 后端 head：${chain.ctx.head?.commitId ?? '未读到'}`)
  console.log(`[impactChains] 项目 id：${chain.project.id}`)
  console.log(`[impactChains] 证据落盘：${OUT_PATH}`)
  if (failure) {
    process.exitCode = 1
  }
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
  console.error(`[impactChains] 运行失败：${message}`)
  if (detail?.step) {
    console.error(`[impactChains] 失败步骤：${detail.step} · ${detail.path} · HTTP ${detail.status} · code=${detail.code}`)
  }
  const steps = detail?.runContext?.steps as
    | { index: number; name: string; method: string; path: string; status: number; ok: boolean; conclusion: string }[]
    | undefined
  if (steps) {
    console.error('[impactChains] 已跑过的步骤：')
    for (const step of steps) {
      console.error(
        `  ${String(step.index).padStart(2, '0')} ${step.method.padEnd(4)} ${step.path} → ${step.status} ${step.ok ? '' : '✗'} ${step.conclusion}`,
      )
    }
  }
  process.exitCode = 1
})
