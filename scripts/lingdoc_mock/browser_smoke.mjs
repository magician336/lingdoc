// Real browser smoke via Chromium CDP. No npm test-only dependency or real login.
// Requires Node 22+ and Chrome/Chromium. Both services must already be running.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { setTimeout as sleep } from 'node:timers/promises'

const browserErrors = []
async function until(fn, label) {
  for (let i = 0; i < 400; i++) {
    if (browserErrors.length) throw new Error('Browser exception: ' + JSON.stringify(browserErrors))
    const value = await fn()
    if (value) return value
    await sleep(100)
  }
  throw new Error('Timed out: ' + label)
}
const profile = await mkdtemp(join(tmpdir(), 'lingdoc-mock-chrome-'))
const chrome = spawn(process.env.CHROME_BIN ?? 'google-chrome', [
  '--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--no-first-run',
  '--remote-debugging-port=0', '--user-data-dir=' + profile, 'about:blank',
], { stdio: 'ignore' })
let startupError
chrome.on('error', error => { startupError = error })
let socket
try {
  const port = await until(async () => {
    if (startupError) throw startupError
    try { return (await readFile(join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0] } catch { return '' }
  }, 'Chrome debugging port')
  const pages = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
  const page = pages.find(value => value.type === 'page')
  assert.ok(page)
  socket = new WebSocket(page.webSocketDebuggerUrl)
  await new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true })
    socket.addEventListener('error', reject, { once: true })
  })
  let next = 0
  const waiting = new Map(), requests = []
  socket.addEventListener('message', ({ data }) => {
    const message = JSON.parse(data)
    if (message.method === 'Network.requestWillBeSent') requests.push(message.params.request)
    if (message.method === 'Runtime.exceptionThrown') browserErrors.push(message.params.exceptionDetails)
    if (waiting.has(message.id)) {
      const { resolve, reject, timer } = waiting.get(message.id)
      clearTimeout(timer); waiting.delete(message.id)
      message.error ? reject(new Error(JSON.stringify(message.error))) : resolve(message.result)
    }
  })
  const call = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++next
    const timer = setTimeout(() => { waiting.delete(id); reject(new Error('CDP timeout: ' + method)) }, 10_000)
    waiting.set(id, { resolve, reject, timer })
    socket.send(JSON.stringify({ id, method, params }))
  })
  const evaluate = async expression => {
    const value = await call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true })
    if (value.exceptionDetails) throw new Error(JSON.stringify(value.exceptionDetails))
    return value.result.value
  }
  await call('Network.enable')
  await call('Runtime.enable')
  await call('Page.navigate', { url: 'http://127.0.0.1:5173/lingdoc-mock.html' })
  await until(() => evaluate("document.querySelector('#status')?.textContent === '尚未运行'"), 'dev page')
  // Module imports may still be loading; wait until its event listeners are installed.
  await until(() => evaluate("document.documentElement.dataset.mockReady === 'true'"), 'request module')
  await evaluate("localStorage.setItem('weknora_token','t06-private-sentinel'); localStorage.setItem('weknora_selected_tenant_id','7'); document.querySelector('#success').click()")
  await until(() => evaluate("document.querySelector('#status').textContent.includes('HTTP 200')"), 'success UI')
  assert.equal(await evaluate("JSON.parse(document.querySelector('#payload').textContent).data.is_demo"), true)
  await evaluate("document.querySelector('#failure').click()")
  await until(() => evaluate("document.querySelector('#status').textContent.includes('422')"), 'failure UI')
  assert.equal(await evaluate("JSON.parse(document.querySelector('#payload').textContent).error.code"), 'invalid_state')
  assert.equal(await evaluate("localStorage.getItem('weknora_token')"), 't06-private-sentinel')
  const api = requests.filter(value => new URL(value.url).pathname.startsWith('/api/'))
  const businessCalls = api.filter(value => value.method !== 'OPTIONS')
  assert.equal(businessCalls.length, 2, 'exactly two business calls; no auth refresh or backend fallback')
  for (const request of api) {
    assert.equal(new URL(request.url).origin, 'http://127.0.0.1:4010')
    const headers = new Headers(request.headers)
    for (const name of ['authorization', 'cookie', 'x-tenant-id']) assert.equal(headers.get(name), null)
  }
  console.log(JSON.stringify({ result: 'PASS', scope: 'real_browser_fixed_mock_only', successes: 1, failures: 1, api_calls: businessCalls.length, preflights: api.length - businessCalls.length }))
} finally {
  socket?.close()
  if (chrome.exitCode === null && !startupError) {
    chrome.kill()
    await new Promise(resolve => chrome.once('close', resolve))
  }
  await rm(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 })
}
