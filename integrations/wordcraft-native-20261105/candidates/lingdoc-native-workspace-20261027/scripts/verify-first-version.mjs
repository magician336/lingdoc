import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createServer as createNetServer } from 'node:net';
import { createServer as createHttpServer } from 'node:http';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

if (!process.argv.includes('--contract')) {
  await runRealFirstVersion();
} else {
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const runId = `${new Date().toISOString().replaceAll(':', '-').replaceAll('.', '-')}-${process.pid}`;
const artifacts = path.join(root, 'artifacts', 'first-version', runId);
const runtime = path.join(root, 'runtime', 'first-version-contract', runId);
const profile = path.join(runtime, 'profile');
const dataDirectory = path.join(runtime, 'data');
const edge = 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe';
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
async function availablePort() {
  const server = createNetServer();
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
async function startGoContract(dsOrigin) {
  const versions = [];
  const calls = [];
  const confirmations = new Map();
  const snapshots = new Map();
  const exports = new Map();
  const sectionIds = ['topic-summary', 'topic-basis', 'research-content', 'innovation', 'expected-results', 'references'];
  let mismatch = false;
  const sendJson = (res, status, value) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(value)); };
  const server = createHttpServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const body = chunks.length && !req.url.includes('/exports/') ? JSON.parse(Buffer.concat(chunks).toString('utf8')) : null;
    calls.push({ method: req.method, url: req.url, body });
    res.setHeader('Access-Control-Allow-Origin', dsOrigin);
    res.setHeader('Access-Control-Allow-Headers', 'Content-Type, Idempotency-Key');
    if (req.method === 'OPTIONS') { res.writeHead(204); res.end(); return; }
    const prefix = '/api/docx/projects/project-1';
    if (req.method === 'GET' && req.url === `${prefix}/versions/latest`) {
      sendJson(res, versions.length ? 200 : 404, versions.at(-1) ?? { error: 'NOT_FOUND' }); return;
    }
    if (req.method === 'POST' && req.url === `${prefix}/versions`) {
      const bytes = Buffer.from(body.data, 'base64');
      const docSha256 = sha256(bytes);
      const version = {
        id: `saved-${versions.length + 1}`, diskVersion: versions.length + 1, docSha256, bytes,
        manifestHash: 'contract-manifest',
        projection: { sections: sectionIds.map(sectionId => ({ sectionId, sectionDeliveryHash: `${docSha256}-${sectionId}` })) },
      };
      versions.push(version);
      sendJson(res, 200, { version, replayed: false }); return;
    }
    const delivery = /^\/api\/docx\/projects\/project-1\/versions\/([^/]+)\/delivery$/.exec(req.url);
    if (req.method === 'GET' && delivery) {
      const version = versions.find(item => item.id === delivery[1]);
      if (!version) { sendJson(res, 404, { error: 'NOT_FOUND' }); return; }
      const chapters = sectionIds.map(sectionId => ({ sectionId, confirmed: confirmations.get(version.id)?.has(sectionId) ?? false }));
      const snapshot = [...snapshots.values()].find(item => item.savedVersionId === version.id) ?? null;
      sendJson(res, 200, { version: mismatch ? { ...version, docSha256: 'b'.repeat(64) } : version, specRevision: 1, chapters, snapshot, deliveryEligible: snapshot?.check.status === 'passed' && chapters.every(chapter => chapter.confirmed) }); return;
    }
    if (req.method === 'POST' && req.url === `${prefix}/confirmations`) {
      const version = versions.find(item => item.id === body.savedVersionId);
      if (!version || version.docSha256 !== body.expectedDocSha256 || !sectionIds.includes(body.sectionId)) { sendJson(res, 409, { error: 'VERSION_CHANGED' }); return; }
      confirmations.get(version.id) ?? confirmations.set(version.id, new Set());
      confirmations.get(version.id).add(body.sectionId);
      sendJson(res, 200, { confirmation: { valid: true, sectionId: body.sectionId }, replayed: false }); return;
    }
    if (req.method === 'POST' && req.url === `${prefix}/freezes`) {
      const version = versions.find(item => item.id === body.savedVersionId);
      const passed = version && sectionIds.every(sectionId => confirmations.get(version.id)?.has(sectionId));
      const snapshot = { id: `snapshot-${body.savedVersionId}`, savedVersionId: body.savedVersionId, check: { status: passed ? 'passed' : 'blocked', issues: [] }, is_current: true };
      snapshots.set(snapshot.id, snapshot);
      sendJson(res, 200, { snapshot, deliveryEligible: passed, replayed: false }); return;
    }
    if (req.method === 'POST' && req.url === `${prefix}/exports`) {
      const snapshot = snapshots.get(body.snapshotId);
      if (!snapshot || snapshot.check.status !== 'passed') { sendJson(res, 422, { error: 'PREFLIGHT_BLOCKED' }); return; }
      const version = versions.find(item => item.id === snapshot.savedVersionId);
      const artifact = { id: `export-${snapshot.id}`, snapshotId: snapshot.id, status: 'verified', file_sha256: version.docSha256, versionId: version.id };
      exports.set(artifact.id, artifact);
      sendJson(res, 200, { export: artifact, replayed: false }); return;
    }
    const download = /^\/api\/docx\/projects\/project-1\/exports\/([^/]+)$/.exec(req.url);
    if (req.method === 'GET' && download) {
      const artifact = exports.get(download[1]);
      const version = versions.find(item => item.id === artifact?.versionId);
      if (!version) { sendJson(res, 404, { error: 'NOT_FOUND' }); return; }
      res.writeHead(200, { 'Content-Type': 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' }); res.end(version.bytes); return;
    }
    sendJson(res, 404, { error: 'NOT_FOUND' });
  });
  const port = await availablePort();
  await new Promise((resolve, reject) => server.once('error', reject).listen(port, '127.0.0.1', resolve));
  return { server, port, versions, calls, setMismatch(value) { mismatch = value; } };
}

