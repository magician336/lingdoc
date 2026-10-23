// Browser-side executor of the 54-Spec dual runner (real browser x real backend).
//
// What this file is
// -----------------
// The HTTP side (scripts/acceptance/beta_http_readback.py) reads the real
// backend over HTTP.  This file reads the *same* project out of a real
// browser, driving the real LingDoc front end against the real backend --
// and then reads it again after a page reload.  The three observations are
// compared afterwards by scripts/acceptance/beta_evidence_parity.py.
//
// Why the values are read out of the DOM
// --------------------------------------
// The point of this face is the assertion "界面显示的 == 接口返回的".  A
// value that the page never renders cannot be compared, so every item below
// is read from the rendered delivery panel
// (frontend/src/views/lingdoc/DeliveryPanel.vue).  Values the panel renders
// truncated (digest, ids) are compared in their truncated form, exactly like
// G4 compared the "9eb2c49a5abe..." prefix.
//
// Connection technique
// --------------------
// Spawn a headless browser -> read DevToolsActivePort -> GET /json/list ->
// take the page target's webSocketDebuggerUrl -> talk CDP over Node's
// built-in WebSocket.  This is the technique of
// scripts/lingdoc_mock/browser_smoke.mjs; the real-input part (native mouse
// and Input.insertText) follows frontend/tests/editor-slice.browser.mjs.
//
// Evidence boundary (Implementation Decision 8)
// --------------------------------------------
// No token, no password, no request body and no response body reach the
// snapshot -- only identifiers, counters and status words.
//
// Usage
// -----
//   node scripts/acceptance/beta_browser_cdp.mjs --url <URL> --out <browser.json>
//   node scripts/acceptance/beta_browser_cdp.mjs --list-items
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { setTimeout as sleep } from 'node:timers/promises'

// The compared item set.  The HTTP side declares the same list; the guard
// test runs `--list-items` on both sides and fails when they drift apart.
const ITEM_KEYS = [
  'browser_snapshot_digest_matches_http',
  'browser_export_id_matches_http',
  'browser_export_sha_matches_http',
  'project_version_matches',
  'snapshot_check_status',
  'snapshot_id_matches',
  'spec_revision_matches',
  'chapters_in_snapshot_matches',
  'sources_in_snapshot_matches',
  'current_check_status_matches',
  'browser_candidate_adoption_state_matches',
  'browser_candidate_cancelled_state_matches',
]

// Items this face cannot run without a real model.  Same wording as the
// HTTP side so the two reports agree on why the item is missing.
const MODEL_GATED_ITEMS = {
  browser_candidate_adoption_state_matches: {
    reason: '候选采纳的读回需要一份真实生成的候选稿；本面不跑真实模型（54-Spec 决策 9）。',
    unblock: '起真实模型（55-Spec / Beta 协议 §2 的模型账本）后重跑 HTTP 侧与浏览器侧。',
  },
  browser_candidate_cancelled_state_matches: {
    reason: '候选取消的读回需要一份真实生成的候选稿；本面不跑真实模型（54-Spec 决策 9）。',
    unblock: '起真实模型（55-Spec / Beta 协议 §2 的模型账本）后重跑 HTTP 侧与浏览器侧。',
  },
}

const DIGEST_PREFIX = 12
const ID_PREFIX = 8
const SHA_PREFIX = 12

function parseArgs(argv) {
  const args = { timeout: 90000 }
  for (let index = 0; index < argv.length; index += 1) {
    const token = argv[index]
    if (!token.startsWith('--')) continue
    const name = token.slice(2)
    if (name === 'list-items') { args['list-items'] = true; continue }
    if (name === 'help') { args.help = true; continue }
    const value = argv[index + 1]
    if (value === undefined || value.startsWith('--')) throw new Error(`missing value for --${name}`)
    args[name] = value
    index += 1
  }
  return args
}

const USAGE = `usage: beta_browser_cdp.mjs --url <URL> --out <browser.json> [options]

options:
  --url <URL>        base URL of the real LingDoc front end (e.g. http://127.0.0.1:5173)
  --out <PATH>       where to write the browser-side snapshot JSON
  --context <PATH>   identity/project hand-off file written by the HTTP side
  --browser-bin <P>  Chrome/Chromium/Edge binary (default: auto-detected, BETA_BROWSER_BIN)
  --timeout <MS>     per-step timeout in milliseconds (default: 90000)
  --list-items       print the compared item key set as JSON and exit
  --help             print this help and exit
`

