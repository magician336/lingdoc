/**
 * G3 验收台-2 · 真实后端跑一遍本票链路，把证据 JSON 落盘。
 *
 * 为什么要有一个 node 脚本：验收台的真实证据**不必**依赖浏览器自动化
 * （本仓库没有 Playwright / Puppeteer，浏览器级 runner 成本高且本轮不新增）。
 * 执行器是纯 TS、注入式 HTTP 客户端，所以同一份执行器既能被页面调用，
 * 也能在这里被直接调用——两处跑的是同一套代码，证据不会被复制粘贴走样。
 *
 * 用法（在 `frontend/` 目录下）：
 *   npx tsx scripts/impactChains/runChain.ts
 *   npx tsx scripts/impactChains/runChain.ts --base http://127.0.0.1:8080 --out ../docs/08-本轮实施方案/xxx.json
 *
 * 环境变量：
 *   G3_BASE_URL  后端地址，默认 http://127.0.0.1:8080
 *   G3_EVIDENCE_OUT  证据落盘路径，默认 <repo>/docs/08-本轮实施方案/G3-验收台-2-真实链路证据-<日期>.json
 */

import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { runChain } from '../../src/views/lingdoc/g3demo/runner/chain'
import { createDemoAccount, type G3DemoAccount } from '../../src/views/lingdoc/g3demo/runner/identity'
import { createRunId } from '../../src/views/lingdoc/g3demo/runner/runContext'
import type { G3HttpClient, G3HttpRequest, G3HttpResponse } from '../../src/views/lingdoc/g3demo/runner/httpClient'

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
        `docs/08-本轮实施方案/G3-验收台-2-真实链路证据-${dateStamp()}.json`,
    )

/** fetch 风格的 HTTP 客户端。 */
const client: G3HttpClient = async (request: G3HttpRequest): Promise<G3HttpResponse> => {
  const started = performance.now()
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (request.body !== undefined) headers['Content-Type'] = 'application/json'
  if (request.token && !request.anonymous) headers.Authorization = `Bearer ${request.token}`
  if (request.idempotencyKey) headers['Idempotency-Key'] = request.idempotencyKey

  const response = await fetch(BASE_URL + request.path, {
    method: request.method,
    headers,
    body: request.body === undefined ? undefined : JSON.stringify(request.body),
  })
  const text = await response.text()
  let body: unknown = text
  try {
    body = text ? JSON.parse(text) : {}
  } catch {
    // 不是 JSON 就保留原文。
  }
  return {
    status: response.status,
    ok: response.ok,
    body,
    durationMs: Math.round(performance.now() - started),
  }
}

async function main(): Promise<void> {
  // 账号文件放在系统临时目录里，**不进仓库**：口令不能落进版本库。
  const accountFile = resolve(argOf('--account-file') ?? resolve(tmpdir(), 'g3demo-account.json'))
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

  const runId = createRunId()
  console.log(`[g3demo] 后端：${BASE_URL}`)
  console.log(`[g3demo] 运行标识：${runId}`)
  console.log(`[g3demo] 演示账号：${account.email}（${registerFirst ? '本次自建' : '复用本地账号'}）`)

  const result = await runChain(client, { account, registerFirst, runId })

  if (!reuse) {
    writeFileSync(accountFile, `${JSON.stringify(account)}\n`, 'utf8')
  }

  const evidence = {
    generated_at: new Date().toISOString(),
    backend_base_url: BASE_URL,
    run_id: result.ctx.runId,
    started_at: result.ctx.startedAt,
    demo_account: { username: account.username, email: account.email },
    account_reused: !registerFirst,
    backend_head: result.ctx.head,
    identity: result.identity,
    project: result.project,
    chapters: result.chapters,
    missing_sections: result.missingSections,
    steps: result.ctx.steps,
  }

  mkdirSync(dirname(OUT_PATH), { recursive: true })
  writeFileSync(OUT_PATH, `${JSON.stringify(evidence, null, 2)}\n`, 'utf8')

  console.log('')
  console.log('[g3demo] 逐步结果：')
  for (const step of result.ctx.steps) {
    console.log(
      `  ${String(step.index).padStart(2, '0')} ${step.method.padEnd(4)} ${step.path}` +
        ` → ${step.status}（${step.durationMs}ms）` +
        `${step.idempotencyKey ? ` 幂等键=${step.idempotencyKey}` : ' 无幂等键'}`,
    )
  }
  console.log('')
  console.log(`[g3demo] 后端 head：${result.ctx.head?.commitId ?? '未读到'}`)
  console.log(`[g3demo] 项目 id：${result.project.id}`)
  console.log(`[g3demo] 项目状态：${result.project.status}`)
  console.log(
    `[g3demo] 激活后的章节：${result.chapters.map((c) => `${c.sectionId}(${c.id})`).join('、')}`,
  )
  console.log(`[g3demo] 必需章节缺失：${result.missingSections.length ? result.missingSections.join('、') : '无'}`)
  console.log(`[g3demo] 证据落盘：${OUT_PATH}`)
}

main().catch((error: unknown) => {
  const message = error instanceof Error ? error.message : String(error)
  console.error(`[g3demo] 运行失败：${message}`)
  process.exitCode = 1
})