await mkdir(artifacts, { recursive: true });
await mkdir(profile, { recursive: true });
await mkdir(dataDirectory, { recursive: true });
const results = { kind: 'real DOCX/DS/WASM with controlled Go contract server', runId, passed: false, pageErrors: [], scope: '前端浏览器契约；不是 SQLite 或整程序重开验收' };
let host;
let browser;
let socket;
let go;
let hostOutput = '';
let browserOutput = '';
try {
  const hostPort = await availablePort();
  const debugPort = await availablePort();
  const origin = `http://127.0.0.1:${hostPort}`;
  go = await startGoContract(origin);
  host = spawn(process.execPath, [path.join(root, 'ds', 'r11-bridge-combination-20261025', 'host', 'server.mjs')], {
    env: {
      ...process.env,
      WORDCRAFT_HOST_PORT: String(hostPort),
      WORDCRAFT_WEB_DIR: path.join(root, 'build', 'out'),
      WORDCRAFT_DATA_DIR: dataDirectory,
      WORDCRAFT_AUDIT_PATH: path.join(artifacts, 'host-audit.jsonl'),
      WORDCRAFT_GO_ORIGIN: `http://127.0.0.1:${go.port}`,
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

  browser = spawn(edge, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--disable-background-timer-throttling', '--disable-backgrounding-occluded-windows',
    '--disable-renderer-backgrounding', `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${profile}`, 'about:blank',
  ], { stdio: ['ignore', 'ignore', 'pipe'] });
  browser.stderr.on('data', chunk => { browserOutput += String(chunk); });
  let pages;
  for (let i = 0; i < 100; i++) {
    try { pages = await (await fetch(`http://127.0.0.1:${debugPort}/json/list`, { signal: AbortSignal.timeout(1000) })).json(); if (pages.some(page => page.type === 'page')) break; } catch {}
    await sleep(150);
  }
  const page = pages?.find(item => item.type === 'page');
  assert(page, '未找到第一版验收浏览器页面');
  socket = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let sequence = 0;
  const pending = new Map();
  socket.onmessage = event => {
    const message = JSON.parse(event.data);
    if (message.method === 'Runtime.exceptionThrown') results.pageErrors.push(message.params.exceptionDetails);
    if (!message.id || !pending.has(message.id)) return;
    const handlers = pending.get(message.id);
    clearTimeout(handlers.timer); pending.delete(message.id);
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
  const waitFor = async (expression, attempts = 300) => {
    for (let i = 0; i < attempts; i++) { if (await evaluate(expression)) return; await sleep(100); }
    throw new Error(`等待状态超时：${expression}\n${JSON.stringify(await evaluate(`({log:document.querySelector('#host-log')?.textContent,diagnostics:window.lingdocDiagnostics?.(),calls:window.__firstVersionCalls})`))}`);
  };
  const keySave = async () => {
    await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Control', code: 'ControlLeft', modifiers: 2 });
    await send('Input.dispatchKeyEvent', { type: 'keyDown', key: 's', code: 'KeyS', modifiers: 2 });
    await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 's', code: 'KeyS', modifiers: 2 });
    await send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Control', code: 'ControlLeft', modifiers: 0 });
  };
  const click = async (x, y) => {
    await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', clickCount: 1 });
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', clickCount: 1 });
  };

  await send('Page.enable'); await send('Runtime.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await send('Page.navigate', { url: origin });
  await waitFor(`(()=>{try{return !!JSON.parse(document.querySelector('#host-log')?.textContent||'{}').opened}catch{return false}})()`, 600);
  await evaluate(`(()=>{
    window.__firstVersionCalls=[];
    const record=value=>window.__firstVersionCalls.push(value);
    const originalFetch=window.fetch;
    window.fetch=async(...args)=>{const url=String(args[0]);if(url==='/api/save')record('ds');else if(url.endsWith('/versions'))record('go');else if(url.endsWith('/delivery'))record('readback');return originalFetch(...args);};
    const bindings=window.wasmBindings;const original=bindings.host_request;
    window.wasmBindings=new Proxy(bindings,{get(target,property){if(property!=='host_request')return Reflect.get(target,property);return text=>{const request=JSON.parse(text);record(request.op);return original(text);};}});
  })()`);
  await evaluate(`(async()=>{await window.wordcraftHost.selection();return window.wordcraftHost.insert('FIRST-VERSION-FOUR-ENTRIES')})()`);
  await click(108, 18);
  await evaluate(`document.querySelector('#host-save').click()`);
  await keySave();
  await waitFor(`window.lingdocDiagnostics?.().lastSave?.version?.id==='saved-1' && !window.lingdocDiagnostics?.().transactionActive`, 600);
  const entries = await evaluate(`({calls:window.__firstVersionCalls,diagnostics:window.lingdocDiagnostics?.()})`);
  const exportCount = entries.calls.filter(call => call === 'export').length;
  const ackCount = entries.calls.filter(call => call === 'ack').length;
  const sources = entries.diagnostics?.saveSources ?? [];
  assert(exportCount === 1 && go.versions.length === 1 && ackCount === 1, `原生、宿主和父页面入口未共享同一保存事务：${JSON.stringify({ exportCount, go: go.versions.length, ackCount, entries })}`);
  for (const source of ['native-canvas', 'host-button', 'parent-keyboard']) assert(sources.includes(source), `保存入口未进入协调器：${source}；实际 ${sources.join(',')}`);
  const dsIndex = entries.calls.indexOf('ds');
  const goIndex = entries.calls.indexOf('go');
  const readbackIndex = entries.calls.indexOf('readback');
  const ackIndex = entries.calls.indexOf('ack');
  assert(dsIndex >= 0 && dsIndex < goIndex && goIndex < readbackIndex && readbackIndex < ackIndex, `保存确认顺序错误：${entries.calls.join(',')}`);
  results.entries = { exportCount, ackCount, sources, calls: entries.calls, savedVersionId: 'saved-1' };

  await evaluate(`(()=>{document.open();document.write('<!doctype html><html><body><iframe id="first-version-frame" src="/" style="width:1440px;height:900px;border:0"></iframe></body></html>');document.close()})()`);
  await waitFor(`(()=>{const f=document.querySelector('#first-version-frame')?.contentWindow;try{return !!f?.wordcraftHost && !!JSON.parse(f.document.querySelector('#host-log')?.textContent||'{}').opened}catch{return false}})()`, 600);
  await evaluate(`(()=>{const f=document.querySelector('#first-version-frame').contentWindow;f.__iframeExports=0;const bindings=f.wasmBindings;const original=bindings.host_request;f.wasmBindings=new Proxy(bindings,{get(target,property){if(property!=='host_request')return Reflect.get(target,property);return text=>{if(JSON.parse(text).op==='export')f.__iframeExports++;return original(text);};}});f.document.dispatchEvent(new f.KeyboardEvent('keydown',{key:'s',ctrlKey:true,bubbles:true,cancelable:true}))})()`);
  await waitFor(`document.querySelector('#first-version-frame').contentWindow.lingdocDiagnostics?.().lastSave?.version?.id==='saved-2' && !document.querySelector('#first-version-frame').contentWindow.lingdocDiagnostics?.().transactionActive`, 600);
  const iframe = await evaluate(`(()=>{const f=document.querySelector('#first-version-frame').contentWindow;return{exports:f.__iframeExports,sources:f.lingdocDiagnostics().saveSources}})()`);
  assert(iframe.exports === 1 && iframe.sources.includes('iframe-keyboard'), `iframe 快捷键未进入同一协调器：${JSON.stringify(iframe)}`);
  results.iframeEntry = iframe;

  await evaluate(`(async()=>{const f=document.querySelector('#first-version-frame').contentWindow;const h=f.wordcraftHost;const originalFetch=f.fetch;let delayed=false;f.fetch=async(...args)=>{if(String(args[0])==='/api/save'&&!delayed){delayed=true;await new Promise(resolve=>setTimeout(resolve,800));}return originalFetch(...args);};await h.selection();await h.insert('FIRST-VERSION-DIRTY-OLD');f.__dirtyOldSave=h.saveIntent('dirty-old-revision')})()`);
  await sleep(200);
  await evaluate(`(async()=>{const f=document.querySelector('#first-version-frame').contentWindow;const h=f.wordcraftHost;await h.selection();await h.insert('FIRST-VERSION-DIRTY-NEW');f.__dirtyNewSave=h.saveIntent('dirty-new-revision')})()`);
  const dirtyDuringSave = await evaluate(`Promise.all([document.querySelector('#first-version-frame').contentWindow.__dirtyOldSave,document.querySelector('#first-version-frame').contentWindow.__dirtyNewSave]).then(([oldSave,newSave])=>({oldPending:oldSave.ackPending,newPending:newSave.ackPending,newRevision:newSave.receipt.revision,liveDirty:newSave.live.dirty,liveRevision:newSave.live.revision}))`);
  assert(dirtyDuringSave.oldPending && !dirtyDuringSave.newPending && !dirtyDuringSave.liveDirty && dirtyDuringSave.newRevision === dirtyDuringSave.liveRevision && go.versions.length === 4, `保存中继续编辑未完整恢复：${JSON.stringify({ dirtyDuringSave, goVersions: go.versions.length })}`);
  results.dirtyDuringSave = { ...dirtyDuringSave, goVersions: go.versions.length, oldRevisionPreserved: true };

  await click(38, 60);
  await sleep(400);
  const backstageShot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await writeFile(path.join(artifacts, 'backstage.png'), Buffer.from(backstageShot.data, 'base64'));
  await click(100, 257);
  const deliveryVersion = go.versions.length + 1;
  await waitFor(`document.querySelector('#first-version-frame').contentDocument.querySelector('#lingdoc-delivery-dialog')?.open && document.querySelector('#first-version-frame').contentWindow.lingdocDiagnostics?.().lastSave?.version?.id==='saved-${deliveryVersion}'`, 600);
  const deliverySectionIds = ['topic-summary', 'topic-basis', 'research-content', 'innovation', 'expected-results', 'references'];
  for (let index = 0; index < deliverySectionIds.length; index++) {
    const sectionId = deliverySectionIds[index];
    await waitFor(`!document.querySelector('#first-version-frame').contentDocument.querySelector('#delivery-confirm-${sectionId}').disabled`, 600);
    await evaluate(`document.querySelector('#first-version-frame').contentDocument.querySelector('#delivery-confirm-${sectionId}').click()`);
    await waitFor(`document.querySelector('#first-version-frame').contentWindow.lingdocDiagnostics?.().lastSave?.delivery?.chapters?.filter(chapter=>chapter.confirmed).length===${index + 1}`, 600);
  }
  await waitFor(`!document.querySelector('#first-version-frame').contentDocument.querySelector('#delivery-freeze').disabled`, 600);
  await evaluate(`document.querySelector('#first-version-frame').contentDocument.querySelector('#delivery-freeze').click()`);
  await waitFor(`document.querySelector('#first-version-frame').contentWindow.lingdocDiagnostics?.().lastSave?.delivery?.deliveryEligible===true`, 600);
  await send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: artifacts, eventsEnabled: true });
  await waitFor(`!document.querySelector('#first-version-frame').contentDocument.querySelector('#delivery-export').disabled`, 600);
  await evaluate(`document.querySelector('#first-version-frame').contentDocument.querySelector('#delivery-export').click()`);
  await waitFor(`document.querySelector('#first-version-frame').contentWindow.lingdocDiagnostics?.().lastExport?.status==='verified'`, 600);
  const exportedPath = path.join(artifacts, `lingdoc-export-snapshot-saved-${deliveryVersion}.docx`);
  let exportedBytes;
  for (let i = 0; i < 100; i++) { try { exportedBytes = await readFile(exportedPath); break; } catch { await sleep(100); } }
  assert(exportedBytes && sha256(exportedBytes) === go.versions[deliveryVersion - 1].docSha256, '准备交付导出的不是指定冻结版本字节');
  results.delivery = { savedVersionId: `saved-${deliveryVersion}`, confirmed: 6, frozen: true, exportSha256: sha256(exportedBytes), nativeEntry: true };
  await evaluate(`document.querySelector('#first-version-frame').contentDocument.querySelector('#delivery-close').click()`);

  await evaluate(`(async()=>{const h=document.querySelector('#first-version-frame').contentWindow.wordcraftHost;await h.selection();return h.insert('FIRST-VERSION-HASH-MISMATCH')})()`);
  go.setMismatch(true);
  const mismatch = await evaluate(`document.querySelector('#first-version-frame').contentWindow.wordcraftHost.saveIntent('hash-mismatch-test').then(()=>({resolved:true}),error=>({resolved:false,error:error.message}))`);
  const mismatchStatus = await evaluate(`document.querySelector('#first-version-frame').contentWindow.wordcraftHost.engine({op:'status'})`);
  assert(!mismatch.resolved && mismatchStatus.dirty, `读回哈希不一致仍清除了 dirty：${JSON.stringify({ mismatch, mismatchStatus })}`);
  results.hashMismatch = { rejected: true, dirtyPreserved: mismatchStatus.dirty };
  assert(results.pageErrors.length === 0, '浏览器存在未捕获脚本错误');
  results.passed = true;
} catch (error) {
  results.error = String(error); results.passed = false; process.exitCode = 1;
} finally {
  if (socket) { try { socket.close(); } catch {} }
  results.browserExited = await stopChild(browser);
  results.hostExited = await stopChild(host);
  if (go) await new Promise(resolve => go.server.close(resolve));
  results.hostLog = hostOutput.slice(-6000); results.browserLog = browserOutput.slice(-3000);
  if (!results.browserExited || !results.hostExited) { results.passed = false; process.exitCode = 1; results.processError = '本次浏览器或宿主进程未确认退出'; }
  await writeFile(path.join(artifacts, 'verification.json'), JSON.stringify(results, null, 2));
  console.log(JSON.stringify({ artifacts, passed: results.passed, entries: results.entries, hashMismatch: results.hashMismatch, error: results.error, processError: results.processError }, null, 2));
}
}

