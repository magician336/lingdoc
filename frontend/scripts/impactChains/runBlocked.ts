/**
 * G3 验收台-6 · 稳定化阻断与恢复路径：对真实后端跑一遍本票链路，把证据 JSON 落盘。
 *
 * 与 #238 / #239 / #240 / #241 同一形态：执行器是纯 TS、注入式 HTTP 客户端，页面与本脚本
 * 跑的是**同一份**执行器，所以页面上的判定与落盘的证据不会走样。
 *
 * 用法（在 `frontend/` 目录下）：
 *   npx tsx scripts/impactChains/runBlocked.ts
 *   npx tsx scripts/impactChains/runBlocked.ts --base http://127.0.0.1:8080 --out ../docs/08-本轮实施方案/xxx.json
 *
 * 环境变量：
 *   G3_BASE_URL  后端地址，默认 http://127.0.0.1:8080
 *   G3_EVIDENCE_OUT  证据落盘路径，默认 <repo>/docs/08-本轮实施方案/G3-验收台-6-阻断与恢复路径证据-<日期>.json
 */

import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { runChain } from '../../src/views/lingdoc/g3demo/runner/chain'
import {
  G3BlockedChainError,
  runBlockedChain,
  type G3BlockedChainResult,
} from '../../src/views/lingdoc/g3demo/runner/blocked'
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
      `docs/08-本轮实施方案/G3-验收台-6-阻断与恢复路径证据-${dateStamp()}.json`,
  )

async function main(): Promise<void> {
  // 账号文件放在系统临时目录里，**不进仓库**：口令不能落进版本库。
  const accountFile = resolve(argOf('--account-file') ?? resolve(tmpdir(), 'g3demo-account-t6.json'))
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

  let result: G3BlockedChainResult | null = null
  let failure = ''
  try {
    result = await runBlockedChain(chain.ctx, client, chain.token, {
      projectId: chain.project.id,
      chapters: chain.chapters.map((chapter) => ({
        id: chapter.id,
        sectionId: chapter.sectionId,
        title: chapter.title,
      })),
      operatorId: chain.identity.userId,
      runReviewProbe: true,
    })
  } catch (error) {
    failure = error instanceof Error ? error.message : String(error)
    if (error instanceof G3BlockedChainError) {
      result = error.partial as G3BlockedChainResult
    }
    console.error(`[g3demo] 链路中断：${failure}`)
  }

  const evidence = {
    generated_at: new Date().toISOString(),
    backend_base_url: BASE_URL,
    run_id: chain.ctx.runId,
    started_at: chain.ctx.startedAt,
    ticket: 'G3 验收台-6：稳定化阻断与恢复路径',
    demo_account: { username: account.username, email: account.email },
    account_reused: !registerFirst,
    backend_head: chain.ctx.head,
    identity: chain.identity,
    project: chain.project,
    chapters: chain.chapters,
    failure,
    blocked_chain: result
      ? {
        change_set: result.changeSet,
        reissued_change_set: result.reissuedChangeSet,
        blocked: result.blocked,
        blocked_lines: result.blockedLines,
        after_disposition: result.afterDisposition,
        stabilized: result.stabilized,
        version_mismatch: result.versionMismatch,
        mismatch_status: result.mismatchStatus,
        mismatch_code: result.mismatchCode,
        final_stabilized: result.finalStabilized,
        check_before: result.checkBefore,
        check_after: result.checkAfter,
        disposition: result.disposition,
        dismiss_attempts: result.dismissAttempts,
        task_transitions: result.transitions,
        stale_tasks: result.staleTasks,
        reissued_task_ids: result.reissuedTaskIds,
        review_probe: result.reviewProbe,
        verdicts: result.verdicts,
      }
      : null,
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

  if (result) {
    console.log('')
    console.log('[g3demo] 稳定化（阻断态）：')
    console.log(`  状态：${result.blocked.status} · 检查时刻：${result.blocked.checkedAt || '（服务端未给）'}`)
    for (const blocker of result.blocked.blockers) {
      console.log(`  阻断原因：${blocker.code}｜对象 ${blocker.targetId}｜原因 ${blocker.reason}`)
    }
    console.log(`  失效任务：${result.blocked.staleTaskIds.length} 条`)
    console.log('')
    console.log('[g3demo] 规则问题处置：')
    console.log(`  交付检查（处置前）：${result.checkBefore.status} · 问题 ${result.checkBefore.issues.length} 条`)
    console.log(`  交付检查（处置后）：${result.checkAfter.status} · 问题 ${result.checkAfter.issues.length} 条`)
    if (result.disposition) {
      console.log(
        `  dismiss → HTTP ${result.disposition.dismissStatus}（${result.disposition.dismissCode || '无码'}）· ` +
          `resolve → HTTP ${result.disposition.resolveStatus}（${result.disposition.resolveCode || '无码'}）· ` +
          `问题带处置=${result.disposition.carriesDisposition}`,
      )
    }
    console.log('')
    console.log('[g3demo] 接受不处理的边界：')
    for (const attempt of result.dismissAttempts) {
      console.log(
        `  ${attempt.label}（severity=${attempt.severity}）→ HTTP ${attempt.status}` +
          `（${attempt.code || '无码'}）· 最终状态 ${attempt.finalStatus}`,
      )
    }
    console.log('')
    console.log('[g3demo] 稳定化的四次结果：')
    console.log(`  ① 阻断态：${result.blocked.status}（阻断原因 ${result.blocked.blockers.length} 条）`)
    console.log(`  ② 规则问题处置后：${result.afterDisposition.status}（阻断原因 ${result.afterDisposition.blockers.length} 条）`)
    console.log(`  ③ 阻断项闭环后：${result.stabilized.status}`)
    console.log(
      `  ④ 目标版本被替换后：${result.versionMismatch.status}（阻断原因 ` +
        `${result.versionMismatch.blockers.map((item) => item.code).join('、')}）`,
    )
    console.log(`  ⑤ 重投闭环后：${result.finalStabilized.status}`)
    console.log(`  失效任务：${result.staleTasks.length} 条 · 重投任务：${result.reissuedTaskIds.length} 条`)
    if (result.reviewProbe) {
      console.log('')
      console.log('[g3demo] 人工复核探针：')
      console.log(`  复核任务 ${result.reviewProbe.taskId}（${result.reviewProbe.taskAction} / ${result.reviewProbe.targetKind}）`)
      console.log(
        `  缺 human_decision → HTTP ${result.reviewProbe.completeWithoutReviewStatus}` +
          `（${result.reviewProbe.completeWithoutReviewCode || '无码'}）`,
      )
      console.log(
        `  带 human_decision → HTTP ${result.reviewProbe.completeWithReviewStatus} · ` +
          `已闭环=${result.reviewProbe.completeWithReviewAccepted}`,
      )
    }
    console.log('')
    console.log('[g3demo] 判定：')
    for (const verdict of result.verdicts) {
      console.log(`  ${verdict.id} ${verdict.holds ? '成立' : '不成立'} —— ${verdict.detail}`)
      for (const line of verdict.evidence) {
        console.log(`      ${line}`)
      }
    }
  }

  console.log('')
  console.log(`[g3demo] 后端 head：${chain.ctx.head?.commitId ?? '未读到'}`)
  console.log(`[g3demo] 项目 id：${chain.project.id}`)
  console.log(`[g3demo] 证据落盘：${OUT_PATH}`)
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
