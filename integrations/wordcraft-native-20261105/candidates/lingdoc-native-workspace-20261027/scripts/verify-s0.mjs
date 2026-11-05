import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createServer } from 'node:net';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const runId = `${new Date().toISOString().replaceAll(':', '-').replaceAll('.', '-')}-${process.pid}`;
const artifacts = path.join(root, 'artifacts', 's0', runId);
const runtime = path.join(root, 'runtime', 's0', runId);
const profile = path.join(runtime, 'profile');
const dataDirectory = path.join(runtime, 'data');
const edge = 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe';
const fixtureSha256 = 'e6ef31b4b284f509b478071f8be09c76efceab9f32ca8803afdf905eee136181';
const expected = {
  wordcraftJs: '0ccaff6bc96bc576f1151931219087dca0e5f9abef4fad5c83153f4793304e16',
  wordcraftWasm: 'd608d2e0d776897e2a1d6fd8be693b459457030fc0d4daa6f51bf3bbb5c8e47d',
};
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
async function availablePort() {
  const server = createServer();
  await new Promise((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return port;
}
async function stopChild(child) {
  if (!child) return true;
  if (child.exitCode === null && child.pid) spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
  for (let i = 0; i < 30 && child.exitCode === null; i++) await sleep(100);
  return child.exitCode !== null;
}

await mkdir(artifacts, { recursive: true });
await mkdir(profile, { recursive: true });
await mkdir(dataDirectory, { recursive: true });
const results = {
  kind: 'S0 unmodified WordCraft WASM with real DOCX',
  runId,
  expected,
  actual: {},
  runtime: { dataDirectory, profile },
  pageErrors: [],
  passed: false,
};
let host;
let browser;
let socket;
let hostOutput = '';
let browserOutput = '';
try {
  const hostPort = await availablePort();
  const debugPort = await availablePort();
  const origin = `http://127.0.0.1:${hostPort}`;
  host = spawn(process.execPath, [path.join(root, 'ds', 'r11-bridge-combination-20261025', 'host', 'server.mjs')], {
    env: {
      ...process.env,
      WORDCRAFT_HOST_PORT: String(hostPort),
      WORDCRAFT_WEB_DIR: path.join(root, 'build', 'out'),
      WORDCRAFT_DATA_DIR: dataDirectory,
      WORDCRAFT_AUDIT_PATH: path.join(artifacts, 'host-audit.jsonl'),
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  host.stdout.on('data', chunk => { hostOutput += String(chunk); });
  host.stderr.on('data', chunk => { hostOutput += String(chunk); });
  let ready;
  for (let i = 0; i < 100 && !ready; i++) {
    if (host.exitCode !== null) throw new Error(`宿主提前退出：${hostOutput}`);
    for (const line of hostOutput.split(/\r?\n/)) {
      try { const value = JSON.parse(line); if (value.ready && value.pid === host.pid) ready = value; } catch {}
    }
    if (!ready) await sleep(100);
  }
  assert(ready, `宿主启动超时：${hostOutput}`);
  results.runtime.hostPort = hostPort;
  results.runtime.edgeCdpPort = debugPort;
  results.hostReady = ready;

  for (const name of ['wordcraft.js', 'wordcraft_bg.wasm']) {
    const bytes = await readFile(path.join(root, 'build', 'out', name));
    results.actual[name] = { bytes: bytes.length, sha256: sha256(bytes) };
  }
  results.identity = {
    jsMatchesDelivered: results.actual['wordcraft.js'].sha256 === expected.wordcraftJs,
    wasmMatchesDelivered: results.actual['wordcraft_bg.wasm'].sha256 === expected.wordcraftWasm,
  };

  browser = spawn(edge, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--disable-background-timer-throttling', '--disable-backgrounding-occluded-windows',
    '--disable-renderer-backgrounding', `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${profile}`, 'about:blank',
  ], { stdio: ['ignore', 'ignore', 'pipe'] });
  browser.stderr.on('data', chunk => { browserOutput += String(chunk); });
  let pages;
  for (let i = 0; i < 100; i++) {
    if (browser.exitCode !== null) throw new Error(`Edge 提前退出：${browserOutput}`);
    try {
      pages = await (await fetch(`http://127.0.0.1:${debugPort}/json/list`, { signal: AbortSignal.timeout(1000) })).json();
      if (pages.some(page => page.type === 'page')) break;
    } catch {}
    await sleep(150);
  }
  const page = pages?.find(item => item.type === 'page');
  assert(page, '未找到 S0 浏览器页面');
  socket = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let sequence = 0;
  const pending = new Map();
  socket.onmessage = event => {
    const message = JSON.parse(event.data);
    if (message.method === 'Runtime.exceptionThrown') results.pageErrors.push(message.params.exceptionDetails);
    if (!message.id || !pending.has(message.id)) return;
    const handlers = pending.get(message.id);
    clearTimeout(handlers.timer);
    pending.delete(message.id);
    if (message.error) handlers.reject(new Error(message.error.message)); else handlers.resolve(message.result);
  };
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timeout`)); }, 30000);
    pending.set(id, { resolve, reject, timer });
    socket.send(JSON.stringify({ id, method, params }));
  });
  const evaluate = async expression => {
    const response = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
    if (response.exceptionDetails) throw new Error(response.exceptionDetails.exception?.description || response.exceptionDetails.text);
    return response.result.value;
  };
  await send('Page.enable');
  await send('Runtime.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await send('Page.navigate', { url: origin });
  let opened;
  for (let i = 0; i < 300; i++) {
    const value = await evaluate(`(()=>{try{const text=document.querySelector('#host-log')?.textContent||'';const parsed=JSON.parse(text);return parsed.opened?parsed:null}catch{return null}})()`);
    if (value) { opened = value; break; }
    await sleep(200);
  }
  assert(opened, `真实 DOCX 打开超时：${await evaluate(`document.querySelector('#host-log')?.textContent`)}`);
  assert(opened.sourceSha256 === fixtureSha256, '打开的 DOCX 不是固定输入字节');
  const status = await evaluate(`window.wordcraftHost.engine({op:'status'})`);
  assert(Number.isInteger(status.documentId) && Number.isInteger(status.revision), '引擎状态缺少文档或修订身份');
  await sleep(500);
  const canvas = await evaluate(`(()=>{const c=document.querySelector('#wordcraft_canvas');const r=c.getBoundingClientRect();const loading=document.querySelector('#wordcraft_loading');return {width:r.width,height:r.height,loadingPresent:!!loading,hostLog:document.querySelector('#host-log').textContent}})()`);
  assert(canvas.width > 500 && canvas.height === 900 && !canvas.loadingPresent, 'Canvas 未完成真实加载');
  assert(results.pageErrors.length === 0, '浏览器存在未捕获脚本错误');
  const shot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(path.join(artifacts, 's0-opened-1440x900.png'), Buffer.from(shot.data, 'base64'));
  results.document = { sourceSha256: opened.sourceSha256, diskVersion: opened.diskVersion, status, canvas };
  results.passed = true;
} catch (error) {
  results.error = String(error);
  results.passed = false;
  process.exitCode = 1;
} finally {
  if (socket) {
    try { await new Promise((resolve, reject) => {
      const timer = setTimeout(resolve, 2000);
      socket.addEventListener('message', event => {
        const message = JSON.parse(event.data);
        if (message.method === 'Browser.close' || message.id === -1) { clearTimeout(timer); resolve(); }
      }, { once: true });
      socket.send(JSON.stringify({ id: -1, method: 'Browser.close', params: {} }), error => error ? reject(error) : resolve());
    }); } catch {}
    socket.close();
  }
  results.browserExited = await stopChild(browser);
  results.hostExited = await stopChild(host);
  results.hostLog = hostOutput.slice(-6000);
  results.browserLog = browserOutput.slice(-3000);
  if (!results.browserExited || !results.hostExited) {
    results.passed = false;
    process.exitCode = 1;
    results.processError = '本次浏览器或宿主进程未确认退出';
  }
  await writeFile(path.join(artifacts, 'verification.json'), JSON.stringify(results, null, 2));
  console.log(JSON.stringify({ artifacts, passed: results.passed, identity: results.identity, document: results.document, browserExited: results.browserExited, hostExited: results.hostExited, error: results.error, processError: results.processError }, null, 2));
}
