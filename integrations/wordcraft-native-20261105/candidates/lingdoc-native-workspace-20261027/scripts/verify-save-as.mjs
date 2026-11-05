// Focused Save As regression: real WASM/Go/SQLite/DS/Edge, isolated user data.
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createServer } from 'node:net';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { startDeliveryRuntime } from './delivery-runtime.mjs';
import { root, edge } from './local-environment.mjs';

const runId = `${new Date().toISOString().replaceAll(':', '-')}-${process.pid}`;
const artifacts = path.join(root, 'artifacts', 'save-as', runId);
const runtimeDir = path.join(root, 'runtime', 'save-as', runId);
const result = { runId, kind: 'real Go/SQLite/DS/WASM/Edge', passed: false, pageErrors: [] };
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
let runtime, browser, socket, send;

async function freePort() {
  const server = createServer();
  await new Promise((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return port;
}

try {
  await mkdir(artifacts, { recursive: true });
  runtime = await startDeliveryRuntime({ runtimeDir });
  result.runtime = { origin: runtime.origin, goOrigin: runtime.goOrigin, database: runtime.database };
  const debugPort = await freePort();
  browser = spawn(edge, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--disable-background-timer-throttling', '--disable-backgrounding-occluded-windows', '--disable-renderer-backgrounding',
    `--remote-debugging-port=${debugPort}`, `--user-data-dir=${path.join(runtimeDir, 'edge')}`, 'about:blank'],
  { windowsHide: true, stdio: ['ignore', 'ignore', 'pipe'] });
  let launchError;
  browser.once('error', error => { launchError = error; });
  browser.stderr.resume();
  let page;
  for (let i = 0; i < 100 && !page; i++) {
    if (launchError) throw launchError;
    try {
      const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`, { signal: AbortSignal.timeout(500) }).then(r => r.json());
      page = pages.find(item => item.type === 'page');
    } catch {}
    if (!page) await sleep(150);
  }
  assert(page, 'Edge did not expose a page');
  socket = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let sequence = 0;
  const pending = new Map();
  socket.onmessage = event => {
    const message = JSON.parse(event.data);
    if (message.method === 'Runtime.exceptionThrown') result.pageErrors.push(message.params.exceptionDetails);
    const request = pending.get(message.id);
    if (!request) return;
    pending.delete(message.id); clearTimeout(request.timer);
    if (message.error) request.reject(new Error(message.error.message)); else request.resolve(message.result);
  };
  send = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`${method} timed out`)); }, 30000);
    pending.set(id, { resolve, reject, timer });
    socket.send(JSON.stringify({ id, method, params }));
  });
  const evaluate = async expression => {
    const reply = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (reply.exceptionDetails) throw new Error(reply.exceptionDetails.exception?.description || reply.exceptionDetails.text);
    return reply.result.value;
  };
  const waitFor = async expression => {
    for (let i = 0; i < 300; i++) { if (await evaluate(expression)) return; await sleep(100); }
    throw new Error(`Timed out: ${expression}; ${await evaluate(`document.querySelector('#host-log')?.textContent`)}`);
  };
  const clickAt = async (x, y) => {
    await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, x, y });
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, x, y });
  };
  const click = async selector => {
    const point = await evaluate(`(()=>{const b=document.querySelector(${JSON.stringify(selector)});if(!b||b.disabled)throw Error('button unavailable');const r=b.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`);
    await clickAt(point.x, point.y);
  };
  const key = async (key, code, modifiers = 0) => {
    await send('Input.dispatchKeyEvent', { type: 'keyDown', key, code, modifiers });
    await send('Input.dispatchKeyEvent', { type: 'keyUp', key, code, modifiers: 0 });
  };
  const screenshot = async name => {
    const shot = await send('Page.captureScreenshot', { format: 'png' });
    await writeFile(path.join(artifacts, name), Buffer.from(shot.data, 'base64'));
  };
  const state = () => evaluate(`(async()=>({engine:await window.wordcraftHost.engine({op:'status'}),diskVersion:window.wordcraftHost.diskVersion,goDiskVersion:window.wordcraftHost.goDiskVersion,calls:[...window.__saveAsCalls]}))()`);
  await send('Page.enable'); await send('Runtime.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: artifacts });
  await send('Page.navigate', { url: runtime.origin });
  await waitFor(`(()=>{try{return !!JSON.parse(document.querySelector('#host-log').textContent).opened}catch{return false}})()`);
  await evaluate(`(()=>{
    // Headless Edge cannot operate an OS picker. Exercise the supported download UI.
    Object.defineProperty(window,'showSaveFilePicker',{configurable:true,value:undefined});
    window.__saveAsCalls=[];
    const record=value=>window.__saveAsCalls.push(value);
    const originalFetch=window.fetch;
    window.fetch=(...args)=>{const url=String(args[0]);if(url==='/api/save')record('ds');else if(url.endsWith('/versions'))record('go');else if(url.endsWith('/delivery'))record('readback');return originalFetch(...args);};
    const bindings=window.wasmBindings,original=bindings.host_request;
    window.wasmBindings=new Proxy(bindings,{get(target,property){if(property!=='host_request')return Reflect.get(target,property);return text=>{const request=JSON.parse(text);if(['export','ack'].includes(request.op))record(request.op);return original(text);};}});
    const nativeIntent=window.wordcraftHost.saveAsIntent;
    window.wordcraftHost.saveAsIntent=name=>{window.__nativeSaveAsName=name;return nativeIntent(name);};
  })()`);
  await evaluate(`(async()=>{await window.wordcraftHost.selection();await window.wordcraftHost.insert('SAVE-AS-REAL-DOCX-20261031')})()`);
  result.before = await state();
  assert.equal(result.before.engine.dirty, true);
  // File > Save As > Browse enters the real Rust command, not a JS-only test hook.
  await clickAt(23, 52); await sleep(250);
  await clickAt(90, 379); await sleep(250);
  await screenshot('backstage.png');
  await clickAt(270, 95);
  await waitFor(`!!document.querySelector('#lingdoc-save-as-dialog')`);
  assert(await evaluate(`!!window.__nativeSaveAsName`), 'native Save As did not call the host');
  await screenshot('name-dialog.png');
  await click('#save-as-cancel');
  await waitFor(`!window.lingdocDiagnostics().saveAsPending`);
  result.cancelled = await state();
  assert.deepEqual(result.cancelled, result.before, 'Cancel changed revision, dirty, disk/Go version or save calls');

  // Cancelling leaves the Save As page open, ready for another deliberate attempt.
  await clickAt(270, 95);
  await waitFor(`!!document.querySelector('#lingdoc-save-as-dialog')`);
  result.duplicatePromiseReused = await evaluate(`(()=>{const h=window.wordcraftHost;return h.saveAsIntent('duplicate.docx')===h.saveAsIntent('duplicate.docx')})()`);
  assert.equal(result.duplicatePromiseReused, true);
  await evaluate(`document.querySelector('#save-as-name').value='申请书另存测试.docx'`);
  await click('#save-as-download');
  await waitFor(`!!window.lingdocDiagnostics().lastSaveAs&&!window.lingdocDiagnostics().saveAsPending`);
  result.saved = await evaluate(`({copy:window.lingdocDiagnostics().lastSaveAs,saved:window.lingdocDiagnostics().lastSave})`);
  result.after = await state();
  assert.equal(result.after.engine.dirty, false);
  assert.equal(result.after.diskVersion, result.before.diskVersion + 1);
  assert.equal(result.after.goDiskVersion, result.before.goDiskVersion + 1);
  for (const stage of ['export', 'ds', 'go', 'readback', 'ack']) assert.equal(result.after.calls.filter(x=>x===stage).length, 1, `duplicate ${stage}`);
  assert.deepEqual(result.after.calls, ['export', 'ds', 'go', 'readback', 'ack']);
  let bytes;
  for (let i = 0; i < 100 && !bytes; i++) {
    try { bytes = await readFile(path.join(artifacts, result.saved.copy.name)); } catch { await sleep(100); }
  }
  assert(bytes, 'Actual browser DOCX download missing');
  result.download = { name: result.saved.copy.name, bytes: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex') };
  assert.equal(result.download.sha256, result.saved.saved.version.docSha256);
  assert.equal(result.download.sha256, result.saved.saved.receipt.sha256);
  await screenshot('saved-copy.png');

  // The shared Rust completion refactor must retain normal Canvas save behavior.
  await key('Escape', 'Escape');
  await evaluate(`(async()=>{await window.wordcraftHost.selection();await window.wordcraftHost.insert(' NORMAL-SAVE-AFTER-COPY')})()`);
  await clickAt(108, 18);
  await waitFor(`window.wordcraftHost.goDiskVersion===${result.after.goDiskVersion + 1}&&!window.lingdocDiagnostics().transactionActive`);
  result.normalSave = await state();
  assert.equal(result.normalSave.engine.dirty, false);
  assert(await evaluate(`window.lingdocDiagnostics().saveSources.includes('native-canvas')`));
  // Exercise native picker cancellation without claiming that headless Edge shows an OS dialog.
  await evaluate(`void Object.defineProperty(window,'showSaveFilePicker',{configurable:true,value:async()=>{throw new DOMException('cancelled','AbortError')}})`);
  const beforePickerCancel = await state();
  const cancelled = await evaluate(`window.wordcraftHost.saveAsIntent('cancelled.docx')`);
  assert.equal(cancelled.cancelled, true);
  assert.deepEqual(await state(), beforePickerCancel);
  result.nativePickerAbort = 'simulated AbortError leaves all versions and save calls unchanged';
  assert.deepEqual(result.pageErrors, []);
  result.passed = true;
} catch (error) {
  result.error = error.stack;
  process.exitCode = 1;
  try {
    const shot = await send?.('Page.captureScreenshot', { format: 'png' });
    if (shot) await writeFile(path.join(artifacts, 'failure.png'), Buffer.from(shot.data, 'base64'));
  } catch {}
} finally {
  if (browser?.pid && browser.exitCode === null) {
    try { await send?.('Browser.close'); } catch {}
    for (let i = 0; i < 20 && browser.exitCode === null; i++) await sleep(100);
    if (browser.exitCode === null) spawnSync('taskkill', ['/PID', String(browser.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' });
    for (let i = 0; i < 30 && browser.exitCode === null; i++) await sleep(100);
  }
  socket?.close();
  result.browserStopped = !browser?.pid || browser.exitCode !== null;
  try { if (runtime) await runtime.stop(); result.servicesStopped = true; }
  catch (error) { result.cleanupError = error.message; result.servicesStopped = false; }
  if (!result.browserStopped || !result.servicesStopped) { result.passed = false; process.exitCode = 1; }
  await mkdir(artifacts, { recursive: true });
  await writeFile(path.join(artifacts, 'verification.json'), JSON.stringify(result, null, 2));
  console.log(JSON.stringify({ artifacts, passed: result.passed, download: result.download, error: result.error,
    browserStopped: result.browserStopped, servicesStopped: result.servicesStopped }, null, 2));
}