function nowIso() {
  return new Date().toISOString().replace(/\.\d{3}Z$/, 'Z')
}

function browserCandidates() {
  const listed = []
  if (process.env.BETA_BROWSER_BIN) listed.push(process.env.BETA_BROWSER_BIN)
  listed.push(
    'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
    'C:/Program Files/Microsoft/Edge/Application/msedge.exe',
    'C:/Program Files/Google/Chrome/Application/chrome.exe',
    'C:/Program Files (x86)/Google/Chrome/Application/chrome.exe',
    'google-chrome',
    'chrome',
    'msedge',
  )
  return listed
}

function resolveBrowserBinary() {
  for (const candidate of browserCandidates()) {
    if (candidate.includes('/') || candidate.includes('\\')) {
      if (existsSync(candidate)) return candidate
      continue
    }
    return candidate // rely on PATH
  }
  throw new Error('no Chrome/Chromium/Edge binary found; set BETA_BROWSER_BIN')
}

/**
 * Read the delivery panel out of the rendered page.
 *
 * This function is stringified and evaluated inside the browser, so it must
 * not close over anything from this module.
 */
function extractDelivery() {
  const statusFromLabel = (label) => {
    const map = { '通过': 'passed', '已阻断': 'blocked', '未评估': 'not_evaluated' }
    const key = String(label === undefined || label === null ? '' : label).trim()
    return Object.prototype.hasOwnProperty.call(map, key) ? map[key] : key
  }
  const list = document.querySelector('.delivery__snapshot')
  const texts = Array.from(document.querySelectorAll('p')).map((node) => node.textContent || '')
  const frozen = texts.find((text) => text.includes('冻结于项目版本')) || ''
  const frozenCheck = texts.find((text) => text.includes('冻结时的交付检查')) || ''
  const currentCheck = document.querySelector('.delivery__status')
  const currentText = currentCheck ? (currentCheck.textContent || '') : ''
  let exportId = null
  let exportSha = null
  if (list) {
    const artifact = list.querySelector('.delivery__artifact-id')
    if (artifact) exportId = (artifact.textContent || '').trim()
    const small = Array.from(list.querySelectorAll('small'))
      .map((node) => node.textContent || '')
      .find((text) => text.includes('摘要'))
    if (small) {
      const matched = small.match(/摘要\s*([0-9a-fA-F]+)/)
      if (matched) exportSha = matched[1]
    }
  }
  const snapshotId = list && list.querySelector('strong')
    ? (list.querySelector('strong').textContent || '').replace(/^快照\s*/, '').trim()
    : null
  const pick = (pattern, text) => {
    const matched = String(text || '').match(pattern)
    return matched ? matched[1] : null
  }
  const frozenLabel = pick(/冻结时的交付检查：\s*([^·\n]+)/, frozenCheck)
  const currentLabel = pick(/当前检查结果：\s*([^（(\n]+)/, currentText)
  return {
    found: Boolean(list),
    snapshot_id: snapshotId,
    project_version: pick(/冻结于项目版本\s*(\d+)/, frozen),
    spec_revision: pick(/研究条件第\s*(\d+)\s*版/, frozen),
    chapters: pick(/章节\s*(\d+)\s*章/, frozen),
    sources: pick(/资料\s*(\d+)\s*份/, frozen),
    digest: pick(/摘要\s*([0-9a-fA-F]+)/, frozen),
    check_status: statusFromLabel(frozenLabel),
    export_id: exportId,
    export_sha: exportSha,
    current_check_status: statusFromLabel(currentLabel),
  }
}

class CdpSession {
  constructor(socket) {
    this.socket = socket
    this.next = 0
    this.waiting = new Map()
    this.browserErrors = []
    socket.addEventListener('message', ({ data }) => {
      const message = JSON.parse(data)
      if (message.method === 'Runtime.exceptionThrown') {
        this.browserErrors.push(message.params.exceptionDetails?.text || 'exception')
      }
      const job = this.waiting.get(message.id)
      if (!job) return
      clearTimeout(job.timer)
      this.waiting.delete(message.id)
      if (message.error) job.reject(new Error(JSON.stringify(message.error)))
      else job.resolve(message.result)
    })
  }

  call(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = ++this.next
      const timer = setTimeout(() => {
        this.waiting.delete(id)
        reject(new Error('CDP timeout: ' + method))
      }, 30000)
      this.waiting.set(id, { resolve, reject, timer })
      this.socket.send(JSON.stringify({ id, method, params }))
    })
  }

  async evaluate(expression) {
    const value = await this.call('Runtime.evaluate', {
      expression, returnByValue: true, awaitPromise: true, userGesture: true,
    })
    if (value.exceptionDetails) {
      throw new Error(value.exceptionDetails.exception?.description || value.exceptionDetails.text)
    }
    return value.result.value
  }

  async waitFor(expression, label, timeoutMs) {
    const deadline = Date.now() + timeoutMs
    for (;;) {
      let value = false
      try {
        value = await this.evaluate(expression)
      } catch (error) {
        value = false
      }
      if (value) return value
      if (Date.now() > deadline) throw new Error('timed out waiting for ' + label)
      await sleep(150)
    }
  }

  async click(selectorExpression, label) {
    const point = await this.evaluate(`(() => { const node = ${selectorExpression}; if (!node) return null;
      node.scrollIntoView({block:'center'}); const rect = node.getBoundingClientRect();
      return { x: Math.round(rect.x + rect.width / 2), y: Math.round(rect.y + rect.height / 2) }; })()`)
    if (!point) throw new Error('no element for ' + label)
    await this.call('Input.dispatchMouseEvent', { type: 'mouseMoved', x: point.x, y: point.y })
    await sleep(40)
    await this.call('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1 })
    await sleep(60)
    await this.call('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1 })
    await sleep(200)
  }

  async typeInto(selectorExpression, label, text) {
    await this.click(selectorExpression, label)
    await this.call('Input.insertText', { text })
    await sleep(120)
  }
}

function itemsFromExtraction(raw, not_run) {
  const items = {}
  if (!raw || !raw.found) {
    not_run.push({
      item: 'browser_snapshot_digest_matches_http',
      reason: 'The delivery panel rendered no frozen snapshot to read.',
      unblock: 'Freeze a delivery snapshot for this project (delivery check -> freeze), then re-run the browser side.',
    })
    return items
  }
  items.browser_snapshot_digest_matches_http = String(raw.digest || '').slice(0, DIGEST_PREFIX)
  items.snapshot_id_matches = String(raw.snapshot_id || '').slice(0, ID_PREFIX)
  items.project_version_matches = raw.project_version === null ? null : Number(raw.project_version)
  items.spec_revision_matches = raw.spec_revision === null ? null : Number(raw.spec_revision)
  items.chapters_in_snapshot_matches = raw.chapters === null ? null : Number(raw.chapters)
  items.sources_in_snapshot_matches = raw.sources === null ? null : Number(raw.sources)
  items.snapshot_check_status = String(raw.check_status || '')
  items.current_check_status = String(raw.current_check_status || '')
  if (raw.export_id) {
    items.browser_export_id_matches_http = String(raw.export_id).slice(0, ID_PREFIX)
  } else {
    not_run.push({
      item: 'browser_export_id_matches_http',
      reason: 'The delivery panel rendered no export artifact under the frozen snapshot.',
      unblock: 'Generate a DOCX export for the frozen snapshot, then re-run the browser side.',
    })
  }
  if (raw.export_sha) {
    items.browser_export_sha_matches_http = String(raw.export_sha).slice(0, SHA_PREFIX)
  } else {
    not_run.push({
      item: 'browser_export_sha_matches_http',
      reason: 'The delivery panel rendered no file hash for the export artifact.',
      unblock: 'Let the DOCX render finish so the artifact carries a file hash, then re-run the browser side.',
    })
  }
  return items
}

async function readDelivery(session, timeoutMs, not_run) {
  // The delivery drawer only opens for an active project, and its button is
  // labelled 交付 (frontend/src/views/lingdoc/Workspace.vue:37).
  await session.waitFor(
    "Array.from(document.querySelectorAll('button')).some(b => (b.textContent||'').trim() === '交付')",
    'the delivery button', timeoutMs)
  await session.click(
    "Array.from(document.querySelectorAll('button')).find(b => (b.textContent||'').trim() === '交付')",
    'the delivery button')
  await session.waitFor(
    "Boolean(document.querySelector('.delivery__snapshot')) || document.body.innerText.includes('还没有冻结过交付快照')",
    'the delivery panel', timeoutMs)

  // The panel computes the delivery check only when the user asks for it
  // (frontend/src/views/lingdoc/DeliveryPanel.vue:431).  Running it through
  // the button is a UI action, and it is what makes the "当前检查结果" line
  // exist at all.
  const hasCheck = await session.evaluate("Boolean(document.querySelector('.delivery__status'))")
  if (!hasCheck) {
    await session.click(
      "Array.from(document.querySelectorAll('.delivery button'))"
      + ".find(b => (b.textContent||'').trim() === '交付检查')",
      'the delivery check button')
    await session.waitFor("Boolean(document.querySelector('.delivery__status'))",
      'the delivery check result', timeoutMs)
  }

  const raw = await session.evaluate(`(${extractDelivery.toString()})()`)
  return itemsFromExtraction(raw, not_run)
}

async function runBrowserSide(args) {
  const not_run = []
  const base = String(args.url || '').replace(/\/+$/, '')
  if (!base) throw new Error('--url is required')
  const context = args.context && existsSync(args.context)
    ? JSON.parse(readFileSync(args.context, 'utf8'))
    : {}
  const projectId = String(context.project_id || '')
  const email = String(context.email || '')
  const password = String(context.password || '')
  if (!projectId || !email || !password) {
    throw new Error('the browser side needs the identity/project hand-off file (--context); '
      + 'run the HTTP side first so it can write one')
  }

  const binary = resolveBrowserBinary()
  const profile = mkdtempSync(join(tmpdir(), 'lingdoc-beta452-chrome-'))
  const child = spawn(binary, [
    '--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--no-first-run',
    '--disable-extensions', '--window-size=1440,1000',
    '--remote-debugging-port=0', '--user-data-dir=' + profile, 'about:blank',
  ], { stdio: 'ignore', windowsHide: true })
  let startupError = null
  child.on('error', (error) => { startupError = error })
  let socket = null
  try {
    let port = 0
    const portDeadline = Date.now() + 30000
    while (!port && Date.now() < portDeadline) {
      if (startupError) throw startupError
      try {
        const raw = readFileSync(join(profile, 'DevToolsActivePort'), 'utf8')
        port = Number(raw.split(/\r?\n/)[0])
      } catch {
        port = 0
      }
      if (!port) await sleep(150)
    }
    if (!port) throw new Error('the browser did not publish a DevTools port')
    const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
    const page = targets.find((target) => target.type === 'page')
    assert.ok(page, 'the browser exposed no page target')
    socket = new WebSocket(page.webSocketDebuggerUrl)
    await new Promise((resolve, reject) => {
      socket.addEventListener('open', resolve, { once: true })
      socket.addEventListener('error', reject, { once: true })
    })
    const session = new CdpSession(socket)
    const timeoutMs = Number(args.timeout || 90000)
    await session.call('Runtime.enable')
    await session.call('Page.enable')
    await session.call('Network.enable')
    await session.call('Emulation.setDeviceMetricsOverride', {
      width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false,
    })

    // 1. Log in through the real form (TDesign inputs, native typing).
    await session.call('Page.navigate', { url: base + '/login' })
    await session.waitFor("Boolean(document.querySelector('input[autocomplete=\"email\"]'))", 'the login form', timeoutMs)
    await session.typeInto("document.querySelector('input[autocomplete=\"email\"]')", 'the email input', email)
    await session.typeInto("document.querySelector('input[autocomplete=\"current-password\"]')", 'the password input', password)
    await session.click("document.querySelector('button[type=\"submit\"]')", 'the login button')
    await session.waitFor("Boolean(localStorage.getItem('weknora_token'))", 'the login result', timeoutMs)

    // 2. Select the project the HTTP side created.  The workspace restores
    //    it from sessionStorage (frontend/src/views/lingdoc/Workspace.vue:1031,2861).
    const identity = await session.evaluate(`(() => {
      const user = JSON.parse(localStorage.getItem('weknora_user') || '{}');
      const tenant = JSON.parse(localStorage.getItem('weknora_tenant') || '{}');
      return { user: String(user.id || ''), tenant: String(tenant.id || '') };
    })()`)
    if (!identity.user || !identity.tenant) throw new Error('the login did not expose the user/tenant identity')
    await session.evaluate(`sessionStorage.setItem(${JSON.stringify('lingdoc:last-project:' + identity.user + ':' + identity.tenant)}, ${JSON.stringify(projectId)})`)
    await session.call('Page.navigate', { url: base + '/platform/lingdoc' })
    await session.waitFor("document.body.innerText.includes('交付') || document.body.innerText.includes('课题')", 'the workspace', timeoutMs)

    // 3. Read the delivery panel, then read it again after a reload.
    const first = await readDelivery(session, timeoutMs, not_run)
    await session.call('Page.reload', {})
    await session.waitFor("Boolean(localStorage.getItem('weknora_token'))", 'the reloaded app', timeoutMs)
    const afterReload = await readDelivery(session, timeoutMs, [])

    for (const item of Object.keys(MODEL_GATED_ITEMS)) {
      not_run.push({ item, ...MODEL_GATED_ITEMS[item] })
    }
    const snapshot = {
      source: 'browser',
      at: nowIso(),
      items: first,
      after_reload: { at: nowIso(), items: afterReload },
      status: Object.keys(first).length ? 'PASS' : 'NOT RUN',
      not_run: not_run,
      url: base,
    }
    if (session.browserErrors.length) {
      snapshot.browser_exceptions = session.browserErrors.slice(0, 3)
    }
    return snapshot
  } finally {
    try { socket?.close() } catch { /* already closed */ }
    if (child.exitCode === null && !startupError) {
      child.kill()
      await new Promise((resolve) => child.once('close', resolve))
    }
    // The scratch profile is disposable, but on Windows the browser's helper
    // processes can still hold files in it for a moment after the main
    // process exited.  A failed cleanup must never mask the run's result,
    // so it is retried and then ignored.
    for (let attempt = 0; attempt < 5; attempt += 1) {
      try {
        rmSync(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 })
        break
      } catch {
        await sleep(500)
      }
    }
  }
}