async function runRealFirstVersion() {
  const { startDeliveryRuntime } = await import('./delivery-runtime.mjs');
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
  const runId = `${new Date().toISOString().replaceAll(':', '-').replaceAll('.', '-')}-${process.pid}`;
  const artifacts = path.join(root, 'artifacts', 'first-version-real', runId);
  const runtimeDir = path.join(root, 'runtime', 'first-version-verification', runId);
  const edge = 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe';
  const sectionIds = ['topic-summary', 'topic-basis', 'research-content', 'innovation', 'expected-results', 'references'];
  const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
  const assert = (condition, message) => { if (!condition) throw new Error(message); };
  const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
  const results = { kind: 'real Go/DS/SQLite/browser full restart', runId, runtimeDir, passed: false, pageErrors: [] };
  let runtime;
  let browserSession;

  async function availablePort() {
    const server = createNetServer();
    await new Promise((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
    const port = server.address().port;
    await new Promise(resolve => server.close(resolve));
    return port;
  }
  async function stopBrowser(session) {
    if (!session) return true;
    try { session.socket.close(); } catch {}
    if (session.browser.exitCode === null && session.browser.pid) spawnSync('taskkill', ['/PID', String(session.browser.pid), '/T', '/F'], { stdio: 'ignore' });
    for (let i = 0; i < 30 && session.browser.exitCode === null; i++) await sleep(100);
    return session.browser.exitCode !== null;
  }
  async function launchBrowser(origin, profile) {
    await mkdir(profile, { recursive: true });
    const debugPort = await availablePort();
    const browser = spawn(edge, [
      '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
      '--disable-background-timer-throttling', '--disable-backgrounding-occluded-windows',
      '--disable-renderer-backgrounding', `--remote-debugging-port=${debugPort}`,
      `--user-data-dir=${profile}`, 'about:blank',
    ], { stdio: ['ignore', 'ignore', 'pipe'] });
    let pages;
    for (let i = 0; i < 100; i++) {
      try { pages = await (await fetch(`http://127.0.0.1:${debugPort}/json/list`, { signal: AbortSignal.timeout(1000) })).json(); if (pages.some(page => page.type === 'page')) break; } catch {}
      await sleep(150);
    }
    const page = pages?.find(item => item.type === 'page');
    assert(page, '真实验收浏览器页面未启动');
    const socket = new WebSocket(page.webSocketDebuggerUrl);
    await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
    let sequence = 0;
    const pending = new Map();
    socket.onmessage = event => {
      const message = JSON.parse(event.data);
      if (message.method === 'Runtime.exceptionThrown') results.pageErrors.push(message.params.exceptionDetails);
      if (!message.id || !pending.has(message.id)) return;
      const handlers = pending.get(message.id);
      clearTimeout(handlers.timer); pending.delete(message.id);
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
    const waitFor = async (expression, attempts = 600) => {
      for (let i = 0; i < attempts; i++) { if (await evaluate(expression)) return; await sleep(100); }
      throw new Error(`等待真实状态超时：${expression}\n${JSON.stringify(await evaluate(`({log:document.querySelector('#host-log')?.textContent,status:document.querySelector('#delivery-status')?.textContent})`))}`);
    };
    const screenshot = async name => {
      const shot = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
      await writeFile(path.join(artifacts, name), Buffer.from(shot.data, 'base64'));
    };
    await send('Page.enable'); await send('Runtime.enable');
    await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
    await send('Page.navigate', { url: origin });
    await waitFor(`(()=>{try{return !!JSON.parse(document.querySelector('#host-log')?.textContent||'{}').opened}catch{return false}})()`);
    return { browser, socket, send, evaluate, waitFor, screenshot };
  }

  try {
    await mkdir(artifacts, { recursive: true });
    runtime = await startDeliveryRuntime({ runtimeDir });
    results.firstRuntime = { origin: runtime.origin, goOrigin: runtime.goOrigin, database: runtime.database };
    browserSession = await launchBrowser(runtime.origin, path.join(runtimeDir, 'profile-first'));
    const { evaluate, waitFor, send, screenshot } = browserSession;

    for (const sectionId of sectionIds) {
      const state = await evaluate(`window.wordcraftHost.engine({op:'status'})`);
      const manifest = await evaluate(`window.wordcraftHost.engine({op:'manifest',expectedDocumentId:${state.documentId},expectedRevision:${state.revision},templateId:'nsfc2026-annual-application',adapterVersion:'nsfc2026-r4-adapter-v1'})`);
      assert(manifest.structure?.ok && manifest.sections.length === 6, '真实模板六章结构不匹配');
      await evaluate(`window.wordcraftHost.engine({op:'focusSection',sectionId:'${sectionId}',expectedDocumentId:${state.documentId},expectedRevision:${state.revision},expectedManifestHash:'${manifest.manifestHash}',adapterVersion:'nsfc2026-r4-adapter-v1'})`);
      await evaluate(`(async()=>{await window.wordcraftHost.selection();return window.wordcraftHost.insert(${JSON.stringify(`LINGDOC-FIRST-VERSION-${sectionId} [[source:source-1]]`)})})()`);
    }
    const saved = await evaluate(`window.wordcraftHost.saveIntent('real-runtime-save')`);
    assert(!saved.ackPending && !saved.live.dirty && saved.delivery.version.docSha256 === saved.receipt.sha256, '真实 Go/DS 保存链未完成');
    results.save = { versionId: saved.version.id, diskVersion: saved.version.diskVersion, sha256: saved.receipt.sha256, revision: saved.receipt.revision };

    await evaluate(`window.wordcraftHost.prepareDelivery()`);
    await waitFor(`document.querySelector('#lingdoc-delivery-dialog')?.open && window.lingdocDiagnostics?.().lastSave?.version?.id && window.lingdocDiagnostics?.().lastSave?.version?.id!=='${saved.version.id}' && !document.querySelector('#delivery-confirm-topic-summary').disabled`);
    for (let index = 0; index < sectionIds.length; index++) {
      const sectionId = sectionIds[index];
      await waitFor(`!document.querySelector('#delivery-confirm-${sectionId}').disabled`);
      await evaluate(`document.querySelector('#delivery-confirm-${sectionId}').click()`);
      await waitFor(`window.lingdocDiagnostics?.().lastSave?.delivery?.chapters?.filter(chapter=>chapter.confirmed).length===${index + 1}`);
    }
    await waitFor(`!document.querySelector('#delivery-freeze').disabled`);
    await evaluate(`document.querySelector('#delivery-freeze').click()`);
    await waitFor(`window.lingdocDiagnostics?.().lastSave?.delivery?.deliveryEligible===true && !document.querySelector('#delivery-export').disabled`);
    const frozen = await evaluate(`({versionId:window.lingdocDiagnostics().lastSave.version.id,sha256:window.lingdocDiagnostics().lastSave.version.docSha256,snapshotId:window.lingdocDiagnostics().lastSave.delivery.snapshot.id})`);
    await screenshot('real-six-confirmed-frozen.png');
    await send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: artifacts, eventsEnabled: true });
    await evaluate(`document.querySelector('#delivery-export').click()`);
    await waitFor(`window.lingdocDiagnostics?.().lastExport?.status==='verified'`);
    const exported = await evaluate(`window.lingdocDiagnostics().lastExport`);
    let exportedBytes;
    const exportedPath = path.join(artifacts, `lingdoc-${exported.id}.docx`);
    for (let i = 0; i < 100; i++) { try { exportedBytes = await readFile(exportedPath); break; } catch { await sleep(100); } }
    assert(exportedBytes && sha256(exportedBytes) === frozen.sha256, '真实导出不是指定冻结版本');
    results.delivery = { ...frozen, exportId: exported.id, exportSha256: sha256(exportedBytes), confirmed: 6 };

    await evaluate(`document.querySelector('#delivery-close').click()`);
    await evaluate(`window.wordcraftHost.engine({op:'focusSection',sectionId:'topic-summary',expectedDocumentId:${saved.live.documentId},expectedRevision:${saved.live.revision + 1},expectedManifestHash:'${saved.version.manifestHash}',adapterVersion:'nsfc2026-r4-adapter-v1'})`).catch(() => {});
    await evaluate(`(async()=>{await window.wordcraftHost.selection();return window.wordcraftHost.insert('LINGDOC-AFTER-FREEZE-NOT-SAVED')})()`);
    assert(sha256(await readFile(exportedPath)) === frozen.sha256, '导出后的新编辑改变了冻结文件');
    results.postExportEdit = { saved: false, frozenSha256Unchanged: true };

    results.firstBrowserExited = await stopBrowser(browserSession); browserSession = null;
    await runtime.stop(); runtime = null;
    results.firstRuntimeStopped = true;
    assert(results.firstBrowserExited, '首次浏览器未退出');

    runtime = await startDeliveryRuntime({ runtimeDir });
    results.secondRuntime = { origin: runtime.origin, goOrigin: runtime.goOrigin, database: runtime.database };
    browserSession = await launchBrowser(runtime.origin, path.join(runtimeDir, 'profile-restarted'));
    const reopened = browserSession;
    await reopened.waitFor(`window.lingdocDiagnostics?.().lastSave?.version?.id==='${frozen.versionId}' && window.lingdocDiagnostics?.().lastSave?.delivery?.snapshot?.id==='${frozen.snapshotId}' && window.lingdocDiagnostics?.().lastSave?.delivery?.chapters?.filter(chapter=>chapter.confirmed).length===6 && window.lingdocDiagnostics?.().lastSave?.delivery?.deliveryEligible===true`);
    const recovered = await reopened.evaluate(`({versionId:window.lingdocDiagnostics().lastSave.version.id,snapshotId:window.lingdocDiagnostics().lastSave.delivery.snapshot.id,goDiskVersion:window.lingdocDiagnostics().goDiskVersion,dirty:window.lingdocDiagnostics().lastSave.live.dirty})`);
    assert(recovered.goDiskVersion > 0 && !recovered.dirty, '整程序重开未恢复 Go 版本或文档状态');
    const oldExportResponse = await fetch(`${runtime.goOrigin}/api/docx/projects/project-1/exports/${encodeURIComponent(exported.id)}`);
    assert(oldExportResponse.ok, '重开后旧导出不可读取');
    const oldExportBytes = Buffer.from(await oldExportResponse.arrayBuffer());
    assert(sha256(oldExportBytes) === frozen.sha256, '重开后旧导出字节改变');
    const pointer = JSON.parse(await readFile(path.join(runtimeDir, 'ds', 'state.json'), 'utf8'));
    const savedBytes = await readFile(path.join(runtimeDir, 'ds', pointer.file));
    assert(pointer.sha256 === frozen.sha256 && sha256(savedBytes) === frozen.sha256, '重开后 DS 当前稿不是冻结保存版本');
    await reopened.screenshot('real-full-restart-recovered.png');
    results.restart = { ...recovered, confirmed: 6, oldExportSha256: sha256(oldExportBytes), dsSha256: sha256(savedBytes), browserAndServicesActuallyRestarted: true };
    assert(results.pageErrors.length === 0, '真实验收存在浏览器脚本错误');
    results.passed = true;
  } catch (error) {
    results.error = String(error); results.passed = false; process.exitCode = 1;
  } finally {
    if (browserSession) results.lastBrowserExited = await stopBrowser(browserSession);
    if (runtime) await runtime.stop();
    results.allOwnedProcessesStopped = true;
    await mkdir(artifacts, { recursive: true });
    await writeFile(path.join(artifacts, 'verification.json'), JSON.stringify(results, null, 2));
    console.log(JSON.stringify({ artifacts, passed: results.passed, save: results.save, delivery: results.delivery, restart: results.restart, error: results.error }, null, 2));
  }
}
