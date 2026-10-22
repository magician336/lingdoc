/**
 * G3 验收台-3 · 变更集生命周期与并发保护：对真实后端跑一遍本票链路，把证据 JSON 落盘。
 *
 * 为什么要有一个 node 脚本：验收台的真实证据**不必**依赖浏览器自动化
 * （本仓库没有 Playwright / Puppeteer，浏览器级 runner 成本高且本轮不新增）。
 * 执行器是纯 TS、注入式 HTTP 客户端，所以同一份执行器既能被页面调用，
 * 也能在这里被直接调用——两处跑的是同一套代码，证据不会被复制粘贴走样。
 *
 * 用法（在 `frontend/` 目录下）：
 *   npx tsx scripts/impactChains/runChangeset.ts
 *   npx tsx scripts/impactChains/runChangeset.ts --base http://127.0.0.1:8080 --out ../docs/08-本轮实施方案/xxx.json
 *
 * 环境变量：
 *   G3_BASE_URL  后端地址，默认 http://127.0.0.1:8080
 *   G3_EVIDENCE_OUT  证据落盘路径，默认 <repo>/docs/08-本轮实施方案/G3-验收台-3-变更集生命周期证据-<日期>.json
 */

import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { runChain } from '../../src/views/lingdoc/impactCenterAcceptance/chains/chainExecution'
import { runChangeSetChain } from '../../src/views/lingdoc/impactCenterAcceptance/chains/changeSetChain'
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

// --out 给的是相对路径时按当前工作目录解析；不给时落到仓库的 docs 目录。
const outArg = argOf('--out')
const OUT_PATH = outArg
  ? resolve(process.cwd(), outArg)
  : resolve(
    REPO_ROOT,
    process.env.G3_EVIDENCE_OUT ??
      `docs/08-本轮实施方案/G3-验收台-3-变更集生命周期证据-${dateStamp()}.json`,
  )

async function main(): Promise<void> {
  // 账号文件放在系统临时目录里，**不进仓库**：口令不能落进版本库。
  const accountFile = resolve(argOf('--account-file') ?? resolve(tmpdir(), 'impactChains-account.json'))
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

  console.log(`[impactChains] 前置链完成：项目 ${chain.project.id} · 章节 ${chain.chapters.map((c) => c.sectionId).join('、')}`)

  const lifecycle = await runChangeSetChain(chain.ctx, client, chain.token, {
    projectId: chain.project.id,
    chapters: chain.chapters.map((chapter) => ({
      id: chapter.id,
      sectionId: chapter.sectionId,
      title: chapter.title,
    })),
    tenantId: chain.identity.tenantId,
  })

  const evidence = {
    generated_at: new Date().toISOString(),
    backend_base_url: BASE_URL,
    run_id: chain.ctx.runId,
    started_at: chain.ctx.startedAt,
    ticket: 'G3 验收台-3：变更集生命周期与并发保护',
    demo_account: { username: account.username, email: account.email },
    account_reused: !registerFirst,
    backend_head: chain.ctx.head,
    identity: chain.identity,
    project: chain.project,
    chapters: chain.chapters,
    change_sets: {
      scoped: lifecycle.scoped,
      concurrent: lifecycle.concurrent,
      reversed: lifecycle.reversed,
      reassessed: lifecycle.reassessed,
    },
    impact_assessment: lifecycle.assessment,
    reverse_impact_assessment: lifecycle.reverseAssessment,
    chapter_states: {
      affected_before: lifecycle.affectedBefore,
      affected_after: lifecycle.affectedAfter,
      affected_reconfirmed: lifecycle.affectedReconfirmed,
      unaffected_before: lifecycle.unaffectedBefore,
      unaffected_after: lifecycle.unaffectedAfter,
    },
    unaffected_reason: lifecycle.unaffected,
    context_revisions: {
      before: lifecycle.revisionsBefore,
      after: lifecycle.revisionsAfter,
    },
    impact_tasks: {
      before: lifecycle.tasksBefore,
      after: lifecycle.tasksAfter,
    },
    permission_probe: lifecycle.permissionProbe ?? null,
    verdicts: lifecycle.verdicts,
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
  console.log('')
  console.log('[impactChains] 判定：')
  for (const verdict of lifecycle.verdicts) {
    console.log(`  ${verdict.id} ${verdict.holds ? '成立' : '不成立'} —— ${verdict.detail}`)
    for (const line of verdict.evidence) {
      console.log(`      ${line}`)
    }
  }
  if (lifecycle.permissionProbe) {
    console.log(
      `  非 Owner 权限探测：读 HTTP ${lifecycle.permissionProbe.read.status} · 应用 HTTP ${lifecycle.permissionProbe.apply.status} ${lifecycle.permissionProbe.apply.code} · 拒绝 HTTP ${lifecycle.permissionProbe.reject.status} ${lifecycle.permissionProbe.reject.code}`,
    )
  }
  console.log('')
  console.log(`[impactChains] 后端 head：${chain.ctx.head?.commitId ?? '未读到'}`)
  console.log(`[impactChains] 项目 id：${chain.project.id}`)
  console.log(`[impactChains] 证据落盘：${OUT_PATH}`)
}

main().catch((error: unknown) => {
  const message = error instanceof Error ? error.message : String(error)
  const detail = error as { step?: string; path?: string; status?: number; code?: string; runContext?: { steps: unknown[] } }
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
      console.error(`  ${String(step.index).padStart(2, '0')} ${step.method.padEnd(4)} ${step.path} → ${step.status} ${step.ok ? '' : '✗'} ${step.conclusion}`)
    }
  }
  process.exitCode = 1
})