async function main() {
  const args = parseArgs(process.argv.slice(2))
  if (args.help) {
    process.stdout.write(USAGE)
    return 0
  }
  if (args['list-items']) {
    process.stdout.write(JSON.stringify({ source: 'browser', items: ITEM_KEYS }) + '\n')
    return 0
  }
  if (!args.url || !args.out) {
    process.stderr.write(USAGE)
    return 2
  }
  let snapshot
  let code = 0
  try {
    snapshot = await runBrowserSide(args)
  } catch (error) {
    const not_run = Object.keys(MODEL_GATED_ITEMS).map((item) => ({ item, ...MODEL_GATED_ITEMS[item] }))
    for (const item of ITEM_KEYS) {
      if (not_run.some((entry) => entry.item === item)) continue
      not_run.push({
        item,
        reason: 'The browser side did not complete: ' + (error && error.message ? error.message : String(error)),
        unblock: 'Start the real front end (npm run dev in frontend/) and the real backend, then re-run the browser side.',
      })
    }
    snapshot = { source: 'browser', at: nowIso(), items: {}, after_reload: { at: nowIso(), items: {} },
      status: 'not_run', not_run: not_run }
    code = 2
  }
  writeFileSync(args.out, JSON.stringify(snapshot, null, 2) + '\n', 'utf8')
  if (snapshot.status === 'PASS') {
    process.stdout.write(`PASS: browser side read ${Object.keys(snapshot.items).length} items from ${args.url}\n`)
  } else {
    process.stdout.write('NOT RUN: the browser side produced no comparable items\n')
  }
  return code
}

process.exitCode = await main()
